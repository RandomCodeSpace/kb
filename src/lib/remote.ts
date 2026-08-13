import type { Board, Status, Task } from './model';
import { newId } from './model';
import { parse, serialize, titleLine, wireTasks } from './markdown';
import { ReauthRequiredError, type Identity } from './auth';
import { authedFetch } from './api';
import type {
  DurableSnapshot,
  DurableVersion,
  PendingBoardWrite,
  PersistenceResult,
} from './store';

const HEALTH_TIMEOUT_MS = 1500;
const SAVE_DEBOUNCE_MS = 800;

export interface SaveAcknowledgement {
  pushed: Board;
  taskIDs: ReadonlyMap<string, string>;
  conflicts?: readonly string[];
  operationID?: string;
  isCurrent?: () => boolean;
  generation?: number;
  durableVersion?: DurableVersion;
  durableSnapshot?: DurableSnapshot;
}

type SaveCallback = (acknowledgement: SaveAcknowledgement) => unknown;

export interface PendingWriteStager {
  stagePendingBoardWrite(pending: {
    operation_id: string;
    body: string;
    sent_board: Board;
    sent_canonical_ids: Record<string, string>;
    if_match: string | null;
  }, expectedVersion: DurableVersion, isLiveCurrent: () => boolean): Promise<{
    ok: boolean;
    generation?: number;
    snapshot?: DurableSnapshot;
    error?: unknown;
    conflict?: true;
  }>;
}

type PendingSave = {
  identity: Identity;
  board: Board;
  canonicalTaskIDs?: ReadonlyMap<string, string>;
  onError?: (err: unknown) => void;
  onSuccess?: SaveCallback;
  keepalive: boolean;
  epoch: number;
  legacy: boolean;
  conflicts: readonly string[];
  operationID?: string;
  generation?: number;
  durableVersion?: DurableVersion;
  durableSnapshot?: DurableSnapshot;
  isLiveCurrent?: () => boolean;
  pendingWriteStager?: PendingWriteStager;
  onObsolete?: () => void;
};

type PushAttempt = {
  pushed: Board;
  pushedIDs: ReadonlyMap<string, string>;
  conflicts: readonly string[];
  response: Response;
};

type PreparedBoardRequest = {
  headers: Record<string, string>;
  body: string;
  durableCreate: boolean;
};

class AmbiguousWriteError extends Error {
  constructor(cause: unknown) {
    super(cause instanceof Error ? cause.message : 'board write outcome is ambiguous', { cause });
  }
}

interface RemoteSnapshot {
  board: Board | null;
  taskIDs: ReadonlyMap<string, string>;
}

export interface MergeResult {
  board: Board;
  canonicalTaskIDs: ReadonlyMap<string, string>;
  conflicts: readonly string[];
}

function taskKey(t: Task): string {
  const checks = t.checks.map((c) => `${c.done ? 'x' : ' '}${c.text}`).join('\n');
  return [titleLine(t), t.desc, checks].join('\u0000');
}

function placedKey(t: Task): string {
  return `${t.status} ${taskKey(t)}`;
}

function invalidTaskIDs(): Error {
  return new Error('PUT /api/board returned invalid task ids');
}

function invalidBoardSnapshot(): Error {
  return new Error('GET /api/board returned invalid board snapshot');
}

function taskIDMapFromBody(
  body: unknown,
  pushed: Board,
  invalid: () => Error,
): ReadonlyMap<string, string> {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) throw invalid();
  const ids = (body as Record<string, unknown>).task_ids;
  const tasks = wireTasks(pushed);
  if (!Array.isArray(ids) || ids.length !== tasks.length) throw invalid();

  const mapped = new Map<string, string>();
  const seenCanonical = new Set<string>();
  for (let i = 0; i < tasks.length; i++) {
    const canonicalID = ids[i];
    if (
      typeof canonicalID !== 'string' ||
      canonicalID.trim() === '' ||
      seenCanonical.has(canonicalID)
    ) throw invalid();
    seenCanonical.add(canonicalID);
    mapped.set(tasks[i]!.id, canonicalID);
  }
  return mapped;
}

function prepareBoardRequest(
  save: PendingSave,
  board: Board,
  ids: ReadonlyMap<string, string>,
): PreparedBoardRequest {
  if (save.legacy || save.canonicalTaskIDs === undefined) {
    return {
      headers: { Accept: 'application/json', 'Content-Type': 'text/markdown' },
      body: serialize(board),
      durableCreate: false,
    };
  }
  const tasks = wireTasks(board);
  return {
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({
      board: serialize(board),
      task_ids: tasks.map((task) => ids.get(task.id) ?? null),
    }),
    durableCreate: tasks.some((task) => !ids.has(task.id)),
  };
}

function durableVersionForSave(save: PendingSave): DurableVersion | undefined {
  if (save.durableVersion) return save.durableVersion;
  const generation = save.generation;
  if (
    typeof generation === 'number' &&
    Number.isSafeInteger(generation) &&
    generation >= 0
  ) {
    return { present: true, generation };
  }
  return undefined;
}

function mergeConflictSnapshot(
  save: PendingSave,
  server: RemoteSnapshot,
  mergeBase: Board | null,
  mergeBaseIDs: ReadonlyMap<string, string>,
): Pick<PushAttempt, 'pushed' | 'pushedIDs' | 'conflicts'> {
  let pushed = save.board;
  let pushedIDs = save.canonicalTaskIDs ?? new Map<string, string>();
  let conflicts = save.conflicts;
  if (!server.board) return { pushed, pushedIDs, conflicts };

  let titleAlreadyMerged = false;
  if (
    mergeBase &&
    mergeBaseIDs.size > 0 &&
    save.canonicalTaskIDs !== undefined &&
    server.taskIDs.size > 0
  ) {
    const merged = mergeCanonicalBoards(
      save.board,
      save.canonicalTaskIDs,
      server.board,
      server.taskIDs,
      mergeBase,
      mergeBaseIDs,
    );
    pushed = merged.board;
    pushedIDs = merged.canonicalTaskIDs;
    conflicts = [...conflicts, ...merged.conflicts];
    titleAlreadyMerged = true;
  } else if (save.canonicalTaskIDs !== undefined && server.taskIDs.size > 0) {
    const merged = mergeWithoutCanonicalBase(
      save.board, save.canonicalTaskIDs, server.board, server.taskIDs,
    );
    pushed = merged.board;
    pushedIDs = merged.taskIDs;
  } else {
    pushed = legacyMerge(save.board, server.board, mergeBase);
  }

  if (titleAlreadyMerged) return { pushed, pushedIDs, conflicts };
  if (mergeBase) {
    const titleConflicts: string[] = [];
    pushed = {
      ...pushed,
      title: chooseField(
        'board title', mergeBase.title, save.board.title, server.board.title, titleConflicts,
      ),
    };
    conflicts = [...conflicts, ...titleConflicts];
  } else if (save.board.title !== server.board.title) {
    conflicts = [...conflicts, 'board title'];
  }
  return { pushed, pushedIDs, conflicts };
}

async function taskIDMap(
  res: Response,
  pushed: Board,
  ambiguousOnInvalidSuccess = false,
): Promise<ReadonlyMap<string, string>> {
  if (res.status === 204) return new Map();
  if (res.status !== 200) throw new Error(`PUT /api/board failed: ${res.status}`);
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    const error = invalidTaskIDs();
    throw ambiguousOnInvalidSuccess ? new AmbiguousWriteError(error) : error;
  }
  try {
    return taskIDMapFromBody(body, pushed, invalidTaskIDs);
  } catch (error) {
    throw ambiguousOnInvalidSuccess ? new AmbiguousWriteError(error) : error;
  }
}

export function mergeTaskIDMaps(
  current: ReadonlyMap<string, string>,
  acknowledged: ReadonlyMap<string, string>,
): ReadonlyMap<string, string> {
  if (acknowledged.size === 0) return current;
  const merged = new Map(current);
  for (const [clientID, canonicalID] of acknowledged) {
    for (const [existingClient, existingCanonical] of merged) {
      if (existingClient !== clientID && existingCanonical === canonicalID) {
        merged.delete(existingClient);
      }
    }
    merged.set(clientID, canonicalID);
  }
  return merged;
}

