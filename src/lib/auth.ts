import type {
  AccountInfo,
  PublicClientApplication,
} from '@azure/msal-browser';
import {
  legacyNamespaceStorageKey,
  migrateLegacyKeys,
  namespaceStorageKey,
  namespaceStorageSuffix,
} from './store';

export type Identity = {
  kind: 'azure' | 'manual';
  id: string;
  name?: string;
  serverToken?: string;
  /** Immutable MSAL account key. Required for new Azure identities. */
  homeAccountId?: string;
};

const IDENTITY_KEY = 'kb.identity.v1';
const TOKEN_KEY = 'kb.serverToken.v1';
const MIGRATED_KEY = 'kb.migrated.identity.v1';
const SCOPES = ['openid', 'profile', 'email'];
const CONFIG_TIMEOUT_MS = 1500;
const AZURE_CLAIM_KEY = 'kb.azure-namespace-claim.v1';
const AZURE_CLAIM_LOCK = 'kb:azure-namespace-claim:';

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
  constructor(message = 'session expired — sign in again') {
    super(message);
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
    if (
      o.homeAccountId !== undefined &&
      (typeof o.homeAccountId !== 'string' || o.homeAccountId.trim() === '')
    ) return null;
    // The shared server token is session-scoped on purpose: it authorizes
    // every user's board, so it never persists in localStorage. A legacy
    // persisted `serverToken` field is deliberately ignored.
    const serverToken = sessionStorage.getItem(TOKEN_KEY) ?? undefined;
    return {
      kind: o.kind,
      id: o.id,
      name: o.name,
      ...(o.kind === 'azure' && typeof o.homeAccountId === 'string'
        ? { homeAccountId: o.homeAccountId }
        : {}),
      serverToken,
    };
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

const DISPLAY_NAME_KEY = 'kb.displayName.v1';

/**
 * A device-local display name, set in Settings and shown in the header in
 * place of the identity label. Cosmetic only: the identity, board namespace,
 * and sync are untouched. Notably it is the only personalization available
 * in open mode, where the identity is auto-adopted and never typed.
 */
export function loadLocalDisplayName(): string {
  try {
    return localStorage.getItem(DISPLAY_NAME_KEY) ?? '';
  } catch {
    return '';
  }
}

export function saveLocalDisplayName(name: string): void {
  try {
    if (name.trim() === '') localStorage.removeItem(DISPLAY_NAME_KEY);
    else localStorage.setItem(DISPLAY_NAME_KEY, name);
  } catch {
    // Storage unavailable — the name lives for this page only.
  }
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

/** Local state is keyed by immutable Azure account identity, never email. */
export function identityNamespace(identity: Identity): string {
  if (identity.kind === 'azure') {
    if (!identity.homeAccountId?.trim()) throw new ReauthRequiredError();
    // localStorage keys accept URI escapes; unlike sanitization, this is
    // injective and cannot collapse two distinct immutable account IDs.
    return `azure.${encodeURIComponent(identity.homeAccountId)}`;
  }
  return sanitizeUser(identity.id);
}

const NAMESPACED_BASES = [
  'kb.board.v1',
  'kb.streak.v1',
  'kb.dirty.v1',
  'kb.migrated.v1',
] as const;
const OUTBOX_PREFIX = 'kb.outbox.v1';

function outboxLogicalKey(raw: string | null): string | null {
  if (raw === null) return null;
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    if (value.kind === 'tombstone' && typeof value.clientTaskId === 'string') {
      return `tombstone:${value.clientTaskId}`;
    }
    if (
      value.kind === 'import' &&
      typeof value.item === 'object' && value.item !== null &&
      typeof (value.item as Record<string, unknown>).external_key === 'string'
    ) {
      return `import:${(value.item as Record<string, unknown>).external_key as string}`;
    }
  } catch {
    // Invalid records cannot prove ownership of an ambiguous legacy key.
  }
  return null;
}

function namespaceKey(key: string, from: string, to: string): string | null {
  for (const base of NAMESPACED_BASES) {
    if (
      key === namespaceStorageKey(base, from) ||
      key === legacyNamespaceStorageKey(base, from)
    ) return namespaceStorageKey(base, to);
  }

  const framedSuffix = namespaceStorageSuffix(OUTBOX_PREFIX, from, key);
  if (framedSuffix !== null && framedSuffix !== '') {
    return namespaceStorageKey(OUTBOX_PREFIX, to, framedSuffix);
  }
  const logical = outboxLogicalKey(localStorage.getItem(key));
  if (!logical) return null;
  const suffix = encodeURIComponent(logical);
  return key === legacyNamespaceStorageKey(OUTBOX_PREFIX, from, suffix)
    ? namespaceStorageKey(OUTBOX_PREFIX, to, suffix)
    : null;
}

function legacyNamespaceKeys(ns: string): string[] {
  const keys: string[] = [];
  try {
    for (let i = 0; i < localStorage.length; i += 1) {
      const key = localStorage.key(i);
      if (key && namespaceKey(key, ns, ns) !== null) keys.push(key);
    }
  } catch {
    throw new ReauthRequiredError(
      'local account data could not be inspected — sign in again and import it manually',
    );
  }
  return keys;
}

/**
 * Non-destructively claims the old email namespace for one immutable account.
 * The marker is durable before the first copy, so a same-owner retry resumes.
 */
export async function claimLegacyAzureNamespace(
  email: string,
  homeAccountId: string,
): Promise<void> {
  if (!homeAccountId.trim()) throw new ReauthRequiredError();
  const legacyNS = sanitizeUser(email);
  const accountNS = identityNamespace({
    kind: 'azure',
    id: email,
    homeAccountId,
  });
  if (legacyNS === accountNS) return;
  const legacyKeys = legacyNamespaceKeys(legacyNS);
  if (legacyKeys.length === 0) return;

  const locks = typeof navigator === 'undefined' ? undefined : navigator.locks;
  if (!locks?.request) {
    throw new ReauthRequiredError(
      'legacy board data needs a browser storage lock — sign in again and import it manually',
    );
  }

  await locks.request(namespaceStorageKey(AZURE_CLAIM_LOCK, legacyNS), async () => {
    const markerKey = namespaceStorageKey(AZURE_CLAIM_KEY, legacyNS);
    const legacyMarkerKey = legacyNamespaceStorageKey(AZURE_CLAIM_KEY, legacyNS);
    const framedOwner = localStorage.getItem(markerKey);
    const legacyOwner = localStorage.getItem(legacyMarkerKey);
    const owner = framedOwner ?? legacyOwner;
    if (owner !== null && owner !== homeAccountId) {
      throw new ReauthRequiredError(
        'legacy board data belongs to another Microsoft account — sign in again or import it manually',
      );
    }
    if (framedOwner === null) localStorage.setItem(markerKey, owner ?? homeAccountId);

    // Reread after claiming: another attempt may have completed some keys.
    for (const key of legacyNamespaceKeys(legacyNS)) {
      const target = namespaceKey(key, legacyNS, accountNS);
      if (!target || localStorage.getItem(target) !== null) continue;
      const value = localStorage.getItem(key);
      if (value !== null) localStorage.setItem(target, value);
    }
  });
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

/** The credential the server demands, or 'unknown' when it cannot be asked. */
export type ServerAuthMode = 'open' | 'token' | 'entra' | 'unknown';

interface ServerConfigSnapshot {
  azure: AzureConfig | null;
  mode: ServerAuthMode;
}

let serverConfig: Promise<ServerConfigSnapshot> | null = null;

/**
 * The client/tenant ids and auth mode the server was started with. The ids
 * are public by design (they ship in every MSAL SPA bundle) and the endpoint
 * needs no auth — the SPA reads it before login. Any failure (no server, dev
 * mode, 404, an older binary without auth_mode) resolves to no ids and
 * 'unknown', so the env fallback and the identity gate apply as before.
 * Cached for the page's life.
 */
function fetchServerConfig(): Promise<ServerConfigSnapshot> {
  serverConfig ??= (async () => {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), CONFIG_TIMEOUT_MS);
    try {
      const res = await fetch('/api/config', { signal: ctrl.signal });
      if (!res.ok) return { azure: null, mode: 'unknown' as const };
      const b = (await res.json()) as Record<string, unknown>;
      const clientId =
        typeof b.azure_client_id === 'string' ? b.azure_client_id.trim() : '';
      const tenantId =
        typeof b.azure_tenant_id === 'string' ? b.azure_tenant_id.trim() : '';
      const mode =
        b.auth_mode === 'open' || b.auth_mode === 'token' || b.auth_mode === 'entra'
          ? b.auth_mode
          : ('unknown' as const);
      return { azure: clientId && tenantId ? { clientId, tenantId } : null, mode };
    } catch {
      return { azure: null, mode: 'unknown' as const };
    } finally {
      clearTimeout(timer);
    }
  })();
  return serverConfig;
}

