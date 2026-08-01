import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Identity } from './auth';
import {
  aiTest,
  aiStories,
  acceptDrift,
  checkDrift,
  coerceDriftResult,
  coerceImportProvenanceItems,
  coerceImportPreview,
  coerceStoryDraft,
  deleteIntegration,
  DriftConflictError,
  forgeTest,
  getImportProvenance,
  getIntegrations,
  getSettings,
  importPreview,
  isAbortError,
  killReasonRequest,
  putIntegration,
  putSettings,
  recordImportLinks,
  recordTombstone,
} from './api';
import type { ForgeSource } from './api';
import { parse, serialize } from './markdown';
import { newTask } from './model';

const identity: Identity = { kind: 'manual', id: 'alice' };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('settings API compatibility', () => {
  it('keeps the established settings functions available from api', () => {
    expect(typeof getSettings).toBe('function');
    expect(typeof putSettings).toBe('function');
    expect(typeof aiTest).toBe('function');
  });
});

describe('integrations API', () => {
  function jsonResponse(body: unknown, status = 200): Response {
    return new Response(status === 204 ? null : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  function stubResponse(body: unknown, status = 200) {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(body, status)),
    );
    vi.stubGlobal('fetch', fetchMock);
    return fetchMock;
  }

  it('coerces public source fields and never retains a PAT from the server', async () => {
    const fetchMock = stubResponse({
      sources: [
        {
          name: 'work',
          kind: 'gitlab',
          base_url: 'https://gitlab.example.com',
          has_token: true,
          pat: 'glpat-server-leak',
          internal: 'discard me',
        },
        {
          name: 'github',
          kind: 'github',
          base_url: 'https://github.com',
          has_token: false,
        },
        null,
        {
          name: 7,
          kind: 'gitlab',
          base_url: 'https://invalid.example.com',
          has_token: true,
        },
      ],
    });

    const sources: ForgeSource[] = await getIntegrations(identity);

    expect(sources).toEqual([
      {
        name: 'work',
        kind: 'gitlab',
        base_url: 'https://gitlab.example.com',
        has_token: true,
      },
      {
        name: 'github',
        kind: 'github',
        base_url: 'https://github.com',
        has_token: false,
      },
    ]);
    expect(JSON.stringify(sources)).not.toContain('glpat-server-leak');
    const call = fetchMock.mock.calls[0];
    if (!call) throw new Error('expected integrations fetch');
    expect(call[0]).toBe('/api/integrations');
    expect(call[1]?.headers).toMatchObject({ 'X-KB-User': 'alice' });
  });

  it('sends a PAT only in the PUT JSON body and exposes only tokenCleared', async () => {
    const fetchMock = stubResponse({ token_cleared: true });
    const pat = 'glpat-client-secret';

    const result = await putIntegration(identity, 'work', {
      kind: 'gitlab',
      base_url: 'gitlab.example.com',
      pat,
    });

    expect(result).toEqual({ tokenCleared: true });
    expect(JSON.stringify(result)).not.toContain(pat);
    const call = fetchMock.mock.calls[0];
    if (!call) throw new Error('expected integration PUT');
    const [url, init] = call;
    expect(url).toBe('/api/integrations/work');
    expect(init?.method).toBe('PUT');
    expect(init?.headers).toMatchObject({
      'Content-Type': 'application/json',
      'X-KB-User': 'alice',
    });
    expect(JSON.parse(String(init?.body))).toEqual({
      kind: 'gitlab',
      base_url: 'gitlab.example.com',
      pat,
    });
    expect(`${url}\n${JSON.stringify(init?.headers)}`).not.toContain(pat);
  });

  it('maps a no-content PUT response to tokenCleared false', async () => {
    stubResponse(null, 204);

    await expect(
      putIntegration(identity, 'github', { kind: 'github' }),
    ).resolves.toEqual({ tokenCleared: false });
  });

  it('deletes the named source through authedFetch without a body', async () => {
    const fetchMock = stubResponse(null, 204);

    await deleteIntegration(identity, 'work');

    const call = fetchMock.mock.calls[0];
    if (!call) throw new Error('expected integration DELETE');
    expect(call[0]).toBe('/api/integrations/work');
    expect(call[1]?.method).toBe('DELETE');
    expect(call[1]?.body).toBeUndefined();
    expect(call[1]?.headers).toMatchObject({ 'X-KB-User': 'alice' });
  });

  it('sends an unsaved test PAT only in JSON and retains no secret in the result', async () => {
    const fetchMock = stubResponse({ ok: false, error: 'connection failed' });
    const pat = 'ghp_unsaved_secret';

    const result = await forgeTest(identity, 'github', {
      base_url: 'github.example.com',
      pat,
    });

    expect(result).toEqual({ ok: false, error: 'connection failed' });
    expect(JSON.stringify(result)).not.toContain(pat);
    const call = fetchMock.mock.calls[0];
    if (!call) throw new Error('expected forge test POST');
    const [url, init] = call;
    expect(url).toBe('/api/integrations/github/test');
    expect(init?.method).toBe('POST');
    expect(init?.headers).toMatchObject({
      'Content-Type': 'application/json',
      'X-KB-User': 'alice',
    });
    expect(JSON.parse(String(init?.body))).toEqual({
      base_url: 'github.example.com',
      pat,
    });
    expect(`${url}\n${JSON.stringify(init?.headers)}`).not.toContain(pat);
  });

  it('never throws when the forge test request itself rejects', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('network down'))),
    );

    const result = await forgeTest(identity, 'work');

    expect(result.ok).toBe(false);
    expect(result.error).toEqual(expect.any(String));
  });
});

