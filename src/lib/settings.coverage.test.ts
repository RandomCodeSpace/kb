import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Identity } from './auth';
import { aiTest, getSettings, putSettings } from './settings';

const identity: Identity = { kind: 'manual', id: 'coverage-user' };

afterEach(() => {
  vi.unstubAllGlobals();
});

function response(body: BodyInit | null, status = 200): Response {
  return new Response(body, { status, headers: { 'Content-Type': 'application/json' } });
}

function denyUnless(expected: string, result: Response | Promise<Response>) {
  const mock = vi.fn((url: string) => {
    if (url !== expected) throw new Error(`unexpected egress: ${url}`);
    return Promise.resolve(result);
  });
  vi.stubGlobal('fetch', mock);
  return mock;
}

describe('settings response validation', () => {
  it('preserves valid settings fields', async () => {
    denyUnless('/api/settings', response(JSON.stringify({
      ai_base_url: 'https://llm.test/v1',
      ai_model: 'model',
      has_key: true,
    })));
    await expect(getSettings(identity)).resolves.toEqual({
      ai_base_url: 'https://llm.test/v1', ai_model: 'model', has_key: true,
    });
  });

  it('coerces malformed settings fields to safe defaults', async () => {
    denyUnless('/api/settings', response(JSON.stringify({
      ai_base_url: 42,
      ai_model: null,
      has_key: 'yes',
    })));
    await expect(getSettings(identity)).resolves.toEqual({
      ai_base_url: '', ai_model: '', has_key: false,
    });
  });

  it('rejects a failed settings load', async () => {
    denyUnless('/api/settings', response('unavailable', 503));
    await expect(getSettings(identity)).rejects.toThrow('GET /api/settings failed: 503');
  });

  it('uses a bounded response body for a failed save', async () => {
    denyUnless('/api/settings', response(`  ${'x'.repeat(250)}  `, 400));
    await expect(putSettings(identity, { ai_model: 'm' })).rejects.toThrow('x'.repeat(200));
  });

  it('uses the fallback when a failed save has an empty body', async () => {
    denyUnless('/api/settings', response('   ', 500));
    await expect(putSettings(identity, {})).rejects.toThrow('save settings failed: 500');
  });

  it('uses the fallback when a failed save body cannot be read', async () => {
    const unreadable = {
      ok: false,
      status: 500,
      text: () => Promise.reject(new Error('stream failed')),
    } as Response;
    denyUnless('/api/settings', unreadable);
    await expect(putSettings(identity, {})).rejects.toThrow('save settings failed: 500');
  });

  it('reports no key clearing for a no-content save', async () => {
    denyUnless('/api/settings', response(null, 204));
    await expect(putSettings(identity, {})).resolves.toEqual({ keyCleared: false });
  });

  it('reports key clearing only for an explicit boolean true', async () => {
    denyUnless('/api/settings', response(JSON.stringify({ key_cleared: true })));
    await expect(putSettings(identity, { ai_key: '' })).resolves.toEqual({ keyCleared: true });
  });

  it('survives a malformed successful save response', async () => {
    denyUnless('/api/settings', response('not-json'));
    await expect(putSettings(identity, {})).resolves.toEqual({ keyCleared: false });
  });

  it('uses a bounded server error for a failed AI test', async () => {
    denyUnless('/api/ai/test', response('provider refused', 502));
    await expect(aiTest(identity)).resolves.toEqual({ ok: false, error: 'provider refused' });
  });

  it('uses a stable fallback for a malformed AI test result', async () => {
    denyUnless('/api/ai/test', response(JSON.stringify({ ok: false, error: 7 })));
    await expect(aiTest(identity)).resolves.toEqual({ ok: false, error: 'connection failed' });
  });

  it('preserves a non-empty provider failure message', async () => {
    denyUnless('/api/ai/test', response(JSON.stringify({ ok: false, error: 'model missing' })));
    await expect(aiTest(identity)).resolves.toEqual({ ok: false, error: 'model missing' });
  });

  it('replaces an empty provider failure message', async () => {
    denyUnless('/api/ai/test', response(JSON.stringify({ ok: false, error: '' })));
    await expect(aiTest(identity)).resolves.toEqual({ ok: false, error: 'connection failed' });
  });
});
