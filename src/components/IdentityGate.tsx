import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import type { Identity } from '../lib/auth';
import { azureAvailable, signInAzure } from '../lib/auth';

export interface IdentityGateProps {
  onIdentity: (identity: Identity) => void;
}

/** The gate renders once, so a constant id is enough to point the field at. */
const ERROR_ID = 'g-error';

export function IdentityGate({ onIdentity }: IdentityGateProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [id, setId] = useState('');
  const [token, setToken] = useState('');
  // The server's GET /api/config decides, so this is only known after a
  // round trip; null means "still asking" and keeps the button disabled
  // without yet claiming Azure is unconfigured.
  const [configured, setConfigured] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    azureAvailable().then(
      (ok) => {
        if (!cancelled) setConfigured(ok);
      },
      () => {
        if (!cancelled) setConfigured(false);
      },
    );
    return () => {
      cancelled = true;
    };
  }, []);

  const handleAzure = async () => {
    setBusy(true);
    setError(null);
    try {
      onIdentity(await signInAzure());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign-in failed');
    } finally {
      setBusy(false);
    }
  };

  const handleManual = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = id.trim();
    if (!trimmed) return;
    // Mirror the server, which rejects (never substitutes) ids with
    // characters outside [a-z0-9._@-] or a leading dot.
    if (!/^[a-zA-Z0-9._@-]+$/.test(trimmed) || trimmed.startsWith('.')) {
      setError(
        'id may only use letters, digits and . _ @ - (no leading dot)',
      );
      return;
    }
    setError(null);
    const serverToken = token.trim();
    onIdentity({
      kind: 'manual',
      id: trimmed,
      ...(serverToken ? { serverToken } : {}),
    });
  };

  return (
    // The sign-in card is the whole page here: without a landmark none of it
    // sits in one.
    <main className="gate">
      <div className="gate-card">
        <h1>kb</h1>
        <p className="gate-tagline">
          a tiny kanban board that lives in markdown
        </p>
        <button
          type="button"
          className="gate-btn ms"
          disabled={configured !== true || busy}
          onClick={handleAzure}
        >
          {busy ? 'Signing in…' : 'Sign in with Microsoft'}
        </button>
        {configured === false && (
          <p className="gate-note">
            not configured — set KB_AZURE_CLIENT_ID / KB_AZURE_TENANT_ID
          </p>
        )}
        {error && (
          <p className="gate-error" id={ERROR_ID} role="alert">
            {error}
          </p>
        )}
        <div className="gate-divider">
          <span>or</span>
        </div>
        <form onSubmit={handleManual}>
          <label htmlFor="g-id">your unique id (email or handle)</label>
          <input
            id="g-id"
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="alice@example.com"
            autoComplete="username"
            // The only rejection this form produces is about this field, and
            // the message says which characters are allowed.
            aria-invalid={error !== null || undefined}
            aria-describedby={error ? ERROR_ID : undefined}
          />
          <label htmlFor="g-token">server token (if the server requires one)</label>
          <input
            id="g-token"
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="optional"
            autoComplete="off"
          />
          <button
            type="submit"
            className="gate-btn cont"
            disabled={!id.trim()}
          >
            Continue
          </button>
        </form>
      </div>
    </main>
  );
}
