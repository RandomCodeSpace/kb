import { useId, useRef, useState } from 'react';
import type { SubmitEvent } from 'react';
import type { Identity } from '../lib/auth';
import { ReauthRequiredError, signInAzure } from '../lib/auth';
import { getLabels } from '../lib/api';
import { useDialogFocus } from '../lib/focus';

export interface ReconnectModalProps {
  /** The signed-in identity whose credential the server is rejecting. */
  identity: Identity;
  /** A credential the server accepted — the caller persists it and retries. */
  onIdentity: (identity: Identity) => void;
  /** Give up on this identity entirely and go back to the gate. */
  onSignOut: () => void;
  /** Dismiss the dialog; the session stays expired and nothing gets saved. */
  onClose: () => void;
}

/**
 * Why a reconnect attempt failed, in the terms the person can act on. A
 * rejected credential and an unreachable server need different responses, and
 * "failed" tells them which one this is.
 */
export function reconnectError(err: unknown): string {
  if (err instanceof ReauthRequiredError) {
    return 'the server did not accept that token';
  }
  return 'could not reach the server — it may be down';
}

/**
 * Recovery for a session the server no longer accepts.
 *
 * The server token lives in sessionStorage on purpose (it authorizes every
 * user's board, so it must not sit in localStorage), while the identity lives
 * in localStorage. A new browser session therefore starts signed in with no
 * token: the header says who you are, and every API call 401s. Before this
 * dialog the only sign of that was a dot in the header, and the only way out
 * was to sign out and back in — which nothing on screen said.
 *
 * The board is the server's; there is no local copy and no outbox behind it,
 * so an expired session means every edit is refused and undone. Dismissable
 * only so the board can be read while it lasts — the header keeps a Reconnect
 * button for as long as the session is expired.
 */
export function ReconnectModal({
  identity,
  onIdentity,
  onSignOut,
  onClose,
}: Readonly<ReconnectModalProps>) {
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const onDialogKeyDown = useDialogFocus(boxRef);
  const titleId = useId();
  const bodyId = useId();
  const errorId = useId();

  /**
   * Prove the credential before adopting it. Accepting it untested would put
   * the app straight back into the state this dialog exists to get out of,
   * with nothing to say the new token was the problem. /api/labels is the
   * cheapest authenticated GET.
   */
  const attempt = async (next: Identity) => {
    setBusy(true);
    setError(null);
    try {
      await getLabels(next);
      onIdentity(next);
    } catch (err) {
      setError(reconnectError(err));
      setBusy(false);
    }
  };

  const handleToken = (e: SubmitEvent<HTMLFormElement>) => {
    e.preventDefault();
    const serverToken = token.trim();
    if (!serverToken) return;
    void attempt({ ...identity, serverToken });
  };

  const handleAzure = async () => {
    setBusy(true);
    setError(null);
    try {
      const next = await signInAzure();
      await attempt(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign-in failed');
      setBusy(false);
    }
  };

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        // Dismissable by clicking away: the board can still be read while the
        // session is dead, so this must not hold the app hostage.
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div
        ref={boxRef}
        className="modal confirm"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={bodyId}
        aria-busy={busy}
        tabIndex={-1}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.stopPropagation();
            if (!busy) onClose();
            return;
          }
          onDialogKeyDown(e);
        }}
      >
        <h2 id={titleId}>Session expired</h2>
        <p id={bodyId} className="mnote">
          The server is no longer accepting this session, so nothing can be
          saved. The board lives on the server and there is no copy on this
          device, so any change you make now is refused and undone. Reconnect
          to start saving again.
        </p>
        {/* The failure message rides in the action row's reserved slot (same
            pattern as Settings): mounted above the controls it grew the modal
            and jumped the buttons at the exact moment the user was about to
            press one. Keyed so a repeat failure is a fresh node — a role that
            appears by mutation is not reliably announced. */}
        {identity.kind === 'azure' ? (
          <>
            <button
              type="button"
              className="gate-btn ms"
              disabled={busy}
              onClick={() => void handleAzure()}
            >
              {busy ? 'Signing in…' : 'Sign in again'}
            </button>
            <div className="actions">
              <button type="button" onClick={onSignOut} disabled={busy}>
                Sign out
              </button>
              <button type="button" onClick={onClose} disabled={busy}>
                Keep looking
              </button>
              <span className="statusline">
                {error && (
                  <span
                    key={error}
                    className="flash err"
                    id={errorId}
                    role="alert"
                    title={error}
                  >
                    {error}
                  </span>
                )}
              </span>
            </div>
          </>
        ) : (
          <form onSubmit={handleToken}>
            <label htmlFor="r-token">server token</label>
            <input
              id="r-token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="the token this server requires"
              autoComplete="off"
              disabled={busy}
              aria-invalid={error !== null || undefined}
              aria-describedby={error ? errorId : undefined}
              autoFocus
            />
            <div className="actions">
              <button type="button" onClick={onSignOut} disabled={busy}>
                Sign out
              </button>
              <button type="button" onClick={onClose} disabled={busy}>
                Keep looking
              </button>
              <span className="statusline">
                {error && (
                  <span
                    key={error}
                    className="flash err"
                    id={errorId}
                    role="alert"
                    title={error}
                  >
                    {error}
                  </span>
                )}
              </span>
              <button
                type="submit"
                className="save"
                disabled={busy || !token.trim()}
              >
                {busy ? 'Checking…' : 'Reconnect'}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
