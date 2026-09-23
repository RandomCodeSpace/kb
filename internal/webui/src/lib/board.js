// Board logic: column layout, drag and drop, keyboard focus and lift,
// selection and bulk actions, and the task verbs shared by cards and the
// detail view. Views read the layout; handlers here mutate state and notify.
import { state, settings, notify, findTask, isOpen, applySettings, STATUSES, STATUS_LABEL, PRIO_LABEL } from '../store';
import { animate, plural, phoneMQ } from './format';
import { api, taskPath, withForce, moveBody, mutate, invalidate } from './api';
import { toast, announce } from './toast';
import { ask } from './dialogs';
import { visibleTasks, anyFilter } from './filters';
import { loadDetail, closeDetail, openDetail } from './detail';

const updatedAt = (t) => t.updatedAt || t.movedAt || t.createdAt || '';
export function sortTasks(list) {
  if (settings.sort === 'prio') return list.slice().sort((a, b) => (a.prio || 3) - (b.prio || 3) || a.position - b.position);
  if (settings.sort === 'due') return list.slice().sort((a, b) => (a.due ? 1 : 2) - (b.due ? 1 : 2) || (a.due || '').localeCompare(b.due || '') || a.position - b.position);
  if (settings.sort === 'created') return list.slice().sort((a, b) => (b.createdAt || '').localeCompare(a.createdAt || '') || a.position - b.position);
  if (settings.sort === 'updated') return list.slice().sort((a, b) => updatedAt(b).localeCompare(updatedAt(a)) || a.position - b.position);
  return list;
}
export function groupTasks(list = state.tasks) {
  const groups = { todo: [], doing: [], done: [], cancelled: [] };
  for (const t of list) (groups[t.status] || groups.todo).push(t);
  return groups;
}
// One entry per status, in board order: the sorted visible cards and the
// column's hidden, collapsed and WIP state.
export function boardLayout() {
  const groups = groupTasks(visibleTasks());
  const filtered = anyFilter();
  return STATUSES.map((status) => {
    const tasks = sortTasks(groups[status]);
    const limit = Number(settings.wip[status]) || 0;
    return {
      status, tasks, limit,
      over: limit > 0 && tasks.length > limit,
      hidden: (status === 'cancelled' && !settings.showCancelled) || (settings.hideEmpty && tasks.length === 0 && !filtered),
      collapsed: !!settings.collapsed[status] && !phoneMQ.matches,
    };
  });
}
export const columnTasks = (status) => boardLayout().find((c) => c.status === status).tasks;
export const visibleStatuses = () => boardLayout().filter((c) => !c.hidden).map((c) => c.status);
const navStatuses = () => boardLayout().filter((c) => !c.hidden && !c.collapsed).map((c) => c.status);
export const cardNode = (id) => document.querySelector(`#board .card[data-id="${CSS.escape(id)}"]`);
export function orderedSelection() {
  const out = [];
  for (const col of boardLayout()) for (const t of col.tasks) if (state.selected.has(t.id)) out.push(t.id);
  return out;
}
export function toggleCollapse(status) {
  settings.collapsed[status] = !settings.collapsed[status];
  applySettings();
}
// One beat of the DONE column's own hue.
export function celebrateShip() {
  state.shipToken++;
  notify();
}

/* ============================== composer ============================== */
export function openComposer(status) {
  if (settings.collapsed[status]) toggleCollapse(status);
  state.composer = status;
  notify();
}
export function closeComposer() {
  if (state.composer === null) return;
  state.composer = null;
  notify();
}
export function addTask(saved) {
  state.tasks = [...state.tasks, saved];
  notify();
}

