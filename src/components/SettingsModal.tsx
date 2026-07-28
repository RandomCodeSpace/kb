import { useEffect, useState } from 'react';
import type { Identity } from '../lib/auth';
import type { AISettings, SettingsPatch } from '../lib/api';
import { aiTest, getSettings, putSettings } from '../lib/api';

export interface SettingsModalProps {
  identity: Identity;
  onClose: () => void;
  /** Called after a successful save with the new client-visible settings. */
  onSaved: (settings: AISettings) => void;
}

type Flash =
  | { kind: 'idle' }
  | { kind: 'busy' }
  | { kind: 'ok'; msg: string }
  | { kind: 'err'; msg: string };

export function SettingsModal({ identity, onClose, onSaved }: SettingsModalProps) {
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [baseUrl, setBaseUrl] = useState('');
  const [model, setModel] = useState('');
  const [key, setKey] = useState('');
  const [hasKey, setHasKey] = useState(false);
  const [save, setSave] = useState<Flash>({ kind: 'idle' });
  const [test, setTest] = useState<Flash>({ kind: 'idle' });

  useEffect(() => {
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
  }, [identity]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

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
    try {
      const r = await aiTest(identity);
      setTest(
        r.ok
          ? { kind: 'ok', msg: 'connection ok' }
          : { kind: 'err', msg: r.error ?? 'connection failed' },
      );
    } catch (err) {
      setTest({
        kind: 'err',
        msg: err instanceof Error ? err.message : 'connection failed',
      });
    }
  };

  return (
    <div
      className="modal-backdrop"
      onPointerDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal settings" role="dialog" aria-modal="true" aria-label="Settings">
        <h2>Settings · AI drafting</h2>
        {loadError && (
          <p className="flash err" role="alert">
            could not load settings — try reopening
          </p>
        )}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void handleSave();
          }}
        >
          <label htmlFor="s-base">AI base URL</label>
          <input
            id="s-base"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://api.openai.com/v1"
            autoComplete="off"
            autoFocus
          />
          <label htmlFor="s-model">Model</label>
          <input
            id="s-model"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="gpt-4o-mini"
            autoComplete="off"
          />
          <label htmlFor="s-key">API key</label>
          <input
            id="s-key"
            type="password"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder={hasKey ? '••• saved — leave blank to keep' : 'sk-…'}
            autoComplete="off"
          />
          <div className="actions">
            <button
              type="submit"
              className="save"
              disabled={!loaded || save.kind === 'busy'}
            >
              {save.kind === 'busy' ? 'Saving…' : 'Save'}
            </button>
            <button
              type="button"
              onClick={() => void handleTest()}
              disabled={test.kind === 'busy'}
            >
              {test.kind === 'busy' ? 'Testing…' : 'Test connection'}
            </button>
            <button type="button" onClick={onClose}>
              Close
            </button>
          </div>
          {save.kind === 'ok' && <p className="flash ok">{save.msg}</p>}
          {save.kind === 'err' && (
            <p className="flash err" role="alert">
              {save.msg}
            </p>
          )}
          {test.kind === 'ok' && <p className="flash ok">✓ {test.msg}</p>}
          {test.kind === 'err' && (
            <p className="flash err" role="alert">
              {test.msg}
            </p>
          )}
          <p className="mnote">
            Test connection uses the last saved settings — Save first.
          </p>
        </form>
      </div>
    </div>
  );
}
