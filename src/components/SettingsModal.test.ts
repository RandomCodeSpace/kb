import { describe, expect, it } from 'vitest';
import { ReauthRequiredError } from '../lib/auth';
import {
  escapeAction,
  formStatus,
  loadErrorMessage,
  testProbe,
} from './SettingsModal';

describe('formStatus', () => {
  const idle = { kind: 'idle' } as const;

  it('is idle when nothing has happened — the reserved line stays empty', () => {
    expect(formStatus(idle, idle)).toEqual({ kind: 'idle' });
  });

  it('a running test outranks everything', () => {
    expect(
      formStatus({ kind: 'err', msg: 'save failed' }, { kind: 'busy' }).kind,
    ).toBe('busy');
  });

  it('a failed save stays visible over a later successful test', () => {
    // The test passing does not save anything — the save is still unsaved,
    // and hiding that behind "connection ok" would read as success.
    const s = formStatus(
      { kind: 'err', msg: 'save failed' },
      { kind: 'ok', msg: 'connection ok' },
    );
    expect(s).toEqual({ kind: 'err', msg: 'save failed', source: 'save' });
  });

  it('a test failure outranks a stale save success', () => {
    const s = formStatus(
      { kind: 'ok', msg: 'saved' },
      { kind: 'err', msg: 'connection failed' },
    );
    expect(s).toEqual({ kind: 'err', msg: 'connection failed', source: 'test' });
  });

  it('shows a success when that is the whole story', () => {
    expect(formStatus({ kind: 'ok', msg: 'saved' }, idle)).toEqual({
      kind: 'ok',
      msg: 'saved',
    });
    expect(formStatus(idle, { kind: 'ok', msg: 'connection ok' })).toEqual({
      kind: 'ok',
      msg: '✓ connection ok',
    });
  });
});

describe('loadErrorMessage', () => {
  it('sends an expired session to the reconnect control, not to reopening', () => {
    // Reopening re-issues the same unauthenticated request: the old wording
    // asked the user to repeat what had just failed.
    const msg = loadErrorMessage(new ReauthRequiredError());
    expect(msg).toContain('session expired');
    expect(msg).not.toContain('reopening');
  });

  it('keeps a plain failure distinct from an auth one', () => {
    expect(loadErrorMessage(new Error('GET /api/settings failed: 500'))).toBe(
      'could not load settings — the server did not answer',
    );
  });
});

describe('testProbe', () => {
  it('sends the values currently in the form', () => {
    expect(testProbe('https://llm.internal/v1', 'tiny', 'sk-unsaved')).toEqual({
      ai_base_url: 'https://llm.internal/v1',
      ai_model: 'tiny',
      ai_key: 'sk-unsaved',
    });
  });

  it('omits a blank key so the stored one is tested with the form URL', () => {
    const probe = testProbe(' https://llm.internal/v1 ', ' tiny ', '');
    expect(probe).toEqual({
      ai_base_url: 'https://llm.internal/v1',
      ai_model: 'tiny',
    });
    // Present-but-empty would test an empty key, which is not what a blank
    // field means.
    expect(probe).not.toHaveProperty('ai_key');
  });

  it('keeps a key exactly as typed — trimming could break a valid key', () => {
    expect(testProbe('u', 'm', ' sk-with-space ').ai_key).toBe(' sk-with-space ');
  });
});

describe('escapeAction', () => {
  it('closes when nothing is in flight', () => {
    expect(escapeAction(false, false)).toBe('close');
  });

  it('cancels a test rather than closing over the values being validated', () => {
    expect(escapeAction(false, true)).toBe('cancel-test');
  });

  // A save carries a key the user has typed and nothing else holds. Closing
  // mid-save unmounts the form, so the key is gone and a failure has nowhere
  // to be reported — the user is told nothing and believes it was saved.
  it('ignores Escape while a save is in flight', () => {
    expect(escapeAction(true, false)).toBe('ignore');
  });

  it('prefers cancelling the test when somehow both are in flight', () => {
    expect(escapeAction(true, true)).toBe('cancel-test');
  });
});
