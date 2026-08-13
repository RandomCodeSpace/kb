import { beforeEach, describe, expect, it, vi } from 'vitest';

// Node test environment has no Web Storage — stub it on globalThis.
const mem = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (k: string) => mem.get(k) ?? null,
  setItem: (k: string, v: string) => {
    mem.set(k, String(v));
  },
  removeItem: (k: string) => {
    mem.delete(k);
  },
  clear: () => {
    mem.clear();
  },
  key: () => null,
  get length() {
    return mem.size;
  },
};

const locks = {
  request: async <T>(
    _name: string,
    _options: LockOptions,
    callback: () => Promise<T> | T,
  ): Promise<T> => callback(),
};
Object.defineProperty(navigator, 'locks', { value: locks, configurable: true });

/**
 * The legacy-key copy runs once per module instance, so every case (and every
 * simulated page load) imports a fresh copy of the module.
 */
async function freshStore() {
  vi.resetModules();
  return import('./store');
}

const today = new Date().toDateString();
const presentVersion = (generation: number) => ({ present: true, generation });

beforeEach(() => {
  mem.clear();
});

describe('legacy webtui.* key migration', () => {
  it('copies board, dirty and streak forward and keeps the old keys', async () => {
    mem.set('webtui.board.v1', JSON.stringify({ title: 'old', tasks: [] }));
    mem.set('webtui.dirty.v1', '1');
    mem.set('webtui.streak.v1', JSON.stringify({ date: today, ids: ['a', 'b'] }));

    const store = await freshStore();
    expect(new store.LocalStore().load()).toEqual({ title: 'old', tasks: [] });
    expect(store.loadDirty()).toBe(true);
    expect(store.shippedToday()).toBe(2);

    expect(mem.get(store.namespaceStorageKey('kb.board.v1', 'default'))).toBeUndefined();
    expect(mem.get(store.namespaceStorageKey('kb.dirty.v1', 'default'))).toBe('1');
    // Rolling back to an older build must still find the data.
    expect(mem.has('webtui.board.v1')).toBe(true);
    expect(mem.has('webtui.dirty.v1')).toBe(true);
    expect(mem.has('webtui.streak.v1')).toBe(true);
    expect(mem.has(store.namespaceStorageKey('kb.board.v1', 'default'))).toBe(false);
  });

  it('auxiliary migration before LocalStore never copies or suppresses a legacy board', async () => {
    mem.set('webtui.board.v1', JSON.stringify({ title: 'legacy board', tasks: [] }));
    mem.set('webtui.dirty.v1', '1');
    mem.set('webtui.streak.v1', JSON.stringify({ date: today, ids: ['done'] }));
    const store = await freshStore();
    expect(store.loadDirty()).toBe(true);
    expect(store.shippedToday()).toBe(1);
    store.setDirty('default', false);
    expect(mem.has(store.namespaceStorageKey('kb.board.v1', 'default'))).toBe(false);
    expect(new store.LocalStore().load()?.title).toBe('legacy board');
    expect(mem.get('webtui.board.v1')).toContain('legacy board');
    expect(mem.get('webtui.streak.v1')).toContain('done');
  });

  it('prefers framed then legacy kb then pre-rename board without requiring a marker', async () => {
    const store = await freshStore();
    const framed = store.namespaceStorageKey('kb.board.v1', 'precedence');
    mem.set('webtui.board.v1.precedence', JSON.stringify({ title: 'pre-rename', tasks: [] }));
    mem.set('kb.board.v1.precedence', JSON.stringify({ title: 'legacy kb', tasks: [] }));
    expect(new store.LocalStore('precedence').load()?.title).toBe('legacy kb');
    mem.set(framed, JSON.stringify({
      version: 3, generation: 9, board: { title: 'framed', tasks: [] }, canonical_ids: {},
    }));
    expect(new store.LocalStore('precedence').loadOrSeed()).toMatchObject({
      board: { title: 'framed', tasks: [] }, generation: 9,
    });
  });

  it('migrates a per-user namespace', async () => {
    mem.set('webtui.board.v1.alice', JSON.stringify({ title: 'alice', tasks: [] }));
    const store = await freshStore();
    expect(new store.LocalStore('alice').load()).toEqual({
      title: 'alice',
      tasks: [],
    });
    expect(mem.get(store.namespaceStorageKey('kb.board.v1', 'alice'))).toBeUndefined();
  });

  it('never overwrites an existing kb.* value', async () => {
    mem.set('webtui.board.v1', JSON.stringify({ title: 'old', tasks: [] }));
    mem.set('kb.board.v1', JSON.stringify({ title: 'new', tasks: [] }));
    const store = await freshStore();
    expect(new store.LocalStore().load()?.title).toBe('new');
  });

  it('keeps dotted namespace prefixes exactly isolated', async () => {
    const store = await freshStore();
    const alice = new store.LocalStore('alice');
    const aliceWork = new store.LocalStore('alice.work');
    await alice.save({ title: 'alice', tasks: [] });
    await aliceWork.save({ title: 'work', tasks: [] });
    store.setDirty('alice', true);
    store.setDirty('alice.work', false);

    expect(new store.LocalStore('alice').load()?.title).toBe('alice');
    expect(new store.LocalStore('alice.work').load()?.title).toBe('work');
    expect(store.loadDirty('alice')).toBe(true);
    expect(store.loadDirty('alice.work')).toBe(false);
  });

  it('runs once: a cleared dirty flag is not resurrected on the next load', async () => {
    mem.set('webtui.dirty.v1', '1');
    const first = await freshStore();
    expect(first.loadDirty()).toBe(true);
    first.setDirty('default', false);

    const second = await freshStore(); // simulates a page reload
    expect(second.loadDirty()).toBe(false);
  });
});

