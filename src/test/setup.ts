import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

afterEach(() => {
  if (typeof document !== 'undefined') cleanup();
});

if (typeof window !== 'undefined') {
  if (typeof globalThis.PointerEvent === 'undefined') {
    Object.defineProperty(globalThis, 'PointerEvent', {
      configurable: true,
      value: MouseEvent,
    });
  }

  if (typeof globalThis.ResizeObserver === 'undefined') {
    class TestResizeObserver implements ResizeObserver {
      disconnect() {}
      observe() {}
      unobserve() {}
    }
    Object.defineProperty(globalThis, 'ResizeObserver', {
      configurable: true,
      value: TestResizeObserver,
    });
  }

  if (typeof window.matchMedia !== 'function') {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn((query: string): MediaQueryList => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(() => true),
      })),
    });
  }

  if (typeof globalThis.requestAnimationFrame !== 'function') {
    Object.defineProperty(globalThis, 'requestAnimationFrame', {
      configurable: true,
      value: (callback: FrameRequestCallback) =>
        setTimeout(() => callback(performance.now()), 0),
    });
    Object.defineProperty(globalThis, 'cancelAnimationFrame', {
      configurable: true,
      value: (handle: number) => clearTimeout(handle),
    });
  }

  for (const [name, value] of [
    ['setPointerCapture', () => undefined],
    ['releasePointerCapture', () => undefined],
    ['hasPointerCapture', () => false],
    ['scrollIntoView', () => undefined],
  ] as const) {
    if (typeof HTMLElement.prototype[name] !== 'function') {
      Object.defineProperty(HTMLElement.prototype, name, {
        configurable: true,
        value,
      });
    }
  }

  if (typeof URL.createObjectURL !== 'function') {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob:test'),
    });
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    });
  }
}
