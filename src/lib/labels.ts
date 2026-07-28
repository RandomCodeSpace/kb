import type { Board } from './model';
import { CONFETTI_COLORS } from './confetti';

/** Stable tag color: same hash the approved prototype used, over the shared palette. */
export function tagColor(tag: string): string {
  return CONFETTI_COLORS[(tag.length + (tag.charCodeAt(0) || 0)) % CONFETTI_COLORS.length];
}

/** Distinct tags used anywhere on the board, sorted. */
export function boardLabels(board: Board): string[] {
  const seen = new Set<string>();
  for (const t of board.tasks) for (const tag of t.tags) seen.add(tag);
  return [...seen].sort((a, b) => a.localeCompare(b));
}

/** Sorted union of two label lists (server labels + board-derived fallback). */
export function unionLabels(a: string[], b: string[]): string[] {
  return [...new Set([...a, ...b])].sort((x, y) => x.localeCompare(y));
}

/**
 * Combobox suggestions: candidates not already selected that match the query
 * case-insensitively, prefix matches ranked before substring matches. An
 * empty query returns all unselected candidates. Capped at `limit`.
 */
export function filterLabels(
  all: string[],
  selected: string[],
  query: string,
  limit = 8,
): string[] {
  const sel = new Set(selected);
  const pool = all.filter((l) => !sel.has(l));
  const q = query.trim().toLowerCase();
  if (q === '') return pool.slice(0, limit);
  const starts: string[] = [];
  const contains: string[] = [];
  for (const l of pool) {
    const ll = l.toLowerCase();
    if (ll.startsWith(q)) starts.push(l);
    else if (ll.includes(q)) contains.push(l);
  }
  return [...starts, ...contains].slice(0, limit);
}

/**
 * Free-text chip entry: whitespace splits into multiple tags, a leading '#'
 * (people type it out of habit) is stripped, empties dropped.
 */
export function parseTagInput(raw: string): string[] {
  return raw
    .split(/\s+/)
    .map((t) => t.replace(/^#+/, ''))
    .filter(Boolean);
}

/** Append parsed tags from `raw` to `current`, skipping duplicates. */
export function addTags(current: string[], raw: string): string[] {
  const next = [...current];
  for (const tag of parseTagInput(raw)) {
    if (!next.includes(tag)) next.push(tag);
  }
  return next;
}