/* ============================== drag and drop ============================== */
function startDrag(ids, height) {
  state.dragging = { ids, height };
  state.dropTarget = null;
  state.dragPos = null;
  document.body.classList.add('select-none');
  cancelAnimationFrame(state.autoScrollRAF);
  state.autoScrollRAF = requestAnimationFrame(autoScrollTick);
  // The render marks the cards in hand inert. Doing that inside dragstart
  // makes Chromium abandon the drag, so it waits for the event to finish.
  setTimeout(notify, 0);
}
export function onDragStart(e, task) {
  const card = e.currentTarget;
  if (!task || state.lifted) { e.preventDefault(); return; }
  const ids = state.selected.has(task.id) && state.selected.size > 1 ? orderedSelection() : [task.id];
  e.dataTransfer.effectAllowed = 'move';
  e.dataTransfer.setData('text/plain', ids.map((id) => '#' + findTask(id).seq).join(' '));
  if (e.dataTransfer.setDragImage) {
    const ghost = card.cloneNode(true);
    ghost.classList.add('ghost');
    if (ids.length > 1) { ghost.classList.add('stack'); ghost.dataset.count = ids.length; }
    ghost.style.width = card.offsetWidth + 'px';
    document.body.append(ghost);
    const r = card.getBoundingClientRect();
    e.dataTransfer.setDragImage(ghost, e.clientX - r.left, e.clientY - r.top);
    setTimeout(() => ghost.remove(), 0);
  }
  startDrag(ids, card.offsetHeight);
}
// The slot goes before the first card (not in hand) whose midpoint is below y.
function slotIndexAt(body, y) {
  let index = 0;
  for (const c of body.querySelectorAll('.card[data-id]:not(.is-dragging)')) {
    const r = c.getBoundingClientRect();
    if (y < r.top + r.height / 2) return index;
    index++;
  }
  return index;
}
// status null means the pointer left every column.
export function hoverColumn(status, x, y, colEl) {
  if (!state.dragging) return;
  const prev = state.dropTarget;
  if (!status) {
    state.dragPos = null;
    if (prev) { state.dropTarget = null; notify(); }
    return;
  }
  const col = boardLayout().find((c) => c.status === status);
  const body = colEl && colEl.querySelector('.col-body');
  const index = col && !col.collapsed && body ? slotIndexAt(body, y) : null;
  state.dragPos = { x, y, status };
  if (!prev || prev.status !== status || prev.index !== index) { state.dropTarget = { status, index }; notify(); }
}
export function onDragOver(e, status) {
  if (!state.dragging) return;
  e.preventDefault();
  e.dataTransfer.dropEffect = 'move';
  hoverColumn(status, e.clientX, e.clientY, e.currentTarget);
}
export function onDragLeave(e, status) {
  if (e.currentTarget.contains(e.relatedTarget)) return;
  if (state.dropTarget && state.dropTarget.status === status) { state.dropTarget = null; notify(); }
}
export function onDrop(e, status) {
  if (!state.dragging) return;
  e.preventDefault();
  dropOn(status);
}
export function dropOn(status) {
  const manual = settings.sort === 'position';
  const target = state.dropTarget;
  const index = manual && target && target.status === status && target.index !== null ? target.index : undefined;
  const ids = state.dragging.ids;
  const same = ids.every((id) => { const t = findTask(id); return t && t.status === status; });
  cleanupDrag(true);
  if (!manual && same) { invalidate(); return; } // nothing to reorder: the sort decides the order
  moveMany(ids, status, index);
}
// dropped is true when a move follows: moveMany renders the new order itself and
// refreshes once the server has it, so a refresh here would only fetch the old
// order and animate the card back before the move lands. A cancelled drag still
// refreshes, because renders were held while the card was in hand.
export function cleanupDrag(dropped) {
  const was = !!state.dragging && dropped !== true;
  state.dragging = null;
  state.dropTarget = null;
  state.dragPos = null;
  document.body.classList.remove('select-none');
  cancelAnimationFrame(state.autoScrollRAF);
  notify();
  if (was) invalidate();
}
// Scrolls the column body near its top/bottom edge and the board track near its left/right edge while dragging.
function autoScrollTick() {
  if (!state.dragging && !state.touch) return;
  const p = state.dragPos;
  if (p) {
    const edge = 48, step = 10;
    const body = document.querySelector(`#board .col[data-status="${p.status}"] .col-body`);
    if (body) {
      const r = body.getBoundingClientRect();
      if (p.y < r.top + edge) body.scrollTop -= step; else if (p.y > r.bottom - edge) body.scrollTop += step;
    }
    const board = document.getElementById('board');
    if (board && board.scrollWidth > board.clientWidth) {
      const b = board.getBoundingClientRect();
      if (p.x < b.left + edge) board.scrollLeft -= step; else if (p.x > b.right - edge) board.scrollLeft += step;
    }
  }
  state.autoScrollRAF = requestAnimationFrame(autoScrollTick);
}
// Touch: long-press (250 ms) lifts the card, pointer moves carry a floating ghost, release drops.
export function onCardPointerDown(e, task) {
  if (e.pointerType !== 'touch' || state.lifted) return;
  const card = e.currentTarget;
  const start = { x: e.clientX, y: e.clientY };
  let timer = setTimeout(() => { timer = 0; beginTouchDrag(card, task, e.pointerId, start); }, 250);
  const cancel = () => { if (timer) clearTimeout(timer); timer = 0; card.removeEventListener('pointermove', onMove); card.removeEventListener('pointerup', cancel); card.removeEventListener('pointercancel', cancel); };
  const onMove = (ev) => { if (timer && Math.hypot(ev.clientX - start.x, ev.clientY - start.y) > 8) cancel(); };
  card.addEventListener('pointermove', onMove);
  card.addEventListener('pointerup', cancel);
  card.addEventListener('pointercancel', cancel);
}
function beginTouchDrag(card, task, pointerId, start) {
  if (!findTask(task.id)) return;
  const ids = state.selected.has(task.id) && state.selected.size > 1 ? orderedSelection() : [task.id];
  const r = card.getBoundingClientRect();
  const ghost = card.cloneNode(true);
  ghost.classList.add('touch-ghost');
  if (ids.length > 1) { ghost.classList.add('stack'); ghost.dataset.count = ids.length; }
  ghost.style.width = r.width + 'px';
  ghost.style.left = r.left + 'px';
  ghost.style.top = r.top + 'px';
  document.body.append(ghost);
  animate(ghost, [{ transform: 'scale(1) rotate(0)' }, { transform: 'scale(1.03) rotate(2deg)' }], 150, { fill: 'forwards' });
  if (navigator.vibrate) navigator.vibrate(10);
  state.touch = { ghost, dx: start.x - r.left, dy: start.y - r.top, pointerId, status: null };
  startDrag(ids, r.height);
  try { card.setPointerCapture(pointerId); } catch (err) { /* ignore */ }
  const move = (ev) => {
    if (!state.touch) return;
    ghost.style.left = ev.clientX - state.touch.dx + 'px';
    ghost.style.top = ev.clientY - state.touch.dy + 'px';
    const under = document.elementFromPoint(ev.clientX, ev.clientY);
    const col = under && under.closest ? under.closest('.col') : null;
    state.touch.status = col ? col.dataset.status : null;
    hoverColumn(state.touch.status, ev.clientX, ev.clientY, col);
  };
  const end = (ev) => {
    card.removeEventListener('pointermove', move);
    card.removeEventListener('pointerup', end);
    card.removeEventListener('pointercancel', end);
    const status = ev.type === 'pointerup' ? state.touch && state.touch.status : null;
    ghost.remove();
    state.touch = null;
    if (status && state.dragging) dropOn(status); else cleanupDrag();
  };
  card.addEventListener('pointermove', move);
  card.addEventListener('pointerup', end);
  card.addEventListener('pointercancel', end);
}

