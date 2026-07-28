import { useCallback, useEffect, useRef } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent, RefObject } from 'react';

/**
 * Where Tab moves inside a focus trap. `current` is -1 when focus is not on any
 * of the trapped controls (a stale/removed node lands here): Tab then enters at
 * the first control, Shift+Tab at the last. Anything else wraps around the ring.
 */
export function focusWrapIndex(
  count: number,
  current: number,
  backwards: boolean,
): number {
  if (count <= 0) return -1;
  if (current < 0 || current >= count) return backwards ? count - 1 : 0;
  return (current + (backwards ? -1 : 1) + count) % count;
}

/** Everything a dialog can hand focus to. */
export const DIALOG_FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

/**
 * The dialog's own focusable controls, in document order. Anything under an
 * `inert` subtree is left out: the modals freeze their form that way while a
 * request is in flight, and Tab must not land on a control the browser has
 * already taken out of play.
 */
export function dialogFocusables(root: HTMLElement | null): HTMLElement[] {
  if (!root) return [];
  return Array.from(root.querySelectorAll<HTMLElement>(DIALOG_FOCUSABLE)).filter(
    (el) => el.closest('[inert]') === null && !el.hasAttribute('hidden'),
  );
}

/**
 * Whatever had focus when the dialog first rendered — read during that render
 * rather than in an effect, because by the time effects run the page behind the
 * dialog is already `inert`, which has blurred that element onto <body>. Focus
 * would then be "restored" to nothing.
 */
export function useFocusTrigger(): RefObject<Element | null> {
  const trigger = useRef<Element | null>(null);
  trigger.current ??= typeof document === 'undefined' ? null : document.activeElement;
  return trigger;
}

/**
 * Which node a closing dialog hands focus back to: the one that opened it while
 * it is still in the document, and otherwise whatever has taken its place.
 *
 * The second case is not theoretical. A keyboard drop commits the move in the
 * same batch that opens the ship warning, and a move across columns unmounts
 * and remounts the card — so the node captured when the dialog rendered is
 * already detached by the time the dialog closes, and `.focus()` on a detached
 * node puts focus on <body>. The card carries its task id in `data-task`,
 * which survives the remount even though the node does not.
 *
 * The DOM lives in the two callbacks so the rule itself stays testable.
 */
export function restoreFocusTarget<T>(
  trigger: T | null,
  attached: (el: T) => boolean,
  replacement: (el: T) => T | null,
): T | null {
  if (trigger === null) return null;
  if (attached(trigger)) return trigger;
  return replacement(trigger);
}

/** The live element standing in for `el`, matched on its task id. */
function replacementFor(el: HTMLElement): HTMLElement | null {
  const id = el.dataset.task;
  if (id === undefined || id === '') return null;
  for (const candidate of document.querySelectorAll<HTMLElement>('[data-task]')) {
    if (candidate.dataset.task === id) return candidate;
  }
  return null;
}

/** Hand focus back to whatever opened a dialog, as `restoreFocusTarget` finds it. */
export function restoreFocus(trigger: Element | null): void {
  if (!(trigger instanceof HTMLElement)) return;
  restoreFocusTarget(trigger, (el) => document.contains(el), replacementFor)?.focus();
}

export interface DialogFocusOptions {
  /**
   * Selector for a subtree that keeps its own keyboard model — Tab inside it
   * is that widget's business, not the trap's. The emoji picker is a custom
   * element with a shadow root: its internal focus is invisible from here, so
   * trapping its Tab would break its grid navigation.
   */
  skipWithin?: string;
}

/**
 * Focus handling every dialog owes a keyboard user: focus moves into the dialog
 * when it opens, Tab cycles inside it rather than walking out into the board
 * behind an `aria-modal` surface, and whatever opened it gets focus back when it
 * closes. Returns the keydown handler to put on the dialog box.
 */
export function useDialogFocus(
  boxRef: RefObject<HTMLElement | null>,
  { skipWithin }: DialogFocusOptions = {},
): (e: ReactKeyboardEvent) => void {
  // Whatever opened the dialog gets focus back when it closes; without this a
  // keyboard user is dumped on <body> at the top of the document.
  const triggerRef = useFocusTrigger();

  useEffect(() => {
    const trigger = triggerRef.current;
    const box = boxRef.current;
    // A field with autoFocus has already claimed focus by now (React applies it
    // while committing, before effects run) — don't take it off that field.
    if (box && !box.contains(document.activeElement)) {
      (dialogFocusables(box)[0] ?? box).focus();
    }
    return () => restoreFocus(trigger);
  }, [boxRef, triggerRef]);

  return useCallback(
    (e: ReactKeyboardEvent) => {
      if (e.key !== 'Tab') return;
      if (skipWithin && (e.target as Element | null)?.closest(skipWithin)) return;
      const nodes = dialogFocusables(boxRef.current);
      if (nodes.length === 0) return;
      e.preventDefault();
      const at = focusWrapIndex(
        nodes.length,
        nodes.indexOf(document.activeElement as HTMLElement),
        e.shiftKey,
      );
      nodes[at]?.focus();
    },
    [boxRef, skipWithin],
  );
}