describe('local persistence result', () => {
	it('upgrades raw and generation-less envelopes from generation zero to one', async () => {
	  const storeModule = await freshStore();
	  for (const [ns, value] of [
	    ['raw-generation', storeModule.seedBoard()],
	    ['v3-generation', {
	      version: 3, board: storeModule.seedBoard(), canonical_ids: {},
	    }],
	  ] as const) {
	    const key = storeModule.namespaceStorageKey('kb.board.v1', ns);
	    mem.set(key, JSON.stringify(value));
	    const local = new storeModule.LocalStore(ns);
	    expect(local.loadOrSeed().generation).toBe(0);
	    expect(await local.save(local.load()!)).toMatchObject({ ok: true, generation: 1 });
	    expect(JSON.parse(mem.get(key)!).generation).toBe(1);
	  }
	});

	it.each(['raw', 'v2', 'legacy'] as const)(
	  'reads %s board state without any storage write',
	  async (kind) => {
	    const storeModule = await freshStore();
	    const ns = `read-only-${kind}`;
	    const board = { title: kind, tasks: [] };
	    if (kind === 'legacy') {
	      mem.set(`webtui.board.v1.${ns}`, JSON.stringify(board));
	    } else {
	      mem.set(storeModule.namespaceStorageKey('kb.board.v1', ns), JSON.stringify(
	        kind === 'v2' ? { version: 2, board, canonical_ids: {} } : board,
	      ));
	    }
	    const original = localStorage.setItem;
	    const setItem = vi.fn(original.bind(localStorage));
	    localStorage.setItem = setItem;
	    try {
	      expect(new storeModule.LocalStore(ns).loadOrSeed()).toMatchObject({
	        board, generation: 0,
	      });
	    } finally {
	      localStorage.setItem = original;
	    }
	    expect(setItem).not.toHaveBeenCalled();
	  },
	);

	it('rejects invalid and exhausted generations without consuming a write', async () => {
	  const storeModule = await freshStore();
	  const invalidKey = storeModule.namespaceStorageKey('kb.board.v1', 'invalid-generation');
	  mem.set(invalidKey, JSON.stringify({
	    version: 3,
	    generation: -1,
	    board: { title: 'invalid', tasks: [] },
	    canonical_ids: {},
	  }));
	  expect(new storeModule.LocalStore('invalid-generation').load()).toBeNull();

	  const maxKey = storeModule.namespaceStorageKey('kb.board.v1', 'max-generation');
	  mem.set(maxKey, JSON.stringify({
	    version: 3,
	    generation: Number.MAX_SAFE_INTEGER,
	    board: { title: 'max', tasks: [] },
	    canonical_ids: {},
	  }));
	  const before = mem.get(maxKey);
	  expect(await new storeModule.LocalStore('max-generation').save({
	    title: 'never written', tasks: [],
	  })).toMatchObject({
	    ok: false,
	    conflict: true,
	    currentGeneration: Number.MAX_SAFE_INTEGER,
	  });
	  expect(mem.get(maxKey)).toBe(before);
	});

	it('requires an exact pending operation even at the current generation', async () => {
	  const storeModule = await freshStore();
	  const local = new storeModule.LocalStore('operation-cas');
	  const board = storeModule.seedBoard();
	  await local.save(board);
	  const pending = {
	    operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
	    body: '{}', sent_board: board, sent_canonical_ids: {}, if_match: null,
	  };
	  const staged = await local.stagePendingBoardWrite(pending, presentVersion(1));
	  expect(staged).toMatchObject({ ok: true, generation: 2 });
	  const before = mem.get(storeModule.namespaceStorageKey('kb.board.v1', 'operation-cas'));
	  expect(await local.saveAcknowledgement(
	    board,
	    new Map(),
	    new Set(),
	    staged.ok ? staged.snapshot.version : presentVersion(-1),
	    '7fa459ea-ee8a-4ca4-894e-db77e160355e',
	  )).toMatchObject({ ok: false, conflict: true, currentGeneration: 2 });
	  expect(mem.get(storeModule.namespaceStorageKey('kb.board.v1', 'operation-cas'))).toBe(before);
	  expect(await local.saveAcknowledgement(
	    board,
	    new Map(),
	    new Set(),
	    staged.ok ? staged.snapshot.version : presentVersion(-1),
	  )).toMatchObject({ ok: false, conflict: true, currentGeneration: 2 });
	});

	it('serializes a legacy copy race into one framed generation sequence', async () => {
	  const storeModule = await freshStore();
	  const ns = 'legacy-copy-race';
	  mem.set(`webtui.board.v1.${ns}`, JSON.stringify({ title: 'legacy', tasks: [] }));
	  let tail: Promise<unknown> = Promise.resolve();
	  const locks = {
	    request: <T>(_name: string, _options: LockOptions, callback: () => T | Promise<T>) => {
	      const result = tail.then(callback);
	      tail = result.then(() => undefined, () => undefined);
	      return result;
	    },
	  };
	  const options = { locks: locks as unknown as LockManager };
	  const results = await Promise.all([
	    new storeModule.LocalStore(ns, options).save({ title: 'first', tasks: [] }),
	    new storeModule.LocalStore(ns, options).save({ title: 'second', tasks: [] }),
	  ]);
	  expect(results.map((result) => ({ ok: result.ok, generation: result.ok ? result.generation : -1 }))).toEqual([
	    { ok: true, generation: 1 },
	    { ok: true, generation: 2 },
	  ]);
	  expect(new storeModule.LocalStore(ns).loadOrSeed()).toMatchObject({
	    board: { title: 'second', tasks: [] }, generation: 2,
	  });
	});

	it('allocates consecutive generations for concurrent identity writes', async () => {
	  const storeModule = await freshStore();
	  let tail: Promise<unknown> = Promise.resolve();
	  const serial = {
	    request: <T>(_name: string, _options: LockOptions, callback: () => T | Promise<T>) => {
	      const result = tail.then(callback);
	      tail = result.then(() => undefined, () => undefined);
	      return result;
	    },
	  };
	  const options = { locks: serial as unknown as LockManager };
	  const first = new storeModule.LocalStore('generations', options);
	  const second = new storeModule.LocalStore('generations', options);
	  const results = await Promise.all([
	    first.save({ title: 'first', tasks: [] }),
	    second.save({ title: 'second', tasks: [] }),
	  ]);
	  expect(results.map((result) => ({ ok: result.ok, generation: result.ok ? result.generation : -1 }))).toEqual([
	    { ok: true, generation: 1 },
	    { ok: true, generation: 2 },
	  ]);
	  expect(first.loadOrSeed().generation).toBe(2);
	});

	it('does not consume a generation when the envelope write fails', async () => {
	  const storeModule = await freshStore();
	  const local = new storeModule.LocalStore('failed-generation');
	  expect(await local.save({ title: 'one', tasks: [] })).toMatchObject({ generation: 1 });
	  const original = localStorage.setItem;
	  localStorage.setItem = (key, value) => {
	    if (key.includes('kb.board.v1')) throw new DOMException('quota', 'QuotaExceededError');
	    original.call(localStorage, key, value);
	  };
	  try {
	    expect(await local.save({ title: 'failed', tasks: [] })).toMatchObject({ ok: false });
	  } finally {
	    localStorage.setItem = original;
	  }
	  expect(local.loadOrSeed()).toMatchObject({ board: { title: 'one' }, generation: 1 });
	  expect(await local.save({ title: 'two', tasks: [] })).toMatchObject({ generation: 2 });
	});

	it('rejects a stale acknowledgement byte-for-byte and retries at the current generation', async () => {
	  const storeModule = await freshStore();
	  const ns = 'stale-ack';
	  const key = storeModule.namespaceStorageKey('kb.board.v1', ns);
	  const board = storeModule.seedBoard();
	  const clientID = board.tasks[0]!.id;
	  const pending = {
	    operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
	    body: '{"board":"exact","task_ids":["server-a"]}',
	    sent_board: board,
	    sent_canonical_ids: { [clientID]: 'server-a' },
	    if_match: '"r1"',
	  };
	  mem.set(key, JSON.stringify({
	    version: 3,
	    generation: 5,
	    board,
	    canonical_ids: { [clientID]: 'server-a' },
	    deleted_canonical_ids: ['server-deleted'],
	    identity_recovery_needed: true,
	    pending_board_write: pending,
	  }));
	  const first = new storeModule.LocalStore(ns);
	  const second = new storeModule.LocalStore(ns);
	  const newer = { ...board, title: 'newer cross-tab board' };
	  expect(await second.save(newer)).toMatchObject({ ok: true, generation: 6 });
	  const protectedBytes = mem.get(key);
	  expect(await first.saveAcknowledgement(
	    board,
	    new Map([[clientID, 'server-a']]),
	    new Set(['server-deleted']),
	    presentVersion(5),
	    pending.operation_id,
	  )).toMatchObject({ ok: false, conflict: true, currentGeneration: 6 });
	  expect(mem.get(key)).toBe(protectedBytes);
	  expect(await first.saveAcknowledgement(
	    newer,
	    new Map([[clientID, 'server-a']]),
	    new Set(['server-deleted']),
	    presentVersion(6),
	    pending.operation_id,
	  )).toMatchObject({ ok: true, generation: 7 });
	  expect(first.loadPendingBoardWrite()).toBeNull();
	});

	it('publishes the framed envelope before its board-specific marker', async () => {
	  const storeModule = await freshStore();
	  const ns = 'marker-order';
	  const boardKey = storeModule.namespaceStorageKey('kb.board.v1', ns);
	  const markerKey = storeModule.namespaceStorageKey('kb.board-migrated.v1', ns);
	  mem.set('webtui.board.v1.marker-order', JSON.stringify({ title: 'legacy', tasks: [] }));
	  const writes: string[] = [];
	  const original = localStorage.setItem;
	  localStorage.setItem = (key, value) => {
	    writes.push(key);
	    if (key === markerKey) throw new DOMException('marker denied', 'QuotaExceededError');
	    original.call(localStorage, key, value);
	  };
	  const local = new storeModule.LocalStore(ns);
	  try {
	    expect(await local.save({ title: 'framed', tasks: [] })).toMatchObject({
	      ok: true, generation: 1,
	    });
	  } finally {
	    localStorage.setItem = original;
	  }
	  expect(writes.slice(-2)).toEqual([boardKey, markerKey]);
	  expect(mem.has(markerKey)).toBe(false);
	  mem.set('webtui.board.v1.marker-order', JSON.stringify({ title: 'resurrected', tasks: [] }));
	  expect(new storeModule.LocalStore(ns).load()?.title).toBe('framed');
	  expect(await new storeModule.LocalStore(ns).save({ title: 'retry', tasks: [] })).toMatchObject({
	    ok: true, generation: 2,
	  });
	  expect(mem.get(markerKey)).toBe('1');
	});
	it('persists without Web Locks through the serial fallback', async () => {
	  const storeModule = await freshStore();
	  const local = new storeModule.LocalStore('no-locks', { locks: null });
	  expect(await local.save(storeModule.seedBoard())).toMatchObject({ ok: true });
	  expect(local.load()).not.toBeNull();
	});

	it('serial lock fallback keeps writes ordered and survives a thrown callback', async () => {
	  const storeModule = await freshStore();
	  const locks = storeModule.serialLockFallback();
	  const order: number[] = [];
	  const slow = locks.request('n', { mode: 'exclusive' }, async () => {
	    await new Promise((resolve) => setTimeout(resolve, 10));
	    order.push(1);
	  });
	  const fast = locks.request('n', { mode: 'exclusive' }, () => {
	    order.push(2);
	  });
	  await Promise.all([slow, fast]);
	  expect(order).toEqual([1, 2]);
	  await expect(
	    locks.request('n', { mode: 'exclusive' }, () => {
	      throw new Error('boom');
	    }),
	  ).rejects.toThrow('boom');
	  // The queue recovers: a later request still runs.
	  expect(await locks.request('n', { mode: 'exclusive' }, () => 'after')).toBe('after');
	});

	it('does not overwrite malformed durable state inside a locked mutation', async () => {
	  const storeModule = await freshStore();
	  const key = storeModule.namespaceStorageKey('kb.board.v1', 'malformed-write');
	  mem.set(key, '{not json');
	  const local = new storeModule.LocalStore('malformed-write');
	  expect(await local.save(storeModule.seedBoard())).toMatchObject({ ok: false });
	  expect(mem.get(key)).toBe('{not json');
	});

	it('serializes cross-instance mutation under the exact framed identity lock', async () => {
	  const storeModule = await freshStore();
	  const names: string[] = [];
	  let tail: Promise<unknown> = Promise.resolve();
	  const serial = {
	    request: <T>(name: string, _options: LockOptions, callback: () => T | Promise<T>) => {
	      names.push(name);
	      const result = tail.then(callback);
	      tail = result.then(() => undefined, () => undefined);
	      return result;
	    },
	  };
	  const options = { locks: serial as unknown as LockManager };
	  const first = new storeModule.LocalStore('cross-tab', options);
	  const second = new storeModule.LocalStore('cross-tab', options);
	  const board = storeModule.seedBoard();
	  expect(await first.save(board)).toMatchObject({ ok: true });
	  const pending = {
	    operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
	    body: '{"board":"exact","task_ids":[null]}',
	    sent_board: board,
	    sent_canonical_ids: {},
	    if_match: null,
	  };
	  await Promise.all([
	    first.stagePendingBoardWrite(pending, presentVersion(1)),
	    second.save({ ...board, title: 'concurrent edit' }),
	  ]);
	  expect(first.load()?.title).toBe('concurrent edit');
	  expect(second.loadPendingBoardWrite()).toEqual(pending);
	  expect(new Set(names)).toEqual(new Set([
	    storeModule.namespaceStorageKey('kb:board-envelope:', 'cross-tab'),
	  ]));
	});

	it('fails closed instead of overwriting conflicting canonical identity', async () => {
	  const storeModule = await freshStore();
	  const local = new storeModule.LocalStore('canonical-conflict');
	  const board = storeModule.seedBoard();
	  const clientID = board.tasks[0]!.id;
	  const saved = await local.save(board, new Map([[clientID, 'server-a']]));
	  expect(saved).toMatchObject({ ok: true });
	  const before = mem.get(storeModule.namespaceStorageKey(
	    'kb.board.v1', 'canonical-conflict',
	  ));
	  expect(await local.saveAcknowledgement(
	    board,
	    new Map([[clientID, 'server-b']]),
	    new Set(),
	    saved.ok ? saved.snapshot.version : presentVersion(-1),
	  )).toMatchObject({ ok: false });
	  expect(mem.get(storeModule.namespaceStorageKey(
	    'kb.board.v1', 'canonical-conflict',
	  ))).toBe(before);
	});

	it('migrates a version 2 envelope logically until the next mutation', async () => {
	  const storeModule = await freshStore();
	  const board = storeModule.seedBoard();
	  const key = storeModule.namespaceStorageKey('kb.board.v1', 'v2-user');
	  mem.set(key, JSON.stringify({ version: 2, board, canonical_ids: {} }));
	  const local = new storeModule.LocalStore('v2-user');
	  expect(local.load()).toEqual(board);
	  expect(JSON.parse(mem.get(key)!).version).toBe(2);
	  expect(await local.save(board)).toMatchObject({ ok: true });
	  expect(JSON.parse(mem.get(key)!).version).toBe(3);
	});

	it('migrates v2 and atomically generation-guards a pending board write', async () => {
	  const storeModule = await freshStore();
	  const local = new storeModule.LocalStore('pending');
	  const board = storeModule.seedBoard();
	  await local.save(board);
	  const first = {
	    operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
	    body: '{"board":"first","task_ids":[null]}',
	    sent_board: board,
	    sent_canonical_ids: {},
	    if_match: '"r1"',
	  };
	  const second = { ...first, operation_id: '7fa459ea-ee8a-4ca4-894e-db77e160355e', body: '{"board":"second","task_ids":[null]}' };
	  const firstStage = await local.stagePendingBoardWrite(first, presentVersion(1));
	  const secondStage = await local.stagePendingBoardWrite(
	    second, firstStage.ok ? firstStage.snapshot.version : presentVersion(-1),
	  );
	  expect(firstStage).toMatchObject({ ok: true });
	  expect(secondStage).toMatchObject({ ok: true });
	  expect(await local.saveAcknowledgement(
	    board,
	    new Map(),
	    new Set(),
	    firstStage.ok ? firstStage.snapshot.version : presentVersion(-1),
	    first.operation_id,
	  )).toMatchObject({ ok: false, conflict: true });
	  expect(local.loadPendingBoardWrite()?.operation_id).toBe(second.operation_id);
	  const originalSetItem = localStorage.setItem;
	  let writes = 0;
	  localStorage.setItem = (key, value) => { writes += 1; originalSetItem.call(localStorage, key, value); };
	  try {
	    expect(await local.saveAcknowledgement(
	      board,
	      new Map(),
	      new Set(),
	      secondStage.ok ? secondStage.snapshot.version : presentVersion(-1),
	      second.operation_id,
	    )).toMatchObject({ ok: true });
	  } finally {
	    localStorage.setItem = originalSetItem;
	  }
	  expect(writes).toBe(1);
	  expect(local.loadPendingBoardWrite()).toBeNull();
	  const raw = JSON.parse(mem.get(storeModule.namespaceStorageKey('kb.board.v1', 'pending'))!);
	  expect(raw.version).toBe(3);
	  expect(raw.pending_board_write).toBeUndefined();
	});

  it('does not publish an unpersisted first save through the cache', async () => {
    const storeModule = await freshStore();
    const local = new storeModule.LocalStore('failing');
    const board = storeModule.seedBoard();
    const original = localStorage.setItem;
    localStorage.setItem = () => {
      throw new DOMException('quota exceeded', 'QuotaExceededError');
    };
    try {
      expect(await local.save(board)).toMatchObject({ ok: false });
      expect(local.load()).toBeNull();
    } finally {
      localStorage.setItem = original;
    }
  });

  it('retains an exact pending operation after failed acknowledgement and ordinary save', async () => {
    const storeModule = await freshStore();
    const local = new storeModule.LocalStore('failed-ack');
    const initial = storeModule.seedBoard();
    expect(await local.save(initial)).toMatchObject({ ok: true });
    const pending = {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
      body: '{"board":"exact","task_ids":[null]}',
      sent_board: initial,
      sent_canonical_ids: {},
      if_match: '"r1"',
    };
    const staged = await local.stagePendingBoardWrite(pending, presentVersion(1));
    expect(staged).toMatchObject({ ok: true });
    const key = storeModule.namespaceStorageKey('kb.board.v1', 'failed-ack');
    const diskBefore = mem.get(key)!;
    const acknowledged = { ...initial, title: 'acknowledged' };
    const originalSetItem = localStorage.setItem;
    localStorage.setItem = () => {
      throw new DOMException('quota exceeded', 'QuotaExceededError');
    };
    try {
      expect(await local.saveAcknowledgement(
        acknowledged,
        new Map(),
        new Set(),
        staged.ok ? staged.snapshot.version : presentVersion(-1),
        pending.operation_id,
      )).toMatchObject({ ok: false });
    } finally {
      localStorage.setItem = originalSetItem;
    }
    expect(local.loadPendingBoardWrite()).toEqual(pending);
    expect(mem.get(key)).toBe(diskBefore);
    expect(JSON.parse(mem.get(key)!).pending_board_write).toEqual(pending);

    const later = { ...initial, title: 'later ordinary save' };
    const laterSaved = await local.save(later);
    expect(laterSaved).toMatchObject({ ok: true });
    expect(local.loadPendingBoardWrite()).toEqual(pending);
    expect(JSON.parse(mem.get(key)!).pending_board_write).toEqual(pending);

    expect(await local.saveAcknowledgement(
      acknowledged,
      new Map(),
      new Set(),
      laterSaved.ok ? laterSaved.snapshot.version : presentVersion(-1),
      pending.operation_id,
    )).toMatchObject({ ok: true });
    expect(local.loadPendingBoardWrite()).toBeNull();
    expect(JSON.parse(mem.get(key)!).pending_board_write).toBeUndefined();
  });

  it('hydrates durable defaults before reconstructed save and recovery mutators', async () => {
    const storeModule = await freshStore();
    const initial = storeModule.seedBoard();
    const first = new storeModule.LocalStore('reconstructed-defaults');
    expect(await first.save(initial)).toMatchObject({ ok: true });
    const pending = {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
      body: '{"board":"exact","task_ids":[null]}',
      sent_board: initial,
      sent_canonical_ids: {},
      if_match: '"r1"',
    };
    expect(await first.stagePendingBoardWrite(pending, presentVersion(1))).toMatchObject({ ok: true });
    const recoveryKey = storeModule.namespaceStorageKey(
      'kb.board.v1', 'reconstructed-defaults',
    );
    const recoveryEnvelope = JSON.parse(mem.get(recoveryKey)!);
    recoveryEnvelope.identity_recovery_needed = true;
    mem.set(recoveryKey, JSON.stringify(recoveryEnvelope));

    const ordinary = new storeModule.LocalStore('reconstructed-defaults');
    expect(await ordinary.save({ ...initial, title: 'ordinary' })).toMatchObject({ ok: true });
    expect(ordinary.loadPendingBoardWrite()).toEqual(pending);

    const recovery = new storeModule.LocalStore('reconstructed-defaults');
    const recoveredBoard = { ...initial, title: 'recovered' };
    const recoverySnapshot = recovery.loadSnapshot()!;
    const recovered = await recovery.completeIdentityRecovery(
      recoveredBoard,
      new Map(),
      recoverySnapshot.deletedCanonicalIDs,
      recoverySnapshot.version,
    );
    expect(recovered).toMatchObject({ ok: true });
    expect(recovery.loadPendingBoardWrite()).toEqual(pending);

    const acknowledgement = new storeModule.LocalStore('reconstructed-defaults');
    expect(await acknowledgement.saveAcknowledgement(
      recoveredBoard,
      new Map(),
      new Set(),
      recovered.ok ? recovered.snapshot.version : presentVersion(-1),
      pending.operation_id,
    )).toMatchObject({ ok: true });
    expect(acknowledgement.loadPendingBoardWrite()).toBeNull();
  });

  it('continues dirty generation and remote scheduling while warning once', async () => {
    const storeModule = await freshStore();
    const local = new storeModule.LocalStore('failing-coordinator');
    const board = storeModule.seedBoard();
    const original = localStorage.setItem;
    localStorage.setItem = () => {
      throw new DOMException('quota exceeded', 'QuotaExceededError');
    };
    const warningGate = { current: false };
    const warn = vi.fn();
    const scheduleRemote = vi.fn();
    let dirtyGeneration = 0;
    try {
      for (let edit = 0; edit < 2; edit += 1) {
        storeModule.continueAfterLocalPersistence(
          await local.save({ ...board, title: `edit-${edit}` }),
          warningGate,
          {
            warn,
            markDirty: () => { dirtyGeneration += 1; },
            scheduleRemote,
          },
        );
      }
    } finally {
      localStorage.setItem = original;
    }
    expect(dirtyGeneration).toBe(2);
    expect(scheduleRemote).toHaveBeenCalledTimes(2);
    expect(warn).toHaveBeenCalledTimes(1);
  });
});

