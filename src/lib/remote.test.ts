import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Board } from './model';
import { newTask } from './model';
import { parse, serialize } from './markdown';
import type { Identity } from './auth';
import { mergeBoards, RemoteStore } from './remote';

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

afterEach(() => {
  vi.unstubAllGlobals();
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
  it('sends the version it last read back as If-Match', async () => {
    const store = new RemoteStore();
    const calls = stubFetch([
      new Response(serialize(board('A')), { headers: { ETag: 'v1' } }),
      new Response(null, { status: 204, headers: { ETag: 'v2' } }),
    ]);
    await store.loadRemote(identity);
    await save(store, board('A', 'C'));
    expect(calls[1]!.method).toBe('PUT');
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
});