describe('import API', () => {
  function jsonResponse(body: unknown, status = 200): Response {
    return new Response(status === 204 ? null : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  it('coerces preview metadata and keeps linked duplicate provenance', () => {
    // D3 metadata must survive the client boundary so review and journaling work.
    expect(
      coerceImportPreview({
        kind: 'issue',
        total_hint: 34.8,
        fetched: 1,
        truncated: true,
        note: 'rate limited — partial results (1 of 34)',
        drafts: [
          {
            title: '  ship login ',
            tags: ['team::auth', 'link::gitlab#42'],
            link: 'gitlab#42',
            external_key: 'gitlab:gitlab.example.com/acme/app#42',
            url: 'https://gitlab.example.com/acme/app/-/issues/42',
            duplicate_of: {
              id: 'task-1',
              title: 'existing login',
              via: 'link',
            },
          },
          { title: '   ', link: 'gitlab#99' },
        ],
      }),
    ).toEqual({
      kind: 'issue',
      total_hint: 35,
      fetched: 1,
      truncated: true,
      note: 'rate limited — partial results (1 of 34)',
      drafts: [
        {
          title: 'ship login',
          emoji: '',
          desc: '',
          prio: 3,
          due: '',
          effort: '',
          tags: ['team::auth', 'link::gitlab#42'],
          checks: [],
          link: 'gitlab#42',
          external_key: 'gitlab:gitlab.example.com/acme/app#42',
          url: 'https://gitlab.example.com/acme/app/-/issues/42',
          duplicate_of: {
            id: 'task-1',
            title: 'existing login',
            via: 'link',
          },
        },
      ],
    });
  });

  it('posts a preview through authedFetch with the caller abort signal', async () => {
    // Cancel must reach fetch rather than merely hiding the modal spinner.
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(
        jsonResponse({
          kind: 'project',
          total_hint: 0,
          fetched: 0,
          truncated: false,
          note: '',
          drafts: [],
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const ctrl = new AbortController();

    await importPreview(
      identity,
      { source: 'work', ref: 'acme/app', max: 8 },
      ctrl.signal,
    );

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/api/import/preview');
    expect(init?.signal).toBe(ctrl.signal);
    expect(JSON.parse(String(init?.body))).toEqual({
      source: 'work',
      ref: 'acme/app',
      max: 8,
    });
  });

  it('throws the capped server text when preview fails', async () => {
    // The modal needs the server's safe explanation, not a generic status code.
    const detail = `bad reference ${'x'.repeat(240)}`;
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(detail, { status: 400 }))),
    );

    await expect(
      importPreview(
        identity,
        { source: 'work', ref: 'bad', max: 8 },
        new AbortController().signal,
      ),
    ).rejects.toThrow(detail.slice(0, 200));
  });

  it('rejects HTTP and fetch failures so provenance can be retried durably', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('no', { status: 500 }))
      .mockRejectedValueOnce(new TypeError('offline'));
    vi.stubGlobal('fetch', fetchMock);
    const request = {
      source: 'work',
      items: [
        {
          external_key: 'gitlab:gitlab.example.com/acme/app#42',
          link: 'gitlab#42',
          url: 'https://gitlab.example.com/acme/app/-/issues/42',
          title: 'ship login',
        },
      ],
    };

    await expect(recordImportLinks(identity, request)).rejects.toMatchObject({
      status: 500,
      message: 'no',
    });
    await expect(recordImportLinks(identity, request)).rejects.toThrow('offline');
  });

  it('coerces every honest drift state without retaining extra fields', () => {
    // A malformed or embellished reply must not invent a comparison claim.
    const common = {
      link: 'gitlab#42',
      url: 'https://gitlab.example.com/acme/app/-/issues/42',
      upstream_title: 'ship login',
      baseline_title: 'draft login',
      baseline_at: '2026-03-14T09:12:00Z',
      checked_at: '2026-07-29T18:00:00Z',
      summary: '',
      internal: 'discard me',
    };

    expect(
      coerceDriftResult({
        ...common,
        state: 'baseline_recorded',
        title_changed: true,
      }),
    ).toEqual({
      state: 'baseline_recorded',
      link: common.link,
      url: common.url,
      upstream_title: common.upstream_title,
      baseline_title: common.baseline_title,
      baseline_at: common.baseline_at,
      checked_at: common.checked_at,
      summary: '',
    });
    expect(
      coerceDriftResult({
        ...common,
        state: 'unchanged',
        title_changed: false,
      }),
    ).toMatchObject({ state: 'unchanged', title_changed: false });
    expect(
      coerceDriftResult({
        ...common,
        state: 'drifted',
        title_changed: true,
        summary: 'The login flow now requires SAML.',
        revision: 'a'.repeat(64),
      }),
    ).toMatchObject({
      state: 'drifted',
      title_changed: true,
      summary: 'The login flow now requires SAML.',
      revision: 'a'.repeat(64),
    });
  });

  it('rejects malformed drift replies instead of guessing at server state', () => {
    // Visible checks may fail, but they must never report a baseline we cannot prove.
    expect(() => coerceDriftResult(null)).toThrow('invalid drift response');
    expect(() =>
      coerceDriftResult({
        state: 'drifted',
        link: 'gitlab#42',
        url: 'https://gitlab.example.com/acme/app/-/issues/42',
        upstream_title: 'ship login',
        baseline_title: 'draft login',
        baseline_at: 'not-a-date',
        checked_at: '2026-07-29T18:00:00Z',
        summary: '',
        title_changed: true,
        revision: 'a'.repeat(64),
      }),
    ).toThrow('invalid drift response');
    expect(() =>
      coerceDriftResult({
        state: 'drifted',
        link: 'gitlab#42',
        url: 'https://gitlab.example.com/acme/app/-/issues/42',
        upstream_title: 'ship login',
        baseline_title: 'draft login',
        baseline_at: '2026-03-14T09:12:00Z',
        checked_at: '2026-07-29T18:00:00Z',
        summary: '',
        title_changed: true,
      }),
    ).toThrow('invalid drift response');
  });

  it('posts the exact drift identity and propagates the caller abort signal', async () => {
    // The check is one user-triggered request; it must not outlive the modal.
    const response = {
      state: 'unchanged',
      link: 'gitlab#42',
      url: 'https://gitlab.example.com/acme/app/-/issues/42',
      title_changed: false,
      upstream_title: 'ship login',
      baseline_title: 'ship login',
      baseline_at: '2026-03-14T09:12:00Z',
      checked_at: '2026-07-29T18:00:00Z',
      summary: '',
    };
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(response)),
    );
    vi.stubGlobal('fetch', fetchMock);
    const ctrl = new AbortController();

    await expect(
      checkDrift(
        identity,
        'work',
        'gitlab:gitlab.example.com/acme/app#42',
        ctrl.signal,
      ),
    ).resolves.toEqual(response);

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/api/import/drift');
    expect(init?.method).toBe('POST');
    expect(init?.signal).toBe(ctrl.signal);
    expect(init?.headers).toMatchObject({
      'Content-Type': 'application/json',
      'X-KB-User': 'alice',
    });
    expect(JSON.parse(String(init?.body))).toEqual({
      source: 'work',
      external_key: 'gitlab:gitlab.example.com/acme/app#42',
    });
  });

  it('throws the capped server text when an explicit drift check fails', async () => {
    // This path is deliberately unlike best-effort provenance journaling.
    const detail = `upstream unavailable ${'x'.repeat(240)}`;
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(detail, { status: 502 }))),
    );

    await expect(
      checkDrift(
        identity,
        'work',
        'gitlab:gitlab.example.com/acme/app#42',
        new AbortController().signal,
      ),
    ).rejects.toThrow(detail.slice(0, 200));
  });

  it('accepts only the exact server revision the user reviewed', async () => {
    // Baseline acceptance is explicit and revision-bound, never best-effort.
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(
        jsonResponse({ baseline_at: '2026-07-29T18:01:00Z' }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const ctrl = new AbortController();
    const revision = 'b'.repeat(64);

    await expect(
      acceptDrift(
        identity,
        'work',
        'gitlab:gitlab.example.com/acme/app#42',
        revision,
        ctrl.signal,
      ),
    ).resolves.toEqual({ baseline_at: '2026-07-29T18:01:00Z' });

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/api/import/drift/accept');
    expect(init?.method).toBe('POST');
    expect(init?.signal).toBe(ctrl.signal);
    expect(JSON.parse(String(init?.body))).toEqual({
      source: 'work',
      external_key: 'gitlab:gitlab.example.com/acme/app#42',
      revision,
    });
  });

  it('distinguishes a stale reviewed revision from a retryable failure', async () => {
    // The modal must clear stale review text before asking for another check.
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response('upstream changed; check again', { status: 409 }),
        ),
      ),
    );

    await expect(
      acceptDrift(
        identity,
        'work',
        'gitlab:gitlab.example.com/acme/app#42',
        'c'.repeat(64),
        new AbortController().signal,
      ),
    ).rejects.toBeInstanceOf(DriftConflictError);
  });

  it('coerces every provenance candidate without retaining server internals', () => {
    // One short link may name several projects, so every valid candidate survives.
    expect(
      coerceImportProvenanceItems({
        items: [
          {
            source: 'work',
            external_key: 'gitlab:gitlab.example.com/acme/app#42',
            title: 'App issue',
            url: 'https://gitlab.example.com/acme/app/-/issues/42',
            kind: 'gitlab',
            pat: 'must-not-survive',
          },
          {
            source: 'work',
            external_key: 'gitlab:gitlab.example.com/acme/api#42',
            title: 'API issue',
            url: 'https://gitlab.example.com/acme/api/-/issues/42',
          },
        ],
      }),
    ).toEqual([
      {
        source: 'work',
        external_key: 'gitlab:gitlab.example.com/acme/app#42',
        title: 'App issue',
        url: 'https://gitlab.example.com/acme/app/-/issues/42',
      },
      {
        source: 'work',
        external_key: 'gitlab:gitlab.example.com/acme/api#42',
        title: 'API issue',
        url: 'https://gitlab.example.com/acme/api/-/issues/42',
      },
    ]);
    expect(() => coerceImportProvenanceItems({ items: [null] })).toThrow(
      'invalid import provenance response',
    );
  });

  it('looks up the exact short link without deriving a forge identity', async () => {
    // Project and source stay server-authored; the client sends only its durable tag.
    const response = {
      items: [
        {
          source: 'work',
          external_key: 'gitlab:gitlab.example.com/acme/app#42',
          title: 'App issue',
          url: 'https://gitlab.example.com/acme/app/-/issues/42',
        },
      ],
    };
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(jsonResponse(response)),
    );
    vi.stubGlobal('fetch', fetchMock);
    const ctrl = new AbortController();

    await expect(
      getImportProvenance(identity, 'gitlab#42', ctrl.signal),
    ).resolves.toEqual(response.items);

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/api/import/provenance');
    expect(init?.signal).toBe(ctrl.signal);
    expect(JSON.parse(String(init?.body))).toEqual({ link: 'gitlab#42' });
  });
});