export function recomputeDeletedCanonicalIDs(
  latestIDs: ReadonlyMap<string, string>,
  latestDeleted: ReadonlySet<string>,
  nextIDs: ReadonlyMap<string, string>,
): ReadonlySet<string> {
  const deleted = new Set(latestDeleted);
  const live = new Set(nextIDs.values());
  for (const canonical of latestIDs.values()) {
    if (!live.has(canonical)) deleted.add(canonical);
  }
  for (const canonical of live) deleted.delete(canonical);
  return deleted;
}

export function sameBoardSemantics(
  current: {
    board: Board;
    canonicalTaskIDs: ReadonlyMap<string, string>;
    deletedCanonicalIDs: ReadonlySet<string>;
    migratedRaw: boolean;
    pendingBoardWrite: PendingBoardWrite | null;
  },
  target: {
    board: Board;
    canonicalTaskIDs: ReadonlyMap<string, string>;
    deletedCanonicalIDs: ReadonlySet<string>;
    migratedRaw: boolean;
    pendingBoardWrite: PendingBoardWrite | null;
  },
): boolean {
  const canonicalSequence = (
    board: Board,
    ids: ReadonlyMap<string, string>,
  ) => wireTasks(board).map((task) => ids.get(task.id) ?? null);
  return (
    serialize(current.board) === serialize(target.board) &&
    same(
      canonicalSequence(current.board, current.canonicalTaskIDs),
      canonicalSequence(target.board, target.canonicalTaskIDs),
    ) &&
    current.deletedCanonicalIDs.size === target.deletedCanonicalIDs.size &&
    [...current.deletedCanonicalIDs].every((id) => target.deletedCanonicalIDs.has(id)) &&
    current.migratedRaw === target.migratedRaw &&
    same(current.pendingBoardWrite, target.pendingBoardWrite)
  );
}

/**
 * Build the one durable board for a successful acknowledgement. A newer local
 * edit is three-way merged over the board that was dispatched, while every
 * task committed by conflict recovery is retained by canonical identity.
 */
export function mergeAcknowledgedState(
  current: Board,
  currentIDs: ReadonlyMap<string, string>,
  sent: Board,
  sentIDs: ReadonlyMap<string, string>,
  pushed: Board,
  committedIDs: ReadonlyMap<string, string>,
): MergeResult {
  if (current === sent) {
    return { board: pushed, canonicalTaskIDs: committedIDs, conflicts: [] };
  }
  return mergeCanonicalBoards(
    current,
    mergeTaskIDMaps(currentIDs, committedIDs),
    pushed,
    committedIDs,
    sent,
    mergeTaskIDMaps(sentIDs, committedIDs),
  );
}

const MERGE_FIELDS = [
  'emoji', 'title', 'desc', 'blocked', 'prio', 'due', 'effort', 'tags', 'checks',
] as const satisfies readonly (keyof Task)[];

