import { describe, expect, it } from 'vitest';
// Shared Go/TS codec fixtures, inlined by Vite so no filesystem access (and no
// @types/node) is needed. See the "shared Go/TS codec fixtures" suite below.
import goldenMd from '../../internal/board/testdata/golden.md?raw';
import goldenJson from '../../internal/board/testdata/golden.json?raw';
import phase3Md from '../../internal/board/testdata/phase3.md?raw';
import phase3Json from '../../internal/board/testdata/phase3.json?raw';
import { parse, serialize, wireTasks } from './markdown';
import type { Board, Task } from './model';
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
      blocked: t.blocked,
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

  it('maps unknown headers by position, saturating at cancelled', () => {
    const board = parse(
      [
        '# B',
        '',
        '## Backlog',
        '',
        '- [ ] a',
        '',
        '## In Progress',
        '',
        '- [ ] b',
        '',
        '## Shipped',
        '',
        '- [ ] c',
        '',
        '## Overflow',
        '',
        '- [ ] d',
        '',
        '## Beyond',
        '',
        '- [ ] e',
      ].join('\n'),
    );
    expect(board.tasks.map((t) => t.status)).toEqual([
      'todo',
      'doing',
      'done',
      'cancelled',
      'cancelled',
    ]);
  });
});

describe('wireTasks', () => {
  it('matches canonical markdown order for interleaved statuses and duplicate titles', () => {
    const tasks = [
      newTask({
        id: 'done-1',
        title: 'Duplicate',
        desc: 'done first',
        status: 'done',
      }),
      newTask({
        id: 'todo-1',
        title: 'Duplicate',
        desc: 'todo first',
        status: 'todo',
      }),
      newTask({
        id: 'cancelled-1',
        title: 'Cancelled',
        desc: 'cancelled',
        status: 'cancelled',
      }),
      newTask({
        id: 'doing-1',
        title: 'Doing',
        desc: 'doing',
        status: 'doing',
      }),
      newTask({
        id: 'todo-2',
        title: 'Duplicate',
        desc: 'todo second',
        status: 'todo',
      }),
      newTask({
        id: 'done-2',
        title: 'Duplicate',
        desc: 'done second',
        status: 'done',
      }),
    ];
    const board: Board = { title: 'B', tasks };

    const ordered = wireTasks(board);
    expect(ordered.map((t) => t.id)).toEqual([
      'todo-1',
      'todo-2',
      'doing-1',
      'done-1',
      'done-2',
      'cancelled-1',
    ]);
    // parse() is a line-for-line port of the Go parser. Its task slice proves
    // the helper follows the exact order the server receives from serialize().
    expect(
      parse(serialize(board)).tasks.map((t) => [t.status, t.title, t.desc]),
    ).toEqual(ordered.map((t) => [t.status, t.title, t.desc]));
  });
});

describe('blocked', () => {
  it('serializes %blocked only when set and parses it back', () => {
    const board: Board = {
      title: 'B',
      tasks: [
        newTask({
          title: 'Waiting on legal',
          status: 'doing',
          blocked: true,
          effort: 'M',
          tags: ['infra'],
        }),
        newTask({ title: 'Free to go', status: 'doing' }),
      ],
    };
    const wire = serialize(board);
    expect(wire).toContain('- [ ] Waiting on legal ~M %blocked #infra\n');
    expect(wire).not.toContain('Free to go %blocked');
    const again = parse(wire);
    expect(again.tasks[0].blocked).toBe(true);
    expect(again.tasks[0].title).toBe('Waiting on legal');
    expect(again.tasks[1].blocked).toBe(false);
  });

  it('keeps a literal %blocked title word out of the flag', () => {
    const titles = ['%blocked', 'Why %blocked matters', '\\%blocked', '%blockedish'];
    for (const title of titles) {
      const again = parse(serialize({ title: 'B', tasks: [newTask({ title })] }));
      expect(again.tasks).toHaveLength(1);
      expect(again.tasks[0].title).toBe(title);
      expect(again.tasks[0].blocked).toBe(false);
    }
  });
});

