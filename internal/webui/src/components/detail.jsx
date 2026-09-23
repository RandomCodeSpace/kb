import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { cn } from '@/lib/utils';
import { state, isOpen, STATUSES, STATUS_LABEL, PRIO_LABEL, EFFORTS } from '@/store';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Chip } from '@/components/ui/chip';
import { DatePicker } from '@/components/ui/date-picker';
import { Dialog } from '@/components/ui/dialog';
import { FieldMessage } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Progress } from '@/components/ui/progress';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Icon, PriorityIcon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { MarkdownEditor } from '@/components/markdown-editor';
import { LabelEditor } from '@/components/label-editor';
import { DueChip, TaskLink, Time, OpenLink, Section } from '@/components/bits';
import { fmtDate, plural } from '@/lib/format';
import { userTags } from '@/lib/labels';
import { mutate, patchTask, setBlocked } from '@/lib/api';
import { isDialogOpen } from '@/lib/dialogs';
import { projectList } from '@/lib/filters';
import { moveMany, shipTask, cancelTask, restoreTask, deleteTask } from '@/lib/board';
import { closeDetail, reloadDetail, addComment, updateComment, deleteComment, addLink, removeLink, checkDrift, acceptDrift, DRIFT_STATE } from '@/lib/detail';
import { openEdit } from '@/lib/edit';

// The detail is a modal at every width: a centred dialog on wide screens, a
// full-height sheet on phones (the sheet class takes over below 768px).
export function DetailDialog() {
  const open = isDialogOpen('detail');
  const key = state.detail;
  const data = state.detailData;
  const bodyRef = useRef(null);
  useEffect(() => { if (open && bodyRef.current) bodyRef.current.focus({ preventScroll: true }); }, [open]);
  const task = data && data.task;
  const save = (patch, msg) => mutate(() => patchTask(task.id, patch), msg).then((out) => { if (out) reloadDetail(); return out; });
  const cancelled = task && task.status === 'cancelled';
  return (
    <Dialog name="detail" open={open} onClose={() => closeDetail()} variant="detail" aria-label="Task details">
      {key && (
        <section id="detail" className="sheet-col flex min-h-0 flex-1 flex-col" aria-labelledby="detail-title">
          <div className="flex flex-none items-start gap-2 border-b border-line px-5 pb-4 pt-4">
            <div className="min-w-0 flex-1">
              <div className="mb-1 flex h-5 items-center gap-2">
                {task ? (
                  <>
                    <span className="seq">#{task.seq}</span>
                    <Chip dot hue={task.status}><span>{STATUS_LABEL[task.status] || task.status}</span></Chip>
                    {task.blocked && <Chip tone="danger" icon="blocked"><span>Blocked</span></Chip>}
                  </>
                ) : <span className="seq">#{key}</span>}
              </div>
              <h2 id="detail-title" className="min-w-0 text-18 font-semibold tracking-tight">
                {task ? <TitleEditor key={task.id} task={task} save={save} /> : <Skeleton className="inline-block h-5 w-2/3 align-middle" />}
              </h2>
            </div>
            <Button variant="ghost" size="icon" className="-mr-2 -mt-1.5" aria-label="Close details" data-tip="Close  Esc" onClick={() => closeDetail()}><Icon name="x" /></Button>
          </div>
          <div ref={bodyRef} className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 pb-5 pt-1 outline-none" tabIndex={-1}>
            {task ? <DetailBody key={task.id} data={data} save={save} /> : (
              <><Skeleton className="h-8 w-full" /><Skeleton className="mt-2 h-8 w-4/5" /><Skeleton className="mt-6 h-24 w-full" /></>
            )}
          </div>
          <footer className="safe-bottom flex h-dialog-footer flex-none items-center gap-2 border-t border-line px-5">
            {task && (
              <>
                <Button onClick={() => openEdit(task)}><Icon name="pencil" size={14} />Edit</Button>
                <span className="flex-1" />
                {!cancelled && <Button variant="ghost" onClick={() => cancelTask(task)}>Cancel task</Button>}
                {cancelled && <Button variant="ghost-destructive" onClick={() => deleteTask(task)}>Delete</Button>}
                {cancelled && <Button variant="default" onClick={() => restoreTask(task)}>Restore</Button>}
                {isOpen(task) && <Button variant="default" onClick={() => shipTask(task)}><Icon name="ship" size={14} />Ship</Button>}
              </>
            )}
          </footer>
        </section>
      )}
    </Dialog>
  );
}

function TitleEditor({ task, save }) {
  const [editing, setEditing] = useState(false);
  const ref = useRef(null);
  const size = () => { const ta = ref.current; if (ta) { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 'px'; } };
  useLayoutEffect(() => { if (editing) { size(); ref.current.focus(); ref.current.select(); } }, [editing]);
  if (!editing) {
    return <button type="button" className="title-edit text-left" data-tip="Edit title" onClick={() => setEditing(true)}>{task.emoji ? task.emoji + ' ' : null}<Markdown text={task.title} mode="inline" /></button>;
  }
  const done = async (commit) => {
    const v = ref.current.value.trim();
    setEditing(false);
    if (commit && v && v !== task.title) await save({ title: v }, `Saved #${task.seq}`);
  };
  return (
    <Textarea ref={ref} variant="title" rows={1} aria-label="Title" data-busy="" defaultValue={task.title} spellCheck onInput={size}
      onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); done(true); } else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); done(false); } }}
      onBlur={() => done(true)} />
  );
}

