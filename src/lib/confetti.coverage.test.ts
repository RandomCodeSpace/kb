import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { burst, pending, setFrameCap, startLoop } from './confetti';

let queued: Map<number, (time: number) => void>;
let handle: number;
let now: number;

const context = {
  globalAlpha: 1,
  fillStyle: '',
  setTransform: vi.fn(), clearRect: vi.fn(), save: vi.fn(), restore: vi.fn(),
  translate: vi.fn(), rotate: vi.fn(), fillRect: vi.fn(),
};

function canvas(): HTMLCanvasElement {
  return { width: 0, height: 0, style: {}, getContext: () => context } as unknown as HTMLCanvasElement;
}

function pump(step: number) {
  const callbacks = [...queued.values()];
  queued.clear();
  now += step;
  callbacks.forEach((callback) => callback(now));
}

function drain() {
  let guard = 0;
  while (queued.size > 0 && guard++ < 200) pump(50);
}

beforeEach(() => {
  queued = new Map();
  handle = 0;
  now = 1000;
  vi.spyOn(performance, 'now').mockImplementation(() => now);
  vi.stubGlobal('requestAnimationFrame', (callback: (time: number) => void) => {
    handle += 1;
    queued.set(handle, callback);
    return handle;
  });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => queued.delete(id));
  vi.stubGlobal('window', {
    innerWidth: 640, innerHeight: 480, devicePixelRatio: 3,
    addEventListener: vi.fn(), removeEventListener: vi.fn(),
  });
  vi.stubGlobal('matchMedia', () => ({ matches: false }));
});

afterEach(() => {
  setFrameCap(null);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('confetti scheduling residual branches', () => {
  it('caps the backing canvas density at two', () => {
    const target = canvas();
    const stop = startLoop(target);
    expect(target.width).toBe(1280);
    expect(target.height).toBe(960);
    stop();
  });

  it('falls back to one for an unusable device pixel ratio', () => {
    (window as unknown as { devicePixelRatio: number }).devicePixelRatio = 0;
    const target = canvas();
    const stop = startLoop(target);
    expect(target.width).toBe(640);
    stop();
  });

  it('does not enqueue particles when reduced motion is requested', () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true }));
    const before = pending();
    burst(10, 10, 5);
    expect(pending()).toBe(before);
  });

  it('does not queue a second frame when another burst arrives while awake', () => {
    const stop = startLoop(canvas());
    burst(10, 10, 1);
    burst(20, 20, 1);
    expect(queued.size).toBe(1);
    drain();
    expect(pending()).toBe(0);
    stop();
  });

  it('reschedules a frame skipped by the configured cap', () => {
    setFrameCap(1);
    const stop = startLoop(canvas());
    burst(10, 10, 1);
    pump(10);
    expect(queued.size).toBe(1);
    setFrameCap(null);
    drain();
    expect(pending()).toBe(0);
    stop();
  });

  it('does not clear a newer loop wake callback when an older canvas unmounts', () => {
    const stopFirst = startLoop(canvas());
    const stopSecond = startLoop(canvas());
    stopFirst();
    burst(10, 10, 1);
    expect(queued.size).toBe(1);
    drain();
    stopSecond();
  });
});
