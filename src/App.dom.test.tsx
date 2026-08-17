// @vitest-environment jsdom

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Status, Task, TaskDraft } from './lib/model';
import type { Identity } from './lib/auth';

/**
 * The server, in memory. Every test drives the real App against it, so the
 * assertions are about what the SPA sends and what it does with what comes
 * back — there is no local board left to assert against.
 */
const state = vi.hoisted(() => ({
  identity: null as Identity | null,
  authMode: 'unknown' as 'open' | 'token' | 'entra' | 'unknown',
  localDisplayName: '',
  resolveIdentity: null as Identity | null,
  resolveError: null as unknown,
  resolveDeferred: null as Promise<Identity> | null,
  resolveCalls: 0,
  present: true,
  tasks: [] as Task[],
  created: 0,
  calls: [] as string[],
  listError: null as unknown,
  writeError: null as unknown,
  // Per-draft create failures, by title. Unlike writeError these are not
  // consumed on the first throw, so one create in a batch can fail while the
  // rest are watched for.
  createErrors: {} as Record<string, unknown>,
  tombstoneError: null as unknown,
  linksError: null as unknown,
  tombstones: [] as Array<{ taskId: string; reason: string }>,
  labels: [] as string[],
  settings: { has_key: false, ai_base_url: '' },
  settingsError: null as unknown,
  sources: [] as Array<{ name: string; kind: 'github' | 'gitlab' }>,
  integrationsError: null as unknown,
  createHold: null as Promise<void> | null,
  savedIdentities: [] as Identity[],
  clearedIdentity: 0,
  exported: [] as string[],
  imported: [] as string[],
  importText: '# Imported\n\n## To Do\n\n- [ ] Imported markdown\n',
  importError: false,
  bursts: [] as Array<[number, number, number]>,
}));

const manual: Identity = { kind: 'manual', id: 'alice' };
const STATUS_ORDER: Status[] = ['todo', 'doing', 'done', 'cancelled'];
const MOVED_AT = '2026-08-13T00:00:00.000Z';

function seed(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    emoji: 'x',
    title: 'First task',
    desc: 'details',
    status: 'todo',
    blocked: false,
    prio: 2,
    tags: ['local'],
    checks: [{ text: 'verify', done: false }],
    createdAt: '2026-08-01T00:00:00.000Z',
    movedAt: '2026-08-01T00:00:00.000Z',
    ...overrides,
  };
}

vi.mock('./lib/auth', () => {
  class ReauthRequiredError extends Error {}
  return {
    ReauthRequiredError,
    loadIdentity: () => state.identity,
    saveIdentity: (identity: Identity) => {
      state.savedIdentities.push(identity);
      state.identity = identity;
    },
    clearIdentity: () => {
      state.clearedIdentity += 1;
      state.identity = null;
    },
    resolveAzureIdentity: async () => {
      state.resolveCalls += 1;
      if (state.resolveDeferred) return state.resolveDeferred;
      if (state.resolveError) throw state.resolveError;
      if (!state.resolveIdentity) throw new Error('no restored identity configured');
      return state.resolveIdentity;
    },
    identityNamespace: (identity: Identity) =>
      identity.kind === 'azure' ? `azure.${identity.homeAccountId ?? 'pending'}` : identity.id,
    displayName: (identity: Identity) => identity.name || identity.id,
    serverAuthMode: async () => state.authMode,
    // Mirrors the real bootAction contract; the real one is unit-tested in
    // auth.test.ts, this copy keeps the mocked module self-contained.
    bootAction: (mode: string, gateRequested: boolean) =>
      gateRequested || mode !== 'open'
        ? { kind: 'gate' }
        : { kind: 'adopt', identity: { kind: 'manual', id: 'default' } },
    loadLocalDisplayName: () => state.localDisplayName,
    saveLocalDisplayName: (name: string) => {
      state.localDisplayName = name;
    },
  };
});

vi.mock('./lib/api', async () => {
  const { ReauthRequiredError } = await import('./lib/auth');
  class TaskRequestError extends Error {
    constructor(message: string, readonly status: number) {
      super(message);
    }
  }
  const find = (id: string): Task => {
    const task = state.tasks.find((t) => t.id === id);
    if (!task) throw new TaskRequestError('task not found', 404);
    return task;
  };
  const place = (moving: Task, to: Status, index: number | undefined) => {
    const rest = state.tasks.filter((t) => t.id !== moving.id);
    const column = rest.filter((t) => t.status === to);
    const slot = Math.max(0, Math.min(index ?? column.length, column.length));
    const at = slot === column.length ? rest.length : rest.indexOf(column[slot]!);
    rest.splice(at, 0, {
      ...moving,
      status: to,
      movedAt: moving.status === to ? moving.movedAt : MOVED_AT,
    });
    state.tasks = rest;
  };
  const raise = (error: unknown) => {
    state.writeError = null;
    throw error;
  };
  return {
    TaskRequestError,
    detect: async () => state.present,
    listTasks: async () => {
      state.calls.push('list');
      if (state.listError) {
        const error = state.listError;
        state.listError = null;
        throw error;
      }
      return STATUS_ORDER.flatMap((status) =>
        state.tasks.filter((t) => t.status === status).map((t) => ({ ...t })),
      );
    },
    createTask: async (_identity: Identity, draft: TaskDraft) => {
      state.calls.push(`create:${draft.title}`);
      if (state.createHold) await state.createHold;
      const refused = state.createErrors[draft.title];
      if (refused) throw refused;
      if (state.writeError) raise(state.writeError);
      state.created += 1;
      const created: Task = {
        ...draft,
        id: `server-${state.created}`,
        createdAt: MOVED_AT,
        movedAt: MOVED_AT,
      };
      state.tasks = [...state.tasks, created];
      place(created, created.status, undefined);
      return created;
    },
    patchTask: async (_identity: Identity, id: string, patch: Record<string, unknown>) => {
      state.calls.push(`patch:${id}:${JSON.stringify(patch)}`);
      if (state.writeError) raise(state.writeError);
      const current = find(id);
      const next: Task = { ...current };
      for (const key of ['title', 'emoji', 'desc', 'blocked', 'prio', 'tags', 'checks'] as const) {
        if (patch[key] !== undefined) Object.assign(next, { [key]: patch[key] });
      }
      const status = patch.status as Status | undefined;
      if (status === 'done' && patch.force !== true) {
        const open = next.checks.some((c) => !c.done);
        if (open || next.blocked) {
          throw new TaskRequestError('checklist items are still open', 409);
        }
      }
      state.tasks = state.tasks.map((t) => (t.id === id ? next : t));
      if (status !== undefined || patch.index !== undefined) {
        place(next, status ?? next.status, patch.index as number | undefined);
      }
      return { ...next };
    },
    deleteTask: async (_identity: Identity, id: string) => {
      state.calls.push(`delete:${id}`);
      if (state.writeError) raise(state.writeError);
      const task = find(id);
      state.tasks = state.tasks.filter((t) => t.id !== id);
      return task;
    },
    replaceBoard: async (_identity: Identity, markdown: string) => {
      state.calls.push('replace');
      if (state.writeError) raise(state.writeError);
      state.imported.push(markdown);
      state.tasks = [seed({ id: 'replaced', title: 'Imported markdown', checks: [] })];
    },
    recordTombstone: async (_identity: Identity, taskId: string, reason: string) => {
      if (state.tombstoneError) {
        const error = state.tombstoneError;
        state.tombstoneError = null;
        throw error;
      }
      state.tombstones.push({ taskId, reason });
    },
    recordImportLinks: async () => {
      if (state.linksError) {
        const error = state.linksError;
        state.linksError = null;
        throw error;
      }
    },
    getLabels: async () => state.labels,
    getSettings: async () => {
      if (state.settingsError) {
        const error = state.settingsError;
        state.settingsError = null;
        throw error;
      }
      return state.settings;
    },
    getIntegrations: async () => {
      if (state.integrationsError) {
        const error = state.integrationsError;
        state.integrationsError = null;
        throw error;
      }
      return state.sources;
    },
    aiStory: vi.fn(async () => ({ title: 'AI draft' })),
    aiStories: vi.fn(async () => [{ title: 'AI story' }]),
    importPreview: vi.fn(async () => ({ drafts: [] })),
    killReasonRequest: (taskId: string, reason: string) =>
      reason.trim() ? { taskId, reason: reason.trim() } : null,
    ReauthRequiredError,
  };
});

