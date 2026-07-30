import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Identity } from './auth';
import { coerceSimilarItems, getSimilar } from './api';

const identity: Identity = { kind: 'manual', id: 'alice' };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('similar items', () => {
  it('coerces fields independently and drops items without a title', () => {
    expect(
      coerceSimilarItems({
        items: [
          {
            id: 'card-1',
            title: '  Fix login redirect  ',
            status: 'todo',
            via: 'card',
            link: 'link::gitlab#1',
          },
          { id: 'bad', title: '   ', via: 'card' },
          { title: 'Imported history', via: 'import', status: 7 },
          {
            id: 'killed-1',
            title: '  Rejected history  ',
            status: 'cancelled',
            via: 'killed',
            reason: 'superseded by SSO',
            killed_at: '2026-03-14T09:12:00Z',
          },
          {
            title: 'Rejected without metadata',
            via: 'killed',
            reason: 7,
            killed_at: null,
          },
          { title: 'Unknown source', via: 'other' },
        ],
      }),
    ).toEqual([
      {
        id: 'card-1',
        title: 'Fix login redirect',
        status: 'todo',
        via: 'card',
        link: 'link::gitlab#1',
      },
      { title: 'Imported history', via: 'import' },
      {
        id: 'killed-1',
        title: 'Rejected history',
        status: 'cancelled',
        via: 'killed',
        reason: 'superseded by SSO',
        killedAt: '2026-03-14T09:12:00Z',
      },
      { title: 'Rejected without metadata', via: 'killed' },
    ]);
  });

  it('returns coerced advisory matches and carries request details', async () => {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(
        new Response(JSON.stringify({ items: [{ title: 'Similar card', via: 'card' }] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const ctrl = new AbortController();

    await expect(
      getSimilar(
        identity,
        'login redirect',
        'card-1',
        ['gitlab#1', 'github#2'],
        ctrl.signal,
      ),
    ).resolves.toEqual([{ title: 'Similar card', via: 'card' }]);
    const firstCall = fetchMock.mock.calls[0];
    if (!firstCall) throw new Error('expected fetch to be called');
    const requestInit = firstCall[1];
    if (!requestInit) throw new Error('expected fetch request init');
    expect(firstCall[0]).toBe(
      '/api/similar?q=login+redirect&exclude=card-1&exclude_link=gitlab%231&exclude_link=github%232',
    );
    expect(requestInit.signal).toBe(ctrl.signal);
  });

  // A card can carry many tags; only the established list cap may become
  // repeated query parameters.
  it('caps repeated provenance exclusions at one hundred values', async () => {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(
        new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const links = Array.from({ length: 101 }, (_, index) => `gitlab#${index}`);

    await getSimilar(identity, 'login redirect', undefined, links);

    const firstCall = fetchMock.mock.calls[0];
    if (!firstCall) throw new Error('expected fetch to be called');
    const requestURL = new URL(firstCall[0], 'https://kb.example');
    expect(requestURL.searchParams.has('exclude')).toBe(false);
    expect(requestURL.searchParams.getAll('exclude_link')).toHaveLength(100);
    expect(requestURL.searchParams.getAll('exclude_link').at(-1)).toBe('gitlab#99');
  });

  it('returns no advisory matches when the server rejects the request', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 500 }))));

    await expect(getSimilar(identity, 'login redirect')).resolves.toEqual([]);
  });

  it('returns no advisory matches when the request rejects', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('network down'))));

    await expect(getSimilar(identity, 'login redirect')).resolves.toEqual([]);
  });

  it('returns no advisory matches when the request is aborted', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new DOMException('aborted', 'AbortError'))),
    );

    await expect(getSimilar(identity, 'login redirect')).resolves.toEqual([]);
  });
});
