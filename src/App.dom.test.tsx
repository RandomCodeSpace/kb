// @vitest-environment jsdom

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board, Task } from './lib/model';
import type { Identity } from './lib/auth';

const state = vi.hoisted(() => ({
  identity: null as Identity | null,
  resolveIdentity: null as Identity | null,
  resolveError: null as unknown,
  board: null as Board | null,
  importBoard: null as Board | null,
  importError: false,
  dirty: false,
  detect: false,
  detectDeferred: false,
  detectResolve: null as null | ((value: boolean) => void),
  remoteBoard: null as Board | null,
  migratedRaw: false,
  pendingWrite: null as null | Record<string, unknown>,
  startupMode: 'normal' as 'normal' | 'merged' | 'throw' | 'callbacks',
  pendingMode: 'clean' as 'clean' | 'push' | 'retry' | 'throw',
  legacyMode: 'clean' as 'clean' | 'push' | 'failed' | 'throw',
  sameSemantics: false,
  commitMode: 'normal' as 'normal' | 'recovery' | 'conflict' | 'stopped',
  omitAckSnapshot: false,
  ackCurrent: true,
  omitIsCurrent: false,
  persistenceFails: false,
  persistenceConflicts: [] as string[],
  remoteSaveError: null as Error | null,
  prepareError: null as Error | null,
  remoteSaveConflicts: [] as string[],
  labels: [] as string[],
  settings: { has_key: false, ai_base_url: '' },
  sources: [] as Array<{ id: string; name: string; kind: 'github' | 'gitlab' }>,
  outboxStatus: null as null | { kind: 'idle' | 'blocked' | 'retry' | 'reauth'; message?: string },
  outboxReject: false,
  savedIdentities: [] as Identity[],
  clearedIdentity: 0,
  dirtyWrites: [] as boolean[],
  exported: [] as string[],
  remoteInstances: [] as Array<{ flush: ReturnType<typeof vi.fn>; cancel: ReturnType<typeof vi.fn> }>,
  outboxInstances: [] as Array<{ cancel: ReturnType<typeof vi.fn> }>,
  bursts: [] as Array<[number, number, number]>,
}));

const manual: Identity = { kind: 'manual', id: 'alice' };

function task(overrides: Partial<Task> = {}): Task {
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

function board(tasks: Task[] = [task()]): Board {
  return { title: 'Test board', tasks };
}

function snapshot(nextBoard = state.board ?? board(), generation = 0) {
  return {
    board: nextBoard,
    seeded: false,
    canonicalTaskIDs: state.migratedRaw
      ? new Map<string, string>()
      : new Map(nextBoard.tasks.map((item) => [item.id, `canonical-${item.id}`])),
    deletedCanonicalIDs: new Set<string>(),
    migratedRaw: state.migratedRaw,
    pendingBoardWrite: state.pendingWrite,
    generation,
    version: { present: true, generation },
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
      if (state.resolveError) throw state.resolveError;
      if (!state.resolveIdentity) throw new Error('no restored identity configured');
      return state.resolveIdentity;
    },
    identityNamespace: (identity: Identity) =>
      identity.kind === 'azure' ? `azure.${identity.homeAccountId ?? 'pending'}` : identity.id,
    displayName: (identity: Identity) => identity.name || identity.id,
  };
});

vi.mock('./lib/store', () => {
  class LocalStore {
    private current = snapshot();
    loadOrSeed() { return this.current; }
    loadSnapshot() { return this.current; }
    private async persist(
      nextBoard: Board,
      ids: ReadonlyMap<string, string>,
      deleted: ReadonlySet<string>,
      ...rest: unknown[]
    ) {
      const guard = [...rest].reverse().find((value) => typeof value === 'function') as
        | (() => boolean)
        | undefined;
      guard?.();
      if (state.persistenceFails) return { ok: false as const, error: new Error('quota exceeded') };
      this.current = {
        ...this.current,
        board: nextBoard,
        canonicalTaskIDs: new Map(ids),
        deletedCanonicalIDs: new Set(deleted),
        generation: this.current.generation + 1,
        version: { present: true, generation: this.current.generation + 1 },
      };
      state.board = nextBoard;
      return { ok: true as const, generation: this.current.generation, snapshot: this.current };
    }
    saveIfGeneration(nextBoard: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>, ...rest: unknown[]) {
      return this.persist(nextBoard, ids, deleted, ...rest);
    }
    saveAcknowledgement(nextBoard: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>, ...rest: unknown[]) {
      return this.persist(nextBoard, ids, deleted, ...rest);
    }
    completeIdentityRecovery(nextBoard: Board, ids: ReadonlyMap<string, string>, deleted: ReadonlySet<string>, ...rest: unknown[]) {
      return this.persist(nextBoard, ids, deleted, ...rest);
    }
  }
  return {
    LocalStore,
    loadDirty: () => state.dirty,
    setDirty: (_ns: string, value: boolean) => {
      state.dirty = value;
      state.dirtyWrites.push(value);
    },
    continueAfterLocalPersistence: (
      result: { ok: boolean },
      gate: { current: boolean },
      next: { warn: () => void; markDirty: () => void; scheduleRemote: () => void },
    ) => {
      if (!result.ok && !gate.current) {
        gate.current = true;
        next.warn();
      }
      next.markDirty();
      next.scheduleRemote();
    },
    shippedToday: () => 0,
    shipKey: (item: Task) => item.id,
    bumpShipped: () => 1,
    unshipToday: () => 0,
  };
});

