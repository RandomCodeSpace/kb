import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Board } from './model';
import { newTask } from './model';
import { parse, serialize, wireTasks } from './markdown';
import type { Identity } from './auth';
import { mergeBoards, mergeTaskIDMaps, RemoteStore } from './remote';

const identity: Identity = { kind: 'manual', id: 'alice' };

function board(...titles: string[]): Board {
  return { title: 'kb', tasks: titles.map((title) => newTask({ title })) };
}

function titles(b: Board): string[] {
  return b.tasks.map((t) => t.title);
}

type Call = { url: string; method: string; headers: Record<string, string>; body: string };

/** fetch stub answering a fixed script of responses, recording every call. */
function stubFetch(script: Response[]): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url,
        method: init?.method ?? 'GET',
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: typeof init?.body === 'string' ? init.body : '',
      });
      const res = script.shift();
      if (!res) throw new Error(`unexpected fetch: ${init?.method ?? 'GET'} ${url}`);
      return Promise.resolve(res);
    }),
  );
  return calls;
}

/** saveRemote is fire-and-forget; resolve when it reports back. */
function save(store: RemoteStore, b: Board): Promise<Board> {
  return new Promise<Board>((resolve, reject) => {
    store.saveRemote(identity, b, reject, resolve);
  });
}

type SaveResult = {
  pushed: Board;
  taskIDs: ReadonlyMap<string, string>;
};

function saveWithTaskIDs(store: RemoteStore, b: Board): Promise<SaveResult> {
  return new Promise<SaveResult>((resolve, reject) => {
    store.saveRemote(identity, b, reject, (pushed, taskIDs) => {
      resolve({ pushed, taskIDs });
    });
  });
}

