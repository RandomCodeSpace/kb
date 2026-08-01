import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Board, Task } from './model';
import {
  legacyNamespaceStorageKey,
  loadDirty,
  LocalStore,
  migrateLegacyKeys,
  namespaceStorageKey,
  namespaceStorageSuffix,
} from './store';

class MemoryStorage implements Storage {
  private readonly values = new Map<string, string>();
  get length() { return this.values.size; }
  clear() { this.values.clear(); }
  getItem(key: string) { return this.values.get(key) ?? null; }
  key(index: number) { return [...this.values.keys()][index] ?? null; }
  removeItem(key: string) { this.values.delete(key); }
  setItem(key: string, value: string) { this.values.set(key, String(value)); }
}

const locks = {
  request: async (...args: unknown[]) => {
    const callback = args.at(-1) as () => unknown;
    return callback();
  },
} as unknown as LockManager;

function task(id: string): Task {
  return {
    id, title: id, status: 'todo', emoji: '', desc: '', blocked: false, prio: 3,
    tags: [], checks: [], createdAt: '2026-08-01T00:00:00Z', movedAt: '2026-08-01T00:00:00Z',
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('local store residual validation branches', () => {
  it('constructs safely without navigator lock support', () => {
    vi.stubGlobal('navigator', undefined);
    const store = new LocalStore('no-navigator', { storage: new MemoryStorage() });
    expect(store.load()).toBeNull();
  });

  it('uses browser locks when constructor options omit them', () => {
    vi.stubGlobal('navigator', { locks });
    const store = new LocalStore('browser-locks', { storage: new MemoryStorage() });
    expect(store.load()).toBeNull();
  });

  it('treats a browser without Web Locks as unlocked storage', () => {
    vi.stubGlobal('navigator', {});
    const store = new LocalStore('browser-no-locks', { storage: new MemoryStorage() });
    expect(store.load()).toBeNull();
  });

  it('rejects a non-object task in a stored board', () => {
    const storage = new MemoryStorage();
    storage.setItem(namespaceStorageKey('kb.board.v1', 'coverage'), JSON.stringify({
      version: 3, board: { title: 'b', tasks: [7] }, canonical_ids: {},
    }));
    expect(() => new LocalStore('coverage', { storage, locks }).loadSnapshot())
      .toThrow('stored board envelope is invalid');
  });

  it('rejects a stored task missing its final timestamp field', () => {
    const storage = new MemoryStorage();
    const invalid = { ...task('bad'), movedAt: 7 };
    storage.setItem(namespaceStorageKey('kb.board.v1', 'coverage'), JSON.stringify({
      version: 3, board: { title: 'b', tasks: [invalid] }, canonical_ids: {},
    }));
    expect(() => new LocalStore('coverage', { storage, locks }).loadSnapshot())
      .toThrow('stored board envelope is invalid');
  });

  it('parses exact and suffixed framed namespace keys only', () => {
    const exact = namespaceStorageKey('base', 'a.b');
    expect(namespaceStorageSuffix('base', 'a.b', exact)).toBe('');
    expect(namespaceStorageSuffix('base', 'a.b', `${exact}.item`)).toBe('item');
    expect(namespaceStorageSuffix('base', 'a', exact)).toBeNull();
  });

  it('migrates only kb-prefixed keys once without overwriting targets', () => {
    const storage = new MemoryStorage();
    storage.setItem('webtui.board.v1', 'old');
    storage.setItem('kb.keep', 'new');
    storage.setItem('webtui.keep', 'old-keep');
    migrateLegacyKeys(storage, 'flag', ['kb.board.v1', 'other', 'kb.keep']);
    expect(storage.getItem('kb.board.v1')).toBe('old');
    expect(storage.getItem('kb.keep')).toBe('new');
    expect(storage.getItem('flag')).toBe('1');
    storage.setItem('webtui.board.v1', 'changed');
    migrateLegacyKeys(storage, 'flag', ['kb.board.v1']);
    expect(storage.getItem('kb.board.v1')).toBe('old');
  });

  it('constructs the legacy namespace key for default and named users', () => {
    expect(legacyNamespaceStorageKey('kb.board.v1', 'default')).toBe('kb.board.v1');
    expect(legacyNamespaceStorageKey('kb.board.v1', 'alice', 'x')).toBe('kb.board.v1.alice.x');
  });

  it('returns empty identity collections for absent storage', () => {
    const store = new LocalStore('coverage', { storage: new MemoryStorage(), locks: null });
    expect([...store.loadCanonicalTaskIDs()]).toEqual([]);
    expect([...store.loadDeletedCanonicalIDs()]).toEqual([]);
    expect(store.loadPendingBoardWrite()).toBeNull();
  });

  it('treats malformed storage as absent for a lenient load', () => {
    const storage = new MemoryStorage();
    storage.setItem(namespaceStorageKey('kb.board.v1', 'coverage'), '{');
    const store = new LocalStore('coverage', { storage, locks: null });
    expect(store.load()).toBeNull();
  });

  it('rejects malformed storage for a durable snapshot load', () => {
    const storage = new MemoryStorage();
    storage.setItem(namespaceStorageKey('kb.board.v1', 'coverage'), '{');
    const store = new LocalStore('coverage', { storage, locks: null });
    expect(() => store.loadSnapshot()).toThrow();
  });

  it('rejects an envelope with an invalid pending write', () => {
    const storage = new MemoryStorage();
    storage.setItem(namespaceStorageKey('kb.board.v1', 'coverage'), JSON.stringify({
      version: 3,
      board: { title: 'b', tasks: [] },
      canonical_ids: {},
      pending_board_write: { operation_id: '' },
    }));
    expect(() => new LocalStore('coverage', { storage, locks }).loadSnapshot())
      .toThrow('stored board envelope is invalid');
  });

  it('rejects canonical identities for clients absent from the replacement board', async () => {
    const storage = new MemoryStorage();
    const store = new LocalStore('coverage', { storage, locks });
    const board: Board = { title: 'board', tasks: [task('live')] };
    const result = await store.saveIfGeneration(
      board,
      new Map([['missing', 'canonical']]),
      new Set(),
      { present: false, generation: 0 },
    );
    expect(result.ok).toBe(false);
    if (result.ok) throw new Error('expected failure');
    expect(String(result.error)).toContain('replacement canonical identities are invalid');
  });

  it('rejects deletion evidence that is still live', async () => {
    const storage = new MemoryStorage();
    const store = new LocalStore('coverage', { storage, locks });
    const board: Board = { title: 'board', tasks: [task('live')] };
    const result = await store.saveIfGeneration(
      board,
      new Map([['live', 'canonical']]),
      new Set(['canonical']),
      { present: false, generation: 0 },
    );
    expect(result.ok).toBe(false);
    if (result.ok) throw new Error('expected failure');
    expect(String(result.error)).toContain('replacement deletion evidence is invalid');
  });

  it('rejects two clients claiming the same existing canonical identity', async () => {
    const storage = new MemoryStorage();
    const store = new LocalStore('coverage', { storage, locks });
    const firstBoard: Board = { title: 'board', tasks: [task('first')] };
    const saved = await store.save(firstBoard, new Map([['first', 'canonical']]));
    if (!saved.ok) throw saved.error;
    const nextBoard: Board = { title: 'board', tasks: [task('first'), task('second')] };
    const result = await store.saveIfGeneration(
      nextBoard,
      new Map([['first', 'canonical'], ['second', 'canonical']]),
      new Set(),
      saved.snapshot.version,
    );
    expect(result.ok).toBe(false);
    if (result.ok) throw new Error('expected failure');
    expect(String(result.error)).toContain('canonical identity conflict');
  });

  it('rejects conflicting incoming canonical identity during an ordinary save', async () => {
    const storage = new MemoryStorage();
    const store = new LocalStore('coverage', { storage, locks });
    const first = task('first');
    const saved = await store.save({ title: 'board', tasks: [first] }, new Map([['first', 'canonical']]));
    if (!saved.ok) throw saved.error;
    const result = await store.save(
      { title: 'board', tasks: [first, task('second')] },
      new Map([['second', 'canonical']]),
    );
    expect(result.ok).toBe(false);
  });

  it('rejects changing the canonical identity of an existing client', async () => {
    const storage = new MemoryStorage();
    const store = new LocalStore('coverage', { storage, locks });
    const first = task('first');
    const saved = await store.save({ title: 'board', tasks: [first] }, new Map([['first', 'one']]));
    if (!saved.ok) throw saved.error;
    const result = await store.save({ title: 'board', tasks: [first] }, new Map([['first', 'two']]));
    expect(result.ok).toBe(false);
    if (result.ok) throw new Error('expected failure');
    expect(String(result.error)).toContain('canonical identity conflict for first');
  });

  it('rejects staging a pending write before the board exists', async () => {
    const store = new LocalStore('coverage', { storage: new MemoryStorage(), locks });
    const result = await store.stagePendingBoardWrite({
      operation_id: 'op', body: '{}', sent_board: { title: 'b', tasks: [] },
      sent_canonical_ids: {}, if_match: null,
    }, { present: false, generation: 0 });
    expect(result.ok).toBe(false);
  });

  it('rejects identity-recovery completion without a current marker', async () => {
    const store = new LocalStore('coverage', { storage: new MemoryStorage(), locks });
    const result = await store.completeIdentityRecovery(
      { title: 'b', tasks: [] }, new Map(), new Set(),
      { present: false, generation: 0 },
    );
    expect(result).toMatchObject({ ok: false, conflict: true, conflictKind: 'durable' });
  });

  it('treats an unreadable dirty flag as clean', () => {
    vi.stubGlobal('localStorage', {
      length: 0,
      getItem: () => { throw new Error('denied'); },
      setItem: () => { throw new Error('denied'); },
      removeItem: () => { throw new Error('denied'); },
      key: () => null,
    });
    expect(loadDirty('unreadable-coverage')).toBe(false);
  });
});
