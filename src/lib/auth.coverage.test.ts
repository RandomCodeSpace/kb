import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  azureAvailable,
  claimLegacyAzureNamespace,
  azureConfigured,
  getApiToken,
  identityNamespace,
  isAuthRedirect,
  loadIdentity,
  ReauthRequiredError,
  resolveAzureIdentity,
} from './auth';
import { legacyNamespaceStorageKey, namespaceStorageKey } from './store';

function storage(mem: Map<string, string>): Storage {
  return {
    getItem: (key) => mem.get(key) ?? null,
    setItem: (key, value) => { mem.set(key, String(value)); },
    removeItem: (key) => { mem.delete(key); },
    clear: () => { mem.clear(); },
    key: (index) => [...mem.keys()][index] ?? null,
    get length() { return mem.size; },
  } as Storage;
}

const local = new Map<string, string>();
const session = new Map<string, string>();

beforeEach(() => {
  local.clear();
  session.clear();
  vi.stubGlobal('localStorage', storage(local));
  vi.stubGlobal('sessionStorage', storage(session));
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.doUnmock('@azure/msal-browser');
});

async function freshAuthWithApp(app: Record<string, unknown>) {
  vi.resetModules();
  vi.doMock('@azure/msal-browser', () => ({
    PublicClientApplication: class { constructor() { return app; } },
    InteractionRequiredAuthError: class extends Error {},
  }));
  vi.stubGlobal('window', { location: { origin: 'https://kb.test' } });
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
    azure_client_id: 'client', azure_tenant_id: 'tenant',
  }), { status: 200 })));
  return import('./auth');
}