describe('shipped counter', () => {
  it('counts one card once across Done -> Doing -> Done', async () => {
    const store = await freshStore();
    expect(store.bumpShipped('default', 't1')).toBe(1); // dropped on Done
    expect(store.unshipToday('default', 't1')).toBe(0); // reopened
    expect(store.bumpShipped('default', 't1')).toBe(1); // dropped on Done again
    expect(store.shippedToday()).toBe(1); // still one card, not three
  });

  it('takes a reopened card back off the tally', async () => {
    const store = await freshStore();
    store.bumpShipped('default', 't1');
    store.bumpShipped('default', 't2');
    expect(store.shippedToday()).toBe(2);
    expect(store.unshipToday('default', 't1')).toBe(1);
    expect(store.shippedToday()).toBe(1);
  });

  it('ignores unshipping a card that never shipped today', async () => {
    const store = await freshStore();
    store.bumpShipped('default', 't1');
    expect(store.unshipToday('default', 'never-shipped')).toBe(1);
    expect(store.shippedToday()).toBe(1);
  });

  it('counts distinct cards', async () => {
    const store = await freshStore();
    store.bumpShipped('default', 't1');
    expect(store.bumpShipped('default', 't2')).toBe(2);
    expect(store.shippedToday()).toBe(2);
  });

  it('keeps namespaces apart', async () => {
    const store = await freshStore();
    store.bumpShipped('alice', 't1');
    expect(store.shippedToday('alice')).toBe(1);
    expect(store.shippedToday('bob')).toBe(0);
  });

  it('resets on day rollover', async () => {
    mem.set(
      'kb.streak.v1',
      JSON.stringify({ date: 'Mon Jan 01 2024', ids: ['t1', 't2'] }),
    );
    const store = await freshStore();
    expect(store.shippedToday()).toBe(0);
    expect(store.bumpShipped('default', 't1')).toBe(1);
  });

  it('counts one card once across a reload that regenerates task ids', async () => {
    const store = await freshStore();
    const { parse } = await import('./markdown');
    const wire = '# kb\n\n## To Do\n\n- [ ] ship me\n';
    // Server mode: every refetch re-parses the wire, and parse() mints a new
    // id for every card — so the shipped record cannot key on ids.
    const before = parse(wire).tasks[0]!;
    const afterReload = parse(wire).tasks[0]!;
    expect(before.id).not.toBe(afterReload.id);
    expect(store.bumpShipped('default', store.shipKey(before))).toBe(1);
    expect(store.bumpShipped('default', store.shipKey(afterReload))).toBe(1);
    expect(store.shippedToday()).toBe(1);
  });

  it('ignores a malformed record', async () => {
    mem.set('kb.streak.v1', '{not json');
    const store = await freshStore();
    expect(store.shippedToday()).toBe(0);
  });
});

