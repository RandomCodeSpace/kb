import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Identity } from './auth';
import { aiTest } from './settings';

const identity: Identity = { kind: 'manual', id: 'alice' };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('aiTest', () => {
  function stubOk() {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) =>
      Promise.resolve(
        new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    return fetchMock;
  }

  function firstFetchCall(fetchMock: ReturnType<typeof stubOk>) {
    const call = fetchMock.mock.calls[0];
    if (!call) throw new Error('expected fetch to be called');
    return call;
  }

  it('sends no body at all when no values are supplied', async () => {
    const fetchMock = stubOk();
    expect(await aiTest(identity)).toEqual({ ok: true });
    const init = firstFetchCall(fetchMock)[1];
    if (!init) throw new Error('expected fetch request init');
    expect(init.method).toBe('POST');
    expect(init.body).toBeUndefined();
  });

  it('tests the values in the form rather than the saved ones', async () => {
    const fetchMock = stubOk();
    await aiTest(identity, {
      ai_base_url: 'https://llm.internal/v1',
      ai_model: 'tiny',
      ai_key: 'sk-unsaved',
    });
    const [url, init] = firstFetchCall(fetchMock);
    if (!init) throw new Error('expected fetch request init');
    expect(url).toBe('/api/ai/test');
    expect(JSON.parse(String(init.body))).toEqual({
      ai_base_url: 'https://llm.internal/v1',
      ai_model: 'tiny',
      ai_key: 'sk-unsaved',
    });
  });

  it('omits the key so the stored one is used, and never writes settings', async () => {
    const fetchMock = stubOk();
    await aiTest(identity, {
      ai_base_url: 'https://llm.internal/v1',
      ai_model: 'tiny',
    });
    const init = firstFetchCall(fetchMock)[1];
    if (!init) throw new Error('expected fetch request init');
    const body: unknown = JSON.parse(String(init.body));
    expect(body).not.toHaveProperty('ai_key');
    // A test is a test: nothing may reach PUT /api/settings.
    expect(
      fetchMock.mock.calls.some(
        ([url, init]) => url === '/api/settings' || init?.method === 'PUT',
      ),
    ).toBe(false);
  });

  it('carries the abort signal so a cancel really aborts', async () => {
    const fetchMock = stubOk();
    const ctrl = new AbortController();
    await aiTest(identity, { ai_base_url: 'u', ai_model: 'm' }, ctrl.signal);
    const init = firstFetchCall(fetchMock)[1];
    if (!init) throw new Error('expected fetch request init');
    expect(init.signal).toBe(ctrl.signal);
  });
});
