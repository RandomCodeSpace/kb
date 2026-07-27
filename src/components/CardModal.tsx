import { useEffect, useState } from 'react';
import type { Check, Effort, Prio, Status, Task } from '../lib/model';
import { newTask, STATUS_LABEL } from '../lib/model';

export type ModalState =
  | { mode: 'add'; status: Status }
  | { mode: 'edit'; task: Task };

export interface CardModalProps {
  state: ModalState;
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

export function CardModal({ state, onSave, onDelete, onClose }: CardModalProps) {
  const base = state.mode === 'edit' ? state.task : null;
  const [emoji, setEmoji] = useState(base?.emoji ?? '');
  const [title, setTitle] = useState(base?.title ?? '');
  const [desc, setDesc] = useState(base?.desc ?? '');
  const [prio, setPrio] = useState<Prio>(base?.prio ?? 3);
  const [due, setDue] = useState(base?.due ?? '');
  const [effort, setEffort] = useState<Effort | ''>(base?.effort ?? '');
  const [tags, setTags] = useState(base ? base.tags.join(' ') : '');
  const [checks, setChecks] = useState(base ? checksToText(base.checks) : '');

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const save = () => {
    if (!title.trim()) return;
    const fields = {
      emoji: emoji.trim(),
      title: title.trim(),
      desc: desc.trim(),
      prio,
      due: due || undefined,
      effort: effort === '' ? undefined : effort,
      tags: tags.split(/\s+/).filter(Boolean),
      checks: textToChecks(checks),
    };
    if (state.mode === 'add') {
      onSave(newTask({ ...fields, status: state.status }));
    } else {
      onSave({ ...state.task, ...fields });
    }
  };

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal" role="dialog" aria-modal="true">
        <h2>
          {state.mode === 'add'
            ? `New task · ${STATUS_LABEL[state.status]}`
            : 'Edit task'}
        </h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          <div className="mrow">
            <div className="emoji">
              <label htmlFor="f-emoji">Emoji</label>
              <input
                id="f-emoji"
                value={emoji}
                onChange={(e) => setEmoji(e.target.value)}
                placeholder="🔧"
              />
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
          <label htmlFor="f-tags">Tags (space-separated, key::value for scoped)</label>
          <input
            id="f-tags"
            value={tags}
            onChange={(e) => setTags(e.target.value)}
            placeholder="infra env::prod"
          />
          <label htmlFor="f-checks">Checklist (one per line, prefix "x " when done)</label>
          <textarea
            id="f-checks"
            value={checks}
            onChange={(e) => setChecks(e.target.value)}
            placeholder={'write failing test\nx reproduce locally'}
          />
          <div className="actions">
            <button type="submit" className="save" disabled={!title.trim()}>
              Save
            </button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
            {state.mode === 'edit' && (
              <button
                type="button"
                className="del"
                onClick={() => {
                  if (window.confirm('Delete this task?')) onDelete(state.task.id);
                }}
              >
                Delete
              </button>
            )}
          </div>
        </form>
      </div>
    </div>
  );
}
