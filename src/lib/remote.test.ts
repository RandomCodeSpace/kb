import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Board } from './model';
import { newTask, seedBoard } from './model';
import { parse, serialize, wireTasks } from './markdown';
import { ReauthRequiredError, type Identity } from './auth';
import {
  commitLiveCandidate,
  mergeBoards,
  mergeAcknowledgedState,
  mergeCanonicalBoards,
  mergeDurableEdit,
  mergeStartupEdit,
  mergeTaskIDMaps,
  reconcileLegacyBootstrap,
  reconcilePendingBoardWrite,
  reconcileStartupBoardFetch,
  RemoteStore,
  sameBoardSemantics,
} from './remote';
import type { LiveBoardSnapshot, SaveAcknowledgement } from './remote';
import { LocalStore } from './store';
import type { DurableSnapshot } from './store';

const identity: Identity = { kind: 'manual', id: 'alice' };

function board(...titles: string[]): Board {
  return { title: 'kb', tasks: titles.map((title) => newTask({ title })) };
}

function durableSnapshot(
  value: Board,
  generation = 1,
  overrides: Partial<{
    migratedRaw: boolean;
    pendingBoardWrite: import('./store').PendingBoardWrite | null;
  }> = {},
): DurableSnapshot {
  return {
    board: value,
    seeded: false,
    canonicalTaskIDs: new Map<string, string>(),
    deletedCanonicalIDs: new Set<string>(),
    migratedRaw: overrides.migratedRaw ?? false,
    pendingBoardWrite: overrides.pendingBoardWrite ?? null,
    generation,
    version: { present: true, generation },
  };
}

function liveSnapshot(
  value: Board,
  generation = 1,
  overrides: Partial<{
    epoch: number;
    canonicalTaskIDs: ReadonlyMap<string, string>;
    deletedCanonicalIDs: ReadonlySet<string>;
    durableBase: ReturnType<typeof durableSnapshot>;
    needsLocalPersistence: boolean;
    remoteClean: boolean;
  }> = {},
): LiveBoardSnapshot {
  const durableBase = overrides.durableBase ?? durableSnapshot(value, generation);
  return {
    epoch: overrides.epoch ?? 0,
    board: value,
    canonicalTaskIDs: overrides.canonicalTaskIDs ?? new Map<string, string>(),
    deletedCanonicalIDs: overrides.deletedCanonicalIDs ?? new Set<string>(),
    durableBase,
    needsLocalPersistence: overrides.needsLocalPersistence ?? false,
    remoteClean: overrides.remoteClean ?? true,
  };
}

function persistenceSuccess(
  value: Board,
  generation: number,
  overrides: Partial<DurableSnapshot> = {},
) {
  const snapshot = { ...durableSnapshot(value, generation), ...overrides };
  return { ok: true as const, generation, snapshot };
}

function durablePendingSnapshot(value: Board, generation = 1) {
  return durableSnapshot(value, generation, {
    pendingBoardWrite: {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
      body: JSON.stringify({ board: serialize(value), task_ids: [null] }),
      sent_board: value,
      sent_canonical_ids: {},
      if_match: null,
    },
  });
}

function titles(b: Board): string[] {
  return b.tasks.map((t) => t.title);
}

function fixtureTask(id: string, title: string, desc = `description:${id}`): Board['tasks'][number] {
  return {
    id,
    emoji: '',
    title,
    desc,
    status: 'todo',
    blocked: false,
    prio: 3,
    tags: [],
    checks: [],
    createdAt: '2026-08-01T00:00:00.000Z',
    movedAt: '2026-08-01T00:00:00.000Z',
  };
}

type Call = { url: string; method: string; headers: Record<string, string>; body: string };

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, resolve, reject };
}

/** fetch stub answering a fixed script of responses, recording every call. */
function stubFetch(script: Response[]): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url,
        method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      const res = script.shift();
      if (!res) throw new Error(`unexpected fetch: ${init?.method ?? 'GET'} ${url}`);
      return Promise.resolve(res);
    }),
  );
  return calls;
}

/** saveRemote is fire-and-forget; resolve when it reports back. */
function save(store: RemoteStore, b: Board): Promise<Board> {
  return new Promise<Board>((resolve, reject) => {
    store.saveRemote(identity, b, reject, ({ pushed }) => resolve(pushed));
  });
}

type SaveResult = {
  pushed: Board;
  taskIDs: ReadonlyMap<string, string>;
};

type CharacterizedAcknowledgement = SaveAcknowledgement;

type CharacterizationCallback = (acknowledgement: SaveAcknowledgement) => void;

function captureAcknowledgement(
  start: (onSuccess: CharacterizationCallback, onError: (error: unknown) => void) => void,
  delivery?: CharacterizationCallback,
): Promise<CharacterizedAcknowledgement> {
  return new Promise((resolve, reject) => {
    start((acknowledgement) => {
      delivery?.(acknowledgement);
      resolve(acknowledgement);
    }, reject);
  });
}

function normalizedAcknowledgement(value: CharacterizedAcknowledgement) {
  return {
    ...value,
    taskIDs: [...value.taskIDs],
    isCurrent: value.isCurrent?.(),
  };
}

function saveWithTaskIDs(store: RemoteStore, b: Board): Promise<SaveResult> {
  return new Promise<SaveResult>((resolve, reject) => {
    store.saveRemote(identity, b, reject, ({ pushed, taskIDs }) => {
      resolve({ pushed, taskIDs });
    });
  });
}

