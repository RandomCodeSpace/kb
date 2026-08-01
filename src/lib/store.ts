import type { Board } from './model';
import { CURRENT_SEED_SCHEMA, seedBoard } from './model';

export { CURRENT_SEED_SCHEMA, SEED_ID_FORMAT_VERSION, seedBoard } from './model';

/** Storage abstraction — a remote (VPS sync) adapter drops in behind this later. */
export interface BoardStore {
  load(): Board | null;
  save(
    board: Board,
    canonicalTaskIDs?: ReadonlyMap<string, string>,
    deletedCanonicalIDs?: ReadonlySet<string>,
  ): Promise<PersistenceResult>;
}

export type PersistenceResult =
  | { ok: true; generation: number; snapshot: DurableSnapshot }
  | {
    ok: false;
    error: unknown;
    conflict?: true;
    conflictKind?: 'durable' | 'live';
    currentGeneration?: number;
    currentVersion?: DurableVersion;
  };

export interface PersistenceContinuation {
  warn: () => void;
  markDirty: () => void;
  scheduleRemote: () => void;
}

/** Keep remote durability independent from a failed browser-storage write. */
export function continueAfterLocalPersistence(
  result: PersistenceResult,
  warningGate: { current: boolean },
  next: PersistenceContinuation,
): void {
  if (!result.ok && !warningGate.current) {
    warningGate.current = true;
    next.warn();
  }
  next.markDirty();
  next.scheduleRemote();
}

const ENVELOPE_VERSION = 3;

export interface PendingBoardWrite {
  operation_id: string;
  body: string;
  sent_board: Board;
  sent_canonical_ids: Record<string, string>;
  if_match: string | null;
}

interface BoardEnvelope {
  version: typeof ENVELOPE_VERSION;
  generation?: number;
  board: Board;
  canonical_ids: Record<string, string>;
  deleted_canonical_ids?: string[];
  identity_recovery_needed?: boolean;
  pending_board_write?: PendingBoardWrite;
}

