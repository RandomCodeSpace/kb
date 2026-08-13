import type { Identity } from './auth';
import { ReauthRequiredError } from './auth';
import type { Board } from './model';
import { newId } from './model';
import type { ImportLinkItem, RecordImportLinksRequest } from './api';
import { recordImportLinks, recordTombstone } from './api';
import {
  legacyNamespaceStorageKey,
  namespaceStorageKey,
  namespaceStorageSuffix,
} from './store';

const PREFIX = 'kb.outbox.v1';
const LOCK_PREFIX = 'kb:outbox:';
const MAX_IMPORT_ITEMS = 100;
const MAX_EXTERNAL_KEY_BYTES = 2048;
const MAX_URL_BYTES = 2048;
const MAX_TITLE_BYTES = 500;
const MAX_REASON_BYTES = 2000;

type OutboxState = 'awaiting_canonical' | 'queued' | 'sending' | 'retry' | 'blocked';

interface BaseRecord {
  version: 1;
  generation: string;
  state: OutboxState;
  error?: string;
}

export interface AwaitingTombstoneRecord extends BaseRecord {
  kind: 'tombstone';
  state: 'awaiting_canonical';
  clientTaskId: string;
  reason: string;
}

export interface TombstoneRecord extends BaseRecord {
  kind: 'tombstone';
  state: Exclude<OutboxState, 'awaiting_canonical'>;
  clientTaskId: string;
  canonicalTaskId: string;
  reason: string;
}

export interface ImportRecord extends BaseRecord {
  kind: 'import';
  state: Exclude<OutboxState, 'awaiting_canonical'>;
  source: string;
  item: ImportLinkItem;
}

export type OutboxRecord = AwaitingTombstoneRecord | TombstoneRecord | ImportRecord;
type NewOutboxRecord =
  | Omit<AwaitingTombstoneRecord, 'version' | 'generation'>
  | Omit<TombstoneRecord, 'version' | 'generation'>
  | Omit<ImportRecord, 'version' | 'generation'>;

type DeliverableRecord = TombstoneRecord | ImportRecord;
type SelectedRecord = { key: string; record: DeliverableRecord };
type DeliveryOutcome = 'success' | 'retry' | 'reauth' | 'blocked';

export type OutboxStatus =
  | { kind: 'idle' }
  | { kind: 'blocked'; message: string }
  | { kind: 'retry'; message: string }
  | { kind: 'reauth'; message: string };

type ReconcileAndDrainTarget = Pick<MetadataOutbox, 'reconcile' | 'drain'>;

/** Classify only failures that escaped per-record delivery handling. */
export async function drainAndReport(
  outbox: Pick<MetadataOutbox, 'drain'>,
  identity: Identity,
  onStatus: (status: OutboxStatus) => void,
): Promise<boolean> {
  try {
    await outbox.drain(identity);
    return true;
  } catch (error) {
    onStatus({
      kind: 'blocked',
      message: `metadata delivery infrastructure failed: ${errorText(error)}`,
    });
    return false;
  }
}

/** Reconcile durable metadata before delivery; a failed journal update blocks delivery. */
export async function reconcileAndDrain(
  outbox: ReconcileAndDrainTarget,
  identity: Identity,
  board: Board,
  canonicalTaskIDs: ReadonlyMap<string, string>,
  onStatus: (status: OutboxStatus) => void,
): Promise<boolean> {
  try {
    await outbox.reconcile(board, canonicalTaskIDs);
  } catch (error) {
    onStatus({
      kind: 'blocked',
      message: `metadata storage could not be reconciled: ${errorText(error)}`,
    });
    return false;
  }
  return drainAndReport(outbox, identity, onStatus);
}

/** Mutate the board only after its stale metadata intent is durably removed. */
export async function removeIntentBeforeMutation(
  remove: () => Promise<void>,
  mutate: () => void,
  onFailure: (error: unknown) => void,
): Promise<boolean> {
  try {
    await remove();
    mutate();
    return true;
  } catch (error) {
    onFailure(error);
    return false;
  }
}

