import { afterEach, describe, expect, it, vi } from 'vitest';
import { ReauthRequiredError } from './auth';
import type { Identity } from './auth';
import type { Board } from './model';
import {
  drainAndReport,
  enqueueIntentBeforeMutation,
  MetadataOutbox,
  reconcileAndDrain,
  removeIntentBeforeMutation,
} from './outbox';
import { namespaceStorageKey } from './store';

const identity: Identity = { kind: 'manual', id: 'coverage' };
const board: Board = { title: 'board', tasks: [] };

class MemoryStorage implements Storage {
  readonly values = new Map<string, string>();
  failRead = false;
  get length() { if (this.failRead) throw new Error('read denied'); return this.values.size; }
  clear() { this.values.clear(); }
  getItem(key: string) { if (this.failRead) throw new Error('read denied'); return this.values.get(key) ?? null; }
  key(index: number) { if (this.failRead) throw new Error('read denied'); return [...this.values.keys()][index] ?? null; }
  removeItem(key: string) { this.values.delete(key); }
  setItem(key: string, value: string) { this.values.set(key, String(value)); }
}

const locks = {
  request: async (...args: unknown[]) => (args.at(-1) as () => unknown)(),
} as unknown as LockManager;

const importRequest = {
  source: 'work',
  items: [{ external_key: 'x:1', link: 'x#1', url: 'https://x/1', title: 'work' }],
};

