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

/** ADR or forge issue to split; `max` is clamped to 1..20 by the server. */
export interface AIStoriesRequest {
  adr?: string;
  url?: string;
  source?: string;
  max?: number;
}

export type ForgeKind = 'gitlab' | 'github';

export interface ForgeSource {
  name: string;
  kind: ForgeKind;
  base_url: string;
  has_token: boolean;
}

export interface IntegrationPatch {
  kind: ForgeKind;
  base_url?: string;
  pat?: string;
}

export interface PutIntegrationResult {
  tokenCleared: boolean;
}

export interface ForgeTestProbe {
  base_url?: string;
  pat?: string;
}

export interface ForgeTestResult {
  ok: boolean;
  error?: string;
}

export interface ImportDuplicate {
  id?: string;
  title: string;
  via: 'link' | 'similar';
}

export interface ImportDraft extends StoryDraft {
  link?: string;
  external_key?: string;
  url?: string;
  duplicate_of?: ImportDuplicate;
}

export interface ImportPreview {
  kind: 'issue' | 'project' | 'milestone';
  total_hint: number;
  fetched: number;
  truncated: boolean;
  note: string;
  drafts: ImportDraft[];
}

export interface ImportPreviewRequest {
  source: string;
  ref: string;
  max: number;
}

export interface ImportLinkItem {
  external_key: string;
  link: string;
  url: string;
  title: string;
}

