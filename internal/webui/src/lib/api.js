// JSON API client and live updates. Talks to /api/* (docs/web-api.md).
import { state, notify, findTask, ALL_PROJECTS } from '../store';
import { clean } from './format';
import { ask } from './dialogs';
import { toast } from './toast';
import { loadDetail } from './detail';

export async function api(method, path, body, opts = {}) {
  const init = { method, headers: { Accept: 'application/json' } };
  if (method !== 'GET') init.headers['Content-Type'] = 'application/json';
  if (body !== undefined) init.body = JSON.stringify(body);
  let timer = 0;
  if (opts.timeout) {
    const ac = new AbortController();
    init.signal = ac.signal;
    timer = setTimeout(() => ac.abort(), opts.timeout);
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch (err) {
    if (err && err.name === 'AbortError') throw new Error(`No answer after ${Math.round(opts.timeout / 1000)} seconds. The request was stopped.`);
    throw err;
  } finally { clearTimeout(timer); }
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch (e) { data = null; }
  if (!res.ok) {
    const err = new Error((data && data.error) || `${res.status} ${res.statusText}`);
    err.status = res.status;
    err.body = data;
    throw err;
  }
  return data;
}
export const taskPath = (id) => `/api/tasks/${encodeURIComponent(id)}`;
// Runs run(force). On a 409 completion guard, offers the TUI's three ways out — tick the open
// checks and retry, finish anyway, or back out — and returns undefined if the user backs out.
// task is the card the guard refused, when the caller knows it: it decides whether ticking is on offer.
export async function withForce(run, task) {
  try {
    return await run(false);
  } catch (err) {
    if (!(err.status === 409 && err.body && err.body.completionBlocked)) throw err;
    const card = task && (findTask(task.id) || task);
    const checks = (card && card.checks) || [];
    const open = checks.filter((c) => !c.done).length;
    const choice = await ask({
      title: card ? `Move “${card.title}” to Done?` : 'Finish this card?',
      // The guard's message is CLI-shaped: drop the --force advice, and the card it names
      // when the dialog title already names it.
      message: err.message.replace(/;\s*re-run with --force.*$/i, '').replace(card ? /\s+on #\d+\s+".*"$/ : /$^/, '') + '.',
      actions: clean([
        { value: null, label: 'Cancel' },
        { value: 'force', label: 'Ship anyway', variant: open ? 'destructive' : 'default' },
        open ? { value: 'tick', label: 'Tick everything', variant: 'default' } : null,
      ]),
    });
    if (!choice || !choice.value) return undefined;
    if (choice.value === 'force') return run(true);
    const ticked = checks.map((c) => ({ text: c.text, done: true }));
    await api('PATCH', taskPath(card.id), { checks: ticked });
    return withForce(run, Object.assign({}, card, { checks: ticked }));
  }
}
export const moveBody = (status, index, force) => Object.assign({ status }, index === undefined ? {} : { index }, force ? { force: true } : {});
export const patchTask = (id, patch) => withForce((force) => api('PATCH', taskPath(id), force ? Object.assign({ force: true }, patch) : patch), findTask(id));
export async function setBlocked(task, on) {
  const out = await patchTask(task.id, { blocked: on });
  task.blocked = on;
  return out;
}

export function tasksURL(unfiltered) {
  const params = new URLSearchParams();
  if (state.project && state.project !== ALL_PROJECTS) params.set('project', state.project);
  if (!unfiltered) {
    if (state.q) params.set('q', state.q);
    for (const tag of state.tags) params.append('tag', tag);
  }
  const qs = params.toString();
  return '/api/tasks' + (qs ? '?' + qs : '');
}
export const serverFiltered = () => !!(state.q || state.tags.size);
// pendingMoves counts move requests in flight: a refresh that lands between the
// optimistic render and the server's answer would show the old order for a beat.
export const interacting = () => !!(state.dragging || state.touch || state.lifted || state.pendingMoves);

export async function refreshTasks() {
  if (interacting()) return false; // never clobber a card the user is holding
  const url = tasksURL();
  const headers = { Accept: 'application/json' };
  if (state.etag && state.etagURL === url) headers['If-None-Match'] = state.etag;
  try {
    const res = await fetch(url, { headers });
    if (res.status === 304) { setOnline(true); return false; }
    if (!res.ok) throw new Error(`${res.status}`);
    const data = await res.json();
    if (tasksURL() !== url || interacting()) return false; // filters changed mid-flight
    state.etag = res.headers.get('ETag');
    state.etagURL = url;
    state.tasks = data.tasks || [];
    state.loaded = true;
    setOnline(true);
    if (serverFiltered()) refreshAll(); else state.all = state.tasks;
    notify();
    if (state.detail) loadDetail(state.detail, true);
    refreshMeta();
    return true;
  } catch (err) {
    setOnline(false);
    return false;
  }
}
export async function refreshAll() {
  try {
    const data = await api('GET', tasksURL(true));
    state.all = data.tasks || [];
    notify();
  } catch (e) { /* stats stay stale */ }
}
export async function refreshMeta() {
  try {
    const meta = await api('GET', '/api/meta');
    state.meta = Object.assign({ projects: [], labels: [] }, meta);
    notify();
  } catch (err) { /* polling reports connectivity */ }
}
// The project list the CLI and the TUI agree on, with per-project counts.
export async function refreshProjects() {
  try {
    const out = await api('GET', '/api/projects');
    state.projects = out.projects || [];
    notify();
  } catch (err) { /* the header falls back to /api/meta */ }
}
// The TUI keyboard registry: one source for the palette and the help sheet.
export async function refreshActions() {
  try { state.actions = (await api('GET', '/api/actions')).actions || []; } catch (err) { state.actions = []; }
  notify();
}
export async function refreshAIStatus() {
  try { state.ai = await api('GET', '/api/ai/status'); } catch (err) { state.ai = { configured: false }; }
  notify();
  return state.ai;
}
export function invalidate() {
  state.etag = null;
  return refreshTasks();
}
/* Live updates. The board follows /api/events: the server pushes one `change` per board
   revision (its own writes included) and a `: ping` every 15s. Polling is the fallback for a
   browser without EventSource, or a stream that never opens. See docs/web-api.md. */
const LIVE = { debounce: 150, poll: 5000, open: 5000 };
// One refresh per burst: a drag that moves three cards is still one GET.
function scheduleRefresh() {
  if (state.refreshTimer) return;
  state.refreshTimer = setTimeout(() => {
    state.refreshTimer = 0;
    refreshTasks();
  }, LIVE.debounce);
}
function openStream() {
  if (state.mode === 'poll' || state.stream) return;
  if (typeof EventSource === 'undefined') { fallBackToPolling(); return; }
  const es = new EventSource('/api/events');
  state.stream = es;
  es.addEventListener('hello', () => {
    state.streamFails = 0;
    state.helloSeen = true;
    setOnline(true);
    scheduleRefresh();
  });
  es.addEventListener('change', scheduleRefresh);
  // EventSource reconnects by itself; one failure is a hiccup, two in a row is an outage.
  es.onerror = () => { if (++state.streamFails >= 2) setOnline(false); };
  // A stream the server refuses (no 2xx) never says hello, and EventSource gives up silently.
  if (!state.helloSeen) setTimeout(() => { if (!state.helloSeen) fallBackToPolling(); }, LIVE.open);
}
function closeStream() {
  if (!state.stream) return;
  state.stream.close();
  state.stream = null;
}
function fallBackToPolling() {
  if (state.mode === 'poll') return;
  state.mode = 'poll';
  closeStream();
  notify(); // the version tooltip names the mode
  schedulePoll();
}
function schedulePoll() {
  clearTimeout(state.pollTimer);
  if (state.mode !== 'poll') return;
  state.pollTimer = setTimeout(async () => {
    if (document.visibilityState === 'visible') await refreshTasks();
    schedulePoll();
  }, LIVE.poll);
}
// Opens (or reopens) whichever channel this page is on. Safe to call repeatedly.
export function startLive() {
  if (state.mode === 'poll') { schedulePoll(); return; }
  openStream();
}
// Mobile browsers keep a backgrounded page alive; nothing holds a socket open for it.
export function stopLive() {
  closeStream();
  clearTimeout(state.pollTimer);
  state.pollTimer = 0;
}
export function setOnline(online) {
  if (state.online === online) return;
  state.online = online;
  notify();
}
// Wraps a mutation: toasts errors, reconciles with the server afterwards. Returns the result or undefined.
export async function mutate(fn, okMessage, undo) {
  try {
    const out = await fn();
    if (out !== undefined && okMessage) toast(okMessage, 'ok', undo ? { action: { label: 'Undo', run: undo }, life: 6000 } : undefined);
    invalidate();
    return out;
  } catch (err) {
    toast(err.message || 'Request failed', 'error');
    invalidate();
    return undefined;
  }
}
