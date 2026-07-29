import { useEffect, useId, useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import type { Effort, Prio, Status, Task } from '../lib/model';
import { newTask, STATUS_LABEL, STATUSES } from '../lib/model';
import type { StoryDraft } from '../lib/api';
import { isAbortError } from '../lib/api';
import { useDialogFocus } from '../lib/focus';

/** Same ceiling the server enforces on the ADR body. */
export const ADR_MAX_BYTES = 64 * 1024;
const MAX_MIN = 1;
const MAX_MAX = 20;
export const MAX_DEFAULT = 8;

const encoder = new TextEncoder();

/** Byte length of the ADR as the server will count it (UTF-8, not UTF-16). */
export function adrBytes(adr: string): number {
  return encoder.encode(adr).length;
}

/** Story count clamped to what the server accepts; junk falls back to 8. */
export function clampMax(n: number): number {
  if (!Number.isFinite(n)) return MAX_DEFAULT;
  return Math.max(MAX_MIN, Math.min(MAX_MAX, Math.round(n)));
}

/** One proposed story plus the user's review edits. */
export interface StoryRow {
  draft: StoryDraft;
  selected: boolean;
  title: string;
  prio: Prio;
  effort: Effort | '';
}

/** Proposals start selected, with the model's values as the edit baseline. */
export function toRows(drafts: readonly StoryDraft[]): StoryRow[] {
  return drafts.map((draft) => ({
    draft,
    selected: true,
    title: draft.title,
    prio: draft.prio,
    effort: draft.effort,
  }));
}

/**
 * The tasks "Add selected" would create. Only selected rows with a non-empty
 * title become tasks — an empty title cannot round-trip through the codec.
 * Nothing here touches the board; the caller commits the result.
 */
export function rowsToTasks(rows: readonly StoryRow[], status: Status): Task[] {
  return rows
    .filter((r) => r.selected && r.title.trim() !== '')
    .map((r) =>
      newTask({
        title: r.title.trim(),
        desc: r.draft.desc,
        status,
        prio: r.prio,
        due: r.draft.due || undefined,
        effort: r.effort === '' ? undefined : r.effort,
        tags: r.draft.tags,
        checks: r.draft.checks.map((c) => ({ ...c })),
      }),
    );
}

export interface AdrModalProps {
  /**
   * Splitter from the API layer; already validates every returned draft. The
   * signal is how Cancel aborts a split in flight rather than only hiding
   * the busy state.
   */
  onSplit: (
    adr: string,
    max: number,
    signal?: AbortSignal,
  ) => Promise<StoryDraft[]>;
  onAdd: (tasks: Task[]) => void;
  onClose: () => void;
}

/**
 * Paste or upload an ADR, get proposed stories, review them, then commit.
 * Nothing reaches the board until "Add selected" — the review step is the
 * whole point of the feature.
 */
export function AdrModal({ onSplit, onAdd, onClose }: AdrModalProps) {
  const [adr, setAdr] = useState('');
  const [max, setMax] = useState(MAX_DEFAULT);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [rows, setRows] = useState<StoryRow[] | null>(null);
  const [dest, setDest] = useState<Status>('todo');
  const splitAbort = useRef<AbortController | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const onDialogKeyDown = useDialogFocus(boxRef);
  const titleId = useId();
  const errorId = useId();
  const sizeErrorId = useId();

  const cancelSplit = () => splitAbort.current?.abort();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      // Mid-split, Escape cancels the request instead of closing the modal:
      // closing would throw away the ADR the user pasted.
      if (splitAbort.current) splitAbort.current.abort();
      else onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // Closed mid-split: don't leave the request running with nowhere to land.
  useEffect(() => () => splitAbort.current?.abort(), []);

  const tooBig = adrBytes(adr) > ADR_MAX_BYTES;

  const readFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const input = e.target;
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    try {
      setAdr(await file.text());
      setError(null);
    } catch {
      setError('could not read that file');
    }
  };

  const run = async () => {
    if (busy || adr.trim() === '' || tooBig) return;
    setBusy(true);
    setError(null);
    const ctrl = new AbortController();
    splitAbort.current = ctrl;
    try {
      const drafts = await onSplit(adr, clampMax(max), ctrl.signal);
      if (drafts.length === 0) {
        setError('the model returned no usable stories');
        return;
      }
      setRows(toRows(drafts));
    } catch (err) {
      // A cancel is not a failure, and nothing was consumed: the ADR and the
      // options are exactly as they were, so say nothing.
      if (!isAbortError(err)) {
        setError(err instanceof Error ? err.message : 'split failed');
      }
    } finally {
      splitAbort.current = null;
      setBusy(false);
    }
  };

  const patch = (i: number, change: Partial<StoryRow>) => {
    setRows((rs) =>
      rs === null ? rs : rs.map((r, j) => (j === i ? { ...r, ...change } : r)),
    );
  };

  const selected = rows === null ? [] : rowsToTasks(rows, dest);

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        // A stray click outside must not discard a split the user is waiting
        // for; Cancel (or Escape) is the way out while a request is running.
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div
        className="modal adr"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-busy={busy}
        tabIndex={-1}
        ref={boxRef}
        onKeyDown={onDialogKeyDown}
      >
        <h2 id={titleId}>Split an ADR into stories</h2>
        {rows === null ? (
          <>
            <label htmlFor="f-adr">Architecture decision record (markdown)</label>
            <textarea
              id="f-adr"
              value={adr}
              onChange={(e) => setAdr(e.target.value)}
              placeholder="# ADR 0007: adopt …"
              disabled={busy}
              // Both failures are about this text, so they are attached to it.
              aria-invalid={tooBig || error !== null || undefined}
              aria-describedby={
                [tooBig ? sizeErrorId : '', error ? errorId : '']
                  .filter(Boolean)
                  .join(' ') || undefined
              }
            />
            {/* Inert while a split runs, so nothing here can change under the
                request; the textarea above is disabled for the same reason. */}
            <div className="mrow" inert={busy}>
              <div>
                <label htmlFor="f-adr-file">…or upload a file</label>
                <input
                  id="f-adr-file"
                  type="file"
                  accept=".md,.markdown,.txt,text/markdown,text/plain"
                  onChange={(e) => void readFile(e)}
                />
              </div>
              <div>
                <label htmlFor="f-adr-max">Max stories</label>
                <input
                  id="f-adr-max"
                  type="number"
                  min={MAX_MIN}
                  max={MAX_MAX}
                  value={max}
                  onChange={(e) => setMax(Number(e.target.value))}
                  onBlur={() => setMax(clampMax(max))}
                />
              </div>
            </div>
            {/* Cancel on the left, the primary action on the right. The
                outcome messages ride in the row's reserved slot (same pattern
                as Settings): mounted above it they resized the modal on every
                action, and the label swaps hold floor widths for the same
                reason. One message at a time — a running split, then the
                size refusal (it blocks the button), then the last failure. */}
            <div className="actions">
              <button
                type="button"
                className="cancel-split"
                onClick={busy ? cancelSplit : onClose}
              >
                {busy ? 'Cancel split' : 'Cancel'}
              </button>
              <span className="statusline">
                {busy ? (
                  <span className="flash busy" role="status">
                    Splitting the ADR…
                  </span>
                ) : tooBig ? (
                  <span className="flash err" id={sizeErrorId} role="alert">
                    that ADR is over {Math.round(ADR_MAX_BYTES / 1024)} KiB —
                    trim it first
                  </span>
                ) : error ? (
                  <span
                    key={error}
                    className="flash err"
                    id={errorId}
                    role="alert"
                    title={error}
                  >
                    {error}
                  </span>
                ) : null}
              </span>
              <button
                type="button"
                className="save propose"
                onClick={() => void run()}
                disabled={busy || adr.trim() === '' || tooBig}
              >
                {busy ? 'Splitting…' : 'Propose stories'}
              </button>
            </div>
          </>
        ) : (
          <>
            <p className="mnote">
              Review before anything is created — untick what you don't want,
              edit titles inline.
            </p>
            <div className="storylist">
              {rows.map((r, i) => (
                <div className="storyrow" key={i}>
                  <input
                    type="checkbox"
                    checked={r.selected}
                    aria-label={`Include "${r.title}"`}
                    onChange={(e) => patch(i, { selected: e.target.checked })}
                  />
                  <input
                    value={r.title}
                    aria-label="Story title"
                    onChange={(e) => patch(i, { title: e.target.value })}
                  />
                  <select
                    value={r.prio}
                    aria-label="Priority"
                    onChange={(e) =>
                      patch(i, { prio: Number(e.target.value) as Prio })
                    }
                  >
                    <option value={1}>!1</option>
                    <option value={2}>!2</option>
                    <option value={3}>!3</option>
                    <option value={4}>!4</option>
                  </select>
                  <select
                    value={r.effort}
                    aria-label="Effort"
                    onChange={(e) =>
                      patch(i, { effort: e.target.value as Effort | '' })
                    }
                  >
                    <option value="">–</option>
                    <option value="S">S</option>
                    <option value="M">M</option>
                    <option value="L">L</option>
                  </select>
                </div>
              ))}
            </div>
            <label htmlFor="f-adr-dest">Add to column</label>
            <select
              id="f-adr-dest"
              value={dest}
              onChange={(e) => setDest(e.target.value as Status)}
            >
              {STATUSES.map((s) => (
                <option key={s} value={s}>
                  {STATUS_LABEL[s]}
                </option>
              ))}
            </select>
            {/* Cancel and Back on the left, the primary action on the right. */}
            <div className="actions">
              <button type="button" onClick={onClose}>
                Cancel
              </button>
              <button type="button" onClick={() => setRows(null)}>
                Back
              </button>
              <button
                type="button"
                className="save"
                disabled={selected.length === 0}
                onClick={() => onAdd(selected)}
              >
                Add selected ({selected.length})
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
