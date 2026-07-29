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
    const pinned = rule('.modal .actions:last-child');
    expect(pinned).toMatch(/position:\s*sticky/);
    expect(pinned).toMatch(/bottom:/);
  });

  /**
   * …but only when it is last. The pinned row is opaque, so anything after it
   * in flow scrolls underneath and cannot be read — which is what happened to
   * the note under Settings' buttons. Any modal that puts content after its
   * action row gets an ordinary in-flow row instead.
   */
  it('does not pin an action row that has content after it', () => {
    expect(rule('.modal .actions')).not.toMatch(/position:\s*sticky/);
  });

  /**
   * The entrance keyframes may only touch `opacity` and `transform`: both run
   * on the compositor, so the 30fps frame cap (or a busy main thread) cannot
   * make them stutter. A property like height, top or box-shadow here would
   * animate on the main thread and janks exactly when it matters.
   */
  it.each(['kb-fade', 'kb-pop', 'kb-drop', 'kb-rise'])(
    'keyframes %s animate compositor properties only',
    (name) => {
      const at = CSS.indexOf(`@keyframes ${name}`);
      expect(at).toBeGreaterThan(-1);
      const body = CSS.slice(at, CSS.indexOf('}\n}', at));
      const props = [...body.matchAll(/([a-z-]+)\s*:/g)].map((m) => m[1]);
      for (const p of props) {
        expect(['opacity', 'transform', 'from', 'to']).toContain(p);
      }
    },
  );

  /**
   * Every entrance animation must be switched off under reduced motion —
   * surfaces and messages then appear in place. A new `animation:` added to
   * the sheet without joining the reduce block is exactly the silent
   * regression this guard exists for.
   */
  it('turns every entrance animation off under prefers-reduced-motion', () => {
    const at = CSS.indexOf('@media (prefers-reduced-motion: reduce)');
    expect(at).toBeGreaterThan(-1);
    const block = CSS.slice(at);
    for (const sel of [
      '.modal-backdrop',
      '.modal',
      '.emojipop',
      '.labeldrop',
      '.datepop',
      '.flash',
      '.notice',
    ]) {
      expect(block).toContain(sel);
    }
    expect(block).toMatch(/animation:\s*none/);
  });

  /**
   * The reduce block must be the sheet's last word. A media query adds no
   * specificity, so any later rule at equal specificity re-enables the
   * animation the block switches off — the .flash entrance shipped exactly
   * that way once, animating under reduced motion while the block claimed
   * otherwise.
   */
  it('keeps prefers-reduced-motion after every animation/transition rule', () => {
    const at = CSS.indexOf('@media (prefers-reduced-motion: reduce)');
    const after = CSS.slice(at);
    // Inside the block every animation/transition is a switch-off; anything
    // else after it would be a re-enable candidate.
    const offenders = [...after.matchAll(/(animation|transition):\s*(?!none)[a-z]/g)];
    expect(offenders).toEqual([]);
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
