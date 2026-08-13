import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  bumpShipped,
  legacyNamespaceStorageKey,
  migrateLegacyKeys,
  namespaceStorageKey,
  shipKey,
  shippedToday,
  unshipToday,
} from './store';

/** A Storage stand-in whose failures are the point of several cases below. */
function memoryStorage(fail: { read?: boolean; write?: boolean } = {}): Storage {
  const map = new Map<string, string>();
  return {
    get length() {
      return map.size;
    },
    key: (i: number) => [...map.keys()][i] ?? null,
    getItem: (key: string) => {
      if (fail.read) throw new Error('storage denied');
      return map.get(key) ?? null;
    },
    setItem: (key: string, value: string) => {
      if (fail.write) throw new Error('quota exceeded');
      map.set(key, String(value));
    },
    removeItem: (key: string) => void map.delete(key),
    clear: () => map.clear(),
  } as Storage;
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('namespaced storage keys', () => {
  it('frames the namespace with its own length so no prefix can be consumed', () => {
    expect(namespaceStorageKey('kb.streak.v1', 'alice')).toBe(
      'kb.streak.v1.ns.5:alice',
    );
    expect(namespaceStorageKey('kb.streak.v1', 'alice.work')).toBe(
      'kb.streak.v1.ns.10:alice.work',
    );
    // Framing is injective: 'alice' can never produce 'alice.work''s key.
    expect(namespaceStorageKey('kb.streak.v1', 'alice')).not.toBe(
      namespaceStorageKey('kb.streak.v1', 'alice.work'),
    );
    expect(namespaceStorageKey('kb.streak.v1', 'a b')).toBe(
      'kb.streak.v1.ns.5:a%20b',
    );
    expect(namespaceStorageKey('kb.outbox.v1', 'alice', 'item')).toBe(
      'kb.outbox.v1.ns.5:alice.item',
    );
  });

  it('reproduces the pre-framing key shape, with default un-suffixed', () => {
    expect(legacyNamespaceStorageKey('kb.streak.v1', 'default')).toBe('kb.streak.v1');
    expect(legacyNamespaceStorageKey('kb.streak.v1', 'alice')).toBe(
      'kb.streak.v1.alice',
    );
    expect(legacyNamespaceStorageKey('kb.streak.v1', 'alice', 'x')).toBe(
      'kb.streak.v1.alice.x',
    );
  });
});

describe('migrateLegacyKeys', () => {
  it('copies the pre-rename value once and leaves the old key in place', () => {
    const storage = memoryStorage();
    storage.setItem('webtui.streak.v1', 'old');
    migrateLegacyKeys(storage, 'flag', ['kb.streak.v1', 'other.key']);
    expect(storage.getItem('kb.streak.v1')).toBe('old');
    expect(storage.getItem('webtui.streak.v1')).toBe('old');
    expect(storage.getItem('flag')).toBe('1');

    // A second run must not resurrect state the app has since removed.
    storage.removeItem('kb.streak.v1');
    migrateLegacyKeys(storage, 'flag', ['kb.streak.v1']);
    expect(storage.getItem('kb.streak.v1')).toBeNull();
  });

  it('never overwrites a value that is already there', () => {
    const storage = memoryStorage();
    storage.setItem('webtui.streak.v1', 'old');
    storage.setItem('kb.streak.v1', 'current');
    migrateLegacyKeys(storage, 'flag', ['kb.streak.v1']);
    expect(storage.getItem('kb.streak.v1')).toBe('current');
  });

  it('gives up quietly when the storage refuses to answer', () => {
    expect(() => migrateLegacyKeys(memoryStorage({ read: true }), 'flag', ['kb.x']))
      .not.toThrow();
  });
});

describe('the shipped-today tally', () => {
  it('keys on the trimmed title, so a re-read of the same card counts once', () => {
    expect(shipKey({ title: '  Ship it  ' })).toBe('Ship it');
  });

  it('counts each card once, and lets a reopened card leave the tally', () => {
    expect(shippedToday('alice')).toBe(0);
    expect(bumpShipped('alice', 'Ship it')).toBe(1);
    expect(bumpShipped('alice', 'Ship it')).toBe(1);
    expect(bumpShipped('alice', 'Other')).toBe(2);
    expect(shippedToday('alice')).toBe(2);
    expect(unshipToday('alice', 'Ship it')).toBe(1);
    // Unknown keys are a no-op rather than a decrement.
    expect(unshipToday('alice', 'Never shipped')).toBe(1);
  });

  it('keeps namespaces apart', () => {
    bumpShipped('alice', 'Ship it');
    expect(shippedToday('bob')).toBe(0);
  });

  it('reads a record from another day, a malformed one, or none as empty', () => {
    localStorage.setItem(
      namespaceStorageKey('kb.streak.v1', 'stale'),
      JSON.stringify({ date: 'Mon Jan 01 2020', ids: ['Ship it'] }),
    );
    expect(shippedToday('stale')).toBe(0);

    localStorage.setItem(namespaceStorageKey('kb.streak.v1', 'broken'), '{');
    expect(shippedToday('broken')).toBe(0);

    // The pre-rename {date, n} counter shape has no ids array.
    localStorage.setItem(
      namespaceStorageKey('kb.streak.v1', 'counter'),
      JSON.stringify({ date: new Date().toDateString(), n: 4 }),
    );
    expect(shippedToday('counter')).toBe(0);
  });

  it('drops non-string entries from a stored record', () => {
    localStorage.setItem(
      namespaceStorageKey('kb.streak.v1', 'mixed'),
      JSON.stringify({ date: new Date().toDateString(), ids: ['Ship it', 7] }),
    );
    expect(shippedToday('mixed')).toBe(1);
  });

  it('migrates a pre-framing tally to the framed key exactly once', () => {
    localStorage.setItem(
      legacyNamespaceStorageKey('kb.streak.v1', 'legacy'),
      JSON.stringify({ date: new Date().toDateString(), ids: ['Ship it'] }),
    );
    expect(shippedToday('legacy')).toBe(1);
    expect(localStorage.getItem(namespaceStorageKey('kb.streak.v1', 'legacy')))
      .not.toBeNull();
  });

  it('lives in memory for the session when storage is unavailable', () => {
    vi.stubGlobal('localStorage', memoryStorage({ read: true, write: true }));
    expect(shippedToday('denied')).toBe(0);
    expect(bumpShipped('denied', 'Ship it')).toBe(1);
  });
});