function isPendingBoardWrite(value: unknown): value is PendingBoardWrite {
  if (!isRecord(value) || !isBoard(value.sent_board)) return false;
  return (
    typeof value.operation_id === 'string' &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value.operation_id) &&
    typeof value.body === 'string' &&
    isCanonicalRecord(value.sent_canonical_ids, value.sent_board) &&
    (value.if_match === null || typeof value.if_match === 'string')
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isBoard(value: unknown): value is Board {
  if (!isRecord(value)) return false;
  const board = value;
  if (typeof board.title !== 'string' || !Array.isArray(board.tasks)) return false;
  const seenIDs = new Set<string>();
  return board.tasks.every((task) => {
    if (!isRecord(task)) return false;
    if (
      typeof task.id !== 'string' ||
      task.id.trim() === '' ||
      seenIDs.has(task.id) ||
      typeof task.emoji !== 'string' ||
      typeof task.title !== 'string' ||
      typeof task.desc !== 'string' ||
      typeof task.status !== 'string' ||
      !['todo', 'doing', 'done', 'cancelled'].includes(task.status) ||
      typeof task.blocked !== 'boolean' ||
      typeof task.prio !== 'number' ||
      ![1, 2, 3, 4].includes(task.prio) ||
      (task.due !== undefined && typeof task.due !== 'string') ||
      (task.effort !== undefined &&
        (typeof task.effort !== 'string' ||
          !['S', 'M', 'L'].includes(task.effort))) ||
      !Array.isArray(task.tags) ||
      !task.tags.every((tag) => typeof tag === 'string') ||
      !Array.isArray(task.checks) ||
      !task.checks.every(
        (check) =>
          isRecord(check) &&
          typeof check.text === 'string' &&
          typeof check.done === 'boolean',
      ) ||
      typeof task.createdAt !== 'string' ||
      typeof task.movedAt !== 'string'
    ) return false;
    seenIDs.add(task.id);
    return true;
  });
}

function isCanonicalRecord(
  value: unknown,
  board: Board,
): value is Record<string, string> {
  if (!isRecord(value)) return false;
  const clientIDs = new Set(board.tasks.map((task) => task.id));
  const canonicalIDs = new Set<string>();
  for (const [client, canonical] of Object.entries(value)) {
    if (
      client.trim() === '' ||
      !clientIDs.has(client) ||
      typeof canonical !== 'string' ||
      canonical.trim() === '' ||
      canonicalIDs.has(canonical)
    ) return false;
    canonicalIDs.add(canonical);
  }
  return true;
}

function isDeletedCanonicalIDs(
  value: unknown,
  liveCanonicalIDs: ReadonlySet<string>,
): value is string[] {
  if (value === undefined) return true;
  if (!Array.isArray(value)) return false;
  const seen = new Set<string>();
  return value.every((canonical) => {
    if (
      typeof canonical !== 'string' ||
      canonical.trim() === '' ||
      liveCanonicalIDs.has(canonical) ||
      seen.has(canonical)
    ) return false;
    seen.add(canonical);
    return true;
  });
}

const BOARD_KEY = 'kb.board.v1';
const STREAK_KEY = 'kb.streak.v1';
const DIRTY_KEY = 'kb.dirty.v1';
const AUX_MIGRATED_KEY = 'kb.aux-migrated.v1';
const BOARD_MIGRATED_KEY = 'kb.board-migrated.v1';

const KEY_PREFIX = 'kb.';
const LEGACY_PREFIX = 'webtui.';
const NAMESPACE_MARKER = '.ns.';

/**
 * Injective namespace framing for localStorage keys. The encoded length makes
 * the boundary explicit, so `alice` can never consume `alice.work`'s suffix.
 */
export function namespaceStorageKey(
  base: string,
  ns: string,
  suffix?: string,
): string {
  const encoded = encodeURIComponent(ns);
  return `${base}${NAMESPACE_MARKER}${encoded.length}:${encoded}${
    suffix === undefined ? '' : `.${suffix}`
  }`;
}

/** Parse only the new framed shape and require the namespace to match exactly. */
export function namespaceStorageSuffix(
  base: string,
  ns: string,
  key: string,
): string | null {
  const prefix = namespaceStorageKey(base, ns);
  if (key === prefix) return '';
  return key.startsWith(`${prefix}.`) ? key.slice(prefix.length + 1) : null;
}

/** Pre-G009 key shape. Direct construction is safe; prefix scans are not. */
export function legacyNamespaceStorageKey(
  base: string,
  ns: string,
  suffix?: string,
): string {
  const namespaced = ns === 'default' ? base : `${base}.${ns}`;
  return suffix === undefined ? namespaced : `${namespaced}.${suffix}`;
}

/**
 * Per-user key: the 'default' namespace keeps the legacy un-suffixed keys so
 * pre-identity boards survive.
 */
function nsKey(base: string, ns: string): string {
  return namespaceStorageKey(base, ns);
}

/**
 * One-time, non-destructive copy of the pre-rename `webtui.*` values to their
 * `kb.*` keys. The old keys are deliberately left in place: a user who rolls
 * back to an older build must not lose their board. `flagKey` makes it run
 * exactly once per storage — re-copying later would resurrect state the new
 * code has since removed (a cleared dirty flag, a signed-out identity).
 */
export function migrateLegacyKeys(
  storage: Storage,
  flagKey: string,
  keys: readonly string[],
): void {
  try {
    if (storage.getItem(flagKey) === '1') return;
    for (const key of keys) {
      if (!key.startsWith(KEY_PREFIX)) continue;
      if (storage.getItem(key) !== null) continue;
      const legacy = storage.getItem(LEGACY_PREFIX + key.slice(KEY_PREFIX.length));
      if (legacy !== null) storage.setItem(key, legacy);
    }
    storage.setItem(flagKey, '1');
  } catch {
    // Storage unavailable — nothing to migrate.
  }
}

const migrated = new Set<string>();

/** Migrate auxiliary keys without ever touching the board or its marker. */
function ensureMigrated(ns: string): void {
  if (migrated.has(ns)) return;
  migrated.add(ns);
  try {
    const legacyFlag = legacyNamespaceStorageKey(AUX_MIGRATED_KEY, ns);
    const legacyKeys = [
      legacyNamespaceStorageKey(STREAK_KEY, ns),
      legacyNamespaceStorageKey(DIRTY_KEY, ns),
    ];
    migrateLegacyKeys(localStorage, legacyFlag, legacyKeys);

    const flag = nsKey(AUX_MIGRATED_KEY, ns);
    if (localStorage.getItem(flag) === '1') return;
    const bases = [STREAK_KEY, DIRTY_KEY] as const;
    for (const base of bases) {
      const target = nsKey(base, ns);
      if (localStorage.getItem(target) !== null) continue;
      const source = legacyNamespaceStorageKey(base, ns);
      const value = localStorage.getItem(source);
      if (value !== null) localStorage.setItem(target, value);
    }
    localStorage.setItem(flag, '1');
  } catch {
    // Storage unavailable — nothing to migrate.
  }
}

/** Board plus whether it came from a fresh seed rather than storage. */
export interface LoadedBoard {
  board: Board;
  seeded: boolean;
  canonicalTaskIDs: ReadonlyMap<string, string>;
  /** Canonical tasks deliberately absent from the local board while dirty. */
  deletedCanonicalIDs: ReadonlySet<string>;
  /** True until a raw pre-envelope board has completed identity recovery. */
  migratedRaw: boolean;
  pendingBoardWrite: PendingBoardWrite | null;
  generation: number;
}

export interface DurableVersion {
  present: boolean;
  generation: number;
}

export interface DurableSnapshot extends LoadedBoard {
  version: DurableVersion;
}

export interface LocalStoreOptions {
  storage?: Storage;
  locks?: LockManager | null;
}

export class LocalStore implements BoardStore {
  private readonly ns: string;
  private readonly key: string;
  private readonly legacyKey: string;
  private readonly preRenameKey: string;
  private readonly migrationMarkerKey: string;
  private readonly lockName: string;
  private readonly storage: Storage;
  private readonly locks: LockManager | null;
  private virtualSeed: Board | null = null;

  constructor(ns: string = 'default', options: LocalStoreOptions = {}) {
    this.ns = ns;
    this.storage = options.storage ?? localStorage;
    if (options.locks !== undefined) {
      this.locks = options.locks;
    } else if (typeof navigator === 'undefined') {
      this.locks = null;
    } else {
      this.locks = navigator.locks ?? null;
    }
    this.key = nsKey(BOARD_KEY, ns);
    this.legacyKey = legacyNamespaceStorageKey(BOARD_KEY, ns);
    this.preRenameKey = LEGACY_PREFIX + this.legacyKey.slice(KEY_PREFIX.length);
    this.migrationMarkerKey = nsKey(BOARD_MIGRATED_KEY, ns);
    this.lockName = namespaceStorageKey('kb:board-envelope:', ns);
  }

  private invalidStoredBoard(strict: boolean): null {
    if (strict) throw new Error('stored board envelope is invalid');
    return null;
  }

  private readSource(strict = false): {
    loaded: DurableSnapshot | null;
    legacy: boolean;
  } {
    try {
      const framed = this.storage.getItem(this.key);
      const legacyKB = framed === null ? this.storage.getItem(this.legacyKey) : null;
      const preRename = framed === null && legacyKB === null
        ? this.storage.getItem(this.preRenameKey)
        : null;
      const raw = framed ?? legacyKB ?? preRename;
      const legacy = framed === null && raw !== null;
      if (!raw) return { loaded: null, legacy };
      const parsed = JSON.parse(raw) as unknown;
      if (isRecord(parsed) && (parsed.version === 2 || parsed.version === ENVELOPE_VERSION) && isBoard(parsed.board)) {
        if (!isCanonicalRecord(parsed.canonical_ids, parsed.board)) {
          return { loaded: this.invalidStoredBoard(strict), legacy };
        }
        const liveCanonicalIDs = new Set(Object.values(parsed.canonical_ids));
        if (!isDeletedCanonicalIDs(parsed.deleted_canonical_ids, liveCanonicalIDs)) {
          return { loaded: this.invalidStoredBoard(strict), legacy };
        }
        if (parsed.pending_board_write !== undefined && !isPendingBoardWrite(parsed.pending_board_write)) {
          return { loaded: this.invalidStoredBoard(strict), legacy };
        }
        if (
          parsed.generation !== undefined &&
          (typeof parsed.generation !== 'number' ||
            !Number.isSafeInteger(parsed.generation) ||
            parsed.generation < 0)
        ) return { loaded: this.invalidStoredBoard(strict), legacy };
        const envelope: BoardEnvelope = {
          version: ENVELOPE_VERSION,
          ...(parsed.generation !== undefined ? { generation: parsed.generation } : {}),
          board: parsed.board,
          canonical_ids: parsed.canonical_ids,
          ...(parsed.deleted_canonical_ids
            ? { deleted_canonical_ids: parsed.deleted_canonical_ids }
            : {}),
          ...(parsed.identity_recovery_needed === true
            ? { identity_recovery_needed: true }
            : {}),
          ...(parsed.pending_board_write
            ? { pending_board_write: parsed.pending_board_write }
            : {}),
        };
        const canonicalTaskIDs = new Map(Object.entries(envelope.canonical_ids));
        const generation = envelope.generation ?? 0;
        const loaded: DurableSnapshot = {
          board: envelope.board,
          seeded: false,
          canonicalTaskIDs,
          deletedCanonicalIDs: new Set(envelope.deleted_canonical_ids ?? []),
          migratedRaw: envelope.identity_recovery_needed === true,
          pendingBoardWrite: envelope.pending_board_write ?? null,
          generation,
          version: { present: true, generation },
        };
        return { loaded, legacy };
      }

      // Pre-G003 values were a raw Board. Treat the migration logically here;
      // the next locked mutation publishes the v3 envelope atomically.
      if (!isBoard(parsed)) {
        return { loaded: this.invalidStoredBoard(strict), legacy };
      }
      const board = parsed;
      const loaded: DurableSnapshot = {
        board,
        seeded: false,
        canonicalTaskIDs: new Map(),
        deletedCanonicalIDs: new Set(),
        migratedRaw: true,
        pendingBoardWrite: null,
        generation: 0,
        version: { present: true, generation: 0 },
      };
      return { loaded, legacy };
    } catch (error) {
      if (strict) throw error;
      return { loaded: null, legacy: false };
    }
  }

  private read(strict = false): DurableSnapshot | null {
    return this.readSource(strict).loaded;
  }

  private absentSnapshot(): DurableSnapshot {
    this.virtualSeed ??= seedBoard(this.ns, CURRENT_SEED_SCHEMA);
    return {
      board: this.virtualSeed,
      seeded: true,
      canonicalTaskIDs: new Map(),
      deletedCanonicalIDs: new Set(),
      migratedRaw: false,
      pendingBoardWrite: null,
      generation: 0,
      version: { present: false, generation: 0 },
    };
  }

  /** Return one coherent durable state without migration, seeding, or writes. */
  loadSnapshot(): DurableSnapshot {
    return this.read(true) ?? this.absentSnapshot();
  }

  load(): Board | null {
    return this.read()?.board ?? null;
  }

  loadCanonicalTaskIDs(): ReadonlyMap<string, string> {
    return this.read()?.canonicalTaskIDs ?? new Map();
  }

  loadDeletedCanonicalIDs(): ReadonlySet<string> {
    return this.read()?.deletedCanonicalIDs ?? new Set();
  }

  loadPendingBoardWrite(): PendingBoardWrite | null {
    return this.read()?.pendingBoardWrite ?? null;
  }

  save(
    board: Board,
    canonicalTaskIDs?: ReadonlyMap<string, string>,
    deletedCanonicalIDs?: ReadonlySet<string>,
  ): Promise<PersistenceResult> {
    return this.mutate((current) => {
      const mergedIDs = this.mergeCanonicalTaskIDs(
        current?.canonicalTaskIDs ?? new Map(),
        canonicalTaskIDs,
      );
      if (!mergedIDs.ok) return mergedIDs;
      const deleted = new Set(current?.deletedCanonicalIDs ?? []);
      if (deletedCanonicalIDs) {
        for (const id of deletedCanonicalIDs) deleted.add(id);
      }
      return this.savedState(board, mergedIDs.value, deleted, current);
    });
  }

  private conflictAt(current: DurableSnapshot | null, expectedVersion: DurableVersion):
    | {
      ok: false;
      error: Error;
      conflict: true;
      conflictKind: 'durable';
      currentGeneration: number;
      currentVersion: DurableVersion;
    }
    | null {
    const currentVersion = current?.version ?? { present: false, generation: 0 };
    if (
      typeof expectedVersion?.present === 'boolean' &&
      Number.isSafeInteger(expectedVersion.generation) &&
      expectedVersion.generation >= 0 &&
      currentVersion.present === expectedVersion.present &&
      currentVersion.generation === expectedVersion.generation
    ) return null;
    return {
      ok: false,
      error: new Error('stale durable board version'),
      conflict: true,
      conflictKind: 'durable',
      currentGeneration: currentVersion.generation,
      currentVersion,
    };
  }

  private replacementState(
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    current: DurableSnapshot | null,
  ): LoadedBoard | { ok: false; error: Error } {
    for (const [clientID, canonicalID] of canonicalTaskIDs) {
      const existing = current?.canonicalTaskIDs.get(clientID);
      if (existing !== undefined && existing !== canonicalID) {
        return { ok: false, error: new Error(`canonical identity conflict for ${clientID}`) };
      }
      for (const [otherClientID, otherCanonicalID] of current?.canonicalTaskIDs ?? []) {
        if (otherClientID !== clientID && otherCanonicalID === canonicalID) {
          return { ok: false, error: new Error(`canonical identity conflict for ${canonicalID}`) };
        }
      }
    }
    const canonical = Object.fromEntries(canonicalTaskIDs);
    if (!isCanonicalRecord(canonical, board)) {
      return { ok: false, error: new Error('replacement canonical identities are invalid') };
    }
    const liveCanonicalIDs = new Set(canonicalTaskIDs.values());
    const deleted = [...deletedCanonicalIDs];
    if (!isDeletedCanonicalIDs(deleted, liveCanonicalIDs)) {
      return { ok: false, error: new Error('replacement deletion evidence is invalid') };
    }
    return {
      board,
      seeded: false,
      canonicalTaskIDs: new Map(canonicalTaskIDs),
      deletedCanonicalIDs: new Set(deletedCanonicalIDs),
      migratedRaw: current?.migratedRaw ?? false,
      pendingBoardWrite: current?.pendingBoardWrite ?? null,
      generation: current?.generation ?? 0,
    };
  }

  saveIfGeneration(
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean = () => true,
  ): Promise<PersistenceResult> {
    return this.transition(
      expectedVersion,
      isLiveCurrent,
      (current) => this.replacementState(
        board, canonicalTaskIDs, deletedCanonicalIDs, current,
      ),
    );
  }

  private mergeCanonicalTaskIDs(
    current: ReadonlyMap<string, string>,
    incoming?: ReadonlyMap<string, string>,
  ): { ok: true; value: ReadonlyMap<string, string> } | { ok: false; error: Error } {
    if (!incoming) return { ok: true, value: current };
    const merged = new Map(current);
    for (const [clientID, canonicalID] of incoming) {
      const existing = merged.get(clientID);
      if (existing !== undefined && existing !== canonicalID) {
        return { ok: false, error: new Error(`canonical identity conflict for ${clientID}`) };
      }
      for (const [otherClientID, otherCanonicalID] of merged) {
        if (otherClientID !== clientID && otherCanonicalID === canonicalID) {
          return { ok: false, error: new Error(`canonical identity conflict for ${canonicalID}`) };
        }
      }
      merged.set(clientID, canonicalID);
    }
    return { ok: true, value: merged };
  }

  private savedState(
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    current: DurableSnapshot | null,
  ): LoadedBoard {
    const liveIDs = new Set(board.tasks.map((task) => task.id));
    const liveEntries = [...canonicalTaskIDs].filter(([client]) => liveIDs.has(client));
    const liveCanonicalIDs = new Set(liveEntries.map(([, canonical]) => canonical));
    const deleted = new Set(deletedCanonicalIDs);
    for (const [client, canonical] of canonicalTaskIDs) {
      if (!liveIDs.has(client)) deleted.add(canonical);
    }
    for (const canonical of liveCanonicalIDs) deleted.delete(canonical);
    return {
      board,
      seeded: false,
      canonicalTaskIDs: new Map(liveEntries),
      deletedCanonicalIDs: deleted,
      migratedRaw: current?.migratedRaw ?? false,
      pendingBoardWrite: current?.pendingBoardWrite ?? null,
      generation: current?.generation ?? 0,
    };
  }

  private serializeEnvelope(next: LoadedBoard): string {
    const envelope: BoardEnvelope = {
      version: ENVELOPE_VERSION,
      generation: next.generation,
      board: next.board,
      canonical_ids: Object.fromEntries(next.canonicalTaskIDs),
      ...(next.deletedCanonicalIDs.size > 0
        ? { deleted_canonical_ids: [...next.deletedCanonicalIDs] }
        : {}),
      ...(next.migratedRaw ? { identity_recovery_needed: true } : {}),
      ...(next.pendingBoardWrite
        ? { pending_board_write: next.pendingBoardWrite }
        : {}),
    };
    return JSON.stringify(envelope);
  }

  private async mutate(
    derive: (
      current: DurableSnapshot | null,
    ) => LoadedBoard | {
      ok: false;
      error: unknown;
      conflict?: true;
      conflictKind?: 'durable' | 'live';
      currentGeneration?: number;
      currentVersion?: DurableVersion;
    },
  ): Promise<PersistenceResult> {
    if (!this.locks) {
      return { ok: false, error: new Error('durable board storage locking unavailable') };
    }
    try {
      return await this.locks.request(
        this.lockName,
        { mode: 'exclusive' },
        () => {
          const source = this.readSource(true);
          const current = source.loaded;
          const next = derive(current);
          if (!('board' in next)) return next;
          const generation = current?.generation ?? 0;
          if (generation === Number.MAX_SAFE_INTEGER) {
            return {
              ok: false,
              error: new Error('board generation exhausted'),
              conflict: true,
              currentGeneration: generation,
            } as const;
          }
          const needsMigrationMarker =
            this.storage.getItem(this.migrationMarkerKey) !== '1';
          const committedGeneration = generation + 1;
          const committed: DurableSnapshot = {
            ...next,
            generation: committedGeneration,
            version: { present: true, generation: committedGeneration },
          };
          this.storage.setItem(this.key, this.serializeEnvelope(committed));
          if (needsMigrationMarker) {
            try {
              this.storage.setItem(this.migrationMarkerKey, '1');
            } catch {
              // The framed envelope is authoritative once committed. A later
              // mutation retries the marker without consulting legacy state.
            }
          }
          return {
            ok: true,
            generation: committed.generation,
            snapshot: committed,
          } as const;
        },
      );
    } catch (error) {
      return { ok: false, error };
    }
  }

  private transition(
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean,
    derive: (
      current: DurableSnapshot | null,
    ) => LoadedBoard | { ok: false; error: unknown },
  ): Promise<PersistenceResult> {
    return this.mutate((current) => {
      const conflict = this.conflictAt(current, expectedVersion);
      if (conflict) return conflict;
      if (!isLiveCurrent()) {
        return {
          ok: false,
          error: new Error('stale live board snapshot'),
          conflict: true,
          conflictKind: 'live',
          currentGeneration: current?.generation ?? 0,
          currentVersion: current?.version ?? { present: false, generation: 0 },
        } as const;
      }
      return derive(current);
    });
  }

  /** Persist the exact create-bearing request before any network side effect. */
  stagePendingBoardWrite(
    pending: PendingBoardWrite,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean = () => true,
  ): Promise<PersistenceResult> {
    return this.transition(
      expectedVersion,
      isLiveCurrent,
      (current) => current
        ? { ...current, pendingBoardWrite: pending }
        : { ok: false, error: new Error('board is not initialized') },
    );
  }

  /**
   * Persist an acknowledgement and clear only the operation it acknowledges
   * in the same localStorage write. A late acknowledgement cannot clear a
   * newer staged generation.
   */
  saveAcknowledgement(
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    operationID?: string,
    isLiveCurrent: () => boolean = () => true,
  ): Promise<PersistenceResult> {
    return this.transition(expectedVersion, isLiveCurrent, (current) => {
      const operationConflict = operationID === undefined
          ? current?.pendingBoardWrite !== null && current?.pendingBoardWrite !== undefined
          : current?.pendingBoardWrite?.operation_id !== operationID;
      if (operationConflict) {
        return {
          ok: false,
          error: new Error('stale board acknowledgement'),
          conflict: true,
          conflictKind: 'durable',
          currentGeneration: current?.generation ?? 0,
          currentVersion: current?.version ?? { present: false, generation: 0 },
        };
      }
      const next = this.replacementState(
        board, canonicalTaskIDs, deletedCanonicalIDs, current,
      );
      if (!('board' in next)) return next;
      if (operationID) {
        next.pendingBoardWrite = null;
      }
      return next;
    });
  }

  /** Clear the durable raw-board recovery marker only after a versioned ack. */
  completeIdentityRecovery(
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
    deletedCanonicalIDs: ReadonlySet<string>,
    expectedVersion: DurableVersion,
    isLiveCurrent: () => boolean = () => true,
  ): Promise<PersistenceResult> {
    return this.transition(expectedVersion, isLiveCurrent, (current) => {
      if (!current?.migratedRaw) {
        return {
          ok: false,
          error: new Error('identity recovery marker is no longer current'),
          conflict: true,
          conflictKind: 'durable',
        };
      }
      const next = this.replacementState(
        board, canonicalTaskIDs, deletedCanonicalIDs, current,
      );
      if (!('board' in next)) return next;
      next.migratedRaw = false;
      return next;
    });
  }

  /**
   * Load the stored board, or seed a fresh one. A seeded board is not local
   * work: it must never be pushed over a board the server already has, so
   * seeding clears the dirty flag instead of setting it. Callers use `seeded`
   * to adopt the server board on first contact rather than uploading the demo.
   */
  loadOrSeed(): DurableSnapshot {
    const loaded = this.read();
    if (loaded) return loaded;
    setDirty(this.ns, false);
    return this.absentSnapshot();
  }
}

/**
 * True when this namespace has local edits that never reached the server
 * (offline session, failed save, or a save pending at shutdown). Startup
 * consults this so a stale remote copy cannot silently overwrite newer
 * local changes.
 */
export function loadDirty(ns: string = 'default'): boolean {
  ensureMigrated(ns);
  try {
    return localStorage.getItem(nsKey(DIRTY_KEY, ns)) === '1';
  } catch {
    return false;
  }
}

export function setDirty(ns: string, dirty: boolean): void {
  ensureMigrated(ns);
  try {
    if (dirty) localStorage.setItem(nsKey(DIRTY_KEY, ns), '1');
    else localStorage.removeItem(nsKey(DIRTY_KEY, ns));
  } catch {
    // Storage unavailable — the next startup treats local state as clean.
  }
}

/** Today's shipped record: the cards shipped, not a count (see bumpShipped). */
interface Streak {
  date: string;
  ids: string[];
}

/**
 * Identity of a card for the shipped counter. A task id cannot be used: the
 * wire format does not carry ids, so parse() mints new ones on every server
 * refetch and the same card would be counted a second time after a reload.
 * The title is what survives that round trip. Two cards sharing a title count
 * once, which is the safe direction — the counter must never inflate.
 */
export function shipKey(task: { title: string }): string {
  return task.title.trim();
}

/**
 * Today's record, or an empty one. A record from an earlier day (or the
 * pre-rename `{date, n}` counter shape) reads as empty — the streak is a
 * cosmetic daily figure, so a rollover simply starts over.
 */
function loadStreak(ns: string): Streak {
  const date = new Date().toDateString();
  ensureMigrated(ns);
  try {
    const raw = localStorage.getItem(nsKey(STREAK_KEY, ns));
    if (!raw) return { date, ids: [] };
    const v = JSON.parse(raw) as { date?: unknown; ids?: unknown };
    if (v.date !== date || !Array.isArray(v.ids)) return { date, ids: [] };
    return { date, ids: v.ids.filter((x): x is string => typeof x === 'string') };
  } catch {
    return { date, ids: [] };
  }
}

export function shippedToday(ns: string = 'default'): number {
  return loadStreak(ns).ids.length;
}

/**
 * Record the card `key` (see shipKey) as shipped today and return the day's
 * count. Storing keys rather than a counter makes this idempotent per card:
 * dragging one card Done → Doing → Done → … still counts once, and so does
 * shipping it again after a reload.
 */
export function bumpShipped(ns: string, key: string): number {
  const streak = loadStreak(ns);
  if (!streak.ids.includes(key)) streak.ids.push(key);
  return saveStreak(ns, streak);
}

/**
 * Drop the card `key` from today's tally, for when it leaves Done, and return
 * the day's count.
 *
 * A reopened card is not shipped: the counter says what is done today, so it
 * has to be able to go down. Re-shipping the same card later adds it back and
 * it still counts once, so this cannot be farmed by moving a card back and
 * forth. Unknown keys are a no-op.
 */
export function unshipToday(ns: string, key: string): number {
  const streak = loadStreak(ns);
  const at = streak.ids.indexOf(key);
  if (at === -1) return streak.ids.length;
  streak.ids.splice(at, 1);
  return saveStreak(ns, streak);
}

function saveStreak(ns: string, streak: Streak): number {
  try {
    localStorage.setItem(nsKey(STREAK_KEY, ns), JSON.stringify(streak));
  } catch {
    // Storage unavailable — the count lives on in memory for this session.
  }
  return streak.ids.length;
}
