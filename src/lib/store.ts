import type { Board } from './model';
import { newTask } from './model';

/** Storage abstraction — a remote (VPS sync) adapter drops in behind this later. */
export interface BoardStore {
  load(): Board | null;
  save(board: Board): void;
}

const BOARD_KEY = 'webtui.board.v1';
const STREAK_KEY = 'webtui.streak.v1';
const DIRTY_KEY = 'webtui.dirty.v1';

/**
 * Per-user key: the 'default' namespace keeps the legacy un-suffixed keys so
 * pre-identity boards survive.
 */
function nsKey(base: string, ns: string): string {
  return ns === 'default' ? base : `${base}.${ns}`;
}

export class LocalStore implements BoardStore {
  private readonly key: string;

  constructor(ns: string = 'default') {
    this.key = nsKey(BOARD_KEY, ns);
  }

  load(): Board | null {
    try {
      const raw = localStorage.getItem(this.key);
      return raw ? (JSON.parse(raw) as Board) : null;
    } catch {
      return null;
    }
  }
  save(board: Board): void {
    localStorage.setItem(this.key, JSON.stringify(board));
  }
}

/**
 * True when this namespace has local edits that never reached the server
 * (offline session, failed save, or a save pending at shutdown). Startup
 * consults this so a stale remote copy cannot silently overwrite newer
 * local changes.
 */
export function loadDirty(ns: string = 'default'): boolean {
  try {
    return localStorage.getItem(nsKey(DIRTY_KEY, ns)) === '1';
  } catch {
    return false;
  }
}

export function setDirty(ns: string, dirty: boolean): void {
  try {
    if (dirty) localStorage.setItem(nsKey(DIRTY_KEY, ns), '1');
    else localStorage.removeItem(nsKey(DIRTY_KEY, ns));
  } catch {
    // Storage unavailable — the next startup treats local state as clean.
  }
}

export function shippedToday(ns: string = 'default'): number {
  try {
    const raw = localStorage.getItem(nsKey(STREAK_KEY, ns));
    if (!raw) return 0;
    const { date, n } = JSON.parse(raw) as { date: string; n: number };
    return date === new Date().toDateString() ? n : 0;
  } catch {
    return 0;
  }
}

export function bumpShipped(ns: string = 'default'): number {
  const n = shippedToday(ns) + 1;
  localStorage.setItem(
    nsKey(STREAK_KEY, ns),
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
