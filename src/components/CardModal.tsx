import { useEffect, useId, useRef, useState } from 'react';
import type { Identity } from '../lib/auth';
import type { Status, Task } from '../lib/model';
import { STATUS_LABEL } from '../lib/model';
import type { AIStoryRequest, SimilarItem, StoryDraft } from '../lib/api';
import { getSimilar } from '../lib/api';
import { useDialogFocus } from '../lib/focus';
import { shouldQuery } from '../lib/similar';
import { CardEditor } from './CardEditor';

export type ModalState =
  | { mode: 'add'; status: Status }
  | { mode: 'edit'; task: Task };

export interface CardModalProps {
  state: ModalState;
  identity: Identity;
  /** Suggestions for the labels combobox (server labels ∪ board tags). */
  labels: string[];
  /**
   * When set, shows the "Draft with AI" section; undefined hides it. The
   * signal is how Cancel aborts the request in flight rather than only
   * hiding the busy state.
   */
  aiDraft?: (req: AIStoryRequest, signal?: AbortSignal) => Promise<StoryDraft>;
  onSave: (task: Task) => void;
  onDelete: (taskId: string) => void;
  onClose: () => void;
}

const KILLED_CHIP_LIMIT = 120;
const KILLED_PREFIX = 'rejected ';
const KILLED_DATE_FORMAT = new Intl.DateTimeFormat('en-US', {
  month: 'long',
  year: 'numeric',
  timeZone: 'UTC',
});

/**
 * Compact graveyard context for the advisory chip. Server validation already
 * rejects line breaks, but collapsing all whitespace keeps malformed replies
 * from turning one advisory row into an essay.
 */
export function killedChipText(item: SimilarItem, now: Date): string {
  const killedAt = item.killedAt ? new Date(item.killedAt) : now;
  const date = Number.isNaN(killedAt.getTime()) ? now : killedAt;
  const reason = item.reason?.replace(/\s+/g, ' ').trim();
  const full = `${KILLED_PREFIX}${KILLED_DATE_FORMAT.format(date)}${
    reason ? ` \u2014 ${reason}` : ''
  }`;
  const characters = Array.from(full);
  if (characters.length <= KILLED_CHIP_LIMIT) return full;
  return `${characters
    .slice(0, KILLED_CHIP_LIMIT - 1)
    .join('')
    .trimEnd()}\u2026`;
}

export function CardModal({
  state,
  identity,
  labels,
  aiDraft,
  onSave,
  onDelete,
  onClose,
}: CardModalProps) {
  const excludeId = state.mode === 'edit' ? state.task.id : undefined;
  const [title, setTitle] = useState(state.mode === 'edit' ? state.task.title : '');
  const [aiBusy, setAiBusy] = useState(false);
  const [items, setItems] = useState<SimilarItem[]>([]);
  const [dismissed, setDismissed] = useState(false);
  const [lastQ, setLastQ] = useState('');
  const draftCancel = useRef<(() => void) | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  // The emoji picker is a custom element with its own keyboard grid; the trap
  // stays out of it, or Tab inside the picker would jump back to the form.
  const onDialogKeyDown = useDialogFocus(boxRef, { skipWithin: '.emojipop' });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      // Mid-draft, Escape cancels the request instead of closing the card:
      // closing would throw away the form the user is waiting to have filled.
      if (aiBusy) draftCancel.current?.();
      else onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [aiBusy, onClose]);

  // A card closed mid-draft (a server refresh can do it) must not leave the
  // request running with nowhere to land.
  useEffect(() => () => draftCancel.current?.(), []);

  useEffect(() => {
    if (!shouldQuery(title, lastQ)) {
      if (Array.from(title.trim()).length < 3) {
        setItems([]);
        setLastQ('');
      }
      return;
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => {
      void getSimilar(
        identity,
        title,
        excludeId,
        ctrl.signal,
      ).then((next) => {
        if (ctrl.signal.aborted) return;
        setItems(next);
        setLastQ(title.trim());
        setDismissed(false);
      });
    }, 400);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [excludeId, identity, lastQ, title]);

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        // A stray click outside must not discard a draft the user is waiting
        // for; Cancel (or Escape) is the way out while a request is running.
        if (e.target === e.currentTarget && !aiBusy) onClose();
      }}
    >
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-busy={aiBusy}
        // Focus lands here for a press on the dialog's own text, so the Tab
        // trap on this node keeps working (see ConfirmDialog).
        tabIndex={-1}
        ref={boxRef}
        onKeyDown={onDialogKeyDown}
      >
        <h2 id={titleId}>
          {state.mode === 'add'
            ? `New task · ${STATUS_LABEL[state.status]}`
            : 'Edit task'}
        </h2>
        <CardEditor
          state={state}
          labels={labels}
          aiDraft={aiDraft}
          title={title}
          onTitleChange={setTitle}
          onBusyChange={setAiBusy}
          cancelRef={draftCancel}
          titleExtras={!dismissed && items.length > 0 && (
            <div className="similar">
              <span className="similar-head" role="status">
                {items.length} similar {items.length === 1 ? 'item' : 'items'} — is this a duplicate?
              </span>
              <div className="similar-list">
                {items.map((item, index) => {
                  const killedText =
                    item.via === 'killed'
                      ? killedChipText(item, new Date())
                      : null;
                  return (
                    <div
                      className={`similar-row${killedText ? ' killed' : ''}`}
                      key={`${item.title}:${index}`}
                    >
                      <span
                        className={`similar-via${killedText ? ' killed' : ''}`}
                      >
                        {killedText
                          ? 'rejected'
                          : item.via === 'card'
                            ? 'on board'
                            : 'imported'}
                      </span>
                      <span
                        className="similar-title"
                        title={
                          killedText
                            ? `${killedText} \u00b7 ${item.title}`
                            : undefined
                        }
                      >
                        {killedText
                          ? `${killedText.slice(KILLED_PREFIX.length)} \u00b7 ${item.title}`
                          : item.title}
                      </span>
                    </div>
                  );
                })}
              </div>
              <button type="button" aria-label="Dismiss similar items" onClick={() => setDismissed(true)}>✕</button>
            </div>
          )}
          onSave={onSave}
          onDelete={onDelete}
          onClose={onClose}
        />
      </div>
    </div>
  );
}
