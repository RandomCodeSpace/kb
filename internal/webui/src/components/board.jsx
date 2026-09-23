import { memo, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { cn } from '@/lib/utils';
import { state, settings, notify, STATUS_LABEL, PRIO_LABEL, DEFAULT_EFFORT } from '@/store';
import { Button } from '@/components/ui/button';
import { Chip } from '@/components/ui/chip';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Skeleton } from '@/components/ui/skeleton';
import { Icon, PriorityIcon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { DueChip, ProgressRing, Counter } from '@/components/bits';
import { animate, plural, wideMQ } from '@/lib/format';
import { shownTags } from '@/lib/labels';
import { api, withForce, invalidate } from '@/lib/api';
import { toast, announce } from '@/lib/toast';
import { anyFilter, toggleTag, writeProject } from '@/lib/filters';
import { parseQuickAdd } from '@/lib/quickadd';
import { boardLayout, onDragStart, onDragOver, onDragLeave, onDrop, cleanupDrag, onCardPointerDown, onCardClick, setFocus, toggleCollapse, openComposer, closeComposer, addTask, shipTask, cancelTask, pruneSelection } from '@/lib/board';
import { openDetail } from '@/lib/detail';

const isOpen = (t) => t.status === 'todo' || t.status === 'doing';

function EmptyColumn({ status }) {
  const msg = anyFilter() ? 'No matches'
    : status === 'todo' ? <>Press <Kbd>n</Kbd> or click <span className="font-medium text-fg-2">+</span> to add a task</>
      : status === 'doing' ? 'Drag a card here to start it' : 'Nothing here yet';
  return <div className="col-empty">{msg}</div>;
}

// The gap a dragged card will drop into; it grows from nothing.
function DropSlot({ height }) {
  const ref = useRef(null);
  useLayoutEffect(() => {
    const node = ref.current;
    node.style.height = '0px';
    void node.offsetHeight;
    node.style.height = height + 'px';
  }, [height]);
  return <div ref={ref} className="drop-slot" aria-hidden="true" />;
}

const Card = memo(function Card({ task, sig, show, selected, focused, lifted, dragging, commentCount, pressedTags, focusReq, cardEpoch }) {
  void sig; void cardEpoch;
  const ref = useRef(null);
  useEffect(() => {
    if (!focused || !focusReq) return;
    const node = ref.current;
    if (focusReq.focus) node.focus({ preventScroll: true });
    node.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'auto' });
  }, [focused, focusReq]);
  const checks = task.checks || [];
  const doneCount = checks.filter((c) => c.done).length;
  const tags = show.tags ? shownTags(task) : [];
  const meta = [
    show.due && task.due ? <DueChip key="due" due={task.due} /> : null,
    show.checks && checks.length ? <ProgressRing key="ring" done={doneCount} total={checks.length} /> : null,
    commentCount ? <Chip key="comments" icon="comment" title={plural(commentCount, 'comment')}><span className="num">{commentCount}</span></Chip> : null,
    task.blocked ? <Chip key="blocked" tone="danger" icon="blocked" title="Blocked"><span>Blocked</span></Chip> : null,
    show.effort && task.effort ? <Chip key="effort" mono title={'Effort ' + task.effort}><span>{task.effort}</span></Chip> : null,
  ].filter(Boolean);
  const quick = (name, label, run) => (
    <Button key={name} variant="ghost" size="icon-xs" aria-label={label} data-tip={label} tabIndex={-1} onClick={(e) => { e.stopPropagation(); run(); }}><Icon name={name} size={14} /></Button>
  );
  return (
    <article ref={ref} className={cn('card', selected && 'is-selected', focused && 'is-focused', lifted && 'is-lifted', dragging && 'is-dragging')}
      draggable tabIndex={focused ? 0 : -1} role="option" aria-selected={selected ? 'true' : 'false'} inert={dragging || undefined}
      data-id={task.id} data-seq={task.seq} data-status={task.status} data-prio={String(task.prio || 3)} aria-label={`#${task.seq} ${task.title}`}
      onClick={(e) => onCardClick(e, task)} onFocus={() => { if (state.focusId !== task.id) setFocus(task.id, { focus: false }); }}
      onDragStart={(e) => onDragStart(e, task)} onDragEnd={() => cleanupDrag()} onPointerDown={(e) => onCardPointerDown(e, task)}>
      <div className="flex items-start gap-2">
        <span className="grid h-5 w-3 flex-none place-items-center"><PriorityIcon prio={task.prio || 3} label={PRIO_LABEL[task.prio || 3]} /></span>
        {show.seq && <span className="seq flex-none leading-5">#{task.seq}</span>}
        <div className="card-title min-w-0 flex-1">{show.emoji && task.emoji ? task.emoji + ' ' : null}<Markdown text={task.title} mode="inline" /></div>
      </div>
      {show.desc && task.desc && task.desc.trim() ? <Markdown text={task.desc} mode="card" /> : null}
      {meta.length > 0 && <div className="flex flex-wrap items-center gap-1">{meta}</div>}
      {tags.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {tags.map((tag) => <Chip key={tag} tag={tag} tabIndex={-1} pressed={pressedTags.includes(tag)} title={'Filter by ' + tag} onClick={() => toggleTag(tag)} />)}
        </div>
      )}
      <div className="card-actions">
        {quick('open', 'Open', () => openDetail(task.seq))}
        {isOpen(task) && quick('ship', 'Ship', () => shipTask(task))}
        {task.status !== 'cancelled' && quick('cancel', 'Cancel task', () => cancelTask(task))}
      </div>
    </article>
  );
}, (a, b) => a.sig === b.sig && a.show === b.show && a.selected === b.selected && a.focused === b.focused && a.lifted === b.lifted && a.dragging === b.dragging
  && a.commentCount === b.commentCount && a.pressedTags === b.pressedTags && a.focusReq === b.focusReq && a.cardEpoch === b.cardEpoch);