/** Runtime server config wins; the build-time env is the dev fallback. */
export async function azureConfig(): Promise<AzureConfig | null> {
  return (await fetchServerConfig()).azure ?? azureEnv();
}

/** Which credential the API will demand for this origin. */
export async function serverAuthMode(): Promise<ServerAuthMode> {
  return (await fetchServerConfig()).mode;
}

export type BootAction = { kind: 'gate' } | { kind: 'adopt'; identity: Identity };

/**
 * What the app does when no identity is stored. Open mode has no credential
 * to collect, so the gate would only be choosing a board namespace — the app
 * adopts "default" instead, the same namespace the CLI writes to without
 * --user, and the board opens directly. Every other mode keeps the gate (it
 * collects a token there), as does an unreachable or older server, where
 * guessing "no auth needed" would be wrong. `gateRequested` is an explicit
 * sign-out or account switch: a person asking to choose is never skipped
 * past the choice.
 */
export function bootAction(
  mode: ServerAuthMode,
  gateRequested: boolean,
): BootAction {
  if (gateRequested || mode !== 'open') return { kind: 'gate' };
  return { kind: 'adopt', identity: { kind: 'manual', id: 'default' } };
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
  await claimLegacyAzureNamespace(email, account.homeAccountId);
  return {
    kind: 'azure',
    id: email,
    name: account.name,
    homeAccountId: account.homeAccountId,
  };
}

