import { describe, expect, it } from 'vitest';
import {
  dayLabel,
  fmtISO,
  monthGrid,
  monthLabel,
  moveDays,
  moveMonths,
  parseISO,
  todayISO,
} from './DateField';

describe('fmtISO / todayISO', () => {
  it('zero-pads to the wire form', () => {
    expect(fmtISO(2026, 0, 5)).toBe('2026-01-05');
    expect(fmtISO(2026, 11, 31)).toBe('2026-12-31');
  });

  it('pads the year, staying the exact inverse of parseISO', () => {
    // The native input serializes a typed "999" as "0999-…"; an unpadded
    // year would fail parseISO's four-digit check and wedge the grid.
    expect(fmtISO(999, 6, 15)).toBe('0999-07-15');
    expect(parseISO(fmtISO(999, 6, 15))).toEqual({ y: 999, m: 6, d: 15 });
  });

  it('renders the local day, not the UTC one', () => {
    // 00:30 local on the 27th: toISOString would say the 26th anywhere
    // east of Greenwich is irrelevant — the point is it uses local getters.
    expect(todayISO(new Date(2026, 6, 27, 0, 30))).toBe('2026-07-27');
    expect(todayISO(new Date(2026, 6, 27, 23, 30))).toBe('2026-07-27');
  });
});

describe('parseISO', () => {
  it('round-trips a valid date', () => {
    expect(parseISO('2026-07-27')).toEqual({ y: 2026, m: 6, d: 27 });
  });

  it('rejects shapes and impossible dates instead of rolling them over', () => {
    expect(parseISO('')).toBeNull();
    expect(parseISO('27/07/2026')).toBeNull();
    expect(parseISO('2026-7-2')).toBeNull();
    // new Date() would make this March 3rd; a due date must not move itself.
    expect(parseISO('2026-02-31')).toBeNull();
  });
});

describe('moveDays', () => {
  it('crosses month and year edges', () => {
    expect(moveDays('2026-07-31', 1)).toBe('2026-08-01');
    expect(moveDays('2026-01-01', -1)).toBe('2025-12-31');
  });

  it('moves by a week for the grid rows', () => {
    expect(moveDays('2026-07-27', 7)).toBe('2026-08-03');
    expect(moveDays('2026-07-27', -7)).toBe('2026-07-20');
  });

  it('leaves an unparseable value untouched', () => {
    expect(moveDays('nope', 1)).toBe('nope');
  });
});

describe('moveMonths', () => {
  it('clamps to the shorter month instead of rolling over', () => {
    // Jan 31 + 1 month: Date arithmetic alone says March 3rd.
    expect(moveMonths('2026-01-31', 1)).toBe('2026-02-28');
    expect(moveMonths('2024-01-31', 1)).toBe('2024-02-29'); // leap year
  });

  it('crosses the year in both directions', () => {
    expect(moveMonths('2026-12-15', 1)).toBe('2027-01-15');
    expect(moveMonths('2026-01-15', -1)).toBe('2025-12-15');
  });
});

describe('monthGrid', () => {
  it('always renders six full weeks so paging never resizes the popover', () => {
    for (const iso of ['2026-02-01', '2026-07-27', '2026-08-01']) {
      expect(monthGrid(iso)).toHaveLength(42);
    }
  });

  it('starts weeks on Monday', () => {
    // July 2026 begins on a Wednesday: Mon 29 and Tue 30 June lead the grid.
    const cells = monthGrid('2026-07-27');
    expect(cells[0]).toEqual({ iso: '2026-06-29', day: 29, inMonth: false });
    expect(cells[2]).toEqual({ iso: '2026-07-01', day: 1, inMonth: true });
  });

  it('marks spill days as outside the month', () => {
    const cells = monthGrid('2026-07-27');
    expect(cells.filter((c) => c.inMonth)).toHaveLength(31);
  });
});

describe('labels', () => {
  it('names the visible month', () => {
    expect(monthLabel('2026-07-27')).toBe('July 2026');
  });

  it('speaks a full day for the grid cells', () => {
    expect(dayLabel('2026-07-27')).toBe('Monday 27 July 2026');
  });
});