vi.mock('./lib/confetti', () => ({
  burst: (x: number, y: number, count: number) => state.bursts.push([x, y, count]),
}));

vi.mock('./components/Board', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./components/Board')>();
  return {
    ...actual,
    BoardView: ({
      board: value,
      onMove,
      onTick,
      onEdit,
      onAdd,
      onRestore,
      onPurge,
      onTagClick,
    }: {
      board: { tasks: Task[] };
      onMove: (id: string, status: Status, index: number) => void;
      onTick: (id: string, index: number, pos: { x: number; y: number }) => void;
      onEdit: (id: string) => void;
      onAdd: (status: Status) => void;
      onRestore: (id: string) => void;
      onPurge: (id: string) => void;
      onTagClick?: (tag: string) => void;
    }) => (
      <section aria-label="board-double">
        <button onClick={() => onAdd('todo')}>add todo</button>
        <button onClick={() => onEdit('missing')}>edit missing</button>
        <button onClick={() => onMove('missing', 'done', 0)}>move missing</button>
        <button onClick={() => onTick('missing', 0, { x: 0, y: 0 })}>tick missing</button>
        {value.tasks.map((item, position) => (
          <div
            key={item.id}
            data-testid={`task-${item.id}`}
            // A card with no DOM node is the case the confetti origin has to
            // survive; 'ghost' stands in for one scrolled out of the tree.
            {...(item.id === 'ghost' ? {} : { 'data-task': item.id })}
          >
            <span>{item.title}:{item.status}:{item.checks.map((check) => String(check.done)).join(',')}</span>
            <button onClick={() => onEdit(item.id)}>edit {item.id}</button>
            <button onClick={() => onMove(item.id, 'doing', 0)}>move doing {item.id}</button>
            <button onClick={() => onMove(item.id, 'done', 0)}>move done {item.id}</button>
            <button onClick={() => onMove(item.id, item.status, position)}>keep {item.id} in place</button>
            <button onClick={() => onTick(item.id, 0, { x: 2, y: 3 })}>tick {item.id}</button>
            <button onClick={() => onTick(item.id, 99, { x: 2, y: 3 })}>tick invalid {item.id}</button>
            <button onClick={() => onRestore(item.id)}>restore {item.id}</button>
            <button onClick={() => onPurge(item.id)}>purge {item.id}</button>
            <button onClick={() => onTagClick?.(item.tags[0] ?? '')}>tag {item.id}</button>
          </div>
        ))}
      </section>
    ),
  };
});

vi.mock('./components/IdentityGate', () => ({
  IdentityGate: ({ onIdentity }: { onIdentity: (identity: Identity) => void }) => (
    <button onClick={() => onIdentity(manual)}>use manual identity</button>
  ),
}));

vi.mock('./components/CardModal', () => ({
  CardModal: ({ state: modal, onSave, onDelete, onClose, onDirty, aiDraft }: {
    state: { mode: 'add'; status: Status } | { mode: 'edit'; task: Task };
    onSave: (save: import('./components/CardEditor').CardSave) => void;
    onDelete: (id: string) => void;
    onClose: () => void;
    onDirty?: () => void;
    aiDraft?: unknown;
  }) => (
    <div role="dialog" aria-label="card modal">
      <span>{aiDraft ? 'ai enabled' : 'ai disabled'}</span>
      {typeof aiDraft === 'function' && <button onClick={() => void aiDraft({ title: 'draft' })}>request ai draft</button>}
      <button onClick={() => onSave(
        modal.mode === 'edit'
          ? { mode: 'edit', taskId: modal.task.id, patch: { title: `${modal.task.title} edited` } }
          : {
            mode: 'add',
            draft: {
              emoji: '', title: 'Added task', desc: '', status: modal.status,
              blocked: false, prio: 3, tags: [], checks: [],
            },
          },
      )}>save card</button>
      {modal.mode === 'edit' && <button onClick={() => onDelete(modal.task.id)}>delete card</button>}
      {onDirty && <button onClick={onDirty}>mark dirty</button>}
      <button onClick={onClose}>close card</button>
    </div>
  ),
}));

vi.mock('./components/ShipDialog', () => ({
  shipWarning: (item: Task) => item.blocked || item.checks.some((check) => !check.done) ? 'warning' : null,
  ShipDialog: ({ onShip, onTickAll, onCancel }: {
    onShip: () => void; onTickAll: () => void; onCancel: () => void;
  }) => <div role="dialog" aria-label="ship"><button onClick={onShip}>ship anyway</button><button onClick={onTickAll}>tick all</button><button onClick={onCancel}>cancel ship</button></div>,
}));

