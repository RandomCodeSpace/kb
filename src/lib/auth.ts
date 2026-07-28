import type {
  AccountInfo,
  PublicClientApplication,
} from '@azure/msal-browser';
import { migrateLegacyKeys } from './store';

export type Identity = {
  kind: 'azure' | 'manual';
  id: string;
  name?: string;
  serverToken?: string;
};

const IDENTITY_KEY = 'kb.identity.v1';
const TOKEN_KEY = 'kb.serverToken.v1';
const MIGRATED_KEY = 'kb.migrated.identity.v1';
const SCOPES = ['openid', 'profile', 'email'];
const CONFIG_TIMEOUT_MS = 1500;

let migrated = false;

/** Copy the pre-rename `webtui.*` identity/token values forward, once. */
function ensureMigrated(): void {
  if (migrated) return;
  migrated = true;
  try {
    migrateLegacyKeys(localStorage, MIGRATED_KEY, [IDENTITY_KEY]);
  } catch {
    // Storage unavailable — nothing to migrate.
  }
  try {
    migrateLegacyKeys(sessionStorage, MIGRATED_KEY, [TOKEN_KEY]);
  } catch {
    // Storage unavailable — nothing to migrate.
  }
}

/**
 * Thrown when no usable API token can be obtained without user interaction
 * (expired/evicted MSAL session, repeated 401). Callers surface a
 * "session expired — sign in again" state; background code paths must never
 * open an MSAL popup, browsers block popups outside a user gesture.
 */
export class ReauthRequiredError extends Error {
  constructor() {
    super('session expired — sign in again');
    this.name = 'ReauthRequiredError';
  }
}

export function loadIdentity(): Identity | null {
  ensureMigrated();
  try {
    const raw = localStorage.getItem(IDENTITY_KEY);
    if (!raw) return null;
    const v: unknown = JSON.parse(raw);
    if (typeof v !== 'object' || v === null) return null;
    const o = v as Record<string, unknown>;
    if (o.kind !== 'azure' && o.kind !== 'manual') return null;
    if (typeof o.id !== 'string' || o.id.trim() === '') return null;
    if (o.name !== undefined && typeof o.name !== 'string') return null;
    // The shared server token is session-scoped on purpose: it authorizes
    // every user's board, so it never persists in localStorage. A legacy
    // persisted `serverToken` field is deliberately ignored.
    const serverToken = sessionStorage.getItem(TOKEN_KEY) ?? undefined;
    return { kind: o.kind, id: o.id, name: o.name, serverToken };
  } catch {
    return null;
  }
}

export function saveIdentity(i: Identity): void {
  // Migrate first: a copy running afterwards would skip keys we just wrote but
  // could still resurrect a legacy token this call deliberately cleared.
  ensureMigrated();
  const { serverToken, ...persisted } = i;
  try {
    localStorage.setItem(IDENTITY_KEY, JSON.stringify(persisted));
  } catch {
    // Storage unavailable/full — identity stays in memory for this session.
  }
  try {
    // sessionStorage only: the token dies with the session (re-prompt next
    // session) instead of sitting in localStorage for any script to read.
    if (serverToken) sessionStorage.setItem(TOKEN_KEY, serverToken);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    // Storage unavailable — token stays in memory for this session.
  }
}

export function clearIdentity(): void {
  // Same reason as saveIdentity: sign-out must not be undone by a later copy.
  ensureMigrated();
  try {
    localStorage.removeItem(IDENTITY_KEY);
  } catch {
    // Storage unavailable — nothing to clear.
  }
  try {
    sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    // Storage unavailable — nothing to clear.
  }
}

/**
 * What the header shows for an identity. An Entra sign-in carries the
 * account's display name, which is what a person recognises; the raw email is
 * not. Display only — `id` stays exactly what it was, because the server keys
 * boards on it (and, in Azure mode, on the immutable `oid` claim behind it).
 * Falls back to the id when there is no name claim, and a manual identity
 * always shows the id the user typed.
 */
export function displayName(i: Identity): string {
  if (i.kind !== 'azure') return i.id;
  const name = i.name?.trim() ?? '';
  return name === '' ? i.id : name;
}

/**
 * Local-storage namespace for an identity: lowercase, keep [a-z0-9._@-],
 * replace everything else with '-', strip leading dots, empty becomes
 * 'default'. Used only to key local state — the server, by contrast, rejects
 * identities with characters outside the allowed set instead of substituting
 * them (substitution would collapse distinct identities onto one board file).
 */
export function sanitizeUser(id: string): string {
  const cleaned = id
    .toLowerCase()
    .replace(/[^a-z0-9._@-]/g, '-')
    .replace(/^\.+/, '');
  return cleaned === '' ? 'default' : cleaned;
}

export interface AzureConfig {
  clientId: string;
  tenantId: string;
}

/**
 * Build-time config. Only a dev-server fallback: `VITE_*` is baked into the
 * bundle, so a released binary can never pick it up — the server's
 * `GET /api/config` is the runtime source.
 */
function azureEnv(): AzureConfig | null {
  const clientId = import.meta.env.VITE_AZURE_CLIENT_ID as string | undefined;
  const tenantId = import.meta.env.VITE_AZURE_TENANT_ID as string | undefined;
  return clientId && tenantId ? { clientId, tenantId } : null;
}

/** Env-only view; kept for callers that cannot await. Prefer azureAvailable(). */
export function azureConfigured(): boolean {
  return azureEnv() !== null;
}

let serverConfig: Promise<AzureConfig | null> | null = null;