// A card fading out where it was, after it left the board.
function LeavingCard({ id, rect, children, onDone }) {
  const ref = useRef(null);
  useLayoutEffect(() => {
    const node = ref.current;
    node.style.left = rect.left + 'px';
    node.style.top = rect.top + 'px';
    node.style.width = rect.width + 'px';
    const a = animate(node, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.96)' }], 160);
    const done = () => onDone(id);
    if (a.finished) a.finished.then(done, done); else done();
  }, [id, rect, onDone]);
  return <div ref={ref} className="card leaving" aria-hidden="true">{children}</div>;
}

function Composer({ status }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const inputRef = useRef(null);
  const formRef = useRef(null);
  useEffect(() => {
    animate(formRef.current, [{ opacity: 0, transform: 'translateY(4px)' }, { opacity: 1, transform: 'none' }], 150);
    inputRef.current.focus();
  }, []);
  const p = parseQuickAdd(text);
  const close = () => {
    closeComposer();
    const tools = document.querySelector(`#board .col[data-status="${status}"] .col-tools button`);
    if (tools) tools.focus();
  };
  const submit = async () => {
    if (!p.title) { inputRef.current.focus(); return; }
    const body = { title: p.title, status, project: writeProject(), tags: p.tags };
    if (p.prio) body.prio = p.prio;
    if (p.due) body.due = p.due;
    if (p.effort) body.effort = p.effort;
    setBusy(true);
    try {
      const saved = await withForce((force) => api('POST', '/api/tasks', force ? Object.assign({ force: true }, body) : body));
      if (saved) {
        addTask(saved);
        setText('');
        const assumed = [p.prio ? '' : 'priority Low', p.effort ? '' : `effort ${DEFAULT_EFFORT}`].filter(Boolean);
        if (assumed.length) toast(`Created #${saved.seq}. Assumed ${assumed.join(', ')}; use !prio and ~effort to choose`, 'warn', { life: 6000 });
        announce(`Created #${saved.seq}`);
        const bodyEl = document.querySelector(`#board .col[data-status="${status}"] .col-body`);
        if (bodyEl) bodyEl.scrollTop = bodyEl.scrollHeight;
        invalidate();
      }
    } catch (err) { toast(err.message, 'error'); } finally { setBusy(false); queueMicrotask(() => inputRef.current && inputRef.current.focus()); }
  };
  return (
    <form ref={formRef} className="composer-open" onSubmit={(e) => { e.preventDefault(); submit(); }}>
      <Input ref={inputRef} value={text} onChange={(e) => setText(e.target.value)} disabled={busy} placeholder="Title  !high #label #type::bug @fri ~M"
        aria-label={`New task in ${STATUS_LABEL[status]}`} autoComplete="off" spellCheck={false} enterKeyHint="done"
        onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submit(); } else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); } }} />
      <div className="flex flex-wrap gap-1 empty:hidden" aria-live="polite">
        {p.prio && <Chip><PriorityIcon prio={p.prio} label={PRIO_LABEL[p.prio]} /><span>{PRIO_LABEL[p.prio]}</span></Chip>}
        {p.effort && <Chip mono><span>Effort {p.effort}</span></Chip>}
        {p.due && <DueChip due={p.due} long />}
        {p.tags.map((t) => <Chip key={t} tag={t} />)}
      </div>
      <div className="flex items-center gap-2">
        <span className="text-11 text-fg-3"><Kbd>Enter</Kbd> adds · <Kbd>Esc</Kbd> closes</span>
        <span className="flex-1" />
        <Button variant="ghost" size="sm" onClick={close}>Close</Button>
        <Button type="submit" variant="default" size="sm">Add</Button>
      </div>
    </form>
  );
}

