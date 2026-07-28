import { useCallback, useEffect, useId, useRef } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent } from 'react';

/**
 * Focus-trap ring members. The dialog renders nothing but buttons, so this is
 * the whole set — keep it in step if that ever stops being true.
 */
const FOCUSABLE = 'button:not([disabled])';

/**
 * Where Tab moves inside the trap. `current` is -1 when focus is not on any of
 * the dialog's own controls (it never should be, but a stale/removed node
 * would land here): Tab then enters at the first control, Shift+Tab at the
 * last. Anything else wraps around the ring.
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

export interface ConfirmDialogProps {
  /** The question, in one line — it labels the dialog for screen readers. */
  title: string;
  /** Consequence of confirming; describes the dialog for screen readers. */
  body?: string;
  /** Label of the confirming action. */
  confirmLabel?: string;
  cancelLabel?: string;
  /**
   * The confirming action is irreversible: it renders in the destructive
   * colour so it cannot be mistaken for the safe way out.
   */
  destructive?: boolean;
  /**
   * What confirming does. Omit for an acknowledgement (what `window.alert`
   * used to be): the dialog then shows only the confirming button, and
   * pressing it just closes.
   */
  onConfirm?: () => void;
  onClose: () => void;
}

/**
 * The app's replacement for `window.confirm`/`window.alert`. A browser dialog
 * freezes the page, cannot be styled, and on mobile names the origin rather
 * than the app — so every confirmation goes through here instead.
 *
 * Focus is trapped while it is open, Escape cancels, Enter takes the default
 * (confirming) action, and focus returns to whatever opened it.
 */
export function ConfirmDialog({
  title,
  body,
  confirmLabel = 'OK',
  cancelLabel = 'Cancel',
  destructive = false,
  onConfirm,
  onClose,
}: ConfirmDialogProps) {
  const boxRef = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    // Whatever opened the dialog gets focus back when it closes; without this
    // a keyboard user is dumped at the top of the document.
    const trigger = document.activeElement;
    confirmRef.current?.focus();
    return () => {
      if (trigger instanceof HTMLElement && document.contains(trigger)) {
        trigger.focus();
      }
    };
  }, []);

  const accept = useCallback(() => {
    onConfirm?.();
    onClose();
  }, [onConfirm, onClose]);

  const onKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape') {
      // Stop the native event here: modals below listen on window, and one
      // Escape must not close the dialog and its opener at once.
      e.stopPropagation();
      onClose();
      return;
    }
    if (e.key === 'Enter') {
      // A focused button already handles Enter itself; this only covers focus
      // sitting somewhere else in the dialog.
      if (e.target instanceof HTMLButtonElement) return;
      e.preventDefault();
      e.stopPropagation();
      accept();
      return;
    }
    if (e.key !== 'Tab') return;
    const nodes = Array.from(
      boxRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [],
    );
    if (nodes.length === 0) return;
    e.preventDefault();
    const at = focusWrapIndex(
      nodes.length,
      nodes.indexOf(document.activeElement as HTMLElement),
      e.shiftKey,
    );
    nodes[at]?.focus();
  };

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        // Clicking away cancels — it can never be the confirming action.
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={boxRef}
        className="modal confirm"
        role="alertdialog"
        aria-modal="true"
        // Escape, Enter and the Tab trap are handled here, and React only
        // routes a key event through this node while focus is inside it.
        // Pressing on the title or the body text would otherwise blur the
        // buttons onto <body> — outside the subtree — and silently disable
        // all three. tabIndex makes the box itself the focus target for a
        // click on its own non-focusable text, so the handler stays live.
        tabIndex={-1}
        aria-labelledby={titleId}
        aria-describedby={body ? bodyId : undefined}
        onKeyDown={onKeyDown}
      >
        <h2 id={titleId}>{title}</h2>
        {body && (
          <p id={bodyId} className="mnote">
            {body}
          </p>
        )}
        <div className="actions">
          {onConfirm && (
            <button type="button" onClick={onClose}>
              {cancelLabel}
            </button>
          )}
          <button
            ref={confirmRef}
            type="button"
            className={destructive ? 'save danger' : 'save'}
            onClick={accept}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
