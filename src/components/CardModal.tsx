import { useEffect, useId, useRef, useState } from 'react';
import type { Check, Effort, Prio, Status, Task } from '../lib/model';
import { newTask, STATUS_LABEL } from '../lib/model';
import type { AIStoryRequest, StoryDraft } from '../lib/api';
import { isAbortError } from '../lib/api';
import { useDialogFocus } from '../lib/focus';
import { EmojiField, firstEmoji } from './EmojiField';
import { LabelsCombobox } from './LabelsCombobox';

export type ModalState =
  | { mode: 'add'; status: Status }
  | { mode: 'edit'; task: Task };

export interface CardModalProps {
  state: ModalState;
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

function checksToText(checks: Check[]): string {
  return checks.map((c) => (c.done ? `x ${c.text}` : c.text)).join('\n');
}

function textToChecks(src: string): Check[] {
  return src
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((l) =>
      l.startsWith('x ')
        ? { text: l.slice(2).trim(), done: true }
        : { text: l, done: false },
    );
}

export function CardModal({
  state,
  labels,
  aiDraft,
  onSave,
  onDelete,
  onClose,
}: CardModalProps) {
  const base = state.mode === 'edit' ? state.task : null;
  const [emoji, setEmoji] = useState(() => firstEmoji(base?.emoji ?? ''));
  const [title, setTitle] = useState(base?.title ?? '');
  const [blocked, setBlocked] = useState(base?.blocked ?? false);
  const [desc, setDesc] = useState(base?.desc ?? '');
  const [prio, setPrio] = useState<Prio>(base?.prio ?? 3);
  const [due, setDue] = useState(base?.due ?? '');
  const [effort, setEffort] = useState<Effort | ''>(base?.effort ?? '');
  const [tags, setTags] = useState<string[]>(base ? base.tags : []);
  const [checks, setChecks] = useState(base ? checksToText(base.checks) : '');
  const [aiPrompt, setAiPrompt] = useState('');
  const [aiBusy, setAiBusy] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);
  const draftAbort = useRef<AbortController | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const aiErrorId = useId();
  // The emoji picker is a custom element with its own keyboard grid; the trap
  // stays out of it, or Tab inside the picker would jump back to the form.
  const onDialogKeyDown = useDialogFocus(boxRef, { skipWithin: '.emojipop' });

  const cancelDraft = () => draftAbort.current?.abort();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      // Mid-draft, Escape cancels the request instead of closing the card:
      // closing would throw away the form the user is waiting to have filled.
      if (draftAbort.current) draftAbort.current.abort();
      else onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // A card closed mid-draft (a server refresh can do it) must not leave the
  // request running with nowhere to land.
  useEffect(() => () => draftAbort.current?.abort(), []);

  const save = () => {
    if (!title.trim()) return;
    const fields = {
      emoji: firstEmoji(emoji),
      title: title.trim(),
      desc: desc.trim(),
      blocked,
      prio,
      due: due || undefined,
      effort: effort === '' ? undefined : effort,
      tags,
      checks: textToChecks(checks),
    };
    if (state.mode === 'add') {
      onSave(newTask({ ...fields, status: state.status }));
    } else {
      onSave({ ...state.task, ...fields });
    }
  };

