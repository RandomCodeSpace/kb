import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Identity } from './auth';
import {
  claimLegacyAzureNamespace,
  displayName,
  identityNamespace,
  isAuthRedirect,
  loadIdentity,
  ReauthRequiredError,
  sanitizeUser,
  saveIdentity,
} from './auth';
import { namespaceStorageKey } from './store';

const IDENTITY_KEY = 'kb.identity.v1';
const TOKEN_KEY = 'kb.serverToken.v1';

// Node test environment has no Web Storage — stub both stores on globalThis.
function stubStorage(mem: Map<string, string>): Storage {
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
    key: (index: number) => [...mem.keys()][index] ?? null,
    get length() {
      return mem.size;
    },
  } as Storage;
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
  vi.doUnmock('@azure/msal-browser');
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

describe('displayName', () => {
  it('shows the Entra display name, not the email', () => {
    expect(
      displayName({ kind: 'azure', id: 'a.person@example.com', name: 'A Person' }),
    ).toBe('A Person');
  });

  it('falls back to the email when there is no name claim', () => {
    expect(displayName({ kind: 'azure', id: 'a.person@example.com' })).toBe(
      'a.person@example.com',
    );
    expect(
      displayName({ kind: 'azure', id: 'a.person@example.com', name: '  ' }),
    ).toBe('a.person@example.com');
  });

  it('shows a manual identity exactly as the user typed it', () => {
    expect(displayName({ kind: 'manual', id: 'alice', name: 'Ignored' })).toBe(
      'alice',
    );
  });

  it('does not change the stored identity', () => {
    const identity: Identity = {
      kind: 'azure',
      id: 'a.person@example.com',
      name: 'A Person',
    };
    saveIdentity(identity);
    displayName(identity);
    // The server keys boards on the id, so it must survive untouched.
    expect(loadIdentity()?.id).toBe('a.person@example.com');
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

  it('round-trips an immutable Azure homeAccountId', () => {
    const identity: Identity = {
      kind: 'azure',
      id: 'alice@example.com',
      homeAccountId: 'home.alice',
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

describe('Azure immutable account identity', () => {
  const account = (homeAccountId: string, username: string) => ({
    homeAccountId,
    username,
    localAccountId: homeAccountId,
    environment: 'login.test',
    tenantId: 'tenant',
  });

  async function mockedAuth(accounts: ReturnType<typeof account>[]) {
    const acquireTokenSilent = vi.fn(async () => ({ idToken: 'id-token' }));
    const app = {
      initialize: vi.fn(async () => undefined),
      getAllAccounts: vi.fn(() => accounts),
      getActiveAccount: vi.fn(() => accounts.at(-1) ?? null),
      acquireTokenSilent,
      setActiveAccount: vi.fn(),
      loginPopup: vi.fn(async () => ({
        account: accounts[0] ?? null,
        idTokenClaims: { email: accounts[0]?.username },
      })),
    };
    vi.doMock('@azure/msal-browser', () => ({
      PublicClientApplication: class {
        constructor() {
          return app;
        }
      },
      InteractionRequiredAuthError: class extends Error {},
    }));
    vi.stubGlobal('window', { location: { origin: 'https://kb.test' } });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({ azure_client_id: 'client', azure_tenant_id: 'tenant' }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      ),
    );
    return { auth: await freshAuth(), acquireTokenSilent, app };
  }

  it('uses homeAccountId as the Azure storage namespace without lossy folding', () => {
    expect(
      identityNamespace({
        kind: 'azure', id: 'same@example.com', homeAccountId: 'A/B',
      }),
    ).toBe('azure.A%2FB');
    expect(
      identityNamespace({
        kind: 'azure', id: 'same@example.com', homeAccountId: 'A-B',
      }),
    ).toBe('azure.A-B');
    expect(identityNamespace({ kind: 'manual', id: 'Alice' })).toBe('alice');
  });

  it('returns a newly signed-in identity with its immutable account key', async () => {
    const locks = serialLockForIdentityTest();
    vi.stubGlobal('navigator', { locks });
    const { auth } = await mockedAuth([
      account('home-alice', 'alice@example.com'),
    ]);
    await expect(auth.signInAzure()).resolves.toEqual(
      expect.objectContaining({
        kind: 'azure',
        id: 'alice@example.com',
        homeAccountId: 'home-alice',
      }),
    );
  });

  it('never falls back to an active or first cached account', async () => {
    const { auth, acquireTokenSilent, app } = await mockedAuth([
      account('home-bob', 'bob@example.com'),
    ]);
    await expect(
      auth.getApiToken({
        kind: 'azure', id: 'alice@example.com', homeAccountId: 'home-alice',
      }),
    ).rejects.toBeInstanceOf(auth.ReauthRequiredError);
    expect(acquireTokenSilent).not.toHaveBeenCalled();
    expect(app.getActiveAccount).not.toHaveBeenCalled();
  });

  it('migrates a legacy email identity only when exactly one account matches', async () => {
    const { auth, acquireTokenSilent } = await mockedAuth([
      account('home-alice', 'ALICE@example.com'),
      account('home-bob', 'bob@example.com'),
    ]);
    const token = await auth.getApiToken({ kind: 'azure', id: 'alice@example.com' });
    expect(token).toBe('id-token');
    expect(acquireTokenSilent).toHaveBeenCalledWith(
      expect.objectContaining({ account: expect.objectContaining({ homeAccountId: 'home-alice' }) }),
    );
    expect(auth.loadIdentity()).toEqual(
      expect.objectContaining({ homeAccountId: 'home-alice' }),
    );
  });

  it.each([
    ['zero', [account('home-bob', 'bob@example.com')]],
    [
      'multiple',
      [
        account('home-a1', 'alice@example.com'),
        account('home-a2', 'ALICE@example.com'),
      ],
    ],
  ])('rejects %s legacy username matches before requesting a token', async (_name, accounts) => {
    const { auth, acquireTokenSilent } = await mockedAuth(accounts);
    await expect(
      auth.getApiToken({ kind: 'azure', id: 'alice@example.com' }),
    ).rejects.toBeInstanceOf(auth.ReauthRequiredError);
    expect(acquireTokenSilent).not.toHaveBeenCalled();
    expect(auth.loadIdentity()).toBeNull();
  });
});

function serialLockForIdentityTest() {
  let tail = Promise.resolve<unknown>(undefined);
  return {
    request: vi.fn((_name: string, callback: () => Promise<unknown>) => {
      const run = tail.then(callback);
      tail = run.catch(() => undefined);
      return run;
    }),
  };
}

describe('legacy Azure namespace claim', () => {
  function serialLocks() {
    return serialLockForIdentityTest();
  }

  it('serializes same-email different-account claims and copies all namespace shapes once', async () => {
    mem.set('kb.board.v1.same@example.com', 'board');
    mem.set('kb.dirty.v1.same@example.com', '1');
    const outboxSuffix = encodeURIComponent('tombstone:client-1');
    mem.set(`kb.outbox.v1.same@example.com.${outboxSuffix}`, JSON.stringify({
      version: 1,
      generation: 'g1',
      kind: 'tombstone',
      state: 'awaiting_canonical',
      clientTaskId: 'client-1',
      reason: 'obsolete',
    }));
    const locks = serialLocks();
    vi.stubGlobal('navigator', { locks });

    const first = claimLegacyAzureNamespace('same@example.com', 'home-one');
    const second = claimLegacyAzureNamespace('same@example.com', 'home-two');
    await expect(first).resolves.toBeUndefined();
    await expect(second).rejects.toBeInstanceOf(ReauthRequiredError);

    expect(mem.get(namespaceStorageKey(
      'kb.azure-namespace-claim.v1', 'same@example.com',
    ))).toBe('home-one');
    expect(mem.get(namespaceStorageKey('kb.board.v1', 'azure.home-one'))).toBe('board');
    expect(mem.get(namespaceStorageKey('kb.dirty.v1', 'azure.home-one'))).toBe('1');
    expect(mem.get(namespaceStorageKey(
      'kb.outbox.v1', 'azure.home-one', outboxSuffix,
    ))).not.toBeNull();
    expect(mem.has(namespaceStorageKey('kb.board.v1', 'azure.home-two'))).toBe(false);
    expect(mem.get('kb.board.v1.same@example.com')).toBe('board');
  });

  it('persists the owner before copy and resumes an interrupted same-owner claim', async () => {
    mem.set('kb.board.v1.alice@example.com', 'board');
    mem.set('kb.dirty.v1.alice@example.com', '1');
    const writes: string[] = [];
    let failDirty = true;
    const storage = stubStorage(mem);
    vi.stubGlobal('localStorage', {
      ...storage,
      setItem(key: string, value: string) {
        writes.push(key);
        if (failDirty && key === namespaceStorageKey('kb.dirty.v1', 'azure.home-alice')) {
          failDirty = false;
          throw new Error('interrupted');
        }
        mem.set(key, String(value));
      },
    });
    vi.stubGlobal('navigator', { locks: serialLocks() });

    await expect(
      claimLegacyAzureNamespace('alice@example.com', 'home-alice'),
    ).rejects.toThrow('interrupted');
    expect(writes[0]).toBe(namespaceStorageKey(
      'kb.azure-namespace-claim.v1', 'alice@example.com',
    ));
    await expect(
      claimLegacyAzureNamespace('alice@example.com', 'home-alice'),
    ).resolves.toBeUndefined();
    expect(mem.get(namespaceStorageKey('kb.board.v1', 'azure.home-alice'))).toBe('board');
    expect(mem.get(namespaceStorageKey('kb.dirty.v1', 'azure.home-alice'))).toBe('1');
    expect(mem.get('kb.board.v1.alice@example.com')).toBe('board');
  });

  it('fails closed without Web Locks and leaves legacy data untouched', async () => {
    mem.set('kb.board.v1.alice@example.com', 'board');
    vi.stubGlobal('navigator', {});
    await expect(
      claimLegacyAzureNamespace('alice@example.com', 'home-alice'),
    ).rejects.toThrow(/import it manually/);
    expect(mem.get('kb.board.v1.alice@example.com')).toBe('board');
    expect(mem.has(namespaceStorageKey('kb.board.v1', 'azure.home-alice'))).toBe(false);
    expect(mem.has(namespaceStorageKey(
      'kb.azure-namespace-claim.v1', 'alice@example.com',
    ))).toBe(false);
  });

  it('does not claim dotted-prefix legacy keys or adversarial outbox records', async () => {
    mem.set('kb.board.v1.alice', 'alice-board');
    mem.set('kb.board.v1.alice.work', 'work-board');
    const aliceSuffix = encodeURIComponent('tombstone:alice-task');
    const workSuffix = encodeURIComponent('tombstone:work-task');
    const record = (clientTaskId: string) => JSON.stringify({
      version: 1, generation: clientTaskId, kind: 'tombstone',
      state: 'awaiting_canonical', clientTaskId, reason: 'obsolete',
    });
    mem.set(`kb.outbox.v1.alice.${aliceSuffix}`, record('alice-task'));
    mem.set(`kb.outbox.v1.alice.work.${workSuffix}`, record('work-task'));
    // Looks like alice by prefix, but its payload proves it belongs elsewhere.
    mem.set(`kb.outbox.v1.alice.${workSuffix}`, record('different-task'));
    vi.stubGlobal('navigator', { locks: serialLocks() });

    await claimLegacyAzureNamespace('alice', 'home-alice');

    expect(mem.get(namespaceStorageKey('kb.board.v1', 'azure.home-alice')))
      .toBe('alice-board');
    expect(mem.get(namespaceStorageKey(
      'kb.outbox.v1', 'azure.home-alice', aliceSuffix,
    ))).toBe(record('alice-task'));
    expect(mem.has(namespaceStorageKey(
      'kb.outbox.v1', 'azure.home-alice', workSuffix,
    ))).toBe(false);
    expect(mem.get('kb.board.v1.alice.work')).toBe('work-board');
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

describe('isAuthRedirect', () => {
  it('is false for a normal load', () => {
    expect(isAuthRedirect('', '')).toBe(false);
    expect(isAuthRedirect('?debug=1', '')).toBe(false);
  });

  it('is true for an Entra code response', () => {
    expect(isAuthRedirect('?code=abc&state=xyz', '')).toBe(true);
  });

  it('is true for an Entra error response', () => {
    expect(isAuthRedirect('?error=access_denied&state=xyz', '')).toBe(true);
  });

  it('reads a fragment response too', () => {
    expect(isAuthRedirect('', '#code=abc&state=xyz')).toBe(true);
  });

  it('ignores an unrelated code parameter with no state', () => {
    expect(isAuthRedirect('?code=PROMO2026', '')).toBe(false);
  });
});
