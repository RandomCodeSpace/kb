import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Identity } from './auth';
import {
  acceptDrift,
  authedFetch,
  aiStories,
  aiStory,
  coerceDriftResult,
  coerceForgeSources,
  coerceImportPreview,
  coerceImportProvenanceItems,
  coerceSimilarItems,
  checkDrift,
  deleteIntegration,
  DriftConflictError,
  forgeTest,
  getIntegrations,
  getImportProvenance,
  getLabels,
  getSimilar,
  importPreview,
  putIntegration,
  recordImportLinks,
  recordTombstone,
} from './api';

const identity: Identity = { kind: 'manual', id: 'coverage' };

afterEach(() => {
  vi.unstubAllGlobals();
  vi.doUnmock('./auth');
});

function stubExact(expectedURL: string, result: Response | Error) {
  const fetchMock = vi.fn((url: string) => {
    if (url !== expectedURL) throw new Error(`unexpected egress: ${url}`);
    return result instanceof Error ? Promise.reject(result) : Promise.resolve(result);
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

describe('API residual validation branches', () => {
  it.each(['', '.', '..'])('rejects invalid integration name %j before egress', async (name) => {
    const fetchMock = vi.fn(() => Promise.reject(new Error('egress denied')));
    vi.stubGlobal('fetch', fetchMock);
    await expect(deleteIntegration(identity, name)).rejects.toThrow('invalid integration name');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('uses the save fallback when an integration error body is empty', async () => {
    stubExact('/api/integrations/work', new Response('  ', { status: 500 }));
    await expect(putIntegration(identity, 'work', { kind: 'gitlab' }))
      .rejects.toThrow('save integration failed: 500');
  });

  it('rejects a failed integration list request', async () => {
    stubExact('/api/integrations', new Response('denied', { status: 403 }));
    await expect(getIntegrations(identity)).rejects.toThrow('denied');
  });

  it('rejects a failed integration deletion', async () => {
    stubExact('/api/integrations/work', new Response('locked', { status: 423 }));
    await expect(deleteIntegration(identity, 'work')).rejects.toThrow('locked');
  });

  it('falls back when a successful integration save has malformed JSON', async () => {
    stubExact('/api/integrations/work', new Response('not-json', { status: 200 }));
    await expect(putIntegration(identity, 'work', { kind: 'github' }))
      .resolves.toEqual({ tokenCleared: false });
  });

  it('returns a stable failure for a non-object forge result', async () => {
    stubExact('/api/integrations/work/test', new Response('null', { status: 200 }));
    await expect(forgeTest(identity, 'work')).resolves.toEqual({
      ok: false, error: 'connection failed',
    });
  });

  it('returns the bounded response body for a failed forge test', async () => {
    stubExact('/api/integrations/work/test', new Response('bad token', { status: 400 }));
    await expect(forgeTest(identity, 'work', { base_url: 'https://git.test', pat: 'secret' }))
      .resolves.toEqual({ ok: false, error: 'bad token' });
  });

  it('accepts an explicit successful forge result', async () => {
    stubExact('/api/integrations/work/test', new Response('{"ok":true}', { status: 200 }));
    await expect(forgeTest(identity, 'work', {})).resolves.toEqual({ ok: true });
  });

  it('contains a rejected forge request without leaking the transport error', async () => {
    stubExact('/api/integrations/work/test', new Error('secret transport detail'));
    await expect(forgeTest(identity, 'work')).resolves.toEqual({
      ok: false, error: 'connection failed',
    });
  });

  it('rejects a failed labels request', async () => {
    stubExact('/api/labels', new Response('no', { status: 503 }));
    await expect(getLabels(identity)).rejects.toThrow('GET /api/labels failed: 503');
  });

  it('coerces a non-array labels response to an empty list', async () => {
    stubExact('/api/labels', new Response('{}', { status: 200 }));
    await expect(getLabels(identity)).resolves.toEqual([]);
  });

  it('filters non-string labels from an array response', async () => {
    stubExact('/api/labels', new Response('["one",7,null,"two"]', { status: 200 }));
    await expect(getLabels(identity)).resolves.toEqual(['one', 'two']);
  });

  it('rejects a failed AI draft with bounded server text', async () => {
    stubExact('/api/ai/story', new Response('provider down', { status: 502 }));
    await expect(aiStory(identity, { mode: 'create', prompt: 'draft' }))
      .rejects.toThrow('provider down');
  });

  it('coerces a successful AI draft', async () => {
    stubExact('/api/ai/story', new Response('{"title":" drafted ","prio":2}', { status: 200 }));
    await expect(aiStory(identity, { mode: 'create', prompt: 'draft' }))
      .resolves.toMatchObject({ title: 'drafted', prio: 2 });
  });

  it.each([null, {}, { stories: 'invalid' }])(
    'returns no AI stories for malformed collection %j',
    async (body) => {
      stubExact('/api/ai/stories', new Response(JSON.stringify(body), { status: 200 }));
      await expect(aiStories(identity, { adr: 'ADR' })).resolves.toEqual([]);
    },
  );

  it('keeps valid AI stories and drops titleless entries', async () => {
    stubExact('/api/ai/stories', new Response(JSON.stringify({
      stories: [{ title: 'kept' }, { title: '  ' }],
    }), { status: 200 }));
    await expect(aiStories(identity, { adr: 'ADR' })).resolves.toHaveLength(1);
  });

  it('rejects a failed AI story split', async () => {
    stubExact('/api/ai/stories', new Response('', { status: 500 }));
    await expect(aiStories(identity, { adr: 'ADR' })).rejects.toThrow('split failed: 500');
  });

  it.each([null, {}, { sources: 'invalid' }])(
    'coerces malformed forge source collection %j to empty',
    (body) => expect(coerceForgeSources(body)).toEqual([]),
  );

  it('drops malformed forge rows and normalises valid rows', () => {
    expect(coerceForgeSources({ sources: [
      null,
      { name: '', kind: 'gitlab', base_url: 'x' },
      { name: 'x', kind: 'other', base_url: 'x' },
      { name: ' work ', kind: 'github', base_url: ' https://git.test ', has_token: true },
    ] })).toEqual([{ name: 'work', kind: 'github', base_url: 'https://git.test', has_token: true }]);
  });

  it('rejects every incomplete forge source shape', () => {
    expect(coerceForgeSources({ sources: [
      7,
      { name: 7, kind: 'github', base_url: 'x' },
      { name: 'x', kind: 'github', base_url: 7 },
      { name: 'x', kind: 'gitlab', base_url: '  ' },
    ] })).toEqual([]);
  });

  it('retries an Azure 401 once with a refreshed token', async () => {
    vi.resetModules();
    const token = vi.fn(async (_identity: Identity, opts?: { forceRefresh?: boolean }) =>
      opts?.forceRefresh ? 'fresh' : 'stale');
    vi.doMock('./auth', async (original) => ({
      ...(await original<typeof import('./auth')>()),
      getApiToken: token,
    }));
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('', { status: 401 }))
      .mockResolvedValueOnce(new Response('', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const freshApi = await import('./api');
    await expect(freshApi.authedFetch({
      kind: 'azure', id: 'alice@example.com', homeAccountId: 'home-1',
    }, '/api/retry')).resolves.toMatchObject({ status: 200 });
    expect(token).toHaveBeenNthCalledWith(2, expect.anything(), { forceRefresh: true });
  });

  it('preserves caller headers when no API token is available', async () => {
    const fetchMock = stubExact('/api/plain', new Response(null, { status: 204 }));
    await authedFetch({ kind: 'manual', id: 'coverage' }, '/api/plain', {
      headers: { Accept: 'text/plain' },
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/plain', expect.objectContaining({
      headers: { Accept: 'text/plain', 'X-KB-User': 'coverage' },
    }));
  });

  it('coerces import preview defaults and every optional provenance field', () => {
    expect(coerceImportPreview({
      kind: 'issue', total_hint: -2, fetched: 1.6, truncated: true, note: 7,
      drafts: [{
        title: 'one', link: ' x#1 ', external_key: ' x:1 ', url: ' https://x/1 ',
        duplicate_of: { id: ' old ', title: ' Older ', via: 'similar' },
      }, null],
    })).toEqual(expect.objectContaining({
      kind: 'issue', total_hint: 0, fetched: 2, truncated: true, note: '',
      drafts: [expect.objectContaining({
        link: 'x#1', external_key: 'x:1', url: 'https://x/1',
        duplicate_of: { id: 'old', title: 'Older', via: 'similar' },
      })],
    }));
  });

  it('uses import preview defaults for a non-object response', () => {
    expect(coerceImportPreview(null)).toEqual({
      kind: 'project', total_hint: 0, fetched: 0, truncated: false, note: '', drafts: [],
    });
  });

  it('drops blank optional import fields and keeps a link duplicate without an id', () => {
    expect(coerceImportPreview({
      kind: 'issue', total_hint: 'many', fetched: Number.NaN, truncated: 1,
      note: ' note ', drafts: [{
        title: 'one', link: ' ', external_key: 7, url: '',
        duplicate_of: { title: 'same', via: 'link' },
      }],
    })).toEqual(expect.objectContaining({
      kind: 'issue', total_hint: 0, fetched: 0, truncated: false, note: ' note ',
      drafts: [expect.objectContaining({
        title: 'one', duplicate_of: { title: 'same', via: 'link' },
      })],
    }));
  });

  it('drops malformed duplicate metadata and non-array drafts', () => {
    expect(coerceImportPreview({
      kind: 'project', drafts: 'invalid', note: null,
    })).toEqual({
      kind: 'project', total_hint: 0, fetched: 0, truncated: false, note: '', drafts: [],
    });
    expect(coerceImportPreview({ drafts: [{
      title: 'one', duplicate_of: { title: '', via: 'other' },
    }] }).drafts[0]).not.toHaveProperty('duplicate_of');
  });

  it('rejects a failed import preview', async () => {
    stubExact('/api/import/preview', new Response('bad reference', { status: 400 }));
    await expect(importPreview(identity, { source: 'x', ref: 'bad', max: 8 }, new AbortController().signal))
      .rejects.toThrow('bad reference');
  });

  it('rejects a failed import-link journal write', async () => {
    stubExact('/api/import/links', new Response('quota', { status: 507 }));
    await expect(recordImportLinks(identity, { source: 'x', items: [] })).rejects.toMatchObject({ status: 507 });
  });

  it('accepts a successful import-link journal write', async () => {
    stubExact('/api/import/links', new Response(null, { status: 204 }));
    await expect(recordImportLinks(identity, { source: 'x', items: [] })).resolves.toBeUndefined();
  });

  it('rejects drift states missing the required change flag', () => {
    expect(() => coerceDriftResult({
      state: 'unchanged', link: 'x#1', url: 'https://x/1',
      upstream_title: '', baseline_title: '', summary: '',
      baseline_at: '2026-08-01T00:00:00Z', checked_at: '2026-08-01T00:00:00Z',
    })).toThrow('invalid drift response');
  });

  it('rejects drift fields that are absent or blank', () => {
    expect(() => coerceDriftResult({
      state: 'baseline_recorded', link: '', url: 'x', upstream_title: '', baseline_title: '',
      baseline_at: '2026-08-01T00:00:00Z', checked_at: '2026-08-01T00:00:00Z', summary: '',
    })).toThrow('invalid drift response');
  });

  it('rejects malformed provenance items instead of dropping them', () => {
    expect(() => coerceImportProvenanceItems({ items: [{ source: 'x' }] }))
      .toThrow('invalid import provenance response');
  });

  it('coerces valid provenance and defaults a non-string title', () => {
    expect(coerceImportProvenanceItems({ items: [{
      source: ' x ', external_key: ' x:1 ', url: ' https://x/1 ', title: 7,
    }] })).toEqual([{ source: 'x', external_key: 'x:1', url: 'https://x/1', title: '' }]);
  });

  it.each([null, {}, { items: 'invalid' }])(
    'rejects malformed provenance collection %j',
    (body) => expect(() => coerceImportProvenanceItems(body)).toThrow('invalid import provenance response'),
  );

  it('rejects a failed provenance lookup', async () => {
    stubExact('/api/import/provenance', new Response('', { status: 404 }));
    await expect(getImportProvenance(identity, 'x#1', new AbortController().signal))
      .rejects.toThrow('import provenance lookup failed: 404');
  });

  it('classifies stale drift acceptance as a conflict', async () => {
    stubExact('/api/import/drift/accept', new Response('stale revision', { status: 409 }));
    await expect(acceptDrift(identity, 'x', 'x:1', 'a'.repeat(64), new AbortController().signal))
      .rejects.toBeInstanceOf(DriftConflictError);
  });

  it('rejects a non-conflict drift acceptance failure normally', async () => {
    stubExact('/api/import/drift/accept', new Response('provider down', { status: 502 }));
    await expect(acceptDrift(identity, 'x', 'x:1', 'a'.repeat(64), new AbortController().signal))
      .rejects.toThrow('provider down');
  });

  it('rejects a successful drift acceptance with no valid timestamp', async () => {
    stubExact('/api/import/drift/accept', new Response('{}', { status: 200 }));
    await expect(acceptDrift(identity, 'x', 'x:1', 'a'.repeat(64), new AbortController().signal))
      .rejects.toThrow('invalid drift acceptance response');
  });

  it('accepts a valid drift baseline timestamp', async () => {
    stubExact('/api/import/drift/accept', new Response(JSON.stringify({
      baseline_at: '2026-08-01T00:00:00Z',
    }), { status: 200 }));
    await expect(acceptDrift(
      identity, 'x', 'x:1', 'a'.repeat(64), new AbortController().signal,
    ))
      .resolves.toEqual({ baseline_at: '2026-08-01T00:00:00Z' });
  });

  it('coerces similar items while dropping malformed rows', () => {
    expect(coerceSimilarItems({ items: [null, {}, {
      id: '1', title: ' Work ', status: 'done', via: 'killed', link: 'x#1',
      reason: 'duplicate', killed_at: '2026-08-01T00:00:00Z',
    }] })).toEqual([{
      id: '1', title: 'Work', status: 'done', via: 'killed', link: 'x#1',
      reason: 'duplicate', killedAt: '2026-08-01T00:00:00Z',
    }]);
  });

  it.each([null, {}, { items: 'invalid' }])(
    'returns no similar items for malformed response %j',
    (body) => expect(coerceSimilarItems(body)).toEqual([]),
  );

  it('contains failed similar-work lookups', async () => {
    stubExact('/api/similar?q=work', new Error('offline'));
    await expect(getSimilar(identity, 'work')).resolves.toEqual([]);
  });

  it('rejects a failed drift check', async () => {
    stubExact('/api/import/drift', new Response('', { status: 500 }));
    await expect(checkDrift(identity, 'x', 'x:1', new AbortController().signal))
      .rejects.toThrow('drift check failed: 500');
  });

  it('rejects a failed tombstone journal write', async () => {
    stubExact('/api/tombstones', new Response('', { status: 503 }));
    await expect(recordTombstone(identity, 'task', 'reason')).rejects.toMatchObject({ status: 503 });
  });
});
