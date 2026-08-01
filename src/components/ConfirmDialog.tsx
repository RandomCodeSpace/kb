import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent } from 'react';
import { focusWrapIndex, restoreFocus, useFocusTrigger } from '../lib/focus';

/**
 * Focus-trap ring members. Reason capture adds an input to the existing button
 * ring; disabled controls stay out because Tab cannot land on them.
 */
const FOCUSABLE = 'input:not([disabled]), button:not([disabled])';

// Every dialog traps focus the same way; the wrap arithmetic lives with the
// shared helper and is re-exported here for the tests that named it.
export { focusWrapIndex };

export interface ConfirmDialogProps {
  /** The question, in one line — it labels the dialog for screen readers. */
  title: string;
  /** Consequence of confirming; describes the dialog for screen readers. */
  body?: string;
  /** Label of the confirming action. */
  confirmLabel?: string;
  cancelLabel?: string;
  /** Optional one-line value collected before the confirming action. */
  inputLabel?: string;
  inputPlaceholder?: string;
  inputMaxLength?: number;
  /** Disables the primary action while the optional input is blank. */
  inputRequired?: boolean;
  /** An explicit alternative to the primary action, before it in tab order. */
  secondaryLabel?: string;
  onSecondary?: () => void;
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
  onConfirm?: (inputValue: string) => void;
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
  inputLabel,
  inputPlaceholder,
  inputMaxLength,
  inputRequired = false,
  secondaryLabel,
  onSecondary,
  destructive = false,
  onConfirm,
  onClose,
}: Readonly<ConfirmDialogProps>) {
  const boxRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const [inputValue, setInputValue] = useState('');
  const titleId = useId();
  const bodyId = useId();
  const inputId = useId();

  // Whatever opened the dialog gets focus back when it closes; without this a
  // keyboard user is dumped at the top of the document. Read while rendering,
  // before the page behind goes inert and blurs it onto <body>.
  const triggerRef = useFocusTrigger();

  useEffect(() => {
    const trigger = triggerRef.current;
    if (inputLabel) inputRef.current?.focus();
    else confirmRef.current?.focus();
    return () => restoreFocus(trigger);
  }, [inputLabel, triggerRef]);

  const confirmDisabled = inputRequired && inputValue.trim() === '';
  const accept = useCallback(() => {
    if (confirmDisabled) return;
    onConfirm?.(inputValue);
    onClose();
  }, [confirmDisabled, inputValue, onConfirm, onClose]);

  const acceptSecondary = useCallback(() => {
    onSecondary?.();
    onClose();
  }, [onClose, onSecondary]);

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
        {inputLabel && (
          <>
            <label htmlFor={inputId}>{inputLabel}</label>
            <input
              ref={inputRef}
              id={inputId}
              type="text"
              value={inputValue}
              placeholder={inputPlaceholder}
              maxLength={inputMaxLength}
              onChange={(e) => setInputValue(e.target.value)}
            />
          </>
        )}
        <div className="actions">
          {(onConfirm || onSecondary) && (
            <button type="button" onClick={onClose}>
              {cancelLabel}
            </button>
          )}
          {secondaryLabel && onSecondary && (
            <button type="button" onClick={acceptSecondary}>
              {secondaryLabel}
            </button>
          )}
          <button
            ref={confirmRef}
            type="button"
            className={destructive ? 'save danger' : 'save'}
            disabled={confirmDisabled}
            onClick={accept}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