async function resolveAzureAccount(
  app: PublicClientApplication,
  identity: Identity,
): Promise<{ identity: Identity; account: AccountInfo }> {
  const accounts = app.getAllAccounts();
  if (identity.homeAccountId) {
    const matches = accounts.filter(
      (account) => account.homeAccountId === identity.homeAccountId,
    );
    if (matches.length !== 1) throw new ReauthRequiredError();
    return { identity, account: matches[0]! };
  }

  const matches = accounts.filter(
    (account) => account.username.toLowerCase() === identity.id.toLowerCase(),
  );
  if (matches.length !== 1 || !matches[0]!.homeAccountId) {
    throw new ReauthRequiredError();
  }
  const account = matches[0]!;
  await claimLegacyAzureNamespace(identity.id, account.homeAccountId);
  const upgraded = { ...identity, homeAccountId: account.homeAccountId };
  saveIdentity(upgraded);
  return { identity: upgraded, account };
}

/** Resolve and persist an old email-only Azure identity before local state mounts. */
export async function resolveAzureIdentity(identity: Identity): Promise<Identity> {
  if (identity.kind !== 'azure' || identity.homeAccountId) return identity;
  if (!(await azureConfig())) throw new ReauthRequiredError();
  const app = await getPca();
  return (await resolveAzureAccount(app, identity)).identity;
}

export async function getApiToken(
  identity: Identity,
  opts?: { forceRefresh?: boolean },
): Promise<string | null> {
  if (identity.kind === 'manual') return identity.serverToken ?? null;
  if (!(await azureConfig())) return null;
  const msal = await loadMsal();
  const app = await getPca();
  const { account } = await resolveAzureAccount(app, identity);
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
