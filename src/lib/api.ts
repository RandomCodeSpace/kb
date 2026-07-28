import type { Identity } from './auth';
import { getApiToken, ReauthRequiredError } from './auth';
import type { Effort, Prio } from './model';

async function authHeaders(
  identity: Identity,
  forceRefresh = false,
): Promise<Record<string, string>> {
  const headers: Record<string, string> = {};
  const token = await getApiToken(identity, { forceRefresh });
  if (token) headers.Authorization = `Bearer ${token}`;
  if (identity.kind === 'manual') headers['X-KB-User'] = identity.id;
  return headers;
}

/**
 * fetch with auth headers. On a 401 an Azure identity retries once with a
 * force-refreshed token; a repeat 401 (or any manual-identity 401) throws
 * ReauthRequiredError so the UI shows "session expired" rather than a
 * generic sync error. Shared by RemoteStore and the /api helpers below.
 */
export async function authedFetch(
  identity: Identity,
  input: string,
  init: RequestInit = {},
): Promise<Response> {
  const attempt = async (forceRefresh: boolean) =>
    fetch(input, {
      ...init,
      headers: {
        ...(init.headers as Record<string, string> | undefined),
        ...(await authHeaders(identity, forceRefresh)),
      },
    });
  let res = await attempt(false);
  if (res.status === 401 && identity.kind === 'azure') {
    res = await attempt(true);
  }
  if (res.status === 401) throw new ReauthRequiredError();
  return res;
}

/** Client view of the per-user AI settings; the key itself never leaves the server. */
export interface AISettings {
  ai_base_url: string;
  ai_model: string;
  has_key: boolean;
}

/** Any subset of fields to change; omit ai_key to keep the stored key. */
export interface SettingsPatch {
  ai_base_url?: string;
  ai_model?: string;
  ai_key?: string;
}

export interface AITestResult {
  ok: boolean;
  error?: string;
}

export interface AIStoryRequest {
  mode: 'create' | 'update';
  prompt: string;
  task?: Record<string, unknown>;
}

/** Card draft returned by POST /api/ai/story, clamped into form-safe values. */
export interface StoryDraft {
  title: string;
  desc: string;
  prio: Prio;
  due: string; // YYYY-MM-DD or ''
  effort: Effort | '';
  tags: string[];
  checks: { text: string; done: boolean }[];
}

/** Short error text from a failed response body, never echoing secrets. */
async function errText(res: Response, fallback: string): Promise<string> {
  try {
    const t = (await res.text()).trim();
    if (t !== '') return t.slice(0, 200);
  } catch {
    // Body unreadable — use the fallback.
  }
  return fallback;
}

export async function getLabels(identity: Identity): Promise<string[]> {
  const res = await authedFetch(identity, '/api/labels');
  if (!res.ok) throw new Error(`GET /api/labels failed: ${res.status}`);
  const body: unknown = await res.json();
  if (!Array.isArray(body)) return [];
  return body.filter((x): x is string => typeof x === 'string');
}

export async function getSettings(identity: Identity): Promise<AISettings> {
  const res = await authedFetch(identity, '/api/settings');
  if (!res.ok) throw new Error(`GET /api/settings failed: ${res.status}`);
  const b = (await res.json()) as Record<string, unknown>;
  return {
    ai_base_url: typeof b.ai_base_url === 'string' ? b.ai_base_url : '',
    ai_model: typeof b.ai_model === 'string' ? b.ai_model : '',
    has_key: b.has_key === true,
  };
}

export interface PutSettingsResult {
  /**
   * True when the server dropped the stored key because the base URL moved
   * to a different scheme/host — the key must be re-entered.
   */
  keyCleared: boolean;
}

export async function putSettings(
  identity: Identity,
  patch: SettingsPatch,
): Promise<PutSettingsResult> {
  const res = await authedFetch(identity, '/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  if (!res.ok) {
    throw new Error(await errText(res, `save settings failed: ${res.status}`));
  }
  if (res.status === 204) return { keyCleared: false };
  try {
    const b = (await res.json()) as { key_cleared?: unknown };
    return { keyCleared: b.key_cleared === true };
  } catch {
    return { keyCleared: false };
  }
}

/** 1-token chat completion against the saved settings; never throws for upstream failures. */
export async function aiTest(identity: Identity): Promise<AITestResult> {
  const res = await authedFetch(identity, '/api/ai/test', { method: 'POST' });
  if (!res.ok) {
    return { ok: false, error: await errText(res, `test failed: ${res.status}`) };
  }
  const b = (await res.json()) as { ok?: unknown; error?: unknown };
  if (b.ok === true) return { ok: true };
  return {
    ok: false,
    error:
      typeof b.error === 'string' && b.error !== ''
        ? b.error
        : 'connection failed',
  };
}

export async function aiStory(
  identity: Identity,
  req: AIStoryRequest,
): Promise<StoryDraft> {
  const res = await authedFetch(identity, '/api/ai/story', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!res.ok) throw new Error(await errText(res, `draft failed: ${res.status}`));
  return coerceStoryDraft(await res.json());
}

const DUE_RE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Clamp an untrusted draft body into form-safe values. The server already
 * coerces, but the form types (Prio union, Effort union) must never trust
 * the wire: prio 1-4 (default 3), effort S/M/L or '', due YYYY-MM-DD or ''.
 */
export function coerceStoryDraft(body: unknown): StoryDraft {
  const b = (typeof body === 'object' && body !== null ? body : {}) as Record<
    string,
    unknown
  >;
  const prioNum = typeof b.prio === 'number' ? Math.round(b.prio) : 3;
  const prio = (prioNum >= 1 && prioNum <= 4 ? prioNum : 3) as Prio;
  const effort =
    b.effort === 'S' || b.effort === 'M' || b.effort === 'L' ? b.effort : '';
  const due = typeof b.due === 'string' && DUE_RE.test(b.due) ? b.due : '';
  const tags = Array.isArray(b.tags)
    ? b.tags
        .filter((t): t is string => typeof t === 'string')
        .map((t) => t.trim())
        .filter(Boolean)
    : [];
  const checks = Array.isArray(b.checks)
    ? b.checks.flatMap((c): { text: string; done: boolean }[] => {
        if (typeof c !== 'object' || c === null) return [];
        const o = c as Record<string, unknown>;
        if (typeof o.text !== 'string' || o.text.trim() === '') return [];
        return [{ text: o.text.trim(), done: o.done === true }];
      })
    : [];
  return {
    title: typeof b.title === 'string' ? b.title.trim() : '',
    desc: typeof b.desc === 'string' ? b.desc : '',
    prio,
    due,
    effort,
    tags,
    checks,
  };
}
