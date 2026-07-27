import type { Board } from './model';
import { newTask } from './model';

/** Storage abstraction — a remote (VPS sync) adapter drops in behind this later. */
export interface BoardStore {
  load(): Board | null;
  save(board: Board): void;
}

const BOARD_KEY = 'webtui.board.v1';

export class LocalStore implements BoardStore {
  load(): Board | null {
    try {
      const raw = localStorage.getItem(BOARD_KEY);
      return raw ? (JSON.parse(raw) as Board) : null;
    } catch {
      return null;
    }
  }
  save(board: Board): void {
    localStorage.setItem(BOARD_KEY, JSON.stringify(board));
  }
}

const STREAK_KEY = 'webtui.streak.v1';

export function shippedToday(): number {
  try {
    const raw = localStorage.getItem(STREAK_KEY);
    if (!raw) return 0;
    const { date, n } = JSON.parse(raw) as { date: string; n: number };
    return date === new Date().toDateString() ? n : 0;
  } catch {
    return 0;
  }
}

export function bumpShipped(): number {
  const n = shippedToday() + 1;
  localStorage.setItem(
    STREAK_KEY,
    JSON.stringify({ date: new Date().toDateString(), n }),
  );
  return n;
}

export function seedBoard(): Board {
  return {
    title: 'webtui',
    tasks: [
      newTask({
        emoji: '👋', title: 'Drag me to Doing', prio: 2, status: 'todo',
        desc: 'Cards move between columns — grab anywhere and drop.',
        tags: ['start::here'],
      }),
      newTask({
        emoji: '✅', title: 'Tick my checklist on the board', prio: 3, status: 'todo',
        desc: 'Expand with the ▾ button. Finishing every item ships the card.',
        checks: [
          { text: 'expand the checklist', done: false },
          { text: 'tick this one', done: false },
          { text: 'and this one — watch the card ship', done: false },
        ],
        tags: ['type::demo'], effort: 'S',
      }),
      newTask({
        emoji: '➕', title: 'Add your own task', prio: 3, status: 'todo',
        desc: 'The + button in any column. Tags support scoped labels like env::prod.',
      }),
    ],
  };
}
