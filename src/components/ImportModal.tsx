import { useEffect, useId, useRef, useState } from 'react';
import type { Status, Task } from '../lib/model';
import { STATUS_LABEL, STATUSES } from '../lib/model';
import type {
  ForgeSource,
  ImportDraft,
  ImportLinkItem,
  ImportPreview,
  ImportPreviewRequest,
  RecordImportLinksRequest,
} from '../lib/api';
import { isAbortError } from '../lib/api';
import { useDialogFocus } from '../lib/focus';
import {
  clampMax,
  MAX_DEFAULT,
  rowsToTasks,
} from './AdrModal';
import type { StoryRow } from './AdrModal';

export interface ImportRow extends StoryRow {
  draft: ImportDraft;
  duplicateChip?: string;
}

/** Seed review state while distinguishing exact imports from fuzzy advice. */
export function toImportRows(drafts: readonly ImportDraft[]): ImportRow[] {
  return drafts.map((draft) => {
    const duplicate = draft.duplicate_of;
    const linked = duplicate?.via === 'link';
    return {
      draft,
      selected: !linked,
      title: draft.title,
      prio: draft.prio,
      effort: draft.effort,
      ...(duplicate === undefined
        ? {}
        : {
            duplicateChip: linked
              ? `already on the board as “${duplicate.title}”`
              : `similar: “${duplicate.title}”`,
          }),
    };
  });
}

/** Convert reviewed imports through the ADR helper so task shape stays shared. */
export function importRowsToTasks(
  rows: readonly ImportRow[],
  status: Status,
): Task[] {
  return rowsToTasks(rows, status);
}

function selectedLinkItems(rows: readonly ImportRow[]): ImportLinkItem[] {
  return rows.flatMap((row) => {
    const title = row.title.trim();
    const { external_key: externalKey, link, url } = row.draft;
    if (
      !row.selected ||
      title === '' ||
      externalKey === undefined ||
      link === undefined ||
      url === undefined
    ) {
      return [];
    }
    return [{ external_key: externalKey, link, url, title }];
  });
}

export interface ImportModalProps {
  sources: readonly ForgeSource[];
  onPreview: (
    req: ImportPreviewRequest,
    signal: AbortSignal,
  ) => Promise<ImportPreview>;
  onAdd: (tasks: Task[]) => void;
  onCommitLinks: (req: RecordImportLinksRequest) => void;
  onClose: () => void;
}

/**
 * Fetch source issues first, then hold every transformed card for review.
 * No board state changes until the explicit Add selected action.
 */
