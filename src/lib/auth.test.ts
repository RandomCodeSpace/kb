import { beforeEach, describe, expect, it } from 'vitest';
import type { Identity } from './auth';
import { loadIdentity, sanitizeUser, saveIdentity } from './auth';

const IDENTITY_KEY = 'webtui.identity.v1';
const TOKEN_KEY = 'webtui.serverToken.v1';

// Node test environment has no Web Storage — stub both stores on globalThis.
function stubStorage(mem: Map<string, string>): unknown {
  return {
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
}

const mem = new Map<string, string>();
const sessionMem = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = stubStorage(mem);
(globalThis as { sessionStorage?: unknown }).sessionStorage =
  stubStorage(sessionMem);

beforeEach(() => {
  mem.clear();
  sessionMem.clear();
});

describe('sanitizeUser', () => {
  it('lowercases', () => {
    expect(sanitizeUser('Alice@Example.COM')).toBe('alice@example.com');
  });

  it('keeps the allowed charset [a-z0-9._@-] untouched', () => {
    expect(sanitizeUser('a.b_c@d-e9')).toBe('a.b_c@d-e9');
  });

  it("replaces every disallowed char with '-'", () => {
    expect(sanitizeUser('a b!c#d')).toBe('a-b-c-d');
  });

  it("neutralizes traversal '../x' (no separators, no leading dot)", () => {
    const s = sanitizeUser('../x');
    expect(s).not.toContain('/');
    expect(s.startsWith('.')).toBe(false);
    expect(s).toBe('-x');
  });

  it("neutralizes 'a/b'", () => {
    expect(sanitizeUser('a/b')).toBe('a-b');
  });

  it("neutralizes backslash traversal '..\\x'", () => {
    expect(sanitizeUser('..\\x')).toBe('-x');
  });

  it("maps empty input to 'default'", () => {
    expect(sanitizeUser('')).toBe('default');
  });

  it("maps dot-only input to 'default'", () => {
    expect(sanitizeUser('...')).toBe('default');
  });
});

describe('loadIdentity', () => {
  it('returns null when nothing is stored', () => {
    expect(loadIdentity()).toBeNull();
  });

  it('returns null on garbage JSON', () => {
    mem.set(IDENTITY_KEY, '{not json!!');
    expect(loadIdentity()).toBeNull();
  });

  it('returns null on non-object JSON', () => {
    mem.set(IDENTITY_KEY, JSON.stringify(42));
    expect(loadIdentity()).toBeNull();
  });

  it('returns null when kind is invalid', () => {
    mem.set(IDENTITY_KEY, JSON.stringify({ kind: 'nope', id: 'x' }));
    expect(loadIdentity()).toBeNull();
  });

  it('returns null when id is missing or empty', () => {
    mem.set(IDENTITY_KEY, JSON.stringify({ kind: 'azure' }));
    expect(loadIdentity()).toBeNull();
    mem.set(IDENTITY_KEY, JSON.stringify({ kind: 'manual', id: '   ' }));
    expect(loadIdentity()).toBeNull();
  });

  it('returns null when optional fields have wrong types', () => {
    mem.set(IDENTITY_KEY, JSON.stringify({ kind: 'manual', id: 'a', name: 7 }));
    expect(loadIdentity()).toBeNull();
  });

  it('ignores a legacy serverToken persisted in localStorage', () => {
    mem.set(
      IDENTITY_KEY,
      JSON.stringify({ kind: 'manual', id: 'a', serverToken: 'legacy' }),
    );
    expect(loadIdentity()?.serverToken).toBeUndefined();
  });

  it('round-trips a saved identity', () => {
    const identity: Identity = {
      kind: 'manual',
      id: 'alice@example.com',
      serverToken: 'secret',
    };
    saveIdentity(identity);
    expect(loadIdentity()).toEqual(identity);
  });

  it('keeps the server token out of localStorage (sessionStorage only)', () => {
    saveIdentity({ kind: 'manual', id: 'a', serverToken: 'secret' });
    expect(mem.get(IDENTITY_KEY)).not.toContain('secret');
    expect(sessionMem.get(TOKEN_KEY)).toBe('secret');
    // Simulate a fresh browser session: the identity survives, the token dies.
    sessionMem.clear();
    expect(loadIdentity()).toEqual({ kind: 'manual', id: 'a' });
  });
});