vi.mock('./components/ConfirmDialog', () => ({
  ConfirmDialog: ({ title, onConfirm, onSecondary, onClose }: {
    title: string; onConfirm?: (value: string) => void; onSecondary?: () => void; onClose: () => void;
  }) => <div role="dialog" aria-label={title}><input aria-label="confirm input" defaultValue="not needed" /><button onClick={(event) => onConfirm?.((event.currentTarget.parentElement?.querySelector('input') as HTMLInputElement).value)}>confirm action</button>{onSecondary && <button onClick={onSecondary}>secondary action</button>}<button onClick={onClose}>close confirm</button></div>,
}));

vi.mock('./components/SettingsModal', () => ({
  SettingsModal: ({ onSaved, onDebugChange, onClose, serverPresent, displayNameValue, onDisplayNameChange }: {
    onSaved: (value: { has_key: boolean; ai_base_url: string }) => void;
    onDebugChange: (value: boolean) => void;
    onClose: () => void;
    serverPresent: boolean;
    displayNameValue: string;
    onDisplayNameChange: (name: string) => void;
  }) => <div role="dialog" aria-label="settings"><span>{String(serverPresent)}</span><span>name:{displayNameValue}</span><button onClick={() => onSaved({ has_key: true, ai_base_url: '' })}>enable ai</button><button onClick={() => onDebugChange(true)}>enable debug</button><button onClick={() => onDisplayNameChange('Amit K')}>set display name</button><button onClick={onClose}>close settings</button></div>,
}));

const draft = (title: string): TaskDraft => ({
  emoji: '', title, desc: '', status: 'todo', blocked: false, prio: 3, tags: [], checks: [],
});

vi.mock('./components/AdrModal', () => ({
  AdrModal: ({ onSplit, onAdd, onClose }: {
    onSplit: (request: unknown, signal?: AbortSignal) => Promise<unknown>;
    onAdd: (drafts: TaskDraft[]) => Promise<ReadonlySet<string>>;
    onClose: () => void;
  }) => <div role="dialog" aria-label="adr"><button onClick={() => void onSplit({ text: 'adr' }, new AbortController().signal)}>split adr request</button><button onClick={() => onAdd([])}>add no adr stories</button><button onClick={() => onAdd([draft('ADR story'), draft('ADR story two')])}>add adr stories</button><button onClick={onClose}>close adr</button></div>,
}));

vi.mock('./components/ImportModal', () => ({
  ImportModal: ({ onPreview, onAdd, onCommitLinks, onClose }: {
    onPreview: (request: unknown, signal?: AbortSignal) => Promise<unknown>;
    onAdd: (drafts: TaskDraft[]) => Promise<ReadonlySet<string>>;
    onCommitLinks: (request: unknown) => void;
    onClose: () => void;
  }) => <div role="dialog" aria-label="issue import"><button onClick={() => void onPreview({ source: 'github' }, new AbortController().signal)}>preview issues</button><button onClick={() => onAdd([draft('Imported issue')])}>add issues</button><button onClick={() => onCommitLinks({ source: 'github', items: [] })}>commit links</button><button onClick={onClose}>close issue import</button></div>,
}));

vi.mock('./components/ReconnectModal', () => ({
  ReconnectModal: ({ onIdentity, onSignOut, onClose }: {
    onIdentity: (identity: Identity) => void; onSignOut: () => void; onClose: () => void;
  }) => <div role="dialog" aria-label="reconnect"><button onClick={() => onIdentity({ ...manual, serverToken: 'fresh' })}>reconnect now</button><button onClick={onSignOut}>reconnect signout</button><button onClick={onClose}>close reconnect</button></div>,
}));

vi.mock('./components/DebugOverlay', () => ({
  debugEnabled: () => false,
  setDebugEnabled: vi.fn(),
  DebugOverlay: ({ onClose }: { onClose: () => void }) => <aside aria-label="debug"><button onClick={onClose}>close debug</button></aside>,
}));
vi.mock('./components/Confetti', () => ({ Confetti: () => <div data-testid="confetti" /> }));

vi.mock('./lib/markdown', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./lib/markdown')>();
  return {
    ...actual,
    serialize: (value: { title: string }) => {
      const text = `# ${value.title}`;
      state.exported.push(text);
      return text;
    },
    parse: (text: string) => {
      if (state.importError) throw new Error('invalid markdown');
      return actual.parse(text);
    },
  };
});

import App from './App';
import { CARD_GONE_NOTICE, NO_SERVER_NOTICE } from './App';

beforeEach(() => {
  vi.useRealTimers();
  localStorage.clear();
  // The filter mirrors itself into the query string; a test that filtered
  // must not seed the next test's view.
  window.history.replaceState(null, '', '/');
  state.identity = manual;
  state.authMode = 'unknown';
  state.localDisplayName = '';
  state.resolveIdentity = null;
  state.resolveError = null;
  state.resolveDeferred = null;
  state.resolveCalls = 0;
  state.present = true;
  state.tasks = [seed()];
  state.created = 0;
  state.calls = [];
  state.listError = null;
  state.writeError = null;
  state.createErrors = {};
  state.tombstoneError = null;
  state.linksError = null;
  state.tombstones = [];
  state.labels = [];
  state.settings = { has_key: false, ai_base_url: '' };
  state.settingsError = null;
  state.sources = [];
  state.integrationsError = null;
  state.createHold = null;
  state.savedIdentities = [];
  state.clearedIdentity = 0;
  state.exported = [];
  state.imported = [];
  state.importError = false;
  state.bursts = [];
  vi.clearAllMocks();
  vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('unexpected network request'); }));
});

/** The board as it is rendered: "title:status:checks". */
function shown(): string[] {
  return [...document.querySelectorAll('[data-testid^="task-"] span')].map(
    (node) => node.textContent ?? '',
  );
}