// Local reorder without the API: shared by drop, keyboard lift and undo.
export function localMove(ids, status, index) {
  const groups = groupTasks();
  const moving = ids.map(findTask).filter(Boolean);
  for (const t of moving) { const list = groups[t.status]; list.splice(list.indexOf(t), 1); t.status = status; }
  const target = groups[status];
  const at = index === undefined ? target.length : Math.min(index, target.length);
  target.splice(at, 0, ...moving);
  state.tasks = STATUSES.flatMap((s) => groups[s]);
}
// Moves ids (in order) to status at index, sequentially, with an Undo toast. index undefined = append.
export async function moveMany(ids, status, index) {
  const groups = groupTasks();
  const prev = ids.map((id) => { const t = findTask(id); return t && { id, status: t.status, index: groups[t.status].indexOf(t) }; }).filter(Boolean);
  if (!prev.length) return;
  if (prev.length === 1 && prev[0].status === status && (index === undefined || prev[0].index === index)) return;
  localMove(ids, status, index);
  // The user just put these cards down at the target: they must appear there,
  // not slide over from where they were picked up. The board clears the set
  // once it has painted them.
  state.dropped = new Set(ids);
  notify();
  let moved = 0;
  state.pendingMoves = (state.pendingMoves || 0) + 1;
  try {
    for (let i = 0; i < ids.length; i++) {
      try {
        const out = await withForce((force) => api('POST', taskPath(ids[i]) + '/move', moveBody(status, index === undefined ? undefined : index + i, force)), findTask(ids[i]));
        if (out === undefined) break;
        moved++;
      } catch (err) { toast(err.message, 'error'); break; }
    }
  } finally { state.pendingMoves--; }
  invalidate();
  if (!moved) return;
  if (status === 'done') celebrateShip();
  const label = moved === 1 ? `#${findTask(ids[0]).seq}` : plural(moved, 'task');
  announce(`Moved ${label} to ${STATUS_LABEL[status]}`);
  const undo = async () => {
    state.pendingMoves = (state.pendingMoves || 0) + 1;
    try {
      for (const p of prev.slice(0, moved).sort((a, b) => a.index - b.index)) {
        try { await withForce((force) => api('POST', taskPath(p.id) + '/move', moveBody(p.status, p.index, force))); } catch (err) { toast(err.message, 'error'); break; }
      }
    } finally { state.pendingMoves--; }
    invalidate();
  };
  toast(`Moved ${label} to ${STATUS_LABEL[status]}`, 'ok', { action: { label: 'Undo', run: undo }, life: 6000 });
}