vi.mock('./lib/remote', () => {
  class RemoteStore {
    flush = vi.fn();
    cancel = vi.fn();
    constructor() { state.remoteInstances.push(this); }
    detect = vi.fn(async () => {
      if (!state.detectDeferred) return state.detect;
      return new Promise<boolean>((resolve) => { state.detectResolve = resolve; });
    });
    prepareDirtyMapped = vi.fn(async (
      _identity: Identity,
      nextBoard: Board,
      ids: ReadonlyMap<string, string>,
      deleted: ReadonlySet<string>,
    ) => {
      if (state.prepareError) throw state.prepareError;
      return { board: nextBoard, taskIDs: ids, deletedCanonicalIDs: deleted };
    });
    saveRemote = vi.fn((
      _identity: Identity,
      nextBoard: Board,
      onError: (error: unknown) => void,
      onSaved: (...args: unknown[]) => unknown,
      options: {
        durableSnapshot?: ReturnType<typeof snapshot>;
        isLiveCurrent?: () => boolean;
      } = {},
    ) => {
      options.isLiveCurrent?.();
      if (state.remoteSaveError) return onError(state.remoteSaveError);
      const ids = new Map(nextBoard.tasks.map((item) => [item.id, `canonical-${item.id}`]));
      return void onSaved(
        nextBoard,
        ids,
        state.remoteSaveConflicts,
        'operation-1',
        state.omitIsCurrent ? undefined : () => state.ackCurrent,
        1,
        { present: true, generation: 0 },
        state.omitAckSnapshot ? undefined : options.durableSnapshot ?? snapshot(nextBoard),
      );
    });
    saveRemoteDebounced = this.saveRemote;
  }
  return {
    RemoteStore,
    sameBoardSemantics: () => state.sameSemantics,
    recomputeDeletedCanonicalIDs: () => new Set<string>(),
    mergeAcknowledgedState: (
      _current: Board,
      _currentIDs: ReadonlyMap<string, string>,
      _sent: Board,
      _sentIDs: ReadonlyMap<string, string>,
      pushed: Board,
      committedIDs: ReadonlyMap<string, string>,
    ) => ({ board: pushed, canonicalTaskIDs: committedIDs, conflicts: state.persistenceConflicts }),
    commitLiveCandidate: async (options: {
      candidate: { board: Board; canonicalTaskIDs: ReadonlyMap<string, string>; deletedCanonicalIDs: ReadonlySet<string> };
      sourceLive: { durableBase: ReturnType<typeof snapshot> };
      persist: (...args: unknown[]) => Promise<{ ok: boolean; snapshot?: ReturnType<typeof snapshot>; error?: unknown }>;
      repairPersist?: (...args: unknown[]) => Promise<unknown>;
      readLive?: () => unknown;
      readDurable?: () => unknown;
      cancelled?: () => boolean;
    }) => {
      options.readLive?.();
      options.readDurable?.();
      options.cancelled?.();
      if (state.commitMode === 'recovery') {
        return {
          candidate: options.candidate, conflicts: ['live board'], persisted: false,
          snapshot: options.sourceLive.durableBase, recoveryPending: true, writes: 2,
        };
      }
      if (state.commitMode === 'conflict') {
        return {
          candidate: options.candidate, conflicts: [], persisted: false, recoveryPending: false,
          writes: 1, failure: { ok: false, error: new Error('stale'), conflict: true },
        };
      }
      if (state.commitMode === 'stopped') {
        return {
          candidate: options.candidate, conflicts: [], persisted: false, recoveryPending: false,
          writes: 0,
        };
      }
      const result = await options.persist(
        options.candidate,
        options.sourceLive.durableBase.version,
        () => true,
      );
      await options.repairPersist?.(
        options.candidate,
        result.snapshot?.version ?? options.sourceLive.durableBase.version,
        () => true,
      );
      return {
        candidate: options.candidate,
        conflicts: state.persistenceConflicts,
        persisted: result.ok,
        snapshot: result.snapshot,
        recoveryPending: false,
        writes: 1,
        ...(!result.ok ? { failure: result } : {}),
      };
    },
    reconcileStartupBoardFetch: async (options: Record<string, (...args: unknown[]) => unknown>) => {
      if (state.startupMode === 'throw') throw new Error('startup fetch failed');
      const remoteBoard = state.remoteBoard;
      const ids = new Map((remoteBoard?.tasks ?? []).map((item) => [item.id, `canonical-${item.id}`]));
      if (state.startupMode === 'callbacks' && remoteBoard) {
        options.readLive();
        options.readSnapshot();
        await options.persist(remoteBoard, ids, new Set(), { present: true, generation: 0 }, () => true);
        options.apply(snapshot(remoteBoard, 1));
        options.push(snapshot(remoteBoard, 1), remoteBoard, ids, 0);
        options.cancelled();
      }
      return {
        remoteBoard,
        remoteTaskIDs: ids,
        merged: state.startupMode === 'merged'
          ? { conflicts: ['startup title'] }
          : null,
        persisted: state.startupMode === 'merged',
      };
    },
    reconcilePendingBoardWrite: async (options: Record<string, (...args: unknown[]) => unknown>) => {
      if (state.pendingMode === 'throw') throw new Error('pending recovery failed');
      const live = options.readLive() as { board: Board; canonicalTaskIDs: ReadonlyMap<string, string> };
      const durable = options.readSnapshot() as ReturnType<typeof snapshot>;
      const saved = await options.persistAcknowledgement(
        live.board, live.canonicalTaskIDs, new Set(), durable.version, 'pending-op', () => true,
      ) as { snapshot?: ReturnType<typeof snapshot> };
      await options.repairPersist(
        live.board, live.canonicalTaskIDs, new Set(), durable.version, () => true,
      );
      const recovered = { needsPush: state.pendingMode === 'push', conflicts: ['pending title'] };
      options.apply(recovered, saved.snapshot ?? durable);
      if (state.pendingMode === 'push') options.queuePush(saved.snapshot ?? durable);
      options.cancelled();
      return {
        recoveryPending: state.pendingMode === 'retry',
        recovered,
        persisted: state.pendingMode !== 'retry',
      };
    },
    reconcileLegacyBootstrap: async (options: Record<string, (...args: unknown[]) => unknown>) => {
      if (state.legacyMode === 'throw') throw new Error('legacy recovery failed');
      const live = options.readLive() as { board: Board; canonicalTaskIDs: ReadonlyMap<string, string> };
      const durable = options.readSnapshot() as ReturnType<typeof snapshot>;
      const saved = await options.persist(
        live.board, live.canonicalTaskIDs, new Set(), durable.version, () => true,
      ) as { snapshot?: ReturnType<typeof snapshot> };
      await options.repairPersist(
        live.board, live.canonicalTaskIDs, new Set(), durable.version, () => true,
      );
      const needsPush = state.legacyMode === 'push';
      const recovered = { needsPush, conflicts: ['legacy title'] };
      options.apply(recovered, needsPush, saved.snapshot ?? durable);
      if (needsPush) options.queuePush(saved.snapshot ?? durable);
      options.cancelled();
      return { recovered, persisted: state.legacyMode !== 'failed' };
    },
  };
});