export function ImportModal({
  sources,
  onPreview,
  onAdd,
  onCommitLinks,
  onClose,
}: ImportModalProps) {
  const [source, setSource] = useState(sources[0]?.name ?? '');
  const [reference, setReference] = useState('');
  const [max, setMax] = useState(MAX_DEFAULT);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [rows, setRows] = useState<ImportRow[] | null>(null);
  const [dest, setDest] = useState<Status>('todo');
  const previewAbort = useRef<AbortController | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const onDialogKeyDown = useDialogFocus(boxRef);
  const titleId = useId();
  const errorId = useId();

  const cancelFetch = () => {
    const active = previewAbort.current;
    previewAbort.current = null;
    active?.abort();
    setBusy(false);
  };

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      if (previewAbort.current !== null) cancelFetch();
      else onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  useEffect(
    () => () => {
      previewAbort.current?.abort();
      previewAbort.current = null;
    },
    [],
  );

  const run = async () => {
    if (busy || source === '' || reference.trim() === '') return;
    const ctrl = new AbortController();
    previewAbort.current = ctrl;
    setBusy(true);
    setError(null);
    try {
      const result = await onPreview(
        { source, ref: reference.trim(), max: clampMax(max) },
        ctrl.signal,
      );
      // Cancel may have released the UI for another request while this promise
      // was settling. Only the controller still owning the slot may publish.
      if (previewAbort.current !== ctrl) return;
      if (result.drafts.length === 0) {
        setError('the import returned no usable cards');
        return;
      }
      setPreview(result);
      setRows(toImportRows(result.drafts));
    } catch (err) {
      if (previewAbort.current === ctrl && !isAbortError(err)) {
        setError(err instanceof Error ? err.message : 'import preview failed');
      }
    } finally {
      if (previewAbort.current === ctrl) {
        previewAbort.current = null;
        setBusy(false);
      }
    }
  };

  const patch = (index: number, change: Partial<ImportRow>) => {
    setRows((current) =>
      current === null
        ? current
        : current.map((row, i) =>
            i === index ? { ...row, ...change } : row,
          ),
    );
  };

  const tasks = rows === null ? [] : importRowsToTasks(rows, dest);
  const links = rows === null ? [] : selectedLinkItems(rows);

  const addSelected = () => {
    if (rows === null || tasks.length === 0) return;
    // Cards are primary. The API helper deliberately makes the provenance
    // journal best-effort, so this call cannot undo or block the board commit.
    onAdd(tasks);
    if (links.length > 0) onCommitLinks({ source, items: links });
  };

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(event) => {
        if (event.target === event.currentTarget && !busy) onClose();
      }}
    >
      <div
        className="modal adr import"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-busy={busy}
        tabIndex={-1}
        ref={boxRef}
        onKeyDown={onDialogKeyDown}
      >
        <h2 id={titleId}>Import issues</h2>
        {rows === null || preview === null ? (
          <>
            <div className="mrow" inert={busy}>
              <div>
                <label htmlFor="f-import-source">Source</label>
                <select
                  id="f-import-source"
                  value={source}
                  onChange={(event) => setSource(event.target.value)}
                >
                  {sources.map((item) => (
                    <option key={item.name} value={item.name}>
                      {item.name} ({item.kind})
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label htmlFor="f-import-max">Max cards</label>
                <input
                  id="f-import-max"
                  type="number"
                  min={1}
                  max={20}
                  value={max}
                  onChange={(event) => setMax(Number(event.target.value))}
                  onBlur={() => setMax(clampMax(max))}
                />
              </div>
            </div>
            <label htmlFor="f-import-ref">
              Issue, project, board or milestone URL — or owner/repo
            </label>
            <input
              id="f-import-ref"
              value={reference}
              onChange={(event) => setReference(event.target.value)}
              disabled={busy}
              aria-invalid={error !== null || undefined}
              aria-describedby={error === null ? undefined : errorId}
              placeholder="gitlab.example.com/group/project/-/issues/42"
            />
            <div className="actions">
              <button
                type="button"
                className="cancel-split"
                onClick={busy ? cancelFetch : onClose}
              >
                {busy ? 'Cancel fetch' : 'Cancel'}
              </button>
              <span className="statusline">
                {busy ? (
                  <span className="flash busy" role="status">
                    Fetching and transforming issues…
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
                disabled={busy || source === '' || reference.trim() === ''}
                onClick={() => void run()}
              >
                {busy ? 'Fetching…' : 'Fetch'}
              </button>
            </div>
          </>
        ) : (
          <>
            <p className="import-notice">
              These cards are AI transformations of the source issues, not
              copies — review each one before adding.
            </p>
            {preview.truncated && (
              <p className="import-banner" role="status">
                showing first {preview.fetched} of {preview.total_hint} — import
                in batches
              </p>
            )}
            {preview.note !== '' && <p className="mnote">{preview.note}</p>}
            <div className="storylist">
              {rows.map((row, index) => (
                <div className="storyrow importrow" key={index}>
                  <input
                    type="checkbox"
                    checked={row.selected}
                    aria-label={`Include "${row.title}"`}
                    onChange={(event) =>
                      patch(index, { selected: event.target.checked })
                    }
                  />
                  <input
                    value={row.title}
                    aria-label="Card title"
                    onChange={(event) =>
                      patch(index, { title: event.target.value })
                    }
                  />
                  {row.draft.link && (
                    <span className="import-chip link-chip">
                      {row.draft.link}
                    </span>
                  )}
                  {row.duplicateChip && (
                    <span
                      className={`import-chip duplicate-chip ${
                        row.draft.duplicate_of?.via ?? ''
                      }`}
                    >
                      {row.duplicateChip}
                    </span>
                  )}
                </div>
              ))}
            </div>
            <label htmlFor="f-import-dest">Add to column</label>
            <select
              id="f-import-dest"
              value={dest}
              onChange={(event) => setDest(event.target.value as Status)}
            >
              {STATUSES.map((status) => (
                <option key={status} value={status}>
                  {STATUS_LABEL[status]}
                </option>
              ))}
            </select>
            <div className="actions">
              <button type="button" onClick={onClose}>
                Cancel
              </button>
              <button
                type="button"
                onClick={() => {
                  setPreview(null);
                  setRows(null);
                  setError(null);
                }}
              >
                Back
              </button>
              <button
                type="button"
                className="save"
                disabled={tasks.length === 0}
                onClick={addSelected}
              >
                Add selected ({tasks.length})
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
