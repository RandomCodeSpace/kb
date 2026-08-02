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

export class MetadataRequestError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
    this.name = 'MetadataRequestError';
  }
}

export type DriftState = 'baseline_recorded' | 'unchanged' | 'drifted';

export interface DriftResult {
  state: DriftState;
  link: string;
  url: string;
  title_changed?: boolean;
  upstream_title: string;
  baseline_title: string;
  baseline_at: string;
  checked_at: string;
  summary: string;
  revision?: string;
}

export interface AcceptDriftResult {
  baseline_at: string;
}

export class DriftConflictError extends Error {}

export interface ImportProvenance {
  source: string;
  external_key: string;
  title: string;
  url: string;
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
 * Journal selected source links after cards are added. The caller owns durable
 * retry; rejecting failures is what prevents the outbox from dropping work.
 */
export async function recordImportLinks(
  identity: Identity,
  req: RecordImportLinksRequest,
): Promise<void> {
  const res = await authedFetch(identity, '/api/import/links', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    throw new MetadataRequestError(
      await errText(res, `import provenance write failed: ${res.status}`),
      res.status,
    );
  }
}

function driftText(
  fields: Record<string, unknown>,
  name: string,
  allowEmpty = false,
): string {
  const value = fields[name];
  if (typeof value !== 'string' || (!allowEmpty && value.trim() === '')) {
    throw new Error('invalid drift response');
  }
  return value;
}

/**
 * Keep a malformed response from claiming that a comparison or baseline write
 * happened. Explicit drift checks fail visibly instead of guessing at state.
 */
export function coerceDriftResult(body: unknown): DriftResult {
  if (typeof body !== 'object' || body === null) {
    throw new Error('invalid drift response');
  }
  const fields = body as Record<string, unknown>;
  const state =
    fields.state === 'baseline_recorded' ||
    fields.state === 'unchanged' ||
    fields.state === 'drifted'
      ? fields.state
      : null;
  if (state === null) throw new Error('invalid drift response');

  const baselineAt = driftText(fields, 'baseline_at');
  const checkedAt = driftText(fields, 'checked_at');
  if (
    Number.isNaN(Date.parse(baselineAt)) ||
    Number.isNaN(Date.parse(checkedAt))
  ) {
    throw new TypeError('invalid drift response');
  }

  const result: DriftResult = {
    state,
    link: driftText(fields, 'link'),
    url: driftText(fields, 'url'),
    upstream_title: driftText(fields, 'upstream_title', true),
    baseline_title: driftText(fields, 'baseline_title', true),
    baseline_at: baselineAt,
    checked_at: checkedAt,
    summary: driftText(fields, 'summary', true),
  };
  if (state !== 'baseline_recorded') {
    if (typeof fields.title_changed !== 'boolean') {
      throw new TypeError('invalid drift response');
    }
    result.title_changed = fields.title_changed;
  }
  if (state === 'drifted') {
    if (
      typeof fields.revision !== 'string' ||
      !/^[0-9a-f]{64}$/.test(fields.revision)
    ) {
      throw new Error('invalid drift response');
    }
    result.revision = fields.revision;
  }
  return result;
}

function coerceImportProvenance(value: unknown): ImportProvenance | null {
  if (typeof value !== 'object' || value === null) return null;
  const fields = value as Record<string, unknown>;
  const source = importText(fields.source);
  const externalKey = importText(fields.external_key);
  const url = importText(fields.url);
  if (source === undefined || externalKey === undefined || url === undefined) {
    return null;
  }
  return {
    source,
    external_key: externalKey,
    title: typeof fields.title === 'string' ? fields.title : '',
    url,
  };
}

export function coerceImportProvenanceItems(
  body: unknown,
): ImportProvenance[] {
  if (typeof body !== 'object' || body === null) {
    throw new Error('invalid import provenance response');
  }
  const items = (body as Record<string, unknown>).items;
  if (!Array.isArray(items)) {
    throw new TypeError('invalid import provenance response');
  }
  const coerced = items.map(coerceImportProvenance);
  if (coerced.includes(null)) {
    throw new Error('invalid import provenance response');
  }
  return coerced as ImportProvenance[];
}

export async function getImportProvenance(
  identity: Identity,
  link: string,
  signal: AbortSignal,
): Promise<ImportProvenance[]> {
  const res = await authedFetch(identity, '/api/import/provenance', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ link }),
    signal,
  });
  if (!res.ok) {
    throw new Error(
      await errText(res, `import provenance lookup failed: ${res.status}`),
    );
  }
  return coerceImportProvenanceItems(await res.json());
}

export async function checkDrift(
  identity: Identity,
  source: string,
  externalKey: string,
  signal: AbortSignal,
): Promise<DriftResult> {
  const res = await authedFetch(identity, '/api/import/drift', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source, external_key: externalKey }),
    signal,
  });
  if (!res.ok) {
    throw new Error(await errText(res, `drift check failed: ${res.status}`));
  }
  return coerceDriftResult(await res.json());
}

export async function acceptDrift(
  identity: Identity,
  source: string,
  externalKey: string,
  revision: string,
  signal: AbortSignal,
): Promise<AcceptDriftResult> {
  const res = await authedFetch(identity, '/api/import/drift/accept', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ source, external_key: externalKey, revision }),
    signal,
  });
  if (!res.ok) {
    const message = await errText(
      res,
      `drift acceptance failed: ${res.status}`,
    );
    if (res.status === 409) throw new DriftConflictError(message);
    throw new Error(message);
  }
  const body: unknown = await res.json();
  const fields = (
    typeof body === 'object' && body !== null ? body : {}
  ) as Record<string, unknown>;
  const baselineAt =
    typeof fields.baseline_at === 'string' ? fields.baseline_at : '';
  if (baselineAt === '' || Number.isNaN(Date.parse(baselineAt))) {
    throw new Error('invalid drift acceptance response');
  }
  return { baseline_at: baselineAt };
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
 * Journal why a card was killed. The caller owns durable retry; the primary
 * board move is never rolled back when this rejects.
 */
export async function recordTombstone(
  identity: Identity,
  taskId: string,
  reason: string,
): Promise<void> {
  const res = await authedFetch(identity, '/api/tombstones', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ task_id: taskId, reason }),
  });
  if (!res.ok) {
    throw new MetadataRequestError(
      await errText(res, `tombstone write failed: ${res.status}`),
      res.status,
    );
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

const SIMILAR_EXCLUDE_LINK_LIMIT = 100;

export async function getSimilar(
  identity: Identity,
  q: string,
  excludeId?: string,
  excludeLinks: readonly string[] = [],
  signal?: AbortSignal,
): Promise<SimilarItem[]> {
  try {
    const params = new URLSearchParams({ q });
    if (excludeId) params.set('exclude', excludeId);
    for (const link of excludeLinks.slice(0, SIMILAR_EXCLUDE_LINK_LIMIT)) {
      params.append('exclude_link', link);
    }
    const res = await authedFetch(identity, `/api/similar?${params}`, { signal });
    if (!res.ok) return [];
    return coerceSimilarItems(await res.json());
  } catch {
    return [];
  }
}
