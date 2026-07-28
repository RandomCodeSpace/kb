import { useEffect, useId, useRef, useState } from 'react';
import type { Identity } from '../lib/auth';
import type { AISettings, AITestProbe, SettingsPatch } from '../lib/api';
import { aiTest, getSettings, isAbortError, putSettings } from '../lib/api';
import { useDialogFocus } from '../lib/focus';

export interface SettingsModalProps {
  identity: Identity;
  /**
   * Whether a kb server answered /api/health. The AI section needs one; the
   * debug toggle is a local display preference, so the modal still opens
   * (and still toggles it) when the board is running offline.
   */
  serverPresent: boolean;
  /** Whether the debug overlay is currently shown. */
  debug: boolean;
  /** Toggle the overlay; the caller persists the flag (see DebugOverlay). */
  onDebugChange: (on: boolean) => void;
  onClose: () => void;
  /** Called after a successful save with the new client-visible settings. */
  onSaved: (settings: AISettings) => void;
}

type Flash =
  | { kind: 'idle' }
  | { kind: 'busy' }
  | { kind: 'ok'; msg: string }
  | { kind: 'err'; msg: string };

/**
 * What "Test connection" sends: the values in the form, so a key can be
 * checked before it is saved. A blank key field means "test the key the
 * server already has" — the field is omitted rather than sent empty, which
 * would test an empty key.
 */
export function testProbe(
  baseUrl: string,
  model: string,
  key: string,
): AITestProbe {
  const probe: AITestProbe = {
    ai_base_url: baseUrl.trim(),
    ai_model: model.trim(),
  };
  if (key !== '') probe.ai_key = key;
  return probe;
}

/**
 * What Escape does, given what is in flight. A test is cancellable, so
 * Escape cancels it rather than closing over the values being validated. A
 * save is not: the PUT may already have been applied, and closing would drop
 * both the typed key and any error the request is about to report — so
 * Escape does nothing until it settles.
 */
export function escapeAction(
  saving: boolean,
  testing: boolean,
): 'cancel-test' | 'close' | 'ignore' {
  if (testing) return 'cancel-test';
  return saving ? 'ignore' : 'close';
}

