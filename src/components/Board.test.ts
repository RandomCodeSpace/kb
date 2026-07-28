import { beforeEach, describe, expect, it } from 'vitest';
import type { Status, Task } from '../lib/model';
import { newTask } from '../lib/model';
import {
  canStartDrag,
  insertionIndex,
  moveTask,
  setShowCancelledFlag,
  showCancelledFlag,
} from './Board';

// Node test environment has no Web Storage — stub it on globalThis.
const mem = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (k: string) => mem.get(k) ?? null,
  setItem: (k: string, v: string) => {
    mem.set(k, String(v));
  },
  removeItem: (k: string) => {
    mem.delete(k);
  },
  clear: () => {
    mem.clear();
  },
  key: () => null,
  get length() {
    return mem.size;
  },
};

beforeEach(() => {
  mem.clear();
});

const T0 = '2024-01-01T00:00:00.000Z';
const NOW = '2024-06-01T00:00:00.000Z';

function task(title: string, status: Status): Task {
  return newTask({ title, status, createdAt: T0, movedAt: T0 });
}

/** Titles in each column, in the order the codec would serialize them. */
function layout(tasks: readonly Task[]): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const t of tasks) (out[t.status] ??= []).push(t.title);
  return out;
}

describe('insertionIndex', () => {
  it('is 0 for an empty column', () => {
    expect(insertionIndex([], 120)).toBe(0);
  });

  it('lands above a card when the pointer is above its midpoint', () => {
    expect(insertionIndex([100, 200, 300], 99)).toBe(0);
    expect(insertionIndex([100, 200, 300], 150)).toBe(1);
    expect(insertionIndex([100, 200, 300], 250)).toBe(2);
  });

  it('appends when the pointer is below every card', () => {
    expect(insertionIndex([100, 200, 300], 999)).toBe(3);
  });
});

describe('moveTask', () => {
  const base = [
    task('a', 'todo'),
    task('b', 'todo'),
    task('c', 'doing'),
    task('d', 'todo'),
  ];

  it('drops a card into the requested slot of another column', () => {
    const next = moveTask(base, base[3].id, 'doing', 0, NOW);
    expect(layout(next)).toEqual({ todo: ['a', 'b'], doing: ['d', 'c'] });
  });

  it('appends when the slot is past the end of the column', () => {
    const next = moveTask(base, base[0].id, 'doing', 99, NOW);
    expect(layout(next).doing).toEqual(['c', 'a']);
  });

  it('reorders within a column and keeps the card age', () => {
    const next = moveTask(base, base[3].id, 'todo', 0, NOW);
    expect(layout(next).todo).toEqual(['d', 'a', 'b']);
    expect(next.find((t) => t.title === 'd')?.movedAt).toBe(T0);
  });

  it('stamps movedAt only when the status changes', () => {
    const next = moveTask(base, base[0].id, 'done', 0, NOW);
    expect(next.find((t) => t.title === 'a')?.movedAt).toBe(NOW);
  });

  it('moves into an empty column', () => {
    const next = moveTask(base, base[1].id, 'cancelled', 0, NOW);
    expect(layout(next)).toEqual({
      todo: ['a', 'd'],
      doing: ['c'],
      cancelled: ['b'],
    });
  });

  it('leaves an unknown id alone', () => {
    expect(layout(moveTask(base, 'nope', 'done', 0, NOW))).toEqual(
      layout(base),
    );
  });
});

describe('canStartDrag', () => {
  it('lets a mouse drag from anywhere on the card', () => {
    expect(canStartDrag('mouse', false)).toBe(true);
    expect(canStartDrag('mouse', true)).toBe(true);
  });

  // The card body keeps `touch-action: pan-y` so the column can be scrolled,
  // which hands a vertical touch drag there to the browser: it ends in
  // pointercancel and the in-column reorder never happens. Touch drags must
  // start on the grip, which opts out with `touch-action: none`.
  it('requires the grip for touch and pen', () => {
    expect(canStartDrag('touch', false)).toBe(false);
    expect(canStartDrag('touch', true)).toBe(true);
    expect(canStartDrag('pen', false)).toBe(false);
    expect(canStartDrag('pen', true)).toBe(true);
  });
});

describe('showCancelledFlag', () => {
  it('is off by default and persists once set', () => {
    expect(showCancelledFlag()).toBe(false);
    setShowCancelledFlag(true);
    expect(mem.get('kb.showCancelled.v1')).toBe('1');
    expect(showCancelledFlag()).toBe(true);
    setShowCancelledFlag(false);
    expect(showCancelledFlag()).toBe(false);
  });
});