function remoteSnapshot(
  b: Board,
  taskIDs: readonly string[],
  etag = 'v1',
): Response {
  return new Response(
    JSON.stringify({ board: serialize(b), task_ids: taskIDs }),
    {
      headers: {
        'Content-Type': 'application/json',
        ETag: etag,
      },
    },
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

// A write acknowledgement contains only that write's identities; replacing
// the prior map makes older cards start self-matching again.
describe('mergeTaskIDMaps', () => {
  it('adds later acknowledgements without dropping earlier mappings', () => {
    const earlier = new Map([
      ['client-a', 'server-a'],
      ['client-b', 'server-b'],
    ]);

    const merged = mergeTaskIDMaps(
      earlier,
      new Map([
        ['client-b', 'server-b-new'],
        ['client-c', 'server-c'],
      ]),
    );

    expect([...merged]).toEqual([
      ['client-a', 'server-a'],
      ['client-b', 'server-b-new'],
      ['client-c', 'server-c'],
    ]);
    expect([...earlier]).toEqual([
      ['client-a', 'server-a'],
      ['client-b', 'server-b'],
    ]);
  });
});

describe('mergeBoards', () => {
  it('keeps every server task the client has never seen', () => {
    const local = board('A', 'C');
    const server = board('A', 'B');
    const merged = mergeBoards(local, server, board('A'));
    expect(titles(merged)).toEqual(['A', 'C', 'B']);
  });

  it('honours a local delete of a task the client had seen', () => {
    const merged = mergeBoards(board('A'), board('A', 'D'), board('A', 'D'));
    expect(titles(merged)).toEqual(['A']);
  });

  it('keeps everything when there is no merge base', () => {
    const merged = mergeBoards(board('A'), board('B'), null);
    expect(titles(merged)).toEqual(['A', 'B']);
  });

  it('does not duplicate a task both sides already have', () => {
    const merged = mergeBoards(board('A'), board('A'), null);
    expect(titles(merged)).toEqual(['A']);
  });

  it('returns the local board unchanged when nothing is new', () => {
    const local = board('A');
    expect(mergeBoards(local, board('A'), board('A'))).toBe(local);
  });

  it('takes a column move by another surface instead of duplicating the card', () => {
    // `kb done A` while the SPA renamed an unrelated card: A must end up in
    // Done once, not once in each column.
    const base = board('A', 'B');
    const local = { ...base, tasks: [base.tasks[0]!, { ...base.tasks[1]!, title: 'B2' }] };
    const server = {
      ...base,
      tasks: [base.tasks[1]!, { ...base.tasks[0]!, status: 'done' as const }],
    };
    const merged = mergeBoards(local, server, base);
    expect(titles(merged)).toEqual(['A', 'B2']);
    expect(merged.tasks.map((t) => t.status)).toEqual(['done', 'todo']);
    // And the board that goes back on the wire holds one copy of each card.
    expect(titles(parse(serialize(merged)))).toEqual(['B2', 'A']);
  });

  it('keeps our own column move when we moved the card ourselves', () => {
    const base = board('A');
    const local = { ...base, tasks: [{ ...base.tasks[0]!, status: 'doing' as const }] };
    const server = { ...base, tasks: [{ ...base.tasks[0]!, status: 'done' as const }] };
    const merged = mergeBoards(local, server, base);
    expect(merged.tasks.map((t) => t.status)).toEqual(['doing']);
  });
});

function canonicalBoard(...titles: string[]): {
  board: Board;
  ids: ReadonlyMap<string, string>;
} {
  const value = board(...titles);
  return {
    board: value,
    ids: new Map(value.tasks.map((task, index) => [task.id, `server-${index}`])),
  };
}

describe('canonical three-way merge', () => {
  it('merges disjoint fields and reports same-field local wins', () => {
    const base = canonicalBoard('A');
    const localTask = { ...base.board.tasks[0]!, title: 'local title', desc: 'local desc' };
    const remoteTask = { ...base.board.tasks[0]!, title: 'remote title', blocked: true };
    const result = mergeCanonicalBoards(
      { ...base.board, tasks: [localTask] }, base.ids,
      { ...base.board, tasks: [remoteTask] }, base.ids,
      base.board, base.ids,
    );

    expect(result.board.tasks[0]).toMatchObject({
      title: 'local title',
      desc: 'local desc',
      blocked: true,
    });
    expect(result.conflicts).toContain('local title: title');
  });

  it('lets deletion win in both directions for base tasks', () => {
    const base = canonicalBoard('A', 'B');
    const localDeleted = mergeCanonicalBoards(
      { ...base.board, tasks: [base.board.tasks[0]!] },
      new Map([[base.board.tasks[0]!.id, 'server-0']]),
      {
        ...base.board,
        tasks: [base.board.tasks[0]!, { ...base.board.tasks[1]!, desc: 'remote edit' }],
      },
      base.ids,
      base.board,
      base.ids,
    );
    expect(titles(localDeleted.board)).toEqual(['A']);

    const remoteDeleted = mergeCanonicalBoards(
      {
        ...base.board,
        tasks: [{ ...base.board.tasks[0]!, desc: 'local edit' }, base.board.tasks[1]!],
      },
      base.ids,
      { ...base.board, tasks: [base.board.tasks[1]!] },
      new Map([[base.board.tasks[1]!.id, 'server-1']]),
      base.board,
      base.ids,
    );
    expect(titles(remoteDeleted.board)).toEqual(['B']);
  });

  it('uses local divergent order and anchors local additions before remote additions', () => {
    const base = canonicalBoard('A', 'B', 'C');
    const localNew = newTask({ title: 'local new' });
    const remoteNew = newTask({ title: 'remote new' });
    const local = {
      ...base.board,
      tasks: [base.board.tasks[1]!, localNew, base.board.tasks[0]!, base.board.tasks[2]!],
    };
    const remote = {
      ...base.board,
      tasks: [base.board.tasks[2]!, remoteNew, base.board.tasks[0]!, base.board.tasks[1]!],
    };
    const result = mergeCanonicalBoards(local, base.ids, remote, base.ids, base.board, base.ids);
    expect(titles(result.board)).toEqual(['B', 'local new', 'A', 'C', 'remote new']);
    expect(result.conflicts).toContain('todo column order');
  });

  it('ignores parser timestamp drift when status did not change', () => {
    const base = canonicalBoard('A');
    const remote = {
      ...base.board,
      tasks: [{ ...base.board.tasks[0]!, movedAt: '2099-01-01T00:00:00.000Z' }],
    };
    const result = mergeCanonicalBoards(
      base.board, base.ids, remote, base.ids, base.board, base.ids,
    );
    expect(result.board.tasks[0]!.movedAt).toBe(base.board.tasks[0]!.movedAt);
    expect(result.conflicts).toEqual([]);
  });

  it('anchors a new task after the nearest survivor when its immediate anchor moved', () => {
    const base = canonicalBoard('anchor', 'moved');
    const added = newTask({ title: 'new after moved' });
    const local = { ...base.board, tasks: [...base.board.tasks, added] };
    const remote = {
      ...base.board,
      tasks: [
        base.board.tasks[0]!,
        { ...base.board.tasks[1]!, status: 'doing' as const },
      ],
    };
    const result = mergeCanonicalBoards(
      local, base.ids, remote, base.ids, base.board, base.ids,
    );
    expect(titles(result.board)).toEqual(['anchor', 'new after moved', 'moved']);
    expect(result.board.tasks.map((task) => task.status)).toEqual([
      'todo', 'todo', 'doing',
    ]);
  });

  it('retains the canonical id of a server-only addition anchored between survivors', () => {
    const base: Board = {
      title: 'Base',
      tasks: [fixtureTask('base-a', 'A'), fixtureTask('base-c', 'C')],
    };
    const baseIDs = new Map([
      ['base-a', 'canonical-a'],
      ['base-c', 'canonical-c'],
    ]);
    const remote: Board = {
      title: 'Base',
      tasks: [
        fixtureTask('remote-a', 'A'),
        fixtureTask('server-b', 'B'),
        fixtureTask('remote-c', 'C'),
      ],
    };
    const result = mergeCanonicalBoards(
      base,
      baseIDs,
      remote,
      new Map([
        ['remote-a', 'canonical-a'],
        ['server-b', 'canonical-b'],
        ['remote-c', 'canonical-c'],
      ]),
      base,
      baseIDs,
    );

    expect(result.board.tasks.map((task) => task.id)).toEqual([
      'base-a', 'server-b', 'base-c',
    ]);
    expect([...result.canonicalTaskIDs].sort(([left], [right]) => left.localeCompare(right))).toEqual([
      ['base-a', 'canonical-a'],
      ['base-c', 'canonical-c'],
      ['server-b', 'canonical-b'],
    ]);
    expect(result.conflicts).toEqual([]);
  });

  it('keeps same-title local and remote additions distinct in deterministic insertion order', () => {
    const base: Board = { title: 'Base', tasks: [] };
    const local: Board = {
      title: 'Base',
      tasks: [fixtureTask('local-same', 'Same title')],
    };
    const remote: Board = {
      title: 'Base',
      tasks: [fixtureTask('remote-same', 'Same title')],
    };
    const result = mergeCanonicalBoards(
      local,
      new Map(),
      remote,
      new Map([['remote-same', 'canonical-remote']]),
      base,
      new Map(),
    );

    expect(result.board.tasks.map((item) => [item.id, item.title, item.desc])).toEqual([
      ['local-same', 'Same title', 'description:local-same'],
      ['remote-same', 'Same title', 'description:remote-same'],
    ]);
    expect([...result.canonicalTaskIDs]).toEqual([
      ['remote-same', 'canonical-remote'],
    ]);
    expect(result.conflicts).toEqual([]);
  });

  it.each([
    ['unchanged', 'Base', 'Base', 'Base', 'Base', []],
    ['local-only', 'Base', 'Local', 'Base', 'Local', []],
    ['remote-only', 'Base', 'Base', 'Remote', 'Remote', []],
    ['same-change', 'Base', 'Same', 'Same', 'Same', []],
    ['divergent-change', 'Base', 'Local', 'Remote', 'Local', ['board title']],
  ] as const)(
    'preserves board-title conflict behavior for %s',
    (_case, baseTitle, localTitle, remoteTitle, expectedTitle, expectedConflicts) => {
      const result = mergeCanonicalBoards(
        { title: localTitle, tasks: [] },
        new Map(),
        { title: remoteTitle, tasks: [] },
        new Map(),
        { title: baseTitle, tasks: [] },
        new Map(),
      );

      expect({ title: result.board.title, conflicts: result.conflicts }).toEqual({
        title: expectedTitle,
        conflicts: expectedConflicts,
      });
    },
  );
});

describe('legacy merge wire characterization', () => {
  it('preserves duplicate identity, order, and exact serialized wire output', () => {
    const local: Board = {
      title: 'Duplicate',
      tasks: [fixtureTask('local-1', 'Repeat'), fixtureTask('local-2', 'Repeat')],
    };
    const remote: Board = {
      title: 'Duplicate',
      tasks: [fixtureTask('remote-1', 'Repeat'), fixtureTask('remote-2', 'Repeat')],
    };
    const merged = mergeBoards(local, remote, { title: 'Duplicate', tasks: [] });

    expect(merged.tasks).toEqual([
      fixtureTask('local-1', 'Repeat'),
      fixtureTask('local-2', 'Repeat'),
      fixtureTask('remote-1', 'Repeat'),
      fixtureTask('remote-2', 'Repeat'),
    ]);
    expect(wireTasks(merged).map((item) => item.id)).toEqual([
      'local-1', 'local-2', 'remote-1', 'remote-2',
    ]);
    expect(serialize(merged)).toBe(
      '# Duplicate\n\n## To Do\n\n' +
      '- [ ] Repeat\n  description:local-1\n' +
      '- [ ] Repeat\n  description:local-2\n' +
      '- [ ] Repeat\n  description:remote-1\n' +
      '- [ ] Repeat\n  description:remote-2\n\n' +
      '## Doing\n\n\n## Done\n\n',
    );
  });
});

describe('deterministic seed identity merge', () => {
  it('rebases concurrent first edits from two stores without replacing the seed card', async () => {
    const values = new Map<string, string>();
    const storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      key: (index: number) => [...values.keys()][index] ?? null,
      removeItem: (key: string) => { values.delete(key); },
      setItem: (key: string, value: string) => { values.set(key, value); },
    } as Storage;
    const locks = {
      request: async <T>(
        _name: string,
        _options: LockOptions,
        callback: () => T | Promise<T>,
      ): Promise<T> => callback(),
    } as LockManager;
    const tabA = new LocalStore('two-tabs-first-edit', { storage, locks });
    const tabB = new LocalStore('two-tabs-first-edit', { storage, locks });

    // Both tabs observe virtual absence before either one creates the envelope.
    const baseA = tabA.loadSnapshot();
    const baseB = tabB.loadSnapshot();
    expect(baseA.version).toEqual({ present: false, generation: 0 });
    expect(baseB.version).toEqual({ present: false, generation: 0 });
    expect(baseB.board).toEqual(baseA.board);
    const seedID = baseA.board.tasks[0]!.id;

    const boardA = {
      ...baseA.board,
      tasks: [{ ...baseA.board.tasks[0]!, title: 'renamed in tab A' }, ...baseA.board.tasks.slice(1)],
    };
    const boardB = {
      ...baseB.board,
      tasks: [{ ...baseB.board.tasks[0]!, blocked: true }, ...baseB.board.tasks.slice(1)],
    };
    const liveA = liveSnapshot(boardA, 0, {
      epoch: 1,
      durableBase: baseA,
      needsLocalPersistence: true,
      remoteClean: false,
    });
    const liveB = liveSnapshot(boardB, 0, {
      epoch: 1,
      durableBase: baseB,
      needsLocalPersistence: true,
      remoteClean: false,
    });
    const candidate = (live: LiveBoardSnapshot) => ({
      board: live.board,
      canonicalTaskIDs: live.canonicalTaskIDs,
      deletedCanonicalIDs: live.deletedCanonicalIDs,
    });

    const committedA = await commitLiveCandidate({
      sourceLive: liveA,
      candidate: candidate(liveA),
      readLive: () => liveA,
      readDurable: () => tabA.loadSnapshot(),
      persist: (next, version, guard) => tabA.saveIfGeneration(
        next.board, next.canonicalTaskIDs, next.deletedCanonicalIDs, version, guard,
      ),
      repairPersist: (next, version, guard) => tabA.saveIfGeneration(
        next.board, next.canonicalTaskIDs, next.deletedCanonicalIDs, version, guard,
      ),
      cancelled: () => false,
    });
    expect(committedA).toMatchObject({ persisted: true, writes: 1 });

    const committedB = await commitLiveCandidate({
      sourceLive: liveB,
      candidate: candidate(liveB),
      readLive: () => liveB,
      readDurable: () => tabB.loadSnapshot(),
      persist: (next, version, guard) => tabB.saveIfGeneration(
        next.board, next.canonicalTaskIDs, next.deletedCanonicalIDs, version, guard,
      ),
      repairPersist: (next, version, guard) => tabB.saveIfGeneration(
        next.board, next.canonicalTaskIDs, next.deletedCanonicalIDs, version, guard,
      ),
      cancelled: () => false,
    });

    expect(committedB).toMatchObject({ persisted: true, writes: 2 });
    const final = tabA.loadSnapshot();
    const mergedCard = final.board.tasks.filter((task) => task.id === seedID);
    expect(mergedCard).toHaveLength(1);
    expect(mergedCard[0]).toMatchObject({ title: 'renamed in tab A', blocked: true });
    expect(final.board.tasks.map((task) => task.id)).toEqual(
      baseA.board.tasks.map((task) => task.id),
    );
    expect(final.canonicalTaskIDs.size).toBe(0);
    expect(final.deletedCanonicalIDs.size).toBe(0);
    expect(final.version).toEqual({ present: true, generation: 2 });

    const conflictA = new LocalStore('two-tabs-same-field', { storage, locks });
    const conflictB = new LocalStore('two-tabs-same-field', { storage, locks });
    const conflictBaseA = conflictA.loadSnapshot();
    const conflictBaseB = conflictB.loadSnapshot();
    const conflictLiveA = liveSnapshot({
      ...conflictBaseA.board,
      tasks: [{
        ...conflictBaseA.board.tasks[0]!,
        title: 'tab A title',
      }, ...conflictBaseA.board.tasks.slice(1)],
    }, 0, { epoch: 1, durableBase: conflictBaseA, needsLocalPersistence: true });
    const conflictLiveB = liveSnapshot({
      ...conflictBaseB.board,
      tasks: [{
        ...conflictBaseB.board.tasks[0]!,
        title: 'tab B title',
      }, ...conflictBaseB.board.tasks.slice(1)],
    }, 0, { epoch: 1, durableBase: conflictBaseB, needsLocalPersistence: true });
    const commit = (local: LocalStore, live: LiveBoardSnapshot) => commitLiveCandidate({
      sourceLive: live,
      candidate: candidate(live),
      readLive: () => live,
      readDurable: () => local.loadSnapshot(),
      persist: (next, version, guard) => local.saveIfGeneration(
        next.board, next.canonicalTaskIDs, next.deletedCanonicalIDs, version, guard,
      ),
      repairPersist: (next, version, guard) => local.saveIfGeneration(
        next.board, next.canonicalTaskIDs, next.deletedCanonicalIDs, version, guard,
      ),
      cancelled: () => false,
    });

    expect(await commit(conflictA, conflictLiveA)).toMatchObject({
      persisted: true,
      conflicts: [],
      writes: 1,
    });
    const conflicted = await commit(conflictB, conflictLiveB);
    expect(conflicted).toMatchObject({ persisted: true, writes: 2 });
    expect(conflicted.conflicts).toContain('tab B title: title');
    const conflictFinal = conflictA.loadSnapshot();
    expect(conflictFinal.board.tasks.filter(
      (task) => task.id === conflictBaseA.board.tasks[0]!.id,
    )).toHaveLength(1);
    expect(conflictFinal.board.tasks[0]!.title).toBe('tab B title');
  });

  it('merges concurrent first edits to different fields of one seed card once', () => {
    const base = seedBoard('concurrent-seed');
    const local = {
      ...base,
      tasks: [{ ...base.tasks[0]!, title: 'local title' }, ...base.tasks.slice(1)],
    };
    const fresh = {
      ...base,
      tasks: [{ ...base.tasks[0]!, blocked: true }, ...base.tasks.slice(1)],
    };

    const result = mergeDurableEdit(
      base, new Map(), local, new Map(), fresh, new Map(),
    );

    expect(result.board.tasks).toHaveLength(base.tasks.length);
    expect(result.board.tasks[0]).toMatchObject({ title: 'local title', blocked: true });
    expect(result.canonicalTaskIDs.size).toBe(0);
    expect(result.conflicts).toEqual([]);
  });

  it('reports a visible same-field seed conflict and keeps the local value', () => {
    const base = seedBoard('same-field');
    const local = {
      ...base,
      tasks: [{ ...base.tasks[0]!, title: 'local title' }, ...base.tasks.slice(1)],
    };
    const fresh = {
      ...base,
      tasks: [{ ...base.tasks[0]!, title: 'remote title' }, ...base.tasks.slice(1)],
    };

    const result = mergeDurableEdit(
      base, new Map(), local, new Map(), fresh, new Map(),
    );

    expect(result.board.tasks[0]!.title).toBe('local title');
    expect(result.conflicts).toContain('local title: title');
  });

  it('combines a real move timestamp with a concurrent field edit', () => {
    const base = seedBoard('move-and-field');
    const movedAt = '2026-08-01T03:00:00.000Z';
    const local = {
      ...base,
      tasks: [{
        ...base.tasks[0]!,
        status: 'doing' as const,
        movedAt,
      }, ...base.tasks.slice(1)],
    };
    const fresh = {
      ...base,
      tasks: [{ ...base.tasks[0]!, desc: 'edited elsewhere' }, ...base.tasks.slice(1)],
    };

    const result = mergeDurableEdit(
      base, new Map(), local, new Map(), fresh, new Map(),
    );

    expect(result.board.tasks.find((task) => task.id === base.tasks[0]!.id)).toMatchObject({
      status: 'doing',
      movedAt,
      desc: 'edited elsewhere',
    });
  });

  it('keeps duplicate titles in distinct immutable seed slots', () => {
    const seeded = seedBoard('duplicate-titles');
    const base = {
      ...seeded,
      tasks: seeded.tasks.map((task) => ({ ...task, title: 'Duplicate' })),
    };
    const local = {
      ...base,
      tasks: [{ ...base.tasks[0]!, desc: 'first slot' }, ...base.tasks.slice(1)],
    };
    const fresh = {
      ...base,
      tasks: [base.tasks[0]!, { ...base.tasks[1]!, blocked: true }, base.tasks[2]!],
    };

    const result = mergeDurableEdit(
      base, new Map(), local, new Map(), fresh, new Map(),
    );

    expect(result.board.tasks).toHaveLength(3);
    expect(result.board.tasks[0]!.desc).toBe('first slot');
    expect(result.board.tasks[1]!.blocked).toBe(true);
    expect(new Set(result.board.tasks.map((task) => task.id)).size).toBe(3);
  });
});

describe('RemoteStore concurrency', () => {
  it('persists an exact create request before fetch starts', async () => {
    const mem = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => mem.get(key) ?? null,
      setItem: (key: string, value: string) => { mem.set(key, value); },
      removeItem: (key: string) => { mem.delete(key); },
      key: () => null,
      clear: () => mem.clear(),
      get length() { return mem.size; },
    });
    const local = new LocalStore('stage-before-fetch');
    const created = board('created');
    const saved = await local.save(created);
    if (!saved.ok) throw saved.error;
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => {
      const pending = local.loadPendingBoardWrite();
      expect(pending?.body).toBe(init?.body);
      expect(pending?.if_match).toBeNull();
      expect(pending?.operation_id).toBe(
        (init?.headers as Record<string, string>)['Idempotency-Key'],
      );
      return Promise.resolve(new Response(
        JSON.stringify({ task_ids: ['server-created'] }),
        { status: 200, headers: { ETag: 'v1' } },
      ));
    }));
    await new Promise<void>((resolve, reject) => {
      new RemoteStore().saveRemote(identity, created, reject, () => resolve(), {
        canonicalTaskIDs: new Map(), pendingWriteStager: local,
        generation: saved.generation,
      });
    });
  });

  it('propagates the exact ordinary persistence generation to acknowledgement', async () => {
    const store = new RemoteStore();
    const known = board('known');
    const ids = new Map([[known.tasks[0]!.id, 'server-known']]);
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ task_ids: ['server-known'] }),
      { status: 200, headers: { ETag: 'v2' } },
    ))));
    const generation = await new Promise<number | undefined>((resolve, reject) => {
      store.saveRemote(
        identity,
        known,
        reject,
        ({ generation: committedGeneration }) => {
          resolve(committedGeneration);
        },
        { canonicalTaskIDs: ids, generation: 41 },
      );
    });
    expect(generation).toBe(41);
  });

  it('uses the second staged generation when a create PUT retries after 409', async () => {
    const store = new RemoteStore();
    const created = board('created');
    const stages: Array<{ operation_id: string; if_match: string | null }> = [];
    const pendingWriteStager = {
      stagePendingBoardWrite: async (pending: {
        operation_id: string;
        if_match: string | null;
      }) => {
        stages.push(pending);
        return { ok: true, generation: 10 + stages.length };
      },
    };
    const calls = stubFetch([
      new Response('conflict', { status: 409 }),
      new Response(null, { status: 404, headers: { ETag: 'v2' } }),
      new Response(JSON.stringify({ task_ids: ['server-created'] }), {
        status: 200,
        headers: { ETag: 'v3' },
      }),
    ]);
    const generation = await new Promise<number | undefined>((resolve, reject) => {
      store.saveRemote(
        identity,
        created,
        reject,
        ({ generation: committedGeneration }) => {
          resolve(committedGeneration);
        },
        { canonicalTaskIDs: new Map(), pendingWriteStager, generation: 9 },
      );
    });
    expect(calls.map((call) => call.method)).toEqual(['PUT', 'GET', 'PUT']);
    expect(stages).toHaveLength(2);
    expect(stages[0]!.operation_id).toBe(stages[1]!.operation_id);
    expect(stages.map((stage) => stage.if_match)).toEqual([null, 'v2']);
    expect(generation).toBe(12);
  });

  it('allows a queued non-create save after a stale acknowledgement conflict', async () => {
    const store = new RemoteStore();
    const active = board('active');
    const latest = { ...active, title: 'latest' };
    const ids = new Map([[active.tasks[0]!.id, 'server-active']]);
    let resolveFirst!: (response: Response) => void;
    const fetchMock = vi.fn(() => {
      if (fetchMock.mock.calls.length === 1) {
        return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      }
      return Promise.resolve(new Response(
        JSON.stringify({ task_ids: ['server-active'] }),
        { status: 200, headers: { ETag: 'v2' } },
      ));
    });
    vi.stubGlobal('fetch', fetchMock);
    store.saveRemote(
      identity,
      active,
      vi.fn(),
      () => ({ persisted: false, conflict: true }),
      { canonicalTaskIDs: ids, generation: 5 },
    );
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    const completed = new Promise<void>((resolve, reject) => {
      store.saveRemote(
        identity,
        latest,
        reject,
        () => resolve(),
        { canonicalTaskIDs: ids, generation: 6 },
      );
    });
    resolveFirst(new Response(JSON.stringify({ task_ids: ['server-active'] }), {
      status: 200,
      headers: { ETag: 'v1' },
    }));
    await completed;
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('rebases a same-page queued save onto the acknowledgement generation', async () => {
    const store = new RemoteStore();
    const active = board('active');
    const latest = { ...active, title: 'latest' };
    const ids = new Map([[active.tasks[0]!.id, 'server-active']]);
    const acknowledgedSnapshot = {
      ...durableSnapshot(latest, 3),
      canonicalTaskIDs: ids,
    };
    let resolveFirst!: (response: Response) => void;
    const fetchMock = vi.fn(() => {
      if (fetchMock.mock.calls.length === 1) {
        return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      }
      return Promise.resolve(new Response(
        JSON.stringify({ task_ids: ['server-active'] }),
        { status: 200, headers: { ETag: 'v2' } },
      ));
    });
    vi.stubGlobal('fetch', fetchMock);
    store.saveRemote(
      identity,
      active,
      vi.fn(),
      () => ({
        persisted: true,
        generation: 3,
        snapshot: acknowledgedSnapshot,
      }),
      { canonicalTaskIDs: ids, generation: 1 },
    );
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    const queuedDurableBase = new Promise<{
      generation: number | undefined;
      version: import('./store').DurableVersion | undefined;
      snapshot: DurableSnapshot | undefined;
    }>((resolve, reject) => {
      store.saveRemote(
        identity,
        latest,
        reject,
        ({ generation, durableVersion, durableSnapshot }) => {
          resolve({ generation, version: durableVersion, snapshot: durableSnapshot });
        },
        { canonicalTaskIDs: ids, generation: 2 },
      );
    });
    resolveFirst(new Response(JSON.stringify({ task_ids: ['server-active'] }), {
      status: 200,
      headers: { ETag: 'v1' },
    }));
    expect(await queuedDurableBase).toEqual({
      generation: 3,
      version: acknowledgedSnapshot.version,
      snapshot: acknowledgedSnapshot,
    });
  });

  it('blocks later dispatch after a create acknowledgement conflict', async () => {
    const store = new RemoteStore();
    const fetchMock = vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ task_ids: ['server-created'] }),
      { status: 200, headers: { ETag: 'v1' } },
    )));
    vi.stubGlobal('fetch', fetchMock);
    const failed = new Promise<unknown>((resolve) => {
      store.saveRemote(
        identity,
        board('created'),
        resolve,
        () => ({ persisted: false, conflict: true }),
        {
          canonicalTaskIDs: new Map(),
          generation: 1,
          pendingWriteStager: {
            stagePendingBoardWrite: async () => ({ ok: true, generation: 2 }),
          },
        },
      );
    });
    await failed;
    store.saveRemote(identity, board('later'), vi.fn(), vi.fn());
    store.flush();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('does not dispatch queued work after an ambiguous acknowledgement', async () => {
    const store = new RemoteStore();
    let resolveFirst!: (response: Response) => void;
    const fetchMock = vi.fn(() => new Promise<Response>((resolve) => {
      resolveFirst = resolve;
    }));
    vi.stubGlobal('fetch', fetchMock);
    const failed = new Promise<void>((resolve) => {
      store.saveRemote(identity, board('A'), () => resolve(), undefined, {
        canonicalTaskIDs: new Map(),
      });
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    store.saveRemote(identity, board('B'), vi.fn(), vi.fn(), {
      canonicalTaskIDs: new Map(),
    });
    resolveFirst(new Response('{bad', { status: 200 }));
    await failed;
    await Promise.resolve();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('keeps immediate, debounced, and flushed work blocked behind the original durable operation', async () => {
    vi.useFakeTimers();
    try {
      const store = new RemoteStore();
      const staged: Array<{ operation_id: string; body: string }> = [];
      const stager = {
        stagePendingBoardWrite: async (pending: { operation_id: string; body: string }) => {
          staged.push(pending);
          return { ok: true, generation: staged.length };
        },
      };
      const fetchMock = vi.fn(() => Promise.reject(new TypeError('connection reset')));
      vi.stubGlobal('fetch', fetchMock);
      const failed = new Promise<unknown>((resolve) => {
        store.saveRemote(identity, board('original'), resolve, undefined, {
          canonicalTaskIDs: new Map(), pendingWriteStager: stager, generation: 0,
        });
      });
      await failed;
      const original = staged[0]!;
      store.saveRemote(identity, board('immediate'), vi.fn(), vi.fn(), {
        canonicalTaskIDs: new Map(), pendingWriteStager: stager, generation: 0,
      });
      store.saveRemoteDebounced(identity, board('debounced'), vi.fn(), vi.fn(), {
        canonicalTaskIDs: new Map(), pendingWriteStager: stager, generation: 0,
      });
      store.flush();
      await vi.runAllTimersAsync();
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(staged).toEqual([original]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('resumes only after exact replay, current GET, and durable acknowledgement', async () => {
    const store = new RemoteStore();
    const sent = board('created');
    const latest = { ...sent, title: 'latest title', tasks: [...sent.tasks, newTask({ title: 'later' })] };
    const stages: Array<{ operation_id: string; body: string }> = [];
    const stager = {
      stagePendingBoardWrite: async (pending: { operation_id: string; body: string }) => {
        stages.push(pending);
        return { ok: true, generation: stages.length };
      },
    };
    const calls: Call[] = [];
    let call = 0;
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url, method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      call += 1;
      if (call === 1) return Promise.reject(new TypeError('connection reset'));
      if (call === 2) return Promise.resolve(new Response(JSON.stringify({ task_ids: ['server-created'] }), {
        status: 200, headers: { ETag: 'v1', 'Idempotency-Replayed': 'true' },
      }));
      if (call === 3) return Promise.resolve(remoteSnapshot(sent, ['server-created'], 'v2'));
      if (call === 4) return Promise.resolve(new Response(JSON.stringify({ task_ids: ['server-created', 'server-later'] }), {
        status: 200, headers: { ETag: 'v3' },
      }));
      throw new Error('unexpected fetch');
    }));
    await new Promise<void>((resolve) => {
      store.saveRemote(identity, sent, () => resolve(), undefined, {
        canonicalTaskIDs: new Map(), pendingWriteStager: stager, generation: 0,
      });
    });
    const original = stages[0]!;
    store.saveRemote(identity, latest, vi.fn(), vi.fn(), {
      canonicalTaskIDs: new Map(), pendingWriteStager: stager, generation: 1,
    });
    const pending = {
      operation_id: original.operation_id,
      body: original.body,
      sent_board: sent,
      sent_canonical_ids: {},
      if_match: null,
    };
    const recovered = await store.replayPendingBoardWrite(identity, pending, latest, new Map());
    expect(calls).toHaveLength(3);
    expect(() => store.resumeAfterPendingBoardWrite(
      { ...pending, operation_id: 'wrong-operation' }, recovered,
    )).toThrow('pending board write was not acknowledged');
    expect(calls).toHaveLength(3);
    store.resumeAfterPendingBoardWrite(pending, recovered);
    await vi.waitFor(() => expect(calls).toHaveLength(4));
    const resumed = JSON.parse(calls[3]!.body) as { board: string; task_ids: Array<string | null> };
    expect(parse(resumed.board).title).toBe('latest title');
    expect(titles(parse(resumed.board))).toEqual(['created', 'later']);
    expect(resumed.task_ids).toEqual(['server-created', null]);
    expect(stages[0]).toEqual(original);
    expect(stages[1]!.operation_id).not.toBe(original.operation_id);
  });

  it('preserves reauthentication failures without ambiguity wrapping', async () => {
    const store = new RemoteStore();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('expired', { status: 401 }))));
    const error = await new Promise<unknown>((resolve) => {
      store.saveRemote(identity, board('created'), resolve, undefined, {
        canonicalTaskIDs: new Map(),
        generation: 0,
        pendingWriteStager: { stagePendingBoardWrite: async () => ({ ok: true, generation: 1 }) },
      });
    });
    expect(error).toBeInstanceOf(ReauthRequiredError);
  });

  it('keeps queued work blocked when exact replay has an invalid acknowledgement', async () => {
    const store = new RemoteStore();
    const sent = board('created');
    let operationID = '';
    let call = 0;
    const fetchMock = vi.fn(() => {
      call += 1;
      if (call === 1) return Promise.reject(new TypeError('connection reset'));
      return Promise.resolve(new Response('{bad', { status: 200 }));
    });
    vi.stubGlobal('fetch', fetchMock);
    await new Promise<void>((resolve) => {
      store.saveRemote(identity, sent, () => resolve(), undefined, {
        canonicalTaskIDs: new Map(),
        generation: 0,
        pendingWriteStager: {
          stagePendingBoardWrite: async (pending) => {
            operationID = pending.operation_id;
            return { ok: true, generation: 1 };
          },
        },
      });
    });
    store.saveRemote(identity, board('later'), vi.fn(), vi.fn(), {
      canonicalTaskIDs: new Map(),
    });
    await expect(store.replayPendingBoardWrite(identity, {
      operation_id: operationID,
      body: JSON.stringify({ board: serialize(sent), task_ids: [null] }),
      sent_board: sent,
      sent_canonical_ids: {},
      if_match: null,
    }, sent, new Map())).rejects.toThrow('PUT /api/board returned invalid task ids');
    store.flush();
    await Promise.resolve();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it.each([
    ['transport rejection', () => Promise.reject(new TypeError('connection reset'))],
    ['invalid acknowledgement', () => Promise.resolve(new Response('{bad', { status: 200 }))],
    ['reauthentication', () => Promise.resolve(new Response('expired', { status: 401 }))],
  ])('blocks a fresh instance when pending replay ends in %s', async (_name, response) => {
    const store = new RemoteStore();
    const sent = board('created');
    const fetchMock = vi.fn(response);
    vi.stubGlobal('fetch', fetchMock);
    await expect(store.replayPendingBoardWrite(identity, {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
      body: JSON.stringify({ board: serialize(sent), task_ids: [null] }),
      sent_board: sent,
      sent_canonical_ids: {},
      if_match: null,
    }, sent, new Map())).rejects.toBeDefined();
    store.saveRemote(identity, board('later'), vi.fn(), vi.fn(), {
      canonicalTaskIDs: new Map(),
    });
    store.flush();
    await Promise.resolve();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('keeps every dispatch surface blocked when replay acknowledgement persistence fails', async () => {
    vi.useFakeTimers();
    try {
      const store = new RemoteStore();
      const sent = board('created');
      let resolveReplay!: (response: Response) => void;
      const calls: Call[] = [];
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
        calls.push({
          url, method: init?.method ?? 'GET',
          headers: (init?.headers ?? {}) as Record<string, string>,
          body: typeof init?.body === 'string' ? init.body : '',
        });
        if (calls.length === 1) {
          return new Promise<Response>((resolve) => { resolveReplay = resolve; });
        }
        return Promise.resolve(remoteSnapshot(sent, ['server-created'], 'v2'));
      }));
      const recovery = reconcilePendingBoardWrite({
        remote: store,
        identity,
        pendingWrite: {
          operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
          body: JSON.stringify({ board: serialize(sent), task_ids: [null] }),
          sent_board: sent,
          sent_canonical_ids: {},
          if_match: null,
        },
        readLive: () => liveSnapshot(sent, 1, {
          durableBase: durablePendingSnapshot(sent),
        }),
        readSnapshot: () => durablePendingSnapshot(sent),
        persistAcknowledgement: async () => ({ ok: false, error: new Error('quota') }),
        repairPersist: async () => ({ ok: false, error: new Error('quota') }),
        apply: vi.fn(),
        queuePush: vi.fn(),
        cancelled: () => false,
      });
      await vi.waitFor(() => expect(calls).toHaveLength(1));
      store.saveRemote(identity, board('immediate'), vi.fn(), vi.fn(), {
        canonicalTaskIDs: new Map(),
      });
      store.saveRemoteDebounced(identity, board('debounced'), vi.fn(), vi.fn(), {
        canonicalTaskIDs: new Map(),
      });
      store.flush();
      expect(calls).toHaveLength(1);
      resolveReplay(new Response(JSON.stringify({ task_ids: ['server-created'] }), {
        status: 200, headers: { ETag: 'v1', 'Idempotency-Replayed': 'true' },
      }));
      // The current GET is the only request allowed after replay starts.
      await vi.waitFor(() => expect(calls).toHaveLength(2));
      await recovery;
      store.saveRemote(identity, board('after failure'), vi.fn(), vi.fn(), {
        canonicalTaskIDs: new Map(),
      });
      store.saveRemoteDebounced(identity, board('after debounce'), vi.fn(), vi.fn(), {
        canonicalTaskIDs: new Map(),
      });
      store.flush();
      await vi.runAllTimersAsync();
      expect(calls).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });

  // Previously only PUTs acknowledged canonical ids, leaving a freshly loaded
  // card able to match itself until somebody edited and saved it once.
  it('maps a freshly loaded board to canonical ids before its first edit', async () => {
    const store = new RemoteStore();
    const serverBoard: Board = {
      title: 'kb',
      tasks: [
        newTask({ title: 'Duplicate', status: 'done' }),
        newTask({ title: 'Duplicate', status: 'todo' }),
        newTask({ title: 'Doing other', status: 'doing' }),
      ],
    };
    const canonical = ['todo-server', 'doing-server', 'done-server'];
    const calls = stubFetch([remoteSnapshot(serverBoard, canonical)]);
    let loadedIDs: ReadonlyMap<string, string> | undefined;

    const loaded = await store.loadRemote(identity, (taskIDs) => {
      loadedIDs = taskIDs;
    });

    expect(calls[0]!.headers.Accept).toBe('application/json');
    expect(loaded).not.toBeNull();
    expect(loadedIDs).toBeDefined();
    expect([...loadedIDs!]).toEqual(
      wireTasks(loaded!).map((task, index) => [task.id, canonical[index]!]),
    );
  });

  // A corrupt positional map could suppress an unrelated card, which is worse
  // than leaving a duplicate advisory visible.
  it.each([
    ['invalid JSON', '{'],
    ['a missing board', '{"task_ids":[]}'],
    ['a non-string board', '{"board":7,"task_ids":[]}'],
    [
      'a task-id count mismatch',
      JSON.stringify({ board: serialize(board('A')), task_ids: [] }),
    ],
    [
      'duplicate canonical ids',
      JSON.stringify({
        board: serialize(board('A', 'B')),
        task_ids: ['server-a', 'server-a'],
      }),
    ],
  ])('rejects a JSON board snapshot with %s', async (_case, bodyText) => {
    const store = new RemoteStore();
    stubFetch([
      new Response(bodyText, {
        headers: { 'Content-Type': 'application/json' },
      }),
    ]);

    await expect(store.loadRemote(identity)).rejects.toThrow(
      'GET /api/board returned invalid board snapshot',
    );
  });

  it('sends the version it last read back as If-Match', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response(null, { status: 204, headers: { ETag: 'v2' } }),
    ]);
    await store.loadRemote(identity);
    await save(store, board('A', 'C'));
    expect(calls[1]!.method).toBe('PUT');
    expect(calls[1]!.headers.Accept).toBe('application/json');
    expect(calls[1]!.headers['If-Match']).toBe('v1');
  });

  it('omits If-Match when no version is known yet', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([new Response(null, { status: 204 })]);
    await save(store, board('A'));
    expect(calls[0]!.headers['If-Match']).toBeUndefined();
  });

  it('keeps the version a 404 carried, so the first write is still conditional', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response('no board saved', { status: 404, headers: { ETag: 'v0' } }),
      new Response('conflict', { status: 409 }),
      // Another surface created the board between our GET and our PUT.
      new Response(serialize(board('from the CLI')), { headers: { ETag: 'v1' } }),
      new Response(null, { status: 204, headers: { ETag: 'v2' } }),
    ]);
    expect(await store.loadRemote(identity)).toBeNull();
    const pushed = await save(store, board('demo card'));
    expect(calls[1]!.headers['If-Match']).toBe('v0');
    // The unseen task survived instead of being replaced by our seed board.
    expect(titles(pushed)).toEqual(['demo card', 'from the CLI']);
  });

  it('preserves fetched canonical identity when an empty-base JSON save conflicts', async () => {
    const store = new RemoteStore();
    const local = board('Duplicate');
    const external = board('Duplicate');
    const calls = stubFetch([
      new Response('no board saved', { status: 404, headers: { ETag: 'v0' } }),
      new Response('conflict', { status: 409, headers: { ETag: 'v1' } }),
      remoteSnapshot(external, ['server-external'], 'v1'),
      new Response(
        JSON.stringify({ task_ids: ['server-local', 'server-external'] }),
        { status: 200, headers: { ETag: 'v2' } },
      ),
    ]);
    expect(await store.loadRemote(identity)).toBeNull();

    const saved = await new Promise<SaveResult>((resolve, reject) => {
      store.saveRemote(
        identity,
        local,
        reject,
        ({ pushed, taskIDs }) => resolve({ pushed, taskIDs }),
        { canonicalTaskIDs: new Map() },
      );
    });

    const first = JSON.parse(calls[1]!.body) as {
      board: string;
      task_ids: Array<string | null>;
    };
    expect(first.task_ids).toEqual([null]);
    const retry = JSON.parse(calls[3]!.body) as {
      board: string;
      task_ids: Array<string | null>;
    };
    expect(titles(parse(retry.board))).toEqual(['Duplicate', 'Duplicate']);
    expect(retry.task_ids).toEqual([null, 'server-external']);
    expect(calls[3]!.headers['If-Match']).toBe('v1');
    expect(saved.pushed.tasks).toHaveLength(2);
    expect(saved.taskIDs.get(saved.pushed.tasks[0]!.id)).toBe('server-local');
    expect(saved.taskIDs.get(saved.pushed.tasks[1]!.id)).toBe('server-external');
  });

  it('refetches, merges and retries once on 409 without dropping an unseen task', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response('conflict', { status: 409 }),
      // Another client added B in the meantime.
      new Response(serialize(board('A', 'B')), { headers: { ETag: 'v2' } }),
      new Response(null, { status: 204, headers: { ETag: 'v3' } }),
    ]);
    await store.loadRemote(identity);
    const pushed = await save(store, board('A', 'C'));

    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'GET /api/board',
      'PUT /api/board',
      'GET /api/board',
      'PUT /api/board',
    ]);
    // The retry carries the version it merged against …
    expect(calls[3]!.headers['If-Match']).toBe('v2');
    // … and the body keeps the local edit and the task we had never seen.
    expect(titles(parse(calls[3]!.body))).toEqual(['A', 'C', 'B']);
    expect(titles(pushed)).toEqual(['A', 'C', 'B']);
  });

  it('retries only once — a second conflict is reported', async () => {
    const store = new RemoteStore();
    stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response('conflict', { status: 409 }),
      new Response(serialize(board('A', 'B')), { headers: { ETag: 'v2' } }),
      new Response('conflict', { status: 409 }),
    ]);
    await store.loadRemote(identity);
    await expect(save(store, board('A', 'C'))).rejects.toThrow('409');
  });

  it('pushes the local board when the server lost its copy mid-conflict', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response('conflict', { status: 409 }),
      new Response('', { status: 404 }),
      new Response(null, { status: 204 }),
    ]);
    await store.loadRemote(identity);
    const pushed = await save(store, board('A', 'C'));
    expect(titles(pushed)).toEqual(['A', 'C']);
    expect(calls[3]!.headers['If-Match']).toBeUndefined();
  });

  it('adopts the version echoed by a successful PUT', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(null, { status: 204, headers: { ETag: 'v9' } }),
      new Response(null, { status: 204 }),
    ]);
    await save(store, board('A'));
    await save(store, board('A', 'B'));
    expect(calls[1]!.headers['If-Match']).toBe('v9');
  });

  it('maps duplicate-title client ids to canonical ids by wire position', async () => {
    const store = new RemoteStore();
    const pushed: Board = {
      title: 'kb',
      tasks: [
        newTask({ id: 'done-client', title: 'Duplicate', status: 'done' }),
        newTask({ id: 'todo-client-1', title: 'Duplicate', status: 'todo' }),
        newTask({ id: 'doing-client', title: 'Other', status: 'doing' }),
        newTask({ id: 'todo-client-2', title: 'Duplicate', status: 'todo' }),
      ],
    };
    const canonical = [
      'todo-server-1',
      'todo-server-2',
      'doing-server',
      'done-server',
    ];
    stubFetch([
      new Response(JSON.stringify({ task_ids: canonical }), {
        status: 200,
        headers: { ETag: 'v1' },
      }),
    ]);

    const saved = await saveWithTaskIDs(store, pushed);

    expect(saved.pushed).toBe(pushed);
    expect(saved.pushed.tasks.map((t) => t.id)).toEqual([
      'done-client',
      'todo-client-1',
      'doing-client',
      'todo-client-2',
    ]);
    expect([...saved.taskIDs]).toEqual(
      wireTasks(pushed).map((task, i) => [task.id, canonical[i]!]),
    );
  });

  it('accepts a legacy 204 response with an empty task-id map', async () => {
    const store = new RemoteStore();
    stubFetch([new Response(null, { status: 204 })]);

    const saved = await saveWithTaskIDs(store, board('A'));

    expect([...saved.taskIDs]).toEqual([]);
  });

  it.each([
    ['invalid JSON', '{'],
    ['a null body', 'null'],
    ['an array body', '[]'],
    ['a missing task_ids field', '{}'],
    ['a non-array task_ids field', '{"task_ids":"server-a"}'],
    ['too few task ids', '{"task_ids":["server-a"]}'],
    [
      'too many task ids',
      '{"task_ids":["server-a","server-b","server-c"]}',
    ],
    ['a non-string task id', '{"task_ids":["server-a",42]}'],
    ['an empty task id', '{"task_ids":["server-a",""]}'],
    ['a whitespace task id', '{"task_ids":["server-a","   "]}'],
    [
      'duplicate task ids',
      '{"task_ids":["server-a","server-a"]}',
    ],
  ])('rejects negotiated 200 JSON with %s', async (_case, bodyText) => {
    const store = new RemoteStore();
    stubFetch([new Response(bodyText, { status: 200 })]);

    await expect(saveWithTaskIDs(store, board('A', 'B'))).rejects.toThrow(
      'PUT /api/board returned invalid task ids',
    );
  });

  it('rejects an unexpected successful PUT status', async () => {
    const store = new RemoteStore();
    stubFetch([new Response(null, { status: 202 })]);

    await expect(saveWithTaskIDs(store, board('A'))).rejects.toThrow(
      'PUT /api/board failed: 202',
    );
  });

  it('maps a 409 retry against the merged board that was actually pushed', async () => {
    const store = new RemoteStore();
    stubFetch([
      remoteSnapshot(board('A'), ['server-a'], 'v1'),
      new Response('conflict', { status: 409 }),
      remoteSnapshot(board('A', 'B'), ['server-a', 'server-b'], 'v2'),
      new Response(
        JSON.stringify({
          task_ids: ['server-a', 'server-c', 'server-b'],
        }),
        { status: 200, headers: { ETag: 'v3' } },
      ),
    ]);
    await store.loadRemote(identity);

    const saved = await saveWithTaskIDs(store, board('A', 'C'));

    expect(titles(saved.pushed)).toEqual(['A', 'C', 'B']);
    expect([...saved.taskIDs]).toEqual(
      wireTasks(saved.pushed).map((task, i) => [
        task.id,
        ['server-a', 'server-c', 'server-b'][i]!,
      ]),
    );
  });

  it('sends mapped saves as JSON with canonical ids and null for new tasks', async () => {
    const store = new RemoteStore();
    const pushed = board('known', 'new');
    const ids = new Map([[pushed.tasks[0]!.id, 'server-known']]);
    const calls = stubFetch([
      new Response(JSON.stringify({ task_ids: ['server-known', 'server-new'] }), {
        status: 200,
        headers: { ETag: 'v2' },
      }),
    ]);

    await new Promise<void>((resolve, reject) => {
      store.saveRemote(identity, pushed, reject, () => resolve(), {
        canonicalTaskIDs: ids,
      });
    });

    expect(calls[0]!.headers['Content-Type']).toBe('application/json');
    expect(JSON.parse(calls[0]!.body)).toEqual({
      board: serialize(pushed),
      task_ids: ['server-known', null],
    });
  });

  it('sends a fully mapped JSON save without staging or idempotency and reports the exact acknowledgement', async () => {
    const store = new RemoteStore();
    const mapped: Board = {
      title: 'Mapped',
      tasks: [
        { ...fixtureTask('z-done', 'Done Z'), status: 'done' },
        fixtureTask('a-todo', 'Todo A'),
        { ...fixtureTask('m-doing', 'Doing M'), status: 'doing' },
      ],
    };
    const canonicalTaskIDs = new Map([
      ['z-done', 'canonical-z'],
      ['a-todo', 'canonical-a'],
      ['m-doing', 'canonical-m'],
    ]);
    const wireTaskIDs = ['canonical-a', 'canonical-m', 'canonical-z'];
    const wireBoard = '# Mapped\n\n## To Do\n\n' +
      '- [ ] Todo A\n  description:a-todo\n\n' +
      '## Doing\n\n- [ ] Doing M\n  description:m-doing\n\n' +
      '## Done\n\n- [x] Done Z\n  description:z-done\n';
    const exactBody = JSON.stringify({ board: wireBoard, task_ids: wireTaskIDs });
    const durable = {
      ...durableSnapshot(mapped, 7),
      canonicalTaskIDs,
      deletedCanonicalIDs: new Set(['canonical-deleted']),
    };
    const stagePendingBoardWrite = vi.fn();
    const calls = stubFetch([
      remoteSnapshot(mapped, wireTaskIDs, '"etag-base"'),
      new Response(JSON.stringify({ task_ids: wireTaskIDs }), {
        status: 200,
        headers: { 'Content-Type': 'application/json', ETag: '"etag-next"' },
      }),
      new Response(JSON.stringify({ task_ids: wireTaskIDs }), {
        status: 200,
        headers: { 'Content-Type': 'application/json', ETag: '"etag-final"' },
      }),
    ]);
    await store.loadRemote(identity);
    const callback = vi.fn<CharacterizationCallback>();

    const acknowledgement = await captureAcknowledgement((onSuccess, onError) => {
      store.saveRemote(
        identity,
        mapped,
        onError,
        onSuccess,
        {
          canonicalTaskIDs,
          generation: 7,
          durableVersion: durable.version,
          durableSnapshot: durable,
          pendingWriteStager: { stagePendingBoardWrite },
        },
      );
    }, callback);

    expect(calls[1]).toEqual({
      url: '/api/board',
      method: 'PUT',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'If-Match': '"etag-base"',
        'X-KB-User': 'alice',
      },
      body: exactBody,
    });
    expect(calls[1]!.headers['Idempotency-Key']).toBeUndefined();
    expect(stagePendingBoardWrite).not.toHaveBeenCalled();
    expect(callback).toHaveBeenCalledOnce();
    expect(normalizedAcknowledgement(acknowledgement)).toEqual({
      pushed: mapped,
      taskIDs: [
        ['z-done', 'canonical-z'],
        ['a-todo', 'canonical-a'],
        ['m-doing', 'canonical-m'],
      ],
      conflicts: [],
      operationID: undefined,
      isCurrent: true,
      generation: 7,
      durableVersion: { present: true, generation: 7 },
      durableSnapshot: durable,
    });

    await new Promise<void>((resolve, reject) => {
      store.saveRemote(
        identity,
        mapped,
        reject,
        () => resolve(),
        { canonicalTaskIDs },
      );
    });
    expect(calls[2]!.headers['If-Match']).toBe('"etag-next"');
    expect(calls[2]!.body).toBe(exactBody);
    expect(wireTasks(mapped).map((item) => item.id)).toEqual([
      'a-todo', 'm-doing', 'z-done',
    ]);
  });

  it('delivers the exact named acknowledgement after the debounce interval', async () => {
    vi.useFakeTimers();
    try {
      const store = new RemoteStore();
      const mapped: Board = {
        title: 'Mapped',
        tasks: [fixtureTask('mapped-a', 'Mapped A')],
      };
      const canonicalTaskIDs = new Map([['mapped-a', 'canonical-a']]);
      const durable = { ...durableSnapshot(mapped, 7), canonicalTaskIDs };
      const calls = stubFetch([
        new Response(JSON.stringify({ task_ids: ['canonical-a'] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json', ETag: '"etag-next"' },
        }),
      ]);
      const completed = captureAcknowledgement((onSuccess, onError) => {
        store.saveRemoteDebounced(
          identity,
          mapped,
          onError,
          onSuccess,
          {
            canonicalTaskIDs,
            generation: 7,
            durableVersion: durable.version,
            durableSnapshot: durable,
          },
        );
      });

      expect(calls).toEqual([]);
      await vi.advanceTimersByTimeAsync(799);
      expect(calls).toEqual([]);
      await vi.advanceTimersByTimeAsync(1);
      await expect(completed.then(normalizedAcknowledgement)).resolves.toEqual({
        pushed: mapped,
        taskIDs: [['mapped-a', 'canonical-a']],
        conflicts: [],
        operationID: undefined,
        isCurrent: true,
        generation: 7,
        durableVersion: { present: true, generation: 7 },
        durableSnapshot: durable,
      });
      expect(calls).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('delivers the exact named acknowledgement after one board-title conflict retry', async () => {
    const store = new RemoteStore();
    const base: Board = {
      title: 'Mapped',
      tasks: [fixtureTask('base-a', 'Mapped A', 'description:mapped-a')],
    };
    const local: Board = {
      title: 'Local title',
      tasks: [fixtureTask('mapped-a', 'Mapped A')],
    };
    const remote: Board = {
      title: 'Remote title',
      tasks: [fixtureTask('server-a', 'Mapped A', 'description:server-a')],
    };
    const canonicalTaskIDs = new Map([['mapped-a', 'canonical-a']]);
    const durable = {
      ...durableSnapshot(local, 7),
      canonicalTaskIDs,
      deletedCanonicalIDs: new Set(['canonical-deleted']),
    };
    const calls = stubFetch([
      remoteSnapshot(base, ['canonical-a'], '"etag-base"'),
      new Response('conflict', { status: 409 }),
      remoteSnapshot(remote, ['canonical-a'], '"etag-conflict"'),
      new Response(JSON.stringify({ task_ids: ['canonical-a'] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json', ETag: '"etag-next"' },
      }),
    ]);
    let loadedIDs = new Map<string, string>();
    await store.loadRemote(identity, (ids) => { loadedIDs = new Map(ids); });
    const localIDs = new Map([[local.tasks[0]!.id, loadedIDs.values().next().value!]]);
    const callback = vi.fn<CharacterizationCallback>();

    const acknowledgement = await captureAcknowledgement((onSuccess, onError) => {
      store.saveRemote(
        identity,
        local,
        onError,
        onSuccess,
        {
          canonicalTaskIDs: localIDs,
          generation: 7,
          durableVersion: durable.version,
          durableSnapshot: durable,
        },
      );
    }, callback);

    const pushed: Board = {
      title: 'Local title',
      tasks: [fixtureTask('mapped-a', 'Mapped A', 'description:server-a')],
    };
    const initialBody = JSON.stringify({
      board: serialize(local),
      task_ids: ['canonical-a'],
    });
    const retryBody = JSON.stringify({
      board: serialize(pushed),
      task_ids: ['canonical-a'],
    });
    expect(calls.map(({ url, method, body }) => ({ url, method, body }))).toEqual([
      { url: '/api/board', method: 'GET', body: '' },
      { url: '/api/board', method: 'PUT', body: initialBody },
      { url: '/api/board', method: 'GET', body: '' },
      { url: '/api/board', method: 'PUT', body: retryBody },
    ]);
    expect(calls[1]!.headers['If-Match']).toBe('"etag-base"');
    expect(calls[3]!.headers['If-Match']).toBe('"etag-conflict"');
    expect(callback).toHaveBeenCalledOnce();
    expect(callback.mock.calls[0]?.[0].conflicts).toEqual(['board title']);
    expect(normalizedAcknowledgement(acknowledgement)).toEqual({
      pushed,
      taskIDs: [['mapped-a', 'canonical-a']],
      conflicts: ['board title'],
      operationID: undefined,
      isCurrent: true,
      generation: 7,
      durableVersion: { present: true, generation: 7 },
      durableSnapshot: durable,
    });
  });

  it('keeps the acknowledgement currentness guard live after delivery', async () => {
    const store = new RemoteStore();
    const mapped: Board = {
      title: 'Mapped',
      tasks: [fixtureTask('mapped-a', 'Mapped A')],
    };
    const canonicalTaskIDs = new Map([['mapped-a', 'canonical-a']]);
    const durable = { ...durableSnapshot(mapped, 7), canonicalTaskIDs };
    stubFetch([
      new Response(JSON.stringify({ task_ids: ['canonical-a'] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json', ETag: '"etag-next"' },
      }),
    ]);

    const acknowledgement = await captureAcknowledgement((onSuccess, onError) => {
      store.saveRemote(
        identity,
        mapped,
        onError,
        onSuccess,
        {
          canonicalTaskIDs,
          generation: 7,
          durableVersion: durable.version,
          durableSnapshot: durable,
        },
      );
    });

    expect(normalizedAcknowledgement(acknowledgement)).toEqual({
      pushed: mapped,
      taskIDs: [['mapped-a', 'canonical-a']],
      conflicts: [],
      operationID: undefined,
      isCurrent: true,
      generation: 7,
      durableVersion: { present: true, generation: 7 },
      durableSnapshot: durable,
    });
    store.cancel();
    expect(acknowledgement.isCurrent?.()).toBe(false);
  });

  it('delivers absent optional acknowledgement fields for a legacy save', async () => {
    const store = new RemoteStore();
    const legacy: Board = {
      title: 'Mapped',
      tasks: [fixtureTask('mapped-a', 'Mapped A')],
    };
    stubFetch([new Response(null, { status: 204 })]);

    const acknowledgement = await captureAcknowledgement((onSuccess, onError) => {
      store.saveRemote(
        identity,
        legacy,
        onError,
        onSuccess,
      );
    });
    expect(normalizedAcknowledgement(acknowledgement)).toEqual({
      pushed: legacy,
      taskIDs: [],
      conflicts: [],
      isCurrent: true,
      operationID: undefined,
      generation: undefined,
      durableVersion: undefined,
      durableSnapshot: undefined,
    });
  });

  it('keeps one request active and dispatches the latest queued board afterward', async () => {
    const store = new RemoteStore();
    const first = board('A');
    const latest = board('A', 'B');
    let resolveFirst!: (response: Response) => void;
    const calls: Call[] = [];
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url,
        method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      if (calls.length === 1) {
        return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      }
      return Promise.resolve(new Response(null, { status: 204, headers: { ETag: 'v2' } }));
    }));

    const completed: string[] = [];
    store.saveRemote(identity, first, undefined, () => completed.push('A'));
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    store.saveRemote(identity, latest, undefined, () => completed.push('B'));
    expect(calls).toHaveLength(1);

    resolveFirst(new Response(null, { status: 204, headers: { ETag: 'v1' } }));
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    await vi.waitFor(() => expect(completed).toEqual(['A', 'B']));
    expect(calls[1]!.headers['If-Match']).toBe('v1');
    expect(titles(parse(calls[1]!.body))).toEqual(['A', 'B']);
  });

  it('rebases the queued latest edit over the active save conflict result', async () => {
    const store = new RemoteStore();
    const initial = board('A');
    let initialIDs: ReadonlyMap<string, string> = new Map();
    let resolveActive!: (response: Response) => void;
    const calls: Call[] = [];
    const serverWithExternal = board('A', 'external');
    const responses: Array<Response | 'deferred'> = [
      remoteSnapshot(initial, ['server-a'], 'v1'),
      'deferred',
      remoteSnapshot(serverWithExternal, ['server-a', 'server-external'], 'v2'),
      new Response(JSON.stringify({ task_ids: ['server-a', 'server-external'] }), {
        status: 200,
        headers: { ETag: 'v3' },
      }),
      new Response(
        JSON.stringify({ task_ids: ['server-a', 'server-b', 'server-external'] }),
        { status: 200, headers: { ETag: 'v4' } },
      ),
    ];
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url,
        method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      const response = responses.shift();
      if (response === 'deferred') {
        return new Promise<Response>((resolve) => { resolveActive = resolve; });
      }
      if (!response) throw new Error('unexpected fetch');
      return Promise.resolve(response);
    }));

    const loaded = await store.loadRemote(identity, (ids) => { initialIDs = ids; });
    const active = {
      ...loaded!,
      tasks: [{ ...loaded!.tasks[0]!, desc: 'active edit' }],
    };
    const latest = {
      ...active,
      tasks: [...active.tasks, newTask({ title: 'B' })],
    };
    const latestDone = new Promise<void>((resolve, reject) => {
      store.saveRemote(identity, active, reject, undefined, {
        canonicalTaskIDs: initialIDs,
      });
      store.saveRemote(identity, latest, reject, () => resolve(), {
        canonicalTaskIDs: initialIDs,
      });
    });
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    resolveActive(new Response('conflict', { status: 409 }));
    await latestDone;

    expect(calls.map((call) => call.method)).toEqual(['GET', 'PUT', 'GET', 'PUT', 'PUT']);
    const finalPayload = JSON.parse(calls[4]!.body) as {
      board: string;
      task_ids: Array<string | null>;
    };
    expect(titles(parse(finalPayload.board))).toEqual(['A', 'B', 'external']);
    expect(finalPayload.task_ids).toEqual(['server-a', null, 'server-external']);
    expect(calls[4]!.headers['If-Match']).toBe('v3');
  });

  it('keeps the learned conflict rebase when the active retry returns 500', async () => {
    const store = new RemoteStore();
    const initial = board('A');
    let initialIDs: ReadonlyMap<string, string> = new Map();
    let resolveActive!: (response: Response) => void;
    const external = board('A', 'external');
    const calls: Call[] = [];
    const responses: Array<Response | 'deferred'> = [
      remoteSnapshot(initial, ['server-a'], 'v1'),
      'deferred',
      remoteSnapshot(external, ['server-a', 'server-external'], 'v2'),
      new Response('failed before commit', { status: 500 }),
      new Response(JSON.stringify({ task_ids: ['server-a', 'server-b', 'server-external'] }), {
        status: 200, headers: { ETag: 'v3' },
      }),
    ];
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url, method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      const response = responses.shift();
      if (response === 'deferred') return new Promise<Response>((resolve) => { resolveActive = resolve; });
      if (!response) throw new Error('unexpected fetch');
      return Promise.resolve(response);
    }));
    const loaded = await store.loadRemote(identity, (ids) => { initialIDs = ids; });
    const active = { ...loaded!, tasks: [{ ...loaded!.tasks[0]!, desc: 'active' }] };
    const latest = { ...active, tasks: [...active.tasks, newTask({ title: 'B' })] };
    const activeError = vi.fn();
    const latestDone = new Promise<void>((resolve, reject) => {
      store.saveRemote(identity, active, activeError, undefined, { canonicalTaskIDs: initialIDs });
      store.saveRemote(identity, latest, reject, () => resolve(), { canonicalTaskIDs: initialIDs });
    });
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    resolveActive(new Response('conflict', { status: 409 }));
    await latestDone;
    expect(activeError).toHaveBeenCalledOnce();
    const queued = JSON.parse(calls[4]!.body) as { board: string; task_ids: Array<string | null> };
    expect(titles(parse(queued.board))).toEqual(['A', 'B', 'external']);
    expect(queued.task_ids).toEqual(['server-a', null, 'server-external']);
    expect(calls[4]!.headers['If-Match']).toBe('v2');
  });

  it('serializes a startup edit merged with an unseen remote task', async () => {
    const store = new RemoteStore();
    const before = board('A');
    const beforeIDs = new Map([[before.tasks[0]!.id, 'server-a']]);
    const current = { ...before, tasks: [...before.tasks, newTask({ title: 'local' })] };
    const server = board('A', 'external');
    const calls = stubFetch([
      remoteSnapshot(server, ['server-a', 'server-external'], 'v2'),
      new Response(JSON.stringify({ task_ids: ['server-a', 'server-local', 'server-external'] }), {
        status: 200, headers: { ETag: 'v3' },
      }),
    ]);
    let serverIDs: ReadonlyMap<string, string> = new Map();
    const loaded = await store.loadRemote(identity, (ids) => { serverIDs = ids; });
    const merged = mergeStartupEdit(before, beforeIDs, current, beforeIDs, loaded!, serverIDs);
    await new Promise<void>((resolve, reject) => {
      store.saveRemote(identity, merged.board, reject, () => resolve(), { canonicalTaskIDs: merged.canonicalTaskIDs });
    });
    const payload = JSON.parse(calls[1]!.body) as { board: string; task_ids: Array<string | null> };
    expect(titles(parse(payload.board))).toEqual(['A', 'local', 'external']);
    expect(payload.task_ids).toEqual(['server-a', null, 'server-external']);
  });

  it('three-way merges a title-only startup edit with empty identity maps', () => {
    const before = { title: 'base', tasks: [] };
    const current = { title: 'local', tasks: [] };
    const server = { title: 'remote', tasks: [] };
    const merged = mergeStartupEdit(before, new Map(), current, new Map(), server, new Map());
    expect(merged.board.title).toBe('local');
    expect(merged.conflicts).toEqual(['board title']);
  });

  it('persists a deferred title-only startup edit before its first conditional PUT', async () => {
    const values = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      key: (index: number) => [...values.keys()][index] ?? null,
      removeItem: (key: string) => { values.delete(key); },
      setItem: (key: string, value: string) => { values.set(key, value); },
    });
    const remote = new RemoteStore();
    const local = new LocalStore('startup-title-order');
    const before = { title: 'base', tasks: [] };
    await local.save(before, new Map());
    let current = before;
    let currentEpoch = 0;
    let resolveGet!: (response: Response) => void;
    const calls: Call[] = [];
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url, method: init?.method ?? 'GET', headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      if (calls.length === 1) {
        return new Promise<Response>((resolve) => { resolveGet = resolve; });
      }
      const persisted = new LocalStore('startup-title-order').loadOrSeed();
      expect(persisted.board.title).toBe('local title');
      return Promise.resolve(new Response(JSON.stringify({ task_ids: [] }), {
        status: 200, headers: { ETag: 'v3' },
      }));
    }));
    const startup = reconcileStartupBoardFetch({
      remote,
      identity,
      readLive: () => liveSnapshot(current, 1, {
        epoch: currentEpoch,
        durableBase: local.loadSnapshot(),
      }),
      readSnapshot: () => local.loadSnapshot(),
      persist: (next, ids, deleted, version, guard) =>
        local.saveIfGeneration(next, ids, deleted, version, guard),
      apply: vi.fn(),
      push: (snapshot) => remote.saveRemote(identity, snapshot.board, vi.fn(), vi.fn(), {
        canonicalTaskIDs: snapshot.canonicalTaskIDs,
        pendingWriteStager: local,
        durableVersion: snapshot.version,
        durableSnapshot: snapshot,
      }),
      cancelled: () => false,
    });
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    current = { title: 'local title', tasks: [] };
    currentEpoch = 1;
    resolveGet(remoteSnapshot({ title: 'remote title', tasks: [] }, [], 'v2'));
    const result = await startup;
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    expect(result.merged?.board.title).toBe('local title');
    expect(calls[1]!.headers['If-Match']).toBe('v2');
    expect(parse((JSON.parse(calls[1]!.body) as { board: string }).board).title).toBe('local title');
  });

  it('does not persist or push a startup edit after cancellation', async () => {
    const remote = new RemoteStore();
    const before = { title: 'base', tasks: [] };
    let current = before;
    let cancelled = false;
    let resolveGet!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { resolveGet = resolve; })));
    const persist = vi.fn(async () => persistenceSuccess(current, 1));
    const push = vi.fn();
    const startup = reconcileStartupBoardFetch({
      remote, identity,
      readLive: () => liveSnapshot(current, 0, { epoch: current === before ? 0 : 1 }),
      readSnapshot: () => durableSnapshot(before),
      persist, apply: vi.fn(), push, cancelled: () => cancelled,
    });
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    current = { title: 'local title', tasks: [] };
    cancelled = true;
    resolveGet(remoteSnapshot({ title: 'remote title', tasks: [] }, [], 'v2'));
    await startup;
    expect(persist).not.toHaveBeenCalled();
    expect(push).not.toHaveBeenCalled();
  });

  it('retains a local title in a zero-task 409 merge and retries with the fresh ETag', async () => {
    const store = new RemoteStore();
    let ids: ReadonlyMap<string, string> = new Map();
    const base = { title: 'base', tasks: [] };
    const calls = stubFetch([
      remoteSnapshot(base, [], 'v1'),
      new Response('conflict', { status: 409 }),
      remoteSnapshot({ title: 'remote title', tasks: [] }, [], 'v2'),
      new Response(JSON.stringify({ task_ids: [] }), { status: 200, headers: { ETag: 'v3' } }),
    ]);
    await store.loadRemote(identity, (loaded) => { ids = loaded; });
    await new Promise<void>((resolve, reject) => {
      store.saveRemote(identity, { title: 'local title', tasks: [] }, reject, () => resolve(), {
        canonicalTaskIDs: ids,
      });
    });
    expect(calls[3]!.headers['If-Match']).toBe('v2');
    const retry = JSON.parse(calls[3]!.body) as { board: string; task_ids: unknown[] };
    expect(parse(retry.board).title).toBe('local title');
    expect(retry.task_ids).toEqual([]);
  });

  it('lets the canonical 409 merge own board-title conflict reporting exactly once', async () => {
    const store = new RemoteStore();
    const base = board('A');
    base.title = 'base title';
    const remote = {
      ...base,
      title: 'remote title',
      tasks: [{ ...base.tasks[0]!, blocked: true }],
    };
    let ids: ReadonlyMap<string, string> = new Map();
    const calls = stubFetch([
      remoteSnapshot(base, ['server-a'], 'v1'),
      new Response('conflict', { status: 409 }),
      remoteSnapshot(remote, ['server-a'], 'v2'),
      new Response(JSON.stringify({ task_ids: ['server-a'] }), {
        status: 200,
        headers: { ETag: 'v3' },
      }),
    ]);
    const loaded = await store.loadRemote(identity, (loadedIDs) => { ids = loadedIDs; });
    const local = {
      ...loaded!,
      title: 'local title',
      tasks: [{ ...loaded!.tasks[0]!, desc: 'local description' }],
    };

    const saved = await new Promise<{
      pushed: Board;
      ids: ReadonlyMap<string, string>;
      conflicts: readonly string[];
    }>((resolve, reject) => {
      store.saveRemote(identity, local, reject, ({ pushed, taskIDs, conflicts = [] }) => {
        resolve({ pushed, ids: taskIDs, conflicts });
      }, { canonicalTaskIDs: ids });
    });

    expect(saved.pushed).toMatchObject({
      title: 'local title',
      tasks: [{ title: 'A', desc: 'local description', blocked: true }],
    });
    expect([...saved.ids]).toEqual([[loaded!.tasks[0]!.id, 'server-a']]);
    expect(saved.conflicts.filter((conflict) => conflict === 'board title')).toHaveLength(1);
    const retry = JSON.parse(calls[3]!.body) as { board: string; task_ids: string[] };
    expect(parse(retry.board).title).toBe('local title');
    expect(parse(retry.board).tasks[0]).toMatchObject({
      desc: 'local description',
      blocked: true,
    });
    expect(retry.task_ids).toEqual(['server-a']);
  });

  it('retains local title and a concurrently created remote board after an initial 404', async () => {
    const store = new RemoteStore();
    const local = { title: 'local title', tasks: [] };
    const remote = board('remote task');
    remote.title = 'remote title';
    const calls = stubFetch([
      new Response('missing', { status: 404, headers: { ETag: 'v0' } }),
      new Response('conflict', { status: 409 }),
      remoteSnapshot(remote, ['server-remote'], 'v2'),
      new Response(JSON.stringify({ task_ids: ['server-remote'] }), {
        status: 200, headers: { ETag: 'v3' },
      }),
    ]);
    await store.loadRemote(identity);
    const saved = await new Promise<{ pushed: Board; conflicts: readonly string[] }>((resolve, reject) => {
      store.saveRemote(identity, local, reject, ({ pushed, conflicts = [] }) => {
        resolve({ pushed, conflicts });
      }, { canonicalTaskIDs: new Map() });
    });
    expect(saved.pushed.title).toBe('local title');
    expect(titles(saved.pushed)).toEqual(['remote task']);
    expect(saved.conflicts).toEqual(['board title']);
    expect(calls[3]!.headers['If-Match']).toBe('v2');
    const retry = JSON.parse(calls[3]!.body) as { board: string; task_ids: string[] };
    expect(parse(retry.board).title).toBe('local title');
    expect(titles(parse(retry.board))).toEqual(['remote task']);
    expect(retry.task_ids).toEqual(['server-remote']);
  });

  it('replays the exact pending create then merges the current server snapshot', async () => {
    const store = new RemoteStore();
    const sent = board('A', 'created');
    const current = { ...sent, tasks: [...sent.tasks, newTask({ title: 'later local' })] };
    const body = JSON.stringify({
      board: serialize(sent),
      task_ids: ['server-a', null],
    });
    const calls = stubFetch([
      new Response(JSON.stringify({ task_ids: ['server-a', 'server-created'] }), {
        status: 200,
        headers: { ETag: 'v2', 'Idempotency-Replayed': 'true' },
      }),
      remoteSnapshot(board('A', 'created', 'external'), ['server-a', 'server-created', 'server-external'], 'v3'),
    ]);
    const recovered = await store.replayPendingBoardWrite(identity, {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
      body,
      sent_board: sent,
      sent_canonical_ids: { [sent.tasks[0]!.id]: 'server-a' },
      if_match: '"r1"',
    }, current, new Map([[sent.tasks[0]!.id, 'server-a']]));
    expect(calls[0]!.body).toBe(body);
    expect(calls[0]!.headers['If-Match']).toBe('"r1"');
    expect(calls[0]!.headers['Idempotency-Key']).toBe('6fa459ea-ee8a-3ca4-894e-db77e160355e');
    expect(titles(recovered.board)).toEqual(['A', 'created', 'later local', 'external']);
    expect(recovered.needsPush).toBe(true);
  });

  it.each(['replay PUT', 'current GET'] as const)(
    'preserves an edit committed while the pending %s is in flight',
    async (deferredRequest) => {
      const store = new RemoteStore();
      const sent = board('created');
      let current = sent;
      let currentGeneration = 1;
      let currentEpoch = 0;
      let acknowledgedGeneration = -1;
      let resolveDeferred!: (response: Response) => void;
      let persisted: Board | null = null;
      const calls: Call[] = [];
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
        calls.push({
          url, method: init?.method ?? 'GET',
          headers: (init?.headers ?? {}) as Record<string, string>,
          body: typeof init?.body === 'string' ? init.body : '',
        });
        if (calls.length === 1) {
          if (deferredRequest === 'replay PUT') {
            return new Promise<Response>((resolve) => { resolveDeferred = resolve; });
          }
          return Promise.resolve(new Response(JSON.stringify({ task_ids: ['server-created'] }), {
            status: 200, headers: { ETag: 'v1', 'Idempotency-Replayed': 'true' },
          }));
        }
        if (calls.length === 2) {
          if (deferredRequest === 'current GET') {
            return new Promise<Response>((resolve) => { resolveDeferred = resolve; });
          }
          return Promise.resolve(remoteSnapshot(sent, ['server-created'], 'v2'));
        }
        expect(persisted).not.toBeNull();
        return Promise.resolve(new Response(JSON.stringify({
          task_ids: ['server-created', 'server-later'],
        }), { status: 200, headers: { ETag: 'v3' } }));
      }));
      const recovery = reconcilePendingBoardWrite({
        remote: store,
        identity,
        pendingWrite: {
          operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
          body: JSON.stringify({ board: serialize(sent), task_ids: [null] }),
          sent_board: sent,
          sent_canonical_ids: {},
          if_match: null,
        },
        readLive: () => liveSnapshot(current, currentGeneration, {
          epoch: currentEpoch,
          durableBase: durablePendingSnapshot(current, currentGeneration),
        }),
        readSnapshot: () => durablePendingSnapshot(current, currentGeneration),
        persistAcknowledgement: async (next, ids, deleted, expectedVersion) => {
          if (expectedVersion.generation !== currentGeneration) {
            return {
              ok: false as const,
              error: new Error('stale board generation'),
              conflict: true as const,
              currentGeneration,
            };
          }
          persisted = next;
          acknowledgedGeneration = expectedVersion.generation;
          return persistenceSuccess(next, expectedVersion.generation + 1, {
            canonicalTaskIDs: ids,
            deletedCanonicalIDs: deleted,
            pendingBoardWrite: null,
          });
        },
        repairPersist: async (next, ids, deleted, expectedVersion) =>
          persistenceSuccess(next, expectedVersion.generation + 1, {
            canonicalTaskIDs: ids,
            deletedCanonicalIDs: deleted,
            pendingBoardWrite: null,
          }),
        apply: vi.fn(),
        queuePush: (snapshot) => store.saveRemote(identity, snapshot.board, vi.fn(), vi.fn(), {
          canonicalTaskIDs: snapshot.canonicalTaskIDs,
          durableVersion: snapshot.version,
          durableSnapshot: snapshot,
        }),
        cancelled: () => false,
      });
      const awaitedCall = deferredRequest === 'replay PUT' ? 1 : 2;
      await vi.waitFor(() => expect(calls).toHaveLength(awaitedCall));
      current = { ...sent, tasks: [...sent.tasks, newTask({ title: 'later' })] };
      currentGeneration = 8;
      currentEpoch = 1;
      resolveDeferred(deferredRequest === 'replay PUT'
        ? new Response(JSON.stringify({ task_ids: ['server-created'] }), {
          status: 200, headers: { ETag: 'v1', 'Idempotency-Replayed': 'true' },
        })
        : remoteSnapshot(sent, ['server-created'], 'v2'));
      const result = await recovery;
      await vi.waitFor(() => expect(calls).toHaveLength(3));
      expect(titles(result.recovered.board)).toEqual(['created', 'later']);
      expect(titles(persisted!)).toEqual(['created', 'later']);
      expect(acknowledgedGeneration).toBe(8);
      const queued = JSON.parse(calls[2]!.body) as { board: string; task_ids: Array<string | null> };
      expect(titles(parse(queued.board))).toEqual(['created', 'later']);
      expect(queued.task_ids).toEqual(['server-created', null]);
    },
  );

  it('cancels pending replay without stale persistence, apply, or unblock', async () => {
    const store = new RemoteStore();
    const sent = board('created');
    let cancelled = false;
    let resolveReplay!: (response: Response) => void;
    const fetchMock = vi.fn(() => new Promise<Response>((resolve) => { resolveReplay = resolve; }));
    vi.stubGlobal('fetch', fetchMock);
    const persist = vi.fn(async () => persistenceSuccess(sent, 2));
    const apply = vi.fn();
    const queuePush = vi.fn();
    const recovery = reconcilePendingBoardWrite({
      remote: store,
      identity,
      pendingWrite: {
        operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
        body: JSON.stringify({ board: serialize(sent), task_ids: [null] }),
        sent_board: sent,
        sent_canonical_ids: {},
        if_match: null,
      },
      readLive: () => liveSnapshot(sent, 1, {
        durableBase: durablePendingSnapshot(sent),
      }),
      readSnapshot: () => durablePendingSnapshot(sent),
      persistAcknowledgement: persist,
      repairPersist: persist,
      apply,
      queuePush,
      cancelled: () => cancelled,
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    cancelled = true;
    store.cancel();
    resolveReplay(new Response(JSON.stringify({ task_ids: ['server-created'] }), {
      status: 200, headers: { ETag: 'stale' },
    }));
    await expect(recovery).rejects.toThrow('remote operation cancelled');
    expect(persist).not.toHaveBeenCalled();
    expect(apply).not.toHaveBeenCalled();
    expect(queuePush).not.toHaveBeenCalled();
    store.saveRemote(identity, board('later'), vi.fn(), vi.fn(), { canonicalTaskIDs: new Map() });
    await Promise.resolve();
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('drops receipt-mapped tasks after a current 404 and keeps unmapped work', async () => {
    const store = new RemoteStore();
    const sent = board('created');
    const later = newTask({ title: 'later local' });
    const current = { ...sent, tasks: [...sent.tasks, later] };
    const body = JSON.stringify({ board: serialize(sent), task_ids: [null] });
    stubFetch([
      new Response(JSON.stringify({ task_ids: ['server-created'] }), {
        status: 200, headers: { ETag: 'v2', 'Idempotency-Replayed': 'true' },
      }),
      new Response('gone', { status: 404, headers: { ETag: 'v3' } }),
    ]);
    const recovered = await store.replayPendingBoardWrite(identity, {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e', body,
      sent_board: sent, sent_canonical_ids: {}, if_match: '"r1"',
    }, current, new Map());
    expect(titles(recovered.board)).toEqual(['later local']);
    expect(recovered.canonicalTaskIDs.size).toBe(0);
    expect(recovered.needsPush).toBe(true);
  });

  it('promotes an active new-task acknowledgement before rebasing the queued edit', async () => {
    const store = new RemoteStore();
    let ids: ReadonlyMap<string, string> = new Map();
    const loaded = await (async () => {
      stubFetch([remoteSnapshot(board('A'), ['server-a'], 'v1')]);
      return store.loadRemote(identity, (loadedIDs) => { ids = loadedIDs; });
    })();
    vi.unstubAllGlobals();

    const added = newTask({ title: 'new task' });
    const active = { ...loaded!, tasks: [...loaded!.tasks, added] };
    const latest = {
      ...active,
      tasks: active.tasks.map((task) =>
        task.id === added.id ? { ...task, desc: 'queued edit' } : task,
      ),
    };
    let resolveActive!: (response: Response) => void;
    const calls: Call[] = [];
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url,
        method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      if (calls.length === 1) {
        return new Promise<Response>((resolve) => { resolveActive = resolve; });
      }
      return Promise.resolve(new Response(
        JSON.stringify({ task_ids: ['server-a', 'server-new'] }),
        { status: 200, headers: { ETag: 'v3' } },
      ));
    }));

    const latestDone = new Promise<void>((resolve, reject) => {
      store.saveRemote(identity, active, reject, undefined, { canonicalTaskIDs: ids });
      store.saveRemote(identity, latest, reject, () => resolve(), { canonicalTaskIDs: ids });
    });
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    resolveActive(new Response(
      JSON.stringify({ task_ids: ['server-a', 'server-new'] }),
      { status: 200, headers: { ETag: 'v2' } },
    ));
    await latestDone;

    const queued = JSON.parse(calls[1]!.body) as {
      board: string;
      task_ids: Array<string | null>;
    };
    expect(titles(parse(queued.board))).toEqual(['A', 'new task']);
    expect(parse(queued.board).tasks[1]!.desc).toBe('queued edit');
    expect(queued.task_ids).toEqual(['server-a', 'server-new']);
  });

  it('cancels queued work and suppresses an older completion callback', async () => {
    const store = new RemoteStore();
    let resolveFirst!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => {
      resolveFirst = resolve;
    })));
    const success = vi.fn();
    store.saveRemote(identity, board('A'), undefined, success);
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    store.saveRemote(identity, board('B'), undefined, success);
    store.cancel();
    resolveFirst(new Response(null, { status: 204 }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(success).not.toHaveBeenCalled();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('cancellation wins while a JSON response body is still parsing', async () => {
    const store = new RemoteStore();
    const body = deferred<unknown>();
    const response = new Response('', {
      headers: { 'Content-Type': 'application/json', ETag: 'stale' },
    });
    const json = vi.fn(() => body.promise);
    Object.defineProperty(response, 'json', { value: json });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(response)));
    const taskIDs = vi.fn();
    const loading = store.loadRemote(identity, taskIDs);
    await vi.waitFor(() => expect(json).toHaveBeenCalledOnce());
    store.cancel();
    body.resolve({ board: serialize(board('stale')), task_ids: ['server-stale'] });
    await expect(loading).rejects.toThrow('remote operation cancelled');
    expect(taskIDs).not.toHaveBeenCalled();
  });

  it('cancellation wins while durable create staging is pending', async () => {
    const store = new RemoteStore();
    const staged = deferred<{ ok: boolean }>();
    const stagePendingBoardWrite = vi.fn(() => staged.promise);
    const success = vi.fn();
    const failure = vi.fn();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    store.saveRemote(identity, board('created'), failure, success, {
      canonicalTaskIDs: new Map(),
      generation: 0,
      pendingWriteStager: { stagePendingBoardWrite },
    });
    await vi.waitFor(() => expect(stagePendingBoardWrite).toHaveBeenCalledOnce());
    store.cancel();
    staged.resolve({ ok: true });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(success).not.toHaveBeenCalled();
    expect(failure).not.toHaveBeenCalled();
  });

  it('does not issue a PUT when durable create staging reports a generation conflict', async () => {
    const store = new RemoteStore();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const error = await new Promise<unknown>((resolve) => {
      store.saveRemote(identity, board('created'), resolve, vi.fn(), {
        canonicalTaskIDs: new Map(),
        generation: 4,
        pendingWriteStager: {
          stagePendingBoardWrite: async (_pending, expectedVersion) => {
            expect(expectedVersion).toEqual({ present: true, generation: 4 });
            return {
              ok: false,
              error: new Error('stale board generation'),
              conflict: true as const,
              currentGeneration: 5,
            };
          },
        },
      });
    });
    expect(error).toBeInstanceOf(Error);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('stops startup publication after one failed CAS rebase retry', async () => {
    const remote = new RemoteStore();
    const before = board('base');
    const current = { ...before, title: 'local' };
    const fresh = { ...current, tasks: [...current.tasks, newTask({ title: 'cross-tab' })] };
    vi.spyOn(remote, 'loadRemote').mockResolvedValue({ ...before, title: 'remote' });
    const snapshots = [durableSnapshot(fresh, 2)];
    const persist = vi.fn(async (
      _board: Board,
      _ids: ReadonlyMap<string, string>,
      _deleted: ReadonlySet<string>,
      _expectedVersion: { present: boolean; generation: number },
    ) => ({
      ok: false as const,
      error: new Error('stale board generation'),
      conflict: true as const,
      currentGeneration: 3,
    }));
    const push = vi.fn();
    const result = await reconcileStartupBoardFetch({
      remote,
      identity,
      readLive: (() => {
        let read = 0;
        return () => read++ === 0
          ? liveSnapshot(before, 1)
          : liveSnapshot(current, 1, { epoch: 1 });
      })(),
      readSnapshot: () => snapshots.shift() ?? durableSnapshot(fresh, 2),
      persist,
      apply: vi.fn(),
      push,
      cancelled: () => false,
    });
    expect(persist.mock.calls.map((call) => call[3])).toEqual([
      { present: true, generation: 1 },
      { present: true, generation: 2 },
    ]);
    expect(result.persisted).toBe(false);
    expect(push).not.toHaveBeenCalled();
  });

  it('keeps replay blocked when a newer pending operation replaces the conflicted one', async () => {
    const remote = new RemoteStore();
    const sent = board('created');
    const pending = durablePendingSnapshot(sent).pendingBoardWrite!;
    const replacement = {
      ...pending,
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355f',
    };
    vi.spyOn(remote, 'replayPendingBoardWrite').mockResolvedValue({
      board: sent,
      canonicalTaskIDs: new Map([[sent.tasks[0]!.id, 'server-created']]),
      conflicts: [],
      needsPush: false,
      acknowledgedOperationID: pending.operation_id,
    });
    const resume = vi.spyOn(remote, 'resumeAfterPendingBoardWrite');
    const baseSnapshot = durablePendingSnapshot(sent, 1);
    const replacementSnapshot = durableSnapshot(sent, 2, {
      pendingBoardWrite: replacement,
    });
    const persist = vi.fn(async () => ({
      ok: false as const,
      error: new Error('stale board generation'),
      conflict: true as const,
      currentGeneration: 2,
    }));
    const apply = vi.fn();
    const queuePush = vi.fn();
    const result = await reconcilePendingBoardWrite({
      remote,
      identity,
      pendingWrite: pending,
      readLive: () => liveSnapshot(sent, 1, {
        durableBase: baseSnapshot,
      }),
      readSnapshot: () => replacementSnapshot,
      persistAcknowledgement: persist,
      repairPersist: persist,
      apply,
      queuePush,
      cancelled: () => false,
    });
    expect(result.persisted).toBe(false);
    expect(persist).toHaveBeenCalledOnce();
    expect(apply).not.toHaveBeenCalled();
    expect(queuePush).not.toHaveBeenCalled();
    expect(resume).not.toHaveBeenCalled();
  });

  it('does not retry legacy recovery after another tab clears the migration marker', async () => {
    const remote = new RemoteStore();
    const local = board('legacy');
    vi.spyOn(remote, 'bootstrapLegacy').mockResolvedValue({
      board: local,
      taskIDs: new Map([[local.tasks[0]!.id, 'server-legacy']]),
    });
    const baseSnapshot = durableSnapshot(local, 1, { migratedRaw: true });
    const clearedSnapshot = durableSnapshot(local, 2, { migratedRaw: false });
    const persist = vi.fn(async () => ({
      ok: false as const,
      error: new Error('stale board generation'),
      conflict: true as const,
      currentGeneration: 2,
    }));
    const apply = vi.fn();
    const queuePush = vi.fn();
    const result = await reconcileLegacyBootstrap({
      remote,
      identity,
      readLive: () => liveSnapshot(local, 1, { durableBase: baseSnapshot }),
      readSnapshot: () => clearedSnapshot,
      persist,
      repairPersist: persist,
      apply,
      queuePush,
      cancelled: () => false,
    });
    expect(result.persisted).toBe(false);
    expect(persist).toHaveBeenCalledOnce();
    expect(apply).not.toHaveBeenCalled();
    expect(queuePush).not.toHaveBeenCalled();
  });

  it('repairs one live edit that lands after a successful durable commit', async () => {
    const baseBoard = { title: 'base', tasks: [] };
    const base = durableSnapshot(baseBoard, 1);
    let live = liveSnapshot(baseBoard, 1, { durableBase: base });
    const persist = vi.fn(async (candidate) => {
      live = liveSnapshot({ title: 'local', tasks: [] }, 1, {
        epoch: 1,
        durableBase: base,
        needsLocalPersistence: true,
        remoteClean: false,
      });
      return persistenceSuccess(candidate.board, 2, {
        canonicalTaskIDs: candidate.canonicalTaskIDs,
        deletedCanonicalIDs: candidate.deletedCanonicalIDs,
      });
    });
    const repair = vi.fn(async (candidate) => persistenceSuccess(candidate.board, 3, {
      canonicalTaskIDs: candidate.canonicalTaskIDs,
      deletedCanonicalIDs: candidate.deletedCanonicalIDs,
    }));
    const outcome = await commitLiveCandidate({
      sourceLive: live,
      candidate: {
        board: { title: 'remote', tasks: [] },
        canonicalTaskIDs: new Map(),
        deletedCanonicalIDs: new Set(),
      },
      readLive: () => live,
      readDurable: () => base,
      persist,
      repairPersist: repair,
      cancelled: () => false,
    });
    expect(outcome.persisted).toBe(true);
    expect(outcome.writes).toBe(2);
    expect(outcome.snapshot?.board.title).toBe('local');
    expect(outcome.conflicts).toContain('board title');
    expect(repair).toHaveBeenCalledOnce();
  });

  it('accumulates durable and live rebase conflicts in one coordinator outcome', async () => {
    const baseBoard = { title: 'base', tasks: [] };
    const base = durableSnapshot(baseBoard, 1);
    const freshDurable = durableSnapshot({ title: 'durable', tasks: [] }, 2);
    let live = liveSnapshot(baseBoard, 1, { durableBase: base });
    const sourceLive = live;
    const persist = vi.fn(async (candidate) => {
      if (persist.mock.calls.length === 1) {
        live = liveSnapshot({ title: 'live', tasks: [] }, 2, {
          epoch: 1,
          durableBase: freshDurable,
          needsLocalPersistence: true,
          remoteClean: false,
        });
        return {
          ok: false as const,
          error: new Error('stale board generation'),
          conflict: true as const,
          currentGeneration: 2,
        };
      }
      return persistenceSuccess(candidate.board, 3, {
        canonicalTaskIDs: candidate.canonicalTaskIDs,
        deletedCanonicalIDs: candidate.deletedCanonicalIDs,
      });
    });
    const outcome = await commitLiveCandidate({
      sourceLive,
      candidate: {
        board: { title: 'candidate', tasks: [] },
        canonicalTaskIDs: new Map(),
        deletedCanonicalIDs: new Set(),
      },
      readLive: () => live,
      readDurable: () => freshDurable,
      persist,
      repairPersist: persist,
      cancelled: () => false,
    });

    expect(outcome).toMatchObject({ persisted: true, writes: 2 });
    expect(outcome.snapshot?.board.title).toBe('live');
    expect(outcome.conflicts).toEqual(['board title', 'board title']);
  });

  it('stops after a second live change during the single repair CAS', async () => {
    const baseBoard = { title: 'base', tasks: [] };
    const base = durableSnapshot(baseBoard, 1);
    let live = liveSnapshot(baseBoard, 1, { durableBase: base });
    const persist = vi.fn(async (candidate) => {
      live = liveSnapshot({ title: 'local one', tasks: [] }, 1, {
        epoch: 1,
        durableBase: base,
        needsLocalPersistence: true,
        remoteClean: false,
      });
      return persistenceSuccess(candidate.board, 2);
    });
    const repair = vi.fn(async (candidate) => {
      live = liveSnapshot({ title: 'local two', tasks: [] }, 1, {
        epoch: 2,
        durableBase: base,
        needsLocalPersistence: true,
        remoteClean: false,
      });
      return persistenceSuccess(candidate.board, 3);
    });
    const outcome = await commitLiveCandidate({
      sourceLive: live,
      candidate: {
        board: { title: 'remote', tasks: [] },
        canonicalTaskIDs: new Map(),
        deletedCanonicalIDs: new Set(),
      },
      readLive: () => live,
      readDurable: () => base,
      persist,
      repairPersist: repair,
      cancelled: () => false,
    });
    expect(outcome.persisted).toBe(false);
    expect(outcome.recoveryPending).toBe(true);
    expect(outcome.writes).toBe(2);
    expect(outcome.snapshot?.generation).toBe(3);
  });

  it('commits and pushes a seeded first edit against virtual absence', async () => {
    const values = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      key: (index: number) => [...values.keys()][index] ?? null,
      removeItem: (key: string) => { values.delete(key); },
      setItem: (key: string, value: string) => { values.set(key, value); },
    });
    const local = new LocalStore('seed-first-edit');
    const seed = local.loadSnapshot();
    expect(seed.version).toEqual({ present: false, generation: 0 });
    let live = liveSnapshot(seed.board, 0, { durableBase: seed });
    const fetched = deferred<Board>();
    const remote = new RemoteStore();
    vi.spyOn(remote, 'loadRemote').mockImplementation(async () => fetched.promise);
    const pushed: DurableSnapshot[] = [];
    const startup = reconcileStartupBoardFetch({
      remote,
      identity,
      readLive: () => live,
      readSnapshot: () => local.loadSnapshot(),
      persist: (next, ids, deleted, version, guard) =>
        local.saveIfGeneration(next, ids, deleted, version, guard),
      apply: (snapshot) => {
        live = {
          ...live,
          board: snapshot.board,
          canonicalTaskIDs: snapshot.canonicalTaskIDs,
          deletedCanonicalIDs: snapshot.deletedCanonicalIDs,
          durableBase: snapshot,
          needsLocalPersistence: false,
        };
      },
      push: (snapshot) => { pushed.push(snapshot); },
      cancelled: () => false,
    });
    live = {
      ...live,
      epoch: 1,
      board: { ...seed.board, title: 'first edit' },
      needsLocalPersistence: true,
      remoteClean: false,
    };
    fetched.resolve({ title: 'server', tasks: [] });
    const result = await startup;
    expect(result.persisted).toBe(true);
    expect(result.snapshot?.version).toEqual({ present: true, generation: 1 });
    expect(result.snapshot?.board.title).toBe('first edit');
    expect(pushed).toHaveLength(1);
    expect(pushed[0]).toBe(result.snapshot);
  });

  it('recognizes unchanged remote semantics without adopting parsed client IDs', () => {
    const currentBase = board('duplicate', 'duplicate');
    const remoteBase = board('duplicate', 'duplicate');
    const current = {
      ...currentBase,
      tasks: currentBase.tasks.map((task, index) => ({ ...task, desc: `copy ${index}` })),
    };
    const remote = {
      ...remoteBase,
      tasks: remoteBase.tasks.map((task, index) => ({ ...task, desc: `copy ${index}` })),
    };
    const currentIDs = new Map([[current.tasks[0]!.id, 'server-one']]);
    const remoteIDs = new Map([[remote.tasks[0]!.id, 'server-one']]);
    const base = {
      board: current,
      canonicalTaskIDs: currentIDs,
      deletedCanonicalIDs: new Set<string>(),
      migratedRaw: false,
      pendingBoardWrite: null,
    };
    expect(sameBoardSemantics(base, {
      ...base,
      board: remote,
      canonicalTaskIDs: remoteIDs,
    })).toBe(true);
    expect(sameBoardSemantics(base, {
      ...base,
      board: { ...remote, tasks: [...remote.tasks].reverse() },
      canonicalTaskIDs: remoteIDs,
    })).toBe(false);
    expect(sameBoardSemantics(base, {
      ...base,
      board: remote,
      canonicalTaskIDs: remoteIDs,
      deletedCanonicalIDs: new Set(['server-deleted']),
    })).toBe(false);
  });

  it.each([
    ['mixed-case', ['server-A', 'server-a', 'Server-b']],
    ['numeric-like', ['server-2', 'server-10', 'server-01']],
    ['non-ASCII', ['server-ä', 'server-Ω', 'server-😀']],
  ])('compares %s deleted canonical IDs independently of insertion order', (_kind, ids) => {
    const value = board('unchanged');
    const canonicalTaskIDs = new Map([[value.tasks[0]!.id, 'server-live']]);
    const base = {
      board: value,
      canonicalTaskIDs,
      deletedCanonicalIDs: new Set(ids),
      migratedRaw: false,
      pendingBoardWrite: null,
    };

    expect(sameBoardSemantics(base, {
      ...base,
      deletedCanonicalIDs: new Set([...ids].reverse()),
    })).toBe(true);
    expect(sameBoardSemantics(base, {
      ...base,
      deletedCanonicalIDs: new Set([...ids.slice(0, -1), `${ids.at(-1)}-different`]),
    })).toBe(false);
  });

  it('exposes a current-epoch guard across an awaited success callback', async () => {
    const store = new RemoteStore();
    const release = deferred<void>();
    const mutated = vi.fn();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(null, { status: 204 }))));
    store.saveRemote(identity, board('A'), vi.fn(), async ({ isCurrent }) => {
      await release.promise;
      if (isCurrent?.()) mutated();
    });
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledOnce());
    store.cancel();
    release.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(mutated).not.toHaveBeenCalled();
  });

  it('keeps later saves blocked when durable acknowledgement persistence fails', async () => {
    const store = new RemoteStore();
    const persistence = deferred<void>();
    const fetchMock = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
    vi.stubGlobal('fetch', fetchMock);
    const failed = new Promise<unknown>((resolve) => {
      store.saveRemote(identity, board('A'), resolve, async () => persistence.promise);
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    store.saveRemote(identity, board('B'), vi.fn(), vi.fn());
    persistence.reject(new DOMException('quota exceeded', 'QuotaExceededError'));
    await expect(failed).resolves.toBeInstanceOf(Error);
    store.flush();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it('suppresses startup publication when cancellation occurs during persistence', async () => {
    const store = new RemoteStore();
    const before = board('base');
    const persistence = deferred<ReturnType<typeof persistenceSuccess>>();
    let cancelled = false;
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(
      remoteSnapshot(board('remote'), ['server-remote'], 'v2'),
    )));
    const push = vi.fn();
    const persist = vi.fn(() => persistence.promise);
    const startup = reconcileStartupBoardFetch({
      remote: store,
      identity,
      readLive: (() => {
        let read = 0;
        return () => read++ === 0
          ? liveSnapshot(before)
          : liveSnapshot(board('edited'), 1, { epoch: 1 });
      })(),
      readSnapshot: () => durableSnapshot(before),
      persist,
      apply: vi.fn(),
      push,
      cancelled: () => cancelled,
    });
    await vi.waitFor(() => expect(persist).toHaveBeenCalledOnce());
    cancelled = true;
    persistence.resolve(persistenceSuccess(board('edited'), 2));
    const result = await startup;
    expect(result.persisted).toBe(false);
    expect(push).not.toHaveBeenCalled();
  });

  it.each(['resolve', 'reject'] as const)(
    'drains one current-epoch save after a cancelled request later %s',
    async (settlement) => {
      const store = new RemoteStore();
      let settleA!: (value: Response | PromiseLike<Response>) => void;
      let rejectA!: (reason?: unknown) => void;
      const calls: Call[] = [];
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
        calls.push({
          url, method: init?.method ?? 'GET',
          headers: (init?.headers ?? {}) as Record<string, string>,
          body: typeof init?.body === 'string' ? init.body : '',
        });
        if (calls.length === 1) {
          return new Promise<Response>((resolve, reject) => {
            settleA = resolve;
            rejectA = reject;
          });
        }
        const etag = calls.length === 2 ? 'vB' : 'vC';
        return Promise.resolve(new Response(null, { status: 204, headers: { ETag: etag } }));
      }));
      const aSuccess = vi.fn();
      const aError = vi.fn();
      const bSuccess = vi.fn();
      store.saveRemote(identity, board('A'), aError, aSuccess);
      await vi.waitFor(() => expect(calls).toHaveLength(1));
      store.cancel();
      store.saveRemote(identity, board('B'), vi.fn(), bSuccess);
      if (settlement === 'resolve') {
        settleA(new Response(null, { status: 204, headers: { ETag: 'stale-A' } }));
      } else {
        rejectA(new TypeError('old request failed'));
      }
      await vi.waitFor(() => expect(bSuccess).toHaveBeenCalledOnce());
      expect(calls).toHaveLength(2);
      expect(aSuccess).not.toHaveBeenCalled();
      expect(aError).not.toHaveBeenCalled();
      expect(calls[1]!.headers['If-Match']).toBeUndefined();

      await new Promise<void>((resolve, reject) => {
        store.saveRemote(identity, board('C'), reject, () => resolve());
      });
      expect(calls).toHaveLength(3);
      expect(calls[2]!.headers['If-Match']).toBe('vB');
    },
  );

  it.each([
    ['loadRemote', 'resolve'],
    ['loadRemote', 'reject'],
    ['prepareDirtyMapped', 'resolve'],
    ['prepareDirtyMapped', 'reject'],
  ] as const)(
    'epoch-binds delayed Alice %s when it later %s after Bob loads',
    async (path, settlement) => {
      const store = new RemoteStore();
      const alice = board('Alice');
      const bob = board('Bob');
      let resolveAlice!: (response: Response) => void;
      let rejectAlice!: (error: unknown) => void;
      const calls: Call[] = [];
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
        calls.push({
          url, method: init?.method ?? 'GET',
          headers: (init?.headers ?? {}) as Record<string, string>,
          body: typeof init?.body === 'string' ? init.body : '',
        });
        if (calls.length === 1) {
          return new Promise<Response>((resolve, reject) => {
            resolveAlice = resolve;
            rejectAlice = reject;
          });
        }
        if (calls.length === 2) {
          return Promise.resolve(remoteSnapshot(bob, ['server-bob'], 'rB'));
        }
        return Promise.resolve(new Response(JSON.stringify({ task_ids: ['server-bob'] }), {
          status: 200, headers: { ETag: 'rB2' },
        }));
      }));
      const aliceIDs = new Map([[alice.tasks[0]!.id, 'server-alice']]);
      const aliceCallback = vi.fn();
      const aliceRequest = path === 'loadRemote'
        ? store.loadRemote({ kind: 'manual', id: 'alice' }, aliceCallback)
        : store.prepareDirtyMapped(
          { kind: 'manual', id: 'alice' }, alice, aliceIDs,
        );
      await vi.waitFor(() => expect(calls).toHaveLength(1));
      store.cancel();
      let bobIDs: ReadonlyMap<string, string> = new Map();
      const loadedBob = await store.loadRemote(
        { kind: 'manual', id: 'bob' },
        (ids) => { bobIDs = ids; },
      );
      if (settlement === 'resolve') {
        resolveAlice(remoteSnapshot(alice, ['server-alice'], 'rA'));
      } else {
        rejectAlice(new TypeError('stale Alice failure'));
      }
      await expect(aliceRequest).rejects.toThrow('remote operation cancelled');
      expect(aliceCallback).not.toHaveBeenCalled();

      await new Promise<void>((resolve, reject) => {
        store.saveRemote(
          { kind: 'manual', id: 'bob' },
          { ...loadedBob!, title: 'Bob edited' },
          reject,
          () => resolve(),
          { canonicalTaskIDs: bobIDs },
        );
      });
      expect(calls[2]!.headers['If-Match']).toBe('rB');
      expect(calls[2]!.headers['X-KB-User']).toBe('bob');
    },
  );

  it('prevents a cancelled startup coordinator from applying a stale Alice snapshot', async () => {
    const store = new RemoteStore();
    const before = board('local');
    let cancelled = false;
    let resolveAlice!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => {
      resolveAlice = resolve;
    })));
    const persist = vi.fn(async () => persistenceSuccess(before, 1));
    const push = vi.fn();
    const startup = reconcileStartupBoardFetch({
      remote: store,
      identity: { kind: 'manual', id: 'alice' },
      readLive: (() => {
        let read = 0;
        return () => read++ === 0
          ? liveSnapshot(before)
          : liveSnapshot({ ...before, title: 'edited' }, 0, { epoch: 1 });
      })(),
      readSnapshot: () => durableSnapshot(before),
      persist,
      apply: vi.fn(),
      push,
      cancelled: () => cancelled,
    });
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledOnce());
    cancelled = true;
    store.cancel();
    resolveAlice(remoteSnapshot(board('stale Alice'), ['server-alice'], 'rA'));
    await expect(startup).rejects.toThrow('remote operation cancelled');
    expect(persist).not.toHaveBeenCalled();
    expect(push).not.toHaveBeenCalled();
  });

  it('does not let an obsolete completion bypass a current replay block', async () => {
    const store = new RemoteStore();
    const sent = board('created');
    let resolveA!: (response: Response) => void;
    let resolveReplay!: (response: Response) => void;
    const calls: Call[] = [];
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url, method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      if (calls.length === 1) return new Promise<Response>((resolve) => { resolveA = resolve; });
      if (calls.length === 2) return new Promise<Response>((resolve) => { resolveReplay = resolve; });
      return Promise.resolve(remoteSnapshot(sent, ['server-created'], 'v2'));
    }));
    store.saveRemote(identity, board('A'));
    await vi.waitFor(() => expect(calls).toHaveLength(1));
    store.cancel();
    const replay = store.replayPendingBoardWrite(identity, {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
      body: JSON.stringify({ board: serialize(sent), task_ids: [null] }),
      sent_board: sent,
      sent_canonical_ids: {},
      if_match: null,
    }, sent, new Map());
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    store.saveRemote(identity, board('B'), vi.fn(), vi.fn(), { canonicalTaskIDs: new Map() });
    resolveA(new Response(null, { status: 204, headers: { ETag: 'stale-A' } }));
    await Promise.resolve();
    expect(calls).toHaveLength(2);
    resolveReplay(new Response(JSON.stringify({ task_ids: ['server-created'] }), {
      status: 200, headers: { ETag: 'v1', 'Idempotency-Replayed': 'true' },
    }));
    await replay;
    expect(calls).toHaveLength(3);
    store.flush();
    await Promise.resolve();
    expect(calls).toHaveLength(3);
  });

  it('flushes only the latest debounced save with keepalive', async () => {
    vi.useFakeTimers();
    try {
      const store = new RemoteStore();
      const requests: RequestInit[] = [];
      vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => {
        requests.push(init ?? {});
        return Promise.resolve(new Response(null, { status: 204 }));
      }));
      const first = vi.fn();
      const latest = vi.fn();
      store.saveRemoteDebounced(identity, board('A'), undefined, first);
      store.saveRemoteDebounced(identity, board('B'), undefined, latest);
      store.flush();
      await vi.runAllTimersAsync();

      expect(requests).toHaveLength(1);
      expect(requests[0]!.keepalive).toBe(true);
      expect(titles(parse(requests[0]!.body as string))).toEqual(['B']);
      expect(first).not.toHaveBeenCalled();
      expect(latest).toHaveBeenCalledOnce();
    } finally {
      vi.useRealTimers();
    }
  });

  it('dirty legacy recovery GETs first and sends only a conditional markdown PUT', async () => {
    const store = new RemoteStore();
    const local = board('same', 'local only');
    const server = board('same', 'CLI task');
    // Make the unchanged card byte-identical despite independently minted ids.
    local.tasks[0] = { ...server.tasks[0]!, id: local.tasks[0]!.id };
    const calls = stubFetch([
      remoteSnapshot(server, ['server-same', 'server-cli'], 'v7'),
      new Response(JSON.stringify({ task_ids: ['server-same', 'server-local', 'server-cli'] }), {
        status: 200,
        headers: { ETag: 'v8' },
      }),
    ]);

    const recovered = await store.bootstrapLegacy(identity, local);
    expect(calls.map((call) => call.method)).toEqual(['GET', 'PUT']);
    expect(calls[1]!.headers['If-Match']).toBe('v7');
    expect(calls[1]!.headers['Content-Type']).toBe('text/markdown');
    expect(titles(recovered.board)).toEqual(['same', 'local only', 'CLI task']);
    expect([...recovered.taskIDs.values()]).toEqual([
      'server-same', 'server-local', 'server-cli',
    ]);
  });

  it.each(['bootstrap GET', 'bootstrap PUT'] as const)(
    'preserves an edit committed while the dirty legacy %s is in flight',
    async (deferredRequest) => {
      const store = new RemoteStore();
      const base = board('base');
      const server = board('base', 'external');
      let current = base;
      let currentEpoch = 0;
      let resolveDeferred!: (response: Response) => void;
      let persisted: Board | null = null;
      const calls: Call[] = [];
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
        calls.push({
          url, method: init?.method ?? 'GET',
          headers: (init?.headers ?? {}) as Record<string, string>,
          body: typeof init?.body === 'string' ? init.body : '',
        });
        if (calls.length === 1) {
          if (deferredRequest === 'bootstrap GET') {
            return new Promise<Response>((resolve) => { resolveDeferred = resolve; });
          }
          return Promise.resolve(remoteSnapshot(server, ['server-base', 'server-external'], 'v2'));
        }
        if (calls.length === 2) {
          if (deferredRequest === 'bootstrap PUT') {
            return new Promise<Response>((resolve) => { resolveDeferred = resolve; });
          }
          return Promise.resolve(new Response(JSON.stringify({
            task_ids: ['server-base', 'server-external'],
          }), { status: 200, headers: { ETag: 'v3' } }));
        }
        expect(persisted).not.toBeNull();
        return Promise.resolve(new Response(JSON.stringify({
          task_ids: ['server-base', 'server-later', 'server-external'],
        }), { status: 200, headers: { ETag: 'v4' } }));
      }));
      const recovery = reconcileLegacyBootstrap({
        remote: store,
        identity,
        readLive: () => liveSnapshot(current, 1, {
          epoch: currentEpoch,
          durableBase: durableSnapshot(base, 1, { migratedRaw: true }),
        }),
        readSnapshot: () => durableSnapshot(base, 1, { migratedRaw: true }),
        persist: async (next, ids, deleted, version) => {
          persisted = next;
          return persistenceSuccess(next, version.generation + 1, {
            canonicalTaskIDs: ids,
            deletedCanonicalIDs: deleted,
            migratedRaw: false,
          });
        },
        repairPersist: async (next, ids, deleted, version) =>
          persistenceSuccess(next, version.generation + 1, {
            canonicalTaskIDs: ids,
            deletedCanonicalIDs: deleted,
            migratedRaw: false,
          }),
        apply: vi.fn(),
        queuePush: (snapshot) => store.saveRemote(identity, snapshot.board, vi.fn(), vi.fn(), {
          canonicalTaskIDs: snapshot.canonicalTaskIDs,
          durableVersion: snapshot.version,
          durableSnapshot: snapshot,
        }),
        cancelled: () => false,
      });
      const awaitedCall = deferredRequest === 'bootstrap GET' ? 1 : 2;
      await vi.waitFor(() => expect(calls).toHaveLength(awaitedCall));
      current = { ...base, tasks: [...base.tasks, newTask({ title: 'later' })] };
      currentEpoch = 1;
      resolveDeferred(deferredRequest === 'bootstrap GET'
        ? remoteSnapshot(server, ['server-base', 'server-external'], 'v2')
        : new Response(JSON.stringify({ task_ids: ['server-base', 'server-external'] }), {
          status: 200, headers: { ETag: 'v3' },
        }));
      const result = await recovery;
      await vi.waitFor(() => expect(calls).toHaveLength(3));
      expect(result.persisted).toBe(true);
      expect(result.needsPush).toBe(true);
      expect(titles(result.recovered.board)).toEqual(['base', 'later', 'external']);
      expect(titles(persisted!)).toEqual(['base', 'later', 'external']);
      const queued = JSON.parse(calls[2]!.body) as { board: string; task_ids: Array<string | null> };
      expect(titles(parse(queued.board))).toEqual(['base', 'later', 'external']);
      expect(queued.task_ids).toEqual(['server-base', null, 'server-external']);
    },
  );

  it('cancels legacy bootstrap PUT without stale apply and releases the next epoch', async () => {
    const store = new RemoteStore();
    const base = board('base');
    let cancelled = false;
    let resolvePut!: (response: Response) => void;
    const calls: Call[] = [];
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url, method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      if (calls.length === 1) {
        return Promise.resolve(remoteSnapshot(base, ['server-base'], 'v1'));
      }
      if (calls.length === 2) {
        return new Promise<Response>((resolve) => { resolvePut = resolve; });
      }
      return Promise.resolve(new Response(null, { status: 204, headers: { ETag: 'v-next' } }));
    }));
    const persist = vi.fn(async () => persistenceSuccess(base, 2));
    const apply = vi.fn();
    const queuePush = vi.fn();
    const recovery = reconcileLegacyBootstrap({
      remote: store,
      identity,
      readLive: () => liveSnapshot(base, 1, {
        durableBase: durableSnapshot(base, 1, { migratedRaw: true }),
      }),
      readSnapshot: () => durableSnapshot(base, 1, { migratedRaw: true }),
      persist,
      repairPersist: persist,
      apply,
      queuePush,
      cancelled: () => cancelled,
    });
    await vi.waitFor(() => expect(calls).toHaveLength(2));
    cancelled = true;
    store.cancel();
    resolvePut(new Response(JSON.stringify({ task_ids: ['server-base'] }), {
      status: 200, headers: { ETag: 'stale-bootstrap' },
    }));
    await expect(recovery).rejects.toThrow('remote operation cancelled');
    expect(persist).not.toHaveBeenCalled();
    expect(apply).not.toHaveBeenCalled();
    expect(queuePush).not.toHaveBeenCalled();
    await new Promise<void>((resolve, reject) => {
      store.saveRemote(identity, board('next epoch'), reject, () => resolve());
    });
    expect(calls).toHaveLength(3);
    expect(calls[2]!.headers['If-Match']).toBeUndefined();
  });

  it('prepares a mapped dirty reload without reviving either deletion direction', async () => {
    const store = new RemoteStore();
    const local = board('kept', 'remote deleted', 'local new');
    const localIDs = new Map([
      [local.tasks[0]!.id, 'server-kept'],
      [local.tasks[1]!.id, 'server-remote-deleted'],
      // The retained mapping of an absent task proves a local deletion.
      ['deleted-client-id', 'server-local-deleted'],
    ]);
    const server = board('kept', 'local deleted', 'external');
    stubFetch([
      remoteSnapshot(
        server,
        ['server-kept', 'server-local-deleted', 'server-external'],
        'v5',
      ),
    ]);

    const prepared = await store.prepareDirtyMapped(identity, local, localIDs);
    expect(titles(prepared.board)).toEqual(['kept', 'local new', 'external']);
    expect([...prepared.taskIDs.values()]).toContain('server-external');
  });

  it('treats a versioned dirty 404 as an empty canonical set and recreates only new work', async () => {
    const store = new RemoteStore();
    const local = board('remote deleted', 'local new');
    const localIDs = new Map([[local.tasks[0]!.id, 'server-deleted']]);
    const calls = stubFetch([
      new Response('gone', { status: 404, headers: { ETag: 'v-gone' } }),
      new Response(JSON.stringify({ task_ids: ['server-new'] }), {
        status: 200,
        headers: { ETag: 'v-recreated' },
      }),
    ]);

    const prepared = await store.prepareDirtyMapped(
      identity,
      local,
      localIDs,
      new Set(['server-older-deletion']),
    );
    expect(titles(prepared.board)).toEqual(['local new']);
    expect([...prepared.taskIDs]).toEqual([]);
    expect([...prepared.deletedCanonicalIDs]).toEqual([]);

    const saved = await new Promise<SaveResult>((resolve, reject) => {
      store.saveRemote(
        identity,
        prepared.board,
        reject,
        ({ pushed, taskIDs }) => resolve({ pushed, taskIDs }),
        { canonicalTaskIDs: prepared.taskIDs },
      );
    });
    const payload = JSON.parse(calls[1]!.body) as {
      board: string;
      task_ids: Array<string | null>;
    };
    expect(calls[1]!.headers['If-Match']).toBe('v-gone');
    expect(titles(parse(payload.board))).toEqual(['local new']);
    expect(payload.task_ids).toEqual([null]);
    expect([...saved.taskIDs.values()]).toEqual(['server-new']);
  });

  it('persists merged additions with a newer local edit before UI adoption', async () => {
    const values = new Map<string, string>();
    const storage: Storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (key) => values.get(key) ?? null,
      key: (index) => [...values.keys()][index] ?? null,
      removeItem: (key) => { values.delete(key); },
      setItem: (key, value) => { values.set(key, String(value)); },
    };
    vi.stubGlobal('localStorage', storage);
    const remote = new RemoteStore();
    const base = board('A');
    const external = board('A', 'external');
    let resolveActive!: (response: Response) => void;
    let call = 0;
    vi.stubGlobal('fetch', vi.fn((_url: string, _init?: RequestInit) => {
      call += 1;
      if (call === 1) return Promise.resolve(remoteSnapshot(base, ['server-a'], 'v1'));
      if (call === 2) {
        return new Promise<Response>((resolve) => { resolveActive = resolve; });
      }
      if (call === 3) {
        return Promise.resolve(remoteSnapshot(
          external,
          ['server-a', 'server-external'],
          'v2',
        ));
      }
      if (call === 4) {
        return Promise.resolve(new Response(JSON.stringify({
          task_ids: ['server-a', 'server-external'],
        }), { status: 200, headers: { ETag: 'v3' } }));
      }
      throw new Error('unexpected fetch');
    }));

    let baseIDs: ReadonlyMap<string, string> = new Map();
    const loaded = (await remote.loadRemote(identity, (ids) => { baseIDs = ids; }))!;
    const local = new LocalStore('ack-crash-test');
    await local.save(loaded, baseIDs);
    const active = {
      ...loaded,
      tasks: [{ ...loaded.tasks[0]!, desc: 'active edit' }],
    };
    const newer = {
      ...active,
      tasks: [{ ...active.tasks[0]!, desc: 'newer edit' }],
    };
    let acknowledged = false;
    const completed = new Promise<void>((resolve, reject) => {
      remote.saveRemote(
        identity,
        active,
        reject,
        async ({ pushed, taskIDs: committedIDs }) => {
          const durable = mergeAcknowledgedState(
            newer, baseIDs, active, baseIDs, pushed, committedIDs,
          );
          expect((await local.save(durable.board, durable.canonicalTaskIDs)).ok).toBe(true);
          acknowledged = true;
          resolve();
          // Deliberately no React/carryMerged state commit: simulate a crash.
        },
        { canonicalTaskIDs: baseIDs },
      );
    });
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    await local.save(newer, baseIDs);
    resolveActive(new Response('conflict', { status: 409 }));
    await completed;
    expect(acknowledged).toBe(true);

    const reconstructed = new LocalStore('ack-crash-test').loadOrSeed();
    expect(titles(reconstructed.board)).toEqual(['A', 'external']);
    expect(reconstructed.board.tasks[0]!.desc).toBe('newer edit');
    expect([...reconstructed.canonicalTaskIDs.values()]).toEqual([
      'server-a',
      'server-external',
    ]);
  });

  it('refuses dirty legacy recovery when the GET provides no version', async () => {
    const store = new RemoteStore();
    stubFetch([remoteSnapshot(board('server'), ['server-a'], '')]);
    await expect(store.bootstrapLegacy(identity, board('local'))).rejects.toThrow(
      'legacy recovery requires a board version',
    );
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('reports a dirty legacy conflict without an unsafe automatic retry', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      remoteSnapshot(board('server'), ['server-a'], 'v1'),
      new Response('conflict', { status: 409, headers: { ETag: 'v2' } }),
    ]);
    await expect(store.bootstrapLegacy(identity, board('local'))).rejects.toThrow(
      'PUT /api/board failed: 409',
    );
    expect(calls.map((call) => call.method)).toEqual(['GET', 'PUT']);
  });
});