function DetailBody({ data, save }) {
  const { task, comments = [], links = {}, tombstone = null, provenance = [], siblings = [] } = data;
  return (
    <>
      <Properties task={task} tombstone={tombstone} save={save} />
      <Description task={task} save={save} />
      <Checklist task={task} save={save} />
      <Blockers task={task} links={links} />
      {provenance.length > 0 && <Provenance links={provenance} siblings={siblings} />}
      <Comments task={task} comments={comments} />
    </>
  );
}

function Properties({ task, tombstone, save }) {
  const [blocked, setBlockedLocal] = useState(!!task.blocked);
  useEffect(() => { setBlockedLocal(!!task.blocked); }, [task.blocked]);
  const toggleBlocked = async (on) => {
    setBlockedLocal(on);
    const out = await mutate(() => setBlocked(task, on), on ? `#${task.seq} marked blocked` : `#${task.seq} unblocked`);
    if (out) reloadDetail(); else setBlockedLocal(!on);
  };
  return (
    <dl className="props">
      <dt>Status</dt>
      <dd>
        <ToggleGroup type="single" grow className="w-full" value={task.status} aria-label="Status" onValueChange={(v) => { if (v && v !== task.status) moveMany([task.id], v); }}>
          {STATUSES.map((s) => <ToggleGroupItem key={s} value={s} hue={s}>{STATUS_LABEL[s]}</ToggleGroupItem>)}
        </ToggleGroup>
      </dd>
      <dt>Priority</dt>
      <dd>
        <Select value={String(task.prio || 3)} onValueChange={(v) => save({ prio: Number(v) }, `#${task.seq} priority ${PRIO_LABEL[v]}`)}>
          <SelectTrigger aria-label="Priority"><span className="flex items-center gap-2"><PriorityIcon prio={task.prio || 3} label={PRIO_LABEL[task.prio || 3]} /><SelectValue /></span></SelectTrigger>
          <SelectContent>{[1, 2, 3].map((p) => <SelectItem key={p} value={String(p)}>{PRIO_LABEL[p]}</SelectItem>)}</SelectContent>
        </Select>
      </dd>
      <dt>Due</dt>
      <dd>
        <DatePicker aria-label="Due date" value={task.due || ''} onChange={(v) => save({ due: v }, v ? `#${task.seq} due ${v}` : `#${task.seq} due date cleared`)}>
          {task.due && <DueChip due={task.due} />}
        </DatePicker>
      </dd>
      <dt>Effort</dt>
      <dd>
        <ToggleGroup type="single" grow className="w-full" value={task.effort || ''} aria-label="Effort" onValueChange={(v) => { if (v && (task.effort || '') !== v) save({ effort: v }, `#${task.seq} effort ${v}`); }}>
          {EFFORTS.map((v) => <ToggleGroupItem key={v} value={v}>{v}</ToggleGroupItem>)}
        </ToggleGroup>
      </dd>
      <dt>Labels</dt>
      <dd><LabelEditor boxed className="w-full" tags={userTags(task)} onChange={(tags) => save({ tags }, `Labels saved on #${task.seq}`)} /></dd>
      <dt>Blocked</dt>
      <dd>
        <Switch checked={blocked} onCheckedChange={toggleBlocked} aria-label="Blocked" />
        <span className="text-12 text-fg-3">{blocked ? 'Finishing needs --force' : 'No'}</span>
      </dd>
      <dt>Project</dt>
      <dd>
        <Select value={task.project || ''} onValueChange={(v) => save({ project: v }, `#${task.seq} moved to ${v}`)}>
          <SelectTrigger aria-label="Project"><SelectValue /></SelectTrigger>
          <SelectContent>{projectList(task.project).map((p) => <SelectItem key={p} value={p}>{p}</SelectItem>)}</SelectContent>
        </Select>
      </dd>
      <dt>Created</dt>
      <dd className="text-12 text-fg-2">
        <Time iso={task.createdAt} />
        {task.movedAt && task.movedAt !== task.createdAt ? <span className="text-fg-3"> · moved <Time iso={task.movedAt} /></span> : null}
        {task.updatedAt && task.updatedAt !== task.createdAt && task.updatedAt !== task.movedAt ? <span className="text-fg-3"> · updated <Time iso={task.updatedAt} /></span> : null}
      </dd>
      {tombstone && (
        <>
          <dt>Killed</dt>
          <dd className="text-12 text-fg-2"><time className="num" dateTime={tombstone.killedAt} data-tip={fmtDate(tombstone.killedAt)}>{fmtDate(tombstone.killedAt)}</time></dd>
          <dt>Reason</dt>
          <dd className="text-13">{tombstone.reason ? <Markdown text={tombstone.reason} mode="inline" /> : <span className="text-fg-3">None given</span>}</dd>
        </>
      )}
    </dl>
  );
}

