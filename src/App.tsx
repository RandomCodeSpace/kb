import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import type { Board, Status, Task } from './lib/model';
import type { BoardFilter } from './lib/filter';
import { emptyFilter, filterBoard, isFilterActive, toggleTag } from './lib/filter';
import { parse, serialize } from './lib/markdown';
import {
  bumpShipped,
  continueAfterLocalPersistence,
  loadDirty,
  LocalStore,
  setDirty,
  shipKey,
  shippedToday,
  unshipToday,
} from './lib/store';
import type {
  DurableSnapshot,
  DurableVersion,
  PendingBoardWrite,
} from './lib/store';
import {
  drainAndReport,
  enqueueIntentBeforeMutation,
  MetadataOutbox,
  reconcileAndDrain,
  removeIntentBeforeMutation,
} from './lib/outbox';
import type { OutboxStatus } from './lib/outbox';
import type { BootAction, Identity } from './lib/auth';
import {
  bootAction,
  clearIdentity,
  displayName,
  identityNamespace,
  loadIdentity,
  loadLocalDisplayName,
  ReauthRequiredError,
  resolveAzureIdentity,
  saveIdentity,
  saveLocalDisplayName,
  serverAuthMode,
} from './lib/auth';
import {
  commitLiveCandidate,
  mergeAcknowledgedState,
  recomputeDeletedCanonicalIDs,
  reconcileLegacyBootstrap,
  reconcilePendingBoardWrite,
  reconcileStartupBoardFetch,
  RemoteStore,
  sameBoardSemantics,
} from './lib/remote';
import type { LiveBoardSnapshot } from './lib/remote';
import type {
  AISettings,
  AIStoryRequest,
  ForgeSource,
  RecordImportLinksRequest,
} from './lib/api';
import {
  aiStories,
  aiStory,
  getIntegrations,
  getLabels,
  getSettings,
  importPreview,
  killReasonRequest,
} from './lib/api';
import { boardLabels, unionLabels } from './lib/labels';
import { burst } from './lib/confetti';
import { AdrModal } from './components/AdrModal';
import {
  BoardView,
  movedAnnouncement,
  moveTask,
  setShowCancelledFlag,
  showCancelledFlag,
} from './components/Board';
import { CardModal } from './components/CardModal';
import type { ModalState } from './components/CardModal';
import { ShipDialog, shipWarning } from './components/ShipDialog';
import type { ShipWarning } from './components/ShipDialog';
import { Confetti } from './components/Confetti';
import { ConfirmDialog } from './components/ConfirmDialog';
import { ReconnectModal } from './components/ReconnectModal';
import { DebugOverlay, debugEnabled, setDebugEnabled } from './components/DebugOverlay';
import { IdentityGate } from './components/IdentityGate';
import { SettingsModal } from './components/SettingsModal';
import { ImportModal } from './components/ImportModal';

type SyncState = 'off' | 'ok' | 'error' | 'expired';

/** A move to Done held back by the F9/F10 confirmation. */
interface ShipPrompt {
  taskId: string;
  index: number;
  title: string;
  warning: ShipWarning;
}

/**
 * Shown when a board refresh (first server contact, or a 409 merge) had to
 * close an open card or a pending Done confirmation. Silently discarding what
 * someone was typing is the part that must not happen unannounced.
 */
const REFRESH_NOTICE =
  'The board was refreshed from the server, so the card you had open was closed. Any unsaved text in it was not kept.';
const LEGACY_RECOVERY_NOTICE =
  'This board was recovered from older local data. Tasks changed or deleted while offline could not be identified exactly, so unmatched server tasks were kept.';
export const LOCAL_PERSISTENCE_NOTICE =
  'This browser could not save the board locally. Keep this tab open while server sync retries, then free browser storage or allow site data.';

export function mergeConflictNotice(conflicts: readonly string[]): string | null {
  if (conflicts.length === 0) return null;
  return `Concurrent edits conflicted in ${conflicts.join(', ')}. Your values and ordering were kept.`;
}

const NOTICE_SEPARATOR = '\n\n';

export type NoticeEvent =
  | { type: 'report'; message: string }
  | { type: 'dismiss' };

export type NoticeTransition = {
  notice: string | null;
  announcement: string | null;
};

/** Pure transition used by the synchronous notice owner in BoardApp. */
export function transitionNotice(
  existing: string | null,
  event: NoticeEvent,
): NoticeTransition {
  if (event.type === 'dismiss') return { notice: null, announcement: null };
  const notices = existing ? existing.split(NOTICE_SEPARATOR) : [];
  if (notices.includes(event.message)) {
    return { notice: existing, announcement: null };
  }
  notices.push(event.message);
  return { notice: notices.join(NOTICE_SEPARATOR), announcement: event.message };
}

const SYNC_TITLE: Record<SyncState, string> = {
  off: 'sync off — local only',
  ok: 'synced to server',
  error: 'last save to server failed',
  expired: 'session expired — reconnect to save to the server',
};