describe('tombstone API', () => {
  it('builds a trimmed reason request and rejects blank reasons', () => {
    expect(killReasonRequest('task-1', '  superseded by SSO  ')).toEqual({
      taskId: 'task-1',
      reason: 'superseded by SSO',
    });
    expect(killReasonRequest('task-1', ' \t ')).toBeNull();
  });

  it('records a reason through the authenticated tombstone endpoint', async () => {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(new Response(null, { status: 204 })),
    );
    vi.stubGlobal('fetch', fetchMock);

    await recordTombstone(identity, 'task-1', 'superseded by SSO');

    const call = fetchMock.mock.calls[0];
    if (!call) throw new Error('expected tombstone fetch');
    expect(call[0]).toBe('/api/tombstones');
    expect(call[1]?.method).toBe('POST');
    expect(call[1]?.headers).toMatchObject({
      'Content-Type': 'application/json',
      'X-KB-User': 'alice',
    });
    expect(JSON.parse(String(call[1]?.body))).toEqual({
      task_id: 'task-1',
      reason: 'superseded by SSO',
    });
  });

  it('rejects HTTP and fetch failures so tombstones can be retried durably', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('no', { status: 500 }))
      .mockRejectedValueOnce(new TypeError('offline'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      recordTombstone(identity, 'task-1', 'superseded'),
    ).rejects.toMatchObject({ status: 500, message: 'no' });
    await expect(
      recordTombstone(identity, 'task-1', 'superseded'),
    ).rejects.toThrow('offline');
  });
});