vi.mock('./lib/outbox', () => {
  class MetadataOutbox {
    cancel = vi.fn();
    private onStatus: (status: unknown) => void;
    constructor(_ns: string, options: { onStatus: (status: unknown) => void }) {
      this.onStatus = options.onStatus;
      state.outboxInstances.push(this);
    }
    surfaceStoredStatus() { if (state.outboxStatus) this.onStatus(state.outboxStatus); }
    reconcile = vi.fn(async () => undefined);
    drain = vi.fn(async () => undefined);
    enqueueTombstone = vi.fn(async () => {
      if (state.outboxReject) throw new Error('storage denied');
    });
    removeTombstone = vi.fn(async () => {
      if (state.outboxReject) throw new Error('storage denied');
    });
    enqueueImportLinks = vi.fn(async () => {
      if (state.outboxReject) throw new Error('storage denied');
    });
  }
  const report = async (
    outbox: { drain: (identity: Identity) => Promise<void> },
    identity: Identity,
    onStatus: (status: unknown) => void,
  ) => {
    try { await outbox.drain(identity); return true; }
    catch (error) { onStatus({ kind: 'blocked', message: String(error) }); return false; }
  };
  return {
    MetadataOutbox,
    drainAndReport: report,
    reconcileAndDrain: async (
      outbox: { reconcile: () => Promise<void>; drain: (identity: Identity) => Promise<void> },
      identity: Identity,
      _board: Board,
      _ids: ReadonlyMap<string, string>,
      onStatus: (status: unknown) => void,
    ) => { await outbox.reconcile(); return report(outbox, identity, onStatus); },
    enqueueIntentBeforeMutation: async (
      enqueue: () => Promise<unknown>, mutate: () => void, fail: (error: unknown) => void,
    ) => { try { await enqueue(); mutate(); return true; } catch (error) { fail(error); return false; } },
    removeIntentBeforeMutation: async (
      remove: () => Promise<unknown>, mutate: () => void, fail: (error: unknown) => void,
    ) => { try { await remove(); mutate(); return true; } catch (error) { fail(error); return false; } },
  };
});

vi.mock('./lib/api', () => ({
  getLabels: async () => state.labels,
  getSettings: async () => state.settings,
  getIntegrations: async () => state.sources,
  aiStory: vi.fn(async () => task({ id: 'ai-draft', title: 'AI draft' })),
  aiStories: vi.fn(async () => [task({ id: 'ai-story', title: 'AI story' })]),
  importPreview: vi.fn(async () => []),
  killReasonRequest: (taskId: string, reason: string) =>
    reason.trim() ? { taskId, reason: reason.trim() } : null,
}));

vi.mock('./lib/markdown', () => ({
  serialize: (value: Board) => {
    const text = `# ${value.title}`;
    state.exported.push(text);
    return text;
  },
  parse: () => {
    if (state.importError || !state.importBoard) throw new Error('invalid markdown');
    return state.importBoard;
  },
}));

vi.mock('./lib/confetti', () => ({
  burst: (x: number, y: number, count: number) => state.bursts.push([x, y, count]),
}));