describe('identity shell', () => {
  it('adopts a manual identity and signs out', async () => {
    state.identity = null;
    const user = userEvent.setup();
    render(<App />);

    // The gate appears once the server's auth mode resolves (mocked 'unknown').
    await user.click(await screen.findByRole('button', { name: 'use manual identity' }));
    expect(await screen.findByText('alice')).not.toBeNull();
    expect(state.savedIdentities).toEqual([manual]);

    await user.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(screen.getByRole('button', { name: 'use manual identity' })).not.toBeNull();
    expect(state.clearedIdentity).toBe(1);
  });

  it('opens the board directly as "default" in open mode, with no gate', async () => {
    state.identity = null;
    state.authMode = 'open';
    render(<App />);

    expect((await screen.findByTitle('default')).textContent).toBe('default');
    expect(screen.queryByRole('button', { name: 'use manual identity' })).toBeNull();
    expect(state.savedIdentities).toEqual([{ kind: 'manual', id: 'default' }]);
  });

  it('sign-out in open mode still reaches the gate to pick another board', async () => {
    state.identity = null;
    state.authMode = 'open';
    const user = userEvent.setup();
    render(<App />);

    await screen.findByTitle('default');
    await user.click(screen.getByRole('button', { name: 'Sign out' }));
    // The auto-adopt must not skip past an explicitly requested gate.
    expect(screen.getByRole('button', { name: 'use manual identity' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'use manual identity' }));
    expect(await screen.findByText('alice')).not.toBeNull();
  });

  it('shows the device-local display name in the header and updates it from settings', async () => {
    state.localDisplayName = 'Board Goblin';
    const user = userEvent.setup();
    render(<App />);

    expect((await screen.findByTitle('alice')).textContent).toBe('Board Goblin');

    await user.click(screen.getByRole('button', { name: 'Settings' }));
    expect(screen.getByText('name:Board Goblin')).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'set display name' }));
    expect(state.localDisplayName).toBe('Amit K');
    expect((await screen.findByTitle('alice')).textContent).toBe('Amit K');
  });

  it('restores an Azure identity, and offers a retry gate when restoration fails', async () => {
    state.identity = { kind: 'azure', id: 'alice@example.com' };
    state.resolveError = new Error('MSAL session expired');
    const user = userEvent.setup();
    const first = render(<App />);

    expect((await screen.findByRole('alert')).textContent).toContain('MSAL session expired');
    await user.click(screen.getByRole('button', { name: 'Sign in again' }));
    expect(screen.getByRole('button', { name: 'use manual identity' })).not.toBeNull();
    first.unmount();

    state.identity = { kind: 'azure', id: 'alice@example.com' };
    state.resolveError = null;
    state.resolveIdentity = {
      kind: 'azure', id: 'alice@example.com', name: 'Alice Azure', homeAccountId: 'home-1',
    };
    const restored = render(<App />);
    expect(screen.getByText('Restoring your Microsoft session…')).not.toBeNull();
    expect(await screen.findByText('Alice Azure')).not.toBeNull();
    expect(state.savedIdentities.at(-1)).toEqual(state.resolveIdentity);
    restored.unmount();

    state.identity = { kind: 'azure', id: 'alice@example.com' };
    state.resolveIdentity = null;
    state.resolveError = 'not an Error instance';
    render(<App />);
    expect((await screen.findByRole('alert')).textContent).toContain(
      'session expired — sign in again',
    );
  });

  it('ignores a resolved Azure identity after unmount', async () => {
    const azureIdentity: Identity = { kind: 'azure', id: 'alice@example.com' };
    let resolveIdentity!: (identity: Identity) => void;
    state.identity = azureIdentity;
    state.resolveDeferred = new Promise((resolve) => { resolveIdentity = resolve; });

    const resolved = render(<App />);
    expect(state.resolveCalls).toBe(1);
    resolved.unmount();
    await act(async () => {
      resolveIdentity({ ...azureIdentity, homeAccountId: 'home-1' });
    });
    expect(state.savedIdentities).toEqual([]);
  });
});

describe('loading the board', () => {
  it('reads the board from the server on mount', async () => {
    render(<App />);
    expect(await screen.findByText('First task:todo:false')).not.toBeNull();
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'synced to server' })).not.toBeNull();
    });
    expect(state.calls).toEqual(['list']);
  });

  it('says so, and shows nothing, when no server answers', async () => {
    state.present = false;
    render(<App />);
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(NO_SERVER_NOTICE);
    });
    expect(screen.queryByTestId('task-task-1')).toBeNull();
    expect(screen.getByRole('img', { name: 'no server — nothing loaded' })).not.toBeNull();
    expect(state.calls).toEqual([]);
  });

  it('reports a failed load and keeps the board empty', async () => {
    state.listError = new Error('storage error');
    render(<App />);
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain('storage error');
    });
    expect(screen.getByRole('img', { name: /did not reach the server/ })).not.toBeNull();
    expect(screen.queryByTestId('task-task-1')).toBeNull();
  });

  it('opens the reconnect dialog on an expired session and reloads after reconnecting', async () => {
    const { ReauthRequiredError } = await import('./lib/auth');
    state.listError = new ReauthRequiredError('expired');
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByRole('dialog', { name: 'reconnect' })).not.toBeNull();
    expect(screen.getByRole('img', { name: /session expired/ })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'reconnect now' }));
    expect(state.savedIdentities.at(-1)).toMatchObject({ serverToken: 'fresh' });
    // The new identity re-runs the load effect, which reads the board again.
    expect(await screen.findByText('First task:todo:false')).not.toBeNull();
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'synced to server' })).not.toBeNull();
    });
  });

  it('keeps an explicit reconnect and sign-out reachable after dismissal', async () => {
    const { ReauthRequiredError } = await import('./lib/auth');
    state.listError = new ReauthRequiredError('expired');
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole('button', { name: 'close reconnect' }));
    await user.click(screen.getByRole('button', { name: 'Reconnect' }));
    await user.click(screen.getByRole('button', { name: 'reconnect signout' }));
    expect(screen.getByRole('button', { name: 'use manual identity' })).not.toBeNull();
  });
});

