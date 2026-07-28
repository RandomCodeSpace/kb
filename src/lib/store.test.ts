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

/**
 * The legacy-key copy runs once per module instance, so every case (and every
 * simulated page load) imports a fresh copy of the module.
 */
async function freshStore() {
  vi.resetModules();
  return import('./store');
}

const today = new Date().toDateString();

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

    expect(mem.get('kb.board.v1')).toBe(mem.get('webtui.board.v1'));
    expect(mem.get('kb.dirty.v1')).toBe('1');
    // Rolling back to an older build must still find the data.
    expect(mem.has('webtui.board.v1')).toBe(true);
    expect(mem.has('webtui.dirty.v1')).toBe(true);
    expect(mem.has('webtui.streak.v1')).toBe(true);
  });

  it('migrates a per-user namespace', async () => {
    mem.set('webtui.board.v1.alice', JSON.stringify({ title: 'alice', tasks: [] }));
    const store = await freshStore();
    expect(new store.LocalStore('alice').load()).toEqual({
      title: 'alice',
      tasks: [],
    });
    expect(mem.get('kb.board.v1.alice')).toBe(mem.get('webtui.board.v1.alice'));
  });

  it('never overwrites an existing kb.* value', async () => {
    mem.set('webtui.board.v1', JSON.stringify({ title: 'old', tasks: [] }));
    mem.set('kb.board.v1', JSON.stringify({ title: 'new', tasks: [] }));
    const store = await freshStore();
    expect(new store.LocalStore().load()?.title).toBe('new');
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
});
