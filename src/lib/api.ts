import type { Identity } from './auth';
import { getApiToken, ReauthRequiredError } from './auth';
import { coerceStoryDraft } from './storyDraft';
import type { StoryDraft } from './storyDraft';

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

export { aiTest, getSettings, putSettings } from './settings';
export type {
  AISettings,
  AITestProbe,
  AITestResult,
  PutSettingsResult,
  SettingsPatch,
} from './settings';
export { coerceStoryDraft } from './storyDraft';
export type { StoryDraft } from './storyDraft';

/**
 * True for the rejection `fetch` produces when its signal is aborted — a
 * user-initiated cancel, not a failure worth showing.
 */
export function isAbortError(err: unknown): boolean {
  return (
    typeof err === 'object' &&
    err !== null &&
    (err as { name?: unknown }).name === 'AbortError'
  );
}

export interface AIStoryRequest {
  mode: 'create' | 'update';
  prompt: string;
  task?: Record<string, unknown>;
}

/** ADR to split into stories; `max` is clamped to 1..20 by the server. */
export interface AIStoriesRequest {
  adr: string;
  max?: number;
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

export async function aiStory(
  identity: Identity,
  req: AIStoryRequest,
  signal?: AbortSignal,
): Promise<StoryDraft> {
  const res = await authedFetch(identity, '/api/ai/story', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    signal,
  });
  if (!res.ok) throw new Error(await errText(res, `draft failed: ${res.status}`));
  return coerceStoryDraft(await res.json());
}

/**
 * Split an ADR into proposed stories (POST /api/ai/stories). Every draft goes
 * through the same coercion as a single draft; a draft that coercion leaves
 * title-less is dropped — it could not round-trip through the codec.
 */
export async function aiStories(
  identity: Identity,
  req: AIStoriesRequest,
  signal?: AbortSignal,
): Promise<StoryDraft[]> {
  const res = await authedFetch(identity, '/api/ai/stories', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    signal,
  });
  if (!res.ok) throw new Error(await errText(res, `split failed: ${res.status}`));
  const body: unknown = await res.json();
  const stories =
    typeof body === 'object' && body !== null
      ? (body as Record<string, unknown>).stories
      : undefined;
  if (!Array.isArray(stories)) return [];
  return stories.map(coerceStoryDraft).filter((d) => d.title !== '');
}

export interface SimilarItem {
  id?: string;
  title: string;
  status?: string;
  via: 'card' | 'import';
  link?: string;
}

function coerceSimilarItem(item: unknown): SimilarItem | null {
  if (typeof item !== 'object' || item === null) return null;
  const fields = item as Record<string, unknown>;
  const title = typeof fields.title === 'string' ? fields.title.trim() : '';
  const via = fields.via === 'card' || fields.via === 'import' ? fields.via : null;
  if (title === '' || via === null) return null;
  return {
    ...(typeof fields.id === 'string' ? { id: fields.id } : {}),
    title,
    ...(typeof fields.status === 'string' ? { status: fields.status } : {}),
    via,
    ...(typeof fields.link === 'string' ? { link: fields.link } : {}),
  };
}

export function coerceSimilarItems(body: unknown): SimilarItem[] {
  if (typeof body !== 'object' || body === null) return [];
  const items = (body as Record<string, unknown>).items;
  return Array.isArray(items)
    ? items.flatMap((item) => {
        const coerced = coerceSimilarItem(item);
        return coerced === null ? [] : [coerced];
      })
    : [];
}

export async function getSimilar(
  identity: Identity,
  q: string,
  excludeId?: string,
  signal?: AbortSignal,
): Promise<SimilarItem[]> {
  try {
    const params = new URLSearchParams({ q });
    if (excludeId) params.set('exclude', excludeId);
    const res = await authedFetch(identity, `/api/similar?${params}`, { signal });
    if (!res.ok) return [];
    return coerceSimilarItems(await res.json());
  } catch {
    return [];
  }
}
