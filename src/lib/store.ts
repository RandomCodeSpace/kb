const STREAK_KEY = 'kb.streak.v1';
const AUX_MIGRATED_KEY = 'kb.aux-migrated.v1';

const KEY_PREFIX = 'kb.';
const LEGACY_PREFIX = 'webtui.';
const NAMESPACE_MARKER = '.ns.';

/**
 * Injective namespace framing for localStorage keys. The encoded length makes
 * the boundary explicit, so `alice` can never consume `alice.work`'s suffix.
 */
export function namespaceStorageKey(
  base: string,
  ns: string,
  suffix?: string,
): string {
  const encoded = encodeURIComponent(ns);
  return `${base}${NAMESPACE_MARKER}${encoded.length}:${encoded}${
    suffix === undefined ? '' : `.${suffix}`
  }`;
}

/** Pre-G009 key shape. Direct construction is safe; prefix scans are not. */
export function legacyNamespaceStorageKey(
  base: string,
  ns: string,
  suffix?: string,
): string {
  const namespaced = ns === 'default' ? base : `${base}.${ns}`;
  return suffix === undefined ? namespaced : `${namespaced}.${suffix}`;
}

/**
 * Per-user key: the 'default' namespace keeps the legacy un-suffixed keys so
 * pre-identity boards survive.
 */
function nsKey(base: string, ns: string): string {
  return namespaceStorageKey(base, ns);
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

/**
 * Migrate the one surviving per-namespace key to its framed name. The board
 * itself is server-authoritative and no longer lives here, so the shipped
 * tally is all this moves.
 */
function ensureMigrated(ns: string): void {
  if (migrated.has(ns)) return;
  migrated.add(ns);
  try {
    const legacyFlag = legacyNamespaceStorageKey(AUX_MIGRATED_KEY, ns);
    migrateLegacyKeys(localStorage, legacyFlag, [
      legacyNamespaceStorageKey(STREAK_KEY, ns),
    ]);

    const flag = nsKey(AUX_MIGRATED_KEY, ns);
    if (localStorage.getItem(flag) === '1') return;
    const target = nsKey(STREAK_KEY, ns);
    if (localStorage.getItem(target) === null) {
      const value = localStorage.getItem(legacyNamespaceStorageKey(STREAK_KEY, ns));
      if (value !== null) localStorage.setItem(target, value);
    }
    localStorage.setItem(flag, '1');
  } catch {
    // Storage unavailable — nothing to migrate.
  }
}

/** Today's shipped record: the cards shipped, not a count (see bumpShipped). */
interface Streak {
  date: string;
  ids: string[];
}

/**
 * Identity of a card for the shipped counter. Deliberately still the title
 * rather than the (now stable) server id: switching the key format would read
 * every card shipped earlier today as unseen and re-count it on the next move.
 * Two cards sharing a title count once, which is the safe direction — the
 * counter must never inflate.
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
