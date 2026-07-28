import { afterEach, describe, expect, it } from 'vitest';
import { frameDue, setFrameCap } from './confetti';

afterEach(() => {
  setFrameCap(null);
});

describe('frame cap', () => {
  it('renders every frame when uncapped', () => {
    setFrameCap(null);
    expect(frameDue(1000, 999)).toBe(true);
    expect(frameDue(1000, 1000)).toBe(true);
  });

  it('skips frames that arrive earlier than the target interval', () => {
    setFrameCap(60);
    // 120Hz display, 60fps cap: every other frame is skipped.
    expect(frameDue(1008.3, 1000)).toBe(false);
    expect(frameDue(1016.6, 1000)).toBe(true);
  });

  it('keeps a 60Hz display at 60 when capped at 60', () => {
    setFrameCap(60);
    // Real frames jitter a little under the nominal 16.67ms interval.
    expect(frameDue(1016.2, 1000)).toBe(true);
  });

  it('treats a non-positive target as uncapped', () => {
    setFrameCap(0);
    expect(frameDue(1000.1, 1000)).toBe(true);
  });
});
