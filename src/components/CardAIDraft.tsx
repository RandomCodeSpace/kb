import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { RefObject } from 'react';
import type { AIStoryRequest, StoryDraft } from '../lib/api';
import { isAbortError } from '../lib/api';
import type { Check, Effort, Prio } from '../lib/model';
import type { ModalState } from './CardModal';

export interface CardAIDraftProps {
  state: ModalState;
  aiDraft: (req: AIStoryRequest, signal?: AbortSignal) => Promise<StoryDraft>;
  title: string;
  desc: string;
  prio: Prio;
  due: string;
  effort: Effort | '';
  tags: string[];
  checks: Check[];
  onApply: (draft: StoryDraft) => void;
  onBusyChange: (busy: boolean) => void;
  cancelRef: RefObject<(() => void) | null>;
}

export function CardAIDraft({
  state,
  aiDraft,
  title,
  desc,
  prio,
  due,
  effort,
  tags,
  checks,
  onApply,
  onBusyChange,
  cancelRef,
}: Readonly<CardAIDraftProps>) {
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const abort = useRef<AbortController | null>(null);
  const errorId = useId();
  const cancel = useCallback(() => abort.current?.abort(), []);

  useEffect(() => {
    cancelRef.current = cancel;
    return () => {
      abort.current?.abort();
      cancelRef.current = null;
    };
  }, [cancel, cancelRef]);

  useEffect(() => onBusyChange(busy), [busy, onBusyChange]);

  const runDraft = async () => {
    if (busy || !prompt.trim()) return;
    setBusy(true);
    setError(null);
    const req: AIStoryRequest = {
      mode: state.mode === 'add' ? 'create' : 'update',
      prompt: prompt.trim(),
    };
    if (state.mode === 'edit') {
      req.task = { title: title.trim(), desc: desc.trim(), prio, due, effort, tags, checks };
    }
    const ctrl = new AbortController();
    abort.current = ctrl;
    try {
      onApply(await aiDraft(req, ctrl.signal));
    } catch (err) {
      if (!isAbortError(err)) setError(err instanceof Error ? err.message : 'draft failed');
    } finally {
      abort.current = null;
      setBusy(false);
    }
  };

  let statusNote = <span className="ai-note">fills the form below — review, then Save</span>;
  if (busy) {
    statusNote = <output className="ai-note busy">Drafting the card…</output>;
  } else if (error) {
    statusNote = (
      <span key={error} className="ai-note flash err" id={errorId} role="alert" title={error}>
        {error}
      </span>
    );
  }

  return (
    <div className="ai-box">
      <label htmlFor="f-ai">✨ Draft with AI</label>
      <textarea
        id="f-ai"
        rows={2}
        value={prompt}
        onChange={(event) => setPrompt(event.target.value)}
        placeholder={state.mode === 'add' ? 'Describe the task to draft…' : 'How should this card change?'}
        disabled={busy}
        aria-invalid={error !== null}
        aria-describedby={error ? errorId : undefined}
      />
      <div className="ai-row">
        <button
          type="button"
          className="ai-go"
          onClick={busy ? cancel : () => void runDraft()}
          disabled={!busy && !prompt.trim()}
        >
          {busy ? 'Cancel' : 'Draft'}
        </button>
        {statusNote}
      </div>
    </div>
  );
}