export function SettingsModal({
  identity,
  serverPresent,
  debug,
  onDebugChange,
  onClose,
  onSaved,
}: SettingsModalProps) {
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [baseUrl, setBaseUrl] = useState('');
  const [model, setModel] = useState('');
  const [key, setKey] = useState('');
  const [hasKey, setHasKey] = useState(false);
  const [save, setSave] = useState<Flash>({ kind: 'idle' });
  const [test, setTest] = useState<Flash>({ kind: 'idle' });
  const testAbort = useRef<AbortController | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const onDialogKeyDown = useDialogFocus(boxRef);
  const errorId = useId();
  const busy = save.kind === 'busy' || test.kind === 'busy';
  // Save and test failures are about the connection these three fields
  // describe, so all three point at whichever messages are showing rather than
  // leaving a keyboard user to find them by chance.
  const errorIds: string[] = [];
  if (loadError) errorIds.push(`${errorId}-load`);
  if (save.kind === 'err') errorIds.push(`${errorId}-save`);
  if (test.kind === 'err') errorIds.push(`${errorId}-test`);
  const failed = errorIds.length > 0;
  const describedBy = failed ? errorIds.join(' ') : undefined;
  // The Escape handler is bound once, so it reads the save through a ref;
  // state captured in that closure would be the value at subscription time.
  const savingRef = useRef(false);
  savingRef.current = save.kind === 'busy';

  const cancelTest = () => testAbort.current?.abort();

  useEffect(() => {
    if (!serverPresent) return;
    let cancelled = false;
    getSettings(identity)
      .then((s) => {
        if (cancelled) return;
        setBaseUrl(s.ai_base_url);
        setModel(s.ai_model);
        setHasKey(s.has_key);
        setLoaded(true);
      })
      .catch(() => {
        if (!cancelled) setLoadError(true);
      });
    return () => {
      cancelled = true;
    };
  }, [identity, serverPresent]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      const act = escapeAction(savingRef.current, testAbort.current !== null);
      if (act === 'cancel-test') testAbort.current?.abort();
      else if (act === 'close') onClose();
      // 'ignore': a save is in flight — see escapeAction.
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // Closed mid-test: don't leave the request running with nowhere to land.
  useEffect(() => () => testAbort.current?.abort(), []);

  const handleSave = async () => {
    setSave({ kind: 'busy' });
    setTest({ kind: 'idle' });
    const patch: SettingsPatch = {
      ai_base_url: baseUrl.trim(),
      ai_model: model.trim(),
    };
    // An empty key field means "keep the stored key" — omit it entirely.
    if (key !== '') patch.ai_key = key;
    try {
      const { keyCleared } = await putSettings(identity, patch);
      // A base-URL host change makes the server drop the stored key so it
      // can never be sent to the new endpoint unconfirmed.
      const nextHasKey = key !== '' ? true : keyCleared ? false : hasKey;
      setHasKey(nextHasKey);
      setKey('');
      setSave({
        kind: 'ok',
        msg: keyCleared
          ? 'saved — base URL changed, re-enter your API key'
          : 'saved',
      });
      onSaved({
        ai_base_url: baseUrl.trim(),
        ai_model: model.trim(),
        has_key: nextHasKey,
      });
    } catch (err) {
      setSave({
        kind: 'err',
        msg: err instanceof Error ? err.message : 'save failed',
      });
    }
  };

  const handleTest = async () => {
    setTest({ kind: 'busy' });
    const ctrl = new AbortController();
    testAbort.current = ctrl;
    try {
      // The form's values, not the stored ones — that is the whole point:
      // no one should have to save an unvalidated key to check it. The key
      // is used for this one request and never persisted.
      const r = await aiTest(identity, testProbe(baseUrl, model, key), ctrl.signal);
      setTest(
        r.ok
          ? { kind: 'ok', msg: 'connection ok' }
          : { kind: 'err', msg: r.error ?? 'connection failed' },
      );
    } catch (err) {
      // A cancel is not a failure and changed nothing — back to idle.
      setTest(
        isAbortError(err)
          ? { kind: 'idle' }
          : {
              kind: 'err',
              msg: err instanceof Error ? err.message : 'connection failed',
            },
      );
    } finally {
      testAbort.current = null;
    }
  };

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        // A stray click outside must not discard the values being tested;
        // Close, Cancel test or Escape are the deliberate ways out.
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div
        className="modal settings"
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        aria-busy={busy}
        tabIndex={-1}
        ref={boxRef}
        onKeyDown={onDialogKeyDown}
      >
        <h2>Settings</h2>
        {/* The header shows a short name and truncates a long one, and its
            full value is otherwise only a hover tooltip — which a keyboard or
            a touch has no way to reach. It is printed in full here. */}
        <p className="mnote">
          Signed in as <code>{identity.id}</code>
        </p>
        {/* Applies immediately and is remembered on this device — it is a
            display preference, not part of the AI form's Save. */}
        <label className="checkline" htmlFor="s-debug">
          <input
            id="s-debug"
            type="checkbox"
            checked={debug}
            onChange={(e) => onDebugChange(e.target.checked)}
          />
          Show debug overlay
        </label>
        <p className="mnote">
          A small panel with the frame rate, renderer capabilities and a frame
          cap. Remembered on this device; <code>?debug=1</code> in the URL still
          forces it on.
        </p>
        <h3>AI drafting</h3>
        {/* Without a server there is nothing to configure and nowhere to load
            from — the modal is open for the toggle above. */}
        {!serverPresent && (
          <>
            <p className="mnote">
              AI drafting needs the kb server. This board is running from local
              storage, so there are no AI settings to change here.
            </p>
            <div className="actions">
              <button type="button" onClick={onClose}>
                Close
              </button>
            </div>
          </>
        )}
        {loadError && (
          <p className="flash err" id={`${errorId}-load`} role="alert">
            could not load settings — try reopening
          </p>
        )}
        {serverPresent && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void handleSave();
            }}
          >
            {/* Locked while a save or a test runs: the request carries these
                values, so editing them mid-flight would make the result belong
                to a form that no longer exists. */}
            <label htmlFor="s-base">AI base URL</label>
            <input
              id="s-base"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://api.openai.com/v1"
              autoComplete="off"
              disabled={busy}
              aria-invalid={failed || undefined}
              aria-describedby={describedBy}
              autoFocus
            />
            <label htmlFor="s-model">Model</label>
            <input
              id="s-model"
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder="gpt-4o-mini"
              autoComplete="off"
              disabled={busy}
              aria-invalid={failed || undefined}
              aria-describedby={describedBy}
            />
            <label htmlFor="s-key">API key</label>
            <input
              id="s-key"
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder={hasKey ? '••• saved — leave blank to keep' : 'sk-…'}
              autoComplete="off"
              disabled={busy}
              aria-invalid={failed || undefined}
              aria-describedby={describedBy}
            />
            {test.kind === 'busy' && (
              <p className="flash busy" role="status">
                Testing the connection…
              </p>
            )}
            {/* Close and Test on the left, the primary action on the right.
                Close is locked while a request is in flight, like the backdrop:
                closing mid-save would discard the key being saved along with
                any error the request is about to report. */}
            <div className="actions">
              <button type="button" onClick={onClose} disabled={busy}>
                Close
              </button>
              {test.kind === 'busy' ? (
                <button type="button" onClick={cancelTest}>
                  Cancel test
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => void handleTest()}
                  disabled={!loaded || busy}
                >
                  Test connection
                </button>
              )}
              <button type="submit" className="save" disabled={!loaded || busy}>
                {save.kind === 'busy' ? 'Saving…' : 'Save'}
              </button>
            </div>
            {save.kind === 'ok' && (
              <p className="flash ok" role="status">
                {save.msg}
              </p>
            )}
            {save.kind === 'err' && (
              <p className="flash err" id={`${errorId}-save`} role="alert">
                {save.msg}
              </p>
            )}
            {test.kind === 'ok' && (
              <p className="flash ok" role="status">
                ✓ {test.msg}
              </p>
            )}
            {test.kind === 'err' && (
              <p className="flash err" id={`${errorId}-test`} role="alert">
                {test.msg}
              </p>
            )}
            <p className="mnote">
              Test connection uses the values in this form, so a key can be
              checked before it is saved. Leave the key blank to test the saved
              one. A key typed here is used for that one request only.
            </p>
          </form>
        )}
      </div>
    </div>
  );
}
