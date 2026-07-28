import type { Board } from './model';
import { parse, serialize } from './markdown';
import type { Identity } from './auth';
import { authedFetch } from './api';

const HEALTH_TIMEOUT_MS = 1500;
const SAVE_DEBOUNCE_MS = 800;

type PendingSave = {
  identity: Identity;
  board: Board;
  onError?: (err: unknown) => void;
  onSuccess?: () => void;
};

export class RemoteStore {
  private saveTimer: ReturnType<typeof setTimeout> | null = null;
  private pending: PendingSave | null = null;

  /** True when a kb server answers GET /api/health within ~1.5s. */
  async detect(): Promise<boolean> {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), HEALTH_TIMEOUT_MS);
    try {
      const res = await fetch('/api/health', { signal: ctrl.signal });
      if (!res.ok) return false;
      const body = (await res.json()) as { ok?: unknown };
      return body.ok === true;
    } catch {
      return false;
    } finally {
      clearTimeout(timer);
    }
  }

  /** null when the server has no board yet (404); throws on other failures. */
  async loadRemote(identity: Identity): Promise<Board | null> {
    const res = await authedFetch(identity, '/api/board');
    if (res.status === 404) return null;
    if (!res.ok) throw new Error(`GET /api/board failed: ${res.status}`);
    return parse(await res.text());
  }

  /** Fire-and-forget PUT of the serialized board. */
  saveRemote(
    identity: Identity,
    board: Board,
    onError?: (err: unknown) => void,
    onSuccess?: () => void,
    opts?: { keepalive?: boolean },
  ): void {
    void (async () => {
      const res = await authedFetch(identity, '/api/board', {
        method: 'PUT',
        headers: { 'Content-Type': 'text/markdown' },
        body: serialize(board),
        keepalive: opts?.keepalive ?? false,
      });
      if (!res.ok) throw new Error(`PUT /api/board failed: ${res.status}`);
      onSuccess?.();
    })().catch((err: unknown) => onError?.(err));
  }

  /** Debounced (800ms) saveRemote — later calls replace pending ones. */
  saveRemoteDebounced(
    identity: Identity,
    board: Board,
    onError?: (err: unknown) => void,
    onSuccess?: () => void,
  ): void {
    if (this.saveTimer !== null) clearTimeout(this.saveTimer);
    this.pending = { identity, board, onError, onSuccess };
    this.saveTimer = setTimeout(() => {
      const p = this.pending;
      this.saveTimer = null;
      this.pending = null;
      if (p) this.saveRemote(p.identity, p.board, p.onError, p.onSuccess);
    }, SAVE_DEBOUNCE_MS);
  }

  /**
   * Drop any pending debounced save without executing it — call on
   * unmount/sign-out so no PUT fires with a stale identity afterwards.
   */
  cancel(): void {
    if (this.saveTimer !== null) clearTimeout(this.saveTimer);
    this.saveTimer = null;
    this.pending = null;
  }

  /**
   * Execute any pending debounced save immediately. keepalive lets the PUT
   * outlive pagehide/tab close (bodies here are small markdown, well under
   * the keepalive body cap).
   */
  flush(): void {
    const p = this.pending;
    this.cancel();
    if (p) {
      this.saveRemote(p.identity, p.board, p.onError, p.onSuccess, {
        keepalive: true,
      });
    }
  }
}