function same(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function chooseField<T>(
  name: string,
  base: T,
  local: T,
  remote: T,
  conflicts: string[],
): T {
  const localChanged = !same(local, base);
  const remoteChanged = !same(remote, base);
  if (!localChanged) return remote;
  if (!remoteChanged || same(local, remote)) return local;
  conflicts.push(name);
  return local;
}

function legacyTaskCounts(local: Board, base: Board | null): Map<string, number> {
  const counts = new Map<string, number>();
  for (const task of [...(base?.tasks ?? []), ...local.tasks]) {
    const key = taskKey(task);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return counts;
}

function legacyPristineTasks(local: Board, base: Board | null): Map<string, Task[]> {
  const pristine = new Map<string, Task[]>();
  if (!base) return pristine;
  const placed = new Set(base.tasks.map(placedKey));
  for (const task of local.tasks) {
    if (!placed.has(placedKey(task))) continue;
    const key = taskKey(task);
    const bucket = pristine.get(key);
    if (bucket) bucket.push(task);
    else pristine.set(key, [task]);
  }
  return pristine;
}

function indexLegacyServerTasks(
  server: Board,
  known: Map<string, number>,
  pristine: Map<string, Task[]>,
): { extra: Task[]; moved: ReadonlyMap<Task, Task> } {
  const extra: Task[] = [];
  const moved = new Map<Task, Task>();
  for (const task of server.tasks) {
    const key = taskKey(task);
    const count = known.get(key) ?? 0;
    if (count === 0) {
      extra.push(task);
      continue;
    }
    known.set(key, count - 1);
    const ours = pristine.get(key)?.shift();
    if (ours && ours.status !== task.status) moved.set(ours, task);
  }
  return { extra, moved };
}

function legacyMerge(local: Board, server: Board, base: Board | null): Board {
  const known = legacyTaskCounts(local, base);
  const pristine = legacyPristineTasks(local, base);
  const { extra, moved } = indexLegacyServerTasks(server, known, pristine);
  if (extra.length === 0 && moved.size === 0) return local;
  return {
    ...local,
    tasks: [
      ...local.tasks.map((t) => {
        const remote = moved.get(t);
        return remote ? { ...t, status: remote.status, movedAt: remote.movedAt } : t;
      }),
      ...extra,
    ],
  };
}

/**
 * Identity-safe conflict recovery when no canonical merge base exists.
 * Content equality proves nothing here: local null-ID tasks and fetched
 * canonical server tasks remain distinct, including duplicate titles.
 */
function mergeWithoutCanonicalBase(
  local: Board,
  localIDs: ReadonlyMap<string, string>,
  server: Board,
  serverIDs: ReadonlyMap<string, string>,
): { board: Board; taskIDs: ReadonlyMap<string, string> } {
  const knownCanonical = new Set(localIDs.values());
  const tasks = [...local.tasks];
  const taskIDs = new Map(localIDs);
  for (const task of server.tasks) {
    const canonical = serverIDs.get(task.id);
    if (canonical && knownCanonical.has(canonical)) continue;
    tasks.push(task);
    if (canonical) {
      knownCanonical.add(canonical);
      taskIDs.set(task.id, canonical);
    }
  }
  return { board: { ...local, tasks }, taskIDs };
}

function byCanonical(
  board: Board,
  ids: ReadonlyMap<string, string>,
): Map<string, Task> {
  const result = new Map<string, Task>();
  for (const task of board.tasks) {
    const canonical = ids.get(task.id);
    if (canonical) result.set(canonical, task);
  }
  return result;
}

function projectedSequence(
  board: Board,
  ids: ReadonlyMap<string, string>,
  status: Status,
  allowed: ReadonlySet<string>,
  mergedByID: ReadonlyMap<string, Task>,
): string[] {
  return board.tasks
    .map((task) => ids.get(task.id))
    .filter(
      (id): id is string =>
        Boolean(id && allowed.has(id) && mergedByID.get(id)?.status === status),
    );
}

function newTaskBuckets(
  source: Board,
  sourceIDs: ReadonlyMap<string, string>,
  baseIDs: ReadonlySet<string>,
  survivorIDs: ReadonlySet<string>,
  mergedByID: ReadonlyMap<string, Task>,
  status: Status,
  already: Set<string>,
): Map<string | null, Task[]> {
  const buckets = new Map<string | null, Task[]>();
  let anchor: string | null = null;
  for (const task of source.tasks.filter((candidate) => candidate.status === status)) {
    const canonical = sourceIDs.get(task.id);
    if (
      canonical &&
      survivorIDs.has(canonical) &&
      mergedByID.get(canonical)?.status === status
    ) {
      anchor = canonical;
      continue;
    }
    const newKey = canonical ? `canonical:${canonical}` : `client:${task.id}`;
    if ((canonical && baseIDs.has(canonical)) || already.has(newKey)) continue;
    already.add(newKey);
    const bucket = buckets.get(anchor);
    if (bucket) {
      bucket.push(task);
    } else {
      buckets.set(anchor, [task]);
    }
  }
  return buckets;
}

function placement(
  canonical: string,
  base: Task,
  local: Task,
  remote: Task,
  conflicts: string[],
): { status: Status; movedAt: string } {
  // parse() necessarily mints timestamps, so only a status change proves that
  // the status+movedAt group changed on the wire.
  const localChanged = local.status !== base.status;
  const remoteChanged = remote.status !== base.status;
  if (!localChanged && !remoteChanged) {
    return { status: local.status, movedAt: local.movedAt };
  }
  if (!localChanged) return { status: remote.status, movedAt: remote.movedAt };
  if (!remoteChanged || local.status === remote.status) {
    return { status: local.status, movedAt: local.movedAt };
  }
  conflicts.push(`${local.title || canonical}: status/order`);
  return { status: local.status, movedAt: local.movedAt };
}

function mergeCanonicalSurvivors(
  baseByID: ReadonlyMap<string, Task>,
  localByID: ReadonlyMap<string, Task>,
  serverByID: ReadonlyMap<string, Task>,
  conflicts: string[],
): Map<string, Task> {
  const mergedByID = new Map<string, Task>();
  for (const [canonical, baseTask] of baseByID) {
    const localTask = localByID.get(canonical);
    const remoteTask = serverByID.get(canonical);
    if (!localTask || !remoteTask) continue;
    const merged: Task = { ...localTask };
    for (const field of MERGE_FIELDS) {
      (merged as Record<keyof Task, unknown>)[field] = chooseField(
        `${localTask.title || canonical}: ${field}`,
        baseTask[field],
        localTask[field],
        remoteTask[field],
        conflicts,
      );
    }
    const mergedPlacement = placement(
      canonical, baseTask, localTask, remoteTask, conflicts,
    );
    merged.status = mergedPlacement.status;
    merged.movedAt = mergedPlacement.movedAt;
    mergedByID.set(canonical, merged);
  }
  return mergedByID;
}

function chosenCanonicalSequence(
  status: Status,
  baseSeq: readonly string[],
  localSeq: readonly string[],
  remoteSeq: readonly string[],
  conflicts: string[],
): readonly string[] {
  if (same(localSeq, baseSeq)) return remoteSeq;
  if (!same(remoteSeq, baseSeq) && !same(localSeq, remoteSeq)) {
    conflicts.push(`${status} column order`);
  }
  return localSeq;
}

function appendCanonicalStatusTasks(
  status: Status,
  context: {
    local: Board;
    localIDs: ReadonlyMap<string, string>;
    server: Board;
    serverIDs: ReadonlyMap<string, string>;
    base: Board;
    baseIDs: ReadonlyMap<string, string>;
    baseCanonical: ReadonlySet<string>;
    survivorIDs: ReadonlySet<string>;
    mergedByID: ReadonlyMap<string, Task>;
    seenNew: Set<string>;
    conflicts: string[];
  },
): Task[] {
  const { local, localIDs, server, serverIDs, base, baseIDs } = context;
  const baseSeq = projectedSequence(
    base, baseIDs, status, context.survivorIDs, context.mergedByID,
  );
  const localSeq = projectedSequence(
    local, localIDs, status, context.survivorIDs, context.mergedByID,
  );
  const remoteSeq = projectedSequence(
    server, serverIDs, status, context.survivorIDs, context.mergedByID,
  );
  const chosen = chosenCanonicalSequence(
    status, baseSeq, localSeq, remoteSeq, context.conflicts,
  );
  const localNew = newTaskBuckets(
    local, localIDs, context.baseCanonical, context.survivorIDs,
    context.mergedByID, status, context.seenNew,
  );
  const remoteNew = newTaskBuckets(
    server, serverIDs, context.baseCanonical, context.survivorIDs,
    context.mergedByID, status, context.seenNew,
  );
  const ordered: Task[] = [
    ...(localNew.get(null) ?? []),
    ...(remoteNew.get(null) ?? []),
  ];
  for (const canonical of chosen) {
    const task = context.mergedByID.get(canonical);
    if (task?.status !== status) continue;
    ordered.push(
      task,
      ...(localNew.get(canonical) ?? []),
      ...(remoteNew.get(canonical) ?? []),
    );
  }
  return ordered;
}

/** Canonical-ID three-way merge used by first-party JSON synchronization. */
export function mergeCanonicalBoards(
  local: Board,
  localIDs: ReadonlyMap<string, string>,
  server: Board,
  serverIDs: ReadonlyMap<string, string>,
  base: Board,
  baseIDs: ReadonlyMap<string, string>,
): MergeResult {
  const conflicts: string[] = [];
  const baseByID = byCanonical(base, baseIDs);
  const localByID = byCanonical(local, localIDs);
  const serverByID = byCanonical(server, serverIDs);
  const mergedByID = mergeCanonicalSurvivors(
    baseByID, localByID, serverByID, conflicts,
  );

  const title = chooseField('board title', base.title, local.title, server.title, conflicts);
  const survivorIDs = new Set(mergedByID.keys());
  const baseCanonical = new Set(baseByID.keys());
  const tasks: Task[] = [];
  const outputIDs = new Map<string, string>();
  const seenNew = new Set<string>();
  const statuses: readonly Status[] = ['todo', 'doing', 'done', 'cancelled'];

  for (const status of statuses) {
    const ordered = appendCanonicalStatusTasks(status, {
      local, localIDs, server, serverIDs, base, baseIDs, baseCanonical,
      survivorIDs, mergedByID, seenNew, conflicts,
    });
    for (const task of ordered) {
      tasks.push(task);
      const canonical = localIDs.get(task.id) ?? serverIDs.get(task.id);
      if (canonical) outputIDs.set(task.id, canonical);
    }
  }

  return { board: { title, tasks }, canonicalTaskIDs: outputIDs, conflicts };
}

/** Backward-compatible content merge for legacy callers and tests. */
export function mergeBoards(local: Board, server: Board, base: Board | null): Board {
  return legacyMerge(local, server, base);
}

/** Merge a clean-startup edit committed while the first GET was in flight. */
export function mergeStartupEdit(
  beforeFetch: Board,
  beforeFetchIDs: ReadonlyMap<string, string>,
  current: Board,
  currentIDs: ReadonlyMap<string, string>,
  server: Board,
  serverIDs: ReadonlyMap<string, string>,
): MergeResult {
  if (beforeFetchIDs.size > 0 && serverIDs.size > 0) {
    return mergeCanonicalBoards(
      current,
      currentIDs,
      server,
      serverIDs,
      beforeFetch,
      beforeFetchIDs,
    );
  }
  const merged = mergeWithoutCanonicalBase(current, currentIDs, server, serverIDs);
  const conflicts: string[] = [];
  return {
    board: {
      ...merged.board,
      title: chooseField('board title', beforeFetch.title, current.title, server.title, conflicts),
    },
    canonicalTaskIDs: merged.taskIDs,
    conflicts,
  };
}

/** Three-way merge snapshots from this client, using stable client IDs until
 * the server has assigned a canonical identity. */
export function mergeDurableEdit(
  base: Board,
  baseIDs: ReadonlyMap<string, string>,
  candidate: Board,
  candidateIDs: ReadonlyMap<string, string>,
  fresh: Board,
  freshIDs: ReadonlyMap<string, string>,
): MergeResult {
  const canonicalByClient = new Map<string, string>([
    ...baseIDs,
    ...candidateIDs,
    ...freshIDs,
  ]);
  const syntheticPrefix = 'client:';
  const identities = (
    board: Board,
    ids: ReadonlyMap<string, string>,
  ): ReadonlyMap<string, string> => new Map(board.tasks.map((task) => [
    task.id,
    ids.get(task.id) ?? canonicalByClient.get(task.id) ?? `${syntheticPrefix}${task.id}`,
  ]));
  const merged = mergeCanonicalBoards(
    candidate,
    identities(candidate, candidateIDs),
    fresh,
    identities(fresh, freshIDs),
    base,
    identities(base, baseIDs),
  );
  return {
    ...merged,
    canonicalTaskIDs: new Map(
      [...merged.canonicalTaskIDs].filter(([, id]) => !id.startsWith(syntheticPrefix)),
    ),
  };
}

export interface LiveBoardSnapshot {
  epoch: number;
  board: Board;
  canonicalTaskIDs: ReadonlyMap<string, string>;
  deletedCanonicalIDs: ReadonlySet<string>;
  durableBase: DurableSnapshot;
  needsLocalPersistence: boolean;
  remoteClean: boolean;
}

export type LiveCandidate = {
  board: Board;
  canonicalTaskIDs: ReadonlyMap<string, string>;
  deletedCanonicalIDs: ReadonlySet<string>;
};

export type LiveCommitOutcome = {
  candidate: LiveCandidate;
  conflicts: readonly string[];
  persisted: boolean;
  snapshot?: DurableSnapshot;
  recoveryPending: boolean;
  writes: number;
  failure?: PersistenceResult;
};

export async function commitLiveCandidate(options: {
  sourceLive: LiveBoardSnapshot;
  candidate: LiveCandidate;
  readLive: () => LiveBoardSnapshot;
  readDurable: () => DurableSnapshot;
  persist: (
    candidate: LiveCandidate,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean,
  ) => Promise<PersistenceResult>;
  repairPersist: (
    candidate: LiveCandidate,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean,
  ) => Promise<PersistenceResult>;
  canRetry?: (snapshot: DurableSnapshot) => boolean;
  cancelled: () => boolean;
}): Promise<LiveCommitOutcome> {
  let sourceLive = options.sourceLive;
  let candidate = options.candidate;
  const conflicts: string[] = [];
  let writes = 0;
  const attempt = () => {
    writes += 1;
    return options.persist(
      candidate,
      sourceLive.durableBase.version,
      () => !options.cancelled() && options.readLive().epoch === sourceLive.epoch,
    );
  };
  let result = await attempt();
  if (options.cancelled()) {
    return { candidate, conflicts, persisted: false, recoveryPending: false, writes };
  }
  if (!result.ok && result.conflict) {
    const freshLive = options.readLive();
    const freshDurable = options.readDurable();
    if (options.cancelled() || (options.canRetry && !options.canRetry(freshDurable))) {
      return { candidate, conflicts, persisted: false, recoveryPending: false, writes };
    }
    const durableRebase = mergeDurableEdit(
      sourceLive.durableBase.board,
      sourceLive.durableBase.canonicalTaskIDs,
      candidate.board,
      candidate.canonicalTaskIDs,
      freshDurable.board,
      freshDurable.canonicalTaskIDs,
    );
    conflicts.push(...durableRebase.conflicts);
    let rebased = durableRebase;
    if (freshLive.epoch !== sourceLive.epoch) {
      rebased = mergeDurableEdit(
        sourceLive.board,
        sourceLive.canonicalTaskIDs,
        freshLive.board,
        freshLive.canonicalTaskIDs,
        durableRebase.board,
        durableRebase.canonicalTaskIDs,
      );
      conflicts.push(...rebased.conflicts);
    }
    candidate = {
      board: rebased.board,
      canonicalTaskIDs: rebased.canonicalTaskIDs,
      deletedCanonicalIDs: recomputeDeletedCanonicalIDs(
        freshDurable.canonicalTaskIDs,
        freshDurable.deletedCanonicalIDs,
        rebased.canonicalTaskIDs,
      ),
    };
    sourceLive = { ...freshLive, durableBase: freshDurable };
    result = await attempt();
  }
  if (!result.ok || options.cancelled()) {
    return {
      candidate, conflicts, persisted: false, recoveryPending: false, writes, failure: result,
    };
  }
  let committed = result.snapshot;
  const afterCommit = options.readLive();
  if (afterCommit.epoch !== sourceLive.epoch) {
    const repaired = mergeDurableEdit(
      sourceLive.board,
      sourceLive.canonicalTaskIDs,
      afterCommit.board,
      afterCommit.canonicalTaskIDs,
      committed.board,
      committed.canonicalTaskIDs,
    );
    conflicts.push(...repaired.conflicts);
    const repairCandidate: LiveCandidate = {
      board: repaired.board,
      canonicalTaskIDs: repaired.canonicalTaskIDs,
      deletedCanonicalIDs: recomputeDeletedCanonicalIDs(
        committed.canonicalTaskIDs,
        committed.deletedCanonicalIDs,
        repaired.canonicalTaskIDs,
      ),
    };
    writes += 1;
    const repair = await options.repairPersist(
      repairCandidate,
      committed.version,
      () => !options.cancelled() && options.readLive().epoch === afterCommit.epoch,
    );
    candidate = repairCandidate;
    if (!repair.ok || options.cancelled()) {
      return {
        candidate,
        conflicts,
        persisted: false,
        snapshot: committed,
        recoveryPending: true,
        writes,
      };
    }
    committed = repair.snapshot;
    sourceLive = afterCommit;
  }
  if (options.readLive().epoch !== sourceLive.epoch || options.cancelled()) {
    return {
      candidate,
      conflicts,
      persisted: false,
      snapshot: committed,
      recoveryPending: true,
      writes,
    };
  }
  return {
    candidate: {
      board: committed.board,
      canonicalTaskIDs: committed.canonicalTaskIDs,
      deletedCanonicalIDs: committed.deletedCanonicalIDs,
    },
    conflicts,
    persisted: true,
    snapshot: committed,
    recoveryPending: false,
    writes,
  };
}

export async function reconcileStartupBoardFetch(options: {
  remote: RemoteStore;
  identity: Identity;
  readLive: () => LiveBoardSnapshot;
  readSnapshot: () => DurableSnapshot;
  persist: (
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean,
  ) => Promise<PersistenceResult>;
  apply: (snapshot: DurableSnapshot, merged: MergeResult) => void;
  push: (snapshot: DurableSnapshot) => void;
  cancelled: () => boolean;
}): Promise<{
  remoteBoard: Board | null;
  remoteTaskIDs: ReadonlyMap<string, string>;
  merged?: MergeResult;
  persisted?: boolean;
  generation?: number;
  snapshot?: DurableSnapshot;
  recoveryPending?: boolean;
}> {
  const baseLive = options.readLive();
  let remoteTaskIDs: ReadonlyMap<string, string> = new Map();
  const remoteBoard = await options.remote.loadRemote(options.identity, (ids) => {
    remoteTaskIDs = ids;
  });
  if (options.cancelled()) return { remoteBoard, remoteTaskIDs };
  const current = options.readLive();
  if (!remoteBoard || current.epoch === baseLive.epoch) {
    return { remoteBoard, remoteTaskIDs };
  }
  const merged = mergeStartupEdit(
    baseLive.board,
    baseLive.canonicalTaskIDs,
    current.board,
    current.canonicalTaskIDs,
    remoteBoard,
    remoteTaskIDs,
  );
  const outcome = await commitLiveCandidate({
    sourceLive: current,
    candidate: {
      board: merged.board,
      canonicalTaskIDs: merged.canonicalTaskIDs,
      deletedCanonicalIDs: recomputeDeletedCanonicalIDs(
        current.durableBase.canonicalTaskIDs,
        current.deletedCanonicalIDs,
        merged.canonicalTaskIDs,
      ),
    },
    readLive: options.readLive,
    readDurable: options.readSnapshot,
    persist: (candidate, version, guard) => options.persist(
      candidate.board,
      candidate.canonicalTaskIDs,
      candidate.deletedCanonicalIDs,
      version,
      guard,
    ),
    repairPersist: (candidate, version, guard) => options.persist(
      candidate.board,
      candidate.canonicalTaskIDs,
      candidate.deletedCanonicalIDs,
      version,
      guard,
    ),
    cancelled: options.cancelled,
  });
  const persistedMerge = {
    ...merged,
    ...outcome.candidate,
    conflicts: [...merged.conflicts, ...outcome.conflicts],
  };
  if (!outcome.persisted || !outcome.snapshot) {
    return {
      remoteBoard,
      remoteTaskIDs,
      merged: persistedMerge,
      persisted: false,
      snapshot: outcome.snapshot,
      recoveryPending: outcome.recoveryPending,
    };
  }
  options.apply(outcome.snapshot, persistedMerge);
  options.push(outcome.snapshot);
  return {
    remoteBoard,
    remoteTaskIDs,
    merged: persistedMerge,
    persisted: true,
    generation: outcome.snapshot.generation,
    snapshot: outcome.snapshot,
    recoveryPending: false,
  };
}

export class RemoteStore {
  private saveTimer: ReturnType<typeof setTimeout> | null = null;
  private pending: PendingSave | null = null;
  private inFlight = false;
  private epoch = 0;
  private etag: string | null = null;
  private base: Board | null = null;
  private baseTaskIDs: ReadonlyMap<string, string> = new Map();
  private ambiguousBlocked = false;
  private recoveryOperationID: string | null = null;

  private assertEpoch(epoch: number): void {
    if (epoch !== this.epoch) throw new Error('remote operation cancelled');
  }

  private async awaitAtEpoch<T>(epoch: number, pending: Promise<T>): Promise<T> {
    try {
      const value = await pending;
      this.assertEpoch(epoch);
      return value;
    } catch (error) {
      this.assertEpoch(epoch);
      throw error;
    }
  }

  private taskIDMapAtEpoch(
    epoch: number,
    response: Response,
    pushed: Board,
    ambiguousOnInvalidSuccess = false,
  ): Promise<ReadonlyMap<string, string>> {
    return this.awaitAtEpoch(
      epoch,
      taskIDMap(response, pushed, ambiguousOnInvalidSuccess),
    );
  }

  async detect(): Promise<boolean> {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), HEALTH_TIMEOUT_MS);
    try {
      const res = await fetch('/api/health', { signal: ctrl.signal });
      if (!res.ok) return false;
      return ((await res.json()) as { ok?: unknown }).ok === true;
    } catch {
      return false;
    } finally {
      clearTimeout(timer);
    }
  }

  private async fetchSnapshot(identity: Identity, epoch: number): Promise<RemoteSnapshot> {
    const res = await this.awaitAtEpoch(epoch, authedFetch(identity, '/api/board', {
      headers: { Accept: 'application/json' },
    }));
    const etag = res.headers.get('ETag');
    if (res.status === 404) {
      this.assertEpoch(epoch);
      this.etag = etag;
      this.base = null;
      this.baseTaskIDs = new Map();
      return { board: null, taskIDs: new Map() };
    }
    if (!res.ok) throw new Error(`GET /api/board failed: ${res.status}`);
    const contentType = res.headers
      .get('Content-Type')
      ?.split(';', 1)[0]
      ?.trim()
      .toLowerCase();
    let board: Board;
    let taskIDs: ReadonlyMap<string, string> = new Map();
    if (contentType === 'application/json') {
      let body: unknown;
      try {
        body = await this.awaitAtEpoch(epoch, res.json());
      } catch {
        throw invalidBoardSnapshot();
      }
      if (typeof body !== 'object' || body === null || Array.isArray(body)) {
        throw invalidBoardSnapshot();
      }
      const markdown = (body as Record<string, unknown>).board;
      if (typeof markdown !== 'string' || markdown.trim() === '') throw invalidBoardSnapshot();
      board = parse(markdown);
      taskIDs = taskIDMapFromBody(body, board, invalidBoardSnapshot);
    } else {
      board = parse(await this.awaitAtEpoch(epoch, res.text()));
    }
    this.assertEpoch(epoch);
    this.etag = etag;
    this.base = board;
    this.baseTaskIDs = taskIDs;
    return { board, taskIDs };
  }

  private async fetchSnapshotAtEpoch(
    identity: Identity,
    epoch: number,
  ): Promise<RemoteSnapshot> {
    try {
      const snapshot = await this.fetchSnapshot(identity, epoch);
      this.assertEpoch(epoch);
      return snapshot;
    } catch (error) {
      this.assertEpoch(epoch);
      throw error;
    }
  }

  async loadRemote(
    identity: Identity,
    onTaskIDs?: (taskIDs: ReadonlyMap<string, string>) => void,
  ): Promise<Board | null> {
    const loadEpoch = this.epoch;
    const snapshot = await this.fetchSnapshotAtEpoch(identity, loadEpoch);
    if (snapshot.taskIDs.size > 0) onTaskIDs?.(snapshot.taskIDs);
    return snapshot.board;
  }

  /**
   * Replay the exact durable create request before ordinary startup sync. The
   * original If-Match is never replaced with a token fetched after the crash.
   */
  async replayPendingBoardWrite(
    identity: Identity,
    pending: PendingBoardWrite,
    current: Board,
    currentIDs: ReadonlyMap<string, string>,
  ): Promise<MergeResult & { needsPush: boolean; acknowledgedOperationID?: string }> {
    const replayEpoch = this.epoch;
    if (
      this.recoveryOperationID !== null &&
      this.recoveryOperationID !== pending.operation_id
    ) {
      throw new Error('another pending board write requires recovery');
    }
    this.ambiguousBlocked = true;
    this.recoveryOperationID = pending.operation_id;
    const headers: Record<string, string> = {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      'Idempotency-Key': pending.operation_id,
    };
    if (pending.if_match) headers['If-Match'] = pending.if_match;
    let res: Response;
    try {
      res = await this.awaitAtEpoch(replayEpoch, authedFetch(identity, '/api/board', {
        method: 'PUT', headers, body: pending.body,
      }));
    } catch (error) {
      if (error instanceof ReauthRequiredError) throw error;
      throw new AmbiguousWriteError(error);
    }
    this.assertEpoch(replayEpoch);
    if (res.status === 409) {
      const snapshot = await this.fetchSnapshotAtEpoch(identity, replayEpoch);
      if (!snapshot.board) {
        return {
          board: { ...current, tasks: current.tasks.filter((task) => !currentIDs.has(task.id)) },
          canonicalTaskIDs: new Map(),
          conflicts: [],
          needsPush: true,
          acknowledgedOperationID: pending.operation_id,
        };
      }
      const merged = mergeWithoutCanonicalBase(current, currentIDs, snapshot.board, snapshot.taskIDs);
      const conflicts: string[] = [];
      return {
        board: {
          ...merged.board,
          title: chooseField(
            'board title', pending.sent_board.title, current.title, snapshot.board.title, conflicts,
          ),
        },
        canonicalTaskIDs: merged.taskIDs,
        conflicts,
        needsPush: true,
        acknowledgedOperationID: pending.operation_id,
      };
    }
    const acknowledged = await this.taskIDMapAtEpoch(
      replayEpoch, res, pending.sent_board, true,
    );
    const acknowledgedIDs = mergeTaskIDMaps(
      new Map(Object.entries(pending.sent_canonical_ids)),
      acknowledged,
    );
    const snapshot = await this.fetchSnapshotAtEpoch(identity, replayEpoch);
    if (!snapshot.board) {
      const recoveredIDs = mergeTaskIDMaps(currentIDs, acknowledgedIDs);
      const board = {
        ...current,
        tasks: current.tasks.filter((task) => !recoveredIDs.has(task.id)),
      };
      return {
        board,
        canonicalTaskIDs: new Map(),
        conflicts: [],
        needsPush: board.tasks.length > 0,
        acknowledgedOperationID: pending.operation_id,
      };
    }
    const merged = mergeCanonicalBoards(
      current,
      mergeTaskIDMaps(currentIDs, acknowledgedIDs),
      snapshot.board,
      snapshot.taskIDs,
      pending.sent_board,
      acknowledgedIDs,
    );
    const snapshotCanonicalIDs = new Set(snapshot.taskIDs.values());
    const needsPush =
      serialize(merged.board) !== serialize(snapshot.board) ||
      [...merged.canonicalTaskIDs.values()].some((id) => !snapshotCanonicalIDs.has(id));
    return { ...merged, needsPush, acknowledgedOperationID: pending.operation_id };
  }

  /** Resume autosave only after the exact replay result is durably persisted. */
  resumeAfterPendingBoardWrite(
    pendingWrite: PendingBoardWrite,
    recovered: MergeResult & { acknowledgedOperationID?: string },
  ): void {
    if (recovered.acknowledgedOperationID !== pendingWrite.operation_id) {
      throw new Error('pending board write was not acknowledged');
    }
    if (this.recoveryOperationID !== pendingWrite.operation_id) {
      throw new Error('pending board write recovery was not started');
    }
    if (this.pending?.canonicalTaskIDs !== undefined) {
      const sentIDs = new Map(Object.entries(pendingWrite.sent_canonical_ids));
      for (const task of pendingWrite.sent_board.tasks) {
        const acknowledged = recovered.canonicalTaskIDs.get(task.id);
        if (acknowledged) sentIDs.set(task.id, acknowledged);
      }
      const rebased = mergeCanonicalBoards(
        this.pending.board,
        mergeTaskIDMaps(this.pending.canonicalTaskIDs, sentIDs),
        recovered.board,
        recovered.canonicalTaskIDs,
        pendingWrite.sent_board,
        sentIDs,
      );
      this.pending.board = rebased.board;
      this.pending.canonicalTaskIDs = rebased.canonicalTaskIDs;
      this.pending.conflicts = [...this.pending.conflicts, ...rebased.conflicts];
    }
    this.base = recovered.board;
    this.baseTaskIDs = recovered.canonicalTaskIDs;
    this.ambiguousBlocked = false;
    this.recoveryOperationID = null;
    this.drain();
  }

  /**
   * Reconcile a dirty reconstructed envelope before its first conditional PUT.
   * Canonical membership still proves both deletion directions even though
   * field-level base values were not persisted: known missing tasks stay
   * deleted, and previously unseen server tasks are retained.
   */
  async prepareDirtyMapped(
    identity: Identity,
    local: Board,
    localIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string> = new Set(),
  ): Promise<{
    board: Board;
    taskIDs: ReadonlyMap<string, string>;
    deletedCanonicalIDs: ReadonlySet<string>;
  }> {
    const prepareEpoch = this.epoch;
    const snapshot = await this.fetchSnapshotAtEpoch(identity, prepareEpoch);
    if (!this.etag) throw new Error('dirty recovery requires a board version');
    if (!snapshot.board) {
      // A versioned 404 is an authoritative empty canonical set. Previously
      // mapped tasks were deleted remotely; only genuinely new null-ID work
      // remains eligible for the conditional recreate.
      return {
        board: {
          ...local,
          tasks: local.tasks.filter((task) => !localIDs.has(task.id)),
        },
        taskIDs: new Map(),
        deletedCanonicalIDs: new Set(),
      };
    }

    const knownCanonical = new Set([
      ...localIDs.values(),
      ...deletedCanonicalIDs,
    ]);
    const serverByCanonical = byCanonical(snapshot.board, snapshot.taskIDs);
    const deleted = new Set(deletedCanonicalIDs);
    const tasks = local.tasks.filter((task) => {
      const canonical = localIDs.get(task.id);
      if (!canonical || serverByCanonical.has(canonical)) return true;
      deleted.add(canonical);
      return false;
    });
    const taskIDs = new Map<string, string>();
    for (const task of tasks) {
      const canonical = localIDs.get(task.id);
      if (canonical) taskIDs.set(task.id, canonical);
    }
    for (const task of snapshot.board.tasks) {
      const canonical = snapshot.taskIDs.get(task.id);
      if (!canonical || knownCanonical.has(canonical)) continue;
      tasks.push(task);
      taskIDs.set(task.id, canonical);
    }
    return {
      board: { ...local, tasks },
      taskIDs,
      deletedCanonicalIDs: deleted,
    };
  }

  private async putBoard(
    save: PendingSave,
    board: Board,
    ids: ReadonlyMap<string, string>,
  ): Promise<Response> {
    const { headers, body, durableCreate } = prepareBoardRequest(save, board, ids);
    if (durableCreate) await this.stagePendingCreate(save, board, ids, body);
    if (save.operationID && durableCreate) headers['Idempotency-Key'] = save.operationID;
    if (this.etag) headers['If-Match'] = this.etag;
    return this.awaitAtEpoch(save.epoch, authedFetch(save.identity, '/api/board', {
      method: 'PUT', headers, body, keepalive: save.keepalive,
    }).catch((error: unknown) => {
      if (error instanceof ReauthRequiredError || !durableCreate) throw error;
      throw new AmbiguousWriteError(error);
    }));
  }

  private async stagePendingCreate(
    save: PendingSave,
    board: Board,
    ids: ReadonlyMap<string, string>,
    body: string,
  ): Promise<void> {
    const expectedVersion = durableVersionForSave(save);
    if (save.pendingWriteStager && !expectedVersion) {
      throw new Error('board write staging requires a durable version');
    }
    save.operationID ??= newId();
    if (!save.pendingWriteStager) return;
    const staged = await this.awaitAtEpoch(
      save.epoch,
      save.pendingWriteStager.stagePendingBoardWrite({
        operation_id: save.operationID,
        body,
        sent_board: board,
        sent_canonical_ids: Object.fromEntries(ids),
        if_match: this.etag,
      }, expectedVersion!, () => (
        save.epoch === this.epoch && (save.isLiveCurrent?.() ?? true)
      )),
    );
    if (!staged.ok) throw staged.error ?? new Error('failed to stage board write');
    const stagedGeneration = staged.generation;
    if (
      typeof stagedGeneration !== 'number' ||
      !Number.isSafeInteger(stagedGeneration) ||
      stagedGeneration < 0
    ) throw new Error('board write staging returned no generation');
    save.generation = stagedGeneration;
    save.durableVersion = staged.snapshot?.version ?? {
      present: true,
      generation: stagedGeneration,
    };
    if (staged.snapshot) save.durableSnapshot = staged.snapshot;
  }

  private rebasePendingFromLearnedConflict(
    save: PendingSave,
    pushed: Board,
    pushedIDs: ReadonlyMap<string, string>,
  ): void {
    const pending = this.pending;
    if (
      pending?.epoch !== save.epoch ||
      pending.canonicalTaskIDs === undefined ||
      save.canonicalTaskIDs === undefined
    ) return;
    const rebased = mergeCanonicalBoards(
      pending.board,
      pending.canonicalTaskIDs,
      pushed,
      pushedIDs,
      save.board,
      save.canonicalTaskIDs,
    );
    pending.board = rebased.board;
    pending.canonicalTaskIDs = rebased.canonicalTaskIDs;
    pending.conflicts = [...pending.conflicts, ...rebased.conflicts];
  }

  private async retryConflict(
    save: PendingSave,
    mergeBase: Board | null,
    mergeBaseIDs: ReadonlyMap<string, string>,
  ): Promise<PushAttempt> {
    if (save.legacy) throw new Error('PUT /api/board failed: 409');
    const server = await this.fetchSnapshotAtEpoch(save.identity, save.epoch);
    const merged = mergeConflictSnapshot(save, server, mergeBase, mergeBaseIDs);
    this.rebasePendingFromLearnedConflict(save, merged.pushed, merged.pushedIDs);
    const response = await this.putBoard(save, merged.pushed, merged.pushedIDs);
    this.assertEpoch(save.epoch);
    return { ...merged, response };
  }

  private applyAcknowledgedPush(
    save: PendingSave,
    attempt: PushAttempt,
    acknowledged: ReadonlyMap<string, string>,
  ): ReadonlyMap<string, string> {
    this.base = attempt.pushed;
    const acknowledgedSaveIDs = mergeTaskIDMaps(attempt.pushedIDs, acknowledged);
    this.baseTaskIDs = acknowledgedSaveIDs;
    const tag = attempt.response.headers.get('ETag');
    if (tag) this.etag = tag;

    const pending = this.pending;
    if (
      pending?.epoch !== save.epoch ||
      pending.canonicalTaskIDs === undefined ||
      save.canonicalTaskIDs === undefined
    ) return acknowledgedSaveIDs;
    const pendingIDs = mergeTaskIDMaps(pending.canonicalTaskIDs, acknowledged);
    const saveBaseIDs = mergeTaskIDMaps(save.canonicalTaskIDs, acknowledged);
    const rebased = mergeCanonicalBoards(
      pending.board,
      pendingIDs,
      attempt.pushed,
      this.baseTaskIDs,
      save.board,
      saveBaseIDs,
    );
    pending.board = rebased.board;
    pending.canonicalTaskIDs = rebased.canonicalTaskIDs;
    pending.conflicts = [...pending.conflicts, ...rebased.conflicts];
    return acknowledgedSaveIDs;
  }

  private applyAcknowledgementOutcome(save: PendingSave, outcome: unknown): void {
    if (
      typeof outcome !== 'object' ||
      outcome === null ||
      !('persisted' in outcome)
    ) return;
    const persistence = outcome as {
      persisted?: unknown;
      generation?: unknown;
      snapshot?: DurableSnapshot;
    };
    if (persistence.persisted === false && save.operationID) {
      throw new Error('create acknowledgement was not durably persisted');
    }
    const pending = this.pending;
    if (persistence.persisted !== true || pending?.epoch !== save.epoch) return;
    if (persistence.snapshot) {
      pending.generation = persistence.snapshot.generation;
      pending.durableVersion = persistence.snapshot.version;
      pending.durableSnapshot = persistence.snapshot;
    } else if (Number.isSafeInteger(persistence.generation)) {
      const generation = persistence.generation as number;
      pending.generation = generation;
      pending.durableVersion = { present: true, generation };
    }
  }

  private async deliverAcknowledgement(
    save: PendingSave,
    attempt: PushAttempt,
    taskIDs: ReadonlyMap<string, string>,
  ): Promise<void> {
    if (!save.onSuccess) return;
    try {
      const outcome = await this.awaitAtEpoch(
        save.epoch,
        Promise.resolve(save.onSuccess({
          pushed: attempt.pushed,
          taskIDs,
          conflicts: attempt.conflicts,
          operationID: save.operationID,
          isCurrent: () => save.epoch === this.epoch,
          generation: save.generation,
          durableVersion: save.durableVersion,
          durableSnapshot: save.durableSnapshot,
        })),
      );
      this.applyAcknowledgementOutcome(save, outcome);
    } catch (error) {
      this.assertEpoch(save.epoch);
      throw new AmbiguousWriteError(error);
    }
  }

  private async execute(save: PendingSave): Promise<void> {
    const mergeBase = this.base;
    const mergeBaseIDs = this.baseTaskIDs;
    const initialResponse = await this.putBoard(
      save, save.board, save.canonicalTaskIDs ?? new Map<string, string>(),
    );
    this.assertEpoch(save.epoch);
    const attempt = initialResponse.status === 409
      ? await this.retryConflict(save, mergeBase, mergeBaseIDs)
      : {
        pushed: save.board,
        pushedIDs: save.canonicalTaskIDs ?? new Map<string, string>(),
        conflicts: save.conflicts,
        response: initialResponse,
      };
    const acknowledged = await this.taskIDMapAtEpoch(
      save.epoch, attempt.response, attempt.pushed, Boolean(save.operationID),
    );
    this.assertEpoch(save.epoch);
    const acknowledgedSaveIDs = this.applyAcknowledgedPush(save, attempt, acknowledged);
    await this.deliverAcknowledgement(save, attempt, acknowledgedSaveIDs);
  }

  private drain(): void {
    if (this.inFlight || !this.pending || this.ambiguousBlocked) return;
    const save = this.pending;
    this.pending = null;
    this.inFlight = true;
    void this.execute(save)
      .catch((err: unknown) => {
        if (save.epoch !== this.epoch) return;
        if (err instanceof AmbiguousWriteError) {
          this.ambiguousBlocked = true;
          this.recoveryOperationID ??= save.operationID ?? null;
        }
        save.onError?.(err);
      })
      .finally(() => {
        this.inFlight = false;
        if (save.epoch !== this.epoch) save.onObsolete?.();
        this.drain();
      });
  }

  private queue(
    identity: Identity,
    board: Board,
    onError?: (err: unknown) => void,
    onSuccess?: SaveCallback,
    opts?: {
      keepalive?: boolean;
      canonicalTaskIDs?: ReadonlyMap<string, string>;
      legacy?: boolean;
      pendingWriteStager?: PendingWriteStager;
      generation?: number;
      durableVersion?: DurableVersion;
      durableSnapshot?: DurableSnapshot;
      isLiveCurrent?: () => boolean;
      onObsolete?: () => void;
    },
  ): void {
    this.pending = {
      identity,
      board,
      onError,
      onSuccess,
      keepalive: opts?.keepalive ?? false,
      canonicalTaskIDs: opts?.canonicalTaskIDs,
      legacy: opts?.legacy ?? false,
      epoch: this.epoch,
      conflicts: [],
      pendingWriteStager: opts?.pendingWriteStager,
      generation: opts?.generation,
      durableVersion: opts?.durableVersion,
      durableSnapshot: opts?.durableSnapshot,
      isLiveCurrent: opts?.isLiveCurrent,
      onObsolete: opts?.onObsolete,
    };
    this.drain();
  }

  saveRemote(
    identity: Identity,
    board: Board,
    onError?: (err: unknown) => void,
    onSuccess?: SaveCallback,
    opts?: {
      keepalive?: boolean;
      canonicalTaskIDs?: ReadonlyMap<string, string>;
      pendingWriteStager?: PendingWriteStager;
      generation?: number;
      durableVersion?: DurableVersion;
      durableSnapshot?: DurableSnapshot;
      isLiveCurrent?: () => boolean;
    },
  ): void {
    this.queue(identity, board, onError, onSuccess, opts);
  }

  saveRemoteDebounced(
    identity: Identity,
    board: Board,
    onError?: (err: unknown) => void,
    onSuccess?: SaveCallback,
    opts?: {
      canonicalTaskIDs?: ReadonlyMap<string, string>;
      pendingWriteStager?: PendingWriteStager;
      generation?: number;
      durableVersion?: DurableVersion;
      durableSnapshot?: DurableSnapshot;
      isLiveCurrent?: () => boolean;
    },
  ): void {
    if (this.saveTimer !== null) clearTimeout(this.saveTimer);
    this.pending = {
      identity,
      board,
      onError,
      onSuccess,
      keepalive: false,
      canonicalTaskIDs: opts?.canonicalTaskIDs,
      legacy: false,
      epoch: this.epoch,
      conflicts: [],
      pendingWriteStager: opts?.pendingWriteStager,
      generation: opts?.generation,
      durableVersion: opts?.durableVersion,
      durableSnapshot: opts?.durableSnapshot,
      isLiveCurrent: opts?.isLiveCurrent,
    };
    this.saveTimer = setTimeout(() => {
      this.saveTimer = null;
      this.drain();
    }, SAVE_DEBOUNCE_MS);
  }

  /**
   * Recover a dirty pre-envelope board without guessing deleted/renamed IDs.
   * The GET supplies the required version; the sole write is conditional.
   */
  async bootstrapLegacy(
    identity: Identity,
    local: Board,
  ): Promise<{ board: Board; taskIDs: ReadonlyMap<string, string> }> {
    const bootstrapEpoch = this.epoch;
    const snapshot = await this.fetchSnapshotAtEpoch(identity, bootstrapEpoch);
    if (!this.etag) throw new Error('legacy recovery requires a board version');
    if (!snapshot.board) {
      return new Promise((resolve, reject) => {
        this.queue(identity, local, reject, ({ pushed, taskIDs }) => {
          resolve({ board: pushed, taskIDs });
        }, {
          legacy: true,
          onObsolete: () => reject(new Error('remote operation cancelled')),
        });
      });
    }

    const serverBuckets = new Map<string, Task[]>();
    for (const task of snapshot.board.tasks) {
      const key = placedKey(task);
      const bucket = serverBuckets.get(key);
      if (bucket) {
        bucket.push(task);
      } else {
        serverBuckets.set(key, [task]);
      }
    }
    const localCounts = new Map<string, number>();
    for (const task of local.tasks) {
      const key = placedKey(task);
      localCounts.set(key, (localCounts.get(key) ?? 0) + 1);
    }
    const recovered = new Map<string, string>();
    const matchedServer = new Set<string>();
    for (const task of local.tasks) {
      const bucket = serverBuckets.get(placedKey(task)) ?? [];
      if (bucket.length !== 1 || localCounts.get(placedKey(task)) !== 1) continue;
      const serverTask = bucket[0]!;
      const canonical = snapshot.taskIDs.get(serverTask.id);
      if (canonical) {
        recovered.set(task.id, canonical);
        matchedServer.add(serverTask.id);
      }
    }
    const merged: Board = {
      ...local,
      tasks: [
        ...local.tasks,
        ...snapshot.board.tasks.filter((task) => !matchedServer.has(task.id)),
      ],
    };
    return new Promise((resolve, reject) => {
      this.queue(identity, merged, reject, ({ pushed, taskIDs }) => {
        resolve({ board: pushed, taskIDs });
      }, {
        legacy: true,
        canonicalTaskIDs: recovered,
        onObsolete: () => reject(new Error('remote operation cancelled')),
      });
    });
  }

  cancel(): void {
    if (this.saveTimer !== null) clearTimeout(this.saveTimer);
    this.saveTimer = null;
    this.pending = null;
    this.epoch += 1;
    this.etag = null;
    this.base = null;
    this.baseTaskIDs = new Map();
  }

  flush(): void {
    if (this.saveTimer !== null) clearTimeout(this.saveTimer);
    this.saveTimer = null;
    if (this.pending) this.pending.keepalive = true;
    this.drain();
  }
}

