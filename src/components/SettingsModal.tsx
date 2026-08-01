import { useEffect, useId, useRef, useState } from 'react';
import type { Identity } from '../lib/auth';
import { ReauthRequiredError } from '../lib/auth';
import { isAbortError } from '../lib/api';
// allow: SIZE_OK - import-only settings API integration; modal decomposition is outside A5.
import type { AISettings, AITestProbe, SettingsPatch } from '../lib/settings';
import { aiTest, getSettings, putSettings } from '../lib/settings';
import { useDialogFocus } from '../lib/focus';
import { IntegrationsSection } from './IntegrationsSection';

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

export type Flash =
  | { kind: 'idle' }
  | { kind: 'busy' }
  | { kind: 'ok'; msg: string }
  | { kind: 'err'; msg: string };

/** What the form's one status line shows; see formStatus. */
export type FormStatus =
  | { kind: 'idle' }
  | { kind: 'busy'; msg: string }
  | { kind: 'ok'; msg: string }
  | { kind: 'err'; msg: string; source: 'save' | 'test' };

/**
 * The one message the form shows for its save and test outcomes. One slot
 * with a reserved line instead of a paragraph per outcome, because paragraphs
 * mounting and unmounting resized the modal on every action — which read as
 * the screen flickering. Priority: a running test, then errors (a failed save
 * stays visible over a later successful test — the save is still unsaved),
 * then successes.
 */
export function formStatus(save: Flash, test: Flash): FormStatus {
  if (test.kind === 'busy') return { kind: 'busy', msg: 'Testing the connection…' };
  if (test.kind === 'err') return { kind: 'err', msg: test.msg, source: 'test' };
  if (save.kind === 'err') return { kind: 'err', msg: save.msg, source: 'save' };
  if (test.kind === 'ok') return { kind: 'ok', msg: `✓ ${test.msg}` };
  if (save.kind === 'ok') return { kind: 'ok', msg: save.msg };
  return { kind: 'idle' };
}

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
 * Why the settings could not be loaded. An expired session is the common
 * cause and the old wording ("try reopening") was actively misleading for it:
 * reopening re-issues the same unauthenticated request and fails the same way.
 * The board carries the Reconnect control, so that is where this points.
 */
export function loadErrorMessage(err: unknown): string {
  return err instanceof ReauthRequiredError
    ? 'session expired — reconnect from the board to load these settings'
    : 'could not load settings — the server did not answer';
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
}: Readonly<SettingsModalProps>) {
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
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
  const status = formStatus(save, test);
  // Save and test failures are about the connection these three fields
  // describe, so all three point at the showing message rather than leaving a
  // keyboard user to find it by chance.
  // A load failure is not in this list: it replaces the form rather than
  // annotating it, so no field is left to point at the message.
  const failed = status.kind === 'err';
  const describedBy = failed ? `${errorId}-status` : undefined;
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
      .catch((err: unknown) => {
        if (!cancelled) setLoadError(loadErrorMessage(err));
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
        <IntegrationsSection identity={identity} serverPresent={serverPresent} />
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
        {/* The form is not shown when the load failed. Nothing came back to
            fill it, and the same failure blocks the save and the test — it
            would be three dead controls under an error, and a row of greyed
            buttons is not an explanation. The message is. */}
        {loadError && (
          <>
            <p className="flash err" id={`${errorId}-load`} role="alert">
              {loadError}
            </p>
            <div className="actions">
              <button type="button" onClick={onClose}>
                Close
              </button>
            </div>
          </>
        )}
        {serverPresent && !loadError && (
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
            {/* Above the row, not below it: what a button does is worth
                reading before pressing it, and the row is pinned to the bottom
                of the modal — anything after it scrolls underneath. */}
            <p className="mnote">
              Test connection uses the values in this form, so a key can be
              checked before it is saved. Leave the key blank to test the saved
              one. A key typed here is used for that one request only.
            </p>
            {/* Close and Test on the left, the primary action on the right.
                Close is locked while a request is in flight, like the backdrop:
                closing mid-save would discard the key being saved along with
                any error the request is about to report. Last in the form so
                the pinned row covers nothing.

                The outcome message lives inside this row (see formStatus): the
                row is pinned and its height is set by the buttons, so the
                message is always in view and its coming and going can never
                resize the modal — a paragraph above the row did both. */}
            <div className="actions">
              <button type="button" onClick={onClose} disabled={busy}>
                Close
              </button>
              {test.kind === 'busy' ? (
                <button type="button" className="test" onClick={cancelTest}>
                  Cancel test
                </button>
              ) : (
                <button
                  type="button"
                  className="test"
                  onClick={() => void handleTest()}
                  disabled={!loaded || busy}
                >
                  Test connection
                </button>
              )}
              {/* Keyed on kind+message: without it React mutates one span's
                  role and text in place, and a role that appears by mutation
                  is not reliably announced — a fresh node in the DOM is. The
                  title carries the untruncated message (the line clamps at
                  two rows so a long server error cannot grow the row). */}
              <span className="statusline">
                {status.kind === 'err' ? (
                  <span
                    key={`err:${status.msg}`}
                    className="flash err"
                    id={`${errorId}-status`}
                    role="alert"
                    title={status.msg}
                  >
                    {status.msg}
                  </span>
                ) : status.kind !== 'idle' ? (
                  <span
                    key={`${status.kind}:${status.msg}`}
                    className={`flash ${status.kind}`}
                    role="status"
                    title={status.msg}
                  >
                    {status.msg}
                  </span>
                ) : null}
              </span>
              <button type="submit" className="save" disabled={!loaded || busy}>
                {save.kind === 'busy' ? 'Saving…' : 'Save'}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