vi.mock('./components/Board', () => ({
  showCancelledFlag: false,
  setShowCancelledFlag: vi.fn(),
  movedAnnouncement: (title: string, status: string) => `${title} moved to ${status}`,
  moveTask: (tasks: Task[], id: string, status: Task['status'], index: number, movedAt: string) => {
    const moving = tasks.find((item) => item.id === id);
    if (!moving) return tasks;
    const rest = tasks.filter((item) => item.id !== id);
    const next = { ...moving, status, movedAt };
    const positions = rest.reduce<number[]>((all, item, i) => {
      if (item.status === status) all.push(i);
      return all;
    }, []);
    const at = index >= positions.length ? rest.length : positions[index];
    return [...rest.slice(0, at), next, ...rest.slice(at)];
  },
  BoardView: ({
    board: value,
    onMove,
    onTick,
    onEdit,
    onAdd,
    onRestore,
    onPurge,
  }: {
    board: Board;
    onMove: (id: string, status: Task['status']) => void;
    onTick: (id: string, index: number, pos: { x: number; y: number }) => void;
    onEdit: (id: string) => void;
    onAdd: (status: Task['status']) => void;
    onRestore: (id: string) => void;
    onPurge: (id: string) => void;
  }) => (
    <section aria-label="board-double">
      <button onClick={() => onAdd('todo')}>add todo</button>
      <button onClick={() => onEdit('missing')}>edit missing</button>
      <button onClick={() => onMove('missing', 'done')}>move missing</button>
      <button onClick={() => onTick('missing', 0, { x: 0, y: 0 })}>tick missing</button>
      {value.tasks.map((item) => (
        <div key={item.id} data-testid={`task-${item.id}`} data-task={item.id}>
          <span>{item.title}:{item.status}:{item.checks.map((check) => String(check.done)).join(',')}</span>
          <button onClick={() => onEdit(item.id)}>edit {item.id}</button>
          <button onClick={() => onMove(item.id, 'doing')}>move doing {item.id}</button>
          <button onClick={() => onMove(item.id, 'done')}>move done {item.id}</button>
          <button onClick={() => onTick(item.id, 0, { x: 2, y: 3 })}>tick {item.id}</button>
          <button onClick={() => onTick(item.id, 99, { x: 2, y: 3 })}>tick invalid {item.id}</button>
          <button onClick={() => onRestore(item.id)}>restore {item.id}</button>
          <button onClick={() => onPurge(item.id)}>purge {item.id}</button>
        </div>
      ))}
    </section>
  ),
}));

vi.mock('./components/IdentityGate', () => ({
  IdentityGate: ({ onIdentity }: { onIdentity: (identity: Identity) => void }) => (
    <button onClick={() => onIdentity(manual)}>use manual identity</button>
  ),
}));

