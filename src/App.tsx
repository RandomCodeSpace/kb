import { useCallback, useEffect, useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import type { Board, Status, Task } from './lib/model';
import { parse, serialize } from './lib/markdown';
import { bumpShipped, LocalStore, seedBoard, shippedToday } from './lib/store';
import { burst } from './lib/confetti';
import { BoardView } from './components/Board';
import { CardModal } from './components/CardModal';
import type { ModalState } from './components/CardModal';
import { Confetti } from './components/Confetti';

const store = new LocalStore();

export default function App() {
  const [board, setBoard] = useState<Board>(() => store.load() ?? seedBoard());
  const [modal, setModal] = useState<ModalState | null>(null);
  const [streak, setStreak] = useState<number>(() => shippedToday());
  const fileRef = useRef<HTMLInputElement>(null);
  const boardRef = useRef(board);
  boardRef.current = board;

  useEffect(() => {
    store.save(board);
  }, [board]);

  const move = useCallback((taskId: string, to: Status) => {
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
      setStreak(bumpShipped());
      const r = document
        .querySelector(`[data-task="${taskId}"]`)
        ?.getBoundingClientRect();
      const x = r ? r.left + r.width / 2 : window.innerWidth / 2;
      const y = r ? r.top + r.height / 2 : window.innerHeight / 2;
      burst(x, y, 70);
    }
  }, []);

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

  return (
    <>
      <header className="app-header">
        <h1>webtui</h1>
        <div className="hactions">
          {streak > 0 && <span className="streak">×{streak} shipped today</span>}
          <button type="button" onClick={handleExport}>
            Export
          </button>
          <button type="button" onClick={() => fileRef.current?.click()}>
            Import
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
          onSave={handleSave}
          onDelete={handleDelete}
          onClose={() => setModal(null)}
        />
      )}
      <Confetti />
    </>
  );
}
