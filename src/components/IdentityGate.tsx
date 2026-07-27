import { useState } from 'react';
import type { FormEvent } from 'react';
import type { Identity } from '../lib/auth';
import { azureConfigured, signInAzure } from '../lib/auth';

export interface IdentityGateProps {
  onIdentity: (identity: Identity) => void;
}

export function IdentityGate({ onIdentity }: IdentityGateProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [id, setId] = useState('');
  const [token, setToken] = useState('');
  const configured = azureConfigured();

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
    <div className="gate">
      <div className="gate-card">
        <h1>kb</h1>
        <p className="gate-tagline">
          a tiny kanban board that lives in markdown
        </p>
        <button
          type="button"
          className="gate-btn ms"
          disabled={!configured || busy}
          onClick={handleAzure}
        >
          {busy ? 'Signing in…' : 'Sign in with Microsoft'}
        </button>
        {!configured && (
          <p className="gate-note">not configured — set VITE_AZURE_* env</p>
        )}
        {error && <p className="gate-error">{error}</p>}
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
    </div>
  );
}
