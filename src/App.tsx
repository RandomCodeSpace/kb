import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import type { Check, Status, Task, TaskDraft } from './lib/model';
import type { BoardFilter } from './lib/filter';
import { emptyFilter, filterBoard, filterFromSearch, filterToSearch, isFilterActive, toggleTag } from './lib/filter';
import { parse, serialize } from './lib/markdown';
import { bumpShipped, shipKey, shippedToday, unshipToday } from './lib/store';
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
import type {
  AISettings,
  AIStoryRequest,
  ForgeSource,
  RecordImportLinksRequest,
} from './lib/api';
import {
  aiStories,
  aiStory,
  createTask,
  deleteTask,
  detect,
  getIntegrations,
  getLabels,
  getSettings,
  importPreview,
  killReasonRequest,
  listTasks,
  patchTask,
  recordImportLinks,
  recordTombstone,
  replaceBoard,
} from './lib/api';
import { boardLabels, unionLabels } from './lib/labels';
import { burst } from './lib/confetti';
import { AdrModal } from './components/AdrModal';
import {
  BoardView,
  fullColumnIndex,
  movedAnnouncement,
  moveTask,
  setShowCancelledFlag,
  showCancelledFlag,
} from './components/Board';
import { CardModal } from './components/CardModal';
import type { ModalState } from './components/CardModal';
import type { CardSave } from './components/CardEditor';
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

/**
 * A move to Done held back by the F9/F10 confirmation. The checklist travels
 * with it so "tick everything and ship" is one patch against what the card
 * looked like when the question was asked.
 */
interface ShipPrompt {
  taskId: string;
  index: number;
  title: string;
  checks: Check[];
  warning: ShipWarning;
}

/**
 * The board is the server's; the export file only needs a heading, and the
 * per-task API carries no board title to put there.
 */
const EXPORT_TITLE = 'kb';

export const NO_SERVER_NOTICE =
  'No kb server answered, so there is no board to show. Start the server, then reload this page.';

/**
 * Shown when a refresh found the open card gone from the server — deleted from
 * the CLI, or by another tab. Silently discarding what someone was typing is
 * the part that must not happen unannounced.
 */
export const CARD_GONE_NOTICE =
  'The card you had open is no longer on the server, so it was closed. Any unsaved text in it was not kept.';

// Without these, a failed config load just makes the AI/import buttons vanish
// from the header with nothing saying why.
export const SETTINGS_LOAD_NOTICE =
  'AI settings could not be loaded, so AI actions are hidden. Open Settings or reload to retry.';
export const INTEGRATIONS_LOAD_NOTICE =
  'Forge sources could not be loaded, so issue import is hidden. Open Settings or reload to retry.';

/**
 * Why a request failed, in the terms the person can act on. The server's own
 * words are kept — they name the field it would not take, or the guard that
 * stopped a move to Done — while an expired session is named as such, because
 * the server sends no body with a 401.
 */
export function failureDetail(err: unknown): string {
  if (err instanceof ReauthRequiredError) return 'the session expired';
  return err instanceof Error ? err.message.trim() : '';
}

/**
 * A write the server refused or never received.
 *
 * `boardMatchesServer` is what makes the second sentence true: it is only
 * "what the server has" when the board was read back after the failure. When
 * that read failed too, the board is the last one the server confirmed, and
 * saying otherwise would vouch for a screen nobody has checked.
 */
export function mutationNotice(err: unknown, boardMatchesServer = true): string {
  const detail = failureDetail(err);
  return `That change was not saved${detail === '' ? '' : `: ${detail}`}. ${
    boardMatchesServer
      ? 'The board below is what the server has.'
      : 'The board below is the last one the server confirmed, so it may be out of date.'
  }`;
}

/**
 * A read that did not come back. Nothing was lost, so this never claims a
 * change was refused — but what is on screen is no longer known to be the
 * server's.
 */