describe('identity residual branches', () => {
  it('rejects a persisted empty immutable Azure account id', () => {
    local.set('kb.identity.v1', JSON.stringify({
      kind: 'azure', id: 'alice@example.com', homeAccountId: '  ',
    }));
    expect(loadIdentity()).toBeNull();
  });

  it('loads the manual server token only from session storage', () => {
    local.set('kb.identity.v1', JSON.stringify({
      kind: 'manual', id: 'alice', serverToken: 'persisted-secret',
    }));
    session.set('kb.serverToken.v1', 'session-secret');
    expect(loadIdentity()).toEqual({
      kind: 'manual', id: 'alice', name: undefined, serverToken: 'session-secret',
    });
  });

  it('reports build-time Azure configuration as absent in tests', () => {
    expect(azureConfigured()).toBe(false);
  });

  it('returns already resolved identities without loading MSAL', async () => {
    const manual = { kind: 'manual', id: 'alice' } as const;
    const azure = { kind: 'azure', id: 'alice@example.com', homeAccountId: 'tenant.user' } as const;
    await expect(resolveAzureIdentity(manual)).resolves.toBe(manual);
    await expect(resolveAzureIdentity(azure)).resolves.toBe(azure);
  });

  it('returns a manual API token and honours its absence', async () => {
    await expect(getApiToken({ kind: 'manual', id: 'alice', serverToken: 'token' }))
      .resolves.toBe('token');
    await expect(getApiToken({ kind: 'manual', id: 'alice' })).resolves.toBeNull();
  });

  it('rejects an Azure namespace without an immutable account id', () => {
    expect(() => identityNamespace({ kind: 'azure', id: 'alice@example.com' }))
      .toThrow(ReauthRequiredError);
  });

  it('rejects a blank legacy-claim account id', async () => {
    await expect(claimLegacyAzureNamespace('alice@example.com', '  '))
      .rejects.toBeInstanceOf(ReauthRequiredError);
  });

  it('does nothing when an old namespace has no data', async () => {
    await expect(claimLegacyAzureNamespace('alice@example.com', 'home-1')).resolves.toBeUndefined();
  });

  it('requires a browser lock before copying legacy data', async () => {
    local.set(namespaceStorageKey('kb.board.v1', 'alice@example.com'), 'board');
    vi.stubGlobal('navigator', {});
    await expect(claimLegacyAzureNamespace('alice@example.com', 'home-1'))
      .rejects.toThrow('browser storage lock');
  });

  it('claims and copies framed board and outbox keys non-destructively', async () => {
    const oldNS = 'alice@example.com';
    const newNS = 'azure.home-1';
    const boardKey = namespaceStorageKey('kb.board.v1', oldNS);
    const outboxKey = namespaceStorageKey('kb.outbox.v1', oldNS, 'item');
    local.set(boardKey, 'board');
    local.set(outboxKey, JSON.stringify({ kind: 'import', item: { external_key: 'x:1' } }));
    vi.stubGlobal('navigator', {
      locks: { request: async (_name: string, callback: () => unknown) => callback() },
    });
    await claimLegacyAzureNamespace('alice@example.com', 'home-1');
    expect(local.get(namespaceStorageKey('kb.board.v1', newNS))).toBe('board');
    expect(local.get(namespaceStorageKey('kb.outbox.v1', newNS, 'item'))).toContain('x:1');
  });

  it('copies a provable legacy outbox record', async () => {
    const oldNS = 'alice@example.com';
    const logical = encodeURIComponent('tombstone:client-1');
    const key = legacyNamespaceStorageKey('kb.outbox.v1', oldNS, logical);
    local.set(key, JSON.stringify({ kind: 'tombstone', clientTaskId: 'client-1' }));
    vi.stubGlobal('navigator', {
      locks: { request: async (_name: string, callback: () => unknown) => callback() },
    });
    await claimLegacyAzureNamespace('alice@example.com', 'home-1');
    expect(local.get(namespaceStorageKey('kb.outbox.v1', 'azure.home-1', logical)))
      .toContain('client-1');
  });

  it('rejects a legacy namespace already claimed by another account', async () => {
    const oldNS = 'alice@example.com';
    local.set(namespaceStorageKey('kb.board.v1', oldNS), 'board');
    local.set(namespaceStorageKey('kb.azure-namespace-claim.v1', oldNS), 'other-home');
    vi.stubGlobal('navigator', {
      locks: { request: async (_name: string, callback: () => unknown) => callback() },
    });
    await expect(claimLegacyAzureNamespace('alice@example.com', 'home-1'))
      .rejects.toThrow('belongs to another Microsoft account');
  });

  it('rejects a legacy namespace when storage enumeration fails', async () => {
    vi.stubGlobal('localStorage', {
      ...storage(local),
      get length() { throw new Error('denied'); },
    });
    await expect(claimLegacyAzureNamespace('alice@example.com', 'home-1'))
      .rejects.toThrow('could not be inspected');
  });

  it('ignores an unprovable exact outbox namespace key', async () => {
    local.set(namespaceStorageKey('kb.outbox.v1', 'alice@example.com'), '{');
    await expect(claimLegacyAzureNamespace('alice@example.com', 'home-1')).resolves.toBeUndefined();
  });

  it('rejects an Azure sign-in that returns no account', async () => {
    const app = {
      initialize: vi.fn(), loginPopup: vi.fn(async () => ({ account: null })),
      setActiveAccount: vi.fn(), getAllAccounts: vi.fn(() => []),
    };
    const auth = await freshAuthWithApp(app);
    await expect(auth.signInAzure()).rejects.toThrow('Sign-in returned no account');
  });

  it('uses preferred_username when an Azure token has no email claim', async () => {
    const account = { homeAccountId: 'home-1', username: 'fallback@example.com', name: 'Alice' };
    const app = {
      initialize: vi.fn(),
      loginPopup: vi.fn(async () => ({
        account, idTokenClaims: { preferred_username: 'preferred@example.com' },
      })),
      setActiveAccount: vi.fn(), getAllAccounts: vi.fn(() => [account]),
    };
    vi.stubGlobal('navigator', { locks: { request: async (_: string, fn: () => unknown) => fn() } });
    const auth = await freshAuthWithApp(app);
    await expect(auth.signInAzure()).resolves.toMatchObject({ id: 'preferred@example.com' });
  });

  it('falls back to the account username when Azure claims omit usernames', async () => {
    const account = { homeAccountId: 'home-1', username: 'fallback@example.com' };
    const app = {
      initialize: vi.fn(), loginPopup: vi.fn(async () => ({ account, idTokenClaims: null })),
      setActiveAccount: vi.fn(), getAllAccounts: vi.fn(() => [account]),
    };
    vi.stubGlobal('navigator', { locks: { request: async (_: string, fn: () => unknown) => fn() } });
    const auth = await freshAuthWithApp(app);
    await expect(auth.signInAzure()).resolves.toMatchObject({ id: 'fallback@example.com' });
  });

  it('rejects a legacy Azure match without an immutable account id', async () => {
    const account = { homeAccountId: '', username: 'alice@example.com' };
    const app = {
      initialize: vi.fn(), getAllAccounts: vi.fn(() => [account]),
      acquireTokenSilent: vi.fn(),
    };
    const auth = await freshAuthWithApp(app);
    await expect(auth.getApiToken({ kind: 'azure', id: 'alice@example.com' }))
      .rejects.toBeInstanceOf(auth.ReauthRequiredError);
  });

  it.each([
    ['', '', false],
    ['?code=x', '', false],
    ['?state=s&code=x', '', true],
    ['', '#state=s&error=denied', true],
  ] as const)('classifies auth redirect search=%j hash=%j', (search, hash, expected) => {
    expect(isAuthRedirect(search, hash)).toBe(expected);
  });

  it('reports Azure unavailable when runtime configuration is missing', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url !== '/api/config') throw new Error(`unexpected egress: ${url}`);
      return Promise.resolve(new Response('', { status: 404 }));
    }));
    await expect(azureAvailable()).resolves.toBe(false);
    await expect(resolveAzureIdentity({ kind: 'azure', id: 'alice@example.com' }))
      .rejects.toBeInstanceOf(ReauthRequiredError);
    await expect(getApiToken({ kind: 'azure', id: 'alice@example.com', homeAccountId: 'home-1' }))
      .resolves.toBeNull();
  });
});
