import { useEffect, useId, useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import type { Effort, Prio, Status, TaskDraft } from '../lib/model';
import { newDraft, STATUS_LABEL, STATUSES } from '../lib/model';
import type { AIStoriesRequest, ForgeSource, StoryDraft } from '../lib/api';
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

/** Build exactly one supported split mode: pasted ADR or forge issue URL. */
export function splitRequest(
  adr: string,
  url: string,
  source: string,
): AIStoriesRequest | { error: string } {
  const hasAdr = adr.trim() !== '';
  const trimmedURL = url.trim();
  const hasURL = trimmedURL !== '';
  if (hasAdr === hasURL) return { error: 'provide adr or url' };
  if (!hasURL) return { adr };

  const trimmedSource = source.trim();
  if (trimmedSource === '') return { error: 'select a forge source' };
  return { url: trimmedURL, source: trimmedSource };
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
 * The drafts "Add selected" would create. Only selected rows with a non-empty
 * title become drafts — the server refuses a blank title. Nothing here touches
 * the board; the caller creates each one.
 */
export function rowsToTasks(
  rows: readonly StoryRow[],
  status: Status,
): TaskDraft[] {
  return rows
    .filter((r) => r.selected && r.title.trim() !== '')
    .map((r) =>
      newDraft({
        title: r.title.trim(),
        emoji: r.draft.emoji,
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
  sources: readonly ForgeSource[];
  /**
   * Splitter from the API layer; already validates every returned draft. The
   * signal is how Cancel aborts a split in flight rather than only hiding
   * the busy state.
   */
  onSplit: (
    req: AIStoriesRequest,
    signal: AbortSignal,
  ) => Promise<StoryDraft[]>;
  onAdd: (drafts: TaskDraft[]) => void;
  onClose: () => void;
}

function updateStoryRows(
  rows: StoryRow[] | null,
  index: number,
  change: Partial<StoryRow>,
): StoryRow[] | null {
  if (rows === null) return null;
  return rows.map((row, rowIndex) => (
    rowIndex === index ? { ...row, ...change } : row
  ));
}

function adrDescribedBy(
  tooBig: boolean,
  sizeErrorId: string,
  hasURL: boolean,
  error: string | null,
  errorId: string,
): string | undefined {
  const ids: string[] = [];
  if (tooBig) ids.push(sizeErrorId);
  if (!hasURL && error) ids.push(errorId);
  return ids.length > 0 ? ids.join(' ') : undefined;
}

function proposeDisabled(
  busy: boolean,
  hasAdr: boolean,
  hasURL: boolean,
  tooBig: boolean,
): boolean {
  return busy || (!hasAdr && !hasURL) || (hasAdr && hasURL) || tooBig;
}

function AdrStatus({
  busy,
  tooBig,
  error,
  sizeErrorId,
  errorId,
}: Readonly<{
  busy: boolean;
  tooBig: boolean;
  error: string | null;
  sizeErrorId: string;
  errorId: string;
}>) {
  if (busy) {
    return <output className="flash busy">Splitting the ADR…</output>;
  }
  if (tooBig) {
    return (
      <span className="flash err" id={sizeErrorId} role="alert">
        that ADR is over {Math.round(ADR_MAX_BYTES / 1024)} KiB — trim it first
      </span>
    );
  }
  if (!error) return null;
  return (
    <span
      key={error}
      className="flash err"
      id={errorId}
      role="alert"
      title={error}
    >
      {error}
    </span>
  );
}

/**
 * Paste or upload an ADR, get proposed stories, review them, then commit.
 * Nothing reaches the board until "Add selected" — the review step is the
 * whole point of the feature.
 */
export function AdrModal({
  sources,
  onSplit,
  onAdd,
  onClose,
}: Readonly<AdrModalProps>) {
  const [adr, setAdr] = useState('');
  const [url, setURL] = useState('');
  const [source, setSource] = useState(sources[0]?.name ?? '');
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

  useEffect(() => {
    if (source === '' && sources[0]) setSource(sources[0].name);
  }, [source, sources]);

  const hasAdr = adr.trim() !== '';
  const hasURL = url.trim() !== '';
  const tooBig = adrBytes(adr) > ADR_MAX_BYTES;
  const urlError = hasURL && error !== null;

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
    if (busy || tooBig) return;
    const req = splitRequest(adr, url, source);
    if ('error' in req) {
      setError(req.error);
      return;
    }
    setBusy(true);
    setError(null);
    const ctrl = new AbortController();
    splitAbort.current = ctrl;
    try {
      const drafts = await onSplit({ ...req, max: clampMax(max) }, ctrl.signal);
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
    setRows((current) => updateStoryRows(current, i, change));
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
              onChange={(e) => {
                setAdr(e.target.value);
                setError(null);
              }}
              placeholder="# ADR 0007: adopt …"
              disabled={busy || hasURL}
              // ADR validation and request failures are attached to this input.
              aria-invalid={tooBig || (!hasURL && error !== null) || undefined}
              aria-describedby={adrDescribedBy(
                tooBig,
                sizeErrorId,
                hasURL,
                error,
                errorId,
              )}
            />
            <p className="mnote">…or split from a forge issue</p>
            <div className="mrow" inert={busy}>
              <div>
                <label htmlFor="f-adr-source">Source</label>
                <select
                  id="f-adr-source"
                  value={source}
                  onChange={(e) => {
                    setSource(e.target.value);
                    setError(null);
                  }}
                  disabled={busy || hasAdr}
                >
                  {sources.map((item) => (
                    <option key={item.name} value={item.name}>
                      {item.name}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label htmlFor="f-adr-url">Forge issue URL</label>
                <input
                  id="f-adr-url"
                  type="text"
                  value={url}
                  onChange={(e) => {
                    setURL(e.target.value);
                    setError(null);
                  }}
                  placeholder="gitlab.example.com/group/project/-/issues/42"
                  disabled={busy || hasAdr}
                  aria-invalid={urlError || undefined}
                  aria-describedby={urlError ? errorId : undefined}
                />
              </div>
            </div>
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
                  disabled={busy || hasURL}
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
                  disabled={busy}
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
                <AdrStatus
                  busy={busy}
                  tooBig={tooBig}
                  error={error}
                  sizeErrorId={sizeErrorId}
                  errorId={errorId}
                />
              </span>
              <button
                type="button"
                className="save propose"
                onClick={() => void run()}
                disabled={proposeDisabled(busy, hasAdr, hasURL, tooBig)}
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