export function readNotice(err: unknown): string {
  const detail = failureDetail(err);
  return `The board could not be read from the server${
    detail === '' ? '' : `: ${detail}`
  }. What you see may be out of date.`;
}

/**
 * Which reviewed drafts never became cards. Named one by one because the modal
 * that held them is gone by the time this is read, and the rest of the batch
 * did land — so "that change was not saved" would be wrong twice over.
 */
export function addStoriesNotice(missed: readonly string[], err: unknown): string {
  const detail = failureDetail(err);
  const heading =
    missed.length === 1
      ? 'One card was not created'
      : `${missed.length} cards were not created`;
  return `${heading}${detail === '' ? '' : `: ${detail}`}. Not saved: ${
    missed.join('; ')
  }. Every other card in that batch was.`;
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

/**
 * How a read of the board ended. 'stale' is a read a newer one has already
 * superseded: it published nothing and must not be reported as either outcome.
 */
type ReadOutcome =
  | { kind: 'ok' }
  | { kind: 'stale' }
  | { kind: 'failed'; error: unknown };

const SYNC_TITLE: Record<SyncState, string> = {
  off: 'no server — nothing loaded',
  ok: 'synced to server',
  error: 'the last change did not reach the server',
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
      // while switching Microsoft accounts starts from that account's board.
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

/** How many cards a column holds — where a card with no stated slot lands. */
function columnLength(tasks: readonly Task[], to: Status): number {
  return tasks.filter((t) => t.status === to).length;
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

function BoardApp({ identity, onIdentity, onSignOut }: Readonly<BoardAppProps>) {
  const ns = identityNamespace(identity);
  // The board is the server's. Nothing here is persisted locally, and there is
  // no board at all until GET /api/tasks answers.
  const [tasks, setTasks] = useState<Task[]>([]);
  const tasksRef = useRef(tasks);
  // Display-only narrowing: the filter never touches the task list, so moves
  // and edits keep operating on the full set by id. The URL query string is
  // its persistence: seeded from it on mount, mirrored back on every change,
  // so a refresh or a shared link restores the same view.
  const [filter, setFilter] = useState<BoardFilter>(() =>
    filterFromSearch(window.location.search),
  );
  useEffect(() => {
    const next = filterToSearch(filter, window.location.search);
    if (next !== window.location.search) {
      window.history.replaceState(
        window.history.state,
        '',
        window.location.pathname + next + window.location.hash,
      );
    }
  }, [filter]);
  const handleTagClick = useCallback(
    (tag: string) => setFilter((current) => toggleTag(current, tag)),
    [],
  );
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
  const dismissNotice = useCallback(() => {
    applyNotice({ type: 'dismiss' });
  }, [applyNotice]);
  const fileRef = useRef<HTMLInputElement>(null);
  // Newest read wins. Every write bumps the generation first, so a refetch
  // still in flight when the next write starts is discarded rather than
  // flashing the board back a step; that write's own refetch carries both.
  const loadGen = useRef(0);
  // The last board the server actually handed over. Where an optimistic change
  // goes back to when the refetch that should have corrected it never arrived:
  // without it the change stays on screen looking saved.
  const serverBoard = useRef<Task[]>([]);

  const publish = useCallback((next: Task[]) => {
    tasksRef.current = next;
    setTasks(next);
  }, []);

  /** The dot, and the reconnect prompt. Says nothing on its own. */
  const noteFailure = useCallback((err: unknown) => {
    setSync(err instanceof ReauthRequiredError ? 'expired' : 'error');
  }, []);

  /**
   * Read the board back from the server; the only way tasks ever change.
   * 'stale' means a newer read has already superseded this one, which is the
   * one outcome no caller may act on — that newer read owns the board.
   */
  const refresh = useCallback(async (): Promise<ReadOutcome> => {
    const gen = loadGen.current + 1;
    loadGen.current = gen;
    try {
      const next = await listTasks(identity);
      if (gen !== loadGen.current) return { kind: 'stale' };
      serverBoard.current = next;
      publish(next);
      setSync('ok');
      return { kind: 'ok' };
    } catch (err) {
      noteFailure(err);
      return { kind: 'failed', error: err };
    }
  }, [identity, publish, noteFailure]);

  /**
   * Run one write, then read the board back. Refetching after every mutation
   * is what keeps the display honest about a server that may also be taking
   * writes from the CLI, MCP, or another tab — and it is what puts an
   * optimistic local move back where the server actually left it.
   *
   * `optimistic` is that local move. It belongs here rather than at the call
   * site because this is the only place that learns whether the refetch came
   * back: a refused write whose refetch also failed has to be undone from the
   * last confirmed board, or the change sits there looking saved while the
   * notice says it was not.
   */
  const mutate = useCallback(async (
    write: () => Promise<unknown>,
    optimistic?: (current: Task[]) => Task[],
  ): Promise<boolean> => {
    loadGen.current += 1;
    if (optimistic) publish(optimistic(tasksRef.current));
    let writeError: unknown = null;
    let ok = true;
    try {
      await write();
    } catch (err) {
      ok = false;
      writeError = err;
      noteFailure(err);
    }
    const read = await refresh();
    if (!ok && read.kind === 'failed' && optimistic) {
      publish(serverBoard.current);
    }
    if (!ok) {
      reportNotice(mutationNotice(writeError, read.kind === 'ok'));
    } else if (read.kind === 'failed' && !(read.error instanceof ReauthRequiredError)) {
      reportNotice(readNotice(read.error));
    }
    return ok;
  }, [noteFailure, refresh, publish, reportNotice]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const present = await detect();
      if (cancelled) return;
      setServerPresent(present);
      if (!present) {
        setSync('off');
        reportNotice(NO_SERVER_NOTICE);
        return;
      }
      const read = await refresh();
      // An expired session says so through the reconnect dialog, which opens
      // itself; a notice repeating it would only be in the way.
      if (read.kind === 'failed' && !(read.error instanceof ReauthRequiredError)) {
        reportNotice(readNotice(read.error));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [refresh, reportNotice]);

  // A card that is no longer on the server cannot be saved or moved, so the
  // editor holding it has to go — said out loud, because whatever was typed
  // into it goes too.
  useEffect(() => {
    if (modal?.mode !== 'edit') return;
    if (tasks.some((t) => t.id === modal.task.id)) return;
    setModal(null);
    reportNotice(CARD_GONE_NOTICE);
  }, [tasks, modal, reportNotice]);

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
      .catch((err) => {
        if (!cancelled && !(err instanceof ReauthRequiredError)) {
          reportNotice(SETTINGS_LOAD_NOTICE);
        }
      });
    getIntegrations(identity)
      .then((nextSources) => {
        if (!cancelled) setSources(nextSources);
      })
      .catch((err) => {
        if (!cancelled && !(err instanceof ReauthRequiredError)) {
          reportNotice(INTEGRATIONS_LOAD_NOTICE);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [serverPresent, identity, showSettings, reportNotice]);

  // The board refreshes on mount and after every local mutation, which leaves
  // CLI/MCP/other-tab writes invisible until the user touches something. A
  // return to the tab is the natural moment to catch up; throttled so focus
  // flapping between windows does not hammer the server.
  const lastWake = useRef(0);
  useEffect(() => {
    if (!serverPresent) return;
    const wake = () => {
      if (document.visibilityState !== 'visible') return;
      const now = Date.now();
      if (now - lastWake.current < 5000) return;
      lastWake.current = now;
      void refresh();
    };
    window.addEventListener('focus', wake);
    document.addEventListener('visibilitychange', wake);
    return () => {
      window.removeEventListener('focus', wake);
      document.removeEventListener('visibilitychange', wake);
    };
  }, [serverPresent, refresh]);

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
  // browser session starts looking signed in and 401s on every request.
  // Dismissing leaves the header's Reconnect button; this only runs on a
  // change into 'expired', so it asks once rather than reopening over the
  // board.
  useEffect(() => {
    if (sync === 'expired') setShowReconnect(true);
  }, [sync]);

  /**
   * Commit a move: PATCH the destination status and slot, then refetch.
   * `index` is the slot within `to`, counted over that column's other cards —
   * the same quantity the server's `index` means. It is always sent for a
   * same-column drop, because a status-only patch there is a move to the end
   * that would reset the card's age.
   */
  const applyMove = useCallback(
    (taskId: string, to: Status, index?: number, force = false) => {
      const prev = tasksRef.current.find((t) => t.id === taskId);
      if (!prev) return;
      if (prev.status === to && index === undefined) return;
      const at = index ?? columnLength(tasksRef.current, to);
      // Where the card ends up, by the same arithmetic the server uses — said
      // out loud below, because a move is otherwise a silent visual event.
      const others = tasksRef.current.filter(
        (t) => t.status === to && t.id !== taskId,
      ).length;
      const slot = Math.max(0, Math.min(at, others));
      // Optimistic: the drop lands where it was released and stays there
      // unless the refetch says otherwise — or, if the refetch never lands,
      // until mutate puts the board back where the server last had it.
      void mutate(
        () => patchTask(identity, taskId, {
          status: to,
          index: at,
          ...(force ? { force: true } : {}),
        }),
        (current) => moveTask(current, taskId, to, at, new Date().toISOString()),
      ).then((ok) => {
        if (!ok) return;
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
      });
    },
    [identity, mutate, ns, announce],
  );

  /**
   * Move a card, warning first when shipping it deserves a second look
   * (unticked checklist items, or a blocked flag — F9/F10). The same guard
   * runs on the server, so a confirmed ship goes out forced; anything the
   * server refuses beyond that (an open blocker) comes back as a notice.
   */
  const move = useCallback(
    (taskId: string, to: Status, index?: number) => {
      const task = tasksRef.current.find((t) => t.id === taskId);
      if (!task) return;
      if (to === 'done' && task.status !== 'done') {
        const warning = shipWarning(task);
        if (warning) {
          setShip({
            taskId,
            index: index ?? columnLength(tasksRef.current, to),
            title: task.title,
            checks: task.checks,
            warning,
          });
          return;
        }
      }
      applyMove(taskId, to, index);
    },
    [applyMove],
  );

  /** Board moves arrive as a slot in the *rendered* column; translate it. */
  const moveVisible = useCallback(
    (taskId: string, to: Status, index: number) => {
      move(
        taskId,
        to,
        fullColumnIndex(
          tasksRef.current,
          filterBoard({ title: EXPORT_TITLE, tasks: tasksRef.current }, filter).tasks,
          taskId,
          to,
          index,
        ),
      );
    },
    [move, filter],
  );

  /** Ship the held-back card as it stands. */
  const shipAnyway = useCallback(() => {
    if (!ship) return;
    applyMove(ship.taskId, 'done', ship.index, true);
    setShip(null);
  }, [ship, applyMove]);

  /**
   * Ship the held-back card, ticking every remaining item first. One patch:
   * the server ticks, guards, and moves in a single transaction.
   */
  const shipTickingAll = useCallback(() => {
    if (!ship) return;
    setShip(null);
    const checks = ship.checks.map((c) => ({ ...c, done: true }));
    void mutate(
      () => patchTask(identity, ship.taskId, {
        checks,
        status: 'done',
        index: ship.index,
        force: true,
      }),
      (current) => current.map(
        (t) => (t.id === ship.taskId ? { ...t, checks } : t),
      ),
    ).then((ok) => {
      if (!ok) return;
      setStreak(bumpShipped(ns, shipKey(ship)));
      burst(window.innerWidth / 2, window.innerHeight / 2, 70);
    });
  }, [ship, identity, mutate, ns]);

  const handleTick = useCallback(
    (taskId: string, checkIdx: number, pos: { x: number; y: number }) => {
      const task = tasksRef.current.find((t) => t.id === taskId);
      const check = task?.checks[checkIdx];
      if (!task || !check) return;
      const turningOn = !check.done;
      const checks = task.checks.map((c, i) =>
        i === checkIdx ? { ...c, done: !c.done } : c,
      );
      void mutate(
        () => patchTask(identity, taskId, { checks }),
        (current) => current.map((t) => (t.id === taskId ? { ...t, checks } : t)),
      );
      if (!turningOn) return;
      burst(pos.x, pos.y, 14);
      // A cancelled card is out of play: finishing its checklist must not
      // resurrect it into Done.
      if (!checks.every((c) => c.done) || !shippable(task.status)) return;
      setTimeout(() => {
        // Re-read at fire time from the refetched board: a tick may have been
        // undone, or the server may have refused this one.
        const cur = tasksRef.current.find((t) => t.id === taskId);
        if (cur && shippable(cur.status) && cur.checks.every((c) => c.done)) {
          move(taskId, 'done');
        }
      }, 350);
    },
    [identity, mutate, move],
  );

  const openAdd = useCallback((status: Status) => {
    setModal({ mode: 'add', status });
  }, []);

  const openEdit = useCallback((taskId: string) => {
    const task = tasksRef.current.find((t) => t.id === taskId);
    if (task) setModal({ mode: 'edit', task });
  }, []);

  // The editor closes only after the write lands: a refused save keeps the
  // typed content on screen with the notice, instead of destroying it.
  const saving = useRef(false);
  const handleSave = useCallback((save: CardSave) => {
    if (saving.current) return;
    saving.current = true;
    void (async () => {
      try {
        const ok = await mutate(() =>
          save.mode === 'add'
            ? createTask(identity, save.draft)
            : patchTask(identity, save.taskId, save.patch),
        );
        if (ok) setModal(null);
      } finally {
        saving.current = false;
      }
    })();
  }, [identity, mutate]);

  // Escape, backdrop, and Cancel all land here: an edited card asks before
  // its typing is discarded; an untouched one just closes.
  const editorDirty = useRef(false);
  const markEditorDirty = useCallback(() => {
    editorDirty.current = true;
  }, []);
  useEffect(() => {
    editorDirty.current = false;
  }, [modal]);
  const requestCloseEditor = useCallback(() => {
    if (!editorDirty.current) {
      setModal(null);
      return;
    }
    setConfirm({
      title: 'Discard unsaved changes?',
      body: 'This card was edited but not saved. Closing it throws the edits away.',
      confirmLabel: 'Discard changes',
      destructive: true,
      onConfirm: () => setModal(null),
    });
  }, []);

  /**
   * Soft delete (F11): the card moves to Cancelled, never a row delete. The
   * cancelled column is where a real delete can be asked for.
   *
   * The reason follows the move rather than preceding it: the server only
   * accepts a tombstone for a task that is already cancelled. A reason that
   * fails to land therefore leaves a cancelled card with none, which is what
   * the notice says.
   */
  const handleDelete = useCallback(
    (taskId: string) => {
      const kill = (reason?: string) => {
        setModal(null);
        void mutate(async () => {
          await patchTask(identity, taskId, { status: 'cancelled' });
          if (reason === undefined) return;
          await recordTombstone(identity, taskId, reason);
        });
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
        onSecondary: () => kill(),
        onConfirm: (reason) => {
          const request = killReasonRequest(taskId, reason);
          kill(request?.reason);
        },
      });
    },
    [identity, mutate],
  );

  // The server drops the tombstone itself on any move out of Cancelled.
  const handleRestore = useCallback(
    (taskId: string) => applyMove(taskId, 'todo'),
    [applyMove],
  );

  /** The one path to a real row delete — so it keeps its confirmation. */
  const handlePurge = useCallback((taskId: string) => {
    setConfirm({
      title: 'Delete this card permanently?',
      body: 'The card is removed from the board for good, along with its comments and links. This cannot be undone.',
      confirmLabel: 'Delete permanently',
      destructive: true,
      onConfirm: () => {
        setModal(null);
        void mutate(() => deleteTask(identity, taskId));
      },
    });
  }, [identity, mutate]);

  /**
   * Create every reviewed draft, then read the board back once. One refusal
   * must not swallow the rest: the modal holding the reviewed selection is
   * already closed, so a draft that is never attempted is a draft nobody can
   * get back. Every one is tried, and the notice names each that did not land.
   *
   * Resolves with the titles that were created, so the caller can journal
   * provenance for those and only those.
   */
  const handleAddStories = useCallback(async (
    drafts: TaskDraft[],
  ): Promise<ReadonlySet<string>> => {
    setShowAdr(false);
    setShowImport(false);
    const created = new Set<string>();
    if (drafts.length === 0) return created;
    loadGen.current += 1;
    const missed: string[] = [];
    let firstError: unknown = null;
    for (const draft of drafts) {
      // Nothing will be accepted until the session is restored, so the rest go
      // straight into the notice rather than through N more 401s.
      if (firstError instanceof ReauthRequiredError) {
        missed.push(draft.title);
        continue;
      }
      try {
        await createTask(identity, draft);
        created.add(draft.title);
      } catch (err) {
        if (firstError === null) firstError = err;
        noteFailure(err);
        missed.push(draft.title);
      }
    }
    await refresh();
    if (missed.length > 0) reportNotice(addStoriesNotice(missed, firstError));
    return created;
  }, [identity, noteFailure, refresh, reportNotice]);

  const handleCommitLinks = useCallback(
    (req: RecordImportLinksRequest) => {
      void recordImportLinks(identity, req).catch((err: unknown) => {
        // An expired session is not a provenance problem — every other write
        // is failing too — so it has to reach the reconnect prompt like any
        // other refused write rather than stopping at a one-off notice.
        noteFailure(err);
        const detail = failureDetail(err);
        reportNotice(
          `Import provenance was not recorded: ${
            detail === '' ? 'the request failed' : detail
          }`,
        );
      });
    },
    [identity, noteFailure, reportNotice],
  );

  const handleExport = () => {
    const blob = new Blob(
      [serialize({ title: EXPORT_TITLE, tasks: tasksRef.current })],
      { type: 'text/markdown' },
    );
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'board.md';
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  /**
   * Replace the whole board from a markdown file. The one write with no
   * per-task equivalent: PUT /api/board so "everything now is discarded" stays
   * a single step the server either takes or does not.
   */
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
        onConfirm: () => {
          void mutate(() => replaceBoard(identity, text));
        },
      });
    } catch {
      setConfirm({
        title: 'Import failed',
        body: 'Could not read that file as a board.',
      });
    }
  };

  const board = useMemo(
    () => ({ title: EXPORT_TITLE, tasks }),
    [tasks],
  );
  // Combobox suggestions: server labels merged with tags already on the
  // board, so a slow label fetch still suggests everything in sight.
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
          onMove={moveVisible}
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
          labels={allLabels}
          aiDraft={aiDraft}
          onSave={handleSave}
          onDelete={handleDelete}
          onClose={requestCloseEditor}
          onDirty={markEditorDirty}
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
            // Not 'ok' — nothing has been read back yet. The load effect
            // re-runs on the new identity and reports what actually happens.
            // Said out loud here because that transition is 'off' → 'ok',
            // which the sync announcer stays quiet about: only a recovery from
            // a reported failure is worth interrupting for, and this is the
            // one case where the failure was reported but the recovery is not.
            setSync('off');
            announce('reconnected — the board is loading from the server again');
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
