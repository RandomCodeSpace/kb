import { useEffect, useId, useMemo, useRef, useState } from 'react';
import type { Identity } from '../lib/auth';
import type { Status, Task } from '../lib/model';
import { STATUS_LABEL } from '../lib/model';
import type {
  AIStoryRequest,
  DriftResult,
  ImportProvenance,
  SimilarItem,
  StoryDraft,
} from '../lib/api';
import {
  acceptDrift,
  checkDrift,
  DriftConflictError,
  getImportProvenance,
  getSimilar,
  isAbortError,
} from '../lib/api';
import { useDialogFocus } from '../lib/focus';
import { shouldQuery } from '../lib/similar';
import { CardEditor } from './CardEditor';

export type ModalState =
  | { mode: 'add'; status: Status }
  | { mode: 'edit'; task: Task };

export interface CardModalProps {
  state: ModalState;
  identity: Identity;
  /** Canonical server id acknowledged for the edited browser task. */
  canonicalTaskId?: string;
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
const DRIFT_DATE_FORMAT = new Intl.DateTimeFormat('en-US', {
  day: 'numeric',
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

/** One-line drift status copy for the modal's existing live-region idiom. */
export function driftMessage(result: DriftResult): string {
  if (result.state === 'baseline_recorded') {
    return 'Recorded what this issue looks like now. Future checks will show what changed.';
  }
  const date = DRIFT_DATE_FORMAT.format(new Date(result.baseline_at));
  if (result.state === 'unchanged') {
    return `No change upstream since ${date}.`;
  }
  return result.title_changed
    ? `Upstream changed since ${date}, including the title.`
    : `Upstream content changed since ${date}.`;
}

/** Durable short links are lookup keys only; forge identity stays server-owned. */
export function taskImportLinks(task: Task): string[] {
  const found = new Set<string>();
  for (const tag of task.tags) {
    if (!tag.startsWith('link::')) continue;
    const link = tag.slice('link::'.length).trim();
    if (link !== '') found.add(link);
  }
  return [...found];
}

/**
 * Identity for a similar-items row. Prefixes keep the id, link, and title
 * namespaces distinct when two server values happen to contain the same text.
 */
export function similarKey(item: SimilarItem): string {
  if (item.id !== undefined) return `id:${item.id}`;
  if (item.link !== undefined) return `link:${item.link}`;
  return `title:${item.title}`;
}

/** Return only rows whose stable identities have not been dismissed. */
export function visibleSimilarItems(
  items: readonly SimilarItem[],
  removed: ReadonlySet<string>,
): SimilarItem[] {
  return items.filter((item) => !removed.has(similarKey(item)));
}

function shownSimilarItems(
  dismissed: boolean,
  items: readonly SimilarItem[],
): readonly SimilarItem[] {
  if (dismissed) return [];
  return items;
}

function similarViaText(item: SimilarItem, killedText: string | null): string {
  if (killedText) return 'rejected';
  if (item.via === 'card') return 'on board';
  return 'imported';
}

function SimilarRow({
  item,
  onDismiss,
}: Readonly<{ item: SimilarItem; onDismiss: (key: string) => void }>) {
  const key = similarKey(item);
  const killedText = item.via === 'killed' ? killedChipText(item, new Date()) : null;
  const title = killedText ? `${killedText} \u00b7 ${item.title}` : undefined;
  const text = killedText
    ? `${killedText.slice(KILLED_PREFIX.length)} \u00b7 ${item.title}`
    : item.title;
  return (
    <div className={`similar-row${killedText ? ' killed' : ''}`}>
      <span className={`similar-via${killedText ? ' killed' : ''}`}>
        {similarViaText(item, killedText)}
      </span>
      <span className="similar-title" title={title}>{text}</span>
      <button
        type="button"
        aria-label={`Dismiss ${item.title}`}
        onClick={() => onDismiss(key)}
      >
        ✕
      </button>
    </div>
  );
}

function SimilarPanel({
  items,
  onDismiss,
  onDismissAll,
}: Readonly<{
  items: readonly SimilarItem[];
  onDismiss: (key: string) => void;
  onDismissAll: () => void;
}>) {
  if (items.length === 0) return null;
  return (
    <div className="similar">
      <span className="similar-head" role="status">
        {items.length} similar {items.length === 1 ? 'item' : 'items'} — is this a duplicate?
      </span>
      <div className="similar-list">
        {items.map((item) => (
          <SimilarRow key={similarKey(item)} item={item} onDismiss={onDismiss} />
        ))}
      </div>
      <button type="button" aria-label="Dismiss similar items" onClick={onDismissAll}>
        ✕
      </button>
    </div>
  );
}

function driftButtonLabel(
  action: 'check' | 'accept' | null,
  multiple: boolean,
): string {
  if (action === 'check') return 'Checking…';
  if (action === 'accept') return 'Updating…';
  return multiple ? 'Check selected' : 'Check upstream';
}

function DriftStatus({
  action,
  error,
  acceptedAt,
  multiple,
  result,
}: Readonly<{
  action: 'check' | 'accept' | null;
  error: string | null;
  acceptedAt: string;
  multiple: boolean;
  result: DriftResult | null;
}>) {
  if (action === 'check') {
    return <span className="flash busy" role="status">Checking upstream…</span>;
  }
  if (action === 'accept') {
    return (
      <span className="flash busy" role="status">
        Updating the comparison baseline…
      </span>
    );
  }
  if (error) {
    return (
      <span key={error} className="flash err" role="alert" title={error}>
        {error}
      </span>
    );
  }
  if (acceptedAt !== '') {
    return (
      <span className="flash ok" role="status">
        Baseline updated {DRIFT_DATE_FORMAT.format(new Date(acceptedAt))}.
      </span>
    );
  }
  if (multiple && !result) {
    return (
      <span className="flash" role="status">
        Choose the imported issue, then check it.
      </span>
    );
  }
  if (!result) return null;
  return <span className="flash ok" role="status">{driftMessage(result)}</span>;
}

function DriftDetails({
  result,
  selected,
  busy,
  onAccept,
}: Readonly<{
  result: DriftResult | null;
  selected: ImportProvenance | undefined;
  busy: boolean;
  onAccept: (candidate: ImportProvenance, revision: string) => void;
}>) {
  if (result?.state !== 'drifted') return null;
  return (
    <div className="drift-details">
      {result.title_changed ? (
        <p>
          <strong>Title:</strong>{' '}
          {result.baseline_title || '(untitled)'} →{' '}
          {result.upstream_title || '(untitled)'}
        </p>
      ) : (
        <p>The issue body changed; the title did not.</p>
      )}
      {result.summary !== '' && (
        <p><strong>Summary:</strong> {result.summary}</p>
      )}
      {selected && result.revision && (
        <div className="drift-accept">
          <p>
            This accepts the checked upstream version as the new comparison
            baseline. Edit the card separately.
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={() => onAccept(selected, result.revision!)}
          >
            Update card
          </button>
        </div>
      )}
    </div>
  );
}

function driftNotice(result: DriftResult | null, acceptedAt: string): string {
  if (result || acceptedAt !== '') {
    return 'kb does not sync. This was a one-time check you asked for; nothing was written to the forge.';
  }
  return 'kb does not sync. Each check is a one-time read you ask for; nothing is written to the forge.';
}

interface DriftReviewProps {
  importLinks: readonly string[];
  linkId: string;
  selectedLink: string;
  provenanceId: string;
  provenance: readonly ImportProvenance[];
  selectedExternalKey: string;
  selectedProvenance: ImportProvenance | undefined;
  result: DriftResult | null;
  error: string | null;
  action: 'check' | 'accept' | null;
  acceptedAt: string;
  busy: boolean;
  onLinkChange: (link: string) => void;
  onProvenanceChange: (externalKey: string) => void;
  onCheck: (candidate?: ImportProvenance) => void;
  onAccept: (candidate: ImportProvenance, revision: string) => void;
}

function DriftReview(props: Readonly<DriftReviewProps>) {
  if (props.importLinks.length === 0) return null;
  const multiple = props.provenance.length > 1;
  return (
    <section className="drift-review" aria-label="Upstream review">
      {props.importLinks.length > 1 && (
        <>
          <label htmlFor={props.linkId}>Upstream link</label>
          <select
            id={props.linkId}
            value={props.selectedLink}
            disabled={props.busy}
            onChange={(event) => props.onLinkChange(event.target.value)}
          >
            {props.importLinks.map((link) => (
              <option key={link} value={link}>{link}</option>
            ))}
          </select>
        </>
      )}
      {multiple && (
        <>
          <label htmlFor={props.provenanceId}>Imported issue</label>
          <select
            id={props.provenanceId}
            value={props.selectedExternalKey}
            disabled={props.busy}
            onChange={(event) => props.onProvenanceChange(event.target.value)}
          >
            {props.provenance.map((item) => (
              <option key={item.external_key} value={item.external_key}>
                {item.title} — {item.external_key}
              </option>
            ))}
          </select>
        </>
      )}
      <div className="drift-controls">
        <button
          type="button"
          disabled={props.busy}
          onClick={() => props.onCheck(multiple ? props.selectedProvenance : undefined)}
        >
          {driftButtonLabel(props.action, multiple)}
        </button>
        <span className="statusline">
          <DriftStatus
            action={props.action}
            error={props.error}
            acceptedAt={props.acceptedAt}
            multiple={multiple}
            result={props.result}
          />
        </span>
      </div>
      <DriftDetails
        result={props.result}
        selected={props.selectedProvenance}
        busy={props.busy}
        onAccept={props.onAccept}
      />
      <p className="drift-notice">{driftNotice(props.result, props.acceptedAt)}</p>
    </section>
  );
}

export function CardModal({
  state,
  identity,
  canonicalTaskId,
  labels,
  aiDraft,
  onSave,
  onDelete,
  onClose,
}: Readonly<CardModalProps>) {
  const excludeId = state.mode === 'edit' ? canonicalTaskId : undefined;
  const [title, setTitle] = useState(state.mode === 'edit' ? state.task.title : '');
  const [aiBusy, setAiBusy] = useState(false);
  const [items, setItems] = useState<SimilarItem[]>([]);
  const [dismissed, setDismissed] = useState(false);
  const [removedSimilar, setRemovedSimilar] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [lastQ, setLastQ] = useState('');
  const importLinks = useMemo(
    () => (state.mode === 'edit' ? taskImportLinks(state.task) : []),
    [state],
  );
  const [selectedLink, setSelectedLink] = useState(importLinks[0] ?? '');
  const [provenance, setProvenance] = useState<ImportProvenance[]>([]);
  const [selectedExternalKey, setSelectedExternalKey] = useState('');
  const [driftResult, setDriftResult] = useState<DriftResult | null>(null);
  const [driftError, setDriftError] = useState<string | null>(null);
  const [driftAction, setDriftAction] = useState<
    'check' | 'accept' | null
  >(null);
  const [acceptedAt, setAcceptedAt] = useState('');
  const driftBusy = driftAction !== null;
  const draftCancel = useRef<(() => void) | null>(null);
  const driftCancel = useRef<AbortController | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const linkId = useId();
  const provenanceId = useId();
  // The emoji picker is a custom element with its own keyboard grid; the trap
  // stays out of it, or Tab inside the picker would jump back to the form.
  const onDialogKeyDown = useDialogFocus(boxRef, { skipWithin: '.emojipop' });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      // Mid-draft, Escape cancels the request instead of closing the card:
      // closing would throw away the form the user is waiting to have filled.
      if (aiBusy || driftBusy) {
        draftCancel.current?.();
        driftCancel.current?.abort();
      }
      else onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [aiBusy, driftBusy, onClose]);

  // A card closed mid-request (a server refresh can do it) must not leave work
  // running with nowhere to land.
  useEffect(
    () => () => {
      draftCancel.current?.();
      driftCancel.current?.abort();
    },
    [],
  );

  useEffect(() => {
    if (!shouldQuery(title, lastQ)) {
      if (Array.from(title.trim()).length < 3) {
        setItems([]);
        setRemovedSimilar(new Set());
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
        importLinks,
        ctrl.signal,
      ).then((next) => {
        if (ctrl.signal.aborted) return;
        setItems(next);
        setRemovedSimilar(new Set());
        setLastQ(title.trim());
        setDismissed(false);
      });
    }, 400);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [excludeId, identity, importLinks, lastQ, title]);

  const selectedProvenance = provenance.find(
    (item) => item.external_key === selectedExternalKey,
  );
  const visibleItems = visibleSimilarItems(items, removedSimilar);
  const shownItems = shownSimilarItems(dismissed, visibleItems);

  const dismissSimilar = (key: string) => {
    setRemovedSimilar((current) => {
      const next = new Set(current);
      next.add(key);
      return next;
    });
  };

  const selectProvenance = (externalKey: string) => {
    setSelectedExternalKey(externalKey);
    setDriftResult(null);
    setDriftError(null);
    setAcceptedAt('');
  };

  const findDriftCandidate = async (
    ctrl: AbortController,
  ): Promise<ImportProvenance | null> => {
    const candidates = await getImportProvenance(
      identity,
      selectedLink,
      ctrl.signal,
    );
    if (ctrl.signal.aborted) return null;
    if (candidates.length === 0) throw new Error('import link not found');
    setProvenance(candidates);
    setSelectedExternalKey(candidates[0]!.external_key);
    if (candidates.length > 1) return null;
    return candidates[0]!;
  };

  const runDrift = async (candidate?: ImportProvenance) => {
    driftCancel.current?.abort();
    const ctrl = new AbortController();
    driftCancel.current = ctrl;
    setDriftAction('check');
    setDriftError(null);
    setDriftResult(null);
    setAcceptedAt('');
    try {
      const chosen = candidate ?? await findDriftCandidate(ctrl);
      if (!chosen) return;
      const result = await checkDrift(
        identity,
        chosen.source,
        chosen.external_key,
        ctrl.signal,
      );
      if (!ctrl.signal.aborted) setDriftResult(result);
    } catch (err) {
      if (!ctrl.signal.aborted && !isAbortError(err)) {
        setDriftError(
          err instanceof Error ? err.message : 'upstream check failed',
        );
      }
    } finally {
      if (driftCancel.current === ctrl) {
        driftCancel.current = null;
        setDriftAction(null);
      }
    }
  };

  const acceptReviewedDrift = async (
    candidate: ImportProvenance,
    revision: string,
  ) => {
    driftCancel.current?.abort();
    const ctrl = new AbortController();
    driftCancel.current = ctrl;
    setDriftAction('accept');
    setDriftError(null);
    try {
      const accepted = await acceptDrift(
        identity,
        candidate.source,
        candidate.external_key,
        revision,
        ctrl.signal,
      );
      if (!ctrl.signal.aborted) {
        setDriftResult(null);
        setAcceptedAt(accepted.baseline_at);
      }
    } catch (err) {
      if (!ctrl.signal.aborted && !isAbortError(err)) {
        if (err instanceof DriftConflictError) {
          setDriftResult(null);
          setAcceptedAt('');
          setDriftError(
            'Upstream changed again. Check upstream before updating the card.',
          );
        } else {
          setDriftError(
            err instanceof Error ? err.message : 'card update failed',
          );
        }
      }
    } finally {
      if (driftCancel.current === ctrl) {
        driftCancel.current = null;
        setDriftAction(null);
      }
    }
  };

  const resetDrift = (link: string) => {
    driftCancel.current?.abort();
    setSelectedLink(link);
    setProvenance([]);
    setSelectedExternalKey('');
    setDriftResult(null);
    setDriftError(null);
    setAcceptedAt('');
  };

  const close = () => {
    driftCancel.current?.abort();
    onClose();
  };

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        // A stray click outside must not discard a draft the user is waiting
        // for; Cancel (or Escape) is the way out while a request is running.
        if (e.target === e.currentTarget && !aiBusy && !driftBusy) close();
      }}
    >
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-busy={aiBusy || driftBusy}
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
          titleExtras={
            <>
              <SimilarPanel
                items={shownItems}
                onDismiss={dismissSimilar}
                onDismissAll={() => setDismissed(true)}
              />
              <DriftReview
                importLinks={importLinks}
                linkId={linkId}
                selectedLink={selectedLink}
                provenanceId={provenanceId}
                provenance={provenance}
                selectedExternalKey={selectedExternalKey}
                selectedProvenance={selectedProvenance}
                result={driftResult}
                error={driftError}
                action={driftAction}
                acceptedAt={acceptedAt}
                busy={driftBusy}
                onLinkChange={resetDrift}
                onProvenanceChange={selectProvenance}
                onCheck={(candidate) => void runDrift(candidate)}
                onAccept={(candidate, revision) => {
                  void acceptReviewedDrift(candidate, revision);
                }}
              />
            </>
          }
          onSave={onSave}
          onDelete={onDelete}
          onClose={close}
        />
      </div>
    </div>
  );
}
