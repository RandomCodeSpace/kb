import { useState } from 'react';
import type { ReactNode, RefObject } from 'react';
import type { AIStoryRequest, StoryDraft, TaskPatch } from '../lib/api';
import type { Check, Effort, Prio, TaskDraft } from '../lib/model';
import { newDraft } from '../lib/model';
import type { ModalState } from './CardModal';
import { CardAIDraft } from './CardAIDraft';
import { DateField } from './DateField';
import { EmojiField, firstEmoji } from './EmojiField';
import { LabelsCombobox } from './LabelsCombobox';

/**
 * What Save asks the board to do. Adding proposes a draft the server turns
 * into a task; editing names the task and the fields to change, so nothing
 * the form did not touch is resent.
 */
export type CardSave =
  | { mode: 'add'; draft: TaskDraft }
  | { mode: 'edit'; taskId: string; patch: TaskPatch };

export interface CardEditorProps {
  state: ModalState;
  labels: string[];
  aiDraft?: (req: AIStoryRequest, signal?: AbortSignal) => Promise<StoryDraft>;
  title: string;
  onTitleChange: (title: string) => void;
  onBusyChange: (busy: boolean) => void;
  cancelRef: RefObject<(() => void) | null>;
  titleExtras: ReactNode;
  onSave: (save: CardSave) => void;
  onDelete: (taskId: string) => void;
  onClose: () => void;
}

function checksToText(checks: Check[]): string {
  return checks.map((check) => (check.done ? `x ${check.text}` : check.text)).join('\n');
}

function textToChecks(src: string): Check[] {
  return src
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) =>
      line.startsWith('x ')
        ? { text: line.slice(2).trim(), done: true }
        : { text: line, done: false },
    );
}

export function CardEditor({
  state,
  labels,
  aiDraft,
  title,
  onTitleChange,
  onBusyChange,
  cancelRef,
  titleExtras,
  onSave,
  onDelete,
  onClose,
}: Readonly<CardEditorProps>) {
  const base = state.mode === 'edit' ? state.task : null;
  const [emoji, setEmoji] = useState(() => firstEmoji(base?.emoji ?? ''));
  const [blocked, setBlocked] = useState(base?.blocked ?? false);
  const [desc, setDesc] = useState(base?.desc ?? '');
  const [prio, setPrio] = useState<Prio>(base?.prio ?? 3);
  const [due, setDue] = useState(base?.due ?? '');
  const [effort, setEffort] = useState<Effort | ''>(base?.effort ?? '');
  const [tags, setTags] = useState<string[]>(base ? base.tags : []);
  const [checks, setChecks] = useState(base ? checksToText(base.checks) : '');
  const [busy, setBusy] = useState(false);

  const applyDraft = (draft: StoryDraft) => {
    if (draft.title !== '') onTitleChange(draft.title);
    setEmoji(firstEmoji(draft.emoji));
    setDesc(draft.desc);
    setPrio(draft.prio);
    setDue(draft.due);
    setEffort(draft.effort);
    setTags(draft.tags);
    setChecks(checksToText(draft.checks));
  };

  const setDraftBusy = (next: boolean) => {
    setBusy(next);
    onBusyChange(next);
  };

  const save = () => {
    if (!title.trim()) return;
    // due and effort go out as '' rather than omitted: on a patch an absent
    // field means "leave it alone", so clearing a date has to be said.
    const next = {
      emoji: firstEmoji(emoji),
      title: title.trim(),
      desc: desc.trim(),
      blocked,
      prio,
      due,
      effort,
      tags,
      checks: textToChecks(checks),
    };
    onSave(
      state.mode === 'add'
        ? {
          mode: 'add',
          draft: newDraft({
            ...next,
            status: state.status,
            due: due || undefined,
            effort: effort === '' ? undefined : effort,
          }),
        }
        : { mode: 'edit', taskId: state.task.id, patch: next },
    );
  };

  return (
    <>
      {aiDraft && (
        <CardAIDraft
          state={state}
          aiDraft={aiDraft}
          title={title}
          desc={desc}
          prio={prio}
          due={due}
          effort={effort}
          tags={tags}
          checks={textToChecks(checks)}
          onApply={applyDraft}
          onBusyChange={setDraftBusy}
          cancelRef={cancelRef}
        />
      )}
      <form
        inert={busy}
        onSubmit={(event) => {
          event.preventDefault();
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
            <input id="f-title" value={title} onChange={(event) => onTitleChange(event.target.value)} placeholder="What needs doing?" autoFocus />
          </div>
        </div>
        {titleExtras}
        <label htmlFor="f-desc">Description</label>
        <textarea id="f-desc" value={desc} onChange={(event) => setDesc(event.target.value)} />
        <div className="mrow">
          <div>
            <label htmlFor="f-prio">Priority</label>
            <select id="f-prio" value={prio} onChange={(event) => setPrio(Number(event.target.value) as Prio)}>
              <option value={1}>1 · urgent</option>
              <option value={2}>2 · high</option>
              <option value={3}>3 · normal</option>
              <option value={4}>4 · low</option>
            </select>
          </div>
          <div>
            <label htmlFor="f-due">Due</label>
            <DateField inputId="f-due" value={due} onChange={setDue} />
          </div>
          <div>
            <label htmlFor="f-effort">Effort</label>
            <select id="f-effort" value={effort} onChange={(event) => setEffort(event.target.value as Effort | '')}>
              <option value="">none</option>
              <option value="S">S</option>
              <option value="M">M</option>
              <option value="L">L</option>
            </select>
          </div>
        </div>
        <label className="checkline" htmlFor="f-blocked">
          <input id="f-blocked" type="checkbox" checked={blocked} onChange={(event) => setBlocked(event.target.checked)} />
          {' '}Blocked — waiting on something else
        </label>
        <label htmlFor="f-tags">Labels (key::value for scoped)</label>
        <LabelsCombobox inputId="f-tags" value={tags} suggestions={labels} onChange={setTags} />
        <label htmlFor="f-checks">Checklist (one per line, prefix "x " when done)</label>
        <textarea id="f-checks" value={checks} onChange={(event) => setChecks(event.target.value)} placeholder={'write failing test\nx reproduce locally'} />
        <div className="actions">
          {state.mode === 'edit' && state.task.status !== 'cancelled' && (
            <button type="button" className="del" title="Moves the card to Cancelled — restore it any time" onClick={() => onDelete(state.task.id)}>Delete</button>
          )}
          <button type="button" onClick={onClose}>Cancel</button>
          <button type="submit" className="save" disabled={!title.trim()}>Save</button>
        </div>
      </form>
    </>
  );
}
