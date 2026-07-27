import type { Status } from './model';

const DAY = 86_400_000;

function daysBetween(fromYmd: string, toYmd: string): number {
  return Math.round((Date.parse(toYmd) - Date.parse(fromYmd)) / DAY);
}

export function ymd(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** Relative due chip. `today` is YYYY-MM-DD. */
export function dueChip(
  due: string,
  today: string,
): { label: string; overdue: boolean } {
  const d = daysBetween(today, due);
  if (d === 0) return { label: 'today', overdue: false };
  if (d === 1) return { label: 'tomorrow', overdue: false };
  if (d > 1) return { label: `in ${d}d`, overdue: false };
  return { label: `overdue · ${-d}d`, overdue: true };
}

/** Age chip: staleness in To Do, dwell time in Doing. */
export function ageChip(
  status: Status,
  createdAt: string,
  movedAt: string,
  now: number,
): string {
  if (status === 'done') return 'shipped';
  const ref = status === 'doing' ? movedAt : createdAt;
  const ms = Math.max(0, now - Date.parse(ref));
  const suffix = status === 'doing' ? 'here' : 'old';
  if (ms < DAY) {
    const h = Math.floor(ms / 3_600_000);
    return status === 'doing' ? `${Math.max(1, h)}h here` : 'new';
  }
  return `${Math.floor(ms / DAY)}d ${suffix}`;
}