describe('writing through the task API', () => {
  it('creates, edits, moves, ticks, ships, cancels, restores, and deletes', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(screen.getByRole('button', { name: 'add todo' }));
    await user.click(within(screen.getByRole('dialog', { name: 'card modal' })).getByRole('button', { name: 'save card' }));
    expect(await screen.findByText('Added task:todo:')).not.toBeNull();
    expect(state.calls).toContain('create:Added task');

    await user.click(screen.getByRole('button', { name: 'edit server-1' }));
    await user.click(screen.getByRole('button', { name: 'save card' }));
    expect(await screen.findByText('Added task edited:todo:')).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'move doing server-1' }));
    expect(await screen.findByText('Added task edited:doing:')).not.toBeNull();
    expect(state.calls).toContain('patch:server-1:{"status":"doing","index":0}');

    // A card with an open checklist item stops at the ship confirmation, and
    // ticking everything there goes out as one forced patch.
    await user.click(screen.getByRole('button', { name: 'move done task-1' }));
    expect(screen.getByRole('dialog', { name: 'ship' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'tick all' }));
    expect(await screen.findByText('First task:done:true')).not.toBeNull();
    expect(state.calls).toContain(
      'patch:task-1:{"checks":[{"text":"verify","done":true}],"status":"done","index":0,"force":true}',
    );
    expect(state.bursts.some((burst) => burst[2] === 70)).toBe(true);
    expect(screen.getByText('×1 shipped today')).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'edit server-1' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.clear(screen.getByRole('textbox', { name: 'confirm input' }));
    await user.type(screen.getByRole('textbox', { name: 'confirm input' }), 'superseded');
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    expect(await screen.findByText('Added task edited:cancelled:')).not.toBeNull();
    expect(state.tombstones).toEqual([{ taskId: 'server-1', reason: 'superseded' }]);

    await user.click(screen.getByRole('button', { name: 'restore server-1' }));
    expect(await screen.findByText('Added task edited:todo:')).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'purge server-1' }));
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    await waitFor(() => expect(screen.queryByTestId('task-server-1')).toBeNull());
    expect(state.calls).toContain('delete:server-1');
  });

  it('kills without a reason from the secondary action and from a blank one', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.click(screen.getByRole('button', { name: 'secondary action' }));
    expect(await screen.findByText('First task:cancelled:false')).not.toBeNull();
    expect(state.tombstones).toEqual([]);

    await user.click(screen.getByRole('button', { name: 'restore task-1' }));
    await screen.findByText('First task:todo:false');
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.clear(screen.getByRole('textbox', { name: 'confirm input' }));
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    expect(await screen.findByText('First task:cancelled:false')).not.toBeNull();
    expect(state.tombstones).toEqual([]);
  });

  it('reports a refused kill reason after the card has already moved', async () => {
    state.tombstoneError = new Error('tombstone target changed');
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'tombstone target changed',
      );
    });
    // The move itself landed: the reason is what was lost.
    expect(screen.getByText('First task:cancelled:false')).not.toBeNull();
  });

  it('ships a warned card as it stands, and cancels the confirmation', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(screen.getByRole('button', { name: 'move done task-1' }));
    await user.click(screen.getByRole('button', { name: 'cancel ship' }));
    expect(screen.queryByRole('dialog', { name: 'ship' })).toBeNull();
    expect(screen.getByText('First task:todo:false')).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'move done task-1' }));
    await user.click(screen.getByRole('button', { name: 'ship anyway' }));
    expect(await screen.findByText('First task:done:false')).not.toBeNull();
    expect(state.calls).toContain(
      'patch:task-1:{"status":"done","index":0,"force":true}',
    );

    // Reopening takes it back off today's tally.
    expect(screen.getByText('×1 shipped today')).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await screen.findByText('First task:doing:false');
    expect(screen.queryByText(/shipped today/)).toBeNull();
  });

  it('ships straight through when nothing warrants a warning', async () => {
    state.tasks = [seed({ checks: [{ text: 'verify', done: true }] })];
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:true');

    await user.click(screen.getByRole('button', { name: 'move done task-1' }));
    expect(await screen.findByText('First task:done:true')).not.toBeNull();
    expect(state.calls).toContain('patch:task-1:{"status":"done","index":0}');
  });

  it('falls back to the viewport centre when the shipped card has no DOM node', async () => {
    state.tasks = [seed({ id: 'ghost', checks: [] })];
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:');

    await user.click(screen.getByRole('button', { name: 'move done ghost' }));
    await screen.findByText('First task:done:');
    expect(state.bursts.at(-1)).toEqual([
      window.innerWidth / 2,
      window.innerHeight / 2,
      70,
    ]);
  });

  it('ignores moves, edits, and ticks of cards that are not on the board', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');
    state.calls = [];

    await user.click(screen.getByRole('button', { name: 'edit missing' }));
    await user.click(screen.getByRole('button', { name: 'move missing' }));
    await user.click(screen.getByRole('button', { name: 'tick missing' }));
    await user.click(screen.getByRole('button', { name: 'tick invalid task-1' }));
    expect(screen.queryByRole('dialog', { name: 'card modal' })).toBeNull();
    expect(state.calls).toEqual([]);
  });

  it('sends no patch for a same-column drop that names no slot', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');
    state.calls = [];

    // The mock's "keep in place" hands back the card's own slot, which is a
    // real reorder request; a status-only repeat of the current column is the
    // one thing applyMove drops.
    await user.click(screen.getByRole('button', { name: 'keep task-1 in place' }));
    await waitFor(() => expect(state.calls).toContain('patch:task-1:{"status":"todo","index":0}'));
  });

  it('ticks a checklist item, unticks it, and auto-ships a finished one', async () => {
    vi.useFakeTimers();
    state.tasks = [
      seed({ id: 'active', checks: [{ text: 'last', done: false }] }),
      seed({ id: 'dropped', status: 'cancelled', checks: [{ text: 'last', done: false }] }),
    ];
    try {
      render(<App />);
      await act(async () => { await Promise.resolve(); });
      await act(async () => { await Promise.resolve(); });
      fireEvent.click(screen.getByRole('button', { name: 'tick active' }));
      fireEvent.click(screen.getByRole('button', { name: 'tick dropped' }));
      await act(async () => { vi.advanceTimersByTime(351); });
      await act(async () => { await Promise.resolve(); });
      expect(shown()).toContain('First task:done:true');
      // Finishing a cancelled card's checklist must not resurrect it.
      expect(shown()).toContain('First task:cancelled:true');
      expect(state.bursts.some((burst) => burst[2] === 14)).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not auto-ship when the tick is undone before the timer fires', async () => {
    vi.useFakeTimers();
    state.tasks = [seed({ id: 'active', checks: [{ text: 'last', done: false }] })];
    try {
      render(<App />);
      await act(async () => { await Promise.resolve(); });
      await act(async () => { await Promise.resolve(); });
      fireEvent.click(screen.getByRole('button', { name: 'tick active' }));
      await act(async () => { await Promise.resolve(); });
      fireEvent.click(screen.getByRole('button', { name: 'tick active' }));
      await act(async () => { vi.advanceTimersByTime(351); });
      await act(async () => { await Promise.resolve(); });
      expect(shown()).toContain('First task:todo:false');
    } finally {
      vi.useRealTimers();
    }
  });

  it('surfaces a refused write and snaps the board back to the server', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    state.writeError = new Error('store: invalid effort "XL"');
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'store: invalid effort "XL"',
      );
    });
    // The optimistic move is undone by the refetch that follows.
    expect(screen.getByText('First task:todo:false')).not.toBeNull();
    expect(state.bursts).toEqual([]);

    await user.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(document.querySelector('.notice')).toBeNull();
  });

  it('undoes an optimistic move when the write and the re-read both fail', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    // The server is unreachable in both directions: the refetch that normally
    // corrects the board never arrives.
    state.writeError = new Error('network down');
    state.listError = new Error('network down');
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain('network down');
    });

    // The notice may not claim more than the screen shows, and the screen may
    // not show a move the server refused.
    expect(shown()).toEqual(['First task:todo:false']);
    expect(state.tasks[0]!.status).toBe('todo');
    expect(document.querySelector('.notice')?.textContent).toContain(
      'The board below is the last one the server confirmed',
    );
  });

  it('keeps an optimistic move that was saved but could not be read back', async () => {
    const user = userEvent.setup();
    state.tasks = [seed({ checks: [] })];
    render(<App />);
    await screen.findByText('First task:todo:');

    // The write landed; only the refetch failed, so the change is real and
    // stays on screen — and the notice says the board may be stale rather
    // than claiming nothing was saved.
    state.listError = new Error('network down');
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'The board could not be read from the server: network down',
      );
    });
    expect(shown()).toEqual(['First task:doing:']);
    expect(state.tasks[0]!.status).toBe('doing');
  });

  it('undoes a move the expired session refused, and says the session expired', async () => {
    const { ReauthRequiredError } = await import('./lib/auth');
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    state.writeError = new ReauthRequiredError('expired');
    state.listError = new ReauthRequiredError('expired');
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));

    // Silence plus a card sitting in its new column is the combination that
    // reads as "saved". Neither is allowed here.
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'That change was not saved: the session expired',
      );
    });
    expect(shown()).toEqual(['First task:todo:false']);
    expect(state.tasks[0]!.status).toBe('todo');
    expect(await screen.findByRole('dialog', { name: 'reconnect' })).not.toBeNull();
  });

  it('announces recovery once a write lands again', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    // The refetch fails alongside the write, so the failed state is on screen
    // rather than passed through inside one commit.
    state.writeError = new Error('store: invalid effort "XL"');
    state.listError = new Error('storage error');
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(screen.getByRole('img', { name: /did not reach the server/ })).not.toBeNull();
    });

    // The move's own announcement lands after the recovery one, which is why
    // only the dot is asserted here.
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'synced to server' })).not.toBeNull();
    });
  });

  it('drops a stale refetch when a newer write is already in flight', async () => {
    state.tasks = [seed({ checks: [] })];
    render(<App />);
    await screen.findByText('First task:todo:');
    state.calls = [];

    // Two moves without awaiting between them: the first refetch resolves
    // against a generation the second has already superseded, so the board
    // never flashes back through the intermediate column.
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'move doing task-1' }));
      fireEvent.click(screen.getByRole('button', { name: 'move done task-1' }));
    });
    await waitFor(() => expect(state.calls.filter((c) => c === 'list')).toHaveLength(2));
    expect(shown()).toEqual(['First task:done:']);
  });

  it('closes an open card the server no longer has', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    expect(screen.getByRole('dialog', { name: 'card modal' })).not.toBeNull();
    // Something else deleted it; the next refetch is what notices.
    state.tasks = [];
    await user.click(screen.getByRole('button', { name: 'close card' }));
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(CARD_GONE_NOTICE);
    });
    expect(screen.queryByRole('dialog', { name: 'card modal' })).toBeNull();
  });
});