function Column({ col, shipping }) {
  const { status, tasks, limit, over, hidden, collapsed } = col;
  const label = STATUS_LABEL[status];
  const dragging = state.dragging;
  const target = state.dropTarget && state.dropTarget.status === status ? state.dropTarget : null;
  const draggingIds = dragging ? new Set(dragging.ids) : null;
  const show = settings.show;
  const pressedTags = [...state.tags];
  const children = [];
  let k = 0;
  const slot = target && !collapsed && target.index !== null ? target.index : -1;
  for (const t of tasks) {
    const inHand = !!(draggingIds && draggingIds.has(t.id));
    if (!inHand) { if (k === slot) children.push(<DropSlot key="slot" height={dragging.height} />); k++; }
    const focused = state.focusId === t.id;
    children.push(<Card key={t.id} task={t} sig={JSON.stringify(t)} show={show} selected={state.selected.has(t.id)} focused={focused} lifted={state.lifted === t.id}
      dragging={inHand} commentCount={show.comments ? state.commentCounts[t.id] : undefined} pressedTags={pressedTags} focusReq={focused ? state.focusRequest : null} cardEpoch={state.cardEpoch} />);
  }
  if (slot >= 0 && slot >= k) children.push(<DropSlot key="slot" height={dragging.height} />);
  const count = limit ? `${tasks.length}/${limit}` : String(tasks.length);
  return (
    <section className={cn('col', over && 'is-over', collapsed && 'is-collapsed', dragging && target && 'is-target')} data-status={status} data-hue={status} aria-label={label} hidden={hidden}
      onDragOver={(e) => onDragOver(e, status)} onDragLeave={(e) => onDragLeave(e, status)} onDrop={(e) => onDrop(e, status)}>
      <div className={cn('col-head', shipping && 'shipped')}>
        <h3 className="col-name">{label}</h3>
        <Counter className="col-count" text={count} data-tip={limit ? (over ? `Over the WIP limit of ${limit}` : `WIP limit ${limit}`) : undefined} />
        <div className="col-tools">
          <Button variant="ghost" size="icon-xs" aria-label={`Add task to ${label}`} data-tip="Add task" onClick={() => openComposer(status)}><Icon name="plus" size={14} /></Button>
          <Button variant="ghost" size="icon-xs" aria-label={`${collapsed ? 'Expand' : 'Collapse'} ${label}`} data-tip="Collapse column" aria-expanded={collapsed ? 'false' : 'true'} onClick={() => toggleCollapse(status)}>
            <Icon name={collapsed ? 'chevR' : 'chevL'} size={14} />
          </Button>
        </div>
      </div>
      <div className="col-body" role="listbox" aria-multiselectable="true" aria-label={label}>
        {!state.loaded
          ? (state.waiting ? <div className="col-empty">Waiting for the server…</div> : <><Skeleton className="h-19" /><Skeleton className="h-24" /><Skeleton className="h-19" /></>)
          : children.length ? children : <EmptyColumn status={status} />}
      </div>
      <div className="composer" hidden={state.composer !== status}>{state.composer === status && <Composer status={status} />}</div>
    </section>
  );
}

