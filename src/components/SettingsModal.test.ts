import { describe, expect, it } from 'vitest';
import { escapeAction, testProbe } from './SettingsModal';

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