/* ============================== keyboard: focus, lift, selection ============================== */
let focusSeq = 0;
// Marks id as the focused card; the card scrolls into view and, unless
// opts.focus is false, takes keyboard focus once painted.
export function setFocus(id, opts = {}) {
  state.focusId = id;
  state.focusRequest = { n: ++focusSeq, focus: opts.focus !== false };
  notify();
}
export const focusedTask = () => (state.focusId ? findTask(state.focusId) : null);
export function moveFocus(dRow, dCol) {
  const statuses = navStatuses();
  if (!statuses.length) return;
  const cur = focusedTask();
  if (!cur) { const first = statuses.map(columnTasks).find((l) => l.length); if (first) setFocus(first[0].id); return; }
  const list = columnTasks(cur.status);
  const idx = list.findIndex((t) => t.id === cur.id);
  if (dCol) {
    let ci = statuses.indexOf(cur.status);
    for (let step = 0; step < statuses.length; step++) {
      ci = (ci + dCol + statuses.length) % statuses.length;
      const other = columnTasks(statuses[ci]);
      if (other.length) { setFocus(other[Math.min(Math.max(idx, 0), other.length - 1)].id); return; }
    }
    return;
  }
  const next = list[Math.max(0, Math.min(list.length - 1, idx + dRow))];
  if (next) setFocus(next.id);
}
export function toggleLift() {
  const t = focusedTask();
  if (!t) return;
  if (state.lifted === t.id) { dropLifted(); return; }
  if (state.lifted) return;
  state.lifted = t.id;
  state.liftOrigin = { status: t.status, index: groupTasks()[t.status].indexOf(t) };
  notify();
  announce(`Lifted #${t.seq}. Move with h, j, k, l; Space drops; Escape cancels.`);
}
export function moveLifted(dRow, dCol) {
  const t = findTask(state.lifted);
  if (!t) return;
  const groups = groupTasks();
  let status = t.status, index = groups[status].indexOf(t);
  if (dCol) {
    const statuses = navStatuses();
    status = statuses[(statuses.indexOf(status) + dCol + statuses.length) % statuses.length];
    index = Math.min(index, groups[status].length);
  } else index = Math.max(0, Math.min(groups[status].length - 1, index + dRow));
  localMove([t.id], status, index);
  setFocus(t.id);
}
export async function dropLifted() {
  const id = state.lifted;
  const t = findTask(id);
  const origin = state.liftOrigin;
  state.lifted = null;
  notify();
  if (!t) return;
  const index = groupTasks()[t.status].indexOf(t);
  if (origin && origin.status === t.status && origin.index === index) return;
  const out = await mutate(() => withForce((force) => api('POST', taskPath(id) + '/move', moveBody(t.status, index, force)), t), `Moved #${t.seq} to ${STATUS_LABEL[t.status]}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(id) + '/move', moveBody(origin.status, origin.index, force)), t)));
  if (out && t.status === 'done') celebrateShip();
  announce(out ? `Moved #${t.seq} to ${STATUS_LABEL[t.status]}` : 'Move cancelled');
  setFocus(id);
}
export function cancelLift() {
  const id = state.lifted;
  state.lifted = null;
  notify();
  invalidate();
  if (id) setFocus(id);
}
export function onCardClick(e, t) {
  if (e.target.closest('a[href]')) return; // markdown links inside the card open in a new tab
  if (e.shiftKey || e.ctrlKey || e.metaKey) {
    e.preventDefault();
    if (e.shiftKey && state.anchorId) {
      const anchor = findTask(state.anchorId);
      const list = anchor && anchor.status === t.status ? columnTasks(t.status) : [];
      const a = list.findIndex((c) => c.id === anchor.id), b = list.findIndex((c) => c.id === t.id);
      if (a >= 0 && b >= 0) for (const c of list.slice(Math.min(a, b), Math.max(a, b) + 1)) state.selected.add(c.id);
      else state.selected.add(t.id);
    } else {
      if (state.selected.has(t.id)) state.selected.delete(t.id); else state.selected.add(t.id);
      state.anchorId = t.id;
    }
    setFocus(t.id);
    return;
  }
  setFocus(t.id, { focus: false });
  openDetail(t.seq);
}
export function toggleSelect(id) {
  if (state.selected.has(id)) state.selected.delete(id); else { state.selected.add(id); state.anchorId = id; }
  notify();
}
export function selectColumn() {
  const t = focusedTask();
  const status = t ? t.status : visibleStatuses()[0];
  for (const c of columnTasks(status)) state.selected.add(c.id);
  notify();
  announce(`Selected ${plural(state.selected.size, 'task')}`);
}
export function clearSelection() {
  if (!state.selected.size) return;
  state.selected.clear();
  notify();
}
// Drops stale focus and selection after a refresh removed their cards.
export function pruneSelection() {
  if (state.focusId && !state.tasks.some((t) => t.id === state.focusId)) state.focusId = null;
  for (const id of state.selected) if (!state.tasks.some((t) => t.id === id)) state.selected.delete(id);
}

/* ============================== bulk actions ============================== */
export async function bulkPatch(ids, patchFor, label) {
  let n = 0;
  for (const id of ids) {
    const t = findTask(id);
    if (!t) continue;
    try { await api('PATCH', taskPath(id), patchFor(t)); n++; } catch (err) { toast(err.message, 'error'); break; }
  }
  if (n) toast(`${label} on ${plural(n, 'task')}`, 'ok');
  clearSelection();
  invalidate();
}
export async function bulkCancel(ids) {
  const answer = await ask({
    title: `Cancel ${plural(ids.length, 'task')}?`,
    note: 'The cards move to Cancelled. You can restore them afterwards.',
    actions: [{ value: null, label: 'Keep cards' }, { value: 'cancel', label: 'Cancel tasks', variant: 'destructive' }],
  });
  if (!answer || !answer.value) return;
  const done = [];
  for (const id of ids) {
    try { await api('POST', taskPath(id) + '/cancel', {}); done.push(id); } catch (err) { toast(err.message, 'error'); break; }
  }
  clearSelection();
  invalidate();
  if (done.length) toast(`Cancelled ${plural(done.length, 'task')}`, 'ok', { life: 6000, action: { label: 'Undo', run: async () => { for (const id of done) { try { await api('POST', taskPath(id) + '/restore'); } catch (err) { toast(err.message, 'error'); break; } } invalidate(); } } });
}
export async function setPriority(t, prio) {
  if (t.prio === prio) return;
  await mutate(() => api('PATCH', taskPath(t.id), { prio }), `#${t.seq} priority ${PRIO_LABEL[prio]}`);
  if (state.detail) loadDetail(state.detail, true);
}

/* ============================== task verbs ============================== */
export async function shipTask(task) {
  const groups = groupTasks();
  const prev = { status: task.status, index: groups[task.status].indexOf(findTask(task.id)) };
  const out = await mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody('done', undefined, force)), task), `Shipped #${task.seq} ${task.title}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody(prev.status, prev.index, force)), task)));
  if (out) { celebrateShip(); announce(`Moved #${task.seq} to Done`); if (state.detail) loadDetail(state.detail, true); }
}
export async function cancelTask(task) {
  const answer = await ask({
    title: `Cancel #${task.seq}?`,
    message: task.title,
    note: 'The card moves to Cancelled. The reason is optional and is kept with the card.',
    field: { label: 'Reason', max: 500, placeholder: 'duplicate of #1' },
    actions: [{ value: null, label: 'Keep card' }, { value: 'cancel', label: 'Cancel card', variant: 'destructive' }],
  });
  if (!answer || !answer.value) return;
  const reason = answer.text.trim();
  const groups = groupTasks();
  const prev = { status: task.status, index: groups[task.status].indexOf(findTask(task.id)) };
  const out = await mutate(() => api('POST', taskPath(task.id) + '/cancel', reason ? { reason } : {}), `Cancelled #${task.seq}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody(prev.status, prev.index, force)), task)));
  if (out) { announce(`Cancelled #${task.seq}`); if (state.detail) loadDetail(state.detail, true); }
}
export async function restoreTask(task) {
  const out = await mutate(() => api('POST', taskPath(task.id) + '/restore'), `Restored #${task.seq}`,
    () => mutate(() => api('POST', taskPath(task.id) + '/cancel', {})));
  if (out) { announce(`Restored #${task.seq} to Todo`); if (state.detail) loadDetail(state.detail, true); }
}
export async function deleteTask(task) {
  if (task.status !== 'cancelled') { toast('Cancel the card before deleting it permanently', 'error'); return; }
  const answer = await ask({
    title: `Delete “${task.title}” permanently?`,
    note: 'The card, its comments, links and cancellation reason are removed for good. This cannot be undone.',
    actions: [{ value: null, label: 'Keep card' }, { value: 'delete', label: 'Delete permanently', variant: 'destructive' }],
  });
  if (!answer || !answer.value) return;
  const out = await mutate(() => api('DELETE', taskPath(task.id)), `Deleted #${task.seq}`);
  if (out) closeDetail();
}
export const isOpenTask = isOpen;
