import { describe, expect, it } from 'vitest';
import { focusWrapIndex } from './ConfirmDialog';

describe('focusWrapIndex', () => {
  it('wraps forwards past the last control', () => {
    expect(focusWrapIndex(3, 0, false)).toBe(1);
    expect(focusWrapIndex(3, 2, false)).toBe(0);
  });

  it('wraps backwards past the first control', () => {
    expect(focusWrapIndex(3, 2, true)).toBe(1);
    expect(focusWrapIndex(3, 0, true)).toBe(2);
  });

  it('pulls focus back in when it is not on a dialog control', () => {
    expect(focusWrapIndex(3, -1, false)).toBe(0);
    expect(focusWrapIndex(3, -1, true)).toBe(2);
    // An index past the ring (a control removed mid-interaction) is treated
    // the same way rather than landing nowhere.
    expect(focusWrapIndex(2, 5, false)).toBe(0);
  });

  it('stays put with a single control', () => {
    expect(focusWrapIndex(1, 0, false)).toBe(0);
    expect(focusWrapIndex(1, 0, true)).toBe(0);
  });

  it('reports no target when there is nothing to focus', () => {
    expect(focusWrapIndex(0, -1, false)).toBe(-1);
  });
});

/** Every .ts/.tsx source under src/, read as text at transform time. */
const SOURCES = import.meta.glob('../**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

// React dispatches a key event along the *native* target's ancestor path, so
// the dialog's onKeyDown — Escape, Enter and the whole Tab trap — only runs
// while focus is inside the dialog box. Pressing on the title or the body
// text is a press on a non-focusable node: without a tabIndex on the box the
// browser moves focus to <body>, outside the subtree, and every one of those
// keys stops working while the modal is still up. There is no DOM in this
// test environment (vite.config.ts sets environment: 'node'), so the guard is
// on the source: it is the attribute that must not be dropped.
describe('confirm dialog focus trap', () => {
  it('keeps the dialog box itself focusable', () => {
    const [, src] =
      Object.entries(SOURCES).find(([p]) => p.endsWith('/ConfirmDialog.tsx')) ??
      [];
    expect(src).toBeTypeOf('string');
    expect(src).toMatch(/tabIndex=\{-1\}/);
  });
});

describe('no browser dialogs', () => {
  // The browser's own dialogs freeze the page, cannot be styled, and on
  // mobile name the origin rather than kb. ConfirmDialog replaces them, and
  // this fails the build if one creeps back in.
  it('never calls the browser confirm/alert/prompt dialogs', () => {
    const offenders = Object.entries(SOURCES)
      .filter(([, src]) => /\bwindow\s*\.\s*(confirm|alert|prompt)\s*\(/.test(src))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });

  it('reads every source (a guard that matches nothing guards nothing)', () => {
    expect(Object.keys(SOURCES).length).toBeGreaterThan(10);
    expect(SOURCES['../App.tsx']).toContain('ConfirmDialog');
  });
});
