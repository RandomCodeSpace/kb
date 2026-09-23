import { useEffect, useId, useRef } from 'react';
import { state, STATUSES, STATUS_LABEL, PRIO_LABEL, EFFORTS } from '@/store';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Chip } from '@/components/ui/chip';
import { DatePicker } from '@/components/ui/date-picker';
import { Dialog, DialogHeader, DialogTitle, DialogBody, DialogFooter } from '@/components/ui/dialog';
import { FieldMessage } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Icon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { MarkdownEditor } from '@/components/markdown-editor';
import { LabelEditor } from '@/components/label-editor';
import { TaskLink } from '@/components/bits';
import { MOD } from '@/lib/format';
import { isDialogOpen, closeDialog } from '@/lib/dialogs';
import { setEditField, editDefaults, requestCloseEdit, saveEdit, setAIPrompt, runAIDraft } from '@/lib/edit';
import { openSettings, isAIConfigured } from '@/lib/settings';
import { openDetail } from '@/lib/detail';

export function EditDialog() {
  const open = isDialogOpen('edit');
  const edit = state.edit;
  const titleRef = useRef(null);
  const wantTitle = edit && edit.focusTitle;
  useEffect(() => {
    if (!open || !wantTitle || !titleRef.current) return;
    state.edit.focusTitle = false;
    titleRef.current.focus();
  }, [open, wantTitle]);
  const onKeyDown = (e) => {
    if (!(e.ctrlKey || e.metaKey)) return;
    if (e.shiftKey && e.key === 'Enter') {
      if (isAIConfigured() && !state.edit.ai.busy) { e.preventDefault(); runAIDraft(); }
      return;
    }
    if (!e.shiftKey && (e.key === 's' || e.key === 'Enter')) { e.preventDefault(); saveEdit(); }
  };
  const f = edit && edit.form;
  const defaults = edit ? editDefaults() : [];
  return (
    <Dialog name="edit" open={open} onClose={requestCloseEdit} variant="sheet" aria-labelledby="edit-title">
      {edit && (
        <form id="edit-form" noValidate autoComplete="off" onSubmit={(e) => { e.preventDefault(); saveEdit(); }} onKeyDown={onKeyDown}>
          <DialogHeader onClose={requestCloseEdit}><DialogTitle id="edit-title">{edit.task ? `Edit #${edit.task.seq}` : 'New task'}</DialogTitle></DialogHeader>
          <DialogBody><div className="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-2">
            <div className="flex gap-2 sm:col-span-2">
              <label className="w-14 flex-none">
                <span className="label-11 mb-1 block">Emoji</span>
                <Input variant="emoji" name="emoji" autoComplete="off" maxLength={8} placeholder="🚀" value={f.emoji} onChange={(e) => setEditField('emoji', e.target.value)} />
              </label>
              <label className="min-w-0 flex-1">
                <span className="label-11 mb-1 block">Title</span>
                <Input ref={titleRef} name="title" required autoComplete="off" placeholder="What needs doing?" enterKeyHint="done" value={f.title} onChange={(e) => setEditField('title', e.target.value)} />
              </label>
            </div>
            {edit.similar.length > 0 && (
              <div className="sm:col-span-2">
                <p className="label-11 mb-1" role="status">Possible duplicates ({edit.similar.length})</p>
                <ul className="flex flex-col gap-0.5">
                  {edit.similar.map((r, i) => (
                    <li key={r.id || r.link || i} className="flex items-center gap-1.5">
                      <TaskLink task={r} className="min-w-0" onClick={r.id ? () => { closeDialog('edit'); openDetail(r.seq || r.id); } : undefined} />
                      {r.link && <Chip mono title="Imported from this upstream issue"><span>{r.link}</span></Chip>}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <div className="sm:col-span-2"><EditAI edit={edit} /></div>
            <div className="sm:col-span-2">
              <span className="label-11 mb-1 block" id="edit-desc-label">Description</span>
              <MarkdownEditor value={f.desc} onChange={(v) => setEditField('desc', v)} label="Description" placeholder="Describe the task in markdown…" rows={5} />
            </div>
            <div>
              <span className="label-11 mb-1 block" id="edit-status-label">Status</span>
              <Select value={f.status} onValueChange={(v) => setEditField('status', v)}>
                <SelectTrigger aria-labelledby="edit-status-label"><SelectValue /></SelectTrigger>
                <SelectContent>{STATUSES.map((s) => <SelectItem key={s} value={s}>{STATUS_LABEL[s]}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            <div>
              <span className="label-11 mb-1 block" id="edit-prio-label">Priority</span>
              <Select value={f.prio} onValueChange={(v) => setEditField('prio', v)}>
                <SelectTrigger aria-labelledby="edit-prio-label"><SelectValue /></SelectTrigger>
                <SelectContent>{[1, 2, 3].map((p) => <SelectItem key={p} value={String(p)}>{PRIO_LABEL[p]}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            <div>
              <span className="label-11 mb-1 block" id="edit-due-label">Due</span>
              <DatePicker aria-labelledby="edit-due-label" value={f.due} onChange={(v) => setEditField('due', v)} />
            </div>
            <div>
              <span className="label-11 mb-1 block" id="edit-effort-label">Effort</span>
              <ToggleGroup type="single" grow value={f.effort} aria-labelledby="edit-effort-label" onValueChange={(v) => { if (v) setEditField('effort', v); }}>
                {EFFORTS.map((v) => <ToggleGroupItem key={v} value={v}>{v}</ToggleGroupItem>)}
              </ToggleGroup>
            </div>
            <div>
              <span className="label-11 mb-1 block" id="edit-project-label">Project</span>
              <Select value={f.project} onValueChange={(v) => setEditField('project', v)}>
                <SelectTrigger aria-labelledby="edit-project-label"><SelectValue placeholder="Project…" /></SelectTrigger>
                <SelectContent>{edit.projects.map((p) => <SelectItem key={p} value={p}>{p}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            <div className="flex items-end">
              <label className="flex h-8 cursor-pointer items-center gap-2 text-13">
                <Checkbox name="blocked" checked={f.blocked} onCheckedChange={(v) => setEditField('blocked', v === true)} /> Blocked
              </label>
            </div>
            <div className="sm:col-span-2">
              <span className="label-11 mb-1 block" id="edit-labels-label">Labels</span>
              <LabelEditor boxed tags={f.tags} onChange={(tags) => setEditField('tags', tags)} />
            </div>
            <label className="sm:col-span-2">
              <span className="label-11 mb-1 block">Checklist <span className="font-normal normal-case tracking-normal text-fg-3">one per line, prefix <code>x </code> when done</span></span>
              <Textarea variant="code" name="checks" rows={3} placeholder={'x reproduce locally\nwrite regression test'} value={f.checks} onChange={(e) => setEditField('checks', e.target.value)} />
            </label>
            <FieldMessage tone="warn" role="status" className="sm:col-span-2">{defaults.length ? `Defaults in use: ${defaults.join(', ')}. Pick each on purpose or save to accept them.` : ''}</FieldMessage>
          </div></DialogBody>
          <DialogFooter>
            <span className="phone-hide text-12 text-fg-3"><Kbd>Ctrl</Kbd> <Kbd>Enter</Kbd> saves</span>
            <span className="flex-1" />
            <Button onClick={requestCloseEdit}>Cancel</Button>
            <Button type="submit" variant="default" id="edit-save" disabled={edit.saving}>{edit.saving ? 'Saving…' : 'Save'}</Button>
          </DialogFooter>
        </form>
      )}
    </Dialog>
  );
}

// The editor's AI field: a prompt, one round trip, and a filled-in form the user still has to save.
function EditAI({ edit }) {
  const id = useId();
  const ta = useRef(null);
  const ai = edit.ai;
  useEffect(() => {
    const node = ta.current;
    if (!node) return;
    node.style.height = 'auto';
    node.style.height = Math.min(node.scrollHeight, 160) + 'px';
  }, [ai.prompt]);
  useEffect(() => { if (ai.focus && ta.current) { ai.focus = false; ta.current.focus(); } });
  if (!isAIConfigured()) {
    return <p className="text-12 text-fg-3">Drafting with AI is off. <Button variant="link" size="none" onClick={() => openSettings('ai')}>Add an AI endpoint</Button> to write a card from a one-line prompt.</p>;
  }
  return (
    <div className="field">
      <div className="field-head">
        <label className="label-11 flex items-center gap-1.5" htmlFor={id}><span className="ai-mark" aria-hidden="true">AI</span>Draft</label>
        <span className="text-11 text-fg-3"><Kbd>{MOD}</Kbd> <Kbd>⇧</Kbd> <Kbd>Enter</Kbd></span>
      </div>
      <div className="ai-composer">
        <Icon name="sparkle" />
        <Textarea ref={ta} variant="bare" rows={1} id={id} placeholder="Describe the card in one line" autoComplete="off" aria-label="AI draft prompt" value={ai.prompt} onChange={(e) => setAIPrompt(e.target.value)}
          onKeyDown={(e) => { if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); if (!ai.busy) runAIDraft(); } }} />
        <Button variant="default" data-ai-draft="" disabled={ai.busy} onClick={runAIDraft}>{ai.busy ? <><Spinner />Drafting…</> : <><Icon name="sparkle" size={14} />Draft</>}</Button>
      </div>
      <FieldMessage tone={ai.tone} role="status">{ai.msg}{ai.partial && <Chip tone="warn" icon="alert" className="ml-2"><span>Partial run</span></Chip>}</FieldMessage>
      {ai.commentary && <div className="mt-2 text-12 text-fg-2"><Markdown text={ai.commentary} /></div>}
    </div>
  );
}