describe('filtering', () => {
  it('narrows by text and label without changing what a move sends', async () => {
    state.tasks = [
      seed({ id: 't1', title: 'Fix login', desc: 'auth expiry', tags: ['bug', 'auth'], checks: [] }),
      seed({ id: 't2', title: 'Landing page', desc: '', tags: ['ui'], checks: [] }),
      seed({ id: 't3', title: 'Fix billing', desc: '', tags: ['bug'], checks: [] }),
    ];
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('Fix login:todo:');

    const input = screen.getByRole('searchbox', { name: 'Filter cards by text' });
    await user.type(input, 'FIX');
    expect(screen.queryByTestId('task-t2')).toBeNull();
    expect(screen.getByText('2 of 3 cards')).toBeTruthy();

    // Slot 1 of the *rendered* column is Fix billing, which sits at slot 2 of
    // the real column — the index that goes to the server.
    state.calls = [];
    await user.click(screen.getByRole('button', { name: 'keep t3 in place' }));
    await waitFor(() => expect(state.calls).toContain('patch:t3:{"status":"todo","index":2}'));

    await user.click(screen.getByRole('button', { name: 'Clear' }));
    expect(screen.getByTestId('task-t2')).toBeTruthy();
    expect(screen.queryByText(/of 3 cards/)).toBeNull();

    await user.click(screen.getByRole('button', { name: 'tag t1' }));
    expect(screen.queryByTestId('task-t2')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Stop filtering by label bug' }));
    expect(screen.getByTestId('task-t2')).toBeTruthy();

    // Filtering is display-only: the server still holds all three.
    expect(state.tasks).toHaveLength(3);
  });

  it('seeds the filter from the URL and mirrors changes back into it', async () => {
    state.tasks = [
      seed({ id: 't1', title: 'Fix login', tags: ['bug'], checks: [] }),
      seed({ id: 't2', title: 'Landing page', tags: ['ui'], checks: [] }),
    ];
    window.history.replaceState(null, '', '/?q=fix&other=1');
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('Fix login:todo:');

    // The refresh restored the view: text seeded, board narrowed.
    const input = screen.getByRole('searchbox', { name: 'Filter cards by text' });
    expect((input as HTMLInputElement).value).toBe('fix');
    expect(screen.queryByTestId('task-t2')).toBeNull();

    // Changes mirror into the query string; foreign params survive.
    await user.click(screen.getByRole('button', { name: 'tag t1' }));
    await waitFor(() => {
      const params = new URLSearchParams(window.location.search);
      expect(params.get('tags')).toBe('bug');
      expect(params.get('other')).toBe('1');
    });

    await user.click(screen.getByRole('button', { name: 'Clear' }));
    await waitFor(() => expect(window.location.search).toBe('?other=1'));
  });

  it('appends a drop past the last visible card', async () => {
    state.tasks = [
      seed({ id: 't1', title: 'Fix login', tags: ['bug'], checks: [] }),
      seed({ id: 't2', title: 'Landing page', tags: ['ui'], checks: [] }),
    ];
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('Fix login:todo:');
    await user.type(
      screen.getByRole('searchbox', { name: 'Filter cards by text' }),
      'Landing',
    );
    state.calls = [];
    // Only one card is rendered, so its own slot index is past the end of the
    // filtered column once the dragged card is excluded.
    await user.click(screen.getByRole('button', { name: 'keep t2 in place' }));
    await waitFor(() => expect(state.calls).toContain('patch:t2:{"status":"todo","index":1}'));
  });

  it('remembers the cancelled column toggle', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');
    await user.click(screen.getByRole('button', { name: /Cancelled/ }));
    expect(screen.getByRole('button', { name: /Cancelled/ }).getAttribute('aria-pressed')).toBe('true');
    expect(localStorage.getItem('kb.showCancelled.v1')).toBe('1');
  });
});