/** Mutate the board only after its metadata intent is durably enqueued. */
export async function enqueueIntentBeforeMutation(
  enqueue: () => Promise<unknown>,
  mutate: () => void,
  onFailure: (error: unknown) => void,
): Promise<boolean> {
  try {
    await enqueue();
    mutate();
    return true;
  } catch (error) {
    onFailure(error);
    return false;
  }
}

interface OutboxOptions {
  storage?: Storage;
  locks?: LockManager | null;
  generation?: () => string;
  sendTombstone?: typeof recordTombstone;
  sendImport?: typeof recordImportLinks;
  onStatus?: (status: OutboxStatus) => void;
}

function recordKey(ns: string, logicalKey: string): string {
  return namespaceStorageKey(PREFIX, ns, encodeURIComponent(logicalKey));
}

function tombstoneLogicalKey(clientTaskId: string): string {
  return `tombstone:${clientTaskId}`;
}

function importLogicalKey(externalKey: string): string {
  return `import:${externalKey}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function validatedText(
  value: unknown,
  field: string,
  maxBytes?: number,
): string {
  if (
    typeof value !== 'string' ||
    value.trim() === '' ||
    value.includes('\r') ||
    value.includes('\n') ||
    (maxBytes !== undefined && new TextEncoder().encode(value).byteLength > maxBytes)
  ) {
    throw new TypeError(`invalid outbox ${field}`);
  }
  return value;
}

function validatedImportRequest(req: RecordImportLinksRequest): RecordImportLinksRequest {
  if (!isRecord(req)) throw new TypeError('invalid outbox import request');
  const source = validatedText(req.source, 'import source');
  if (!Array.isArray(req.items) || req.items.length > MAX_IMPORT_ITEMS) {
    throw new TypeError('invalid outbox import items');
  }
  const items = req.items.map((item) => {
    if (!isRecord(item)) throw new TypeError('invalid outbox import item');
    return {
      external_key: validatedText(
        item.external_key,
        'import external key',
        MAX_EXTERNAL_KEY_BYTES,
      ),
      link: validatedText(item.link, 'import link'),
      url: validatedText(item.url, 'import URL', MAX_URL_BYTES),
      title: validatedText(item.title, 'import title', MAX_TITLE_BYTES),
    };
  });
  return { source, items };
}

function storedError(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined;
  const safe = value.replace(/[\r\n]+/g, ' ').slice(0, 200);
  return safe === '' ? undefined : safe;
}

function validatedStorageRecord(record: OutboxRecord): OutboxRecord {
  const generation = validatedText(record.generation, 'generation');
  const error = storedError(record.error);
  if (record.kind === 'import') {
    const validated = validatedImportRequest({ source: record.source, items: [record.item] });
    return {
      version: 1,
      generation,
      kind: 'import',
      state: record.state,
      source: validated.source,
      item: validated.items[0]!,
      ...(error === undefined ? {} : { error }),
    };
  }
  const clientTaskId = validatedText(record.clientTaskId, 'tombstone task ID');
  const reason = validatedText(record.reason, 'tombstone reason', MAX_REASON_BYTES);
  if (record.state === 'awaiting_canonical') {
    return {
      version: 1,
      generation,
      kind: 'tombstone',
      state: 'awaiting_canonical',
      clientTaskId,
      reason,
      ...(error === undefined ? {} : { error }),
    };
  }
  return {
    version: 1,
    generation,
    kind: 'tombstone',
    state: record.state,
    clientTaskId,
    canonicalTaskId: validatedText(record.canonicalTaskId, 'canonical task ID'),
    reason,
    ...(error === undefined ? {} : { error }),
  };
}

function parseRecord(raw: string | null): OutboxRecord | null {
  if (raw === null) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value) || value.version !== 1 || typeof value.generation !== 'string') {
      return null;
    }
    if (value.kind === 'tombstone') {
      if (
        typeof value.clientTaskId !== 'string' ||
        typeof value.reason !== 'string' ||
        (value.state !== 'awaiting_canonical' &&
          value.state !== 'queued' &&
          value.state !== 'sending' &&
          value.state !== 'retry' &&
          value.state !== 'blocked')
      ) return null;
      if (value.state === 'awaiting_canonical') return value as unknown as AwaitingTombstoneRecord;
      if (typeof value.canonicalTaskId !== 'string') return null;
      return value as unknown as TombstoneRecord;
    }
    if (
      value.kind !== 'import' ||
      (value.state !== 'queued' && value.state !== 'sending' &&
        value.state !== 'retry' && value.state !== 'blocked') ||
      typeof value.source !== 'string' ||
      !isRecord(value.item) ||
      typeof value.item.external_key !== 'string' ||
      typeof value.item.link !== 'string' ||
      typeof value.item.url !== 'string' ||
      typeof value.item.title !== 'string'
    ) return null;
    return value as unknown as ImportRecord;
  } catch {
    return null;
  }
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : 'metadata delivery failed';
}

function logicalKey(record: OutboxRecord): string {
  return record.kind === 'tombstone'
    ? tombstoneLogicalKey(record.clientTaskId)
    : importLogicalKey(record.item.external_key);
}

function compareCodeUnits(left: string, right: string): number {
  return Number(left > right) - Number(left < right);
}

function deliveryFailureOutcome(error: unknown): Exclude<DeliveryOutcome, 'success'> {
  if (error instanceof ReauthRequiredError) return 'reauth';
  const status = isRecord(error) && typeof error.status === 'number' ? error.status : 0;
  return status === 0 || status >= 500 ? 'retry' : 'blocked';
}

/** Durable, credential-free metadata journal for one immutable user namespace. */
export class MetadataOutbox {
  private readonly storage: Storage;
  private readonly locks?: LockManager;
  private readonly nextGeneration: () => string;
  private readonly sendTombstone: typeof recordTombstone;
  private readonly sendImport: typeof recordImportLinks;
  private readonly onStatus: (status: OutboxStatus) => void;
  private draining = false;
  private session = 0;

  constructor(private readonly ns: string, options: OutboxOptions = {}) {
    this.storage = options.storage ?? localStorage;
    if (options.locks === undefined) {
      this.locks = typeof navigator === 'undefined' ? undefined : navigator.locks;
    } else {
      this.locks = options.locks ?? undefined;
    }
    this.nextGeneration = options.generation ?? newId;
    this.sendTombstone = options.sendTombstone ?? recordTombstone;
    this.sendImport = options.sendImport ?? recordImportLinks;
    this.onStatus = options.onStatus ?? (() => {});
    this.migrateLegacyRecords();
  }

  /**
   * Copy only records whose payload proves the exact old key. Prefix matching
   * alone cannot distinguish namespace `alice` from `alice.work`.
   */
  private migrateLegacyRecords(): void {
    try {
      for (const key of this.legacyCandidateKeys()) this.migrateLegacyRecord(key);
    } catch {
      // Storage failures are surfaced by the normal record operations.
    }
  }

  private legacyCandidateKeys(): string[] {
    const candidates: string[] = [];
    for (let i = 0; i < this.storage.length; i += 1) {
      const key = this.storage.key(i);
      if (key?.startsWith(`${PREFIX}.`)) candidates.push(key);
    }
    return candidates;
  }

  private migrateLegacyRecord(key: string): void {
    const record = parseRecord(this.storage.getItem(key));
    if (!record) return;
    const encodedLogical = encodeURIComponent(logicalKey(record));
    const expected = legacyNamespaceStorageKey(PREFIX, this.ns, encodedLogical);
    if (key !== expected) return;
    const target = recordKey(this.ns, logicalKey(record));
    if (this.storage.getItem(target) !== null) return;
    try {
      this.write(target, record);
    } catch (error) {
      if (!(error instanceof TypeError)) throw error;
    }
  }

  /** Surface a durable blocked record after the owning UI has mounted. */
  surfaceStoredStatus(): void {
    try {
      const blocked = this.records().find((record) => record.state === 'blocked');
      if (blocked) {
        this.onStatus({
          kind: 'blocked',
          message: blocked.error ?? 'metadata delivery needs attention',
        });
      }
    } catch {
      this.onStatus({
        kind: 'blocked',
        message: 'metadata storage could not be read',
      });
    }
  }

  private lockName(): string {
    return namespaceStorageKey(LOCK_PREFIX, this.ns);
  }

  private keys(): string[] {
    const keys: string[] = [];
    for (let i = 0; i < this.storage.length; i += 1) {
      const key = this.storage.key(i);
      if (key && namespaceStorageSuffix(PREFIX, this.ns, key) !== null) keys.push(key);
    }
    return keys.sort(compareCodeUnits);
  }

  private async locked<T>(fn: () => T | Promise<T>): Promise<T | undefined> {
    if (!this.locks?.request) {
      this.onStatus({
        kind: 'blocked',
        message: 'Metadata is queued locally but this browser cannot safely synchronize it across tabs.',
      });
      return undefined;
    }
    return this.locks.request(this.lockName(), fn);
  }

  private write(key: string, record: OutboxRecord): void {
    this.storage.setItem(key, JSON.stringify(validatedStorageRecord(record)));
  }

  private fresh(record: NewOutboxRecord): OutboxRecord {
    return {
      ...record,
      version: 1,
      generation: this.nextGeneration(),
    } as OutboxRecord;
  }

  /** Persist the user's reason before the board PUT can acknowledge an ID. */
  async enqueueTombstone(clientTaskId: string, reason: string): Promise<boolean> {
    const safeClientTaskId = validatedText(clientTaskId, 'tombstone task ID');
    const safeReason = validatedText(reason, 'tombstone reason', MAX_REASON_BYTES);
    const key = recordKey(this.ns, tombstoneLogicalKey(safeClientTaskId));
    const record = this.fresh({
      kind: 'tombstone',
      state: 'awaiting_canonical',
      clientTaskId: safeClientTaskId,
      reason: safeReason,
    });
    const written = await this.locked(() => this.write(key, record));
    if (written === undefined && !this.locks?.request) {
      // Retaining the intent is safer than dropping it. The warning above
      // explains that automatic delivery is blocked until exclusivity exists.
      this.write(key, record);
    }
    return true;
  }

  async enqueueImportLinks(req: RecordImportLinksRequest): Promise<boolean> {
    const validated = validatedImportRequest(req);
    const writeAll = () => {
      for (const item of validated.items) {
        const key = recordKey(this.ns, importLogicalKey(item.external_key));
        this.write(key, this.fresh({
          kind: 'import', state: 'queued', source: validated.source, item,
        }));
      }
    };
    const written = await this.locked(writeAll);
    if (written === undefined && !this.locks?.request) writeAll();
    return true;
  }

  /** Resolve crash-safe awaiting intents after canonical IDs are persisted. */
  async reconcile(
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
  ): Promise<void> {
    await this.locked(() => {
      for (const key of this.keys()) {
        const record = parseRecord(this.storage.getItem(key));
        if (!record) continue;
        this.reconcileRecord(key, record, board, canonicalTaskIDs);
      }
    });
  }

  private reconcileRecord(
    key: string,
    record: OutboxRecord,
    board: Board,
    canonicalTaskIDs: ReadonlyMap<string, string>,
  ): void {
    if (record.kind === 'import') {
      this.retryInterruptedRecord(key, record);
      return;
    }
    const task = board.tasks.find((candidate) => candidate.id === record.clientTaskId);
    if (task?.status !== 'cancelled') {
      this.storage.removeItem(key);
      return;
    }
    if (this.retryInterruptedRecord(key, record)) return;
    if (record.state !== 'awaiting_canonical') return;
    const canonicalTaskId = canonicalTaskIDs.get(record.clientTaskId);
    if (!canonicalTaskId) return;
    this.write(key, this.fresh({
      kind: 'tombstone',
      state: 'queued',
      clientTaskId: record.clientTaskId,
      canonicalTaskId,
      reason: record.reason,
    }));
  }

  private retryInterruptedRecord(key: string, record: OutboxRecord): boolean {
    if (record.state !== 'sending') return false;
    this.write(key, this.fresh({ ...record, state: 'retry' }));
    return true;
  }

  /** Restore and purge both cancel awaiting and already-deliverable intent. */
  async removeTombstone(clientTaskId: string): Promise<void> {
    if (!this.locks?.request) {
      const error = new Error(
        'a browser storage lock is required before cancellation intent can be removed',
      );
      this.onStatus({ kind: 'blocked', message: error.message });
      throw error;
    }
    const key = recordKey(this.ns, tombstoneLogicalKey(clientTaskId));
    await this.locked(() => this.storage.removeItem(key));
  }

  private selectUnlocked(): SelectedRecord | null {
    for (const key of this.keys()) {
      const record = parseRecord(this.storage.getItem(key));
      if (
        !record ||
        record.state === 'awaiting_canonical' ||
        record.state === 'blocked' ||
        record.state === 'sending'
      ) continue;
      const sending = this.fresh({ ...record, state: 'sending' }) as
        | TombstoneRecord
        | ImportRecord;
      this.write(key, sending);
      return { key, record: sending };
    }
    return null;
  }

  private completeUnlocked(
    selected: SelectedRecord,
    outcome: DeliveryOutcome,
    error?: unknown,
  ): boolean {
    const current = parseRecord(this.storage.getItem(selected.key));
    if (
      !current ||
      current.state === 'awaiting_canonical' ||
      current.generation !== selected.record.generation
    ) return false;
    // All completion effects, including UI/auth effects, occur only after
    // the generation check. A stale response is therefore a total no-op.
    if (outcome === 'success') {
      this.storage.removeItem(selected.key);
      this.onStatus({ kind: 'idle' });
      return true;
    }
    const message = errorText(error);
    if (outcome === 'retry' || outcome === 'reauth') {
      this.write(selected.key, this.fresh({ ...current, state: 'retry', error: message }));
      this.onStatus({ kind: outcome, message });
    } else {
      this.write(selected.key, this.fresh({ ...current, state: 'blocked', error: message }));
      this.onStatus({ kind: 'blocked', message });
    }
    return true;
  }

  private dispatchDelivery(identity: Identity, record: DeliverableRecord): Promise<void> {
    return record.kind === 'tombstone'
      ? this.sendTombstone(identity, record.canonicalTaskId, record.reason)
      : this.sendImport(identity, { source: record.source, items: [record.item] });
  }

  private async drainUnlocked(identity: Identity, session: number): Promise<void> {
    for (;;) {
      if (session !== this.session) return;
      const selected = this.selectUnlocked();
      if (!selected) return;
      try {
        await this.dispatchDelivery(identity, selected.record);
        if (session !== this.session) return;
        if (!this.completeUnlocked(selected, 'success')) return;
      } catch (error) {
        if (session !== this.session) return;
        this.completeUnlocked(selected, deliveryFailureOutcome(error), error);
        return;
      }
    }
  }

  async drain(identity: Identity): Promise<void> {
    if (this.draining) return;
    if (!this.locks?.request) {
      this.onStatus({
        kind: 'blocked',
        message: 'Metadata is queued locally but this browser cannot safely synchronize it across tabs.',
      });
      return;
    }
    this.draining = true;
    const session = this.session;
    try {
      await this.locks.request(this.lockName(), () => this.drainUnlocked(identity, session));
    } finally {
      this.draining = false;
    }
  }

  /** Invalidate an old identity/session before sign-out or component teardown. */
  cancel(): void {
    this.session += 1;
  }

  /** Test/debug view. Credentials cannot appear because records have no slot for them. */
  records(): OutboxRecord[] {
    return this.keys()
      .map((key) => parseRecord(this.storage.getItem(key)))
      .filter((record): record is OutboxRecord => record !== null);
  }
}
