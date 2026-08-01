import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Board, Status, Task } from './model';
import type { DurableSnapshot, PendingBoardWrite } from './store';
import {
  commitLiveCandidate,
  mergeAcknowledgedState,
  mergeBoards,
  mergeCanonicalBoards,
  mergeTaskIDMaps,
  reconcileLegacyBootstrap,
  reconcilePendingBoardWrite,
  reconcileStartupBoardFetch,
  recomputeDeletedCanonicalIDs,
  RemoteStore,
  sameBoardSemantics,
} from './remote';
import type { LiveBoardSnapshot } from './remote';
import { serialize } from './markdown';

function task(id: string, title: string, status: Status = 'todo'): Task {
  return {
    id, title, status, emoji: '', desc: '', blocked: false, prio: 3,
    tags: [], checks: [], createdAt: '2026-08-01T00:00:00Z', movedAt: '2026-08-01T00:00:00Z',
  };
}

function board(...tasks: Task[]): Board {
  return { title: 'board', tasks };
}

function snapshot(value: Board, overrides: Partial<DurableSnapshot> = {}): DurableSnapshot {
  return {
    board: value,
    seeded: false,
    canonicalTaskIDs: new Map(),
    deletedCanonicalIDs: new Set(),
    migratedRaw: false,
    pendingBoardWrite: null,
    generation: 1,
    version: { present: true, generation: 1 },
    ...overrides,
  };
}

function live(durable: DurableSnapshot, overrides: Partial<LiveBoardSnapshot> = {}): LiveBoardSnapshot {
  return {
    epoch: 0,
    board: durable.board,
    canonicalTaskIDs: durable.canonicalTaskIDs,
    deletedCanonicalIDs: durable.deletedCanonicalIDs,
    durableBase: durable,
    needsLocalPersistence: false,
    remoteClean: true,
    ...overrides,
  };
}

const pendingWrite: PendingBoardWrite = {
  operation_id: 'op-1',
  body: '{}',
  sent_board: board(),
  sent_canonical_ids: {},
  if_match: null,
};

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('remote residual identity and merge branches', () => {
  it('replaces a stale client mapping that claims an acknowledged canonical id', () => {
    expect([...mergeTaskIDMaps(
      new Map([['stale-client', 'canonical-1'], ['other', 'canonical-2']]),
      new Map([['current-client', 'canonical-1']]),
    )]).toEqual([['other', 'canonical-2'], ['current-client', 'canonical-1']]);
  });

  it('records canonical ids removed from the next live map', () => {
    expect([...recomputeDeletedCanonicalIDs(
      new Map([['a', 'canonical-a'], ['b', 'canonical-b']]),
      new Set(['already-deleted']),
      new Map([['b', 'canonical-b']]),
    )].sort()).toEqual(['already-deleted', 'canonical-a']);
  });

  it('adopts an acknowledgement directly when no newer edit exists', () => {
    const sent = board(task('client-a', 'sent'));
    const pushed = board(task('server-a', 'pushed'));
    const ids = new Map([['server-a', 'canonical-a']]);
    expect(mergeAcknowledgedState(sent, new Map(), sent, new Map(), pushed, ids))
      .toEqual({ board: pushed, canonicalTaskIDs: ids, conflicts: [] });
  });

  it('keeps the local placement and reports a simultaneous status conflict', () => {
    const base = board(task('base', 'work', 'todo'));
    const local = board(task('local', 'work', 'doing'));
    const remote = board(task('remote', 'work', 'done'));
    const merged = mergeCanonicalBoards(
      local, new Map([['local', 'canonical']]),
      remote, new Map([['remote', 'canonical']]),
      base, new Map([['base', 'canonical']]),
    );
    expect(merged.board.tasks[0]?.status).toBe('doing');
    expect(merged.conflicts).toContain('work: status/order');
  });

  it('distinguishes durable snapshots by deleted canonical identity', () => {
    const value = {
      board: board(), canonicalTaskIDs: new Map<string, string>(),
      migratedRaw: false, pendingBoardWrite: null,
    };
    expect(sameBoardSemantics(
      { ...value, deletedCanonicalIDs: new Set(['a']) },
      { ...value, deletedCanonicalIDs: new Set(['b']) },
    )).toBe(false);
  });

  it('moves every matching legacy duplicate when the server placement changed', () => {
    const first = task('a', 'duplicate', 'todo');
    const second = task('b', 'duplicate', 'todo');
    const moved = task('server', 'duplicate', 'doing');
    expect(mergeBoards(board(first, second), board(moved, moved), board(first, second)).tasks)
      .toHaveLength(2);
  });
});