describe('pre-freeze hardening', () => {
  it('keeps the editor open with its content when a save is refused', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));

    state.writeError = new Error('save refused');
    await user.click(screen.getByRole('button', { name: 'save card' }));
    await screen.findAllByText(/That change was not saved: save refused/);
    expect(screen.getByRole('dialog', { name: 'card modal' })).toBeTruthy();

    // The retry lands and only then does the editor close.
    await user.click(screen.getByRole('button', { name: 'save card' }));
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'card modal' })).toBeNull());
    expect(shown()).toContain('First task edited:todo:false');
  });

  it('creates one card when save is pressed twice before the server answers', async () => {
    let release = () => {};
    state.createHold = new Promise((resolve) => {
      release = resolve;
    });
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');
    await user.click(screen.getByRole('button', { name: 'add todo' }));
    await user.click(screen.getByRole('button', { name: 'save card' }));
    await user.click(screen.getByRole('button', { name: 'save card' }));
    release();
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'card modal' })).toBeNull());
    expect(state.created).toBe(1);
  });

  it('asks before discarding an edited card, and only discards on confirm', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'mark dirty' }));

    await user.click(screen.getByRole('button', { name: 'close card' }));
    // The editor is still there behind the question.
    expect(screen.getByRole('dialog', { name: 'card modal' })).toBeTruthy();
    await user.click(
      within(screen.getByRole('dialog', { name: 'Discard unsaved changes?' }))
        .getByRole('button', { name: 'confirm action' }),
    );
    expect(screen.queryByRole('dialog', { name: 'card modal' })).toBeNull();
  });

  it('closes an untouched card without asking', async () => {
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'close card' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('catches up on outside writes when the tab regains focus, throttled', async () => {
    render(<App />);
    await screen.findByText('First task:todo:false');
    const listsBefore = state.calls.filter((c) => c === 'list').length;

    // A CLI write happens while the tab is away; focus catches it up.
    state.tasks = [...state.tasks, seed({ id: 'cli-1', title: 'From the CLI', checks: [] })];
    fireEvent(window, new Event('focus'));
    await screen.findByText('From the CLI:todo:');

    // A second wake inside the throttle window sends nothing.
    fireEvent(document, new Event('visibilitychange'));
    expect(state.calls.filter((c) => c === 'list').length).toBe(listsBefore + 1);
  });

  it('ignores a wake while the tab is hidden', async () => {
    render(<App />);
    await screen.findByText('First task:todo:false');
    const listsBefore = state.calls.filter((c) => c === 'list').length;
    Object.defineProperty(document, 'visibilityState', {
      value: 'hidden',
      configurable: true,
    });
    fireEvent(document, new Event('visibilitychange'));
    Object.defineProperty(document, 'visibilityState', {
      value: 'visible',
      configurable: true,
    });
    expect(state.calls.filter((c) => c === 'list').length).toBe(listsBefore);
  });

  it('says why AI and import actions are hidden when their config fails to load', async () => {
    state.settingsError = new Error('boom');
    state.integrationsError = new Error('boom');
    render(<App />);
    await screen.findAllByText(/AI settings could not be loaded/);
    await screen.findAllByText(/Forge sources could not be loaded/);
  });

  it('stays quiet about hidden actions when the session itself expired', async () => {
    const { ReauthRequiredError } = await import('./lib/auth');
    state.settingsError = new ReauthRequiredError('expired');
    state.integrationsError = new ReauthRequiredError('expired');
    render(<App />);
    await screen.findByText('First task:todo:false');
    expect(screen.queryByText(/AI settings could not be loaded/)).toBeNull();
    expect(screen.queryByText(/Forge sources could not be loaded/)).toBeNull();
  });
});

