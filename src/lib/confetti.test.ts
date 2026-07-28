import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { burst, frameDue, pending, setFrameCap, startLoop } from './confetti';

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

// Node has no rAF, no window and no canvas — drive the loop by hand so the
// scheduling behaviour can be asserted frame by frame.
let queued: Map<number, (t: number) => void>;
let nextHandle: number;
let clock: number;

function pumpFrame(stepMs = 50): number {
  const due = [...queued.entries()];
  queued.clear();
  clock += stepMs;
  for (const [, cb] of due) cb(clock);
  return due.length;
}

/** Run frames until nothing is scheduled; returns how many ran. */
function drain(limit = 200): number {
  let n = 0;
  while (queued.size > 0 && n < limit) {
    pumpFrame();
    n++;
  }
  return n;
}

const ctxStub = {
  globalAlpha: 1,
  fillStyle: '',
  setTransform: () => {},
  clearRect: () => {},
  save: () => {},
  restore: () => {},
  translate: () => {},
  rotate: () => {},
  fillRect: () => {},
};

function fakeCanvas(): HTMLCanvasElement {
  return {
    width: 0,
    height: 0,
    style: {},
    getContext: () => ctxStub,
  } as unknown as HTMLCanvasElement;
}

describe('animation loop', () => {
  beforeEach(() => {
    queued = new Map();
    nextHandle = 1;
    clock = 1000;
    const g = globalThis as Record<string, unknown>;
    g.requestAnimationFrame = (cb: (t: number) => void) => {
      const h = nextHandle++;
      queued.set(h, cb);
      return h;
    };
    g.cancelAnimationFrame = (h: number) => {
      queued.delete(h);
    };
    g.window = {
      innerWidth: 800,
      innerHeight: 600,
      devicePixelRatio: 2,
      addEventListener: () => {},
      removeEventListener: () => {},
    };
  });

  it('schedules no frame at all while nothing is animating', () => {
    const stop = startLoop(fakeCanvas());
    expect(pending()).toBe(0);
    // An idle board must not hold a requestAnimationFrame callback open.
    expect(queued.size).toBe(0);
    stop();
  });

  it('wakes on a burst and parks again once the particles are gone', () => {
    const stop = startLoop(fakeCanvas());
    burst(100, 100, 5);
    expect(pending()).toBe(5);
    expect(queued.size).toBe(1);
    const frames = drain();
    expect(pending()).toBe(0);
    // The loop ran, then stopped rescheduling itself.
    expect(frames).toBeGreaterThan(1);
    expect(queued.size).toBe(0);
    stop();
  });

  it('a second burst re-arms a parked loop', () => {
    const stop = startLoop(fakeCanvas());
    burst(100, 100, 3);
    drain();
    expect(queued.size).toBe(0);
    burst(200, 200, 3);
    expect(queued.size).toBe(1);
    drain();
    stop();
  });

  it('stops scheduling after the canvas unmounts', () => {
    const stop = startLoop(fakeCanvas());
    burst(100, 100, 3);
    stop();
    expect(queued.size).toBe(0);
    // A burst with no canvas mounted must not resurrect the loop.
    burst(100, 100, 3);
    expect(queued.size).toBe(0);
    // Leave no particles behind for the next test.
    const stop2 = startLoop(fakeCanvas());
    drain();
    stop2();
    expect(pending()).toBe(0);
  });
});