describe('remote health detection', () => {
  it('accepts only an explicit healthy response', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{"ok":true}', {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))));
    await expect(new RemoteStore().detect()).resolves.toBe(true);
    expect(fetch).toHaveBeenCalledWith('/api/health', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it('returns false for a failed or malformed health request', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('offline'))));
    await expect(new RemoteStore().detect()).resolves.toBe(false);
  });

  it('returns false for a non-ok health response', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 503 }))));
    await expect(new RemoteStore().detect()).resolves.toBe(false);
  });
});

describe('remote protocol residual branches', () => {
  it('rejects a non-object JSON board snapshot', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('null', {
      status: 200, headers: { 'Content-Type': 'application/json' },
    })));
    await expect(new RemoteStore().loadRemote({ kind: 'manual', id: 'x' }))
      .rejects.toThrow('invalid board snapshot');
  });

  it('rejects a non-success board response', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 503 })));
    await expect(new RemoteStore().loadRemote({ kind: 'manual', id: 'x' }))
      .rejects.toThrow('GET /api/board failed: 503');
  });

  it('wraps a non-error pending replay failure with a stable message', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject('offline')));
    await expect(new RemoteStore().replayPendingBoardWrite(
      { kind: 'manual', id: 'x' }, pendingWrite, board(), new Map(),
    )).rejects.toThrow('board write outcome is ambiguous');
  });

  it('blocks a second pending-write recovery operation', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('offline'))));
    const remote = new RemoteStore();
    await expect(remote.replayPendingBoardWrite(
      { kind: 'manual', id: 'x' }, pendingWrite, board(), new Map(),
    )).rejects.toThrow('offline');
    await expect(remote.replayPendingBoardWrite(
      { kind: 'manual', id: 'x' }, { ...pendingWrite, operation_id: 'op-2' }, board(), new Map(),
    )).rejects.toThrow('another pending board write');
  });

  it('recovers a conflicted replay against a versioned empty board', async () => {
    const current = board(task('known', 'known'), task('new', 'new'));
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('', { status: 409 }))
      .mockResolvedValueOnce(new Response(null, { status: 404, headers: { ETag: 'v2' } }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await new RemoteStore().replayPendingBoardWrite(
      { kind: 'manual', id: 'x' }, pendingWrite, current, new Map([['known', 'canonical-known']]),
    );
    expect(result.board.tasks.map((item) => item.id)).toEqual(['new']);
    expect(result).toMatchObject({ needsPush: true, acknowledgedOperationID: 'op-1' });
  });

  it('merges a conflicted replay with the fetched canonical board', async () => {
    const server = board(task('server', 'remote'));
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('', { status: 409 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        board: serialize(server), task_ids: ['canonical-server'],
      }), { status: 200, headers: { 'Content-Type': 'application/json', ETag: 'v2' } }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await new RemoteStore().replayPendingBoardWrite(
      { kind: 'manual', id: 'x' }, pendingWrite, board(task('local', 'local')), new Map(),
    );
    expect(result.board.tasks).toHaveLength(2);
    expect(result.canonicalTaskIDs.size).toBe(1);
  });

  it('refuses to resume a recovery that was never started', () => {
    expect(() => new RemoteStore().resumeAfterPendingBoardWrite(pendingWrite, {
      board: board(), canonicalTaskIDs: new Map(), conflicts: [], acknowledgedOperationID: 'op-1',
    })).toThrow('recovery was not started');
  });

  it('requires a durable version before staging a create', async () => {
    const error = await new Promise<unknown>((resolve) => {
      new RemoteStore().saveRemote(
        { kind: 'manual', id: 'x' }, board(task('new', 'new')), resolve, undefined,
        { canonicalTaskIDs: new Map(), pendingWriteStager: { stagePendingBoardWrite: vi.fn() } },
      );
    });
    expect(String(error)).toContain('staging requires a durable version');
  });

  it('rejects a staged create without a returned generation', async () => {
    const error = await new Promise<unknown>((resolve) => {
      new RemoteStore().saveRemote(
        { kind: 'manual', id: 'x' }, board(task('new', 'new')), resolve, undefined,
        {
          canonicalTaskIDs: new Map(), durableVersion: { present: true, generation: 1 },
          pendingWriteStager: { stagePendingBoardWrite: vi.fn(async () => ({ ok: true })) },
        },
      );
    });
    expect(String(error)).toContain('staging returned no generation');
  });

  it('uses the fallback error when create staging fails without one', async () => {
    const error = await new Promise<unknown>((resolve) => {
      new RemoteStore().saveRemote(
        { kind: 'manual', id: 'x' }, board(task('new', 'new')), resolve, undefined,
        {
          canonicalTaskIDs: new Map(), durableVersion: { present: true, generation: 1 },
          pendingWriteStager: { stagePendingBoardWrite: vi.fn(async () => ({ ok: false })) },
        },
      );
    });
    expect(String(error)).toContain('failed to stage board write');
  });

  it('requires a board version for mapped dirty recovery', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(serialize(board()), { status: 200 })));
    await expect(new RemoteStore().prepareDirtyMapped(
      { kind: 'manual', id: 'x' }, board(), new Map(),
    )).rejects.toThrow('dirty recovery requires a board version');
  });

  it('drains a debounced save when its timer expires', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })));
    const completed = new Promise<void>((resolve, reject) => {
      new RemoteStore().saveRemoteDebounced(
        { kind: 'manual', id: 'x' }, board(), reject, () => resolve(),
      );
    });
    await vi.advanceTimersByTimeAsync(800);
    await expect(completed).resolves.toBeUndefined();
  });

  it('cancels a pending debounce timer and flushes an immediate pending save', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })));
    const cancelled = new RemoteStore();
    cancelled.saveRemoteDebounced({ kind: 'manual', id: 'x' }, board());
    cancelled.cancel();
    await vi.advanceTimersByTimeAsync(800);
    expect(fetch).not.toHaveBeenCalled();

    const flushed = new Promise<void>((resolve, reject) => {
      const remote = new RemoteStore();
      remote.saveRemoteDebounced({ kind: 'manual', id: 'x' }, board(), reject, () => resolve());
      remote.flush();
    });
    await expect(flushed).resolves.toBeUndefined();
  });

  it('flushes safely when no save is pending', () => {
    expect(() => new RemoteStore().flush()).not.toThrow();
  });

  it('bootstraps a legacy board after a versioned 404', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(null, { status: 404, headers: { ETag: 'v1' } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    await expect(new RemoteStore().bootstrapLegacy(
      { kind: 'manual', id: 'x' }, board(task('local', 'local')),
    )).resolves.toMatchObject({ board: expect.objectContaining({ title: 'board' }) });
  });

  it('preserves duplicate server buckets during legacy bootstrap', async () => {
    const server = board(task('s1', 'duplicate'), task('s2', 'duplicate'));
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        board: serialize(server), task_ids: ['canonical-1', 'canonical-2'],
      }), { status: 200, headers: { 'Content-Type': 'application/json', ETag: 'v1' } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    const result = await new RemoteStore().bootstrapLegacy(
      { kind: 'manual', id: 'x' }, board(task('l1', 'duplicate'), task('l2', 'duplicate')),
    );
    expect(result.board.tasks).toHaveLength(4);
  });

  it('carries a persisted generation into a newer queued save', async () => {
    let release!: (response: Response) => void;
    const firstResponse = new Promise<Response>((resolve) => { release = resolve; });
    const fetchMock = vi.fn()
      .mockReturnValueOnce(firstResponse)
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    const remote = new RemoteStore();
    const first = new Promise<void>((resolve, reject) => {
      remote.saveRemote(
        { kind: 'manual', id: 'x' }, board(task('first', 'first')), reject,
        () => ({ persisted: true, generation: 7 }),
        { canonicalTaskIDs: new Map() },
      );
      remote.saveRemote(
        { kind: 'manual', id: 'x' }, board(task('second', 'second')), reject,
        () => resolve(), { canonicalTaskIDs: new Map() },
      );
    });
    release(new Response(JSON.stringify({ task_ids: ['canonical-first'] }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }));
    await expect(first).resolves.toBeUndefined();
  });

  it('rejects an obsolete legacy bootstrap after cancellation', async () => {
    let release!: (response: Response) => void;
    const put = new Promise<Response>((resolve) => { release = resolve; });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(null, { status: 404, headers: { ETag: 'v1' } }))
      .mockReturnValueOnce(put);
    vi.stubGlobal('fetch', fetchMock);
    const remote = new RemoteStore();
    const recovery = remote.bootstrapLegacy(
      { kind: 'manual', id: 'x' }, board(task('local', 'local')),
    );
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    remote.cancel();
    release(new Response(null, { status: 204 }));
    await expect(recovery).rejects.toThrow('remote operation cancelled');
  });
});

