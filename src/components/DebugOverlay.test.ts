import { beforeEach, describe, expect, it } from 'vitest';
import { debugEnabled, setDebugEnabled } from './DebugOverlay';

// Node test environment has no Web Storage — stub it on globalThis.
const mem = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (k: string) => mem.get(k) ?? null,
  setItem: (k: string, v: string) => {
    mem.set(k, String(v));
  },
  removeItem: (k: string) => {
    mem.delete(k);
  },
  clear: () => {
    mem.clear();
  },
  key: () => null,
  get length() {
    return mem.size;
  },
};

beforeEach(() => {
  mem.clear();
});

describe('debugEnabled', () => {
  it('is off with no flag and no parameter', () => {
    expect(debugEnabled('')).toBe(false);
  });

  it('turns on with ?debug=1 and persists across a reload', () => {
    expect(debugEnabled('?debug=1')).toBe(true);
    expect(mem.get('kb.debug.v1')).toBe('1');
    // Next load has no parameter — the stored flag keeps it on.
    expect(debugEnabled('')).toBe(true);
  });

  it('turns off with ?debug=0 and clears the stored flag', () => {
    debugEnabled('?debug=1');
    expect(debugEnabled('?debug=0')).toBe(false);
    expect(mem.has('kb.debug.v1')).toBe(false);
    expect(debugEnabled('')).toBe(false);
  });

  it('reads other parameters without turning on', () => {
    expect(debugEnabled('?other=1')).toBe(false);
  });
});

describe('setDebugEnabled', () => {
  it('dismissal from the overlay survives the next load', () => {
    debugEnabled('?debug=1');
    setDebugEnabled(false);
    expect(debugEnabled('')).toBe(false);
  });

  it('the settings toggle turns it on, and it stays on across a reload', () => {
    setDebugEnabled(true);
    expect(mem.get('kb.debug.v1')).toBe('1');
    expect(debugEnabled('')).toBe(true);
  });

  it('?debug=1 still overrides a toggle left off', () => {
    setDebugEnabled(false);
    expect(debugEnabled('?debug=1')).toBe(true);
  });
});