type PendingReplayResult = MergeResult & {
  needsPush: boolean;
  acknowledgedOperationID?: string;
};

function mergeLiveEditAfterRecovery(
  base: LiveBoardSnapshot,
  latest: LiveBoardSnapshot,
  recovered: MergeResult,
): MergeResult {
  if (latest.epoch === base.epoch) return recovered;
  const merged = mergeAcknowledgedState(
    latest.board,
    latest.canonicalTaskIDs,
    base.board,
    base.canonicalTaskIDs,
    recovered.board,
    recovered.canonicalTaskIDs,
  );
  return {
    ...merged,
    conflicts: [...recovered.conflicts, ...merged.conflicts],
  };
}

/**
 * Complete startup receipt recovery without losing edits made during either
 * awaited request. The recovered state is persisted before UI adoption or a
 * resumed network write, and a failed persistence keeps RemoteStore blocked.
 */
export async function reconcilePendingBoardWrite(options: {
  remote: RemoteStore;
  identity: Identity;
  pendingWrite: PendingBoardWrite;
  readLive: () => LiveBoardSnapshot;
  readSnapshot: () => DurableSnapshot;
  persistAcknowledgement: (
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    operationID: string,
    isLiveCurrent: () => boolean,
  ) => Promise<PersistenceResult>;
  repairPersist: (
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean,
  ) => Promise<PersistenceResult>;
  apply: (recovered: PendingReplayResult, snapshot: DurableSnapshot) => void;
  queuePush: (snapshot: DurableSnapshot) => void;
  cancelled: () => boolean;
}): Promise<{
  recovered: PendingReplayResult;
  persisted: boolean;
  recoveryPending?: boolean;
  snapshot?: DurableSnapshot;
}> {
  const base = options.readLive();
  const durableBase = base.durableBase;
  if (
    durableBase.pendingBoardWrite?.operation_id !== options.pendingWrite.operation_id
  ) {
    return {
      recovered: {
        board: durableBase.board,
        canonicalTaskIDs: durableBase.canonicalTaskIDs,
        conflicts: [],
        needsPush: false,
      },
      persisted: false,
    };
  }
  const replayed = await options.remote.replayPendingBoardWrite(
    options.identity,
    options.pendingWrite,
    base.board,
    base.canonicalTaskIDs,
  );
  const latest = options.readLive();
  const merged = mergeLiveEditAfterRecovery(base, latest, replayed);
  const recovered: PendingReplayResult = {
    ...merged,
    needsPush:
      replayed.needsPush ||
      serialize(merged.board) !== serialize(replayed.board) ||
      !same([...merged.canonicalTaskIDs], [...replayed.canonicalTaskIDs]),
    acknowledgedOperationID: replayed.acknowledgedOperationID,
  };
  if (options.cancelled() || !recovered.acknowledgedOperationID) {
    return { recovered, persisted: false };
  }
  const outcome = await commitLiveCandidate({
    sourceLive: latest,
    candidate: {
      board: recovered.board,
      canonicalTaskIDs: recovered.canonicalTaskIDs,
      deletedCanonicalIDs: recomputeDeletedCanonicalIDs(
        durableBase.canonicalTaskIDs,
        durableBase.deletedCanonicalIDs,
        recovered.canonicalTaskIDs,
      ),
    },
    readLive: options.readLive,
    readDurable: options.readSnapshot,
    persist: (candidate, version, guard) => options.persistAcknowledgement(
      candidate.board,
      candidate.canonicalTaskIDs,
      candidate.deletedCanonicalIDs,
      version,
      recovered.acknowledgedOperationID!,
      guard,
    ),
    repairPersist: (candidate, version, guard) => options.repairPersist(
      candidate.board,
      candidate.canonicalTaskIDs,
      candidate.deletedCanonicalIDs,
      version,
      guard,
    ),
    canRetry: (snapshot) =>
      snapshot.pendingBoardWrite?.operation_id === options.pendingWrite.operation_id,
    cancelled: options.cancelled,
  });
  const persistedState: PendingReplayResult = {
    ...recovered,
    ...outcome.candidate,
    conflicts: [...recovered.conflicts, ...outcome.conflicts],
    needsPush: recovered.needsPush || outcome.writes > 1,
  };
  if (!outcome.persisted || !outcome.snapshot) {
    return {
      recovered: persistedState,
      persisted: false,
      recoveryPending: outcome.recoveryPending,
      snapshot: outcome.snapshot,
    };
  }
  options.apply(persistedState, outcome.snapshot);
  if (persistedState.needsPush) options.queuePush(outcome.snapshot);
  options.remote.resumeAfterPendingBoardWrite(options.pendingWrite, persistedState);
  return {
    recovered: persistedState,
    persisted: true,
    recoveryPending: false,
    snapshot: outcome.snapshot,
  };
}