function Description({ task, save }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const start = () => { setDraft(task.desc || ''); setEditing(true); };
  return (
    <Section title="Description" extra={!editing && <Button variant="ghost" size="icon-xs" className="ml-auto" aria-label="Edit description" data-tip="Edit description" onClick={start}><Icon name="pencil" size={12} /></Button>}>
      {editing ? (
        <MarkdownEditor value={draft} onChange={setDraft} label="Description" placeholder="Describe the task in markdown…" rows={6} busy autoFocus
          onSave={async (v) => { if (v !== (task.desc || '')) { const out = await save({ desc: v }, `Saved #${task.seq}`); if (out === undefined) return; } setEditing(false); }}
          onCancel={() => setEditing(false)} />
      ) : (
        <div className="desc-box"><Markdown text={task.desc} empty="No description yet." /></div>
      )}
    </Section>
  );
}

function Checklist({ task, save }) {
  const checks = task.checks || [];
  const doneCount = checks.filter((c) => c.done).length;
  const [text, setText] = useState('');
  const [adding, setAdding] = useState(false);
  const addRef = useRef(null);
  const saveChecks = (next, msg) => save({ checks: next.map((c) => ({ text: c.text, done: !!c.done })) }, msg);
  const addItem = async () => {
    const v = text.trim();
    if (!v) return;
    setText('');
    setAdding(true);
    await saveChecks(checks.concat([{ text: v, done: false }]), '');
    setAdding(false);
    if (addRef.current) addRef.current.focus();
  };
  return (
    <Section title="Checklist" extra={checks.length ? <span className="num text-11 text-fg-3">{doneCount}/{checks.length}</span> : null}>
      {checks.length > 0 && <Progress className="mb-2" value={doneCount} max={checks.length} aria-label="Checklist progress" />}
      <ul className="flex flex-col">
        {checks.map((c, i) => <CheckRow key={i} c={c} i={i} checks={checks} saveChecks={saveChecks} />)}
      </ul>
      <div className="check-add">
        <span className="grid size-4 place-items-center text-fg-3"><Icon name="plus" size={12} /></span>
        <Input ref={addRef} variant="ghost-flush" className="flex-1" value={text} placeholder="Add an item…" aria-label="New checklist item" autoComplete="off" data-busy={adding ? '' : undefined}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addItem(); } else if (e.key === 'Escape' && text) { e.preventDefault(); e.stopPropagation(); setText(''); } }} />
      </div>
    </Section>
  );
}
function CheckRow({ c, i, checks, saveChecks }) {
  const [editing, setEditing] = useState(false);
  const [done, setDone] = useState(!!c.done);
  useEffect(() => { setDone(!!c.done); }, [c.done]);
  const ref = useRef(null);
  useLayoutEffect(() => { if (editing) { ref.current.focus(); ref.current.select(); } }, [editing]);
  const finish = (commit) => {
    const v = ref.current.value.trim();
    setEditing(false);
    if (commit && v && v !== c.text) saveChecks(checks.map((x, j) => (j === i ? { text: v, done: x.done } : x)), '');
    else if (commit && !v) saveChecks(checks.filter((_, j) => j !== i), 'Checklist item removed');
  };
  return (
    <li className={cn('check-row', done && 'done')}>
      <Checkbox className="mt-2" checked={done} aria-label={c.text} onCheckedChange={(v) => { setDone(v === true); saveChecks(checks.map((x, j) => (j === i ? { text: x.text, done: v === true } : x)), ''); }} />
      {editing ? (
        <Input ref={ref} variant="inline" className="flex-1" defaultValue={c.text} aria-label="Checklist item" data-busy=""
          onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); finish(true); } else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(false); } }}
          onBlur={() => finish(true)} />
      ) : (
        <button type="button" className="text min-w-0 text-left" data-tip="Edit item" onClick={() => setEditing(true)}><Markdown text={c.text} mode="inline" /></button>
      )}
      <Button variant="ghost" size="icon-xs" aria-label={`Remove “${c.text}”`} onClick={() => saveChecks(checks.filter((_, j) => j !== i), 'Checklist item removed')}><Icon name="x" size={12} /></Button>
    </li>
  );
}

