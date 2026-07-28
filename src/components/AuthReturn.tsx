import { useEffect, useState } from 'react';

const STALL_MS = 12_000;

export interface AuthReturnProps {
  search?: string;
  hash?: string;
}

/**
 * Rendered in the Entra sign-in popup instead of the board.
 *
 * MSAL runs in the opener: it polls this popup, reads the authorization
 * response off the URL and closes the window. Normally this component is on
 * screen for a few hundred milliseconds and nobody reads it.
 *
 * It exists to explain the two ways that handshake stalls, because a popup
 * stuck on "Completing sign-in…" tells the user nothing:
 *
 *  1. The response arrived in the query string rather than the fragment. MSAL
 *     watches the fragment, so it never sees this and polls forever. Entra
 *     replies this way when the redirect URI is registered under the **Web**
 *     platform instead of **Single-page application** — the single most common
 *     misconfiguration, and one that also blocks the token request with CORS.
 *  2. Anything else — after a stall we show what actually came back so there
 *     is something concrete to report.
 */
export function AuthReturn({
  search = typeof location === 'undefined' ? '' : location.search,
  hash = typeof location === 'undefined' ? '' : location.hash,
}: AuthReturnProps) {
  const [stalled, setStalled] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);

  // msal-browser v5 does NOT poll the popup's URL the way v3 did — it waits on
  // a BroadcastChannel, and the redirect page is responsible for parsing the
  // response and publishing it. Skip this and the opener waits forever while
  // this window sits on "Completing sign-in…" with a perfectly good response
  // in its address bar. The same call covers the redirect flow, where it
  // navigates back to the app instead of broadcasting.
  useEffect(() => {
    let live = true;
    import('@azure/msal-browser/redirect-bridge')
      .then((m) => m.broadcastResponseToMainFrame())
      .catch((err: unknown) => {
        if (live) {
          setFailed(err instanceof Error ? err.message : String(err));
        }
      });
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    const t = setTimeout(() => setStalled(true), STALL_MS);
    return () => clearTimeout(t);
  }, []);

  const diag = failed
    ? ({ kind: 'error', detail: failed } as const)
    : diagnose(search, hash);

  if (diag.kind === 'web-platform') {
    return (
      <div className="auth-return bad">
        <h1>Sign-in cannot complete</h1>
        <p>
          Entra returned the authorization code in the <b>query string</b>. kb
          (and MSAL) expect it in the <b>fragment</b>, so the sign-in never
          finishes.
        </p>
        <p>
          This happens when the redirect URI is registered under the{' '}
          <b>Web</b> platform. In the Azure portal, open your app registration →{' '}
          <b>Authentication</b>, remove <code>{origin()}</code> from{' '}
          <b>Web</b>, and add it under <b>Single-page application (SPA)</b>{' '}
          instead — exactly <code>{origin()}</code>, no trailing slash.
        </p>
        <p className="auth-note">You can close this window.</p>
      </div>
    );
  }

  if (diag.kind === 'error') {
    return (
      <div className="auth-return bad">
        <h1>Sign-in failed</h1>
        <p>
          <code>{diag.detail}</code>
        </p>
        <p className="auth-note">You can close this window.</p>
      </div>
    );
  }

  return (
    <div className="auth-return">
      <p>Completing sign-in…</p>
      {stalled && (
        <>
          <p className="auth-note">
            This is taking longer than it should. The sign-in window should
            close on its own once the app reads the response.
          </p>
          <p className="auth-note">
            Response location: <code>{diag.where}</code>. Close this window and
            check the browser console in the kb tab behind it for an MSAL
            error.
          </p>
        </>
      )}
    </div>
  );
}

function origin(): string {
  return typeof location === 'undefined' ? 'your kb URL' : location.origin;
}

type Diagnosis =
  | { kind: 'web-platform' }
  | { kind: 'error'; detail: string }
  | { kind: 'pending'; where: string };

/**
 * Classify the authorization response purely from the URL. Exported for tests
 * so the stall paths can be checked without a browser or a tenant.
 */
export function diagnose(search: string, hash: string): Diagnosis {
  const frag = new URLSearchParams(hash.startsWith('#') ? hash.slice(1) : hash);
  const query = new URLSearchParams(search);

  const fragErr = frag.get('error');
  const queryErr = query.get('error');
  if (fragErr || queryErr) {
    const p = fragErr ? frag : query;
    const desc = p.get('error_description');
    return {
      kind: 'error',
      detail: desc ? `${p.get('error')}: ${desc}` : String(p.get('error')),
    };
  }

  // A code in the query with an empty fragment is the Web-platform signature.
  if (query.has('code') && query.has('state') && !frag.has('code')) {
    return { kind: 'web-platform' };
  }
  return { kind: 'pending', where: frag.has('code') ? 'fragment' : 'unknown' };
}
