import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import type { Board, Status, Task } from './lib/model';
import { parse, serialize } from './lib/markdown';
import { bumpShipped, loadDirty, LocalStore, seedBoard, setDirty, shippedToday } from './lib/store';
import type { Identity } from './lib/auth';
import { clearIdentity, loadIdentity, ReauthRequiredError, sanitizeUser, saveIdentity } from './lib/auth';
import { RemoteStore } from './lib/remote';
import type { AISettings, AIStoryRequest } from './lib/api';
import { aiStory, getLabels, getSettings } from './lib/api';
import { boardLabels, unionLabels } from './lib/labels';
import { burst } from './lib/confetti';
import { BoardView } from './components/Board';
import { CardModal } from './components/CardModal';
import type { ModalState } from './components/CardModal';
import { Confetti } from './components/Confetti';
import { IdentityGate } from './components/IdentityGate';
import { SettingsModal } from './components/SettingsModal';

type SyncState = 'off' | 'ok' | 'error' | 'expired';

const SYNC_TITLE: Record<SyncState, string> = {
  off: 'sync off — local only',
  ok: 'synced to server',
  error: 'last save to server failed',
  expired: 'session expired — sign out and sign in again',
};

export default function App() {
  const [identity, setIdentity] = useState<Identity | null>(() => loadIdentity());

  if (!identity) {
    return (
      <IdentityGate
        onIdentity={(i) => {
          saveIdentity(i);
          setIdentity(i);
        }}
      />
    );
  }
  return (
    <BoardApp
      key={identity.id}
      identity={identity}
      onSignOut={() => {
        clearIdentity();
        setIdentity(null);
      }}
    />
  );
}

interface BoardAppProps {
  identity: Identity;
  onSignOut: () => void;
}