function Blockers({ task, links }) {
  const [dir, setDir] = useState('blockedBy');
  const [number, setNumber] = useState('');
  const items = (list) => (list.length ? list.map((t) => (
    <span key={t.id} className="link-item">
      <TaskLink task={t} />
      <Button variant="ghost" size="icon-xs" aria-label={`Unlink #${t.seq}`} data-tip="Unlink" onClick={() => removeLink(task, t)}><Icon name="x" size={12} /></Button>
    </span>
  )) : <span className="text-12 text-fg-3">None</span>);
  return (
    <Section title="Blockers">
      <dl className="props">
        <dt>Blocked by</dt><dd>{items(links.blockedBy || [])}</dd>
        <dt>Blocks</dt><dd>{items(links.blocks || [])}</dd>
      </dl>
      <form className="mt-3 flex items-center gap-2" noValidate onSubmit={(e) => { e.preventDefault(); addLink(task, dir, number).then(() => setNumber('')); }}>
        <Select value={dir} onValueChange={setDir}>
          <SelectTrigger className="w-auto" aria-label="Link direction"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value="blockedBy">Blocked by</SelectItem><SelectItem value="blocks">Blocks</SelectItem></SelectContent>
        </Select>
        <Input className="w-20" mono inputMode="numeric" placeholder="#12" aria-label="Task number" required pattern="#?\d+" autoComplete="off" value={number} onChange={(e) => setNumber(e.target.value)} />
        <Button type="submit"><Icon name="link" size={14} />Link</Button>
      </form>
    </Section>
  );
}

function Provenance({ links, siblings }) {
  return (
    <Section title="Provenance" extra={<span className="text-11 text-fg-3">kb never syncs upstream changes into a card</span>}>
      <div className="rows">
        {links.map((l) => <ProvenanceRow key={l.externalKey || l.link} link={l} siblings={siblings.filter((s) => s.link === l.link)} />)}
      </div>
    </Section>
  );
}
function ProvenanceRow({ link, siblings }) {
  const drift = state.drift[link.externalKey];
  return (
    <div className="row">
      <div className="row-main">
        <div className="flex flex-wrap items-center gap-2">
          <Chip mono><span>{link.kind || 'forge'}</span></Chip>
          <span className="text-13 font-medium">{link.source}</span>
          <span className="seq">{link.link}</span>
        </div>
        {link.title && <p className="text-13">{link.title}</p>}
        {link.url && <p className="text-12"><OpenLink href={link.url} /></p>}
        {siblings.length > 0 && (
          <p className="text-12 text-fg-2">Also imported onto {plural(siblings.length, 'other card')}: {siblings.map((s, i) => (
            <span key={s.id}>{i ? ', ' : null}<Button variant="link" size="none" onClick={() => openDetailById(s.id)}>{s.title}</Button></span>
          ))}</p>
        )}
        <DriftBlock link={link} drift={drift} />
      </div>
    </div>
  );
}
function openDetailById(id) { import('@/lib/detail').then((m) => m.openDetail(id)); }
function DriftBlock({ link, drift }) {
  const [busy, setBusy] = useState('');
  const run = async (what, fn) => { setBusy(what); try { await fn(); } finally { setBusy(''); } };
  const check = <Button size="sm" disabled={busy === 'check'} onClick={() => run('check', () => checkDrift(link))}><Icon name="sync" size={14} />{busy === 'check' ? 'Checking drift…' : 'Check drift'}</Button>;
  if (!drift) return <div className="flex flex-col gap-2"><div className="flex flex-wrap items-center gap-2">{check}</div></div>;
  const tone = drift.state === 'drifted' ? 'warn' : drift.state === 'unchanged' ? 'ok' : 'fg-2';
  return (
    <div className="flex flex-col gap-2">
      <dl className="drift-grid">
        <dt>State</dt><dd><Chip tone={tone}><span>{DRIFT_STATE[drift.state] || drift.state}</span></Chip></dd>
        <dt>Title</dt><dd>{drift.titleChanged ? <span className="text-warn">changed upstream</span> : <span className="text-fg-2">unchanged</span>}</dd>
        {drift.upstreamTitle && <><dt>Upstream</dt><dd>{drift.upstreamTitle}</dd></>}
        {drift.baselineTitle && <><dt>Baseline</dt><dd>{drift.baselineTitle}</dd></>}
        {drift.checkedAt && <><dt>Checked</dt><dd><Time iso={drift.checkedAt} /></dd></>}
      </dl>
      {drift.state === 'baseline_recorded' && <FieldMessage>No import snapshot existed; the comparison starts from this check.</FieldMessage>}
      {drift.summary && <div className="rounded-md border border-line bg-surface px-3 py-2"><Markdown text={drift.summary} /></div>}
      <div className="flex flex-wrap items-center gap-2">
        {check}
        {drift.state === 'drifted' && drift.revision && (
          <Button variant="default" size="sm" disabled={busy === 'accept'} onClick={() => run('accept', () => acceptDrift(link, drift))}>{busy === 'accept' ? 'Updating baseline…' : 'Update baseline'}</Button>
        )}
      </div>
    </div>
  );
}