/**
 * The client/tenant ids the server was started with. Both are public by
 * design (they ship in every MSAL SPA bundle) and the endpoint needs no auth —
 * the SPA reads it before login. Any failure (no server, dev mode, 404)
 * resolves to null so the env fallback applies. Cached for the page's life.
 */
function fetchServerConfig(): Promise<AzureConfig | null> {
  serverConfig ??= (async () => {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), CONFIG_TIMEOUT_MS);
    try {
      const res = await fetch('/api/config', { signal: ctrl.signal });
      if (!res.ok) return null;
      const b = (await res.json()) as Record<string, unknown>;
      const clientId =
        typeof b.azure_client_id === 'string' ? b.azure_client_id.trim() : '';
      const tenantId =
        typeof b.azure_tenant_id === 'string' ? b.azure_tenant_id.trim() : '';
      return clientId && tenantId ? { clientId, tenantId } : null;
    } catch {
      return null;
    } finally {
      clearTimeout(timer);
    }
  })();
  return serverConfig;
}

/** Runtime server config wins; the build-time env is the dev fallback. */
export async function azureConfig(): Promise<AzureConfig | null> {
  return (await fetchServerConfig()) ?? azureEnv();
}

/** True when either source supplies both ids. */
export async function azureAvailable(): Promise<boolean> {
  return (await azureConfig()) !== null;
}

/**
 * True when this document is the tail end of an MSAL sign-in rather than a
 * normal page load.
 *
 * The redirect URI is the app's own origin, so Entra sends the popup back to
 * `/?code=…&state=…`. Without this check the whole SPA boots inside that
 * popup and renders the identity gate, which looks to the user like a second
 * login page that never closes. MSAL (running in the opener) reads the
 * parameters off the popup and closes it, so the popup only has to avoid
 * getting in the way.
 *
 * Deliberately narrow: `state` must be present alongside `code`/`error`, so an
 * ordinary URL carrying an unrelated `code` parameter still loads the board.
 */
export function isAuthRedirect(
  search: string = typeof location === 'undefined' ? '' : location.search,
  hash: string = typeof location === 'undefined' ? '' : location.hash,
): boolean {
  const params = new URLSearchParams(
    search || (hash.startsWith('#') ? hash.slice(1) : hash),
  );
  if (!params.has('state')) return false;
  return params.has('code') || params.has('error');
}

/**
 * msal is imported lazily so the chunk stays out of the critical path and the
 * app loads fine when Azure is unconfigured.
 */
function loadMsal(): Promise<typeof import('@azure/msal-browser')> {
  return import('@azure/msal-browser');
}

let pcaPromise: Promise<PublicClientApplication> | null = null;

function getPca(): Promise<PublicClientApplication> {
  pcaPromise ??= (async () => {
    // Runtime config first: MSAL must not be constructed before the server has
    // had its say, or a released binary would build the app with no ids.
    const cfg = await azureConfig();
    if (!cfg) throw new Error('Azure sign-in is not configured');
    const msal = await loadMsal();
    const app = new msal.PublicClientApplication({
      auth: {
        clientId: cfg.clientId,
        authority: `https://login.microsoftonline.com/${cfg.tenantId}`,
        redirectUri: window.location.origin,
      },
      // MSAL's default sessionStorage cache is kept deliberately: refresh/ID
      // tokens in localStorage would outlive the session and be readable by
      // any script on the origin. Cost: re-auth once per browser session.
    });
    // msal v3+ requires initialize() before any other call.
    await app.initialize();
    return app;
  })();
  return pcaPromise;
}

export async function signInAzure(): Promise<Identity> {
  const app = await getPca();
  const result = await app.loginPopup({ scopes: SCOPES });
  const account = result.account;
  if (!account) throw new Error('Sign-in returned no account');
  app.setActiveAccount(account);
  const claims = (result.idTokenClaims ?? {}) as Record<string, unknown>;
  const email =
    (typeof claims.email === 'string' && claims.email) ||
    (typeof claims.preferred_username === 'string' &&
      claims.preferred_username) ||
    account.username;
  return { kind: 'azure', id: email, name: account.name };
}

function pickAccount(
  app: PublicClientApplication,
  id: string,
): AccountInfo | null {
  const accounts = app.getAllAccounts();
  return (
    accounts.find((a) => a.username.toLowerCase() === id.toLowerCase()) ??
    app.getActiveAccount() ??
    accounts[0] ??
    null
  );
}

export async function getApiToken(
  identity: Identity,
  opts?: { forceRefresh?: boolean },
): Promise<string | null> {
  if (identity.kind === 'manual') return identity.serverToken ?? null;
  if (!(await azureConfig())) return null;
  const msal = await loadMsal();
  const app = await getPca();
  const account = pickAccount(app, identity.id);
  // No cached MSAL account: the session is gone even though our identity
  // persisted. Don't send unauthenticated requests — demand re-auth.
  if (!account) throw new ReauthRequiredError();
  const request = {
    scopes: SCOPES,
    account,
    forceRefresh: opts?.forceRefresh ?? false,
  };
  try {
    const result = await app.acquireTokenSilent(request);
    return result.idToken;
  } catch (err) {
    // Never acquireTokenPopup here: callers run in background paths (mount
    // load, debounced autosave) where browsers block popups. Surface a typed
    // error so the UI can ask for an interactive sign-in instead.
    if (err instanceof msal.InteractionRequiredAuthError) {
      throw new ReauthRequiredError();
    }
    throw err;
  }
}