/** Preserve edits made while dirty legacy recovery is awaiting GET or PUT. */
export async function reconcileLegacyBootstrap(options: {
  remote: RemoteStore;
  identity: Identity;
  readLive: () => LiveBoardSnapshot;
  readSnapshot: () => DurableSnapshot;
  persist: (
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean,
  ) => Promise<PersistenceResult>;
  repairPersist: (
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean,
  ) => Promise<PersistenceResult>;
  apply: (recovered: MergeResult, needsPush: boolean, snapshot: DurableSnapshot) => void;
  queuePush: (snapshot: DurableSnapshot) => void;
  cancelled: () => boolean;
}): Promise<{
  recovered: MergeResult;
  persisted: boolean;
  needsPush: boolean;
  recoveryPending?: boolean;
  snapshot?: DurableSnapshot;
}> {
  const base = options.readLive();
  const durableBase = base.durableBase;
  if (!durableBase.migratedRaw) {
    return {
      recovered: { ...base, conflicts: [] },
      persisted: false,
      needsPush: false,
    };
  }
  const acknowledged = await options.remote.bootstrapLegacy(
    options.identity,
    base.board,
  );
  const latest = options.readLive();
  const recovered = mergeLiveEditAfterRecovery(base, latest, {
    board: acknowledged.board,
    canonicalTaskIDs: acknowledged.taskIDs,
    conflicts: [],
  });
  let needsPush =
    serialize(recovered.board) !== serialize(acknowledged.board) ||
    !same([...recovered.canonicalTaskIDs], [...acknowledged.taskIDs]);
  if (options.cancelled()) return { recovered, persisted: false, needsPush };
  const outcome = await commitLiveCandidate({
    sourceLive: latest,
    candidate: {
      board: recovered.board,
      canonicalTaskIDs: recovered.canonicalTaskIDs,
      deletedCanonicalIDs: recomputeDeletedCanonicalIDs(
        durableBase.canonicalTaskIDs,
        durableBase.deletedCanonicalIDs,
        recovered.canonicalTaskIDs,
      ),
    },
    readLive: options.readLive,
    readDurable: options.readSnapshot,
    persist: (candidate, version, guard) => options.persist(
      candidate.board,
      candidate.canonicalTaskIDs,
      candidate.deletedCanonicalIDs,
      version,
      guard,
    ),
    repairPersist: (candidate, version, guard) => options.repairPersist(
      candidate.board,
      candidate.canonicalTaskIDs,
      candidate.deletedCanonicalIDs,
      version,
      guard,
    ),
    canRetry: (snapshot) => snapshot.migratedRaw,
    cancelled: options.cancelled,
  });
  const persistedState: MergeResult = {
    ...recovered,
    ...outcome.candidate,
    conflicts: [...recovered.conflicts, ...outcome.conflicts],
  };
  needsPush = needsPush || outcome.writes > 1;
  if (!outcome.persisted || !outcome.snapshot) {
    return {
      recovered: persistedState,
      persisted: false,
      needsPush,
      recoveryPending: outcome.recoveryPending,
      snapshot: outcome.snapshot,
    };
  }
  options.apply(persistedState, needsPush, outcome.snapshot);
  if (needsPush) options.queuePush(outcome.snapshot);
  return {
    recovered: persistedState,
    persisted: true,
    needsPush,
    recoveryPending: false,
    snapshot: outcome.snapshot,
  };
}
