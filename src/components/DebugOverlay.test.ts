import { beforeEach, describe, expect, it } from 'vitest';
import {
  debugEnabled,
  overlaps,
  restingBox,
  setDebugEnabled,
} from './DebugOverlay';

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

// 2.4.11: the panel is opaque, so anything it covers is gone rather than
// dimmed. It steps aside when the focused element is underneath it.
describe('stepping aside from the focused element', () => {
  const viewport = { width: 1280, height: 577 };
  const rest = restingBox(232, 221, viewport);

  it('sits in the bottom-right corner at rest', () => {
    expect(rest).toEqual({ left: 1036, right: 1268, top: 344, bottom: 565 });
  });

  // The measured case from the audit: "Delete permanently" on a cancelled
  // card, 119x22 at (1083, 464) — entirely inside the panel.
  it('finds a focused button underneath it', () => {
    expect(
      overlaps(rest, { left: 1083, right: 1202, top: 464, bottom: 486 }),
    ).toBe(true);
  });

  it('leaves anything outside the corner alone', () => {
    expect(overlaps(rest, { left: 20, right: 200, top: 40, bottom: 66 })).toBe(
      false,
    );
    // Touching edges is not covering.
    expect(
      overlaps(rest, { left: 900, right: 1036, top: 344, bottom: 400 }),
    ).toBe(false);
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