function BoardApp({ identity, onSignOut }: BoardAppProps) {
  const ns = sanitizeUser(identity.id);
  const store = useMemo(() => new LocalStore(ns), [ns]);
  const remote = useMemo(() => new RemoteStore(), []);
  const [board, setBoard] = useState<Board>(() => store.load() ?? seedBoard());
  const [modal, setModal] = useState<ModalState | null>(null);
  const [streak, setStreak] = useState<number>(() => shippedToday(ns));
  const [sync, setSync] = useState<SyncState>('off');
  const [serverPresent, setServerPresent] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [serverLabels, setServerLabels] = useState<string[]>([]);
  const [aiSettings, setAiSettings] = useState<AISettings | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const boardRef = useRef(board);
  boardRef.current = board;
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

  useEffect(() => {
    let cancelled = false;
    // Local edits that never reached the server win over the remote copy:
    // push them instead of silently adopting (and destroying) newer local work.
    const pushLocal = () => {
      syncOnRef.current = true;
      const gen = editGenRef.current;
      remote.saveRemote(
        identity,
        boardRef.current,
        (err) => {
          if (!cancelled) onSaveError(err);
        },
        () => {
          // Only an ack for the newest edit may clear the dirty flag.
          if (editGenRef.current !== gen) return;
          setDirty(ns, false);
          if (!cancelled) setSync('ok');
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
      if (remoteBoard) {
        // Adopted state, not a user edit — don't echo it back to the server.
        cleanBoardRef.current = remoteBoard;
        // parse() regenerates task ids; drop modal state holding old ones so
        // a later Save cannot append a duplicate of an existing task.
        setModal(null);
        setBoard(remoteBoard);
      }
      syncOnRef.current = true;
      setSync('ok');
    })();
    return () => {
      cancelled = true;
    };
  }, [identity, remote, ns, onSaveError]);

  useEffect(() => {
    store.save(board);
    if (board === cleanBoardRef.current) return; // initial load / server adoption
    editGenRef.current += 1;
    const gen = editGenRef.current;
    setDirty(ns, true);
    if (!syncOnRef.current) return;
    remote.saveRemoteDebounced(identity, board, onSaveError, () => {
      // A stale ack (a newer edit exists) must not clear the dirty flag the
      // newer edit depends on for crash/offline safety.
      if (editGenRef.current !== gen) return;
      setDirty(ns, false);
      setSync('ok');
    });
  }, [board, store, remote, identity, ns, onSaveError]);

  useEffect(() => {
    // Server-only extras: label suggestions and AI settings. Failures degrade
    // silently — labels fall back to board tags, the AI section stays hidden.
    if (!serverPresent) return;
    let cancelled = false;
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
    return () => {
      cancelled = true;
    };
  }, [serverPresent, identity]);

  useEffect(() => {
    // Flush the pending debounced save when the page goes away so edits made
    // <800ms before close/reload still reach the server; cancel on
    // unmount/sign-out so no PUT fires with a stale identity afterwards.
    const flush = () => remote.flush();
    window.addEventListener('pagehide', flush);
    return () => {
      window.removeEventListener('pagehide', flush);
      remote.cancel();
    };
  }, [remote]);

  const move = useCallback(
    (taskId: string, to: Status) => {
      const prev = boardRef.current.tasks.find((t) => t.id === taskId);
      if (!prev || prev.status === to) return;
      const movedAt = new Date().toISOString();
      setBoard((b) => ({
        ...b,
        tasks: b.tasks.map((t) =>
          t.id === taskId ? { ...t, status: to, movedAt } : t,
        ),
      }));
      if (to === 'done') {
        setStreak(bumpShipped(ns));
        const r = document
          .querySelector(`[data-task="${taskId}"]`)
          ?.getBoundingClientRect();
        const x = r ? r.left + r.width / 2 : window.innerWidth / 2;
        const y = r ? r.top + r.height / 2 : window.innerHeight / 2;
        burst(x, y, 70);
      }
    },
    [ns],
  );

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
        if (checks.every((c) => c.done) && task.status !== 'done') {
          setTimeout(() => {
            // Re-check at fire time: a tick may have been undone meanwhile.
            const cur = boardRef.current.tasks.find((t) => t.id === taskId);
            if (cur && cur.status !== 'done' && cur.checks.every((c) => c.done)) {
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

  const handleDelete = useCallback((taskId: string) => {
    setBoard((b) => ({ ...b, tasks: b.tasks.filter((t) => t.id !== taskId) }));
    setModal(null);
  }, []);

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
      if (
        window.confirm(
          `Replace current board with "${next.title}" (${next.tasks.length} tasks)? This cannot be undone.`,
        )
      ) {
        setBoard(next);
      }
    } catch {
      window.alert('Could not read that file as a board.');
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
  const aiDraft = useMemo(
    () =>
      aiEnabled
        ? (req: AIStoryRequest) => aiStory(identity, req)
        : undefined,
    [aiEnabled, identity],
  );

  return (
    <>
      <header className="app-header">
        <h1>webtui</h1>
        <div className="hactions">
          {streak > 0 && <span className="streak">×{streak} shipped today</span>}
          <span className="who" title={identity.id}>
            {identity.id}
          </span>
          <span
            className={`dot ${sync}`}
            title={SYNC_TITLE[sync]}
            role="status"
            aria-label={SYNC_TITLE[sync]}
          />
          {serverPresent && (
            <button
              type="button"
              aria-label="Settings"
              title="AI settings"
              onClick={() => setShowSettings(true)}
            >
              ⚙
            </button>
          )}
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
      <BoardView
        board={board}
        onMove={move}
        onTick={handleTick}
        onEdit={openEdit}
        onAdd={openAdd}
      />
      <div className="foot">
        drag cards between columns · tap ▾ to expand checklists · tap a card to edit
      </div>
      {modal && (
        <CardModal
          state={modal}
          labels={allLabels}
          aiDraft={aiDraft}
          onSave={handleSave}
          onDelete={handleDelete}
          onClose={() => setModal(null)}
        />
      )}
      {showSettings && serverPresent && (
        <SettingsModal
          identity={identity}
          onClose={() => setShowSettings(false)}
          onSaved={setAiSettings}
        />
      )}
      <Confetti />
    </>
  );
}