export interface RecordImportLinksRequest {
  source: string;
  items: ImportLinkItem[];
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

function coerceForgeSource(value: unknown): ForgeSource | null {
  if (typeof value !== 'object' || value === null) return null;
  const fields = value as Record<string, unknown>;
  const name = typeof fields.name === 'string' ? fields.name.trim() : '';
  const baseURL =
    typeof fields.base_url === 'string' ? fields.base_url.trim() : '';
  const kind =
    fields.kind === 'gitlab' || fields.kind === 'github' ? fields.kind : null;
  if (name === '' || baseURL === '' || kind === null) return null;
  return {
    name,
    kind,
    base_url: baseURL,
    has_token: fields.has_token === true,
  };
}

export function coerceForgeSources(body: unknown): ForgeSource[] {
  if (typeof body !== 'object' || body === null) return [];
  const sources = (body as Record<string, unknown>).sources;
  if (!Array.isArray(sources)) return [];
  return sources.flatMap((source) => {
    const coerced = coerceForgeSource(source);
    return coerced === null ? [] : [coerced];
  });
}

function integrationPath(name: string, suffix = ''): string {
  if (name === '' || name === '.' || name === '..') {
    throw new Error('invalid integration name');
  }
  return `/api/integrations/${encodeURIComponent(name)}${suffix}`;
}

export async function getIntegrations(
  identity: Identity,
): Promise<ForgeSource[]> {
  const res = await authedFetch(identity, '/api/integrations');
  if (!res.ok) {
    throw new Error(
      await errText(res, `load integrations failed: ${res.status}`),
    );
  }
  return coerceForgeSources(await res.json());
}

export async function putIntegration(
  identity: Identity,
  name: string,
  patch: IntegrationPatch,
): Promise<PutIntegrationResult> {
  const body = {
    kind: patch.kind,
    ...(typeof patch.base_url === 'string' ? { base_url: patch.base_url } : {}),
    ...(typeof patch.pat === 'string' ? { pat: patch.pat } : {}),
  };
  const res = await authedFetch(
    identity,
    integrationPath(name),
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) {
    throw new Error(
      await errText(res, `save integration failed: ${res.status}`),
    );
  }
  if (res.status === 204) return { tokenCleared: false };
  try {
    const result = (await res.json()) as { token_cleared?: unknown };
    return { tokenCleared: result.token_cleared === true };
  } catch {
    return { tokenCleared: false };
  }
}

export async function deleteIntegration(
  identity: Identity,
  name: string,
): Promise<void> {
  const res = await authedFetch(identity, integrationPath(name), {
    method: 'DELETE',
  });
  if (!res.ok) {
    throw new Error(
      await errText(res, `remove integration failed: ${res.status}`),
    );
  }
}

export async function forgeTest(
  identity: Identity,
  name: string,
  probe?: ForgeTestProbe,
): Promise<ForgeTestResult> {
  try {
    const init: RequestInit = { method: 'POST' };
    if (probe !== undefined) {
      init.headers = { 'Content-Type': 'application/json' };
      init.body = JSON.stringify({
        ...(typeof probe.base_url === 'string'
          ? { base_url: probe.base_url }
          : {}),
        ...(typeof probe.pat === 'string' ? { pat: probe.pat } : {}),
      });
    }
    const res = await authedFetch(
      identity,
      integrationPath(name, '/test'),
      init,
    );
    if (!res.ok) {
      return {
        ok: false,
        error: await errText(res, `test failed: ${res.status}`),
      };
    }
    const result: unknown = await res.json();
    if (typeof result !== 'object' || result === null) {
      return { ok: false, error: 'connection failed' };
    }
    const fields = result as Record<string, unknown>;
    if (fields.ok === true) return { ok: true };
    return {
      ok: false,
      error:
        typeof fields.error === 'string' && fields.error.trim() !== ''
          ? fields.error
          : 'connection failed',
    };
  } catch {
    return { ok: false, error: 'connection failed' };
  }
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

function importCount(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.max(0, Math.round(value))
    : 0;
}

function importText(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined;
  const text = value.trim();
  return text === '' ? undefined : text;
}

function coerceImportDuplicate(value: unknown): ImportDuplicate | undefined {
  if (typeof value !== 'object' || value === null) return undefined;
  const fields = value as Record<string, unknown>;
  const title = importText(fields.title);
  const via =
    fields.via === 'link' || fields.via === 'similar' ? fields.via : undefined;
  if (title === undefined || via === undefined) return undefined;
  const id = importText(fields.id);
  return { ...(id === undefined ? {} : { id }), title, via };
}

function coerceImportDraft(value: unknown): ImportDraft | null {
  const draft = coerceStoryDraft(value);
  if (draft.title === '') return null;
  const fields = value as Record<string, unknown>;
  const link = importText(fields.link);
  const externalKey = importText(fields.external_key);
  const url = importText(fields.url);
  const duplicate = coerceImportDuplicate(fields.duplicate_of);
  return {
    ...draft,
    ...(link === undefined ? {} : { link }),
    ...(externalKey === undefined ? {} : { external_key: externalKey }),
    ...(url === undefined ? {} : { url }),
    ...(duplicate === undefined ? {} : { duplicate_of: duplicate }),
  };
}

/** Coerce the D3 response without discarding server-authored provenance. */
export function coerceImportPreview(body: unknown): ImportPreview {
  const fields = (
    typeof body === 'object' && body !== null ? body : {}
  ) as Record<string, unknown>;
  const kind =
    fields.kind === 'issue' ||
    fields.kind === 'project' ||
    fields.kind === 'milestone'
      ? fields.kind
      : 'project';
  const drafts = Array.isArray(fields.drafts)
    ? fields.drafts.flatMap((value) => {
        const draft = coerceImportDraft(value);
        return draft === null ? [] : [draft];
      })
    : [];
  return {
    kind,
    total_hint: importCount(fields.total_hint),
    fetched: importCount(fields.fetched),
    truncated: fields.truncated === true,
    note: typeof fields.note === 'string' ? fields.note : '',
    drafts,
  };
}

export async function importPreview(
  identity: Identity,
  req: ImportPreviewRequest,
  signal: AbortSignal,
): Promise<ImportPreview> {
  const res = await authedFetch(identity, '/api/import/preview', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    signal,
  });
  if (!res.ok) {
    throw new Error(
      await errText(res, `import preview failed: ${res.status}`),
    );
  }
  return coerceImportPreview(await res.json());
}

/**
 * Journal selected source links after cards are added. This is deliberately
 * best-effort: losing provenance must not roll back work already on the board.
 */
export async function recordImportLinks(
  identity: Identity,
  req: RecordImportLinksRequest,
): Promise<void> {
  try {
    const res = await authedFetch(identity, '/api/import/links', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    if (!res.ok) return;
  } catch {
    // The cards are already committed; the provenance journal is secondary.
  }
}

/**
 * Keep reason capture testable without mounting a dialog. The endpoint uses
 * snake_case, but the UI carries the task id in its usual client-side shape.
 */
export function killReasonRequest(
  taskId: string,
  reason: string,
): { taskId: string; reason: string } | null {
  const trimmed = reason.trim();
  return trimmed === '' ? null : { taskId, reason: trimmed };
}

/**
 * Journal why a card was killed. This is deliberately best-effort: the board
 * move has already happened, so losing its annotation must never undo it.
 */
export async function recordTombstone(
  identity: Identity,
  taskId: string,
  reason: string,
): Promise<void> {
  try {
    const res = await authedFetch(identity, '/api/tombstones', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ task_id: taskId, reason }),
    });
    if (!res.ok) return;
  } catch {
    // The card is already cancelled; the graveyard annotation is secondary.
  }
}

export interface SimilarItem {
  id?: string;
  title: string;
  status?: string;
  via: 'card' | 'import' | 'killed';
  link?: string;
  reason?: string;
  killedAt?: string;
}

function coerceSimilarItem(item: unknown): SimilarItem | null {
  if (typeof item !== 'object' || item === null) return null;
  const fields = item as Record<string, unknown>;
  const title = typeof fields.title === 'string' ? fields.title.trim() : '';
  const via =
    fields.via === 'card' ||
    fields.via === 'import' ||
    fields.via === 'killed'
      ? fields.via
      : null;
  if (title === '' || via === null) return null;
  return {
    ...(typeof fields.id === 'string' ? { id: fields.id } : {}),
    title,
    ...(typeof fields.status === 'string' ? { status: fields.status } : {}),
    via,
    ...(typeof fields.link === 'string' ? { link: fields.link } : {}),
    ...(typeof fields.reason === 'string' ? { reason: fields.reason } : {}),
    ...(typeof fields.killed_at === 'string'
      ? { killedAt: fields.killed_at }
      : {}),
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