describe('server-backed panels', () => {
  it('gates AI and import actions on server settings, and creates from both', async () => {
    state.labels = ['server-label'];
    state.settings = { has_key: true, ai_base_url: '' };
    state.sources = [{ name: 'GitHub', kind: 'github' }];
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    expect(screen.getByText('ai enabled')).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'request ai draft' }));
    await user.click(screen.getByRole('button', { name: 'close card' }));

    await user.click(await screen.findByRole('button', { name: /Split ADR/ }));
    await user.click(screen.getByRole('button', { name: 'split adr request' }));
    await user.click(screen.getByRole('button', { name: 'add no adr stories' }));
    expect(screen.queryByRole('dialog', { name: 'adr' })).toBeNull();

    await user.click(screen.getByRole('button', { name: /Split ADR/ }));
    await user.click(screen.getByRole('button', { name: 'close adr' }));
    await user.click(screen.getByRole('button', { name: /Split ADR/ }));
    await user.click(screen.getByRole('button', { name: 'add adr stories' }));
    expect(await screen.findByText('ADR story:todo:')).not.toBeNull();
    expect(await screen.findByText('ADR story two:todo:')).not.toBeNull();
    // Two creates, one refetch.
    expect(state.calls.filter((c) => c.startsWith('create:'))).toHaveLength(2);

    await user.click(screen.getByRole('button', { name: /Import issues/ }));
    await user.click(screen.getByRole('button', { name: 'preview issues' }));
    state.linksError = new Error('provenance rejected');
    await user.click(screen.getByRole('button', { name: 'commit links' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'Import provenance was not recorded: provenance rejected',
      );
    });
    state.linksError = 'not an Error';
    await user.click(screen.getByRole('button', { name: 'commit links' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'Import provenance was not recorded: the request failed',
      );
    });
    await user.click(screen.getByRole('button', { name: 'close issue import' }));
    await user.click(screen.getByRole('button', { name: /Import issues/ }));
    await user.click(screen.getByRole('button', { name: 'add issues' }));
    expect(await screen.findByText('Imported issue:todo:')).not.toBeNull();
  });

  it('still creates the drafts behind a refused one, and names the one that was missed', async () => {
    state.settings = { has_key: true, ai_base_url: 'https://ai.example' };
    state.createErrors = { 'ADR story': new Error('store: title must not be empty') };
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(await screen.findByRole('button', { name: /Split ADR/ }));
    await user.click(screen.getByRole('button', { name: 'add adr stories' }));

    // The modal holding the reviewed selection is already closed, so a draft
    // the run never attempted is a draft nobody can get back.
    expect(await screen.findByText('ADR story two:todo:')).not.toBeNull();
    expect(state.calls.filter((c) => c.startsWith('create:'))).toEqual([
      'create:ADR story',
      'create:ADR story two',
    ]);
    const notice = document.querySelector('.notice')?.textContent ?? '';
    expect(notice).toContain('One card was not created: store: title must not be empty');
    expect(notice).toContain('Not saved: ADR story.');
    // One card did land, so the blanket refusal wording would be a lie.
    expect(notice).not.toContain('That change was not saved');
  });

  it('names every draft when a whole batch is refused', async () => {
    state.settings = { has_key: true, ai_base_url: 'https://ai.example' };
    state.createErrors = {
      'ADR story': new Error('store: title must not be empty'),
      'ADR story two': new Error('store: still no'),
    };
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(await screen.findByRole('button', { name: /Split ADR/ }));
    await user.click(screen.getByRole('button', { name: 'add adr stories' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        '2 cards were not created: store: title must not be empty',
      );
    });
    // The first refusal is the one quoted; every title is still listed.
    expect(document.querySelector('.notice')?.textContent).toContain(
      'Not saved: ADR story; ADR story two.',
    );
  });

  it('stops a batch at an expired session, names the rest, and asks to reconnect', async () => {
    const { ReauthRequiredError } = await import('./lib/auth');
    state.settings = { has_key: true, ai_base_url: 'https://ai.example' };
    state.createErrors = { 'ADR story': new ReauthRequiredError('expired') };
    const user = userEvent.setup();
    // A session the server has stopped accepting rejects the read back too.
    const expireReads = () => { state.listError = new ReauthRequiredError('expired'); };
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(await screen.findByRole('button', { name: /Split ADR/ }));
    expireReads();
    await user.click(screen.getByRole('button', { name: 'add adr stories' }));

    expect(await screen.findByRole('dialog', { name: 'reconnect' })).not.toBeNull();
    // Nothing would be accepted until the session is restored, so the rest go
    // into the notice rather than through more 401s.
    expect(state.calls.filter((c) => c.startsWith('create:'))).toEqual(['create:ADR story']);
    expect(document.querySelector('.notice')?.textContent).toContain(
      '2 cards were not created: the session expired. Not saved: ADR story; ADR story two.',
    );
  });

  it('raises the reconnect prompt when the session expires on the provenance write', async () => {
    const { ReauthRequiredError } = await import('./lib/auth');
    state.settings = { has_key: true, ai_base_url: '' };
    state.sources = [{ name: 'GitHub', kind: 'github' }];
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText('First task:todo:false');

    await user.click(await screen.findByRole('button', { name: /Import issues/ }));
    state.linksError = new ReauthRequiredError('expired');
    await user.click(screen.getByRole('button', { name: 'commit links' }));

    // Every other write raises this; a dead session found here is the same
    // dead session, not a provenance quirk.
    expect(await screen.findByRole('dialog', { name: 'reconnect' })).not.toBeNull();
    expect(screen.getByRole('img', { name: /session expired/ })).not.toBeNull();
    expect(document.querySelector('.notice')?.textContent).toContain(
      'Import provenance was not recorded: the session expired',
    );
  });

  it('hides the import action when the server offers no sources', async () => {
    state.settings = { has_key: true, ai_base_url: '' };
    render(<App />);
    await screen.findByText('First task:todo:false');
    expect(await screen.findByRole('button', { name: /Split ADR/ })).not.toBeNull();
    expect(screen.queryByRole('button', { name: /Import issues/ })).toBeNull();
  });

  it('opens settings, toggles the debug overlay, and closes both', async () => {
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getByRole('button', { name: 'Settings' }));
    const settings = screen.getByRole('dialog', { name: 'settings' });
    expect(document.querySelector('.app-shell')?.hasAttribute('inert')).toBe(true);
    await user.click(within(settings).getByRole('button', { name: 'enable ai' }));
    await user.click(within(settings).getByRole('button', { name: 'enable debug' }));
    expect(screen.getByLabelText('debug')).not.toBeNull();
    await user.click(within(settings).getByRole('button', { name: 'close settings' }));
    await user.click(screen.getByRole('button', { name: 'close debug' }));
    expect(screen.queryByLabelText('debug')).toBeNull();
  });

  it('tells settings there is no server when detection failed', async () => {
    state.present = false;
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getByRole('button', { name: 'Settings' }));
    expect(within(screen.getByRole('dialog', { name: 'settings' })).getByText('false'))
      .not.toBeNull();
  });
});

describe('markdown file exchange', () => {
  it('exports the board and replaces it from a file in one write', async () => {
    const user = userEvent.setup();
    const { container } = render(<App />);
    await screen.findByText('First task:todo:false');

    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    await user.click(screen.getByRole('button', { name: 'Export' }));
    expect(state.exported).toEqual(['# kb']);
    expect(click).toHaveBeenCalledOnce();

    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const inputClick = vi.spyOn(input, 'click');
    await user.click(screen.getByRole('button', { name: 'Import' }));
    expect(inputClick).toHaveBeenCalledOnce();
    // A change event with no file at all is a cancelled picker.
    fireEvent.change(input, { target: { files: [] } });
    await user.upload(input, new File([state.importText], 'board.md', { type: 'text/markdown' }));
    const dialog = await screen.findByRole('dialog', { name: 'Replace the current board?' });
    expect(dialog).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    expect(await screen.findByText('Imported markdown:todo:')).not.toBeNull();
    expect(state.imported).toEqual([state.importText]);
    expect(state.calls.filter((c) => c === 'replace')).toHaveLength(1);
  });

  it('reports a file it cannot read as a board and dismisses the dialog', async () => {
    state.importError = true;
    const user = userEvent.setup();
    const { container } = render(<App />);
    await screen.findByText('First task:todo:false');
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, new File(['bad'], 'bad.md', { type: 'text/markdown' }));
    expect(await screen.findByRole('dialog', { name: 'Import failed' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'close confirm' }));
    expect(screen.queryByRole('dialog', { name: 'Import failed' })).toBeNull();
  });

  it('counts a single-task import in the singular', async () => {
    const user = userEvent.setup();
    const { container } = render(<App />);
    await screen.findByText('First task:todo:false');
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, new File([state.importText], 'one.md', { type: 'text/markdown' }));
    await screen.findByRole('dialog', { name: 'Replace the current board?' });
    await user.click(screen.getByRole('button', { name: 'close confirm' }));
    expect(screen.queryByRole('dialog', { name: 'Replace the current board?' })).toBeNull();
  });
});
