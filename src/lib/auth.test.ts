import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Identity } from './auth';
import { loadIdentity, sanitizeUser, saveIdentity } from './auth';

const IDENTITY_KEY = 'kb.identity.v1';
const TOKEN_KEY = 'kb.serverToken.v1';

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

afterEach(() => {
  vi.unstubAllGlobals();
});

/**
 * The legacy-key copy and the /api/config fetch are both cached per module
 * instance, so cases that exercise them import a fresh copy of the module.
 */
async function freshAuth() {
  vi.resetModules();
  return import('./auth');
}

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

describe('legacy webtui.* identity migration', () => {
  it('copies the identity and session token forward, keeping the old keys', async () => {
    mem.set('webtui.identity.v1', JSON.stringify({ kind: 'manual', id: 'a' }));
    sessionMem.set('webtui.serverToken.v1', 'secret');

    const auth = await freshAuth();
    expect(auth.loadIdentity()).toEqual({
      kind: 'manual',
      id: 'a',
      serverToken: 'secret',
    });
    expect(mem.has('webtui.identity.v1')).toBe(true);
    expect(sessionMem.has('webtui.serverToken.v1')).toBe(true);
  });

  it('runs once: signing out is not undone on the next load', async () => {
    mem.set('webtui.identity.v1', JSON.stringify({ kind: 'manual', id: 'a' }));
    const first = await freshAuth();
    expect(first.loadIdentity()).not.toBeNull();
    first.clearIdentity();

    const second = await freshAuth(); // simulates a page reload
    expect(second.loadIdentity()).toBeNull();
  });
});

describe('azureConfig', () => {
  function stubConfigFetch(body: unknown, status = 200) {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(
        new Response(JSON.stringify(body), {
          status,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    return fetchMock;
  }

  it('prefers the ids the server was started with', async () => {
    stubConfigFetch({ azure_client_id: 'cid', azure_tenant_id: 'tid' });
    const auth = await freshAuth();
    expect(await auth.azureConfig()).toEqual({
      clientId: 'cid',
      tenantId: 'tid',
    });
    expect(await auth.azureAvailable()).toBe(true);
  });

  it('fetches /api/config once and caches the answer', async () => {
    const fetchMock = stubConfigFetch({
      azure_client_id: 'cid',
      azure_tenant_id: 'tid',
    });
    const auth = await freshAuth();
    await auth.azureConfig();
    await auth.azureConfig();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/config');
  });

  it('ignores a partial server config (falls back to the build-time env)', async () => {
    stubConfigFetch({ azure_client_id: 'cid', azure_tenant_id: '' });
    const auth = await freshAuth();
    // No VITE_AZURE_* in the test env, so the fallback yields nothing either.
    expect(await auth.azureConfig()).toBeNull();
    expect(await auth.azureAvailable()).toBe(false);
  });

  it('treats a missing endpoint (dev server, old binary) as unconfigured', async () => {
    stubConfigFetch({}, 404);
    const auth = await freshAuth();
    expect(await auth.azureConfig()).toBeNull();
  });

  it('survives a failing fetch', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new Error('offline'))),
    );
    const auth = await freshAuth();
    expect(await auth.azureConfig()).toBeNull();
  });
});