describe('coerceStoryDraft', () => {
  it('passes a well-formed draft through', () => {
    const d = coerceStoryDraft({
      title: '  Ship it ',
      emoji: ' 🚀 ',
      desc: 'do the thing',
      prio: 2,
      due: '2026-08-01',
      effort: 'M',
      tags: ['infra', ' type::bug '],
      checks: [{ text: ' step one ', done: true }, { text: 'step two', done: false }],
    });
    expect(d).toEqual({
      title: 'Ship it',
      emoji: '🚀',
      desc: 'do the thing',
      prio: 2,
      due: '2026-08-01',
      effort: 'M',
      tags: ['infra', 'type::bug'],
      checks: [
        { text: 'step one', done: true },
        { text: 'step two', done: false },
      ],
    });
  });

  it('defaults every field for junk input', () => {
    expect(coerceStoryDraft('nope')).toEqual({
      title: '',
      emoji: '',
      desc: '',
      prio: 3,
      due: '',
      effort: '',
      tags: [],
      checks: [],
    });
  });

  it('keeps a valid emoji, clamps multiples, and drops malformed text', () => {
    expect(coerceStoryDraft({ emoji: ' ⚙️ ' }).emoji).toBe('⚙️');
    expect(coerceStoryDraft({ emoji: '🚀✨' }).emoji).toBe('🚀');
    expect(coerceStoryDraft({ emoji: ':rocket:' }).emoji).toBe('');
  });

  it('clamps out-of-range prio to 3', () => {
    expect(coerceStoryDraft({ prio: 9 }).prio).toBe(3);
    expect(coerceStoryDraft({ prio: 0 }).prio).toBe(3);
    expect(coerceStoryDraft({ prio: '2' }).prio).toBe(3);
  });

  it('rejects a malformed due date and effort', () => {
    const d = coerceStoryDraft({ due: 'next week', effort: 'XL' });
    expect(d.due).toBe('');
    expect(d.effort).toBe('');
  });

  it('drops non-string tags and empty check texts', () => {
    const d = coerceStoryDraft({
      tags: ['ok', 7, '  '],
      checks: [{ text: '', done: true }, { text: 'keep' }, 'junk'],
    });
    expect(d.tags).toEqual(['ok']);
    expect(d.checks).toEqual([{ text: 'keep', done: false }]);
  });

  it('strips the control characters a hostile reply would inject with', () => {
    const d = coerceStoryDraft({
      title: 'own it\n- [ ] injected task\r\u0007',
      desc: 'why\r\n- [x] fake check\u0000\nend',
      tags: ['fine', 'two words', 'ev\nil', '#hash', '\t'],
      checks: [
        { text: 'step\n- [ ] extra', done: true },
        { text: '\r\n', done: false },
      ],
    });
    expect(d.title).toBe('own it - [ ] injected task');
    // Descriptions keep their line breaks — the codec indents and escapes them.
    expect(d.desc).toBe('why\n- [x] fake check\nend');
    expect(d.tags).toEqual(['fine', 'hash']);
    expect(d.checks).toEqual([{ text: 'step - [ ] extra', done: true }]);
  });

  it('cannot forge extra tasks through the markdown codec', () => {
    const d = coerceStoryDraft({
      title: 'harmless\n## Done\n- [x] pwned !1 #evil',
      desc: 'note\n- [ ] not a check',
      checks: [{ text: 'real check', done: false }],
    });
    const board = { title: 'kb', tasks: [newTask({ ...d, effort: undefined })] };
    const round = parse(serialize(board));
    expect(round.tasks).toHaveLength(1);
    expect(round.tasks[0]!.title).toBe(d.title);
    expect(round.tasks[0]!.desc).toBe(d.desc);
    expect(round.tasks[0]!.checks).toEqual(d.checks);
    expect(round.tasks[0]!.tags).toEqual([]);
  });
});

