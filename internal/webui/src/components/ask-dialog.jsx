import { useEffect, useRef, useState } from 'react';
import { state } from '@/store';
import { Button } from '@/components/ui/button';
import { Dialog, DialogHeader, DialogTitle, DialogBody, DialogFooter } from '@/components/ui/dialog';
import { Field } from '@/components/ui/field';
import { Textarea } from '@/components/ui/textarea';
import { Markdown } from '@/components/markdown';
import { isDialogOpen } from '@/lib/dialogs';

// One modal for confirmations and short prompts. Actions render left to
// right, so the recommended one goes last.
export function AskDialog() {
  const open = isDialogOpen('ask');
  const ask = state.ask;
  const finish = (value, text) => { if (state.ask) state.ask.finish(value, text); };
  return (
    <Dialog name="ask" open={open} onClose={() => finish(null)} aria-labelledby="ask-title" aria-describedby="ask-body">
      {ask && <AskForm key={ask.opts.title + open} opts={ask.opts} finish={finish} open={open} />}
    </Dialog>
  );
}
function AskForm({ opts, finish, open }) {
  const [text, setText] = useState('');
  const inputRef = useRef(null);
  const lastRef = useRef(null);
  const actions = opts.actions && opts.actions.length ? opts.actions : [{ value: null, label: 'Cancel' }, { value: 'ok', label: 'OK', variant: 'default' }];
  const primary = actions[actions.length - 1];
  const max = opts.field ? opts.field.max || 500 : 0;
  const over = !!opts.field && text.length > max;
  useEffect(() => {
    if (!open) return;
    if (inputRef.current) inputRef.current.focus();
    else if (lastRef.current) lastRef.current.focus();
  }, [open]);
  return (
    <form id="ask-form" noValidate onSubmit={(e) => { e.preventDefault(); if (!over) finish(primary.value, text); }}>
      <DialogHeader onClose={() => finish(null)}><DialogTitle id="ask-title">{opts.title || 'Are you sure?'}</DialogTitle></DialogHeader>
      <DialogBody id="ask-body">
        {opts.message && <p className="text-13"><Markdown text={opts.message} mode="inline" /></p>}
        {opts.note && <p className="mt-2 text-12 text-fg-2">{opts.note}</p>}
        {opts.field && (
          // Never block a paste: the text lands, and the count says it is too long.
          <Field label={opts.field.label} className="mt-4" tone={over ? 'error' : ''} message={<span className="num">{text.length}/{max}{over ? ` — trim ${text.length - max} to save` : ''}</span>}>
            <Textarea ref={inputRef} rows={2} placeholder={opts.field.placeholder || ''} value={text} onChange={(e) => setText(e.target.value)} />
          </Field>
        )}
      </DialogBody>
      <DialogFooter>
        <span className="flex-1" />
        {actions.map((a, i) => (
          <Button key={i} ref={a === primary ? lastRef : undefined} type={a === primary ? 'submit' : 'button'} variant={a.variant || 'secondary'} disabled={a === primary && over}
            onClick={a === primary ? undefined : () => finish(a.value, text)}>{a.label}</Button>
        ))}
      </DialogFooter>
    </form>
  );
}
