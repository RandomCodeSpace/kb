import type { Identity } from './auth';
import { authedFetch } from './api';

export interface AISettings {
  ai_base_url: string;
  ai_model: string;
  has_key: boolean;
}

export interface SettingsPatch {
  ai_base_url?: string;
  ai_model?: string;
  ai_key?: string;
}

export interface AITestResult {
  ok: boolean;
  error?: string;
}

export interface AITestProbe {
  ai_base_url: string;
  ai_model: string;
  ai_key?: string;
}

async function errText(res: Response, fallback: string): Promise<string> {
  try {
    const text = (await res.text()).trim();
    return text === '' ? fallback : text.slice(0, 200);
  } catch {
    return fallback;
  }
}

export async function getSettings(identity: Identity): Promise<AISettings> {
  const res = await authedFetch(identity, '/api/settings');
  if (!res.ok) throw new Error(`GET /api/settings failed: ${res.status}`);
  const body = (await res.json()) as Record<string, unknown>;
  return {
    ai_base_url: typeof body.ai_base_url === 'string' ? body.ai_base_url : '',
    ai_model: typeof body.ai_model === 'string' ? body.ai_model : '',
    has_key: body.has_key === true,
  };
}

export interface PutSettingsResult {
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
    const body = (await res.json()) as { key_cleared?: unknown };
    return { keyCleared: body.key_cleared === true };
  } catch {
    return { keyCleared: false };
  }
}

export async function aiTest(
  identity: Identity,
  probe?: AITestProbe,
  signal?: AbortSignal,
): Promise<AITestResult> {
  const init: RequestInit = { method: 'POST', signal };
  if (probe) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(probe);
  }
  const res = await authedFetch(identity, '/api/ai/test', init);
  if (!res.ok) return { ok: false, error: await errText(res, `test failed: ${res.status}`) };
  const body = (await res.json()) as { ok?: unknown; error?: unknown };
  if (body.ok === true) return { ok: true };
  return {
    ok: false,
    error:
      typeof body.error === 'string' && body.error !== ''
        ? body.error
        : 'connection failed',
  };
}