describe('cancelled', () => {
  it('emits the Cancelled section after Done and parses it back', () => {
    const board: Board = {
      title: 'B',
      tasks: [
        newTask({ title: 'Landed', status: 'done' }),
        newTask({ title: 'Dropped', status: 'cancelled' }),
      ],
    };
    const wire = serialize(board);
    expect(wire).toBe(
      '# B\n\n## To Do\n\n\n## Doing\n\n\n## Done\n\n- [x] Landed\n\n## Cancelled\n\n- [ ] Dropped\n',
    );
    const again = parse(wire);
    expect(again.tasks[1].status).toBe('cancelled');
    expect(projection(parse(serialize(again)))).toEqual(projection(again));
  });

  it('leaves a legacy three-section board byte-identical', () => {
    const wire = serialize(parse(DOC));
    expect(wire).toBe(DOC);
    expect(wire).not.toContain('Cancelled');
  });
});

// The internal/board/testdata/<name>.md + <name>.json pairs are the shared
// codec fixtures: internal/board/fixtures_test.go reads the very same files and
// asserts the very same two properties. If the TypeScript and Go codecs ever
// drift apart, one of the two suites fails on the fixture the other still
// passes.
//
//   golden — a legacy three-section board (no Cancelled section, no %blocked)
//   phase3 — the phase-3 grammar: %blocked, an escaped literal "%blocked" title
//            word, a Cancelled section, unicode titles, and every
//            metadata-shaped word escaped as title text
const SHARED_FIXTURES: { name: string; md: string; json: string }[] = [
  { name: 'golden', md: goldenMd, json: goldenJson },
  { name: 'phase3', md: phase3Md, json: phase3Json },
];

/** projection() in the shape the shared JSON fixtures use (absent = ''). */
function fixtureProjection(board: Board) {
  return {
    title: board.title,
    tasks: board.tasks.map((t) => ({
      emoji: t.emoji,
      title: t.title,
      desc: t.desc,
      status: t.status,
      blocked: t.blocked,
      prio: t.prio,
      due: t.due ?? '',
      effort: t.effort ?? '',
      tags: t.tags,
      checks: t.checks,
    })),
  };
}

describe('shared Go/TS codec fixtures', () => {
  for (const { name, md, json } of SHARED_FIXTURES) {
    it(`${name}.md is canonical`, () => {
      expect(serialize(parse(md))).toBe(md);
    });

    it(`${name}.md parses to ${name}.json`, () => {
      expect(fixtureProjection(parse(md))).toEqual(JSON.parse(json));
    });
  }
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

  // The codec side of the empty-title fix (store.ValidateTaskFields): as long
  // as a title is not blank, the serialized line is one parse() reads back as
  // a task rather than as description text grafted onto the task before it.
  it('serializes every non-blank task to a line parse() reads back', () => {
    const tasks: Task[] = [
      newTask({ title: 'plain' }),
      newTask({ title: '0', prio: 4 }),
      newTask({ title: '\\' }),
      newTask({ title: '%blocked', blocked: true }),
      newTask({ title: '- [x] forged' }),
      newTask({
        title: '#tag !1 ~S @2026-01-01',
        status: 'doing',
        prio: 2,
        blocked: true,
      }),
      newTask({
        title: '日本語 café 🚀',
        emoji: '🔥',
        status: 'done',
        prio: 1,
        due: '2026-02-03',
        effort: 'L',
        tags: ['a', 'k::v'],
      }),
      newTask({
        title: 'cancelled one',
        status: 'cancelled',
        checks: [{ text: 'step', done: true }],
      }),
    ];
    for (const task of tasks) {
      const board: Board = {
        title: 'B',
        tasks: [newTask({ title: 'anchor', status: task.status }), task],
      };
      const again = parse(serialize(board));
      expect(again.tasks).toHaveLength(2);
      expect(projection(again).tasks[1]).toEqual(projection(board).tasks[1]);
    }
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

  it('parses single-line, multi-line, and bullet descriptions from a server payload', () => {
    // Captured verbatim from a live kb server's GET /api/board.
    const wire = `# Board

## To Do

- [ ] Multi
  para one
  para two
- [ ] Bullets
  - item one
  - item two

## Doing


## Done
`;
    const board = parse(wire);
    expect(board.tasks.map((t) => [t.title, t.desc, t.checks.length])).toEqual([
      ['Multi', 'para one\npara two', 0],
      ['Bullets', '- item one\n- item two', 0],
    ]);
  });
});
