import { useEffect, useId, useRef } from 'react';
import type { Task } from '../lib/model';
import { useDialogFocus } from '../lib/focus';

/** Why a move to Done deserves a confirmation. */
export interface ShipWarning {
  /** Unticked checklist items. */
  open: number;
  /** Checklist size, for "3 of 5". */
  total: number;
  /** The card carries the blocked flag. */
  blocked: boolean;
}

/**
 * The reasons shipping `task` should be confirmed, or null when there are
 * none. Both reasons fold into one warning so a card that is blocked *and*
 * half-ticked asks once rather than twice.
 */
export function shipWarning(task: Task): ShipWarning | null {
  const total = task.checks.length;
  const open = task.checks.filter((c) => !c.done).length;
  if (open === 0 && !task.blocked) return null;
  return { open, total, blocked: task.blocked };
}

/** One sentence per reason, in the order they are shown. */
export function shipWarningLines(w: ShipWarning): string[] {
  const lines: string[] = [];
  if (w.open > 0) {
    lines.push(
      `${w.open} of ${w.total} checklist ${w.total === 1 ? 'item is' : 'items are'} still open.`,
    );
  }
  if (w.blocked) lines.push('This card is flagged blocked.');
  return lines;
}

export interface ShipDialogProps {
  warning: ShipWarning;
  /** Card title, so the dialog names what is about to ship. */
  title: string;
  onShip: () => void;
  /** Tick every checklist item and ship; hidden when nothing is open. */
  onTickAll: () => void;
  onCancel: () => void;
}

/** Confirm-style dialog shown instead of silently shipping a card (F9/F10). */
export function ShipDialog({
  warning,
  title,
  onShip,
  onTickAll,
  onCancel,
}: Readonly<ShipDialogProps>) {
  const boxRef = useRef<HTMLDivElement>(null);
  const onDialogKeyDown = useDialogFocus(boxRef);
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onCancel]);

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <div
        className="modal ship"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={bodyId}
        tabIndex={-1}
        ref={boxRef}
        onKeyDown={onDialogKeyDown}
      >
        <h2 id={titleId}>Move “{title}” to Done?</h2>
        <div id={bodyId}>
          {shipWarningLines(warning).map((line) => (
            <p key={line} className="mnote">
              {line}
            </p>
          ))}
        </div>
        {/* Cancel on the left, the primary action on the right. */}
        <div className="actions">
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
          {warning.open > 0 && (
            <button type="button" onClick={onTickAll}>
              Tick everything
            </button>
          )}
          <button type="button" className="save" onClick={onShip}>
            Ship anyway
          </button>
        </div>
      </div>
    </div>
  );
}
