import { describe, expect, it } from 'vitest';
import { parse, serialize } from './markdown';
import type { Board } from './model';
import { newTask } from './model';

const DOC = `# Ops Board

## To Do

- [ ] 🚚 Ship crates !1 @2026-07-21 ~L #infra #env::prod
  Coordinate with the warehouse team.
  Confirm the loading dock slot.
  - [ ] pack boxes
  - [x] book truck

## Doing

- [ ] Migrate database

## Done

- [x] Provision servers
`;

function projection(board: Board) {
  return {
    title: board.title,
    tasks: board.tasks.map((t) => ({
      emoji: t.emoji,
      title: t.title,
      desc: t.desc,
      status: t.status,
      prio: t.prio,
      due: t.due,
      effort: t.effort,
      tags: t.tags,
      checks: t.checks,
    })),
  };
}

describe('parse', () => {
  it('parses a full document with title, three columns, and a rich task', () => {
    const board = parse(DOC);

    expect(board.title).toBe('Ops Board');
    expect(board.tasks).toHaveLength(3);

    const rich = board.tasks[0];
    expect(rich.emoji).toBe('🚚');
    expect(rich.title).toBe('Ship crates');
    expect(rich.status).toBe('todo');
    expect(rich.prio).toBe(1);
    expect(rich.due).toBe('2026-07-21');
    expect(rich.effort).toBe('L');
    expect(rich.tags).toEqual(['infra', 'env::prod']);
    expect(rich.desc).toBe(
      'Coordinate with the warehouse team.\nConfirm the loading dock slot.',
    );
    expect(rich.checks).toEqual([
      { text: 'pack boxes', done: false },
      { text: 'book truck', done: true },
    ]);
    expect(rich.id).toBeTruthy();
    expect(rich.createdAt).toBeTruthy();
    expect(rich.movedAt).toBeTruthy();

    const doing = board.tasks[1];
    expect(doing.title).toBe('Migrate database');
    expect(doing.status).toBe('doing');
    expect(doing.emoji).toBe('');
    expect(doing.prio).toBe(3);
    expect(doing.due).toBeUndefined();
    expect(doing.effort).toBeUndefined();
    expect(doing.tags).toEqual([]);
    expect(doing.desc).toBe('');
    expect(doing.checks).toEqual([]);

    const done = board.tasks[2];
    expect(done.title).toBe('Provision servers');
    expect(done.status).toBe('done');
  });

  it('round-trips: parse(serialize(parse(doc))) equals parse(doc) modulo ids/timestamps', () => {
    const first = parse(DOC);
    const second = parse(serialize(first));
    expect(projection(second)).toEqual(projection(first));
  });

  it('leaves invalid tokens (!9, @not-a-date, ~X, bare #) in the title text', () => {
    const board = parse(
      ['# B', '', '## To Do', '', '- [ ] Fix thing !9 @not-a-date ~X #'].join('\n'),
    );
    expect(board.tasks).toHaveLength(1);
    const t = board.tasks[0];
    expect(t.title).toBe('Fix thing !9 @not-a-date ~X #');
    expect(t.prio).toBe(3);
    expect(t.due).toBeUndefined();
    expect(t.effort).toBeUndefined();
    expect(t.tags).toEqual([]);
  });

  it("parses '- [x]' items under any column as status 'done'", () => {
    const board = parse(
      [
        '# B',
        '',
        '## To Do',
        '',
        '- [x] Checked in todo',
        '',
        '## Doing',
        '',
        '- [x] Checked in doing',
      ].join('\n'),
    );
    expect(board.tasks).toHaveLength(2);
    expect(board.tasks[0].title).toBe('Checked in todo');
    expect(board.tasks[0].status).toBe('done');
    expect(board.tasks[1].title).toBe('Checked in doing');
    expect(board.tasks[1].status).toBe('done');
  });

  it("maps an unknown first column header to 'todo' by position", () => {
    const board = parse(
      ['# B', '', '## Backlog', '', '- [ ] Something someday'].join('\n'),
    );
    expect(board.tasks).toHaveLength(1);
    expect(board.tasks[0].title).toBe('Something someday');
    expect(board.tasks[0].status).toBe('todo');
  });
});

describe('escaping', () => {
  it('round-trips metadata-shaped title words (#tag, !prio, ~effort, @due, \\) as text', () => {
    const board: Board = {
      title: 'B',
      tasks: [
        newTask({ title: 'Fix #123 login !2 ~S @2026-01-01 \\raw', status: 'todo' }),
      ],
    };
    const again = parse(serialize(board));
    expect(again.tasks).toHaveLength(1);
    const t = again.tasks[0];
    expect(t.title).toBe('Fix #123 login !2 ~S @2026-01-01 \\raw');
    expect(t.tags).toEqual([]);
    expect(t.prio).toBe(3);
    expect(t.due).toBeUndefined();
    expect(t.effort).toBeUndefined();
    // The wire form is stable across repeated round trips.
    expect(serialize(again)).toBe(serialize(board));
  });

  it('keeps checkbox-shaped description lines in the description', () => {
    const board: Board = {
      title: 'B',
      tasks: [newTask({ title: 'T', desc: 'first\n- [ ] buy milk\n- [x] paid' })],
    };
    const again = parse(serialize(board));
    expect(again.tasks[0].desc).toBe('first\n- [ ] buy milk\n- [x] paid');
    expect(again.tasks[0].checks).toEqual([]);
  });
});
