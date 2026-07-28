import { describe, expect, it } from 'vitest';

/**
 * Structural accessibility guards that live in the markup and the stylesheet
 * rather than in a function — the kind of thing a later edit undoes silently
 * and only a browser audit catches. Read through the bundler so the guard
 * needs no Node types and no new dependency, the same trick viewport.test.ts
 * and egress.test.ts use.
 */
const APP = Object.values(
  import.meta.glob('./App.tsx', {
    query: '?raw',
    import: 'default',
    eager: true,
  }) as Record<string, string>,
)[0];

const CSS = Object.values(
  import.meta.glob('./styles.css', {
    query: '?raw',
    import: 'default',
    eager: true,
  }) as Record<string, string>,
)[0];

/**
 * The source of the element that starts at `open`, up to its matching close
 * tag. Only `<div>`s are counted, which is all the app shell is built from.
 */
function divSubtree(src: string, open: string): string {
  const start = src.indexOf(open);
  if (start === -1) return '';
  let depth = 0;
  const re = /<div\b|<\/div>/g;
  re.lastIndex = start;
  for (let m = re.exec(src); m !== null; m = re.exec(src)) {
    depth += m[0] === '</div>' ? -1 : 1;
    if (depth === 0) return src.slice(start, re.lastIndex);
  }
  return src.slice(start);
}

describe('app shell structure (WCAG 4.1.3 Status Messages)', () => {
  const shell = divSubtree(APP, '<div className="app-shell"');

  it('reads the shell as a subtree at all', () => {
    // A rename would otherwise make every assertion below vacuously pass.
    expect(shell).toContain('inert={dialogOpen}');
    expect(shell).toContain('<BoardView');
  });

  /**
   * `inert` removes a subtree from the accessibility tree. The live region is
   * the only thing that speaks a save failure, a sync expiry or a board move,
   * and a dialog being open is exactly when that has to be heard — so it must
   * not live inside the part of the app a dialog switches off.
   */
  it('keeps the live region outside the part that goes inert', () => {
    expect(APP).toContain('aria-live="polite"');
    expect(shell).not.toContain('aria-live');
  });
});

describe('stylesheet guards', () => {
  /** The declarations of the rule whose selector is exactly `selector`. */
  function rule(selector: string): string {
    const at = CSS.indexOf(`\n${selector} {`);
    return at === -1 ? '' : CSS.slice(at, CSS.indexOf('}', at));
  }

  /**
   * The footer hint is text, so its height grows with the text size. Fixed to
   * the viewport bottom it printed itself over the last card's chips and
   * chevron at 200% text (SC 1.4.4) and under the 1.4.12 spacing overrides on
   * a 320px screen, with no scrolling that could recover them — the board's
   * clearance below it was a fixed 32px that does not scale.
   */
  it('keeps the footer hint in flow rather than fixed over the board', () => {
    const foot = rule('.foot');
    expect(foot).not.toBe('');
    expect(foot).not.toMatch(/position:\s*fixed/);
  });

  /**
   * Two fields drop their outline on `:focus` to draw a hard shadow instead.
   * The shared `:focus-visible` restore sits earlier in the file at the same
   * specificity, so it loses — each of these has to restore the ring beside
   * its own rule (SC 2.4.7).
   */
  it.each(['.gate-card input', '.debug select'])(
    'restores the keyboard focus ring after %s:focus removes it',
    (sel) => {
      const off = CSS.indexOf(`${sel}:focus {`);
      const on = CSS.indexOf(`${sel}:focus-visible {`);
      expect(off).toBeGreaterThan(-1);
      expect(on).toBeGreaterThan(off);
      expect(CSS.slice(on, CSS.indexOf('}', on))).toMatch(/outline:\s*3px solid/);
    },
  );

  /**
   * The card form is taller than a 720px laptop viewport, and the modal is its
   * own scroll box with no affordance saying so. Pinned to the bottom of that
   * box, Delete/Cancel/Save are always whole rather than a 6px sliver at the
   * edge.
   */
  it('pins the modal action row to the bottom of the modal', () => {
    const actions = rule('.modal .actions');
    expect(actions).toMatch(/position:\s*sticky/);
    expect(actions).toMatch(/bottom:/);
  });

  /**
   * An absolutely positioned ::after resolves `inset` against its containing
   * block's *padding* box, so on a bordered button a negative inset silently
   * lost 2px per side — a 36x36 target where 40x40 was intended. Sizing the
   * hit area outright is what makes the number in the README true.
   */
  it('sizes the small targets hit areas outright', () => {
    expect(CSS).toMatch(/--hit:\s*40px/);
    for (const sel of ['.addbtn::after', '.chev::after', '.card .grip::after']) {
      const at = CSS.indexOf(`${sel} {`);
      expect(at).toBeGreaterThan(-1);
      expect(CSS.slice(at, CSS.indexOf('}', at))).toContain('var(--hit)');
    }
  });
});