vi.mock('./components/CardModal', () => ({
  CardModal: ({ state: modal, onSave, onDelete, onClose, aiDraft }: {
    state: { mode: 'add'; status: Task['status'] } | { mode: 'edit'; task: Task };
    onSave: (item: Task) => void;
    onDelete: (id: string) => void;
    onClose: () => void;
    aiDraft?: unknown;
  }) => (
    <div role="dialog" aria-label="card modal">
      <span>{aiDraft ? 'ai enabled' : 'ai disabled'}</span>
      {typeof aiDraft === 'function' && <button onClick={() => void aiDraft({ title: 'draft' })}>request ai draft</button>}
      <button onClick={() => onSave(
        modal.mode === 'edit'
          ? { ...modal.task, title: `${modal.task.title} edited` }
          : task({ id: 'added', title: 'Added task', status: modal.status, checks: [] }),
      )}>save card</button>
      {modal.mode === 'edit' && <button onClick={() => onDelete(modal.task.id)}>delete card</button>}
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
  SettingsModal: ({ onSaved, onDebugChange, onClose, serverPresent }: {
    onSaved: (value: { has_key: boolean; ai_base_url: string }) => void;
    onDebugChange: (value: boolean) => void;
    onClose: () => void;
    serverPresent: boolean;
  }) => <div role="dialog" aria-label="settings"><span>{String(serverPresent)}</span><button onClick={() => onSaved({ has_key: true, ai_base_url: '' })}>enable ai</button><button onClick={() => onDebugChange(true)}>enable debug</button><button onClick={onClose}>close settings</button></div>,
}));

vi.mock('./components/AdrModal', () => ({
  AdrModal: ({ onSplit, onAdd, onClose }: {
    onSplit: (request: unknown, signal?: AbortSignal) => Promise<Task[]>;
    onAdd: (tasks: Task[]) => void;
    onClose: () => void;
  }) => <div role="dialog" aria-label="adr"><button onClick={() => void onSplit({ text: 'adr' }, new AbortController().signal)}>split adr request</button><button onClick={() => onAdd([])}>add no adr stories</button><button onClick={() => onAdd([task({ id: 'adr-added', title: 'ADR story', checks: [] })])}>add adr stories</button><button onClick={onClose}>close adr</button></div>,
}));

vi.mock('./components/ImportModal', () => ({
  ImportModal: ({ onPreview, onAdd, onCommitLinks, onClose }: {
    onPreview: (request: unknown, signal?: AbortSignal) => Promise<unknown>;
    onAdd: (tasks: Task[]) => void; onCommitLinks: (request: unknown) => void; onClose: () => void;
  }) => <div role="dialog" aria-label="issue import"><button onClick={() => void onPreview({ source: 'github' }, new AbortController().signal)}>preview issues</button><button onClick={() => onAdd([])}>add no issues</button><button onClick={() => onAdd([task({ id: 'issue-added', title: 'Imported issue', checks: [] })])}>add issues</button><button onClick={() => onCommitLinks({ source: 'github', items: [] })}>commit links</button><button onClick={onClose}>close issue import</button></div>,
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

import App, { LOCAL_PERSISTENCE_NOTICE } from './App';

beforeEach(() => {
  state.identity = manual;
  state.resolveIdentity = null;
  state.resolveError = null;
  state.board = board();
  state.importBoard = null;
  state.importError = false;
  state.dirty = false;
  state.detect = false;
  state.detectDeferred = false;
  state.detectResolve = null;
  state.remoteBoard = null;
  state.migratedRaw = false;
  state.pendingWrite = null;
  state.startupMode = 'normal';
  state.pendingMode = 'clean';
  state.legacyMode = 'clean';
  state.sameSemantics = false;
  state.commitMode = 'normal';
  state.omitAckSnapshot = false;
  state.ackCurrent = true;
  state.omitIsCurrent = false;
  state.persistenceFails = false;
  state.persistenceConflicts = [];
  state.remoteSaveError = null;
  state.prepareError = null;
  state.remoteSaveConflicts = [];
  state.labels = [];
  state.settings = { has_key: false, ai_base_url: '' };
  state.sources = [];
  state.outboxStatus = null;
  state.outboxReject = false;
  state.savedIdentities = [];
  state.clearedIdentity = 0;
  state.dirtyWrites = [];
  state.exported = [];
  state.remoteInstances = [];
  state.outboxInstances = [];
  state.bursts = [];
  vi.clearAllMocks();
  vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('unexpected network request'); }));
});

describe('App DOM orchestration', () => {
  it('adopts a manual identity and signs out without network access', async () => {
    state.identity = null;
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByRole('button', { name: 'use manual identity' }));
    expect(await screen.findByText('alice')).not.toBeNull();
    expect(state.savedIdentities).toEqual([manual]);

    await user.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(screen.getByRole('button', { name: 'use manual identity' })).not.toBeNull();
    expect(state.clearedIdentity).toBe(1);
    expect(fetch).not.toHaveBeenCalled();
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

  it('adds, edits, moves, checks, ships, cancels, restores, and purges cards', async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByRole('button', { name: 'add todo' }));
    await user.click(within(screen.getByRole('dialog', { name: 'card modal' })).getByRole('button', { name: 'save card' }));
    expect(await screen.findByText(/Added task:todo/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'edit added' }));
    await user.click(screen.getByRole('button', { name: 'save card' }));
    expect(await screen.findByText(/Added task edited:todo/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'move doing added' }));
    expect(await screen.findByText(/Added task edited:doing/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'move done task-1' }));
    expect(screen.getByRole('dialog', { name: 'ship' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'tick all' }));
    expect(await screen.findByText(/First task:done:true/)).not.toBeNull();
    expect(state.bursts.some((burst) => burst[2] === 70)).toBe(true);

    await user.click(screen.getByRole('button', { name: 'edit added' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.clear(screen.getByRole('textbox', { name: 'confirm input' }));
    await user.type(screen.getByRole('textbox', { name: 'confirm input' }), 'superseded');
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    expect(await screen.findByText(/Added task edited:cancelled/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'restore added' }));
    expect(await screen.findByText(/Added task edited:todo/)).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'purge added' }));
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    await waitFor(() => expect(screen.queryByTestId('task-added')).toBeNull());
  });

  it('handles ship cancellation/override, cancellation without a reason, and cancelled visibility', async () => {
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByRole('button', { name: 'move done task-1' }));
    await user.click(screen.getByRole('button', { name: 'cancel ship' }));
    expect(screen.queryByRole('dialog', { name: 'ship' })).toBeNull();
    expect(screen.getByText(/First task:todo:false/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'move done task-1' }));
    await user.click(screen.getByRole('button', { name: 'ship anyway' }));
    expect(await screen.findByText(/First task:done:false/)).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    expect(await screen.findByText(/First task:doing:false/)).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    expect(screen.getByText(/First task:doing:false/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.click(screen.getByRole('button', { name: 'secondary action' }));
    expect(await screen.findByText(/First task:cancelled:false/)).not.toBeNull();
    await user.click(screen.getByRole('button', { name: /Cancelled/ }));
    expect(screen.getByRole('button', { name: /Cancelled/ }).getAttribute('aria-pressed')).toBe('true');
  });

  it('ignores missing task/check callbacks, unticks completed checks, and handles a blank kill reason', async () => {
    state.board = board([task({ checks: [
      { text: 'done', done: true },
      { text: 'other', done: false },
    ] })]);
    const user = userEvent.setup();
    render(<App />);

    await user.click(screen.getByRole('button', { name: 'edit missing' }));
    await user.click(screen.getByRole('button', { name: 'move missing' }));
    await user.click(screen.getByRole('button', { name: 'tick missing' }));
    await user.click(screen.getByRole('button', { name: 'tick invalid task-1' }));
    expect(screen.queryByRole('dialog', { name: 'card modal' })).toBeNull();
    expect(screen.getByText(/First task:todo:true,false/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: 'tick task-1' }));
    expect(await screen.findByText(/First task:todo:false,false/)).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.clear(screen.getByRole('textbox', { name: 'confirm input' }));
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    expect(await screen.findByText(/First task:cancelled:false,false/)).not.toBeNull();
  });

  it('blocks restore when its durable tombstone cannot be removed', async () => {
    state.board = board([task({ status: 'cancelled' })]);
    state.outboxReject = true;
    render(<App />);
    await userEvent.setup().click(screen.getByRole('button', { name: 'restore task-1' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'card remains cancelled',
      );
    });
    expect(screen.getByText(/First task:cancelled/)).not.toBeNull();
  });

  it('auto-ships a completed checklist but never resurrects a cancelled card', async () => {
    vi.useFakeTimers();
    state.board = board([
      task({ id: 'active', checks: [{ text: 'last', done: false }] }),
      task({ id: 'cancelled', status: 'cancelled', checks: [{ text: 'last', done: false }] }),
    ]);
    try {
      render(<App />);
      fireEvent.click(screen.getByRole('button', { name: 'tick active' }));
      fireEvent.click(screen.getByRole('button', { name: 'tick cancelled' }));
      await act(async () => { vi.advanceTimersByTime(351); });
      expect(screen.getByText(/First task moved to done/)).not.toBeNull();
      expect(screen.getByText(/First task:cancelled:true/)).not.toBeNull();
      expect(state.bursts.some((burst) => burst[2] === 14)).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('surfaces persistence and outbox failures, deduplicates notices, and re-arms after dismissal', async () => {
    state.persistenceFails = true;
    state.outboxStatus = { kind: 'blocked', message: 'journal corrupt' };
    const user = userEvent.setup();
    render(<App />);

    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'Metadata delivery is blocked: journal corrupt',
      );
    });
    await user.click(screen.getByRole('button', { name: 'tick task-1' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(LOCAL_PERSISTENCE_NOTICE);
    });
    expect(document.querySelector('.notice')?.textContent?.match(/journal corrupt/g)).toHaveLength(1);

    await user.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(document.querySelector('.notice')).toBeNull();
    state.outboxStatus = { kind: 'retry', message: 'offline' };
    fireEvent(window, new Event('online'));
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(LOCAL_PERSISTENCE_NOTICE);
    });
  });

  it('ignores idle outbox status and renders retry status distinctly', async () => {
    state.outboxStatus = { kind: 'idle' };
    const idle = render(<App />);
    await waitFor(() => expect(state.outboxInstances.length).toBeGreaterThan(0));
    expect(document.querySelector('.notice')).toBeNull();
    idle.unmount();

    state.outboxStatus = { kind: 'retry', message: 'network offline' };
    render(<App />);
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'Metadata delivery will retry: network offline',
      );
    });
  });

  it('bootstraps a remote board and gates AI/source actions from server settings', async () => {
    state.detect = true;
    state.remoteBoard = board([task({ id: 'remote', title: 'Remote task', checks: [] })]);
    state.labels = ['server-label'];
    state.settings = { has_key: true, ai_base_url: '' };
    state.sources = [{ id: 'github', name: 'GitHub', kind: 'github' }];
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByText(/Remote task:todo/)).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'edit remote' }));
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
    expect(await screen.findByText(/ADR story:todo/)).not.toBeNull();

    await user.click(screen.getByRole('button', { name: /Import issues/ }));
    await user.click(screen.getByRole('button', { name: 'preview issues' }));
    await user.click(screen.getByRole('button', { name: 'add no issues' }));
    expect(screen.queryByRole('dialog', { name: 'issue import' })).toBeNull();
    await user.click(screen.getByRole('button', { name: /Import issues/ }));
    state.outboxReject = true;
    await user.click(screen.getByRole('button', { name: 'commit links' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'import provenance could not be stored locally',
      );
    });
    state.outboxReject = false;
    await user.click(screen.getByRole('button', { name: 'close issue import' }));
    await user.click(screen.getByRole('button', { name: /Import issues/ }));
    await user.click(screen.getByRole('button', { name: 'add issues' }));
    expect(await screen.findByText(/Imported issue:todo/)).not.toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('executes startup reconciliation callbacks and recognizes unchanged server state', async () => {
    state.detect = true;
    state.remoteBoard = board([task({ id: 'same', title: 'Same remote', checks: [] })]);
    state.startupMode = 'callbacks';
    state.sameSemantics = true;
    render(<App />);

    expect(await screen.findByText(/Same remote:todo/)).not.toBeNull();
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'synced to server' })).not.toBeNull();
    });
    expect(state.remoteInstances[0].flush).not.toHaveBeenCalled();
  });

  it('takes the unchanged remote-board fast path without writing local state', async () => {
    state.detect = true;
    state.remoteBoard = board([task({ id: 'same', title: 'Same remote', checks: [] })]);
    state.sameSemantics = true;
    render(<App />);
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'synced to server' })).not.toBeNull();
    });
    expect(screen.getByText(/First task:todo/)).not.toBeNull();
    expect(state.dirtyWrites).not.toContain(true);
  });

  it('closes open card work and reports it when a delayed remote board is adopted', async () => {
    state.detect = true;
    state.detectDeferred = true;
    state.remoteBoard = board([task({ id: 'late', title: 'Late remote', checks: [] })]);
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    expect(screen.getByRole('dialog', { name: 'card modal' })).not.toBeNull();
    await act(async () => { state.detectResolve?.(true); });
    expect(await screen.findByText(/Late remote:todo/)).not.toBeNull();
    expect(screen.queryByRole('dialog', { name: 'card modal' })).toBeNull();
    expect(document.querySelector('.notice')?.textContent).toContain(
      'card you had open was closed',
    );
  });

  it('reports a merged startup and a failed startup fetch without overwriting local state', async () => {
    state.detect = true;
    state.remoteBoard = board([task({ id: 'merged', title: 'Merged remote', checks: [] })]);
    state.startupMode = 'merged';
    const merged = render(<App />);
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'Concurrent edits conflicted in startup title',
      );
    });
    expect(screen.getByText(/First task:todo/)).not.toBeNull();
    merged.unmount();

    state.startupMode = 'throw';
    state.remoteBoard = null;
    render(<App />);
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'last save to server failed' })).not.toBeNull();
    });
    expect(screen.getByText(/First task:todo/)).not.toBeNull();
  });

  it('keeps local data dirty when remote adoption cannot persist', async () => {
    state.detect = true;
    state.remoteBoard = board([task({ id: 'remote-fail', title: 'Remote failed', checks: [] })]);
    state.persistenceFails = true;
    render(<App />);
    await waitFor(() => expect(state.dirtyWrites).toContain(true));
    expect(screen.getByText(/First task:todo/)).not.toBeNull();
    expect(screen.queryByText(/Remote failed/)).toBeNull();
  });

  it('recovers a clean pending write and exposes its merge result', async () => {
    state.detect = true;
    state.pendingWrite = { operation_id: 'pending-op' };
    state.pendingMode = 'clean';
    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'synced to server' })).not.toBeNull();
      expect(document.querySelector('.notice')?.textContent).toContain(
        'Concurrent edits conflicted in pending title',
      );
    });
    expect(state.dirtyWrites).toContain(false);
  });

  it('queues pending-write push recovery and retries a deferred recovery after persistence', async () => {
    state.detect = true;
    state.pendingWrite = { operation_id: 'pending-op' };
    state.pendingMode = 'push';
    const pushed = render(<App />);
    await waitFor(() => expect(state.dirtyWrites).toContain(true));
    expect(state.remoteSaveConflicts).toEqual([]);
    pushed.unmount();

    state.pendingMode = 'retry';
    state.dirtyWrites = [];
    render(<App />);
    await waitFor(() => expect(state.dirtyWrites).toContain(true));
    state.pendingMode = 'clean';
    await userEvent.setup().click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => expect(state.dirtyWrites).toContain(false));
  });

  it('surfaces pending-write recovery failures as save errors', async () => {
    state.detect = true;
    state.pendingWrite = { operation_id: 'pending-op' };
    state.pendingMode = 'throw';
    render(<App />);
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'last save to server failed' })).not.toBeNull();
    });
  });

  it('completes legacy identity recovery for clean, push, failed, and thrown outcomes', async () => {
    state.detect = true;
    state.dirty = true;
    state.migratedRaw = true;
    state.board = board([task({ id: 'legacy', title: 'Legacy local', checks: [] })]);
    state.legacyMode = 'clean';
    const clean = render(<App />);
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'recovered from older local data',
      );
      expect(state.dirtyWrites).toContain(false);
    });
    clean.unmount();

    state.dirty = true;
    state.dirtyWrites = [];
    state.legacyMode = 'push';
    const pushed = render(<App />);
    await waitFor(() => expect(state.dirtyWrites).toContain(true));
    pushed.unmount();

    state.dirty = true;
    state.dirtyWrites = [];
    state.legacyMode = 'failed';
    const failed = render(<App />);
    await waitFor(() => expect(state.dirtyWrites).toContain(true));
    failed.unmount();

    state.dirty = true;
    state.legacyMode = 'throw';
    render(<App />);
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'last save to server failed' })).not.toBeNull();
    });
  });

  it('keeps dirty on local commit conflicts, recovery-pending commits, and stopped commits', async () => {
    const run = async (mode: 'recovery' | 'conflict' | 'stopped') => {
      state.commitMode = mode;
      state.dirtyWrites = [];
      const rendered = render(<App />);
      await userEvent.setup().click(screen.getByRole('button', { name: 'move doing task-1' }));
      await waitFor(() => expect(state.dirtyWrites).toContain(true));
      rendered.unmount();
      state.board = board();
    };
    await run('recovery');
    await run('conflict');
    await run('stopped');
  });

  it('does not accept remote acknowledgements without a durable base or current epoch', async () => {
    state.detect = true;
    state.dirty = true;
    state.omitAckSnapshot = true;
    const missingBase = render(<App />);
    await waitFor(() => expect(state.remoteInstances[0]).toBeDefined());
    expect(state.dirtyWrites).not.toContain(false);
    missingBase.unmount();

    state.board = board();
    state.dirty = true;
    state.omitAckSnapshot = false;
    state.ackCurrent = false;
    render(<App />);
    await waitFor(() => expect(state.remoteInstances.length).toBeGreaterThan(1));
    expect(state.dirtyWrites.at(-1)).not.toBe(false);
  });

  it('uses the acknowledgement currentness default when the adapter omits the callback', async () => {
    state.detect = true;
    state.dirty = true;
    state.omitIsCurrent = true;
    render(<App />);
    await waitFor(() => expect(state.remoteInstances.length).toBeGreaterThan(0));
    expect(state.dirtyWrites).not.toContain(false);
    expect(screen.getByRole('img', { name: 'sync off — local only' })).not.toBeNull();
  });

  it('handles dirty-map preparation and persistence failures without discarding the board', async () => {
    state.detect = true;
    state.dirty = true;
    state.prepareError = new Error('mapping failed');
    const preparation = render(<App />);
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'last save to server failed' })).not.toBeNull();
    });
    expect(screen.getByText(/First task:todo/)).not.toBeNull();
    preparation.unmount();

    state.board = board();
    state.prepareError = null;
    state.persistenceFails = true;
    state.dirty = true;
    state.dirtyWrites = [];
    render(<App />);
    await waitFor(() => expect(state.dirtyWrites).toContain(true));
    expect(screen.getByText(/First task:todo/)).not.toBeNull();
  });

  it('opens settings offline, updates AI/debug state, and closes modal gates', async () => {
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getByRole('button', { name: 'Settings' }));
    const settings = screen.getByRole('dialog', { name: 'settings' });
    expect(within(settings).getByText('false')).not.toBeNull();
    expect(document.querySelector('.app-shell')?.hasAttribute('inert')).toBe(true);
    await user.click(within(settings).getByRole('button', { name: 'enable debug' }));
    expect(screen.getByLabelText('debug')).not.toBeNull();
    await user.click(within(settings).getByRole('button', { name: 'close settings' }));
    await user.click(screen.getByRole('button', { name: 'close debug' }));
    expect(screen.queryByLabelText('debug')).toBeNull();
  });

  it('exports, imports, confirms replacement, and reports malformed imports', async () => {
    const user = userEvent.setup();
    const { container } = render(<App />);
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    await user.click(screen.getByRole('button', { name: 'Export' }));
    expect(state.exported).toEqual(['# Test board']);
    expect(click).toHaveBeenCalledOnce();

    state.importBoard = board([
      task({ id: 'imported', title: 'Imported markdown', checks: [] }),
      task({ id: 'imported-2', title: 'Second markdown task', checks: [] }),
    ]);
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const inputClick = vi.spyOn(input, 'click');
    await user.click(screen.getByRole('button', { name: 'Import' }));
    expect(inputClick).toHaveBeenCalledOnce();
    fireEvent.change(input, { target: { files: [] } });
    await user.upload(input, new File(['# imported'], 'board.md', { type: 'text/markdown' }));
    expect(await screen.findByRole('dialog', { name: 'Replace the current board?' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    expect(await screen.findByText(/Imported markdown:todo/)).not.toBeNull();
    expect(screen.getByText(/Second markdown task:todo/)).not.toBeNull();

    state.importError = true;
    await user.upload(input, new File(['bad'], 'bad.md', { type: 'text/markdown' }));
    expect(await screen.findByRole('dialog', { name: 'Import failed' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'close confirm' }));
    expect(screen.queryByRole('dialog', { name: 'Import failed' })).toBeNull();

    state.importError = false;
    state.importBoard = board([task({ id: 'single-import', title: 'Single import', checks: [] })]);
    await user.upload(input, new File(['# single'], 'single.md', { type: 'text/markdown' }));
    expect(await screen.findByRole('dialog', { name: 'Replace the current board?' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'close confirm' }));
  });

  it('surfaces reauthentication, reconnects, and announces conflict recovery', async () => {
    const { ReauthRequiredError } = await import('./lib/auth');
    state.detect = true;
    state.dirty = true;
    state.remoteSaveError = new ReauthRequiredError('expired');
    state.persistenceConflicts = ['board title'];
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByRole('dialog', { name: 'reconnect' })).not.toBeNull();
    expect(screen.getByRole('img', { name: /session expired/ })).not.toBeNull();
    state.remoteSaveError = null;
    await user.click(screen.getByRole('button', { name: 'reconnect now' }));
    expect(state.savedIdentities.at(-1)).toMatchObject({ serverToken: 'fresh' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'reconnect' })).toBeNull());

    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    expect(await screen.findByText(/Concurrent edits conflicted in board title/)).not.toBeNull();
  });

  it('announces recovery after an ordinary remote save error', async () => {
    state.detect = true;
    state.dirty = true;
    state.remoteSaveError = new Error('server unavailable');
    const user = userEvent.setup();
    render(<App />);
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'last save to server failed' })).not.toBeNull();
    });
    state.remoteSaveError = null;
    await user.click(screen.getByRole('button', { name: 'move doing task-1' }));
    await waitFor(() => {
      expect(screen.getByRole('img', { name: 'synced to server' })).not.toBeNull();
      expect(screen.getByText('synced to server')).not.toBeNull();
    });
  });

  it('surfaces stored outbox reauthentication and retains explicit reconnect/signout exits', async () => {
    state.outboxStatus = { kind: 'reauth', message: 'token expired' };
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByRole('dialog', { name: 'reconnect' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'close reconnect' }));
    expect(screen.getByRole('button', { name: 'Reconnect' })).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'Reconnect' }));
    await user.click(screen.getByRole('button', { name: 'reconnect signout' }));
    expect(screen.getByRole('button', { name: 'use manual identity' })).not.toBeNull();
  });

  it('keeps cancelled/purge mutations blocked when durable metadata cannot change', async () => {
    state.outboxReject = true;
    const user = userEvent.setup();
    render(<App />);
    await user.click(screen.getByRole('button', { name: 'edit task-1' }));
    await user.click(screen.getByRole('button', { name: 'delete card' }));
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain(
        'cancellation reason could not be stored',
      );
    });
    expect(screen.getByText(/First task:todo/)).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'close card' }));
    await user.click(screen.getByRole('button', { name: 'purge task-1' }));
    await user.click(screen.getByRole('button', { name: 'confirm action' }));
    await waitFor(() => {
      expect(document.querySelector('.notice')?.textContent).toContain('card was not deleted');
    });
    expect(screen.getByTestId('task-task-1')).not.toBeNull();
  });

  it('flushes on pagehide and cancels remote/outbox work on unmount', () => {
    const rendered = render(<App />);
    fireEvent(window, new Event('pagehide'));
    expect(state.remoteInstances[0].flush).toHaveBeenCalledOnce();
    rendered.unmount();
    expect(state.remoteInstances[0].cancel).toHaveBeenCalledOnce();
    expect(state.outboxInstances[0].cancel).toHaveBeenCalledOnce();
  });
});