export function Board() {
  const boardRef = useRef(null);
  const rects = useRef(new Map());
  const firstPaint = useRef(true);
  const lastFocused = useRef(null);
  const [leaving, setLeaving] = useState([]);
  const [shipping, setShipping] = useState(false);
  pruneSelection();
  const layout = boardLayout();
  const tasksById = new Map();
  for (const col of layout) for (const t of col.tasks) tasksById.set(t.id, t);
  const template = wideMQ.matches ? layout.filter((c) => !c.hidden).map((c) => (c.collapsed ? '44px' : 'minmax(240px, 1fr)')).join(' ') : '';
  const shipToken = state.shipToken;
  useEffect(() => {
    if (!shipToken) return undefined;
    setShipping(true);
    const t = setTimeout(() => setShipping(false), 1000);
    return () => clearTimeout(t);
  }, [shipToken]);
  useEffect(() => {
    const onFocus = (e) => { lastFocused.current = e.target; };
    document.addEventListener('focusin', onFocus);
    return () => document.removeEventListener('focusin', onFocus);
  }, []);
  // FLIP: cards slide from where they were to where they are, dropped cards
  // settle in place, new cards fade in, and cards that left fade out where
  // they stood. Nothing animates while a card is in hand.
  useLayoutEffect(() => {
    const board = boardRef.current;
    board.style.gridTemplateColumns = template;
    const prev = rects.current;
    const next = new Map();
    const cards = board.querySelectorAll('.card[data-id]:not(.leaving)');
    const still = !!(state.dragging || state.touch);
    const dropped = state.dropped;
    const gone = [];
    // Measure everything first: a running animation shows its first keyframe
    // to getBoundingClientRect, so measuring after animating would record
    // the old positions and replay the slide on every later render.
    for (const c of cards) {
      const ring = c.querySelector('.progress-ring .fill');
      next.set(c.dataset.id, { rect: c.getBoundingClientRect(), ring: ring ? ring.getAttribute('stroke-dashoffset') : null, task: tasksById.get(c.dataset.id), html: null });
    }
    let i = 0;
    for (const c of cards) {
      const id = c.dataset.id;
      const ring = c.querySelector('.progress-ring .fill');
      const before = prev.get(id);
      const r = next.get(id).rect;
      if (!still) {
        if (dropped && dropped.has(id)) animate(c, [{ transform: 'scale(0.98)', opacity: 0.6 }, { transform: 'none', opacity: 1 }], 140);
        else if (before && before.rect.width) {
          const dx = before.rect.left - r.left, dy = before.rect.top - r.top;
          if (dx || dy) animate(c, [{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], 180);
          if (ring && before.ring !== null && before.ring !== ring.getAttribute('stroke-dashoffset')) {
            animate(ring, [{ strokeDashoffset: before.ring }, { strokeDashoffset: ring.getAttribute('stroke-dashoffset') }], 180);
          }
        } else animate(c, [{ opacity: 0, transform: 'translateY(4px)' }, { opacity: 1, transform: 'none' }], 160, { delay: firstPaint.current ? Math.min(i * 10, 200) : 0 });
      }
      i++;
    }
    if (!still) {
      for (const [id, before] of prev) {
        if (!next.has(id) && !(dropped && dropped.has(id)) && before.rect.width && before.html) gone.push({ id, rect: before.rect, html: before.html });
      }
    }
    // Keep the markup of every card so a card that leaves can fade out as it was.
    for (const c of cards) { const entry = next.get(c.dataset.id); if (entry) entry.html = c.innerHTML; }
    rects.current = next;
    if (state.loaded) firstPaint.current = false;
    state.dropped = null;
    if (gone.length) setLeaving((cur) => cur.concat(gone.filter((g) => !cur.some((x) => x.id === g.id))));
    // A card that moved column was re-created: hand focus back to it.
    const was = lastFocused.current;
    if (state.focusId && was && !was.isConnected && was.closest && was.closest('.card') && document.activeElement === document.body) {
      const again = board.querySelector(`.card[data-id="${CSS.escape(state.focusId)}"]`);
      if (again) again.focus({ preventScroll: true });
    }
  });
  const onScroll = () => {
    const board = boardRef.current;
    let best = null, bestDist = Infinity;
    for (const col of board.querySelectorAll('.col')) {
      const d = Math.abs(col.offsetLeft - board.scrollLeft);
      if (d < bestDist) { bestDist = d; best = col.dataset.status; }
    }
    if (best && state.activeSegment !== best) { state.activeSegment = best; notify(); }
  };
  const doneLeaving = (id) => setLeaving((cur) => cur.filter((x) => x.id !== id));
  return (
    <main id="board" ref={boardRef} className="board" aria-label="Board" tabIndex={-1} onScroll={onScroll}>
      {layout.map((col) => <Column key={col.status} col={col} shipping={shipping && col.status === 'done'} />)}
      {leaving.map((g) => <LeavingCard key={g.id} id={g.id} rect={g.rect} onDone={doneLeaving}><div dangerouslySetInnerHTML={{ __html: g.html }} /></LeavingCard>)}
    </main>
  );
}