describe('durable reconciliation early and repair paths', () => {
  it('marks a successful commit as recovery-pending when repair persistence fails', async () => {
    const base = snapshot(board(task('a', 'base')));
    const newer = live(base, { epoch: 1, board: board(task('a', 'newer')) });
    const committed = snapshot(board(task('a', 'committed')), {
      generation: 2, version: { present: true, generation: 2 },
    });
    const outcome = await commitLiveCandidate({
      sourceLive: live(base),
      candidate: {
        board: committed.board, canonicalTaskIDs: new Map(), deletedCanonicalIDs: new Set(),
      },
      readLive: () => newer,
      readDurable: () => base,
      persist: async () => ({ ok: true, generation: 2, snapshot: committed }),
      repairPersist: async (_candidate, _version, guard) => {
        expect(guard()).toBe(true);
        return { ok: false, error: new Error('quota') };
      },
      cancelled: () => false,
    });
    expect(outcome).toMatchObject({ persisted: false, recoveryPending: true, writes: 2 });
  });

  it('returns startup data without persistence when no live edit occurred', async () => {
    const durable = snapshot(board(task('local', 'local')));
    const remoteBoard = board(task('remote', 'remote'));
    const remote = {
      loadRemote: vi.fn(async (_identity, onIDs: (ids: ReadonlyMap<string, string>) => void) => {
        onIDs(new Map([['remote', 'canonical-remote']]));
        return remoteBoard;
      }),
    } as unknown as RemoteStore;
    const result = await reconcileStartupBoardFetch({
      remote, identity: { kind: 'manual', id: 'x' }, readLive: () => live(durable),
      readSnapshot: () => durable, persist: vi.fn(), apply: vi.fn(), push: vi.fn(),
      cancelled: () => false,
    });
    expect(result).toMatchObject({ remoteBoard });
    expect(result.persisted).toBeUndefined();
  });

  it('repairs a startup commit when another live edit lands during persistence', async () => {
    const baseDurable = snapshot(board(task('base', 'base')));
    const base = live(baseDurable);
    const current = live(baseDurable, { epoch: 1, board: board(task('current', 'current')) });
    const after = live(baseDurable, { epoch: 2, board: board(task('after', 'after')) });
    const states = [base, current, after, after, after];
    const readLive = vi.fn(() => states.shift() ?? after);
    const remote = {
      loadRemote: vi.fn(async () => board(task('server', 'server'))),
    } as unknown as RemoteStore;
    let generation = 1;
    const persist = vi.fn(async (
      value: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>,
    ) => {
      generation += 1;
      return { ok: true as const, generation, snapshot: snapshot(value, {
        canonicalTaskIDs: ids, deletedCanonicalIDs: deleted, generation,
        version: { present: true, generation },
      }) };
    });
    const result = await reconcileStartupBoardFetch({
      remote, identity: { kind: 'manual', id: 'x' }, readLive,
      readSnapshot: () => baseDurable, persist, apply: vi.fn(), push: vi.fn(),
      cancelled: () => false,
    });
    expect(result.persisted).toBe(true);
    expect(persist).toHaveBeenCalledTimes(2);
  });

  it('returns durable state when the pending operation no longer matches', async () => {
    const durable = snapshot(board(task('a', 'durable')));
    const remote = { replayPendingBoardWrite: vi.fn() } as unknown as RemoteStore;
    const result = await reconcilePendingBoardWrite({
      remote, identity: { kind: 'manual', id: 'x' }, pendingWrite,
      readLive: () => live(durable), readSnapshot: () => durable,
      persistAcknowledgement: vi.fn(), repairPersist: vi.fn(), apply: vi.fn(), queuePush: vi.fn(),
      cancelled: () => false,
    });
    expect(result.persisted).toBe(false);
    expect(remote.replayPendingBoardWrite).not.toHaveBeenCalled();
  });

  it('does not persist replay recovery without an acknowledgement id', async () => {
    const durable = snapshot(board(), { pendingBoardWrite: pendingWrite });
    const remote = {
      replayPendingBoardWrite: vi.fn(async () => ({
        board: durable.board, canonicalTaskIDs: new Map(), conflicts: [], needsPush: false,
      })),
    } as unknown as RemoteStore;
    const result = await reconcilePendingBoardWrite({
      remote, identity: { kind: 'manual', id: 'x' }, pendingWrite,
      readLive: () => live(durable), readSnapshot: () => durable,
      persistAcknowledgement: vi.fn(), repairPersist: vi.fn(), apply: vi.fn(), queuePush: vi.fn(),
      cancelled: () => false,
    });
    expect(result.persisted).toBe(false);
  });

  it('repairs a replay acknowledgement when another live edit lands during persistence', async () => {
    const durable = snapshot(board(task('base', 'base')), { pendingBoardWrite: pendingWrite });
    const base = live(durable);
    const after = live(durable, { epoch: 1, board: board(task('after', 'after')) });
    const states = [base, base, after, after, after];
    const readLive = vi.fn(() => states.shift() ?? after);
    const remote = {
      replayPendingBoardWrite: vi.fn(async () => ({
        board: durable.board, canonicalTaskIDs: new Map(), conflicts: [], needsPush: false,
        acknowledgedOperationID: 'op-1',
      })),
      resumeAfterPendingBoardWrite: vi.fn(),
    } as unknown as RemoteStore;
    let generation = 1;
    const resultFor = (
      value: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>,
    ) => {
      generation += 1;
      return { ok: true as const, generation, snapshot: snapshot(value, {
        canonicalTaskIDs: ids, deletedCanonicalIDs: deleted, pendingBoardWrite: pendingWrite,
        generation, version: { present: true, generation },
      }) };
    };
    const persistAcknowledgement = vi.fn(async (
      value: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>,
    ) => resultFor(value, ids, deleted));
    const repairPersist = vi.fn(async (
      value: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>,
    ) => resultFor(value, ids, deleted));
    const queuePush = vi.fn();
    const result = await reconcilePendingBoardWrite({
      remote, identity: { kind: 'manual', id: 'x' }, pendingWrite, readLive,
      readSnapshot: () => durable, persistAcknowledgement, repairPersist,
      apply: vi.fn(), queuePush, cancelled: () => false,
    });
    expect(result.persisted).toBe(true);
    expect(repairPersist).toHaveBeenCalledOnce();
    expect(queuePush).toHaveBeenCalledOnce();
  });

  it('skips legacy recovery when the durable marker is absent', async () => {
    const durable = snapshot(board(task('a', 'clean')));
    const remote = { bootstrapLegacy: vi.fn() } as unknown as RemoteStore;
    const result = await reconcileLegacyBootstrap({
      remote, identity: { kind: 'manual', id: 'x' },
      readLive: () => live(durable), readSnapshot: () => durable,
      persist: vi.fn(), repairPersist: vi.fn(), apply: vi.fn(), queuePush: vi.fn(),
      cancelled: () => false,
    });
    expect(result).toMatchObject({ persisted: false, needsPush: false });
    expect(remote.bootstrapLegacy).not.toHaveBeenCalled();
  });

  it('does not persist a legacy recovery after cancellation', async () => {
    const durable = snapshot(board(task('a', 'legacy')), { migratedRaw: true });
    const remote = {
      bootstrapLegacy: vi.fn(async () => ({ board: durable.board, taskIDs: new Map() })),
    } as unknown as RemoteStore;
    const result = await reconcileLegacyBootstrap({
      remote, identity: { kind: 'manual', id: 'x' },
      readLive: () => live(durable), readSnapshot: () => durable,
      persist: vi.fn(), repairPersist: vi.fn(), apply: vi.fn(), queuePush: vi.fn(),
      cancelled: () => true,
    });
    expect(result.persisted).toBe(false);
  });

  it('repairs a legacy commit when another live edit lands during persistence', async () => {
    const durable = snapshot(board(task('base', 'base')), { migratedRaw: true });
    const base = live(durable);
    const after = live(durable, { epoch: 1, board: board(task('after', 'after')) });
    const states = [base, base, after, after, after];
    const readLive = vi.fn(() => states.shift() ?? after);
    const remote = {
      bootstrapLegacy: vi.fn(async () => ({
        board: board(task('server', 'server')), taskIDs: new Map(),
      })),
    } as unknown as RemoteStore;
    let generation = 1;
    const resultFor = (
      value: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>,
    ) => {
      generation += 1;
      return { ok: true as const, generation, snapshot: snapshot(value, {
        canonicalTaskIDs: ids, deletedCanonicalIDs: deleted, migratedRaw: true,
        generation, version: { present: true, generation },
      }) };
    };
    const persist = vi.fn(async (value: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>) =>
      resultFor(value, ids, deleted));
    const repairPersist = vi.fn(async (value: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>) =>
      resultFor(value, ids, deleted));
    const result = await reconcileLegacyBootstrap({
      remote, identity: { kind: 'manual', id: 'x' }, readLive,
      readSnapshot: () => durable, persist, repairPersist,
      apply: vi.fn(), queuePush: vi.fn(), cancelled: () => false,
    });
    expect(result.persisted).toBe(true);
    expect(repairPersist).toHaveBeenCalledOnce();
  });
});
