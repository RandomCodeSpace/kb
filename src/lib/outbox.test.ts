import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ReauthRequiredError } from './auth';
import type { Board } from './model';
import {
  drainAndReport,
  enqueueIntentBeforeMutation,
  MetadataOutbox,
  reconcileAndDrain,
  removeIntentBeforeMutation,
} from './outbox';

class MemoryStorage implements Storage {
  readonly values = new Map<string, string>();
  failRemove = false;
  failSet = false;
  get length() { return this.values.size; }
  clear() { this.values.clear(); }
  getItem(key: string) { return this.values.get(key) ?? null; }
  key(index: number) { return [...this.values.keys()][index] ?? null; }
  removeItem(key: string) {
    if (this.failRemove) throw new DOMException('storage denied', 'SecurityError');
    this.values.delete(key);
  }
  setItem(key: string, value: string) {
    if (this.failSet) throw new DOMException('quota exceeded', 'QuotaExceededError');
    this.values.set(key, String(value));
  }
}

class SerialLocks {
  private tail: Promise<unknown> = Promise.resolve();
  request<T>(_name: string, callback: () => Promise<T> | T): Promise<T> {
    const result = this.tail.then(callback);
    this.tail = result.then(() => undefined, () => undefined);
    return result;
  }
}

const identity = { kind: 'manual' as const, id: 'alice', serverToken: 'secret' };
const cancelled: Board = {
  title: 'kb',
  tasks: [{
    id: 'client-1', emoji: '', title: 'cancel me', desc: '', status: 'cancelled',
    blocked: false, prio: 2, tags: [], checks: [],
    createdAt: '2026-01-01T00:00:00Z', movedAt: '2026-01-01T00:00:00Z',
  }],
};
const restored: Board = {
  ...cancelled,
  tasks: cancelled.tasks.map((task) => ({ ...task, status: 'todo' as const })),
};
const importRequest = {
  source: 'work',
  items: [{
    external_key: 'gitlab:example/acme#42',
    link: 'gitlab#42',
    url: 'https://gitlab.example/acme/-/issues/42',
    title: 'login',
  }],
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function generations() {
  let value = 0;
  return () => `generation-${++value}`;
}

let storage: MemoryStorage;
let locks: SerialLocks;

beforeEach(() => {
  storage = new MemoryStorage();
  locks = new SerialLocks();
});

describe('MetadataOutbox', () => {
  it.each(['acknowledgement', 'startup'])(
    'blocks %s drain when durable reconciliation fails',
    async () => {
      const failure = new DOMException('quota exceeded', 'QuotaExceededError');
      const target = {
        reconcile: vi.fn(() => Promise.reject(failure)),
        drain: vi.fn(() => Promise.resolve()),
      };
      const statuses = vi.fn();

      await expect(reconcileAndDrain(
        target,
        identity,
        cancelled,
        new Map([['client-1', 'canonical-1']]),
        statuses,
      )).resolves.toBe(false);

      expect(target.reconcile).toHaveBeenCalledOnce();
      expect(target.drain).not.toHaveBeenCalled();
      expect(statuses).toHaveBeenCalledOnce();
      expect(statuses).toHaveBeenCalledWith({
        kind: 'blocked',
        message: 'metadata storage could not be reconciled: quota exceeded',
      });
    },
  );

  it('classifies one escaped drain failure without rejecting', async () => {
    const failure = new ReauthRequiredError();
    const statuses = vi.fn();
    const target = {
      reconcile: vi.fn(() => Promise.resolve()),
      drain: vi.fn(() => Promise.reject(failure)),
    };

    await expect(reconcileAndDrain(
      target,
      identity,
      cancelled,
      new Map(),
      statuses,
    )).resolves.toBe(false);
    expect(statuses).toHaveBeenCalledOnce();
    expect(statuses).toHaveBeenCalledWith({
      kind: 'blocked',
      message: `metadata delivery infrastructure failed: ${failure.message}`,
    });
  });

  it('reports a real Web Lock rejection once and resolves false', async () => {
    const outbox = new MetadataOutbox('alice', {
      storage,
      locks: {
        request: () => Promise.reject(new Error('lock manager failed')),
      } as unknown as LockManager,
      generation: generations(),
    });
    const statuses = vi.fn();

    await expect(drainAndReport(outbox, identity, statuses)).resolves.toBe(false);
    expect(statuses).toHaveBeenCalledOnce();
    expect(statuses).toHaveBeenCalledWith({
      kind: 'blocked',
      message: 'metadata delivery infrastructure failed: lock manager failed',
    });
  });

  it('reports a real storage failure once and leaves the record durable', async () => {
    const outbox = new MetadataOutbox('alice', {
      storage,
      locks: locks as unknown as LockManager,
      generation: generations(),
    });
    await outbox.enqueueImportLinks(importRequest);
    storage.failSet = true;
    const statuses = vi.fn();

    await expect(drainAndReport(outbox, identity, statuses)).resolves.toBe(false);
    storage.failSet = false;
    expect(outbox.records()).toHaveLength(1);
    expect(statuses).toHaveBeenCalledOnce();
    expect(statuses).toHaveBeenCalledWith({
      kind: 'blocked',
      message: 'metadata delivery infrastructure failed: quota exceeded',
    });
  });

  it('persists awaiting intent, promotes only a persisted cancelled task, and survives reload', async () => {
    const sendTombstone = vi.fn(() => Promise.resolve());
    const first = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendTombstone,
    });
    await first.enqueueTombstone('client-1', 'superseded');
    expect(first.records()).toMatchObject([{
      state: 'awaiting_canonical', clientTaskId: 'client-1', reason: 'superseded',
    }]);

    await first.reconcile(cancelled, new Map([['client-1', 'canonical-1']]));
    expect(first.records()).toMatchObject([{
      state: 'queued', canonicalTaskId: 'canonical-1',
    }]);

    const recreated = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendTombstone,
    });
    await recreated.drain(identity);
    expect(sendTombstone).toHaveBeenCalledWith(identity, 'canonical-1', 'superseded');
    expect(recreated.records()).toEqual([]);
    expect(JSON.stringify([...storage.values])).not.toContain('secret');
  });

  it('removes awaiting or deliverable intent when the task was restored or purged', async () => {
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });
    await outbox.enqueueTombstone('client-1', 'obsolete');
    await outbox.reconcile(restored, new Map([['client-1', 'canonical-1']]));
    expect(outbox.records()).toEqual([]);

    await outbox.enqueueTombstone('client-1', 'obsolete again');
    await outbox.reconcile(cancelled, new Map([['client-1', 'canonical-1']]));
    await outbox.removeTombstone('client-1');
    expect(outbox.records()).toEqual([]);
  });

  it('uses external-key identity for provenance replacement and never stores auth', async () => {
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });
    await outbox.enqueueImportLinks(importRequest);
    await outbox.enqueueImportLinks({
      ...importRequest,
      source: 'renamed-source',
      items: [{ ...importRequest.items[0]!, title: 'updated title' }],
    });
    expect(outbox.records()).toHaveLength(1);
    expect(outbox.records()[0]).toMatchObject({
      source: 'renamed-source', item: { title: 'updated title' },
    });
    expect(JSON.stringify([...storage.values])).not.toContain('secret');
  });

  it('serializes only validated import fields and never invokes caller serialization hooks', async () => {
    const toJSON = vi.fn(() => ({ poisoned: true }));
    const item = {
      ...importRequest.items[0],
      ignored: 'attacker-controlled extra field',
      toJSON,
    };
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });

    await outbox.enqueueImportLinks({ source: importRequest.source, items: [item] });

    expect(toJSON).not.toHaveBeenCalled();
    expect(outbox.records()).toEqual([{
      version: 1,
      generation: 'generation-1',
      kind: 'import',
      state: 'queued',
      source: importRequest.source,
      item: importRequest.items[0],
    }]);
    expect(JSON.stringify([...storage.values])).not.toContain('ignored');
    expect(JSON.stringify([...storage.values])).not.toContain('poisoned');
  });

  it.each([
    ['non-object request', null],
    ['blank source', { ...importRequest, source: '   ' }],
    ['non-array items', { ...importRequest, items: {} }],
    ['too many items', { ...importRequest, items: Array(101).fill(importRequest.items[0]) }],
    ['non-object item', { ...importRequest, items: [null] }],
    ['blank field', {
      ...importRequest,
      items: [{ ...importRequest.items[0], link: ' ' }],
    }],
    ['line break', {
      ...importRequest,
      items: [{ ...importRequest.items[0], title: 'forged\nmetadata' }],
    }],
    ['carriage return', {
      ...importRequest,
      items: [{ ...importRequest.items[0], link: 'forged\rmetadata' }],
    }],
    ['oversized external key', {
      ...importRequest,
      items: [{ ...importRequest.items[0], external_key: 'é'.repeat(1025) }],
    }],
    ['oversized URL', {
      ...importRequest,
      items: [{ ...importRequest.items[0], url: 'é'.repeat(1025) }],
    }],
    ['oversized title', {
      ...importRequest,
      items: [{ ...importRequest.items[0], title: 'é'.repeat(251) }],
    }],
    ['non-string field', {
      ...importRequest,
      items: [{ ...importRequest.items[0], url: 7 }],
    }],
  ])('rejects invalid import metadata atomically: %s', async (_label, request) => {
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });

    await expect(outbox.enqueueImportLinks(
      request as unknown as Parameters<MetadataOutbox['enqueueImportLinks']>[0],
    )).rejects.toThrow('invalid outbox');
    expect(storage.values.size).toBe(0);
  });

  it('sanitizes server-controlled error metadata before persisting it', async () => {
    const malicious = `invalid\r\n${'x'.repeat(300)}`;
    const statuses = vi.fn();
    const outbox = new MetadataOutbox('alice', {
      storage,
      locks: locks as unknown as LockManager,
      generation: generations(),
      sendImport: () => Promise.reject(Object.assign(new Error(malicious), { status: 400 })),
      onStatus: statuses,
    });

    await outbox.enqueueImportLinks(importRequest);
    await outbox.drain(identity);

    expect(outbox.records()[0]).toMatchObject({
      state: 'blocked',
      error: expect.not.stringContaining('\n'),
    });
    expect(outbox.records()[0]?.error).toHaveLength(200);
    expect(statuses).toHaveBeenCalledWith({ kind: 'blocked', message: malicious });
  });

  it('omits empty or non-string error metadata from durable records', async () => {
    const emptyError = new MetadataOutbox('alice', {
      storage,
      locks: locks as unknown as LockManager,
      generation: generations(),
      sendImport: () => Promise.reject(Object.assign(new Error(''), { status: 400 })),
    });
    await emptyError.enqueueImportLinks(importRequest);
    await emptyError.drain(identity);
    expect(emptyError.records()[0]).not.toHaveProperty('error');

    const key = [...storage.values.keys()][0]!;
    storage.setItem(key, JSON.stringify({
      ...JSON.parse(storage.getItem(key)!),
      state: 'sending',
      error: 7,
    }));
    await emptyError.reconcile(cancelled, new Map());
    expect(emptyError.records()[0]).toMatchObject({ state: 'retry' });
    expect(emptyError.records()[0]).not.toHaveProperty('error');
  });

  it.each([
    ['blank task id', ' ', 'valid reason'],
    ['line break in reason', 'client-1', 'forged\nmetadata'],
    ['oversized reason', 'client-1', 'é'.repeat(1001)],
  ])('rejects invalid tombstone metadata before storage: %s', async (_label, taskId, reason) => {
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });

    await expect(outbox.enqueueTombstone(taskId, reason)).rejects.toThrow('invalid outbox');
    expect(storage.values.size).toBe(0);
  });

  it.each([
    ['2xx', undefined],
    ['network', new TypeError('offline')],
    ['500', Object.assign(new Error('server'), { status: 500 })],
    ['400', Object.assign(new Error('bad'), { status: 400 })],
    ['401', new ReauthRequiredError()],
  ])('makes stale %s completion byte-identical with no callback', async (_label, failure) => {
    const pending = deferred<void>();
    const statuses = vi.fn();
    const sendImport = vi.fn(async () => {
      await pending.promise;
      if (failure) throw failure;
    });
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendImport, onStatus: statuses,
    });
    await outbox.enqueueImportLinks(importRequest);
    const draining = outbox.drain(identity);
    await vi.waitFor(() => expect(sendImport).toHaveBeenCalledTimes(1));
    const key = [...storage.values.keys()][0]!;
    const newer = JSON.stringify({
      ...JSON.parse(storage.getItem(key)!),
      generation: 'replacement-generation',
      state: 'queued',
      item: { ...importRequest.items[0], title: 'newer' },
    });
    storage.setItem(key, newer);
    statuses.mockClear();
    pending.resolve();
    await draining;
    expect(storage.getItem(key)).toBe(newer);
    expect(statuses).not.toHaveBeenCalled();
  });

  it('serializes two instances through one identity lock', async () => {
    const pending = deferred<void>();
    const sendImport = vi.fn(() => pending.promise);
    const options = {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendImport,
    };
    const first = new MetadataOutbox('alice', options);
    const second = new MetadataOutbox('alice', options);
    await first.enqueueImportLinks(importRequest);
    const firstDrain = first.drain(identity);
    await vi.waitFor(() => expect(sendImport).toHaveBeenCalledTimes(1));
    const secondDrain = second.drain(identity);
    await Promise.resolve();
    expect(sendImport).toHaveBeenCalledTimes(1);
    pending.resolve();
    await Promise.all([firstDrain, secondDrain]);
    expect(sendImport).toHaveBeenCalledTimes(1);
  });

  it('preserves cross-user startup isolation', async () => {
    const sendAlice = vi.fn(() => Promise.resolve());
    const sendBob = vi.fn(() => Promise.resolve());
    const alice = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendImport: sendAlice,
    });
    const bob = new MetadataOutbox('bob', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendImport: sendBob,
    });
    await alice.enqueueImportLinks(importRequest);
    await bob.enqueueImportLinks(importRequest);
    await bob.drain({ kind: 'manual', id: 'bob', serverToken: 'bob-secret' });
    expect(sendAlice).not.toHaveBeenCalled();
    expect(sendBob).toHaveBeenCalledTimes(1);
    expect(alice.records()).toHaveLength(1);
    expect(bob.records()).toEqual([]);
    expect(JSON.stringify([...storage.values])).not.toContain('bob-secret');
  });

  it('keeps alice and alice.work send, remove, and list operations isolated', async () => {
    const sendAlice = vi.fn(() => Promise.resolve());
    const sendWork = vi.fn(() => Promise.resolve());
    const alice = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendTombstone: sendAlice,
    });
    const work = new MetadataOutbox('alice.work', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendTombstone: sendWork,
    });
    await alice.enqueueTombstone('client-1', 'alice reason');
    await work.enqueueTombstone('client-1', 'work reason');
    await alice.reconcile(cancelled, new Map([['client-1', 'canonical-alice']]));
    await work.reconcile(cancelled, new Map([['client-1', 'canonical-work']]));

    expect(alice.records()).toHaveLength(1);
    expect(work.records()).toHaveLength(1);
    await alice.removeTombstone('client-1');
    expect(alice.records()).toEqual([]);
    expect(work.records()).toHaveLength(1);
    await work.drain({ kind: 'manual', id: 'alice.work', serverToken: 'work' });
    expect(sendAlice).not.toHaveBeenCalled();
    expect(sendWork).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'alice.work' }),
      'canonical-work',
      'work reason',
    );
  });

  it('migrates only exact payload-proven legacy records', () => {
    const awaiting = (clientTaskId: string) => JSON.stringify({
      version: 1,
      generation: clientTaskId,
      kind: 'tombstone',
      state: 'awaiting_canonical',
      clientTaskId,
      reason: 'obsolete',
    });
    storage.values.set(
      `kb.outbox.v1.alice.${encodeURIComponent('tombstone:alice-task')}`,
      awaiting('alice-task'),
    );
    storage.values.set(
      `kb.outbox.v1.alice.work.${encodeURIComponent('tombstone:work-task')}`,
      awaiting('work-task'),
    );
    storage.values.set(
      `kb.outbox.v1.alice.${encodeURIComponent('tombstone:forged-task')}`,
      awaiting('different-task'),
    );

    const alice = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });

    expect(alice.records()).toMatchObject([{ clientTaskId: 'alice-task' }]);
    expect([...storage.values.keys()]).toContain(
      `kb.outbox.v1.alice.work.${encodeURIComponent('tombstone:work-task')}`,
    );
  });

  it('canonicalizes legacy records instead of copying attacker-controlled fields', () => {
    const logicalKey = `import:${importRequest.items[0]!.external_key}`;
    const legacyKey = `kb.outbox.v1.alice.${encodeURIComponent(logicalKey)}`;
    storage.values.set(legacyKey, JSON.stringify({
      version: 1,
      generation: 'legacy-generation',
      kind: 'import',
      state: 'queued',
      source: importRequest.source,
      item: {
        ...importRequest.items[0],
        ignored: 'attacker-controlled extra field',
        toJSON: { poisoned: true },
      },
      ignored: 'attacker-controlled record field',
    }));

    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });

    expect(outbox.records()).toEqual([{
      version: 1,
      generation: 'legacy-generation',
      kind: 'import',
      state: 'queued',
      source: importRequest.source,
      item: importRequest.items[0],
    }]);
    const migrated = [...storage.values.entries()].find(([key]) => key !== legacyKey);
    expect(migrated).toBeDefined();
    expect(migrated![1]).not.toContain('ignored');
    expect(migrated![1]).not.toContain('toJSON');
    expect(migrated![1]).not.toContain('poisoned');
  });

  it.each([
    ['line break', { ...importRequest.items[0], title: 'forged\nmetadata' }],
    ['oversized URL', { ...importRequest.items[0], url: 'é'.repeat(1025) }],
  ])('retains but does not migrate invalid legacy metadata: %s', (_label, item) => {
    const logicalKey = `import:${item.external_key}`;
    const legacyKey = `kb.outbox.v1.alice.${encodeURIComponent(logicalKey)}`;
    const raw = JSON.stringify({
      version: 1,
      generation: 'legacy-generation',
      kind: 'import',
      state: 'queued',
      source: importRequest.source,
      item,
    });
    storage.values.set(legacyKey, raw);

    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });

    expect(outbox.records()).toEqual([]);
    expect(storage.values).toEqual(new Map([[legacyKey, raw]]));
  });

  it('retains a valid legacy record when canonical migration storage fails', () => {
    const logicalKey = `import:${importRequest.items[0]!.external_key}`;
    const legacyKey = `kb.outbox.v1.alice.${encodeURIComponent(logicalKey)}`;
    const raw = JSON.stringify({
      version: 1,
      generation: 'legacy-generation',
      kind: 'import',
      state: 'queued',
      source: importRequest.source,
      item: importRequest.items[0],
    });
    storage.values.set(legacyKey, raw);
    storage.failSet = true;

    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });

    expect(outbox.records()).toEqual([]);
    expect(storage.values).toEqual(new Map([[legacyKey, raw]]));
  });

  it('does not lose interleaved two-instance additions and removals', async () => {
    const first = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });
    const second = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });
    await Promise.all([
      first.enqueueTombstone('client-1', 'old'),
      second.enqueueImportLinks(importRequest),
    ]);
    await Promise.all([
      first.removeTombstone('client-1'),
      second.enqueueTombstone('client-2', 'new'),
    ]);
    expect(first.records()).toMatchObject([
      { kind: 'import', item: { external_key: importRequest.items[0]!.external_key } },
      { kind: 'tombstone', clientTaskId: 'client-2', reason: 'new' },
    ]);
  });

  it.each(['restore', 'purge'])(
    'keeps the cancelled board unchanged when %s intent removal fails',
    async () => {
      const outbox = new MetadataOutbox('alice', {
        storage, locks: locks as unknown as LockManager, generation: generations(),
      });
      await outbox.enqueueTombstone('client-1', 'keep me');
      storage.failRemove = true;
      const mutate = vi.fn();
      const failure = vi.fn();
      await expect(removeIntentBeforeMutation(
        () => outbox.removeTombstone('client-1'),
        mutate,
        failure,
      )).resolves.toBe(false);
      expect(mutate).not.toHaveBeenCalled();
      expect(failure).toHaveBeenCalledWith(expect.objectContaining({ name: 'SecurityError' }));
      expect(outbox.records()).toHaveLength(1);
    },
  );

  it('keeps an active task unchanged when durable cancellation enqueue fails', async () => {
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager, generation: generations(),
    });
    storage.failSet = true;
    const mutate = vi.fn();
    const failure = vi.fn();
    await expect(enqueueIntentBeforeMutation(
      () => outbox.enqueueTombstone('client-1', 'must persist first'),
      mutate,
      failure,
    )).resolves.toBe(false);
    expect(mutate).not.toHaveBeenCalled();
    expect(failure).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'QuotaExceededError' }),
    );
    expect(outbox.records()).toEqual([]);
  });

  it.each(['restore', 'purge'])(
    'fails closed for %s when Web Locks are unavailable',
    async () => {
      const statuses = vi.fn();
      const outbox = new MetadataOutbox('alice', {
        storage, locks: null, generation: generations(), onStatus: statuses,
      });
      await outbox.enqueueTombstone('client-1', 'retain without lock');
      const mutate = vi.fn();
      const failure = vi.fn();
      await expect(removeIntentBeforeMutation(
        () => outbox.removeTombstone('client-1'),
        mutate,
        failure,
      )).resolves.toBe(false);
      expect(mutate).not.toHaveBeenCalled();
      expect(failure).toHaveBeenCalledWith(
        expect.objectContaining({
          message: expect.stringContaining('storage lock is required'),
        }),
      );
      expect(outbox.records()).toHaveLength(1);
      expect(statuses).toHaveBeenCalledWith(expect.objectContaining({ kind: 'blocked' }));
    },
  );

  it('retains work and surfaces a block when Web Locks are unavailable', async () => {
    const statuses = vi.fn();
    const outbox = new MetadataOutbox('alice', {
      storage, locks: null, generation: generations(), onStatus: statuses,
    });
    await outbox.enqueueImportLinks(importRequest);
    await outbox.drain(identity);
    expect(outbox.records()).toHaveLength(1);
    expect(statuses).toHaveBeenCalledWith(expect.objectContaining({ kind: 'blocked' }));
  });

  it('makes completion from a cancelled identity session a total no-op', async () => {
    const pending = deferred<void>();
    const statuses = vi.fn();
    const outbox = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), sendImport: () => pending.promise, onStatus: statuses,
    });
    await outbox.enqueueImportLinks(importRequest);
    const draining = outbox.drain(identity);
    await vi.waitFor(() => expect(outbox.records()[0]?.state).toBe('sending'));
    const before = JSON.stringify([...storage.values]);
    outbox.cancel();
    statuses.mockClear();
    pending.resolve();
    await draining;
    expect(JSON.stringify([...storage.values])).toBe(before);
    expect(statuses).not.toHaveBeenCalled();
  });

  it('surfaces reconstructed blocked state immediately', async () => {
    const first = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(),
      sendImport: () => Promise.reject(Object.assign(new Error('invalid'), { status: 400 })),
    });
    await first.enqueueImportLinks(importRequest);
    await first.drain(identity);
    const statuses = vi.fn();
    const recreated = new MetadataOutbox('alice', {
      storage, locks: locks as unknown as LockManager,
      generation: generations(), onStatus: statuses,
    });
    recreated.surfaceStoredStatus();
    expect(statuses).toHaveBeenCalledWith({ kind: 'blocked', message: 'invalid' });
  });
});
