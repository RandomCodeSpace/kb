import type { Board } from './model';
import { newTask } from './model';

/** Storage abstraction — a remote (VPS sync) adapter drops in behind this later. */
export interface BoardStore {
  load(): Board | null;
  save(board: Board): void;
}

const BOARD_KEY = 'kb.board.v1';
const STREAK_KEY = 'kb.streak.v1';
const DIRTY_KEY = 'kb.dirty.v1';
/** Set once the pre-rename values have been copied forward (see below). */
const MIGRATED_KEY = 'kb.migrated.v1';

const KEY_PREFIX = 'kb.';
const LEGACY_PREFIX = 'webtui.';

/**
 * Per-user key: the 'default' namespace keeps the legacy un-suffixed keys so
 * pre-identity boards survive.
 */
function nsKey(base: string, ns: string): string {
  return ns === 'default' ? base : `${base}.${ns}`;
}

/**
 * One-time, non-destructive copy of the pre-rename `webtui.*` values to their
 * `kb.*` keys. The old keys are deliberately left in place: a user who rolls
 * back to an older build must not lose their board. `flagKey` makes it run
 * exactly once per storage — re-copying later would resurrect state the new
 * code has since removed (a cleared dirty flag, a signed-out identity).
 */
export function migrateLegacyKeys(
  storage: Storage,
  flagKey: string,
  keys: readonly string[],
): void {
  try {
    if (storage.getItem(flagKey) === '1') return;
    for (const key of keys) {
      if (!key.startsWith(KEY_PREFIX)) continue;
      if (storage.getItem(key) !== null) continue;
      const legacy = storage.getItem(LEGACY_PREFIX + key.slice(KEY_PREFIX.length));
      if (legacy !== null) storage.setItem(key, legacy);
    }
    storage.setItem(flagKey, '1');
  } catch {
    // Storage unavailable — nothing to migrate.
  }
}

const migrated = new Set<string>();

/** Migrate this namespace's keys before the first read/write touches them. */
function ensureMigrated(ns: string): void {
  if (migrated.has(ns)) return;
  migrated.add(ns);
  try {
    migrateLegacyKeys(localStorage, nsKey(MIGRATED_KEY, ns), [
      nsKey(BOARD_KEY, ns),
      nsKey(STREAK_KEY, ns),
      nsKey(DIRTY_KEY, ns),
    ]);
  } catch {
    // Storage unavailable — nothing to migrate.
  }
}

/** Board plus whether it came from a fresh seed rather than storage. */
export interface LoadedBoard {
  board: Board;
  seeded: boolean;
}

export class LocalStore implements BoardStore {
  private readonly ns: string;
  private readonly key: string;

  constructor(ns: string = 'default') {
    this.ns = ns;
    ensureMigrated(ns);
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

  /**
   * Load the stored board, or seed a fresh one. A seeded board is not local
   * work: it must never be pushed over a board the server already has, so
   * seeding clears the dirty flag instead of setting it. Callers use `seeded`
   * to adopt the server board on first contact rather than uploading the demo.
   */
  loadOrSeed(): LoadedBoard {
    const board = this.load();
    if (board) return { board, seeded: false };
    setDirty(this.ns, false);
    return { board: seedBoard(), seeded: true };
  }
}

/**
 * True when this namespace has local edits that never reached the server
 * (offline session, failed save, or a save pending at shutdown). Startup
 * consults this so a stale remote copy cannot silently overwrite newer
 * local changes.
 */
export function loadDirty(ns: string = 'default'): boolean {
  ensureMigrated(ns);
  try {
    return localStorage.getItem(nsKey(DIRTY_KEY, ns)) === '1';
  } catch {
    return false;
  }
}

export function setDirty(ns: string, dirty: boolean): void {
  ensureMigrated(ns);
  try {
    if (dirty) localStorage.setItem(nsKey(DIRTY_KEY, ns), '1');
    else localStorage.removeItem(nsKey(DIRTY_KEY, ns));
  } catch {
    // Storage unavailable — the next startup treats local state as clean.
  }
}

/** Today's shipped record: the cards shipped, not a count (see bumpShipped). */
interface Streak {
  date: string;
  ids: string[];
}

/**
 * Identity of a card for the shipped counter. A task id cannot be used: the
 * wire format does not carry ids, so parse() mints new ones on every server
 * refetch and the same card would be counted a second time after a reload.
 * The title is what survives that round trip. Two cards sharing a title count
 * once, which is the safe direction — the counter must never inflate.
 */
export function shipKey(task: { title: string }): string {
  return task.title.trim();
}

/**
 * Today's record, or an empty one. A record from an earlier day (or the
 * pre-rename `{date, n}` counter shape) reads as empty — the streak is a
 * cosmetic daily figure, so a rollover simply starts over.
 */
function loadStreak(ns: string): Streak {
  const date = new Date().toDateString();
  ensureMigrated(ns);
  try {
    const raw = localStorage.getItem(nsKey(STREAK_KEY, ns));
    if (!raw) return { date, ids: [] };
    const v = JSON.parse(raw) as { date?: unknown; ids?: unknown };
    if (v.date !== date || !Array.isArray(v.ids)) return { date, ids: [] };
    return { date, ids: v.ids.filter((x): x is string => typeof x === 'string') };
  } catch {
    return { date, ids: [] };
  }
}

export function shippedToday(ns: string = 'default'): number {
  return loadStreak(ns).ids.length;
}

/**
 * Record the card `key` (see shipKey) as shipped today and return the day's
 * count. Storing keys rather than a counter makes this idempotent per card:
 * dragging one card Done → Doing → Done → … still counts once, and so does
 * shipping it again after a reload.
 */
export function bumpShipped(ns: string, key: string): number {
  const streak = loadStreak(ns);
  if (!streak.ids.includes(key)) streak.ids.push(key);
  return saveStreak(ns, streak);
}

/**
 * Drop the card `key` from today's tally, for when it leaves Done, and return
 * the day's count.
 *
 * A reopened card is not shipped: the counter says what is done today, so it
 * has to be able to go down. Re-shipping the same card later adds it back and
 * it still counts once, so this cannot be farmed by moving a card back and
 * forth. Unknown keys are a no-op.
 */
export function unshipToday(ns: string, key: string): number {
  const streak = loadStreak(ns);
  const at = streak.ids.indexOf(key);
  if (at === -1) return streak.ids.length;
  streak.ids.splice(at, 1);
  return saveStreak(ns, streak);
}

function saveStreak(ns: string, streak: Streak): number {
  try {
    localStorage.setItem(nsKey(STREAK_KEY, ns), JSON.stringify(streak));
  } catch {
    // Storage unavailable — the count lives on in memory for this session.
  }
  return streak.ids.length;
}

export function seedBoard(): Board {
  return {
    title: 'kb',
    tasks: [
      newTask({
        emoji: '👋', title: 'Drag me to Doing', prio: 2, status: 'todo',
        desc: 'Drag the ⠿ grip (or anywhere with a mouse) and drop.',
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