  const runDraft = async () => {
    if (!aiDraft || aiBusy || !aiPrompt.trim()) return;
    setAiBusy(true);
    setAiError(null);
    const req: AIStoryRequest = {
      mode: state.mode === 'add' ? 'create' : 'update',
      prompt: aiPrompt.trim(),
    };
    if (state.mode === 'edit') {
      req.task = {
        title: title.trim(),
        desc: desc.trim(),
        prio,
        due,
        effort,
        tags,
        checks: textToChecks(checks),
      };
    }
    const ctrl = new AbortController();
    draftAbort.current = ctrl;
    try {
      const d = await aiDraft(req, ctrl.signal);
      // Prefill the form only — the user still reviews and presses Save.
      if (d.title !== '') setTitle(d.title);
      setDesc(d.desc);
      setPrio(d.prio);
      setDue(d.due);
      setEffort(d.effort);
      setTags(d.tags);
      setChecks(checksToText(d.checks));
    } catch (err) {
      // A cancel is not a failure, and nothing was written: the form is
      // exactly as it was, so say nothing.
      if (!isAbortError(err)) {
        setAiError(err instanceof Error ? err.message : 'draft failed');
      }
    } finally {
      draftAbort.current = null;
      setAiBusy(false);
    }
  };

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
        {aiDraft && (
          <div className="ai-box">
            <label htmlFor="f-ai">✨ Draft with AI</label>
            <textarea
              id="f-ai"
              rows={2}
              value={aiPrompt}
              onChange={(e) => setAiPrompt(e.target.value)}
              placeholder={
                state.mode === 'add'
                  ? 'Describe the task to draft…'
                  : 'How should this card change?'
              }
              disabled={aiBusy}
              // The failure is about this request, so it is attached to the
              // field the request came from rather than left floating.
              aria-invalid={aiError !== null}
              aria-describedby={aiError ? aiErrorId : undefined}
            />
            <div className="ai-row">
              <button
                type="button"
                className="ai-go"
                onClick={() => void runDraft()}
                disabled={aiBusy || !aiPrompt.trim()}
              >
                {aiBusy ? 'Drafting…' : 'Draft'}
              </button>
              {aiBusy && (
                <button type="button" className="ai-stop" onClick={cancelDraft}>
                  Cancel
                </button>
              )}
              {aiBusy ? (
                <span className="ai-note busy" role="status">
                  Drafting the card…
                </span>
              ) : (
                <span className="ai-note">
                  fills the form below — review, then Save
                </span>
              )}
            </div>
            {aiError && (
              <p className="flash err" id={aiErrorId} role="alert">
                {aiError}
              </p>
            )}
          </div>
        )}
        {/* Inert while a draft is running: these fields are about to be
            overwritten, and Save/Delete would act on values the reply is
            about to replace. It blocks this form only — the page still
            scrolls, and the draft's own Cancel above stays live. */}
        <form
          inert={aiBusy}
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          <div className="mrow">
            <div className="emoji">
              <label htmlFor="f-emoji">Emoji</label>
              <EmojiField inputId="f-emoji" value={emoji} onChange={setEmoji} />
            </div>
            <div>
              <label htmlFor="f-title">Title</label>
              <input
                id="f-title"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="What needs doing?"
                autoFocus
              />
            </div>
          </div>
          <label htmlFor="f-desc">Description</label>
          <textarea
            id="f-desc"
            value={desc}
            onChange={(e) => setDesc(e.target.value)}
          />
          <div className="mrow">
            <div>
              <label htmlFor="f-prio">Priority</label>
              <select
                id="f-prio"
                value={prio}
                onChange={(e) => setPrio(Number(e.target.value) as Prio)}
              >
                <option value={1}>1 · urgent</option>
                <option value={2}>2 · high</option>
                <option value={3}>3 · normal</option>
                <option value={4}>4 · low</option>
              </select>
            </div>
            <div>
              <label htmlFor="f-due">Due</label>
              <input
                id="f-due"
                type="date"
                value={due}
                onChange={(e) => setDue(e.target.value)}
              />
            </div>
            <div>
              <label htmlFor="f-effort">Effort</label>
              <select
                id="f-effort"
                value={effort}
                onChange={(e) => setEffort(e.target.value as Effort | '')}
              >
                <option value="">none</option>
                <option value="S">S</option>
                <option value="M">M</option>
                <option value="L">L</option>
              </select>
            </div>
          </div>
          <label className="checkline" htmlFor="f-blocked">
            <input
              id="f-blocked"
              type="checkbox"
              checked={blocked}
              onChange={(e) => setBlocked(e.target.checked)}
            />
            Blocked — waiting on something else
          </label>
          <label htmlFor="f-tags">Labels (key::value for scoped)</label>
          <LabelsCombobox
            inputId="f-tags"
            value={tags}
            suggestions={labels}
            onChange={setTags}
          />
          <label htmlFor="f-checks">Checklist (one per line, prefix "x " when done)</label>
          <textarea
            id="f-checks"
            value={checks}
            onChange={(e) => setChecks(e.target.value)}
            placeholder={'write failing test\nx reproduce locally'}
          />
          {/* Delete and Cancel on the left, the primary action on the right. */}
          <div className="actions">
            {state.mode === 'edit' && state.task.status !== 'cancelled' && (
              // Soft delete: the card moves to Cancelled, so this needs no
              // confirmation — the only irreversible delete lives in that
              // column and keeps its own.
              <button
                type="button"
                className="del"
                title="Moves the card to Cancelled — restore it any time"
                onClick={() => onDelete(state.task.id)}
              >
                Delete
              </button>
            )}
            <button type="button" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="save" disabled={!title.trim()}>
              Save
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