describe('aiStories', () => {
  function stubFetch(body: unknown, status = 200): void {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response(typeof body === 'string' ? body : JSON.stringify(body), {
            status,
            headers: { 'Content-Type': 'application/json' },
          }),
        ),
      ),
    );
  }

  it('coerces every story and drops the ones left title-less', async () => {
    stubFetch({
      stories: [
        { title: ' Split the store ', prio: 1, effort: 'S', tags: ['infra'] },
        { title: '   ', desc: 'no title, unusable' },
        { title: 'Wire it up\nmore', prio: 99 },
      ],
    });
    const out = await aiStories(identity, { adr: '# ADR', max: 3 });
    expect(out).toHaveLength(2);
    expect(out[0]!.title).toBe('Split the store');
    expect(out[0]!.prio).toBe(1);
    expect(out[1]).toMatchObject({ title: 'Wire it up more', prio: 3 });
  });

  it('returns [] when the reply has no stories array', async () => {
    stubFetch({ stories: 'nope' });
    expect(await aiStories(identity, { adr: '# ADR' })).toEqual([]);
  });

  it('throws with the server message on a failure', async () => {
    stubFetch('adr too large', 413);
    await expect(aiStories(identity, { adr: 'x' })).rejects.toThrow(
      'adr too large',
    );
  });
});

describe('isAbortError', () => {
  it('recognises the rejection an aborted fetch produces', () => {
    const ctrl = new AbortController();
    ctrl.abort();
    expect(isAbortError(ctrl.signal.reason)).toBe(true);
  });

  it('does not swallow a real failure', () => {
    expect(isAbortError(new Error('upstream 500'))).toBe(false);
    expect(isAbortError(null)).toBe(false);
    expect(isAbortError('AbortError')).toBe(false);
  });
});
