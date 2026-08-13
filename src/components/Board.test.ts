import { beforeEach, describe, expect, it } from 'vitest';
import type { Status, Task } from '../lib/model';
import { cardLabel, STATUSES } from '../lib/model';
import { makeTask } from '../test/task';
import type { LiftPos } from './Board';
import {
  cancelAnnouncement,
  canStartDrag,
  columnSizes,
  dropIndex,
  fullColumnIndex,
  insertionIndex,
  isLiftKey,
  liftAnnouncement,
  liftSurvivesFocus,
  liftMoveAnnouncement,
  moveLift,
  movedAnnouncement,
  pastThreshold,
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
  return makeTask({ title, status, createdAt: T0, movedAt: T0 });
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

describe('dropIndex', () => {
  // Columns are hit-tested against whatever is rendered, and the dragged card
  // is only pulled out of its column once the drag goes active. A flick that
  // presses, crosses the threshold and releases inside one animation frame
  // drops while the card is still there, so it has to be excluded here — or
  // the index lands one slot too low against the list moveTask works on
  // (which has already removed it).
  const column = [
    { taskId: 'a', mid: 158 },
    { taskId: 'b', mid: 294 },
    { taskId: 'c', mid: 436 },
  ];

  it('ignores the dragged card while it is still rendered', () => {
    // Between b and c: 1 over [b, c], not 2 over [a, b, c].
    expect(dropIndex(column, 'a', 350)).toBe(1);
    expect(dropIndex(column, 'a', 100)).toBe(0);
    expect(dropIndex(column, 'a', 999)).toBe(2);
  });

  it('matches insertionIndex once the card has left the column', () => {
    expect(dropIndex(column.slice(1), 'a', 350)).toBe(1);
    expect(dropIndex(column, 'a', 350)).toBe(
      insertionIndex([294, 436], 350),
    );
  });

  it('counts every card when the drag came from another column', () => {
    expect(dropIndex(column, 'z', 350)).toBe(2);
  });

  // End to end: [a, b, c], flick a down past b's midpoint and release before
  // the first animation frame. It belongs between b and c, not after c.
  it('reorders a flicked card to where it was released', () => {
    const tasks = [task('a', 'todo'), task('b', 'todo'), task('c', 'todo')];
    const cards = tasks.map((t, i) => ({ taskId: t.id, mid: 158 + i * 138 }));
    const at = dropIndex(cards, tasks[0].id, 350);
    expect(layout(moveTask(tasks, tasks[0].id, 'todo', at, NOW)).todo).toEqual([
      'b',
      'a',
      'c',
    ]);
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

describe('columnSizes', () => {
  it('counts every column with the lifted card left out', () => {
    const tasks = [
      task('a', 'todo'),
      task('b', 'todo'),
      task('c', 'doing'),
    ];
    expect(columnSizes(tasks, tasks[0].id)).toEqual({
      todo: 1,
      doing: 1,
      done: 0,
      cancelled: 0,
    });
  });
});

describe('moveLift', () => {
  const columns: Status[] = ['todo', 'doing', 'done'];
  const sizes = { todo: 2, doing: 3, done: 0, cancelled: 0 };
  const at = (to: Status, index: number): LiftPos => ({ to, index });

  it('reorders inside a column and stops at both ends', () => {
    expect(moveLift(at('todo', 0), 'ArrowDown', columns, sizes)).toEqual(
      at('todo', 1),
    );
    expect(moveLift(at('todo', 2), 'ArrowDown', columns, sizes)).toEqual(
      at('todo', 2),
    );
    expect(moveLift(at('todo', 1), 'ArrowUp', columns, sizes)).toEqual(
      at('todo', 0),
    );
    expect(moveLift(at('todo', 0), 'ArrowUp', columns, sizes)).toEqual(
      at('todo', 0),
    );
  });

  it('crosses columns, keeping the slot where it exists', () => {
    expect(moveLift(at('todo', 1), 'ArrowRight', columns, sizes)).toEqual(
      at('doing', 1),
    );
    expect(moveLift(at('doing', 2), 'ArrowRight', columns, sizes)).toEqual(
      // Done is empty: the only slot there is the first one.
      at('done', 0),
    );
  });

  // A card that jumped from the last column back to the first would be a move
  // nobody asked for, so both ends are walls.
  it('does not wrap past the first or last column', () => {
    expect(moveLift(at('todo', 0), 'ArrowLeft', columns, sizes)).toEqual(
      at('todo', 0),
    );
    expect(moveLift(at('done', 0), 'ArrowRight', columns, sizes)).toEqual(
      at('done', 0),
    );
  });

  it('ignores a column that is not on the board', () => {
    expect(moveLift(at('cancelled', 0), 'ArrowLeft', columns, sizes)).toEqual(
      at('cancelled', 0),
    );
  });
});

describe('move announcements', () => {
  it('says where the card landed and how far down', () => {
    expect(movedAnnouncement('Fix login', 'doing', 1, 4)).toBe(
      'Fix login moved to Doing, position 2 of 4',
    );
  });

  it('teaches the keys when the card is picked up', () => {
    const said = liftAnnouncement('Fix login', { to: 'todo', index: 0 }, 3);
    expect(said).toContain('Fix login lifted from To Do, position 1 of 3');
    expect(said).toContain('arrow keys');
    expect(said).toContain('Escape');
  });

  it('names the position on every arrow press', () => {
    expect(liftMoveAnnouncement('Fix login', { to: 'done', index: 2 }, 5)).toBe(
      'Fix login, Done, position 3 of 5',
    );
  });

  it('says the card is back where it started when the move is cancelled', () => {
    expect(cancelAnnouncement('Fix login', { to: 'todo', index: 1 }, 3)).toBe(
      'Move cancelled. Fix login is back in To Do, position 2 of 3.',
    );
  });
});

/**
 * The board's keyboard move, driven through exactly the functions BoardView
 * drives: pick up, arrow, drop or cancel. Nothing is committed until a drop,
 * which is what makes Escape a restore rather than an undo.
 */
function keyboardMove(
  tasks: readonly Task[],
  taskId: string,
  keys: readonly string[],
  columns: readonly Status[] = STATUSES,
): { tasks: Task[]; said: string[]; lifted: boolean } {
  const moving = tasks.find((t) => t.id === taskId);
  if (!moving) throw new Error('no such task');
  const column = tasks.filter((t) => t.status === moving.status);
  const start: LiftPos = {
    to: moving.status,
    index: column.findIndex((t) => t.id === taskId),
  };
  const sizes = columnSizes(tasks, taskId);
  const total = (s: Status) => (sizes[s] ?? 0) + 1;
  const said: string[] = [];
  let lift: LiftPos | null = null;
  let out = [...tasks];
  for (const key of keys) {
    if (key === ' ' || key === 'Enter') {
      if (lift) {
        out = moveTask(out, taskId, lift.to, lift.index, NOW);
        const slot = Math.min(lift.index, sizes[lift.to] ?? 0);
        said.push(movedAnnouncement(moving.title, lift.to, slot, total(lift.to)));
        lift = null;
      } else {
        lift = start;
        said.push(liftAnnouncement(moving.title, start, total(start.to)));
      }
      continue;
    }
    if (key === 'Escape') {
      if (!lift) continue;
      lift = null;
      said.push(cancelAnnouncement(moving.title, start, total(start.to)));
      continue;
    }
    if (!isLiftKey(key) || !lift) continue;
    const next = moveLift(lift, key, columns, sizes);
    if (next.to === lift.to && next.index === lift.index) continue;
    lift = next;
    said.push(liftMoveAnnouncement(moving.title, next, total(next.to)));
  }
  return { tasks: out, said, lifted: lift !== null };
}

describe('keyboard move', () => {
  // [a, b, d] in To Do, [c] in Doing — the same board moveTask is tested on.
  const base = [
    task('a', 'todo'),
    task('b', 'todo'),
    task('c', 'doing'),
    task('d', 'todo'),
  ];

  it('picks a card up without moving anything', () => {
    const r = keyboardMove(base, base[0].id, [' ']);
    expect(r.lifted).toBe(true);
    expect(layout(r.tasks)).toEqual(layout(base));
    expect(r.said).toEqual([
      liftAnnouncement('a', { to: 'todo', index: 0 }, 3),
    ]);
  });

  it('moves a card to the next column and announces where it landed', () => {
    const r = keyboardMove(base, base[0].id, [' ', 'ArrowRight', 'Enter']);
    expect(layout(r.tasks)).toEqual({ todo: ['b', 'd'], doing: ['a', 'c'] });
    expect(r.said.at(-1)).toBe('a moved to Doing, position 1 of 2');
    expect(r.lifted).toBe(false);
  });

  it('reorders within a column', () => {
    const r = keyboardMove(base, base[0].id, [
      ' ',
      'ArrowDown',
      'ArrowDown',
      ' ',
    ]);
    expect(layout(r.tasks).todo).toEqual(['b', 'd', 'a']);
    expect(r.said).toEqual([
      liftAnnouncement('a', { to: 'todo', index: 0 }, 3),
      'a, To Do, position 2 of 3',
      'a, To Do, position 3 of 3',
      'a moved to To Do, position 3 of 3',
    ]);
  });

  it('restores the original position on Escape', () => {
    const r = keyboardMove(base, base[0].id, [
      ' ',
      'ArrowRight',
      'ArrowDown',
      'Escape',
    ]);
    expect(layout(r.tasks)).toEqual(layout(base));
    expect(r.lifted).toBe(false);
    expect(r.said.at(-1)).toBe(
      'Move cancelled. a is back in To Do, position 1 of 3.',
    );
  });

  // The card is never taken out of the board until it is dropped, so an
  // arrow press costs no save and Escape has nothing to undo.
  it('commits nothing until the card is dropped', () => {
    const r = keyboardMove(base, base[3].id, [' ', 'ArrowUp', 'ArrowUp']);
    expect(r.tasks).toEqual(base);
    expect(r.lifted).toBe(true);
  });
});

/**
 * A lift is a preview of a move nobody has committed: while it is up, the
 * board draws — and every column and card name reports — an order that is not
 * the saved one. Every key that can commit or cancel it lives on the card, so
 * the moment focus is anywhere else the preview is unreachable. It has to end
 * there rather than sit on screen misreporting the board.
 */
describe('liftSurvivesFocus', () => {
  it('survives while the lifted card itself has focus', () => {
    expect(liftSurvivesFocus('t1', 't1', true)).toBe(true);
  });

  it('ends when Tab moves focus to another card', () => {
    expect(liftSurvivesFocus('t1', 't2', true)).toBe(false);
  });

  it('ends when focus lands inside the lifted card', () => {
    // A link in the description or the chevron: the card's key handler
    // deliberately ignores keys that did not come from the card itself, so a
    // lift left running there answers to nothing.
    expect(liftSurvivesFocus('t1', 't1', false)).toBe(false);
  });

  it('ends when focus leaves the board entirely', () => {
    // A header button (Settings, Export) or anything else off the board.
    expect(liftSurvivesFocus('t1', null, false)).toBe(false);
  });
});

describe('cardLabel', () => {
  it('names the card, its column, its place and its state', () => {
    const t = task('Fix login', 'doing');
    expect(cardLabel(t, 1, 4)).toBe('Fix login, Doing, 2 of 4');
    expect(cardLabel({ ...t, blocked: true }, 1, 4)).toBe(
      'Fix login, Doing, 2 of 4, blocked',
    );
    expect(
      cardLabel(
        {
          ...t,
          checks: [
            { text: 'one', done: true },
            { text: 'two', done: false },
          ],
        },
        0,
        1,
      ),
    ).toBe('Fix login, Doing, 1 of 1, 1 of 2 checklist items done');
  });

  it('says so while the card is lifted', () => {
    expect(cardLabel(task('a', 'todo'), 0, 2, true)).toBe(
      'a, To Do, 1 of 2, lifted',
    );
  });
});

describe('pastThreshold', () => {
  it('ignores the wobble of a tap', () => {
    expect(pastThreshold(0, 0)).toBe(false);
    expect(pastThreshold(4, 4)).toBe(false);
    expect(pastThreshold(-6, 6)).toBe(false);
  });

  it('accepts a real drag in any direction', () => {
    expect(pastThreshold(9, 0)).toBe(true);
    expect(pastThreshold(0, -9)).toBe(true);
    expect(pastThreshold(-7, -7)).toBe(true);
  });

  // Release is measured against the same line as the move that starts the
  // drag: a flick that presses, crosses and releases inside a single frame
  // has no animation frame to mark it active, and would otherwise be dropped
  // as a click on the card.
  it('is the same line for the move that starts a drag and the release', () => {
    const dx = 40;
    const dy = 120;
    expect(pastThreshold(dx, dy)).toBe(true);
  });
});

describe('fullColumnIndex', () => {
  const all = [
    task('one', 'todo'),
    task('two', 'todo'),
    task('three', 'todo'),
    task('elsewhere', 'doing'),
  ];

  it('is the identity when nothing is filtered out', () => {
    const moving = all[2]!.id;
    expect(fullColumnIndex(all, all, moving, 'todo', 0)).toBe(0);
    expect(fullColumnIndex(all, all, moving, 'todo', 1)).toBe(1);
    // Past the last other card: append.
    expect(fullColumnIndex(all, all, moving, 'todo', 2)).toBe(2);
  });

  it('anchors a filtered slot to the card it would land in front of', () => {
    // Only "one" and "three" are rendered; dropping in front of "three" is
    // slot 1 of the visible column but slot 2 of the real one.
    const visible = [all[0]!, all[2]!];
    const moving = all[0]!.id;
    expect(fullColumnIndex(all, visible, moving, 'todo', 0)).toBe(1);
    expect(fullColumnIndex(all, visible, moving, 'todo', 1)).toBe(2);
  });

  it('appends when the drop is past the last rendered card', () => {
    const visible = [all[0]!];
    expect(fullColumnIndex(all, visible, all[0]!.id, 'todo', 0)).toBe(2);
    expect(fullColumnIndex(all, visible, 'unknown', 'todo', 9)).toBe(3);
  });

  it('appends when the rendered anchor is not in the full column at all', () => {
    const ghost = task('ghost', 'todo');
    expect(fullColumnIndex(all, [ghost], all[0]!.id, 'todo', 0)).toBe(2);
  });

  it('measures against the destination column, not the source', () => {
    expect(fullColumnIndex(all, all, all[0]!.id, 'doing', 0)).toBe(0);
    expect(fullColumnIndex(all, all, all[0]!.id, 'doing', 5)).toBe(1);
  });
});

describe('showCancelledFlag', () => {
  it('reads and writes the persisted toggle', () => {
    expect(showCancelledFlag()).toBe(false);
    setShowCancelledFlag(true);
    expect(showCancelledFlag()).toBe(true);
    setShowCancelledFlag(false);
    expect(showCancelledFlag()).toBe(false);
  });

  it('treats an unavailable storage as off rather than throwing', () => {
    const real = (globalThis as { localStorage?: unknown }).localStorage;
    (globalThis as { localStorage?: unknown }).localStorage = {
      getItem() { throw new Error('denied'); },
      setItem() { throw new Error('denied'); },
      removeItem() { throw new Error('denied'); },
    };
    try {
      expect(showCancelledFlag()).toBe(false);
      expect(() => setShowCancelledFlag(true)).not.toThrow();
      expect(() => setShowCancelledFlag(false)).not.toThrow();
    } finally {
      (globalThis as { localStorage?: unknown }).localStorage = real;
    }
  });
});
