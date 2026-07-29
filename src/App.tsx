import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import type { Board, Status, Task } from './lib/model';
import { parse, serialize } from './lib/markdown';
import {
  bumpShipped,
  loadDirty,
  LocalStore,
  setDirty,
  shipKey,
  shippedToday,
  unshipToday,
} from './lib/store';
import type { Identity } from './lib/auth';
import {
  clearIdentity,
  displayName,
  loadIdentity,
  ReauthRequiredError,
  sanitizeUser,
  saveIdentity,
} from './lib/auth';
import { RemoteStore } from './lib/remote';
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
  recordImportLinks,
  recordTombstone,
} from './lib/api';
import { boardLabels, unionLabels } from './lib/labels';
import { acknowledgedTombstones } from './lib/graveyard';
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

const SYNC_TITLE: Record<SyncState, string> = {
  off: 'sync off — local only',
  ok: 'synced to server',
  error: 'last save to server failed',
  expired: 'session expired — reconnect to save to the server',
};

export default function App() {
  const [identity, setIdentity] = useState<Identity | null>(() => loadIdentity());

  const adoptIdentity = (i: Identity) => {
    saveIdentity(i);
    setIdentity(i);
  };

  if (!identity) {
    return <IdentityGate onIdentity={adoptIdentity} />;
  }
  return (
    <BoardApp
      // Keyed on the id alone: restoring a session replaces the identity
      // object but is the same person, and remounting would throw away the
      // board state and any open card.
      key={identity.id}
      identity={identity}
      onIdentity={adoptIdentity}
      onSignOut={() => {
        clearIdentity();
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

function BoardApp({ identity, onIdentity, onSignOut }: BoardAppProps) {
  const ns = sanitizeUser(identity.id);
  const store = useMemo(() => new LocalStore(ns), [ns]);
  const remote = useMemo(() => new RemoteStore(), []);
  // Seeding clears the dirty flag (see LocalStore.loadOrSeed), so a demo board
  // is never pushed over a board the server already has.
  const [board, setBoard] = useState<Board>(() => store.loadOrSeed().board);
  const [modal, setModal] = useState<ModalState | null>(null);
  const [ship, setShip] = useState<ShipPrompt | null>(null);
  const [streak, setStreak] = useState<number>(() => shippedToday(ns));
  const [sync, setSync] = useState<SyncState>('off');
  const [serverPresent, setServerPresent] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
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
  const fileRef = useRef<HTMLInputElement>(null);
  const boardRef = useRef(board);
  boardRef.current = board;
  // Browser task ids never cross the markdown wire. Reasons wait here until
  // the cancelling PUT acknowledges the corresponding SQLite task id.
  const pendingTombstonesRef = useRef(new Map<string, string>());
  const drainTombstones = useCallback(
    (pushed: Board, taskIDs: ReadonlyMap<string, string>) => {
      for (const ready of acknowledgedTombstones(
        pendingTombstonesRef.current,
        pushed,
        taskIDs,
      )) {
        // Best-effort means one POST attempt per acknowledged decision. Remove
        // it first so a later unrelated board save cannot duplicate the call.
        pendingTombstonesRef.current.delete(ready.clientTaskId);
        void recordTombstone(identity, ready.serverTaskId, ready.reason);
      }
    },
    [identity],
  );
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
  const adopt = useCallback((next: Board) => {
    cleanBoardRef.current = next;
    if (openWorkRef.current) setNotice(REFRESH_NOTICE);
    setModal(null);
    setShip(null);
    setBoard(next);
  }, []);

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
  const carryMerged = useCallback(
    (sent: Board, pushed: Board) => {
      if (pushed === sent) return;
      if (boardRef.current === sent) {
        adopt(pushed);
        return;
      }
      // The merge keeps our own tasks (ids and all) and appends the server's,
      // which parse() gave fresh ids — so an unknown id is a carried-over task.
      const ours = new Set(sent.tasks.map((t) => t.id));
      const extra = pushed.tasks.filter((t) => !ours.has(t.id));
      if (extra.length === 0) return;
      setBoard((b) => ({ ...b, tasks: [...b.tasks, ...extra] }));
    },
    [adopt],
  );

  useEffect(() => {
    let cancelled = false;
    // Local edits that never reached the server win over the remote copy:
    // push them instead of silently adopting (and destroying) newer local work.
    const pushLocal = () => {
      syncOnRef.current = true;
      const gen = editGenRef.current;
      const sent = boardRef.current;
      remote.saveRemote(
        identity,
        sent,
        (err) => {
          if (!cancelled) onSaveError(err);
        },
        (pushed, taskIDs) => {
          // Only an ack for the newest edit may clear the dirty flag.
          const fresh = editGenRef.current === gen;
          if (fresh) setDirty(ns, false);
          if (cancelled) return;
          drainTombstones(pushed, taskIDs);
          // A 409 merge wrote a different board than we sent — that is now
          // the server's state, so it becomes ours. Unconditionally: the
          // merged-in tasks must survive a newer edit too, or the next save
          // deletes them.
          carryMerged(sent, pushed);
          if (fresh) setSync('ok');
        },
      );
    };
    void (async () => {
      const present = await remote.detect();
      if (cancelled || !present) return;
      setServerPresent(true);
      let remoteBoard: Board | null = null;
      if (!loadDirty(ns)) {
        try {
          remoteBoard = await remote.loadRemote(identity);
        } catch (err) {
          // Server present but board fetch failed — keep the local board,
          // report the failure, and do not enable autosave over broken auth.
          if (!cancelled) onSaveError(err);
          return;
        }
      }
      if (cancelled) return;
      // Re-check dirtiness after the fetch: an edit may have landed while the
      // remote copy was in flight (boardRef catches commits whose save effect
      // has not flushed yet).
      if (loadDirty(ns) || boardRef.current !== cleanBoardRef.current) {
        pushLocal();
        return;
      }
      if (remoteBoard) adopt(remoteBoard);
      syncOnRef.current = true;
      setSync('ok');
    })();
    return () => {
      cancelled = true;
    };
  }, [
    identity,
    remote,
    ns,
    onSaveError,
    adopt,
    carryMerged,
    drainTombstones,
  ]);

  useEffect(() => {
    store.save(board);
    if (board === cleanBoardRef.current) return; // initial load / server adoption
    editGenRef.current += 1;
    const gen = editGenRef.current;
    setDirty(ns, true);
    if (!syncOnRef.current) return;
    remote.saveRemoteDebounced(
      identity,
      board,
      onSaveError,
      (pushed, taskIDs) => {
        drainTombstones(pushed, taskIDs);
        // After a 409 merge the board that reached the server carries tasks we
        // have never seen; take them or the next save deletes them again. This
        // happens even when the ack is stale, because RemoteStore has already
        // committed to the merged version — the next PUT would carry a matching
        // If-Match and drop those tasks with no conflict and no error.
        carryMerged(board, pushed);
        // A stale ack (a newer edit exists) must not clear the dirty flag the
        // newer edit depends on for crash/offline safety.
        if (editGenRef.current !== gen) return;
        setDirty(ns, false);
        setSync('ok');
      },
    );
  }, [
    board,
    store,
    remote,
    identity,
    ns,
    onSaveError,
    carryMerged,
    drainTombstones,
  ]);

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
      pendingTombstonesRef.current.clear();
      remote.cancel();
    };
  }, [remote]);

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
      setBoard((b) => {
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
    setBoard((b) => ({
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
      setBoard((b) => ({
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
    setBoard((b) => {
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
          // Move first: the board stays responsive and starts its PUT before
          // the deliberately best-effort graveyard annotation is queued.
          kill();
          const request = killReasonRequest(taskId, reason);
          if (request) {
            pendingTombstonesRef.current.set(
              request.taskId,
              request.reason,
            );
          }
        },
      });
    },
    [applyMove],
  );

  const handleRestore = useCallback(
    (taskId: string) => {
      pendingTombstonesRef.current.delete(taskId);
      applyMove(taskId, 'todo');
    },
    [applyMove],
  );

  /** The one path to a real row delete — so it keeps its confirmation. */
  const handlePurge = useCallback((taskId: string) => {
    setConfirm({
      title: 'Delete this card permanently?',
      body: 'The card is removed from the board for good. This cannot be undone.',
      confirmLabel: 'Delete permanently',
      destructive: true,
      onConfirm: () => {
        pendingTombstonesRef.current.delete(taskId);
        setBoard((b) => ({ ...b, tasks: b.tasks.filter((t) => t.id !== taskId) }));
        setModal(null);
      },
    });
  }, []);

  const handleAddStories = useCallback((tasks: Task[]) => {
    if (tasks.length > 0) {
      setBoard((b) => ({ ...b, tasks: [...b.tasks, ...tasks] }));
    }
    setShowAdr(false);
    setShowImport(false);
  }, []);

  const handleCommitLinks = useCallback(
    (req: RecordImportLinksRequest) => {
      void recordImportLinks(identity, req);
    },
    [identity],
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
        onConfirm: () => setBoard(next),
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
              {displayName(identity)}
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
            <button type="button" aria-label="Dismiss" onClick={() => setNotice(null)}>
              ✕
            </button>
          </div>
        )}
        <BoardView
          board={board}
          onMove={move}
          onTick={handleTick}
          onEdit={openEdit}
          onAdd={openAdd}
          showCancelled={showCancelled}
          onRestore={handleRestore}
          onPurge={handlePurge}
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
