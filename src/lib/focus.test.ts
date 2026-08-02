import { describe, expect, it } from 'vitest';
import { restoreFocusTarget } from './focus';

/**
 * A dialog captures the element that opened it and gives focus back to it when
 * it closes. The captured *node* is not always the element any more: a keyboard
 * drop commits the move in the same batch that opens the ship warning, and a
 * move across columns unmounts and remounts the card — so by the time the
 * dialog closes, the node it captured is detached and `.focus()` on it puts
 * focus on <body>, at the top of the document.
 *
 * The DOM is injected here (`attached` / `replacement`) so the rule can be
 * tested without one.
 */
describe('restoreFocusTarget', () => {
  const gone = () => false;
  const here = () => true;
  const none = () => null;

  it('gives focus back to the opener when it is still there', () => {
    const trigger = { id: 'settings-button' };
    expect(restoreFocusTarget(trigger, here, none)).toBe(trigger);
  });

  it('gives focus to the node that replaced a re-rendered opener', () => {
    const detached = { id: 'card-a', live: false };
    const remounted = { id: 'card-a', live: true };
    expect(restoreFocusTarget(detached, gone, () => remounted)).toBe(remounted);
  });

  it('does not look for a replacement while the opener is attached', () => {
    // The live node wins even when a replacement could be found: re-finding it
    // would be a no-op at best and could pick a different element.
    const trigger = { id: 'card-a', live: true };
    const other = { id: 'card-a', live: false };
    expect(restoreFocusTarget(trigger, here, () => other)).toBe(trigger);
  });

  it('reports nothing to focus when the opener is gone and unidentified', () => {
    // Not every trigger carries an identity that survives a remount — a
    // button in a list that was rebuilt, for example. Focus then stays where
    // the browser put it rather than jumping somewhere arbitrary.
    expect(restoreFocusTarget({ id: 'x' }, gone, none)).toBeNull();
  });

  it('reports nothing to focus when there was no opener', () => {
    expect(restoreFocusTarget(null, here, none)).toBeNull();
  });
});