describe('loadOrSeed', () => {
  it('builds deterministic seeds without consulting UUIDs or the clock', async () => {
    const randomUUID = vi.spyOn(crypto, 'randomUUID');
    const toISOString = vi.spyOn(Date.prototype, 'toISOString');
    const store = await freshStore();

    const first = store.seedBoard('same namespace');
    const second = store.seedBoard('same namespace');

    expect(second).toEqual(first);
    expect(new Set(first.tasks.map((task) => task.id)).size).toBe(first.tasks.length);
    expect(first.tasks.every(
      (task) => task.createdAt === task.movedAt && task.createdAt === '2026-08-01T00:00:00.000Z',
    )).toBe(true);
    expect(randomUUID).not.toHaveBeenCalled();
    expect(toISOString).not.toHaveBeenCalled();
    randomUUID.mockRestore();
    toISOString.mockRestore();
  });

  it('reconstructs the same seed for a namespace across stores and module reloads', async () => {
    const firstModule = await freshStore();
    const first = new firstModule.LocalStore('reconstructed').loadSnapshot();
    const samePage = new firstModule.LocalStore('reconstructed').loadSnapshot();
    const secondModule = await freshStore();
    const reconstructed = new secondModule.LocalStore('reconstructed').loadSnapshot();

    expect(samePage.board).toEqual(first.board);
    expect(reconstructed.board).toEqual(first.board);
    expect(reconstructed.canonicalTaskIDs.size).toBe(0);
  });

  it('keeps framed seed identities disjoint for tricky namespace boundaries', async () => {
    const store = await freshStore();
    const namespaces = [
      'a',
      'a.b',
      'a%2Eb',
      'prefix',
      'prefix-long',
      '用户 / punctuation!?',
    ];
    const identities = namespaces.map((namespace) =>
      new Set(store.seedBoard(namespace).tasks.map((task) => task.id))
    );

    for (let left = 0; left < identities.length; left += 1) {
      for (let right = left + 1; right < identities.length; right += 1) {
        expect([...identities[left]!].some((id) => identities[right]!.has(id))).toBe(false);
      }
    }
  });

  it('keeps slot identities stable across content-schema rollback', async () => {
    const store = await freshStore();
    const ids = (schema: number) =>
      store.seedBoard('schema-rollback', schema).tasks.map((task) => task.id);

    expect(ids(0)).toEqual(ids(store.CURRENT_SEED_SCHEMA));
    expect(ids(store.CURRENT_SEED_SCHEMA + 1)).toEqual(ids(store.CURRENT_SEED_SCHEMA));
    expect(store.SEED_ID_FORMAT_VERSION).toBe(1);
  });

  it('seeds when nothing is stored and never leaves the namespace dirty', async () => {
    mem.set('kb.dirty.v1', '1'); // stale flag with no board behind it
    const store = await freshStore();
    const { board, seeded } = new store.LocalStore().loadOrSeed();
    expect(seeded).toBe(true);
    expect(board.title).toBe('kb');
    expect(store.loadDirty()).toBe(false);
  });

  it('returns the stored board untouched', async () => {
    mem.set('kb.board.v1', JSON.stringify({ title: 'real', tasks: [] }));
    mem.set('kb.dirty.v1', '1');
    const store = await freshStore();
    const { board, seeded } = new store.LocalStore().loadOrSeed();
    expect(seeded).toBe(false);
    expect(board.title).toBe('real');
    expect(store.loadDirty()).toBe(true);
  });

  it('persists board and canonical ids in one envelope across reconstruction', async () => {
    const first = await freshStore();
    const board = first.seedBoard();
    const ids = new Map([[board.tasks[0]!.id, 'server-a']]);
    await new first.LocalStore().save(board, ids);

    const second = await freshStore();
    const loaded = new second.LocalStore().loadOrSeed();
    expect(loaded.board).toEqual(board);
    expect([...loaded.canonicalTaskIDs]).toEqual([...ids]);
    expect(loaded.migratedRaw).toBe(false);
  });

  it('persists a deterministic seed and later canonical acknowledgement unchanged', async () => {
    const first = await freshStore();
    const board = first.seedBoard('canonical-seed');
    const ids = new Map([[board.tasks[0]!.id, 'server-seed-a']]);
    const local = new first.LocalStore('canonical-seed');
    const saved = await local.save(board, ids);
    expect(saved.ok).toBe(true);

    const second = await freshStore();
    const loaded = new second.LocalStore('canonical-seed').loadSnapshot();
    expect(loaded.board).toEqual(board);
    expect([...loaded.canonicalTaskIDs]).toEqual([...ids]);
    expect(loaded.seeded).toBe(false);
  });

  it('leaves a previously stored random-id raw board untouched', async () => {
    const old = {
      title: 'old raw board',
      tasks: [{
        id: 'random-id-from-an-older-release',
        emoji: '',
        title: 'existing task',
        desc: '',
        status: 'todo' as const,
        blocked: false,
        prio: 3 as const,
        tags: [],
        checks: [],
        createdAt: '2024-01-02T03:04:05.000Z',
        movedAt: '2024-01-02T03:04:05.000Z',
      }],
    };
    const store = await freshStore();
    mem.set(store.namespaceStorageKey('kb.board.v1', 'old-random'), JSON.stringify(old));

    expect(new store.LocalStore('old-random').loadSnapshot().board).toEqual(old);
  });

  it('keeps raw-board recovery durable until an acknowledged completion', async () => {
    mem.set('kb.board.v1', JSON.stringify({ title: 'legacy', tasks: [] }));
    const first = await freshStore();
    expect(new first.LocalStore().loadOrSeed().migratedRaw).toBe(true);

    const second = await freshStore();
    const reconstructed = new second.LocalStore();
    const loaded = reconstructed.loadOrSeed();
    expect(loaded.migratedRaw).toBe(true);
    await reconstructed.completeIdentityRecovery(
      loaded.board,
      new Map(),
      loaded.deletedCanonicalIDs,
      loaded.version,
    );

    const third = await freshStore();
    expect(new third.LocalStore().loadOrSeed().migratedRaw).toBe(false);
  });

  it.each([
    ['invalid status', (envelope: any) => { envelope.board.tasks[0].status = 'later'; }],
    ['missing field', (envelope: any) => { delete envelope.board.tasks[0].desc; }],
    ['malformed tags', (envelope: any) => { envelope.board.tasks[0].tags = ['ok', 7]; }],
    ['malformed checks', (envelope: any) => {
      envelope.board.tasks[0].checks = [{ text: 'x', done: 'yes' }];
    }],
    ['empty client id', (envelope: any) => { envelope.board.tasks[0].id = ''; }],
    ['duplicate client ids', (envelope: any) => {
      envelope.board.tasks[1].id = envelope.board.tasks[0].id;
    }],
    ['orphan map key', (envelope: any) => {
      envelope.canonical_ids.orphan = 'server-orphan';
    }],
    ['empty canonical id', (envelope: any) => {
      envelope.canonical_ids[envelope.board.tasks[0].id] = '';
    }],
    ['duplicate canonical ids', (envelope: any) => {
      envelope.canonical_ids[envelope.board.tasks[1].id] = 'server-a';
    }],
    ['empty map key', (envelope: any) => {
      envelope.canonical_ids[''] = 'server-empty-client';
    }],
    ['non-array deletion evidence', (envelope: any) => {
      envelope.deleted_canonical_ids = 'server-deleted';
    }],
    ['duplicate deletion evidence', (envelope: any) => {
      envelope.deleted_canonical_ids = ['server-deleted', 'server-deleted'];
    }],
    ['empty deletion evidence', (envelope: any) => {
      envelope.deleted_canonical_ids = [''];
    }],
    ['live/deleted overlap', (envelope: any) => {
      envelope.deleted_canonical_ids = ['server-a'];
    }],
  ])('rejects an envelope with %s', async (_case, mutate) => {
    const store = await freshStore();
    const board = store.seedBoard();
    const envelope = {
      version: 3,
      board,
      canonical_ids: {
        [board.tasks[0]!.id]: 'server-a',
        [board.tasks[1]!.id]: 'server-b',
      },
    };
    mutate(envelope);
    mem.set('kb.board.v1', JSON.stringify(envelope));
    expect(new store.LocalStore().load()).toBeNull();
  });

  it('retains local deletion evidence across reconstruction and dirty recovery', async () => {
    const firstModule = await freshStore();
    const original = firstModule.seedBoard();
    const ids = new Map(
      original.tasks.map((task, index) => [task.id, `server-${index}`]),
    );
    const firstStore = new firstModule.LocalStore();
    await firstStore.save(original, ids);
    const deletedTitle = original.tasks[1]!.title;
    const local = {
      ...original,
      tasks: original.tasks.filter((task) => task.id !== original.tasks[1]!.id),
    };
    await firstStore.save(local, ids);

    const persisted = JSON.parse(mem.get(
      firstModule.namespaceStorageKey('kb.board.v1', 'default'),
    )!) as {
      canonical_ids: Record<string, string>;
      deleted_canonical_ids: string[];
    };
    expect(persisted.canonical_ids[original.tasks[1]!.id]).toBeUndefined();
    expect(persisted.deleted_canonical_ids).toContain('server-1');

    const secondModule = await freshStore();
    const reconstructed = new secondModule.LocalStore();
    const loaded = reconstructed.loadOrSeed();
    expect(loaded.deletedCanonicalIDs).toContain('server-1');

    const { serialize } = await import('./markdown');
    const { RemoteStore } = await import('./remote');
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({
        board: serialize(original),
        task_ids: original.tasks.map((_task, index) => `server-${index}`),
      }),
      { headers: { 'Content-Type': 'application/json', ETag: 'v2' } },
    ))));
    try {
      const prepared = await new RemoteStore().prepareDirtyMapped(
        { kind: 'manual', id: 'alice' },
        loaded.board,
        loaded.canonicalTaskIDs,
        loaded.deletedCanonicalIDs,
      );
      expect(prepared.board.tasks.map((task) => task.title)).not.toContain(
        deletedTitle,
      );
      expect(prepared.deletedCanonicalIDs).toContain('server-1');
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('loads one complete snapshot without writes and rejects malformed state explicitly', async () => {
    const storeModule = await freshStore();
    const board = storeModule.seedBoard();
    const key = storeModule.namespaceStorageKey('kb.board.v1', 'snapshot');
    const pending = {
      operation_id: '6fa459ea-ee8a-3ca4-894e-db77e160355e',
      body: '{"board":"exact","task_ids":[]}',
      sent_board: board,
      sent_canonical_ids: {},
      if_match: 'v1',
    };
    const values = new Map([[key, JSON.stringify({
      version: 3,
      generation: 7,
      board,
      canonical_ids: {},
      deleted_canonical_ids: ['server-deleted'],
      identity_recovery_needed: true,
      pending_board_write: pending,
    })]]);
    const setItem = vi.fn((storageKey: string, value: string) => {
      values.set(storageKey, value);
    });
    const storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (storageKey: string) => values.get(storageKey) ?? null,
      key: (index: number) => [...values.keys()][index] ?? null,
      removeItem: (storageKey: string) => { values.delete(storageKey); },
      setItem,
    } as Storage;
    const local = new storeModule.LocalStore('snapshot', { storage, locks: null });
    expect(local.loadSnapshot()).toMatchObject({
      board,
      generation: 7,
      migratedRaw: true,
      pendingBoardWrite: pending,
    });
    expect(local.loadSnapshot()?.deletedCanonicalIDs).toEqual(new Set(['server-deleted']));
    expect(setItem).not.toHaveBeenCalled();

    values.set(key, '{"version":3,"board":{"title":"bad","tasks":[]},"canonical_ids":[],"generation":7}');
    expect(() => local.loadSnapshot()).toThrow('stored board envelope is invalid');
    expect(setItem).not.toHaveBeenCalled();
  });

  it('preserves exact bytes on stale CAS and succeeds only against the current generation', async () => {
    const storeModule = await freshStore();
    const local = new storeModule.LocalStore('strict-cas');
    const board = storeModule.seedBoard();
    const clientID = board.tasks[0]!.id;
    const initial = await local.save(
      board,
      new Map([[clientID, 'server-a']]),
      new Set(['server-deleted']),
    );
    if (!initial.ok) throw initial.error;
    const key = storeModule.namespaceStorageKey('kb.board.v1', 'strict-cas');
    const before = mem.get(key)!;
    const changed = { ...board, title: 'current only' };
    expect(await local.saveIfGeneration(
      changed,
      new Map([[clientID, 'server-a']]),
      new Set(['server-deleted']),
      presentVersion(initial.generation - 1),
    )).toMatchObject({ ok: false, conflict: true, currentGeneration: initial.generation });
    expect(mem.get(key)).toBe(before);

    expect(await local.saveIfGeneration(
      changed,
      new Map([[clientID, 'server-a']]),
      new Set(['server-deleted']),
      initial.snapshot.version,
    )).toMatchObject({ ok: true, generation: initial.generation + 1 });
    expect(local.loadSnapshot()).toMatchObject({ board: changed, generation: initial.generation + 1 });
    expect(local.loadSnapshot()?.deletedCanonicalIDs).toEqual(new Set(['server-deleted']));
  });

  it('distinguishes virtual absence from a stored generation-zero board', async () => {
    const storeModule = await freshStore();
    const absent = new storeModule.LocalStore('absent-version');
    const virtual = absent.loadSnapshot();
    expect(virtual.version).toEqual({ present: false, generation: 0 });
    expect(absent.loadSnapshot().board).toBe(virtual.board);
    const absentKey = storeModule.namespaceStorageKey('kb.board.v1', 'absent-version');
    expect(await absent.saveIfGeneration(
      virtual.board,
      virtual.canonicalTaskIDs,
      virtual.deletedCanonicalIDs,
      { present: true, generation: 0 },
    )).toMatchObject({ ok: false, conflictKind: 'durable' });
    expect(mem.has(absentKey)).toBe(false);
    const created = await absent.saveIfGeneration(
      virtual.board,
      virtual.canonicalTaskIDs,
      virtual.deletedCanonicalIDs,
      virtual.version,
    );
    expect(created).toMatchObject({
      ok: true,
      generation: 1,
      snapshot: { version: { present: true, generation: 1 } },
    });

    const rawBoard = { title: 'stored zero', tasks: [] };
    const rawKey = storeModule.namespaceStorageKey('kb.board.v1', 'stored-zero');
    mem.set(rawKey, JSON.stringify(rawBoard));
    const stored = new storeModule.LocalStore('stored-zero');
    const storedSnapshot = stored.loadSnapshot();
    expect(storedSnapshot.version).toEqual({ present: true, generation: 0 });
    expect(await stored.saveIfGeneration(
      rawBoard,
      new Map(),
      new Set(),
      { present: false, generation: 0 },
    )).toMatchObject({ ok: false, conflictKind: 'durable' });
    expect(mem.get(rawKey)).toBe(JSON.stringify(rawBoard));
  });

  it('checks the live guard inside a deferred lock and performs zero writes on cleanup', async () => {
    const storeModule = await freshStore();
    let release!: () => void;
    const waiting = new Promise<void>((resolve) => { release = resolve; });
    const locks = {
      request: async <T>(
        _name: string,
        _options: LockOptions,
        callback: () => T | Promise<T>,
      ) => {
        await waiting;
        return callback();
      },
    };
    const local = new storeModule.LocalStore('live-lock', {
      locks: locks as unknown as LockManager,
    });
    const base = local.loadSnapshot();
    let live = true;
    const saving = local.saveIfGeneration(
      { ...base.board, title: 'must not commit' },
      base.canonicalTaskIDs,
      base.deletedCanonicalIDs,
      base.version,
      () => live,
    );
    live = false;
    release();
    expect(await saving).toMatchObject({
      ok: false,
      conflict: true,
      conflictKind: 'live',
    });
    expect(mem.has(storeModule.namespaceStorageKey('kb.board.v1', 'live-lock'))).toBe(false);
  });
});