function recordKey(ns: string, logical: string) {
  return namespaceStorageKey('kb.outbox.v1', ns, encodeURIComponent(logical));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('outbox successful sequencing', () => {
  it('reports a successful drain without emitting a status', async () => {
    const drain = vi.fn(async () => undefined);
    const onStatus = vi.fn();
    await expect(drainAndReport({ drain }, identity, onStatus)).resolves.toBe(true);
    expect(drain).toHaveBeenCalledWith(identity);
    expect(onStatus).not.toHaveBeenCalled();
  });

  it('drains only after reconciliation succeeds', async () => {
    const calls: string[] = [];
    const outbox = {
      reconcile: vi.fn(async () => { calls.push('reconcile'); }),
      drain: vi.fn(async () => { calls.push('drain'); }),
    };
    await expect(reconcileAndDrain(outbox, identity, board, new Map(), vi.fn()))
      .resolves.toBe(true);
    expect(calls).toEqual(['reconcile', 'drain']);
  });

  it('mutates only after a stale intent is removed', async () => {
    const calls: string[] = [];
    await expect(removeIntentBeforeMutation(
      async () => { calls.push('remove'); },
      () => { calls.push('mutate'); },
      vi.fn(),
    )).resolves.toBe(true);
    expect(calls).toEqual(['remove', 'mutate']);
  });

  it('mutates only after a new intent is enqueued', async () => {
    const calls: string[] = [];
    await expect(enqueueIntentBeforeMutation(
      async () => { calls.push('enqueue'); },
      () => { calls.push('mutate'); },
      vi.fn(),
    )).resolves.toBe(true);
    expect(calls).toEqual(['enqueue', 'mutate']);
  });
});

describe('outbox residual record states', () => {
  it('surfaces a durable blocked record with its fallback message', () => {
    const storage = new MemoryStorage();
    storage.setItem(recordKey('coverage', 'import:x:1'), JSON.stringify({
      version: 1, generation: 'g1', kind: 'import', state: 'blocked',
      source: 'work', item: importRequest.items[0],
    }));
    const onStatus = vi.fn();
    new MetadataOutbox('coverage', { storage, locks, onStatus }).surfaceStoredStatus();
    expect(onStatus).toHaveBeenCalledWith({
      kind: 'blocked', message: 'metadata delivery needs attention',
    });
  });

  it('surfaces storage read failures', () => {
    const storage = new MemoryStorage();
    const onStatus = vi.fn();
    const outbox = new MetadataOutbox('coverage', { storage, locks, onStatus });
    storage.failRead = true;
    outbox.surfaceStoredStatus();
    expect(onStatus).toHaveBeenCalledWith({
      kind: 'blocked', message: 'metadata storage could not be read',
    });
  });

  it('ignores malformed records while preserving valid ones', () => {
    const invalid = [
      null,
      {},
      { version: 1, generation: 'g', kind: 'tombstone', state: 'queued' },
      { version: 1, generation: 'g', kind: 'import', state: 'queued', source: 'x', item: {} },
    ];
    for (const [index, value] of invalid.entries()) {
      const storage = new MemoryStorage();
      storage.setItem(recordKey('coverage', `import:${index}`), value === null ? '{' : JSON.stringify(value));
      expect(new MetadataOutbox('coverage', { storage, locks }).records()).toEqual([]);
    }
  });

  it.each([
    ['array', []],
    ['wrong version', { version: 2, generation: 'g' }],
    ['missing generation', { version: 1 }],
    ['tombstone client', { version: 1, generation: 'g', kind: 'tombstone', state: 'queued', clientTaskId: 7, reason: 'x', canonicalTaskId: 'c' }],
    ['tombstone reason', { version: 1, generation: 'g', kind: 'tombstone', state: 'queued', clientTaskId: 'x', reason: 7, canonicalTaskId: 'c' }],
    ['tombstone state', { version: 1, generation: 'g', kind: 'tombstone', state: 'other', clientTaskId: 'x', reason: 'x' }],
    ['queued canonical', { version: 1, generation: 'g', kind: 'tombstone', state: 'queued', clientTaskId: 'x', reason: 'x' }],
    ['import state', { version: 1, generation: 'g', kind: 'import', state: 'other' }],
    ['import source', { version: 1, generation: 'g', kind: 'import', state: 'queued', source: 7 }],
    ['import item', { version: 1, generation: 'g', kind: 'import', state: 'queued', source: 'x', item: [] }],
    ['import external key', { version: 1, generation: 'g', kind: 'import', state: 'queued', source: 'x', item: { external_key: 7, link: 'x', url: 'x', title: 'x' } }],
    ['import link', { version: 1, generation: 'g', kind: 'import', state: 'queued', source: 'x', item: { external_key: 'x', link: 7, url: 'x', title: 'x' } }],
    ['import url', { version: 1, generation: 'g', kind: 'import', state: 'queued', source: 'x', item: { external_key: 'x', link: 'x', url: 7, title: 'x' } }],
    ['import title', { version: 1, generation: 'g', kind: 'import', state: 'queued', source: 'x', item: { external_key: 'x', link: 'x', url: 'x', title: 7 } }],
  ] as const)('rejects malformed %s records', (_name, value) => {
    const storage = new MemoryStorage();
    storage.setItem(recordKey('coverage', 'invalid'), JSON.stringify(value));
    expect(new MetadataOutbox('coverage', { storage, locks }).records()).toEqual([]);
  });

  it('uses browser defaults when constructor options are omitted', async () => {
    const storage = new MemoryStorage();
    vi.stubGlobal('localStorage', storage);
    vi.stubGlobal('navigator', { locks });
    const outbox = new MetadataOutbox('coverage');
    await outbox.enqueueImportLinks(importRequest);
    expect(outbox.records()).toHaveLength(1);
  });

  it('falls back to local persistence when navigator is absent', async () => {
    const storage = new MemoryStorage();
    vi.stubGlobal('localStorage', storage);
    vi.stubGlobal('navigator', undefined);
    const outbox = new MetadataOutbox('coverage');
    await outbox.enqueueTombstone('client', 'reason');
    expect(outbox.records()).toHaveLength(1);
  });

  it('ignores a key whose value disappears before parsing', () => {
    const storage = new MemoryStorage();
    storage.setItem(recordKey('coverage', 'missing'), 'temporary');
    storage.getItem = vi.fn(() => null);
    expect(new MetadataOutbox('coverage', { storage, locks }).records()).toEqual([]);
  });

  it('copies a valid legacy record without overwriting a framed record', () => {
    const storage = new MemoryStorage();
    const legacy = `kb.outbox.v1.coverage.${encodeURIComponent('import:x:1')}`;
    const framed = recordKey('coverage', 'import:x:1');
    const record = JSON.stringify({
      version: 1, generation: 'old', kind: 'import', state: 'queued',
      source: 'work', item: importRequest.items[0],
    });
    storage.setItem(legacy, record);
    new MetadataOutbox('coverage', { storage, locks });
    expect(storage.getItem(framed)).toBe(record);
    storage.setItem(framed, record.replace('old', 'new'));
    new MetadataOutbox('coverage', { storage, locks });
    expect(storage.getItem(framed)).toContain('new');
  });

  it('skips records that are not eligible for delivery', async () => {
    const storage = new MemoryStorage();
    for (const [index, state] of ['awaiting_canonical', 'blocked', 'sending'].entries()) {
      storage.setItem(recordKey('coverage', `tombstone:${index}`), JSON.stringify({
        version: 1, generation: `g${index}`, kind: 'tombstone', state,
        clientTaskId: String(index), reason: 'x',
        ...(state === 'awaiting_canonical' ? {} : { canonicalTaskId: `c${index}` }),
      }));
    }
    const send = vi.fn();
    await new MetadataOutbox('coverage', { storage, locks, sendTombstone: send }).drain(identity);
    expect(send).not.toHaveBeenCalled();
  });

  it('ignores malformed and already-queued records during reconciliation', async () => {
    const storage = new MemoryStorage();
    storage.setItem(recordKey('coverage', 'bad'), '{');
    storage.setItem(recordKey('coverage', 'import:x:1'), JSON.stringify({
      version: 1, generation: 'g', kind: 'import', state: 'queued',
      source: 'work', item: importRequest.items[0],
    }));
    storage.setItem(recordKey('coverage', 'tombstone:client'), JSON.stringify({
      version: 1, generation: 'g', kind: 'tombstone', state: 'queued',
      clientTaskId: 'client', canonicalTaskId: 'canonical', reason: 'reason',
    }));
    const outbox = new MetadataOutbox('coverage', { storage, locks });
    await outbox.reconcile({ title: 'b', tasks: [{
      id: 'client', title: 't', status: 'cancelled', emoji: '', desc: '', blocked: false,
      prio: 3, tags: [], checks: [], createdAt: 'x', movedAt: 'x',
    }] }, new Map([['client', 'canonical']]));
    expect(outbox.records()).toHaveLength(2);
  });

  it('uses the safe fallback message for a non-error rejection', async () => {
    const storage = new MemoryStorage();
    const status = vi.fn();
    const outbox = new MetadataOutbox('coverage', {
      storage, locks, sendImport: vi.fn(() => Promise.reject('offline')), onStatus: status,
    });
    await outbox.enqueueImportLinks(importRequest);
    await outbox.drain(identity);
    expect(status).toHaveBeenCalledWith({ kind: 'retry', message: 'metadata delivery failed' });
  });

  it('ignores a second drain while delivery is already running', async () => {
    const storage = new MemoryStorage();
    let finish!: () => void;
    const sending = new Promise<void>((resolve) => { finish = resolve; });
    const sendImport = vi.fn(() => sending);
    const outbox = new MetadataOutbox('coverage', { storage, locks, sendImport });
    await outbox.enqueueImportLinks(importRequest);
    const first = outbox.drain(identity);
    await Promise.resolve();
    await expect(outbox.drain(identity)).resolves.toBeUndefined();
    finish();
    await first;
    expect(sendImport).toHaveBeenCalledTimes(1);
  });

  it('drops a successful response after its session is cancelled', async () => {
    const storage = new MemoryStorage();
    let finish!: () => void;
    const sending = new Promise<void>((resolve) => { finish = resolve; });
    const outbox = new MetadataOutbox('coverage', {
      storage, locks, sendImport: vi.fn(() => sending),
    });
    await outbox.enqueueImportLinks(importRequest);
    const drain = outbox.drain(identity);
    await Promise.resolve();
    outbox.cancel();
    finish();
    await drain;
    expect(outbox.records()[0]?.state).toBe('sending');
  });

  it('drops a failed response after its session is cancelled', async () => {
    const storage = new MemoryStorage();
    let fail!: (reason: unknown) => void;
    const sending = new Promise<void>((_resolve, reject) => { fail = reject; });
    const status = vi.fn();
    const outbox = new MetadataOutbox('coverage', {
      storage, locks, sendImport: vi.fn(() => sending), onStatus: status,
    });
    await outbox.enqueueImportLinks(importRequest);
    const drain = outbox.drain(identity);
    await Promise.resolve();
    outbox.cancel();
    fail(new Error('late'));
    await drain;
    expect(status).not.toHaveBeenCalled();
  });

  it('resets interrupted import and tombstone sends to retry', async () => {
    const storage = new MemoryStorage();
    storage.setItem(recordKey('coverage', 'import:x:1'), JSON.stringify({
      version: 1, generation: 'g1', kind: 'import', state: 'sending',
      source: 'work', item: importRequest.items[0],
    }));
    storage.setItem(recordKey('coverage', 'tombstone:client'), JSON.stringify({
      version: 1, generation: 'g2', kind: 'tombstone', state: 'sending',
      clientTaskId: 'client', canonicalTaskId: 'canonical', reason: 'old',
    }));
    const outbox = new MetadataOutbox('coverage', { storage, locks, generation: () => 'next' });
    await outbox.reconcile({ title: 'b', tasks: [{
      id: 'client', title: 't', status: 'cancelled', emoji: '', desc: '', blocked: false,
      prio: 3, tags: [], checks: [], createdAt: 'x', movedAt: 'x',
    }] }, new Map([['client', 'canonical']]));
    expect(outbox.records().map((record) => record.state)).toEqual(['retry', 'retry']);
  });

  it('leaves awaiting tombstones pending until canonical identity exists', async () => {
    const storage = new MemoryStorage();
    const outbox = new MetadataOutbox('coverage', { storage, locks, generation: () => 'g' });
    await outbox.enqueueTombstone('client', 'reason');
    await outbox.reconcile({ title: 'b', tasks: [{
      id: 'client', title: 't', status: 'cancelled', emoji: '', desc: '', blocked: false,
      prio: 3, tags: [], checks: [], createdAt: 'x', movedAt: 'x',
    }] }, new Map());
    expect(outbox.records()[0]?.state).toBe('awaiting_canonical');
  });

  it.each([
    ['retry', new TypeError('offline')],
    ['retry', Object.assign(new Error('server'), { status: 503 })],
    ['blocked', Object.assign(new Error('bad request'), { status: 400 })],
    ['reauth', new ReauthRequiredError()],
  ] as const)('classifies delivery failure as %s', async (kind, failure) => {
    const storage = new MemoryStorage();
    const statuses = vi.fn();
    const outbox = new MetadataOutbox('coverage', {
      storage, locks, generation: () => crypto.randomUUID(),
      sendImport: vi.fn(() => Promise.reject(failure)),
      onStatus: statuses,
    });
    await outbox.enqueueImportLinks(importRequest);
    await outbox.drain(identity);
    expect(statuses).toHaveBeenCalledWith(expect.objectContaining({ kind }));
    expect(outbox.records()[0]?.state).toBe(kind === 'blocked' ? 'blocked' : 'retry');
  });
});