// Each comment reads as text with Edit and Delete revealed on hover; Edit
// swaps the body for an inline editor. The composer stays collapsed behind
// an "Add a comment" row until asked for.
function Comments({ task, comments }) {
  const [composing, setComposing] = useState(false);
  const [draft, setDraft] = useState('');
  const [focusTick, setFocusTick] = useState(0);
  const [sending, setSending] = useState(false);
  const wantFocus = state.detailFocusComment;
  useEffect(() => {
    if (!wantFocus) return;
    state.detailFocusComment = false;
    setComposing(true);
    setFocusTick((n) => n + 1);
  }, [wantFocus]);
  const submit = async (v) => {
    setSending(true);
    const ok = await addComment(task, v);
    setSending(false);
    if (!ok) return;
    setDraft('');
    setComposing(false);
    reloadDetail();
  };
  return (
    <Section title="Comments" extra={comments.length ? <span className="num text-11 text-fg-3">{comments.length}</span> : null}>
      {comments.length > 0 && <div className="mb-2 flex flex-col gap-2">{comments.map((c) => <Comment key={c.id} task={task} c={c} />)}</div>}
      {composing ? (
        <MarkdownEditor key={focusTick} value={draft} onChange={setDraft} label="New comment" placeholder="Write a comment…" rows={3} saveLabel="Comment" busy autoFocus
          onSave={sending ? undefined : submit} onCancel={() => { setDraft(''); setComposing(false); }} />
      ) : (
        <button type="button" className="comment-add" onClick={() => { setComposing(true); setFocusTick((n) => n + 1); }}>
          <span className="grid size-4 place-items-center"><Icon name="plus" size={12} /></span>Add a comment
        </button>
      )}
    </Section>
  );
}
function Comment({ task, c }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  return (
    <article className="comment">
      <div className="comment-head">
        <span className="text-12 font-medium">{c.author || 'default'}</span>
        <Time iso={c.createdAt} className="text-11 text-fg-3" />
        {!editing && (
          <span className="act ml-auto flex items-center">
            <Button variant="ghost" size="icon-xs" aria-label="Edit comment" data-tip="Edit comment" onClick={() => { setDraft(c.body); setEditing(true); }}><Icon name="pencil" size={12} /></Button>
            <Button variant="ghost" size="icon-xs" aria-label="Delete comment" data-tip="Delete comment" onClick={() => deleteComment(task, c)}><Icon name="trash" size={12} /></Button>
          </span>
        )}
      </div>
      {editing ? (
        <MarkdownEditor value={draft} onChange={setDraft} label="Edit comment" rows={3} busy autoFocus
          onSave={async (v) => {
            if (v.trim() === c.body) { setEditing(false); return; }
            const out = await updateComment(c, v);
            if (!out) return;
            setEditing(false);
            reloadDetail();
          }}
          onCancel={() => setEditing(false)} />
      ) : <div><Markdown text={c.body} /></div>}
    </article>
  );
}