function remoteSnapshot(
  b: Board,
  taskIDs: readonly string[],
  etag = 'v1',
): Response {
  return new Response(
    JSON.stringify({ board: serialize(b), task_ids: taskIDs }),
    {
      headers: {
        'Content-Type': 'application/json',
        ETag: etag,
      },
    },
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

// A write acknowledgement contains only that write's identities; replacing
// the prior map makes older cards start self-matching again.
describe('mergeTaskIDMaps', () => {
  it('adds later acknowledgements without dropping earlier mappings', () => {
    const earlier = new Map([
      ['client-a', 'server-a'],
      ['client-b', 'server-b'],
    ]);

    const merged = mergeTaskIDMaps(
      earlier,
      new Map([
        ['client-b', 'server-b-new'],
        ['client-c', 'server-c'],
      ]),
    );

    expect([...merged]).toEqual([
      ['client-a', 'server-a'],
      ['client-b', 'server-b-new'],
      ['client-c', 'server-c'],
    ]);
    expect([...earlier]).toEqual([
      ['client-a', 'server-a'],
      ['client-b', 'server-b'],
    ]);
  });
});

describe('mergeBoards', () => {
  it('keeps every server task the client has never seen', () => {
    const local = board('A', 'C');
    const server = board('A', 'B');
    const merged = mergeBoards(local, server, board('A'));
    expect(titles(merged)).toEqual(['A', 'C', 'B']);
  });

  it('honours a local delete of a task the client had seen', () => {
    const merged = mergeBoards(board('A'), board('A', 'D'), board('A', 'D'));
    expect(titles(merged)).toEqual(['A']);
  });

  it('keeps everything when there is no merge base', () => {
    const merged = mergeBoards(board('A'), board('B'), null);
    expect(titles(merged)).toEqual(['A', 'B']);
  });

  it('does not duplicate a task both sides already have', () => {
    const merged = mergeBoards(board('A'), board('A'), null);
    expect(titles(merged)).toEqual(['A']);
  });

  it('returns the local board unchanged when nothing is new', () => {
    const local = board('A');
    expect(mergeBoards(local, board('A'), board('A'))).toBe(local);
  });

  it('takes a column move by another surface instead of duplicating the card', () => {
    // `kb done A` while the SPA renamed an unrelated card: A must end up in
    // Done once, not once in each column.
    const base = board('A', 'B');
    const local = { ...base, tasks: [base.tasks[0]!, { ...base.tasks[1]!, title: 'B2' }] };
    const server = {
      ...base,
      tasks: [base.tasks[1]!, { ...base.tasks[0]!, status: 'done' as const }],
    };
    const merged = mergeBoards(local, server, base);
    expect(titles(merged)).toEqual(['A', 'B2']);
    expect(merged.tasks.map((t) => t.status)).toEqual(['done', 'todo']);
    // And the board that goes back on the wire holds one copy of each card.
    expect(titles(parse(serialize(merged)))).toEqual(['B2', 'A']);
  });

  it('keeps our own column move when we moved the card ourselves', () => {
    const base = board('A');
    const local = { ...base, tasks: [{ ...base.tasks[0]!, status: 'doing' as const }] };
    const server = { ...base, tasks: [{ ...base.tasks[0]!, status: 'done' as const }] };
    const merged = mergeBoards(local, server, base);
    expect(merged.tasks.map((t) => t.status)).toEqual(['doing']);
  });
});

describe('RemoteStore concurrency', () => {
  // Previously only PUTs acknowledged canonical ids, leaving a freshly loaded
  // card able to match itself until somebody edited and saved it once.
  it('maps a freshly loaded board to canonical ids before its first edit', async () => {
    const store = new RemoteStore();
    const serverBoard: Board = {
      title: 'kb',
      tasks: [
        newTask({ title: 'Duplicate', status: 'done' }),
        newTask({ title: 'Duplicate', status: 'todo' }),
        newTask({ title: 'Doing other', status: 'doing' }),
      ],
    };
    const canonical = ['todo-server', 'doing-server', 'done-server'];
    const calls = stubFetch([remoteSnapshot(serverBoard, canonical)]);
    let loadedIDs: ReadonlyMap<string, string> | undefined;

    const loaded = await store.loadRemote(identity, (taskIDs) => {
      loadedIDs = taskIDs;
    });

    expect(calls[0]!.headers.Accept).toBe('application/json');
    expect(loaded).not.toBeNull();
    expect(loadedIDs).toBeDefined();
    expect([...loadedIDs!]).toEqual(
      wireTasks(loaded!).map((task, index) => [task.id, canonical[index]!]),
    );
  });

  // A corrupt positional map could suppress an unrelated card, which is worse
  // than leaving a duplicate advisory visible.
  it.each([
    ['invalid JSON', '{'],
    ['a missing board', '{"task_ids":[]}'],
    ['a non-string board', '{"board":7,"task_ids":[]}'],
    [
      'a task-id count mismatch',
      JSON.stringify({ board: serialize(board('A')), task_ids: [] }),
    ],
    [
      'duplicate canonical ids',
      JSON.stringify({
        board: serialize(board('A', 'B')),
        task_ids: ['server-a', 'server-a'],
      }),
    ],
  ])('rejects a JSON board snapshot with %s', async (_case, bodyText) => {
    const store = new RemoteStore();
    stubFetch([
      new Response(bodyText, {
        headers: { 'Content-Type': 'application/json' },
      }),
    ]);

    await expect(store.loadRemote(identity)).rejects.toThrow(
      'GET /api/board returned invalid board snapshot',
    );
  });

  it('sends the version it last read back as If-Match', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response(null, { status: 204, headers: { ETag: 'v2' } }),
    ]);
    await store.loadRemote(identity);
    await save(store, board('A', 'C'));
    expect(calls[1]!.method).toBe('PUT');
    expect(calls[1]!.headers.Accept).toBe('application/json');
    expect(calls[1]!.headers['If-Match']).toBe('v1');
  });

  it('omits If-Match when no version is known yet', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([new Response(null, { status: 204 })]);
    await save(store, board('A'));
    expect(calls[0]!.headers['If-Match']).toBeUndefined();
  });

  it('keeps the version a 404 carried, so the first write is still conditional', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response('no board saved', { status: 404, headers: { ETag: 'v0' } }),
      new Response('conflict', { status: 409 }),
      // Another surface created the board between our GET and our PUT.
      new Response(serialize(board('from the CLI')), { headers: { ETag: 'v1' } }),
      new Response(null, { status: 204, headers: { ETag: 'v2' } }),
    ]);
    expect(await store.loadRemote(identity)).toBeNull();
    const pushed = await save(store, board('demo card'));
    expect(calls[1]!.headers['If-Match']).toBe('v0');
    // The unseen task survived instead of being replaced by our seed board.
    expect(titles(pushed)).toEqual(['demo card', 'from the CLI']);
  });

  it('refetches, merges and retries once on 409 without dropping an unseen task', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response('conflict', { status: 409 }),
      // Another client added B in the meantime.
      new Response(serialize(board('A', 'B')), { headers: { ETag: 'v2' } }),
      new Response(null, { status: 204, headers: { ETag: 'v3' } }),
    ]);
    await store.loadRemote(identity);
    const pushed = await save(store, board('A', 'C'));

    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'GET /api/board',
      'PUT /api/board',
      'GET /api/board',
      'PUT /api/board',
    ]);
    // The retry carries the version it merged against …
    expect(calls[3]!.headers['If-Match']).toBe('v2');
    // … and the body keeps the local edit and the task we had never seen.
    expect(titles(parse(calls[3]!.body))).toEqual(['A', 'C', 'B']);
    expect(titles(pushed)).toEqual(['A', 'C', 'B']);
  });

  it('retries only once — a second conflict is reported', async () => {
    const store = new RemoteStore();
    stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response('conflict', { status: 409 }),
      new Response(serialize(board('A', 'B')), { headers: { ETag: 'v2' } }),
      new Response('conflict', { status: 409 }),
    ]);
    await store.loadRemote(identity);
    await expect(save(store, board('A', 'C'))).rejects.toThrow('409');
  });

  it('pushes the local board when the server lost its copy mid-conflict', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response('conflict', { status: 409 }),
      new Response('', { status: 404 }),
      new Response(null, { status: 204 }),
    ]);
    await store.loadRemote(identity);
    const pushed = await save(store, board('A', 'C'));
    expect(titles(pushed)).toEqual(['A', 'C']);
    expect(calls[3]!.headers['If-Match']).toBeUndefined();
  });

  it('adopts the version echoed by a successful PUT', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(null, { status: 204, headers: { ETag: 'v9' } }),
      new Response(null, { status: 204 }),
    ]);
    await save(store, board('A'));
    await save(store, board('A', 'B'));
    expect(calls[1]!.headers['If-Match']).toBe('v9');
  });

  it('maps duplicate-title client ids to canonical ids by wire position', async () => {
    const store = new RemoteStore();
    const pushed: Board = {
      title: 'kb',
      tasks: [
        newTask({ id: 'done-client', title: 'Duplicate', status: 'done' }),
        newTask({ id: 'todo-client-1', title: 'Duplicate', status: 'todo' }),
        newTask({ id: 'doing-client', title: 'Other', status: 'doing' }),
        newTask({ id: 'todo-client-2', title: 'Duplicate', status: 'todo' }),
      ],
    };
    const canonical = [
      'todo-server-1',
      'todo-server-2',
      'doing-server',
      'done-server',
    ];
    stubFetch([
      new Response(JSON.stringify({ task_ids: canonical }), {
        status: 200,
        headers: { ETag: 'v1' },
      }),
    ]);

    const saved = await saveWithTaskIDs(store, pushed);

    expect(saved.pushed).toBe(pushed);
    expect(saved.pushed.tasks.map((t) => t.id)).toEqual([
      'done-client',
      'todo-client-1',
      'doing-client',
      'todo-client-2',
    ]);
    expect([...saved.taskIDs]).toEqual(
      wireTasks(pushed).map((task, i) => [task.id, canonical[i]!]),
    );
  });

  it('accepts a legacy 204 response with an empty task-id map', async () => {
    const store = new RemoteStore();
    stubFetch([new Response(null, { status: 204 })]);

    const saved = await saveWithTaskIDs(store, board('A'));

    expect([...saved.taskIDs]).toEqual([]);
  });

  it.each([
    ['invalid JSON', '{'],
    ['a null body', 'null'],
    ['an array body', '[]'],
    ['a missing task_ids field', '{}'],
    ['a non-array task_ids field', '{"task_ids":"server-a"}'],
    ['too few task ids', '{"task_ids":["server-a"]}'],
    [
      'too many task ids',
      '{"task_ids":["server-a","server-b","server-c"]}',
    ],
    ['a non-string task id', '{"task_ids":["server-a",42]}'],
    ['an empty task id', '{"task_ids":["server-a",""]}'],
    ['a whitespace task id', '{"task_ids":["server-a","   "]}'],
    [
      'duplicate task ids',
      '{"task_ids":["server-a","server-a"]}',
    ],
  ])('rejects negotiated 200 JSON with %s', async (_case, bodyText) => {
    const store = new RemoteStore();
    stubFetch([new Response(bodyText, { status: 200 })]);

    await expect(saveWithTaskIDs(store, board('A', 'B'))).rejects.toThrow(
      'PUT /api/board returned invalid task ids',
    );
  });

  it('rejects an unexpected successful PUT status', async () => {
    const store = new RemoteStore();
    stubFetch([new Response(null, { status: 202 })]);

    await expect(saveWithTaskIDs(store, board('A'))).rejects.toThrow(
      'PUT /api/board failed: 202',
    );
  });

  it('maps a 409 retry against the merged board that was actually pushed', async () => {
    const store = new RemoteStore();
    stubFetch([
      remoteSnapshot(board('A'), ['server-a'], 'v1'),
      new Response('conflict', { status: 409 }),
      remoteSnapshot(board('A', 'B'), ['server-a', 'server-b'], 'v2'),
      new Response(
        JSON.stringify({
          task_ids: ['server-a', 'server-c', 'server-b'],
        }),
        { status: 200, headers: { ETag: 'v3' } },
      ),
    ]);
    await store.loadRemote(identity);

    const saved = await saveWithTaskIDs(store, board('A', 'C'));

    expect(titles(saved.pushed)).toEqual(['A', 'C', 'B']);
    expect([...saved.taskIDs]).toEqual(
      wireTasks(saved.pushed).map((task, i) => [
        task.id,
        ['server-a', 'server-c', 'server-b'][i]!,
      ]),
    );
  });
});
