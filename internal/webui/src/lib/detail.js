// The task detail: loading, routing and the comment, link and drift verbs.
import { state, settings, notify, findTask } from '../store';
import { api, taskPath, setBlocked, invalidate } from './api';
import { toast, announce } from './toast';
import { openDialog, closeDialog, confirmDiscard } from './dialogs';
import { setFocus } from './board';

export function openDetail(ref, opts = {}) {
  const key = String(ref);
  const changed = state.detail !== key;
  state.detail = key;
  if (changed) {
    state.detailJSON = '';
    state.detailData = null;
  }
  if (opts.focusComment) state.detailFocusComment = true;
  openDialog('detail');
  notify();
  if (!opts.route && location.hash !== '#/t/' + key) location.hash = '#/t/' + key;
  loadDetail(key);
}
export async function closeDetail(opts = {}) {
  if (!state.detail) return;
  if (panelDirty() && !(await confirmDiscard('This card has edits that have not been saved.'))) return;
  if (!state.detail) return;
  const t = state.detailData && state.detailData.task;
  state.detail = null;
  state.detailJSON = '';
  state.detailData = null;
  state.detailFocusComment = false;
  closeDialog('detail');
  notify();
  if (!opts.route && /^#\/t\//.test(location.hash)) history.replaceState(null, '', location.pathname + location.search + '#/');
  if (t && findTask(t.id)) setFocus(t.id);
  else { const board = document.getElementById('board'); if (board) board.focus({ preventScroll: true }); }
}
// dirty: an inline editor in the panel holds unsaved changes (confirm before closing).
export const panelDirty = () => !!document.querySelector('#detail [data-busy]:not(.editor), #detail .editor[data-busy][data-dirty]');
// The card plus what only other endpoints know: why it was killed, and where it was imported from.
async function detailExtras(ref, task) {
  const links = (task.tags || []).filter((t) => t.startsWith('link::')).map((t) => t.slice(6)).filter(Boolean);
  const [tombstone, provenance, siblings] = await Promise.all([
    task.status === 'cancelled' ? api('GET', taskPath(ref) + '/tombstone').catch(() => null) : Promise.resolve(null),
    links.length ? Promise.all(links.map((link) => api('GET', '/api/forge/provenance?link=' + encodeURIComponent(link)).then((r) => r.links || []).catch(() => []))).then((all) => all.flat()) : Promise.resolve([]),
    // Other cards carrying the same upstream link: the TUI's "already imported?" lookup.
    links.length ? Promise.all(links.map((link) => api('GET', '/api/by-link?link=' + encodeURIComponent('link::' + link)).then((r) => (r.items || []).filter((i) => i.id !== task.id).map((i) => Object.assign({ link }, i))).catch(() => []))).then((all) => all.flat()) : Promise.resolve([]),
  ]);
  return { tombstone, provenance, siblings };
}
export async function loadDetail(ref, silent) {
  try {
    const data = await api('GET', taskPath(ref));
    if (state.detail !== String(ref)) return;
    Object.assign(data, await detailExtras(ref, data.task));
    if (state.detail !== String(ref)) return;
    const json = JSON.stringify(data);
    const count = (data.comments || []).length;
    if (state.commentCounts[data.task.id] !== count) { state.commentCounts[data.task.id] = count; if (settings.show.comments) notify(); }
    if (json === state.detailJSON) return;
    state.detailJSON = json;
    state.detailData = data;
    notify();
  } catch (err) {
    if (!silent) toast(err.message, 'error');
    if (err.status === 404) closeDetail();
  }
}
export const reloadDetail = () => { if (state.detail) loadDetail(state.detail, true); };
// Opens the comment composer in the detail view and focuses it.
export function focusComposer() {
  state.detailFocusComment = true;
  notify();
}
// Posts a comment; resolves true when it landed so the caller can close the composer.
export async function addComment(task, body) {
  const text = body.trim();
  if (!text) return false;
  try {
    await api('POST', taskPath(task.id) + '/comments', { body: text });
    announce('Comment added');
    return true;
  } catch (err) { toast(err.message, 'error'); return false; }
}
// Replaces a comment's body; resolves with the saved comment, or null on failure.
export async function updateComment(c, body) {
  const text = body.trim();
  if (!text) { toast('A comment needs some text', 'error'); return null; }
  try {
    const out = await api('PUT', `/api/comments/${encodeURIComponent(c.id)}`, { body: text });
    announce('Comment saved');
    return out;
  } catch (err) { toast(err.message, 'error'); return null; }
}
export async function deleteComment(task, c) {
  try {
    await api('DELETE', `/api/comments/${encodeURIComponent(c.id)}`);
    reloadDetail();
    toast('Comment deleted', 'ok', { life: 6000, action: { label: 'Undo', run: async () => { try { await api('POST', taskPath(task.id) + '/comments', { body: c.body }); reloadDetail(); } catch (err) { toast(err.message, 'error'); } } } });
  } catch (err) { toast(err.message, 'error'); }
}
export async function addLink(task, direction, number) {
  const other = String(number).trim().replace(/^#/, '');
  if (!/^\d+$/.test(other)) { toast('Enter a task number, like #12', 'error'); return; }
  const body = direction === 'blocks' ? { blocker: String(task.seq), blocked: other } : { blocker: other, blocked: String(task.seq) };
  try {
    await api('POST', '/api/links', body);
    // A new blocker flips the card's blocked flag, so the toggle and the chip follow the link.
    if (direction === 'blockedBy' && !task.blocked) await setBlocked(task, true);
    toast(`Linked #${task.seq} and #${other}`, 'ok');
    reloadDetail();
    invalidate();
  } catch (err) { toast(err.message, 'error'); }
}
export async function removeLink(task, other) {
  const links = (state.detailData && state.detailData.links) || {};
  const blocks = (links.blocks || []).some((t) => t.id === other.id);
  try {
    await api('DELETE', '/api/links', { a: task.id, b: other.id });
    // The last blocker gone clears the flag it set.
    const left = (links.blockedBy || []).filter((t) => t.id !== other.id);
    if (!blocks && task.blocked && !left.length) await setBlocked(task, false);
    reloadDetail();
    invalidate();
    const relink = blocks ? { blocker: task.id, blocked: other.id } : { blocker: other.id, blocked: task.id };
    toast(`Unlinked #${other.seq}`, 'ok', { life: 6000, action: { label: 'Undo', run: async () => { try { await api('POST', '/api/links', relink); reloadDetail(); invalidate(); } catch (err) { toast(err.message, 'error'); } } } });
  } catch (err) { toast(err.message, 'error'); }
}

/* ============================== provenance and upstream drift ============================== */
export const DRIFT_STATE = { unchanged: 'unchanged', drifted: 'drifted', baseline_recorded: 'baseline recorded' };
export async function checkDrift(link) {
  try {
    state.drift[link.externalKey] = await api('POST', '/api/forge/drift/check', { source: link.source, externalKey: link.externalKey }, { timeout: 60000 });
    announce(`Upstream ${link.link} is ${DRIFT_STATE[state.drift[link.externalKey].state] || 'checked'}`);
  } catch (err) { toast(err.message, 'error'); } finally { notify(); }
}
export async function acceptDrift(link, drift) {
  try {
    await api('POST', '/api/forge/drift/accept', { source: link.source, externalKey: link.externalKey, revision: drift.revision });
    delete state.drift[link.externalKey];
    toast('Upstream baseline updated', 'ok');
    await loadDetail(state.detail, true);
  } catch (err) {
    toast(err.status === 409 ? 'Upstream changed again. Check it before updating the card.' : err.message, 'error');
  } finally { notify(); }
}