export default function App() {
  const [identity, setIdentity] = useState<Identity | null>(() => loadIdentity());
  const [identityError, setIdentityError] = useState<string | null>(null);
  // An explicit sign-out or account switch: the gate is then a request, and
  // the open-mode auto-adopt below must not skip past it.
  const [gateRequested, setGateRequested] = useState(false);
  const [boot, setBoot] = useState<BootAction | null>(null);

  const adoptIdentity = (i: Identity) => {
    saveIdentity(i);
    setIdentityError(null);
    setGateRequested(false);
    setIdentity(i);
  };

  // With no stored identity the next step depends on the server's auth mode:
  // open mode adopts "default" and shows the board directly (the gate would
  // only be choosing a board namespace), every other mode gets the gate. An
  // explicitly requested gate renders synchronously and skips the ask.
  useEffect(() => {
    if (identity !== null || gateRequested) return;
    let cancelled = false;
    setBoot(null);
    void serverAuthMode().then((mode) => {
      if (cancelled) return;
      const action = bootAction(mode, false);
      if (action.kind === 'adopt') adoptIdentity(action.identity);
      else setBoot(action);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity, gateRequested]);

  useEffect(() => {
    if (identity?.kind !== 'azure' || identity.homeAccountId) return;
    let cancelled = false;
    void resolveAzureIdentity(identity).then(
      (resolved) => {
        if (!cancelled) adoptIdentity(resolved);
      },
      (err: unknown) => {
        if (!cancelled) {
          setIdentityError(
            err instanceof Error ? err.message : 'session expired — sign in again',
          );
        }
      },
    );
    return () => {
      cancelled = true;
    };
  }, [identity]);

  if (!identity) {
    if (gateRequested || boot?.kind === 'gate') {
      return <IdentityGate onIdentity={adoptIdentity} />;
    }
    // The auth mode is still being asked for; in open mode the board follows
    // with no gate at all, so nothing interactive belongs here.
    return (
      <main className="gate">
        <div className="gate-card">
          <h1>kb</h1>
          <p className="gate-note">Connecting…</p>
        </div>
      </main>
    );
  }
  if (identity.kind === 'azure' && !identity.homeAccountId) {
    return (
      <main className="gate">
        <div className="gate-card">
          <h1>kb</h1>
          {identityError ? (
            <>
              <p className="gate-error" role="alert">{identityError}</p>
              <button
                type="button"
                className="gate-btn"
                onClick={() => {
                  clearIdentity();
                  setGateRequested(true);
                  setIdentity(null);
                }}
              >
                Sign in again
              </button>
            </>
          ) : (
            <p className="gate-note">Restoring your Microsoft session…</p>
          )}
        </div>
      </main>
    );
  }
  return (
    <BoardApp
      // Immutable account namespace: restoring a session does not remount,
      // while switching Microsoft accounts cannot reuse local board state.
      key={identityNamespace(identity)}
      identity={identity}
      onIdentity={adoptIdentity}
      onSignOut={() => {
        clearIdentity();
        // Sign-out must land on the gate even in open mode: it is the one
        // way to pick a different board namespace there.
        setGateRequested(true);
        setIdentity(null);
      }}
    />
  );
}

/** Slot a card lands in when the caller has no opinion: end of the column. */
function appendIndex(board: Board, to: Status): number {
  return board.tasks.filter((t) => t.status === to).length;
}

/** Statuses the auto-ship timer may move a card out of. */
function shippable(status: Status): boolean {
  return status !== 'done' && status !== 'cancelled';
}

/** A pending in-app confirmation or acknowledgement (never a browser dialog). */
interface ConfirmState {
  title: string;
  body?: string;
  confirmLabel?: string;
  destructive?: boolean;
  inputLabel?: string;
  inputPlaceholder?: string;
  inputMaxLength?: number;
  inputRequired?: boolean;
  secondaryLabel?: string;
  onSecondary?: () => void;
  /** Omitted for an acknowledgement: the dialog then only closes. */
  onConfirm?: (inputValue: string) => void;
}

/** "3 tasks", "1 task" — the import confirmation names what it would replace. */
function taskCount(n: number): string {
  return `${n} ${n === 1 ? 'task' : 'tasks'}`;
}

interface BoardAppProps {
  identity: Identity;
  /** A credential the server accepted — see ReconnectModal. */
  onIdentity: (identity: Identity) => void;
  onSignOut: () => void;
}

interface RemoteAcknowledgementInput {
  sent: Board;
  sentIDs: ReadonlyMap<string, string>;
  pushed: Board;
  committedIDs: ReadonlyMap<string, string>;
  operationID?: string;
  isCurrent?: () => boolean;
  expectedGeneration?: number;
  expectedVersion?: DurableVersion;
  acknowledgementBase?: DurableSnapshot;
}

function BoardApp({ identity, onIdentity, onSignOut }: Readonly<BoardAppProps>) {
  const ns = identityNamespace(identity);
  const store = useMemo(() => new LocalStore(ns), [ns]);
  const remote = useMemo(() => new RemoteStore(), []);
  const initial = useMemo(() => store.loadOrSeed(), [store]);
  // Seeding clears the dirty flag (see LocalStore.loadOrSeed), so a demo board
  // is never pushed over a board the server already has.
  const [board, setBoardState] = useState<Board>(() => initial.board);
  // Display-only narrowing: the filter never touches the stored board, so
  // moves and edits keep operating on the full task set by id.
  const [filter, setFilter] = useState<BoardFilter>(emptyFilter);
  const handleTagClick = useCallback(
    (tag: string) => setFilter((current) => toggleTag(current, tag)),
    [],
  );
  // Browser ids stay on the local board; write acknowledgements supply the
  // canonical ids needed by server-side exclusion.
  const [canonicalTaskIDs, setCanonicalTaskIDs] = useState<
    ReadonlyMap<string, string>
  >(() => initial.canonicalTaskIDs);
  const [modal, setModal] = useState<ModalState | null>(null);
  const [ship, setShip] = useState<ShipPrompt | null>(null);
  const [streak, setStreak] = useState<number>(() => shippedToday(ns));
  const [sync, setSync] = useState<SyncState>('off');
  const [serverPresent, setServerPresent] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  // Device-local display name shown in the header; set in Settings. Cosmetic
  // only — the identity and board namespace never change with it.
  const [localName, setLocalName] = useState(loadLocalDisplayName);
  // Open while the person is being asked to restore an expired session; see
  // the effect below and ReconnectModal.
  const [showReconnect, setShowReconnect] = useState(false);
  const [showAdr, setShowAdr] = useState(false);
  const [showImport, setShowImport] = useState(false);
  const [showCancelled, setShowCancelled] = useState<boolean>(showCancelledFlag);
  const [serverLabels, setServerLabels] = useState<string[]>([]);
  const [aiSettings, setAiSettings] = useState<AISettings | null>(null);
  const [sources, setSources] = useState<ForgeSource[]>([]);
  // Off unless the settings toggle turned it on (persisted) or ?debug=1
  // overrides: nothing mounts, so a disabled overlay drives no
  // requestAnimationFrame at all.
  const [debug, setDebug] = useState<boolean>(() => debugEnabled());
  // Set when a server refresh had to drop something the user was in the
  // middle of; see adopt.
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<string | null>(null);
  // Pending in-app confirmation. Never window.confirm/window.alert: those
  // freeze the page, cannot be styled, and name the origin rather than kb.
  const [confirm, setConfirm] = useState<ConfirmState | null>(null);
  // What the polite live region says. `seq` remounts the text node so the same
  // sentence twice running is still a DOM change, which is what a screen
  // reader announces on — identical text rewritten in place says nothing.
  const [said, setSaid] = useState<{ text: string; seq: number }>({
    text: '',
    seq: 0,
  });
  const announce = useCallback((text: string) => {
    setSaid((s) => ({ text, seq: s.seq + 1 }));
  }, []);
  const applyNotice = useCallback((event: NoticeEvent) => {
    const transition = transitionNotice(noticeRef.current, event);
    noticeRef.current = transition.notice;
    setNotice(transition.notice);
    if (transition.announcement) announce(transition.announcement);
  }, [announce]);
  const reportNotice = useCallback((message: string) => {
    applyNotice({ type: 'report', message });
  }, [applyNotice]);
  const reportMergeConflicts = useCallback((conflicts: readonly string[]) => {
    const message = mergeConflictNotice(conflicts);
    if (!message) return;
    reportNotice(message);
  }, [reportNotice]);
  const localPersistenceWarnedRef = useRef(false);
  const dismissNotice = useCallback(() => {
    localPersistenceWarnedRef.current = false;
    applyNotice({ type: 'dismiss' });
  }, [applyNotice]);
  const reportPersistence = useCallback(
    (ok: boolean) => {
      if (ok) return;
      reportNotice(LOCAL_PERSISTENCE_NOTICE);
    },
    [reportNotice],
  );
  const handleOutboxStatus = useCallback(
    (status: OutboxStatus) => {
      if (status.kind === 'idle') return;
      if (status.kind === 'reauth') {
        setSync('expired');
        return;
      }
      const message =
        status.kind === 'blocked'
          ? `Metadata delivery is blocked: ${status.message}`
          : `Metadata delivery will retry: ${status.message}`;
      reportNotice(message);
    },
    [reportNotice],
  );
  const outbox = useMemo(
    () => new MetadataOutbox(ns, { onStatus: handleOutboxStatus }),
    [ns, handleOutboxStatus],
  );
  const fileRef = useRef<HTMLInputElement>(null);
  const boardRef = useRef(board);
  const canonicalTaskIDsRef = useRef(canonicalTaskIDs);
  const durableSnapshotRef = useRef(initial);
  const deletedCanonicalIDsRef = useRef(initial.deletedCanonicalIDs);
  const liveRef = useRef<LiveBoardSnapshot>({
    epoch: 0,
    board: initial.board,
    canonicalTaskIDs: initial.canonicalTaskIDs,
    deletedCanonicalIDs: initial.deletedCanonicalIDs,
    durableBase: initial,
    needsLocalPersistence: false,
    remoteClean: !loadDirty(ns),
  });
  const recoveryContinuationRef = useRef<(() => void) | null>(null);
  // Whether the user has work in progress a board refresh would discard.
  const openWorkRef = useRef(false);
  openWorkRef.current = modal !== null || ship !== null;
  const syncOnRef = useRef(false);
  // Last board that did NOT come from a user edit (initial load or server
  // adoption). The save effect uses identity against this to tell user edits
  // (mark dirty, push) from adopted state (neither).
  const cleanBoardRef = useRef(board);
  // Monotonically increasing edit generation. Save acknowledgements capture
  // the generation at dispatch and may clear the dirty flag only when it is
  // still current — a stale success (edit B landed while edit A's PUT was in
  // flight) must not clear dirtiness that belongs to the newer edit, or a
  // later failed save plus reload would silently adopt the remote copy and
  // destroy B.
  const editGenRef = useRef(0);

  const publishCommitted = useCallback((
    snapshot: DurableSnapshot,
    remoteClean: boolean,
  ) => {
    const current = liveRef.current;
    const next: LiveBoardSnapshot = {
      epoch: current.epoch,
      board: snapshot.board,
      canonicalTaskIDs: snapshot.canonicalTaskIDs,
      deletedCanonicalIDs: snapshot.deletedCanonicalIDs,
      durableBase: snapshot,
      needsLocalPersistence: false,
      remoteClean,
    };
    liveRef.current = next;
    boardRef.current = next.board;
    canonicalTaskIDsRef.current = next.canonicalTaskIDs;
    deletedCanonicalIDsRef.current = next.deletedCanonicalIDs;
    durableSnapshotRef.current = snapshot;
    setCanonicalTaskIDs(next.canonicalTaskIDs);
    setBoardState(next.board);
  }, []);

  const publishUserEdit = useCallback((
    update: Board | ((current: Board) => Board),
  ) => {
    const current = liveRef.current;
    const nextBoard = typeof update === 'function' ? update(current.board) : update;
    if (nextBoard === current.board) return;
    const liveIDs = new Set(nextBoard.tasks.map((task) => task.id));
    const canonicalTaskIDs = new Map(
      [...current.canonicalTaskIDs].filter(([clientID]) => liveIDs.has(clientID)),
    );
    const deletedCanonicalIDs = new Set(current.deletedCanonicalIDs);
    for (const [clientID, canonicalID] of current.canonicalTaskIDs) {
      if (!liveIDs.has(clientID)) deletedCanonicalIDs.add(canonicalID);
    }
    const next: LiveBoardSnapshot = {
      ...current,
      epoch: current.epoch + 1,
      board: nextBoard,
      canonicalTaskIDs,
      deletedCanonicalIDs,
      needsLocalPersistence: true,
      remoteClean: false,
    };
    liveRef.current = next;
    boardRef.current = next.board;
    canonicalTaskIDsRef.current = next.canonicalTaskIDs;
    deletedCanonicalIDsRef.current = next.deletedCanonicalIDs;
    editGenRef.current = next.epoch;
    setCanonicalTaskIDs(next.canonicalTaskIDs);
    setBoardState(next.board);
  }, []);

  const onSaveError = useCallback((err: unknown) => {
    setSync(err instanceof ReauthRequiredError ? 'expired' : 'error');
  }, []);

  /**
   * Take `next` as state that did NOT come from a user edit — the server copy
   * on first contact, or the merged board a 409 retry actually wrote. Marking
   * it clean stops the save effect echoing it back; failing to adopt a merged
   * board would make the next save delete the tasks the merge carried over.
   * parse() regenerates task ids, so any modal holding an old one is dropped:
   * a later Save could otherwise append a duplicate of an existing task. That
   * discards whatever the user had typed, which happens without warning at
   * moments they cannot see coming — so say so rather than vanishing.
   */
  const adopt = useCallback((snapshot: DurableSnapshot) => {
    cleanBoardRef.current = snapshot.board;
    if (openWorkRef.current) reportNotice(REFRESH_NOTICE);
    setModal(null);
    setShip(null);
    publishCommitted(snapshot, true);
  }, [publishCommitted, reportNotice]);

  /**
   * Fold the tasks a 409 merge carried over into the board we hold now.
   * `sent` is the board we asked to push, `pushed` the one that reached the
   * server. When `sent` is still current this is the clean-state adoption.
   * When a newer edit landed while the PUT was in flight we cannot adopt
   * (that would drop the newer edit) — but we cannot drop the merged-in tasks
   * either: RemoteStore has already committed to the merged version, so the
   * next PUT would carry a matching If-Match and delete them with no 409 and
   * no error shown.
   */
  const acknowledgeRemote = useCallback(
    async ({
      sent,
      sentIDs,
      pushed,
      committedIDs,
      operationID,
      isCurrent = () => true,
      expectedGeneration: _expectedGeneration,
      expectedVersion: _expectedVersion,
      acknowledgementBase,
    }: Readonly<RemoteAcknowledgementInput>): Promise<{
      persisted: boolean;
      conflict?: true;
      generation?: number;
      version?: DurableVersion;
      snapshot?: DurableSnapshot;
      conflicts: readonly string[];
    }> => {
      const current = liveRef.current;
      const acknowledged = mergeAcknowledgedState(
        current.board,
        current.canonicalTaskIDs,
        sent,
        sentIDs,
        pushed,
        committedIDs,
      );
      // One localStorage write contains the merged board and the complete ID
      // map. Nothing may clear dirty state or promote outbox work before it.
      if (!acknowledgementBase) {
        return { persisted: false, conflict: true, conflicts: acknowledged.conflicts };
      }
      const sourceLive = { ...current, durableBase: acknowledgementBase };
      const outcome = await commitLiveCandidate({
        sourceLive,
        candidate: {
          board: acknowledged.board,
          canonicalTaskIDs: acknowledged.canonicalTaskIDs,
          deletedCanonicalIDs: recomputeDeletedCanonicalIDs(
            acknowledgementBase.canonicalTaskIDs,
            current.deletedCanonicalIDs,
            acknowledged.canonicalTaskIDs,
          ),
        },
        readLive: () => liveRef.current,
        readDurable: () => store.loadSnapshot(),
        persist: (candidate, version, guard) => store.saveAcknowledgement(
          candidate.board,
          candidate.canonicalTaskIDs,
          candidate.deletedCanonicalIDs,
          version,
          operationID,
          () => isCurrent() && guard(),
        ),
        repairPersist: (candidate, version, guard) => store.saveIfGeneration(
          candidate.board,
          candidate.canonicalTaskIDs,
          candidate.deletedCanonicalIDs,
          version,
          () => isCurrent() && guard(),
        ),
        cancelled: () => !isCurrent(),
      });
      if (!outcome.persisted || !outcome.snapshot) {
        return {
          persisted: false,
          conflicts: [...acknowledged.conflicts, ...outcome.conflicts],
        };
      }
      publishCommitted(outcome.snapshot, true);
      void reconcileAndDrain(
        outbox,
        identity,
        outcome.snapshot.board,
        outcome.snapshot.canonicalTaskIDs,
        handleOutboxStatus,
      );
      return {
        persisted: true,
        generation: outcome.snapshot.generation,
        version: outcome.snapshot.version,
        snapshot: outcome.snapshot,
        conflicts: [...acknowledged.conflicts, ...outcome.conflicts],
      };
    },
    [store, publishCommitted, outbox, identity, handleOutboxStatus],
  );

  useEffect(() => {
    let cancelled = false;
    // Local edits that never reached the server win over the remote copy:
    // push them instead of silently adopting (and destroying) newer local work.
    const pushLocal = (
      snapshot: DurableSnapshot = durableSnapshotRef.current,
      localBoard: Board = snapshot.board,
      localTaskIDs: ReadonlyMap<string, string> = snapshot.canonicalTaskIDs,
      liveEpoch: number = liveRef.current.epoch,
    ) => {
      syncOnRef.current = true;
      const gen = liveEpoch;
      const sent = localBoard;
      remote.saveRemote(
        identity,
        sent,
        (err) => {
          if (!cancelled) onSaveError(err);
        },
        async ({
          pushed,
          taskIDs,
          conflicts = [],
          operationID,
          isCurrent,
          generation: ackGeneration,
          durableVersion: ackVersion,
          durableSnapshot: ackSnapshot,
        }) => {
          // Only an ack for the newest edit may clear the dirty flag.
          const fresh = editGenRef.current === gen;
          if (cancelled) return;
          const acknowledged = await acknowledgeRemote({
            sent,
            sentIDs: localTaskIDs,
            pushed,
            committedIDs: taskIDs,
            operationID,
            isCurrent,
            expectedGeneration: ackGeneration,
            expectedVersion: ackVersion,
            acknowledgementBase: ackSnapshot,
          });
          if (!isCurrent?.()) return;
          if (acknowledged.conflict) return acknowledged;
          if (fresh && acknowledged.persisted) setDirty(ns, false);
          reportMergeConflicts([
            ...conflicts,
            ...acknowledged.conflicts,
          ]);
          if (fresh && acknowledged.persisted) setSync('ok');
          return acknowledged;
        },
        {
          canonicalTaskIDs: localTaskIDs,
          pendingWriteStager: store,
          durableVersion: snapshot.version,
          durableSnapshot: snapshot,
          isLiveCurrent: () => !cancelled && liveRef.current.epoch === gen,
        },
      );
    };
    const persistCandidate = (
      sourceLive: LiveBoardSnapshot,
      nextBoard: Board,
      nextIDs: ReadonlyMap<string, string>,
      nextDeleted: ReadonlySet<string>,
      recovery = false,
    ) => commitLiveCandidate({
      sourceLive,
      candidate: {
        board: nextBoard,
        canonicalTaskIDs: nextIDs,
        deletedCanonicalIDs: nextDeleted,
      },
      readLive: () => liveRef.current,
      readDurable: () => store.loadSnapshot(),
      persist: (candidate, version, guard) => recovery
        ? store.completeIdentityRecovery(
          candidate.board,
          candidate.canonicalTaskIDs,
          candidate.deletedCanonicalIDs,
          version,
          () => !cancelled && guard(),
        )
        : store.saveIfGeneration(
          candidate.board,
          candidate.canonicalTaskIDs,
          candidate.deletedCanonicalIDs,
          version,
          () => !cancelled && guard(),
        ),
      repairPersist: (candidate, version, guard) => store.saveIfGeneration(
        candidate.board,
        candidate.canonicalTaskIDs,
        candidate.deletedCanonicalIDs,
        version,
        () => !cancelled && guard(),
      ),
      cancelled: () => cancelled,
    });
    const recoverPending: (pendingWrite: PendingBoardWrite) => Promise<void> = async (
      pendingWrite,
    ) => {
      try {
        const result = await reconcilePendingBoardWrite({
          remote,
          identity,
          pendingWrite,
          readLive: () => liveRef.current,
          readSnapshot: () => store.loadSnapshot(),
          persistAcknowledgement: (
            nextBoard,
            taskIDs,
            deletedIDs,
            expectedVersion,
            operationID,
            guard,
          ) => store.saveAcknowledgement(
            nextBoard,
            taskIDs,
            deletedIDs,
            expectedVersion,
            operationID,
            () => !cancelled && guard(),
          ),
          repairPersist: (nextBoard, taskIDs, deletedIDs, version, guard) =>
            store.saveIfGeneration(
              nextBoard,
              taskIDs,
              deletedIDs,
              version,
              () => !cancelled && guard(),
            ),
          apply: (recovered, snapshot) => {
            cleanBoardRef.current = snapshot.board;
            publishCommitted(snapshot, !recovered.needsPush);
          },
          queuePush: pushLocal,
          cancelled: () => cancelled,
        });
        recoveryContinuationRef.current = result.recoveryPending
          ? () => { void recoverPending(pendingWrite); }
          : null;
        reportMergeConflicts(result.recovered.conflicts);
        reportPersistence(result.persisted);
        if (!result.persisted || result.recovered.needsPush) {
          setDirty(ns, true);
        } else {
          setDirty(ns, false);
          syncOnRef.current = true;
          setSync('ok');
        }
      } catch (err) {
        if (!cancelled) onSaveError(err);
      }
    };

    const fetchCleanStartup = async (): Promise<{
      remoteBoard: Board | null;
      remoteTaskIDs: ReadonlyMap<string, string>;
      stopped: boolean;
    }> => {
      if (loadDirty(ns)) {
        return { remoteBoard: null, remoteTaskIDs: new Map(), stopped: false };
      }
      try {
        const startup = await reconcileStartupBoardFetch({
          remote,
          identity,
          readLive: () => liveRef.current,
          readSnapshot: () => store.loadSnapshot(),
          persist: (nextBoard, taskIDs, deletedIDs, expectedVersion, guard) =>
            store.saveIfGeneration(
              nextBoard,
              taskIDs,
              deletedIDs,
              expectedVersion,
              () => !cancelled && guard(),
            ),
          apply: (snapshot) => {
            cleanBoardRef.current = snapshot.board;
            publishCommitted(snapshot, false);
            setDirty(ns, true);
          },
          push: pushLocal,
          cancelled: () => cancelled,
        });
        if (cancelled) {
          return { remoteBoard: null, remoteTaskIDs: new Map(), stopped: true };
        }
        if (startup.merged) {
          reportMergeConflicts(startup.merged.conflicts);
          reportPersistence(startup.persisted === true);
          return { remoteBoard: null, remoteTaskIDs: new Map(), stopped: true };
        }
        return {
          remoteBoard: startup.remoteBoard,
          remoteTaskIDs: startup.remoteTaskIDs,
          stopped: false,
        };
      } catch (err) {
        // Server present but board fetch failed — keep the local board,
        // report the failure, and do not enable autosave over broken auth.
        if (!cancelled) onSaveError(err);
        return { remoteBoard: null, remoteTaskIDs: new Map(), stopped: true };
      }
    };

    const recoverLegacyStartup = async (): Promise<boolean> => {
      if (
        !loadDirty(ns) ||
        !initial.migratedRaw ||
        canonicalTaskIDsRef.current.size !== 0
      ) {
        return false;
      }
      try {
        const result = await reconcileLegacyBootstrap({
          remote,
          identity,
          readLive: () => liveRef.current,
          readSnapshot: () => store.loadSnapshot(),
          persist: (nextBoard, taskIDs, deletedIDs, expectedVersion, guard) =>
            store.completeIdentityRecovery(
              nextBoard,
              taskIDs,
              deletedIDs,
              expectedVersion,
              () => !cancelled && guard(),
            ),
          repairPersist: (nextBoard, taskIDs, deletedIDs, version, guard) =>
            store.saveIfGeneration(
              nextBoard,
              taskIDs,
              deletedIDs,
              version,
              () => !cancelled && guard(),
            ),
          apply: (_recovered, needsPush, snapshot) => {
            cleanBoardRef.current = snapshot.board;
            publishCommitted(snapshot, !needsPush);
            reportNotice(LEGACY_RECOVERY_NOTICE);
            setDirty(ns, needsPush);
            if (!needsPush) {
              syncOnRef.current = true;
              setSync('ok');
            }
          },
          queuePush: pushLocal,
          cancelled: () => cancelled,
        });
        reportMergeConflicts(result.recovered.conflicts);
        reportPersistence(result.persisted);
        if (!result.persisted) setDirty(ns, true);
      } catch (err) {
        if (!cancelled) onSaveError(err);
      }
      return true;
    };

    const pushDirtyStartup = async (): Promise<boolean> => {
      if (!loadDirty(ns)) return false;
      try {
        const dirtyLive = liveRef.current;
        const prepared = await remote.prepareDirtyMapped(
          identity,
          dirtyLive.board,
          dirtyLive.canonicalTaskIDs,
          dirtyLive.deletedCanonicalIDs,
        );
        if (cancelled) return true;
        const persisted = await persistCandidate(
          dirtyLive,
          prepared.board,
          prepared.taskIDs,
          prepared.deletedCanonicalIDs,
        );
        if (cancelled) return true;
        reportMergeConflicts(persisted.conflicts);
        reportPersistence(persisted.persisted);
        if (!persisted.persisted || !persisted.snapshot) {
          setDirty(ns, true);
          return true;
        }
        cleanBoardRef.current = persisted.snapshot.board;
        publishCommitted(persisted.snapshot, false);
        pushLocal(persisted.snapshot);
      } catch (err) {
        if (!cancelled) onSaveError(err);
      }
      return true;
    };

    const markStartupSynced = () => {
      syncOnRef.current = true;
      setSync('ok');
    };

    const adoptCleanStartup = async (
      remoteBoard: Board | null,
      remoteTaskIDs: ReadonlyMap<string, string>,
      cleanStartupLive: LiveBoardSnapshot,
    ): Promise<void> => {
      // Re-check dirtiness after the fetch: an edit may have landed while the
      // remote copy was in flight (boardRef catches commits whose save effect
      // has not flushed yet).
      if (liveRef.current.needsLocalPersistence) return;
      if (!remoteBoard) {
        markStartupSynced();
        return;
      }
      const current = liveRef.current;
      if (current.epoch !== cleanStartupLive.epoch) return;
      const unchanged = sameBoardSemantics({
        board: current.board,
        canonicalTaskIDs: current.canonicalTaskIDs,
        deletedCanonicalIDs: current.deletedCanonicalIDs,
        migratedRaw: current.durableBase.migratedRaw,
        pendingBoardWrite: current.durableBase.pendingBoardWrite,
      }, {
        board: remoteBoard,
        canonicalTaskIDs: remoteTaskIDs,
        deletedCanonicalIDs: new Set(),
        migratedRaw: false,
        pendingBoardWrite: null,
      });
      if (unchanged) {
        markStartupSynced();
        return;
      }
      const persisted = await persistCandidate(
        current,
        remoteBoard,
        remoteTaskIDs,
        new Set(),
        initial.migratedRaw,
      );
      if (cancelled) return;
      reportMergeConflicts(persisted.conflicts);
      reportPersistence(persisted.persisted);
      if (!persisted.persisted || !persisted.snapshot) {
        setDirty(ns, true);
        return;
      }
      adopt(persisted.snapshot);
      markStartupSynced();
    };

    void (async () => {
      const present = await remote.detect();
      if (cancelled || !present) return;
      setServerPresent(true);
      const pendingWrite = liveRef.current.durableBase.pendingBoardWrite;
      if (pendingWrite) {
        await recoverPending(pendingWrite);
        return;
      }
      const cleanStartupLive = liveRef.current;
      const startup = await fetchCleanStartup();
      if (startup.stopped || cancelled) return;
      const { remoteBoard, remoteTaskIDs } = startup;
      if (await recoverLegacyStartup()) return;
      if (await pushDirtyStartup()) return;
      await adoptCleanStartup(remoteBoard, remoteTaskIDs, cleanStartupLive);
    })();
    return () => {
      cancelled = true;
      recoveryContinuationRef.current = null;
    };
  }, [
    identity,
    remote,
    ns,
    onSaveError,
    adopt,
    acknowledgeRemote,
    initial.migratedRaw,
    store,
    reportPersistence,
    reportMergeConflicts,
    reportNotice,
  ]);

  useEffect(() => {
    const sourceLive = liveRef.current;
    if (!sourceLive.needsLocalPersistence || sourceLive.board !== board) return;
    let cancelled = false;
    const gen = sourceLive.epoch;
    void (async () => {
      const outcome = await commitLiveCandidate({
        sourceLive,
        candidate: {
          board: sourceLive.board,
          canonicalTaskIDs: sourceLive.canonicalTaskIDs,
          deletedCanonicalIDs: sourceLive.deletedCanonicalIDs,
        },
        readLive: () => liveRef.current,
        readDurable: () => store.loadSnapshot(),
        persist: (candidate, version, guard) => store.saveIfGeneration(
          candidate.board,
          candidate.canonicalTaskIDs,
          candidate.deletedCanonicalIDs,
          version,
          () => !cancelled && guard(),
        ),
        repairPersist: (candidate, version, guard) => store.saveIfGeneration(
          candidate.board,
          candidate.canonicalTaskIDs,
          candidate.deletedCanonicalIDs,
          version,
          () => !cancelled && guard(),
        ),
        cancelled: () => cancelled,
      });
      if (cancelled) return;
      reportMergeConflicts(outcome.conflicts);
      if (
        (outcome.failure && !outcome.failure.ok && outcome.failure.conflict) ||
        outcome.recoveryPending
      ) {
        setDirty(ns, true);
        return;
      }
      const persistence = outcome.persisted && outcome.snapshot
        ? {
          ok: true as const,
          generation: outcome.snapshot.generation,
          snapshot: outcome.snapshot,
        }
        : outcome.failure ?? { ok: false as const, error: new Error('board persistence stopped') };
      const persistedBoard = outcome.candidate.board;
      const sentIDs = outcome.candidate.canonicalTaskIDs;
      const remoteBase = outcome.snapshot ?? sourceLive.durableBase;
      if (outcome.snapshot) publishCommitted(outcome.snapshot, false);
      continueAfterLocalPersistence(persistence, localPersistenceWarnedRef, {
        warn: () => {
          reportNotice(LOCAL_PERSISTENCE_NOTICE);
        },
        markDirty: () => setDirty(ns, true),
        scheduleRemote: () => {
          if (cancelled) return;
          const continuation = recoveryContinuationRef.current;
          if (continuation) {
            recoveryContinuationRef.current = null;
            continuation();
            return;
          }
          if (!syncOnRef.current) return;
          remote.saveRemoteDebounced(
            identity,
            persistedBoard,
            onSaveError,
            async ({
              pushed,
              taskIDs,
              conflicts = [],
              operationID,
              isCurrent,
              generation: ackGeneration,
              durableVersion: ackVersion,
              durableSnapshot: ackSnapshot,
            }) => {
              const acknowledged = await acknowledgeRemote({
                sent: persistedBoard,
                sentIDs,
                pushed,
                committedIDs: taskIDs,
                operationID,
                isCurrent,
                expectedGeneration: ackGeneration,
                expectedVersion: ackVersion,
                acknowledgementBase: ackSnapshot,
              });
              if (!isCurrent?.()) return;
              if (acknowledged.conflict) return acknowledged;
              reportMergeConflicts([
                ...conflicts,
                ...acknowledged.conflicts,
              ]);
              if (editGenRef.current !== gen) return;
              if (acknowledged.persisted) {
                setDirty(ns, false);
                setSync('ok');
              }
              return acknowledged;
            },
            {
              canonicalTaskIDs: sentIDs,
              pendingWriteStager: store,
              durableVersion: remoteBase.version,
              durableSnapshot: remoteBase,
              isLiveCurrent: () => !cancelled && liveRef.current.epoch === gen,
            },
          );
        },
      });
    })();
    return () => { cancelled = true; };
  }, [
    board,
    store,
    remote,
    identity,
    ns,
    onSaveError,
    acknowledgeRemote,
    reportPersistence,
    reportMergeConflicts,
    reportNotice,
  ]);

  useEffect(() => {
    outbox.surfaceStoredStatus();
    void reconcileAndDrain(
      outbox,
      identity,
      boardRef.current,
      canonicalTaskIDsRef.current,
      handleOutboxStatus,
    );
    const retry = () => {
      void drainAndReport(outbox, identity, handleOutboxStatus);
    };
    window.addEventListener('online', retry);
    return () => window.removeEventListener('online', retry);
  }, [outbox, identity, handleOutboxStatus]);

  useEffect(() => {
    // Server-only extras: label suggestions and AI settings. Failures degrade
    // silently — labels fall back to board tags, AI/import actions stay hidden.
    let cancelled = false;
    setSources([]);
    if (!serverPresent) return;
    getLabels(identity)
      .then((ls) => {
        if (!cancelled) setServerLabels(ls);
      })
      .catch(() => {});
    getSettings(identity)
      .then((s) => {
        if (!cancelled) setAiSettings(s);
      })
      .catch(() => {});
    getIntegrations(identity)
      .then((nextSources) => {
        if (!cancelled) setSources(nextSources);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [serverPresent, identity, showSettings]);

  useEffect(() => {
    // Flush the pending debounced save when the page goes away so edits made
    // <800ms before close/reload still reach the server; cancel on
    // unmount/sign-out so no PUT fires with a stale identity afterwards.
    const flush = () => remote.flush();
    window.addEventListener('pagehide', flush);
    return () => {
      window.removeEventListener('pagehide', flush);
      outbox.cancel();
      remote.cancel();
    };
  }, [remote, outbox]);

  // A save that failed is the one thing the dot could never tell anyone who is
  // not looking at it. Recovery is announced too, so "it broke" is not the last
  // word; a first successful sync is not — nothing went wrong to report.
  const prevSync = useRef<SyncState>(sync);
  useEffect(() => {
    const was = prevSync.current;
    prevSync.current = sync;
    if (sync === was) return;
    if (sync === 'error' || sync === 'expired') announce(SYNC_TITLE[sync]);
    else if (sync === 'ok' && (was === 'error' || was === 'expired')) {
      announce(SYNC_TITLE[sync]);
    }
  }, [sync, announce]);

  // Ask to restore the session as soon as it goes bad. The dot on its own was
  // not enough: the server token is session-scoped on purpose, so a new
  // browser session starts looking signed in, reads the board from local
  // storage, and 401s on every request — the only visible symptom being that
  // server-backed panels fail to load. Dismissing leaves the header's
  // Reconnect button; this only runs on a change into 'expired', so it asks
  // once rather than reopening over the board.
  useEffect(() => {
    if (sync === 'expired') setShowReconnect(true);
  }, [sync]);

  /**
   * Commit a move. `index` is the slot within `to` (array order, which is what
   * the codec persists). Shipping bookkeeping only fires when the card was not
   * already in Done, so reordering inside Done neither re-counts nor re-bursts.
   */
  const applyMove = useCallback(
    (taskId: string, to: Status, index?: number) => {
      const prev = boardRef.current.tasks.find((t) => t.id === taskId);
      if (!prev) return;
      const at = index ?? appendIndex(boardRef.current, to);
      if (prev.status === to && index === undefined) return;
      const movedAt = new Date().toISOString();
      // Where the card ends up, by the same arithmetic moveTask uses — said
      // out loud below, because a move is otherwise a silent visual event.
      const others = boardRef.current.tasks.filter(
        (t) => t.status === to && t.id !== taskId,
      ).length;
      const slot = Math.max(0, Math.min(at, others));
      publishUserEdit((b) => {
        const tasks = moveTask(b.tasks, taskId, to, at, movedAt);
        // A drop that changes nothing (same column, same slot) must not mint a
        // new board: that would mark the board dirty and push an identical PUT.
        const same =
          tasks.length === b.tasks.length && tasks.every((t, i) => t === b.tasks[i]);
        return same ? b : { ...b, tasks };
      });
      announce(movedAnnouncement(prev.title, to, slot, others + 1));
      // Reopening a card takes it back off today's tally: "shipped today"
      // means done, and a card that is open again is not done.
      if (to !== 'done' && prev.status === 'done') {
        setStreak(unshipToday(ns, shipKey(prev)));
      }
      if (to === 'done' && prev.status !== 'done') {
        setStreak(bumpShipped(ns, shipKey(prev)));
        const r = document
          .querySelector(`[data-task="${taskId}"]`)
          ?.getBoundingClientRect();
        const x = r ? r.left + r.width / 2 : window.innerWidth / 2;
        const y = r ? r.top + r.height / 2 : window.innerHeight / 2;
        burst(x, y, 70);
      }
    },
    [ns, announce],
  );

  /**
   * Move a card, warning first when shipping it deserves a second look
   * (unticked checklist items, or a blocked flag — F9/F10). The auto-ship
   * timer goes through here too: with nothing open it only ever stops for a
   * blocked card.
   */
  const move = useCallback(
    (taskId: string, to: Status, index?: number) => {
      const task = boardRef.current.tasks.find((t) => t.id === taskId);
      if (!task) return;
      if (to === 'done' && task.status !== 'done') {
        const warning = shipWarning(task);
        if (warning) {
          setShip({
            taskId,
            index: index ?? appendIndex(boardRef.current, to),
            title: task.title,
            warning,
          });
          return;
        }
      }
      applyMove(taskId, to, index);
    },
    [applyMove],
  );

  /** Ship the held-back card as it stands. */
  const shipAnyway = useCallback(() => {
    if (!ship) return;
    applyMove(ship.taskId, 'done', ship.index);
    setShip(null);
  }, [ship, applyMove]);

  /** Ship the held-back card, ticking every remaining item first. */
  const shipTickingAll = useCallback(() => {
    if (!ship) return;
    publishUserEdit((b) => ({
      ...b,
      tasks: b.tasks.map((t) =>
        t.id === ship.taskId
          ? { ...t, checks: t.checks.map((c) => ({ ...c, done: true })) }
          : t,
      ),
    }));
    applyMove(ship.taskId, 'done', ship.index);
    setShip(null);
  }, [ship, applyMove]);

  const handleTick = useCallback(
    (taskId: string, checkIdx: number, pos: { x: number; y: number }) => {
      const task = boardRef.current.tasks.find((t) => t.id === taskId);
      const check = task?.checks[checkIdx];
      if (!task || !check) return;
      const turningOn = !check.done;
      const checks = task.checks.map((c, i) =>
        i === checkIdx ? { ...c, done: !c.done } : c,
      );
      publishUserEdit((b) => ({
        ...b,
        tasks: b.tasks.map((t) => (t.id === taskId ? { ...t, checks } : t)),
      }));
      if (turningOn) {
        burst(pos.x, pos.y, 14);
        // A cancelled card is out of play: finishing its checklist must not
        // resurrect it into Done.
        if (checks.every((c) => c.done) && shippable(task.status)) {
          setTimeout(() => {
            // Re-check at fire time: a tick may have been undone meanwhile.
            const cur = boardRef.current.tasks.find((t) => t.id === taskId);
            if (cur && shippable(cur.status) && cur.checks.every((c) => c.done)) {
              move(taskId, 'done');
            }
          }, 350);
        }
      }
    },
    [move],
  );

  const openAdd = useCallback((status: Status) => {
    setModal({ mode: 'add', status });
  }, []);

  const openEdit = useCallback((taskId: string) => {
    const task = boardRef.current.tasks.find((t) => t.id === taskId);
    if (task) setModal({ mode: 'edit', task });
  }, []);

  const handleSave = useCallback((task: Task) => {
    publishUserEdit((b) => {
      const exists = b.tasks.some((t) => t.id === task.id);
      return {
        ...b,
        tasks: exists
          ? b.tasks.map((t) => (t.id === task.id ? task : t))
          : [...b.tasks, task],
      };
    });
    setModal(null);
  }, []);

  /**
   * Soft delete (F11): the card moves to Cancelled, never a row delete. The
   * cancelled column is where a real delete can be asked for.
   */
  const handleDelete = useCallback(
    (taskId: string) => {
      const kill = () => {
        applyMove(taskId, 'cancelled');
        setModal(null);
      };
      setConfirm({
        title: 'Why reject this card?',
        body: 'The card moves to Cancelled. A short reason helps future similar-work checks.',
        confirmLabel: 'Kill with reason',
        destructive: true,
        inputLabel: 'Reason',
        inputPlaceholder: 'e.g. superseded by the SSO work',
        // 500 UTF-16 code units stay below the server's 2000-byte ceiling,
        // including the three-byte replacement for an unpaired surrogate.
        inputMaxLength: 500,
        inputRequired: true,
        secondaryLabel: 'Kill without a reason',
        onSecondary: kill,
        onConfirm: (reason) => {
          const request = killReasonRequest(taskId, reason);
          if (!request) return kill();
          void enqueueIntentBeforeMutation(
            () => outbox.enqueueTombstone(request.taskId, request.reason),
            kill,
            () => handleOutboxStatus({
              kind: 'blocked',
              message: 'the cancellation reason could not be stored; the card remains active',
            }),
          );
        },
      });
    },
    [applyMove, outbox, handleOutboxStatus],
  );

  const handleRestore = useCallback(
    (taskId: string) => {
      void removeIntentBeforeMutation(
        () => outbox.removeTombstone(taskId),
        () => applyMove(taskId, 'todo'),
        () => handleOutboxStatus({
          kind: 'blocked',
          message: 'the cancellation intent could not be removed; the card remains cancelled',
        }),
      );
    },
    [applyMove, outbox, handleOutboxStatus],
  );

  const purgeTask = useCallback((taskId: string) => {
    publishUserEdit((board) => ({
      ...board,
      tasks: board.tasks.filter((task) => task.id !== taskId),
    }));
    setModal(null);
  }, []);

  /** The one path to a real row delete — so it keeps its confirmation. */
  const handlePurge = useCallback((taskId: string) => {
    setConfirm({
      title: 'Delete this card permanently?',
      body: 'The card is removed from the board for good. This cannot be undone.',
      confirmLabel: 'Delete permanently',
      destructive: true,
      onConfirm: () => {
        void removeIntentBeforeMutation(
          () => outbox.removeTombstone(taskId),
          () => purgeTask(taskId),
          () => handleOutboxStatus({
            kind: 'blocked',
            message: 'the cancellation intent could not be removed; the card was not deleted',
          }),
        );
      },
    });
  }, [outbox, handleOutboxStatus, purgeTask]);

  const handleAddStories = useCallback((tasks: Task[]) => {
    if (tasks.length > 0) {
      publishUserEdit((b) => ({ ...b, tasks: [...b.tasks, ...tasks] }));
    }
    setShowAdr(false);
    setShowImport(false);
  }, []);

  const handleCommitLinks = useCallback(
    (req: RecordImportLinksRequest) => {
      void outbox
        .enqueueImportLinks(req)
        .then(() => drainAndReport(outbox, identity, handleOutboxStatus))
        .catch(() => handleOutboxStatus({
          kind: 'blocked',
          message: 'import provenance could not be stored locally',
        }));
    },
    [identity, outbox, handleOutboxStatus],
  );

  const handleExport = () => {
    const blob = new Blob([serialize(boardRef.current)], {
      type: 'text/markdown',
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'board.md';
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  const handleImport = async (e: ChangeEvent<HTMLInputElement>) => {
    const input = e.target;
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    try {
      const text = await file.text();
      const next = parse(text);
      setConfirm({
        title: 'Replace the current board?',
        body: `“${next.title}” carries ${taskCount(next.tasks.length)}. Everything on the board now is discarded, and this cannot be undone.`,
        confirmLabel: 'Replace board',
        destructive: true,
        onConfirm: () => publishUserEdit(next),
      });
    } catch {
      setConfirm({
        title: 'Import failed',
        body: 'Could not read that file as a board.',
      });
    }
  };

  // Combobox suggestions: server labels merged with tags already on the
  // board, so offline (or pre-fetch) still suggests everything in sight.
  const allLabels = useMemo(
    () => unionLabels(serverLabels, boardLabels(board)),
    [serverLabels, board],
  );
  const visibleBoard = useMemo(() => filterBoard(board, filter), [board, filter]);
  const aiEnabled =
    serverPresent &&
    aiSettings !== null &&
    (aiSettings.has_key || aiSettings.ai_base_url.trim() !== '');
  useEffect(() => {
    if (!aiEnabled || sources.length === 0) setShowImport(false);
  }, [aiEnabled, sources.length]);
  // The signal comes from the modal: an AI call takes seconds, so the user
  // must be able to abort it rather than only hide the spinner.
  const aiDraft = useMemo(
    () =>
      aiEnabled
        ? (req: AIStoryRequest, signal?: AbortSignal) =>
            aiStory(identity, req, signal)
        : undefined,
    [aiEnabled, identity],
  );

  // A dialog is open, so everything behind it is out of play: `inert` keeps
  // Tab (and the screen reader's cursor) inside the dialog rather than walking
  // out onto board controls that aria-modal has already hidden.
  const dialogOpen =
    modal !== null ||
    ship !== null ||
    showAdr ||
    showImport ||
    showSettings ||
    showReconnect ||
    confirm !== null;

  return (
    <>
      {/* Column flex shell: the header keeps its height, the board takes the
          rest of the viewport and scrolls inside its columns. */}
      <div className="app-shell" inert={dialogOpen}>
        <header className="app-header">
          <h1>kb</h1>
          <div className="hactions">
            {streak > 0 && <span className="streak">×{streak} shipped today</span>}
            {/* The name is what a person recognises; the full email stays as
                the tooltip, and the stored identity is untouched. */}
            <span className="who" title={identity.id}>
              {localName.trim() === '' ? displayName(identity) : localName.trim()}
            </span>
            {/* The dot is a picture of the sync state, not a live region: its
                content never changes, so swapping its aria-label announced
                nothing. The state is spoken by the live region below. */}
            <span
              className={`dot ${sync}`}
              title={SYNC_TITLE[sync]}
              role="img"
              aria-label={SYNC_TITLE[sync]}
            />
            {/* The way back from an expired session. The dialog opens itself
                once; this is what remains after it is dismissed, so the state
                is never a dead end. */}
            {sync === 'expired' && (
              <button
                type="button"
                className="reconnect"
                title="Restore the session so the board saves to the server again"
                onClick={() => setShowReconnect(true)}
              >
                Reconnect
              </button>
            )}
            <button
              type="button"
              aria-pressed={showCancelled}
              title="Show the cancelled column"
              onClick={() => {
                setShowCancelledFlag(!showCancelled);
                setShowCancelled(!showCancelled);
              }}
            >
              {showCancelled ? '☑' : '☐'} Cancelled
            </button>
            {aiEnabled && (
              <button
                type="button"
                title="Split an ADR into stories"
                onClick={() => setShowAdr(true)}
              >
                ✨ Split ADR
              </button>
            )}
            {aiEnabled && sources.length > 0 && (
              <button
                type="button"
                title="Import issues as reviewed AI-transformed cards"
                onClick={() => setShowImport(true)}
              >
                ⇪ Import issues
              </button>
            )}
            {/* Always present: the modal also carries the debug-overlay
                toggle, which is a local display preference — needing it is
                most likely exactly when no server is reachable. */}
            <button
              type="button"
              aria-label="Settings"
              title="Settings"
              onClick={() => setShowSettings(true)}
            >
              ⚙
            </button>
            <button type="button" onClick={handleExport}>
              Export
            </button>
            <button type="button" onClick={() => fileRef.current?.click()}>
              Import
            </button>
            <button type="button" onClick={onSignOut}>
              Sign out
            </button>
            <input
              ref={fileRef}
              type="file"
              accept=".md,.markdown,.txt,text/markdown,text/plain"
              hidden
              onChange={handleImport}
            />
          </div>
        </header>
        {notice && (
          <div className="notice" role="status">
            <span>{notice}</span>
            <button type="button" aria-label="Dismiss" onClick={dismissNotice}>
              ✕
            </button>
          </div>
        )}
        <div className="filterbar">
          <input
            type="search"
            className="filter-text"
            placeholder="Filter cards"
            aria-label="Filter cards by text"
            value={filter.text}
            onChange={(e) => {
              const text = e.target.value;
              setFilter((current) => ({ ...current, text }));
            }}
          />
          {filter.tags.map((tag) => (
            <button
              key={tag}
              type="button"
              className="ftag"
              aria-label={`Stop filtering by label ${tag}`}
              onClick={() => handleTagClick(tag)}
            >
              {tag} ✕
            </button>
          ))}
          {isFilterActive(filter) && (
            <>
              <button
                type="button"
                className="fclear"
                onClick={() => setFilter(emptyFilter())}
              >
                Clear
              </button>
              <output className="fcount">
                {visibleBoard.tasks.length} of {board.tasks.length} cards
              </output>
            </>
          )}
        </div>
        <BoardView
          board={visibleBoard}
          onMove={move}
          onTick={handleTick}
          onEdit={openEdit}
          onAdd={openAdd}
          showCancelled={showCancelled}
          onRestore={handleRestore}
          onPurge={handlePurge}
          onTagClick={handleTagClick}
          announce={announce}
        />
        {/* In the shell's flow rather than fixed over it: the hint's height
            grows with the text size, and a fixed one printed itself over the
            bottom row of the last card as soon as it did. */}
        <div className="foot">
          drag cards between columns (⠿ on touch) · or focus a card and press
          Space to pick it up, arrows to move, Enter to drop · tap ▾ to expand
          checklists
        </div>
      </div>
      {/* The app's one polite live region: every board move, every sync state
          change, every outcome that only showed on screen. Outside the shell
          on purpose — the shell goes `inert` whenever a dialog opens, which
          takes its whole subtree out of the accessibility tree, and a save
          failure while a card is open is exactly when this has to be heard. */}
      <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">
        <span key={said.seq}>{said.text}</span>
      </div>
      {modal && (
        <CardModal
          // allow: SIZE_OK - A5 needs authenticated advisory lookup; decomposing BoardApp is out of scope.
          identity={identity}
          state={modal}
          canonicalTaskId={
            modal.mode === 'edit'
              ? canonicalTaskIDs.get(modal.task.id)
              : undefined
          }
          labels={allLabels}
          aiDraft={aiDraft}
          onSave={handleSave}
          onDelete={handleDelete}
          onClose={() => setModal(null)}
        />
      )}
      {ship && (
        <ShipDialog
          warning={ship.warning}
          title={ship.title}
          onShip={shipAnyway}
          onTickAll={shipTickingAll}
          onCancel={() => setShip(null)}
        />
      )}
      {showAdr && aiEnabled && (
        <AdrModal
          sources={sources}
          onSplit={(req, signal) => aiStories(identity, req, signal)}
          onAdd={handleAddStories}
          onClose={() => setShowAdr(false)}
        />
      )}
      {showImport && aiEnabled && sources.length > 0 && (
        <ImportModal
          sources={sources}
          onPreview={(req, signal) => importPreview(identity, req, signal)}
          onAdd={handleAddStories}
          onCommitLinks={handleCommitLinks}
          onClose={() => setShowImport(false)}
        />
      )}
      {showSettings && (
        <SettingsModal
          identity={identity}
          serverPresent={serverPresent}
          debug={debug}
          onDebugChange={(on) => {
            setDebugEnabled(on);
            setDebug(on);
          }}
          displayNameValue={localName}
          onDisplayNameChange={(name) => {
            saveLocalDisplayName(name);
            setLocalName(name);
          }}
          onClose={() => setShowSettings(false)}
          onSaved={setAiSettings}
        />
      )}
      {showReconnect && (
        <ReconnectModal
          identity={identity}
          onIdentity={(next) => {
            setShowReconnect(false);
            // Not 'ok' — nothing has synced yet. The load effect re-runs on the
            // new identity and reports what actually happens. Said out loud
            // here because that transition is 'off' → 'ok', which the sync
            // announcer stays quiet about: only a recovery from a reported
            // failure is worth interrupting for, and this is the one case
            // where the failure was reported but the recovery is not.
            setSync('off');
            announce('reconnected — the board is saving to the server again');
            onIdentity(next);
          }}
          onSignOut={onSignOut}
          onClose={() => setShowReconnect(false)}
        />
      )}
      {confirm && (
        <ConfirmDialog
          title={confirm.title}
          body={confirm.body}
          confirmLabel={confirm.confirmLabel}
          destructive={confirm.destructive}
          inputLabel={confirm.inputLabel}
          inputPlaceholder={confirm.inputPlaceholder}
          inputMaxLength={confirm.inputMaxLength}
          inputRequired={confirm.inputRequired}
          secondaryLabel={confirm.secondaryLabel}
          onSecondary={confirm.onSecondary}
          onConfirm={confirm.onConfirm}
          onClose={() => setConfirm(null)}
        />
      )}
      <Confetti />
      {debug && (
        <DebugOverlay
          inert={dialogOpen}
          onClose={() => {
            setDebugEnabled(false);
            setDebug(false);
          }}
        />
      )}
    </>
  );
}
