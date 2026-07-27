import { describe, expect, it } from 'vitest';
import { ageChip, dueChip } from './urgency';

const TODAY = '2026-07-27';
const NOW = Date.parse('2026-07-27T12:00:00Z');
const HOUR = 3_600_000;
const DAY = 86_400_000;

describe('dueChip', () => {
  it('labels a due date of today', () => {
    expect(dueChip('2026-07-27', TODAY)).toEqual({ label: 'today', overdue: false });
  });

  it('labels a due date of tomorrow', () => {
    expect(dueChip('2026-07-28', TODAY)).toEqual({
      label: 'tomorrow',
      overdue: false,
    });
  });

  it('labels a due date 5 days out', () => {
    expect(dueChip('2026-08-01', TODAY)).toEqual({ label: 'in 5d', overdue: false });
  });

  it('labels a due date 6 days past as overdue', () => {
    expect(dueChip('2026-07-21', TODAY)).toEqual({
      label: 'overdue · 6d',
      overdue: true,
    });
  });
});

describe('ageChip', () => {
  it("shows 'new' for a todo created 2h ago", () => {
    const created = new Date(NOW - 2 * HOUR).toISOString();
    expect(ageChip('todo', created, created, NOW)).toBe('new');
  });

  it("shows '3d old' for a todo created 3 days ago", () => {
    const created = new Date(NOW - 3 * DAY).toISOString();
    expect(ageChip('todo', created, created, NOW)).toBe('3d old');
  });

  it("shows '5h here' for a doing task moved 5h ago", () => {
    const created = new Date(NOW - 10 * DAY).toISOString();
    const moved = new Date(NOW - 5 * HOUR).toISOString();
    expect(ageChip('doing', created, moved, NOW)).toBe('5h here');
  });

  it("shows '2d here' for a doing task moved 2 days ago", () => {
    const created = new Date(NOW - 10 * DAY).toISOString();
    const moved = new Date(NOW - 2 * DAY).toISOString();
    expect(ageChip('doing', created, moved, NOW)).toBe('2d here');
  });

  it("shows 'shipped' for done tasks regardless of timestamps", () => {
    const iso = new Date(NOW - 30 * DAY).toISOString();
    expect(ageChip('done', iso, iso, NOW)).toBe('shipped');
  });
});
