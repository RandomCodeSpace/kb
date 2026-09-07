/* kb web frontend. Plain ES2020, no modules, no dependencies. Talks to /api/* (docs/web-api.md).
   Sections: utilities · settings · state · api/polling · markdown · filters · render (header, board, cards)
   · drag and drop (mouse, touch, autoscroll) · keyboard, lift, selection · detail · edit · composer
   · palette · display options · toasts/undo · routing · handlers · init */
'use strict';

/* ============================== utilities ============================== */
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const STATUSES = ['todo', 'doing', 'done', 'cancelled'];
const STATUS_LABEL = { todo: 'Todo', doing: 'Doing', done: 'Done', cancelled: 'Cancelled' };
const PRIO_LABEL = { 1: 'High', 2: 'Medium', 3: 'Low' };
const EASE = 'cubic-bezier(0.2, 0, 0, 1)';
const SVG_NS = 'http://www.w3.org/2000/svg';
const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
const wideMQ = matchMedia('(min-width: 1024px)');
const phoneMQ = matchMedia('(max-width: 767px)');
const discreteTransitions = typeof CSS !== 'undefined' && CSS.supports && CSS.supports('transition-behavior', 'allow-discrete');
const dur = (ms) => (reducedMotion.matches ? 0 : ms);
const isMac = /Mac|iPhone|iPad/.test(navigator.platform || '');

// el('div', {class: 'x', onclick: fn, 'data-id': id}, 'text', node, null) -> element. Strings become text nodes.
function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  setAttrs(node, attrs);
  appendChildren(node, children);
  return node;
}
function svg(tag, attrs, ...children) {
  const node = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v !== null && v !== undefined && v !== false) node.setAttribute(k, v);
  appendChildren(node, children);
  return node;
}
function setAttrs(node, attrs) {
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === null || value === undefined || value === false) continue;
    if (key.startsWith('on') && typeof value === 'function') node.addEventListener(key.slice(2), value);
    else if (key === 'class') node.className = value;
    else if (key === 'style') node.style.cssText = value;
    else if (key === 'checked' || key === 'disabled' || key === 'value' || key === 'selected') node[key] = value;
    else node.setAttribute(key, value === true ? '' : value);
  }
}
function appendChildren(node, children) {
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
}
const clean = (list) => list.filter(Boolean);

function animate(node, frames, ms, extra) {
  if (!node.animate) return { finished: Promise.resolve(), onfinish: null };
  return node.animate(frames, Object.assign({ duration: dur(ms), easing: EASE, fill: 'none' }, extra || {}));
}
const wait = (ms) => new Promise((r) => setTimeout(r, ms));

function localToday() {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), now.getDate());
}
function isoDate(d) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
function dueDays(due) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(due || '');
  if (!m) return null;
  return Math.round((new Date(+m[1], +m[2] - 1, +m[3]) - localToday()) / 86400000);
}
// Relative due label; the board's dueChip plus the "overdue" reading the panel uses.
function dueChip(due) {
  const days = dueDays(due);
  if (days === null) return { label: due, tone: '' };
  if (days === 0) return { label: 'today', tone: 'soon' };
  if (days === 1) return { label: 'tomorrow', tone: 'soon' };
  if (days > 1) return { label: `in ${days}d`, tone: days <= 3 ? 'soon' : '' };
  return { label: `${-days}d overdue`, tone: 'overdue' };
}
function fmtDate(iso) {
  const d = new Date(iso);
  return isNaN(d) ? iso : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}
function relTime(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return iso;
  const s = Math.round((Date.now() - d) / 1000);
  if (s < 45) return 'just now';
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  const days = Math.round(h / 24);
  if (days < 30) return `${days}d ago`;
  return d.toLocaleDateString(undefined, { dateStyle: 'medium' });
}
const isProjectTag = (tag) => tag.startsWith('project::');
const userTags = (t) => (t.tags || []).filter((tag) => !isProjectTag(tag));
const isOpen = (t) => t.status === 'todo' || t.status === 'doing';
function labelHue(name) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return (h % 12) * 30;
}
// Subsequence fuzzy match: returns {score, positions} or null.
function fuzzy(query, text) {
  const q = query.toLowerCase(), t = text.toLowerCase();
  if (!q) return { score: 0, positions: [] };
  const idx = t.indexOf(q);
  if (idx >= 0) return { score: 100 - idx + (idx === 0 || /\W/.test(t[idx - 1]) ? 20 : 0), positions: Array.from({ length: q.length }, (_, i) => idx + i) };
  const positions = [];
  let ti = 0, score = 0, streak = 0;
  for (let qi = 0; qi < q.length; qi++) {
    const found = t.indexOf(q[qi], ti);
    if (found < 0) return null;
    streak = found === ti && qi > 0 ? streak + 1 : 0;
    score += 1 + streak * 2 + (found === 0 || /\W/.test(t[found - 1]) ? 3 : 0);
    positions.push(found);
    ti = found + 1;
  }
  return { score: score - t.length / 50, positions };
}

/* ============================== settings ============================== */
const SETTINGS_KEY = 'kb-web-settings';
function defaultSettings() {
  return {
    theme: 'system', density: 'comfortable',
    show: { seq: true, emoji: true, tags: true, due: true, effort: true, checks: true, comments: true },
    hideEmpty: false, showCancelled: true, wip: {}, sort: 'position', collapsed: {}, panelWidth: 520,
  };
}
function loadSettings() {
  const base = defaultSettings();
  try {
    const raw = JSON.parse(localStorage.getItem(SETTINGS_KEY) || '{}');
    for (const k of Object.keys(base)) {
      if (raw[k] === undefined) continue;
      if (typeof base[k] === 'object' && base[k] !== null) Object.assign(base[k], raw[k] || {});
      else base[k] = raw[k];
    }
  } catch (e) { /* storage unavailable or corrupt: defaults */ }
  return base;
}
const settings = loadSettings();
function saveSettings() {
  try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)); } catch (e) { /* ignore */ }
}
function applySettings(rerender = true) {
  const root = document.documentElement;
  if (settings.theme === 'light' || settings.theme === 'dark') root.dataset.theme = settings.theme; else delete root.dataset.theme;
  if (settings.density === 'compact') root.dataset.density = 'compact'; else delete root.dataset.density;
  root.style.setProperty('--panel-w', Math.round(settings.panelWidth) + 'px');
  saveSettings();
  if (rerender && state.loaded) render();
}
const currentTheme = () => (settings.theme === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : settings.theme);

/* ============================== state ============================== */
const state = {
  meta: { version: '', activeProject: '', projects: [], labels: [] },
  project: '', q: '', tokens: [], tags: new Set(), quick: new Set(),
  tasks: [], all: [], loaded: false, etag: null, etagURL: '', online: true, firstPaint: true,
  dragging: null, touch: null, dragPos: null, autoScrollRAF: 0,
  focusId: null, lifted: null, liftOrigin: null, selected: new Set(), anchorId: null, routePushed: false,
  detail: null, detailJSON: '', detailData: null, commentCounts: {},
  editing: null, editSnapshot: '', pollTimer: 0, paletteIndex: 0, paletteItems: [],
};
const dom = {
  board: $('#board'), panel: $('#panel'), panelHandle: $('#panel-handle'), detail: $('#detail'),
  labels: $('#labels'), quick: $('#quick'), active: $('#active'), resultCount: $('#result-count'), stats: $('#stats'),
  search: $('#search'), project: $('#project'), clear: $('#clear-filters'), banner: $('#banner'), conn: $('#conn'), version: $('#version'),
  toasts: $('#toasts'), live: $('#live'), segments: $('#segments'), filters: $('#filters'), scrim: $('#filters-scrim'), filterCount: $('#filter-count'),
  bulkbar: $('#bulkbar'), settings: $('#settings'),
  editDialog: $('#edit-dialog'), editForm: $('#edit-form'), editTitle: $('#edit-title'),
  detailDialog: $('#detail-dialog'), detailTitle: $('#detail-title'), detailBody: $('#detail-body'), detailActions: $('#detail-actions'),
  helpDialog: $('#help-dialog'), palette: $('#palette'), paletteInput: $('#palette-search'), paletteList: $('#palette-list'),
};
const cols = {}; // status -> {col, body, count, composer}
const dropSlot = el('div', { class: 'drop-slot', 'aria-hidden': 'true' });

/* ============================== api / polling ============================== */
async function api(method, path, body) {
  const init = { method, headers: { Accept: 'application/json' } };
  if (method !== 'GET') init.headers['Content-Type'] = 'application/json';
  if (body !== undefined) init.body = JSON.stringify(body);
  const res = await fetch(path, init);
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
const taskPath = (id) => `/api/tasks/${encodeURIComponent(id)}`;

// Runs run(force). On a 409 completion guard, asks and retries with force. Returns undefined if the user declines.
async function withForce(run) {
  try {
    return await run(false);
  } catch (err) {
    if (err.status === 409 && err.body && err.body.completionBlocked) {
      if (!confirm(`${err.message}\n\nFinish anyway?`)) return undefined;
      return run(true);
    }
    throw err;
  }
}
const moveBody = (status, index, force) => Object.assign({ status }, index === undefined ? {} : { index }, force ? { force: true } : {});

function tasksURL(unfiltered) {
  const params = new URLSearchParams();
  if (state.project) params.set('project', state.project);
  if (!unfiltered) {
    if (state.q) params.set('q', state.q);
    for (const tag of state.tags) params.append('tag', tag);
  }
  const qs = params.toString();
  return '/api/tasks' + (qs ? '?' + qs : '');
}
const serverFiltered = () => !!(state.q || state.tags.size);
const interacting = () => !!(state.dragging || state.touch || state.lifted);

async function refreshTasks() {
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
    if (serverFiltered()) refreshAll(); else { state.all = state.tasks; }
    render();
    if (state.detail) loadDetail(state.detail, true);
    refreshMeta();
    return true;
  } catch (err) {
    setOnline(false);
    return false;
  }
}
async function refreshAll() {
  try {
    const data = await api('GET', tasksURL(true));
    state.all = data.tasks || [];
    renderStats();
    renderResultCount();
  } catch (e) { /* stats stay stale */ }
}
async function refreshMeta() {
  try {
    const meta = await api('GET', '/api/meta');
    state.meta = Object.assign({ projects: [], labels: [] }, meta);
    if (!state.project && meta.activeProject) state.project = meta.activeProject;
    if (!state.project && meta.projects && meta.projects.length) state.project = meta.projects[0];
    renderHeader();
    renderLabels();
  } catch (err) { /* polling reports connectivity */ }
}
function invalidate() {
  state.etag = null;
  return refreshTasks();
}
function schedulePoll() {
  clearTimeout(state.pollTimer);
  state.pollTimer = setTimeout(async () => {
    if (document.visibilityState === 'visible') await refreshTasks();
    schedulePoll();
  }, 2000);
}
function setOnline(online) {
  if (state.online === online) return;
  state.online = online;
  dom.banner.hidden = online;
  dom.conn.classList.toggle('offline', !online);
  dom.conn.title = online ? 'Connected' : 'Connection lost';
}
// Wraps a mutation: toasts errors, reconciles with the server afterwards. Returns the result or undefined.
async function mutate(fn, okMessage, undo) {
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
const findTask = (id) => state.tasks.find((t) => t.id === id || String(t.seq) === String(id));

/* ============================== markdown ============================== */
const INLINE_RE = /(`[^`\n]+`)|(\*\*[^*\n]+\*\*)|((?<![\w])_[^_\n]+_(?![\w]))|\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g;
function mdInline(text) {
  const out = [];
  const INLINE = new RegExp(INLINE_RE.source, 'g'); // fresh per call: recursion below must not share lastIndex
  let last = 0, m;
  while ((m = INLINE.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index));
    if (m[1]) out.push(el('code', {}, m[1].slice(1, -1)));
    else if (m[2]) out.push(el('strong', {}, ...mdInline(m[2].slice(2, -2))));
    else if (m[3]) out.push(el('em', {}, ...mdInline(m[3].slice(1, -1))));
    else out.push(el('a', { href: m[5], target: '_blank', rel: 'noopener noreferrer' }, m[4]));
    last = INLINE.lastIndex;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}
// Safe minimal Markdown -> DOM. Everything is text nodes; no HTML passes through.
function renderMarkdown(src, emptyText = 'No description') {
  const root = el('div', { class: 'md' });
  if (!src || !src.trim()) return el('div', { class: 'md md-empty' }, emptyText);
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  let para = [], list = null, code = null;
  const flushPara = () => { if (para.length) { root.append(el('p', {}, ...mdInline(para.join(' ')))); para = []; } };
  const flushCode = () => { if (code) { root.append(el('pre', {}, el('code', {}, code.join('\n')))); code = null; } };
  for (const raw of lines) {
    if (code) { if (/^\s*```/.test(raw)) flushCode(); else code.push(raw); continue; }
    const line = raw.trimEnd();
    let m;
    if (/^\s*```/.test(line)) { flushPara(); list = null; code = []; continue; }
    if (!line.trim()) { flushPara(); list = null; continue; }
    if ((m = /^(#{1,3})\s+(.*)$/.exec(line))) { flushPara(); list = null; root.append(el('h' + m[1].length, {}, ...mdInline(m[2]))); continue; }
    if ((m = /^\s*[-*]\s+(?:\[([ xX])\]\s+)?(.*)$/.exec(line))) {
      flushPara();
      if (!list) { list = el('ul'); root.append(list); }
      if (m[1] !== undefined) {
        const done = m[1] !== ' ';
        list.append(el('li', { class: 'task' }, el('input', { type: 'checkbox', class: 'cb', disabled: true, checked: done, tabindex: '-1' }), el('span', { class: done ? 'done-text' : '' }, ...mdInline(m[2]))));
      } else list.append(el('li', {}, ...mdInline(m[2])));
      continue;
    }
    list = null;
    para.push(line.trim());
  }
  flushCode();
  flushPara();
  return root;
}

/* ============================== filters ============================== */
const QUICK = [
  { id: 'overdue', label: 'Overdue', test: (t) => isOpen(t) && dueDays(t.due) !== null && dueDays(t.due) < 0 },
  { id: 'week', label: 'Due this week', test: (t) => isOpen(t) && dueDays(t.due) !== null && dueDays(t.due) >= 0 && dueDays(t.due) <= 7 },
  { id: 'high', label: 'High priority', test: (t) => t.prio === 1 },
  { id: 'blocked', label: 'Blocked', test: (t) => !!t.blocked },
  { id: 'checklist', label: 'Open checklist', test: (t) => (t.checks || []).some((c) => !c.done) },
  { id: 'nolabels', label: 'No labels', test: (t) => userTags(t).length === 0 },
];
const quickById = (id) => QUICK.find((q) => q.id === id);
const PRIO_WORDS = { high: 1, medium: 2, med: 2, low: 3, 1: 1, 2: 2, 3: 3 };

// "tag:web prio:high due:week is:blocked has:checklist effort:M #12 free words" -> {q, tokens}
function parseSearch(text) {
  const tokens = [], words = [];
  for (const w of text.split(/\s+/)) {
    if (!w) continue;
    const m = /^(tag|prio|due|is|has|effort):(.+)$/i.exec(w);
    if (m) tokens.push({ key: m[1].toLowerCase(), value: m[2], raw: w });
    else if (/^#\d+$/.test(w)) tokens.push({ key: 'seq', value: w.slice(1), raw: w });
    else words.push(w);
  }
  return { q: words.join(' '), tokens };
}
function tokenTest(tok) {
  const v = tok.value.toLowerCase();
  switch (tok.key) {
    case 'tag': return (t) => (t.tags || []).some((x) => x.toLowerCase() === v);
    case 'prio': return PRIO_WORDS[v] ? (t) => t.prio === PRIO_WORDS[v] : null;
    case 'due':
      if (v === 'today') return (t) => dueDays(t.due) === 0;
      if (v === 'week') return quickById('week').test;
      if (v === 'overdue') return quickById('overdue').test;
      if (v === 'none') return (t) => !t.due;
      return null;
    case 'is': return v === 'blocked' ? (t) => !!t.blocked : v === 'open' ? isOpen : STATUSES.includes(v) ? (t) => t.status === v : null;
    case 'has': return v === 'checklist' ? (t) => (t.checks || []).length > 0 : v === 'due' ? (t) => !!t.due : null;
    case 'effort': return (t) => (t.effort || '').toLowerCase() === v;
    case 'seq': return (t) => String(t.seq) === tok.value;
    default: return null;
  }
}
function visibleTasks() {
  const tests = clean([...state.quick].map((id) => quickById(id) && quickById(id).test).concat(state.tokens.map(tokenTest)));
  return tests.length ? state.tasks.filter((t) => tests.every((fn) => fn(t))) : state.tasks;
}
const anyFilter = () => !!(state.q || state.tags.size || state.quick.size || state.tokens.length);

function applySearchText(text, push = true) {
  const { q, tokens } = parseSearch(text);
  const changed = q !== state.q;
  state.q = q;
  state.tokens = tokens;
  renderFilters();
  if (changed) invalidate(); else renderBoard();
  if (push && dom.search.value !== text) dom.search.value = text;
}
function removeToken(raw) {
  const rest = dom.search.value.split(/\s+/).filter((w) => w && w !== raw).join(' ');
  applySearchText(rest);
}
function toggleQuick(id) {
  if (state.quick.has(id)) state.quick.delete(id); else state.quick.add(id);
  renderFilters();
  renderBoard();
}
function toggleTag(tag) {
  if (state.tags.has(tag)) state.tags.delete(tag); else state.tags.add(tag);
  renderFilters();
  invalidate();
}
function clearFilters() {
  state.tags.clear();
  state.quick.clear();
  state.tokens = [];
  state.q = '';
  dom.search.value = '';
  renderFilters();
  invalidate();
}

/* ============================== render: header, filters ============================== */
function renderHeader() {
  const { meta } = state;
  dom.version.textContent = meta.version ? 'v' + String(meta.version).replace(/^v/, '') : '';
  const projects = Array.from(new Set([...(meta.projects || []), state.project].filter(Boolean)));
  const fill = (select, current) => { select.replaceChildren(...projects.map((p) => el('option', { value: p, selected: p === current }, p))); select.value = current; };
  fill(dom.project, state.project);
  if (!dom.editDialog.open) fill(dom.editForm.elements.project, state.project);
  renderStats();
}
function renderStats() {
  const all = state.all.length || !serverFiltered() ? state.all : state.tasks;
  const open = all.filter(isOpen).length;
  const week = all.filter(quickById('week').test).length;
  const blocked = all.filter((t) => isOpen(t) && t.blocked).length;
  const item = (n, label, quick) => quick
    ? el('button', { type: 'button', 'aria-pressed': state.quick.has(quick) ? 'true' : 'false', title: `Filter: ${quickById(quick).label}`, onclick: () => toggleQuick(quick) }, el('b', {}, n), ' ' + label)
    : el('span', {}, el('b', {}, n), ' ' + label);
  dom.stats.replaceChildren(item(open, 'open'), el('span', { class: 'sep' }, '·'), item(week, 'due this week', 'week'), el('span', { class: 'sep' }, '·'), item(blocked, 'blocked', 'blocked'));
}
function renderFilters() {
  renderLabels();
  dom.quick.replaceChildren(...QUICK.map((q) => el('button', { type: 'button', class: 'chip chip-btn quick-chip', 'aria-pressed': state.quick.has(q.id) ? 'true' : 'false', onclick: () => toggleQuick(q.id) }, el('span', {}, q.label))));
  const chips = [];
  const removable = (label, onRemove) => el('span', { class: 'chip removable' }, el('span', {}, label), el('button', { type: 'button', class: 'x', 'aria-label': `Remove filter ${label}`, onclick: onRemove }, '×'));
  if (state.q) chips.push(removable(`"${state.q}"`, () => applySearchText(state.tokens.map((t) => t.raw).join(' '))));
  for (const tok of state.tokens) chips.push(removable(tok.raw, () => removeToken(tok.raw)));
  for (const tag of state.tags) chips.push(removable(`label: ${tag}`, () => toggleTag(tag)));
  for (const id of state.quick) chips.push(removable(quickById(id).label, () => toggleQuick(id)));
  dom.active.replaceChildren(...chips);
  const active = chips.length;
  dom.clear.hidden = active === 0;
  dom.filterCount.hidden = active === 0;
  dom.filterCount.textContent = active;
  renderResultCount();
  renderStats();
}
function renderResultCount() {
  if (!state.loaded) { dom.resultCount.textContent = ''; return; }
  const shown = visibleTasks().length;
  const total = serverFiltered() && state.all.length ? state.all.length : (serverFiltered() ? null : state.tasks.length);
  dom.resultCount.textContent = anyFilter() ? (total === null ? `${shown} shown` : `${shown} of ${total}`) : `${shown} tasks`;
}
function renderLabels() {
  const labels = (state.meta.labels || []).filter((l) => !isProjectTag(l));
  for (const tag of state.tags) if (!labels.includes(tag)) labels.push(tag);
  dom.labels.replaceChildren(...labels.map((tag) => el('button', {
    type: 'button', class: 'chip chip-btn hued', style: `--h:${labelHue(tag)}`, 'aria-pressed': state.tags.has(tag) ? 'true' : 'false', onclick: () => toggleTag(tag),
  }, el('span', {}, tag))));
}

/* ============================== render: board ============================== */
function buildBoard() {
  for (const status of STATUSES) {
    const count = el('span', { class: 'count num' }, '');
    const body = el('div', { class: 'col-body', role: 'listbox', 'aria-multiselectable': 'true', 'aria-label': STATUS_LABEL[status] });
    for (let i = 0; i < 3; i++) body.append(el('div', { class: 'card skeleton', 'aria-hidden': 'true' }));
    const composer = el('div', { class: 'composer' });
    const col = el('section', { class: 'col', 'data-status': status, style: `--hue: var(--${status})` },
      el('div', { class: 'col-head' },
        el('h3', {}, STATUS_LABEL[status]), count, el('span', { class: 'grow' }),
        el('button', { type: 'button', class: 'btn sm icon ghost add-btn', 'aria-label': `Add task to ${STATUS_LABEL[status]}`, title: 'Add task', onclick: () => openComposer(status) }, '+'),
        el('button', { type: 'button', class: 'btn sm icon ghost collapse-btn', 'aria-label': `Collapse ${STATUS_LABEL[status]}`, title: 'Collapse column', onclick: () => toggleCollapse(status) },
          svg('svg', { width: 12, height: 12, viewBox: '0 0 16 16', 'aria-hidden': 'true' }, svg('path', { d: 'M10 3 5 8l5 5', stroke: 'currentColor', 'stroke-width': 1.8, fill: 'none', 'stroke-linecap': 'round', 'stroke-linejoin': 'round' })))),
      body, composer);
    col.addEventListener('dragover', onDragOver);
    col.addEventListener('dragleave', onDragLeave);
    col.addEventListener('drop', onDrop);
    body.addEventListener('scroll', () => { /* keep focus ring sane */ }, { passive: true });
    cols[status] = { col, body, count, composer };
    dom.board.append(col);
    renderComposer(status);
  }
  for (const btn of $$('[role="tab"]', dom.segments)) {
    btn.style.setProperty('--hue', `var(--${btn.dataset.status})`);
    btn.addEventListener('click', () => cols[btn.dataset.status].col.scrollIntoView({ behavior: dur(1) ? 'smooth' : 'auto', inline: 'start', block: 'nearest' }));
  }
}
function sortTasks(list) {
  if (settings.sort === 'prio') return list.slice().sort((a, b) => (a.prio || 3) - (b.prio || 3) || a.position - b.position);
  if (settings.sort === 'due') return list.slice().sort((a, b) => (a.due ? 1 : 2) - (b.due ? 1 : 2) || (a.due || '').localeCompare(b.due || '') || a.position - b.position);
  return list;
}
function groupTasks(list = state.tasks) {
  const groups = { todo: [], doing: [], done: [], cancelled: [] };
  for (const t of list) (groups[t.status] || groups.todo).push(t);
  return groups;
}
const visibleStatuses = () => STATUSES.filter((s) => !cols[s].col.classList.contains('hidden-empty') && !(s === 'cancelled' && !settings.showCancelled));

function priorityIcon(prio) {
  return svg('svg', { class: `prio-icon p${prio}`, width: 12, height: 10, viewBox: '0 0 12 10', 'aria-label': `Priority ${PRIO_LABEL[prio]}`, role: 'img' },
    svg('rect', { x: 0, y: 6, width: 3, height: 4, rx: 0.8 }), svg('rect', { x: 4.5, y: 3, width: 3, height: 7, rx: 0.8 }), svg('rect', { x: 9, y: 0, width: 3, height: 10, rx: 0.8 }));
}
function progressRing(done, total) {
  const c = 2 * Math.PI * 5;
  return el('span', { class: 'ring' + (done === total ? ' complete' : ''), title: `${done} of ${total} checklist items done` },
    svg('svg', { viewBox: '0 0 14 14', 'aria-hidden': 'true' }, svg('circle', { class: 'track', cx: 7, cy: 7, r: 5 }), svg('circle', { class: 'fill', cx: 7, cy: 7, r: 5, 'stroke-dasharray': c.toFixed(2), 'stroke-dashoffset': (c * (1 - done / total)).toFixed(2), 'stroke-linecap': 'round' })),
    el('span', { class: 'num' }, `${done}/${total}`));
}
const iconPath = (d) => svg('svg', { width: 12, height: 12, viewBox: '0 0 16 16', 'aria-hidden': 'true' }, svg('path', { d, stroke: 'currentColor', 'stroke-width': 1.8, fill: 'none', 'stroke-linecap': 'round', 'stroke-linejoin': 'round' }));
const ICON = { open: 'M4 12 12 4M6 4h6v6', ship: 'M3 8.5 6.5 12 13 4.5', cancel: 'M4 4l8 8M12 4l-8 8', comment: 'M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z' };

function cardEl(t) {
  const show = settings.show;
  const checks = t.checks || [];
  const doneCount = checks.filter((c) => c.done).length;
  const due = show.due && t.due ? dueChip(t.due) : null;
  const tags = show.tags ? userTags(t) : [];
  const commentCount = show.comments ? state.commentCounts[t.id] : undefined;
  const selected = state.selected.has(t.id);
  const meta = [
    due ? el('span', { class: 'pill date ' + due.tone, title: 'Due ' + t.due }, due.label) : null,
    show.checks && checks.length ? progressRing(doneCount, checks.length) : null,
    commentCount ? el('span', { class: 'pill plain', title: `${commentCount} comments` }, iconPath(ICON.comment), el('span', { class: 'num' }, commentCount)) : null,
    t.blocked ? el('span', { class: 'pill blocked-mark', title: 'Blocked' }, 'blocked') : null,
    show.effort && t.effort ? el('span', { class: 'pill effort', title: 'Effort ' + t.effort }, t.effort) : null,
  ];
  const quick = (name, label, run) => el('button', { type: 'button', class: 'btn', 'aria-label': label, title: label, tabindex: '-1', onclick: (e) => { e.stopPropagation(); run(); } }, iconPath(ICON[name]));
  const actions = clean([
    quick('open', 'Open', () => openDetail(t.seq)),
    isOpen(t) ? quick('ship', 'Ship', () => shipTask(t)) : null,
    t.status !== 'cancelled' ? quick('cancel', 'Cancel task', () => cancelTask(t)) : null,
  ]);
  const card = el('article', {
    class: 'card' + (t.blocked ? ' blocked' : '') + (selected ? ' selected' : '') + (state.focusId === t.id ? ' focused' : '') + (state.lifted === t.id ? ' lifted' : ''),
    draggable: 'true', tabindex: state.focusId === t.id ? '0' : '-1', role: 'option', 'aria-selected': selected ? 'true' : 'false',
    'data-id': t.id, 'data-seq': t.seq, 'data-prio': t.prio || 3, 'data-status': t.status, 'aria-label': `#${t.seq} ${t.title}`,
    onclick: (e) => onCardClick(e, t),
    onfocus: () => { if (state.focusId !== t.id) setFocus(t.id, { focus: false }); },
    ondragstart: onDragStart, ondragend: cleanupDrag, onpointerdown: onCardPointerDown,
  },
    el('div', { class: 'card-top' }, priorityIcon(t.prio || 3), show.seq ? el('span', { class: 'seq' }, '#' + t.seq) : null,
      el('span', { class: 'card-title' }, show.emoji && t.emoji ? el('span', { class: 'emoji' }, t.emoji) : null, t.title)),
    el('div', { class: 'card-meta' }, ...meta),
    tags.length ? el('div', { class: 'card-tags' }, ...tags.map((tag) => el('button', {
      type: 'button', class: 'chip chip-btn hued', style: `--h:${labelHue(tag)}`, tabindex: '-1', 'aria-pressed': state.tags.has(tag) ? 'true' : 'false', title: 'Filter by ' + tag,
      onclick: (e) => { e.stopPropagation(); toggleTag(tag); },
    }, el('span', {}, tag)))) : null,
    el('div', { class: 'card-actions' }, ...actions),
  );
  return card;
}
function emptyEl(status) {
  const msg = anyFilter() ? ['No matches'] : status === 'todo' ? ['Press ', el('kbd', {}, 'n'), ' to add a task'] : ['Nothing here'];
  return el('div', { class: 'empty' }, ...msg);
}
function leaveCard(card, rect) {
  card.classList.add('leaving');
  card.style.cssText = `left:${rect.left}px;top:${rect.top}px;width:${rect.width}px`;
  document.body.append(card);
  const a = animate(card, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.96)' }], 180);
  a.onfinish = a.oncancel = () => card.remove();
}
function tickCounter(node, text) {
  if (node.textContent === text) return;
  node.textContent = text;
  animate(node, [{ transform: 'translateY(6px)', opacity: 0 }, { transform: 'none', opacity: 1 }], 180);
}
function renderBoard() {
  if (state.dragging || state.touch) return;
  const first = new Map();
  for (const c of $$('.card[data-id]', dom.board)) {
    const ring = c.querySelector('.ring .fill');
    first.set(c.dataset.id, { rect: c.getBoundingClientRect(), ring: ring ? ring.getAttribute('stroke-dashoffset') : null });
  }
  const hadFocus = document.activeElement && document.activeElement.closest && document.activeElement.closest('.card');
  const groups = groupTasks(visibleTasks());
  if (state.focusId && !state.tasks.some((t) => t.id === state.focusId)) state.focusId = null;
  for (const id of state.selected) if (!state.tasks.some((t) => t.id === id)) state.selected.delete(id);
  const template = [];
  for (const status of STATUSES) {
    const { col, body, count } = cols[status];
    const tasks = sortTasks(groups[status]);
    const keep = new Set(tasks.map((t) => t.id));
    for (const c of $$('.card[data-id]', body)) if (!keep.has(c.dataset.id)) leaveCard(c, first.get(c.dataset.id).rect);
    body.replaceChildren(...(tasks.length ? tasks.map(cardEl) : [emptyEl(status)]));
    const limit = Number(settings.wip[status]) || 0;
    const over = limit > 0 && tasks.length > limit;
    col.classList.toggle('over', over);
    tickCounter(count, limit ? `${tasks.length}/${limit}` : String(tasks.length));
    count.title = over ? `Over the WIP limit of ${limit}` : limit ? `WIP limit ${limit}` : '';
    const seg = dom.segments.querySelector(`[data-status="${status}"] .seg-count`);
    if (seg) tickCounter(seg, String(tasks.length));
    const hidden = (status === 'cancelled' && !settings.showCancelled) || (settings.hideEmpty && tasks.length === 0 && !anyFilter());
    col.classList.toggle('hidden-empty', hidden);
    const collapsed = !!settings.collapsed[status];
    col.classList.toggle('collapsed', collapsed);
    col.querySelector('.collapse-btn').setAttribute('aria-label', `${collapsed ? 'Expand' : 'Collapse'} ${STATUS_LABEL[status]}`);
    col.querySelector('.collapse-btn').setAttribute('aria-expanded', String(!collapsed));
    if (!hidden) template.push(collapsed ? '44px' : 'minmax(0, 1fr)');
  }
  dom.board.style.gridTemplateColumns = template.join(' ');
  let i = 0;
  for (const c of $$('.card[data-id]', dom.board)) {
    const prev = first.get(c.dataset.id);
    if (prev) {
      const r = c.getBoundingClientRect();
      const dx = prev.rect.left - r.left, dy = prev.rect.top - r.top;
      if (dx || dy) animate(c, [{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], 180);
      const ring = c.querySelector('.ring .fill');
      if (ring && prev.ring !== null && prev.ring !== ring.getAttribute('stroke-dashoffset')) {
        animate(ring, [{ strokeDashoffset: prev.ring }, { strokeDashoffset: ring.getAttribute('stroke-dashoffset') }], 180);
      }
    } else animate(c, [{ opacity: 0, transform: 'scale(0.96)' }, { opacity: 1, transform: 'none' }], 180, { delay: state.firstPaint ? Math.min(i * 12, 240) : 0 });
    i++;
  }
  state.firstPaint = false;
  if (hadFocus && state.focusId) { const again = cardNode(state.focusId); if (again) again.focus({ preventScroll: true }); }
  renderBulkBar();
  renderResultCount();
}
function render() {
  renderHeader();
  renderFilters();
  renderBoard();
}
function toggleCollapse(status) {
  settings.collapsed[status] = !settings.collapsed[status];
  applySettings();
}

/* ============================== drag and drop ============================== */
const cardNode = (id) => dom.board.querySelector(`.card[data-id="${CSS.escape(id)}"]`);
function orderedSelection() {
  return $$('.card[data-id]', dom.board).map((c) => c.dataset.id).filter((id) => state.selected.has(id));
}
function startDrag(ids, height) {
  state.dragging = { ids, height };
  state.dragPos = null;
  cancelAnimationFrame(state.autoScrollRAF);
  state.autoScrollRAF = requestAnimationFrame(autoScrollTick);
}
function onDragStart(e) {
  const card = e.currentTarget;
  const task = findTask(card.dataset.id);
  if (!task || state.lifted) { e.preventDefault(); return; }
  const ids = state.selected.has(task.id) && state.selected.size > 1 ? orderedSelection() : [task.id];
  startDrag(ids, card.offsetHeight);
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
  setTimeout(() => { for (const id of ids) { const n = cardNode(id); if (n) n.classList.add('dragging'); } }, 0);
}
function placeSlot(body, y) {
  if (!state.dragging) return;
  const cards = $$('.card[data-id]:not(.dragging)', body);
  let before = null;
  for (const c of cards) {
    const r = c.getBoundingClientRect();
    if (y < r.top + r.height / 2) { before = c; break; }
  }
  const fresh = !dropSlot.isConnected;
  if (before ? dropSlot.nextElementSibling !== before || dropSlot.parentNode !== body : body.lastElementChild !== dropSlot) body.insertBefore(dropSlot, before);
  if (fresh) { dropSlot.style.height = '0px'; void dropSlot.offsetHeight; }
  dropSlot.style.height = state.dragging.height + 'px';
}
function slotIndex(body) {
  let index = 0;
  for (const node of body.children) {
    if (node === dropSlot) return index;
    if (node.classList.contains('card') && node.dataset.id && !node.classList.contains('dragging')) index++;
  }
  return index; // no slot (collapsed column): append
}
function hoverColumn(col, x, y) {
  for (const c of $$('.col.drop-target', dom.board)) if (c !== col) c.classList.remove('drop-target');
  if (!col) { dropSlot.remove(); return; }
  col.classList.add('drop-target');
  if (!col.classList.contains('collapsed')) placeSlot(cols[col.dataset.status].body, y);
  state.dragPos = { x, y, body: cols[col.dataset.status].body };
}
function onDragOver(e) {
  if (!state.dragging) return;
  e.preventDefault();
  e.dataTransfer.dropEffect = 'move';
  hoverColumn(e.currentTarget, e.clientX, e.clientY);
}
function onDragLeave(e) {
  const col = e.currentTarget;
  if (col.contains(e.relatedTarget)) return;
  col.classList.remove('drop-target');
  if (dropSlot.parentNode === cols[col.dataset.status].body) dropSlot.remove();
}
function onDrop(e) {
  if (!state.dragging) return;
  e.preventDefault();
  dropOn(e.currentTarget);
}
function dropOn(col) {
  const status = col.dataset.status;
  const index = col.classList.contains('collapsed') ? undefined : slotIndex(cols[status].body);
  const ids = state.dragging.ids;
  cleanupDrag();
  moveMany(ids, status, index);
}
function cleanupDrag() {
  const was = !!state.dragging;
  state.dragging = null;
  state.dragPos = null;
  cancelAnimationFrame(state.autoScrollRAF);
  dropSlot.remove();
  for (const c of $$('.card.dragging', dom.board)) c.classList.remove('dragging');
  for (const c of $$('.col.drop-target', dom.board)) c.classList.remove('drop-target');
  if (was) invalidate();
}
// Scrolls the column body near its top/bottom edge and the board track near its left/right edge while dragging.
function autoScrollTick() {
  if (!state.dragging && !state.touch) return;
  const p = state.dragPos;
  if (p) {
    const edge = 48, step = 10;
    if (p.body) {
      const r = p.body.getBoundingClientRect();
      if (p.y < r.top + edge) p.body.scrollTop -= step; else if (p.y > r.bottom - edge) p.body.scrollTop += step;
    }
    const b = dom.board.getBoundingClientRect();
    if (dom.board.scrollWidth > dom.board.clientWidth) {
      if (p.x < b.left + edge) dom.board.scrollLeft -= step; else if (p.x > b.right - edge) dom.board.scrollLeft += step;
    }
  }
  state.autoScrollRAF = requestAnimationFrame(autoScrollTick);
}

// Touch: long-press (250 ms) lifts the card, pointer moves carry a floating ghost, release drops.
function onCardPointerDown(e) {
  if (e.pointerType !== 'touch' || state.lifted) return;
  const card = e.currentTarget;
  const start = { x: e.clientX, y: e.clientY };
  let timer = setTimeout(() => { timer = 0; beginTouchDrag(card, e.pointerId, start); }, 250);
  const cancel = () => { if (timer) clearTimeout(timer); timer = 0; card.removeEventListener('pointermove', onMove); card.removeEventListener('pointerup', cancel); card.removeEventListener('pointercancel', cancel); };
  const onMove = (ev) => { if (timer && Math.hypot(ev.clientX - start.x, ev.clientY - start.y) > 8) cancel(); };
  card.addEventListener('pointermove', onMove);
  card.addEventListener('pointerup', cancel);
  card.addEventListener('pointercancel', cancel);
}
function beginTouchDrag(card, pointerId, start) {
  const task = findTask(card.dataset.id);
  if (!task) return;
  const ids = state.selected.has(task.id) && state.selected.size > 1 ? orderedSelection() : [task.id];
  const r = card.getBoundingClientRect();
  const ghost = card.cloneNode(true);
  ghost.classList.add('touch-ghost');
  if (ids.length > 1) { ghost.classList.add('ghost', 'stack'); ghost.dataset.count = ids.length; ghost.style.position = 'fixed'; ghost.style.top = ''; ghost.style.left = ''; }
  ghost.style.width = r.width + 'px';
  ghost.style.left = r.left + 'px';
  ghost.style.top = r.top + 'px';
  document.body.append(ghost);
  animate(ghost, [{ transform: 'scale(1)' }, { transform: 'scale(1.04) rotate(1.5deg)' }], 150, { fill: 'forwards' });
  if (navigator.vibrate) navigator.vibrate(10);
  startDrag(ids, r.height);
  state.touch = { ghost, dx: start.x - r.left, dy: start.y - r.top, pointerId, col: null };
  try { card.setPointerCapture(pointerId); } catch (err) { /* ignore */ }
  for (const id of ids) { const n = cardNode(id); if (n) n.classList.add('dragging'); }
  const move = (ev) => {
    if (!state.touch) return;
    ghost.style.left = ev.clientX - state.touch.dx + 'px';
    ghost.style.top = ev.clientY - state.touch.dy + 'px';
    const under = document.elementFromPoint(ev.clientX, ev.clientY);
    const col = under && under.closest ? under.closest('.col') : null;
    state.touch.col = col;
    hoverColumn(col, ev.clientX, ev.clientY);
  };
  const end = (ev) => {
    card.removeEventListener('pointermove', move);
    card.removeEventListener('pointerup', end);
    card.removeEventListener('pointercancel', end);
    const col = ev.type === 'pointerup' ? state.touch && state.touch.col : null;
    ghost.remove();
    state.touch = null;
    if (col && state.dragging) dropOn(col); else cleanupDrag();
  };
  card.addEventListener('pointermove', move);
  card.addEventListener('pointerup', end);
  card.addEventListener('pointercancel', end);
}
document.addEventListener('touchmove', (e) => { if (state.touch) e.preventDefault(); }, { passive: false });

// Local reorder without the API: shared by drop, keyboard lift and undo.
function localMove(ids, status, index) {
  const groups = groupTasks();
  const moving = ids.map(findTask).filter(Boolean);
  for (const t of moving) { const list = groups[t.status]; list.splice(list.indexOf(t), 1); t.status = status; }
  const target = groups[status];
  const at = index === undefined ? target.length : Math.min(index, target.length);
  target.splice(at, 0, ...moving);
  state.tasks = STATUSES.flatMap((s) => groups[s]);
}
// Moves ids (in order) to status at index, sequentially, with an Undo toast. index undefined = append.
async function moveMany(ids, status, index) {
  const groups = groupTasks();
  const prev = ids.map((id) => { const t = findTask(id); return t && { id, status: t.status, index: groups[t.status].indexOf(t) }; }).filter(Boolean);
  if (!prev.length) return;
  if (prev.length === 1 && prev[0].status === status && (index === undefined || prev[0].index === index)) return;
  localMove(ids, status, index);
  renderBoard();
  let moved = 0;
  for (let i = 0; i < ids.length; i++) {
    try {
      const out = await withForce((force) => api('POST', taskPath(ids[i]) + '/move', moveBody(status, index === undefined ? undefined : index + i, force)));
      if (out === undefined) break;
      moved++;
    } catch (err) { toast(err.message, 'error'); break; }
  }
  invalidate();
  if (!moved) return;
  const label = moved === 1 ? `#${findTask(ids[0]).seq}` : `${moved} tasks`;
  announce(`Moved ${label} to ${STATUS_LABEL[status]}`);
  const undo = async () => {
    for (const p of prev.slice(0, moved).sort((a, b) => a.index - b.index)) {
      try { await withForce((force) => api('POST', taskPath(p.id) + '/move', moveBody(p.status, p.index, force))); } catch (err) { toast(err.message, 'error'); break; }
    }
    invalidate();
  };
  toast(`Moved ${label} to ${STATUS_LABEL[status]}`, 'ok', { action: { label: 'Undo', run: undo }, life: 6000 });
}

/* ============================== keyboard: focus, lift, selection ============================== */
function setFocus(id, opts = {}) {
  const prev = state.focusId ? cardNode(state.focusId) : null;
  if (prev && prev.dataset.id !== id) { prev.classList.remove('focused'); prev.tabIndex = -1; }
  state.focusId = id;
  const node = id ? cardNode(id) : null;
  if (!node) return;
  node.classList.add('focused');
  node.tabIndex = 0;
  if (opts.focus !== false) node.focus({ preventScroll: true });
  node.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: dur(1) ? 'smooth' : 'auto' });
}
const columnCards = (status) => $$('.card[data-id]', cols[status].body);
function focusedCard() { return state.focusId ? cardNode(state.focusId) : null; }
function moveFocus(dRow, dCol) {
  const statuses = visibleStatuses().filter((s) => !settings.collapsed[s]);
  if (!statuses.length) return;
  const cur = focusedCard();
  if (!cur) { const first = statuses.map(columnCards).find((l) => l.length); if (first) setFocus(first[0].dataset.id); return; }
  const status = cur.dataset.status;
  const list = columnCards(status);
  const idx = list.indexOf(cur);
  if (dCol) {
    let ci = statuses.indexOf(status);
    for (let step = 0; step < statuses.length; step++) {
      ci = (ci + dCol + statuses.length) % statuses.length;
      const other = columnCards(statuses[ci]);
      if (other.length) { setFocus(other[Math.min(idx, other.length - 1)].dataset.id); return; }
    }
    return;
  }
  const next = list[Math.max(0, Math.min(list.length - 1, idx + dRow))];
  if (next) setFocus(next.dataset.id);
}
function toggleLift() {
  const card = focusedCard();
  if (!card) return;
  if (state.lifted === card.dataset.id) { dropLifted(); return; }
  if (state.lifted) return;
  state.lifted = card.dataset.id;
  const t = findTask(state.lifted);
  state.liftOrigin = { status: t.status, index: groupTasks()[t.status].indexOf(t) };
  card.classList.add('lifted');
  announce(`Lifted #${t.seq}. Move with h, j, k, l; Space drops; Escape cancels.`);
}
function moveLifted(dRow, dCol) {
  const t = findTask(state.lifted);
  if (!t) return;
  const groups = groupTasks();
  let status = t.status, index = groups[status].indexOf(t);
  if (dCol) {
    const statuses = visibleStatuses().filter((s) => !settings.collapsed[s]);
    status = statuses[(statuses.indexOf(status) + dCol + statuses.length) % statuses.length];
    index = Math.min(index, groups[status].length);
  } else index = Math.max(0, Math.min(groups[status].length - 1, index + dRow));
  localMove([t.id], status, index);
  renderBoard();
  setFocus(t.id);
}
async function dropLifted() {
  const id = state.lifted;
  const t = findTask(id);
  const origin = state.liftOrigin;
  state.lifted = null;
  if (!t) return;
  const index = groupTasks()[t.status].indexOf(t);
  const node = cardNode(id);
  if (node) node.classList.remove('lifted');
  if (origin && origin.status === t.status && origin.index === index) return;
  const out = await mutate(() => withForce((force) => api('POST', taskPath(id) + '/move', moveBody(t.status, index, force))), `Moved #${t.seq} to ${STATUS_LABEL[t.status]}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(id) + '/move', moveBody(origin.status, origin.index, force)))));
  announce(out ? `Moved #${t.seq} to ${STATUS_LABEL[t.status]}` : 'Move cancelled');
  setFocus(id);
}
function cancelLift() {
  const id = state.lifted;
  state.lifted = null;
  invalidate();
  if (id) setFocus(id);
}
function onCardClick(e, t) {
  if (e.shiftKey || e.ctrlKey || e.metaKey) {
    e.preventDefault();
    if (e.shiftKey && state.anchorId) {
      const anchor = cardNode(state.anchorId);
      const list = anchor && anchor.dataset.status === t.status ? columnCards(t.status) : [];
      const a = list.indexOf(anchor), b = list.findIndex((c) => c.dataset.id === t.id);
      if (a >= 0 && b >= 0) for (const c of list.slice(Math.min(a, b), Math.max(a, b) + 1)) state.selected.add(c.dataset.id);
      else state.selected.add(t.id);
    } else {
      if (state.selected.has(t.id)) state.selected.delete(t.id); else state.selected.add(t.id);
      state.anchorId = t.id;
    }
    setFocus(t.id);
    renderBoard();
    return;
  }
  setFocus(t.id, { focus: false });
  openDetail(t.seq);
}
function toggleSelect(id) {
  if (state.selected.has(id)) state.selected.delete(id); else { state.selected.add(id); state.anchorId = id; }
  renderBoard();
}
function selectColumn() {
  const card = focusedCard();
  const status = card ? card.dataset.status : visibleStatuses()[0];
  for (const c of columnCards(status)) state.selected.add(c.dataset.id);
  renderBoard();
  announce(`Selected ${state.selected.size} tasks`);
}
function clearSelection() {
  if (!state.selected.size) return;
  state.selected.clear();
  renderBoard();
}
function renderBulkBar() {
  const n = state.selected.size;
  dom.bulkbar.hidden = n === 0;
  if (!n) return;
  const ids = () => orderedSelection();
  const moveSel = el('select', { 'aria-label': 'Move selection to column', onchange: (e) => { if (e.target.value) { moveMany(ids(), e.target.value); clearSelection(); } } },
    el('option', { value: '' }, 'Move to…'), ...STATUSES.map((s) => el('option', { value: s }, STATUS_LABEL[s])));
  const prioSel = el('select', { 'aria-label': 'Set priority', onchange: (e) => { if (e.target.value) bulkPatch(ids(), () => ({ prio: Number(e.target.value) }), `Priority ${PRIO_LABEL[e.target.value]}`); } },
    el('option', { value: '' }, 'Priority…'), ...[1, 2, 3].map((p) => el('option', { value: p }, `${p} · ${PRIO_LABEL[p]}`)));
  const labelInput = el('input', { type: 'text', placeholder: 'Add label', 'aria-label': 'Add label to selection', onkeydown: (e) => { if (e.key === 'Enter') { e.preventDefault(); addLabelToSelection(labelInput.value); } } });
  dom.bulkbar.replaceChildren(
    el('span', { class: 'n' }, n), el('span', {}, 'selected'),
    moveSel, prioSel, labelInput,
    el('button', { type: 'button', class: 'btn sm', onclick: () => addLabelToSelection(labelInput.value) }, 'Add'),
    el('button', { type: 'button', class: 'btn sm', onclick: () => bulkCancel(ids()) }, 'Cancel tasks'),
    el('button', { type: 'button', class: 'btn sm', 'aria-label': 'Clear selection', onclick: clearSelection }, 'Clear'),
  );
}
async function bulkPatch(ids, patchFor, label) {
  let n = 0;
  for (const id of ids) {
    const t = findTask(id);
    if (!t) continue;
    try { await api('PATCH', taskPath(id), patchFor(t)); n++; } catch (err) { toast(err.message, 'error'); break; }
  }
  if (n) toast(`${label} on ${n} task${n === 1 ? '' : 's'}`, 'ok');
  clearSelection();
  invalidate();
}
function addLabelToSelection(label) {
  const tag = (label || '').trim();
  if (!tag) return;
  bulkPatch(orderedSelection(), (t) => ({ tags: Array.from(new Set([...userTags(t), tag])) }), `Label ${tag}`);
}
async function bulkCancel(ids) {
  if (!confirm(`Cancel ${ids.length} task${ids.length === 1 ? '' : 's'}?`)) return;
  const done = [];
  for (const id of ids) {
    try { await api('POST', taskPath(id) + '/cancel', {}); done.push(id); } catch (err) { toast(err.message, 'error'); break; }
  }
  clearSelection();
  invalidate();
  if (done.length) toast(`Cancelled ${done.length} task${done.length === 1 ? '' : 's'}`, 'ok', { life: 6000, action: { label: 'Undo', run: async () => { for (const id of done) { try { await api('POST', taskPath(id) + '/restore'); } catch (err) { toast(err.message, 'error'); break; } } invalidate(); } } });
}
async function setPriority(t, prio) {
  if (t.prio === prio) return;
  await mutate(() => api('PATCH', taskPath(t.id), { prio }), `#${t.seq} priority ${PRIO_LABEL[prio]}`);
}

/* ============================== detail ============================== */
function mountDetail() {
  const wide = wideMQ.matches;
  const open = !!state.detail;
  if (wide) {
    if (dom.detail.parentNode !== dom.panel) dom.panel.append(dom.detail);
    if (dom.detailDialog.open) dom.detailDialog.close();
    dom.panel.hidden = !open;
    dom.detail.hidden = !open;
  } else {
    if (dom.detail.parentNode !== dom.detailDialog) dom.detailDialog.append(dom.detail);
    dom.panel.hidden = true;
    dom.detail.hidden = !open;
    if (open && !dom.detailDialog.open) showDialog(dom.detailDialog);
    if (!open && dom.detailDialog.open) closeDialog(dom.detailDialog);
  }
}
function openDetail(ref, opts = {}) {
  const key = String(ref);
  const changed = state.detail !== key;
  state.detail = key;
  if (changed) {
    state.detailJSON = '';
    state.detailData = null;
    dom.detailTitle.replaceChildren(el('span', { class: 'seq' }, '#' + key));
    dom.detailBody.replaceChildren(el('div', { class: 'card skeleton', style: 'grid-column: 1 / -1' }));
    dom.detailActions.replaceChildren();
  }
  const wasHidden = dom.detail.hidden;
  mountDetail();
  if (wasHidden && wideMQ.matches) animate(dom.panel, [{ opacity: 0, transform: 'translateX(12px)' }, { opacity: 1, transform: 'none' }], 180);
  if (!opts.route && location.hash !== '#/t/' + key) { state.routePushed = true; location.hash = '#/t/' + key; }
  loadDetail(key).then(() => { if (opts.focusComment) { const ta = dom.detailBody.querySelector('.comment-form textarea'); if (ta) ta.focus(); } });
}
function closeDetail(opts = {}) {
  if (!state.detail) return;
  const t = state.detailData && state.detailData.task;
  state.detail = null;
  state.detailJSON = '';
  state.detailData = null;
  mountDetail();
  if (!opts.route && /^#\/t\//.test(location.hash)) history.replaceState(null, '', location.pathname + location.search + '#/');
  const back = t ? cardNode(t.id) : null;
  if (back) setFocus(t.id); else dom.board.focus({ preventScroll: true });
}
async function loadDetail(ref, silent) {
  try {
    const data = await api('GET', taskPath(ref));
    if (state.detail !== String(ref)) return;
    const json = JSON.stringify(data);
    const count = (data.comments || []).length;
    if (state.commentCounts[data.task.id] !== count) { state.commentCounts[data.task.id] = count; if (settings.show.comments) renderBoard(); }
    if (json === state.detailJSON) return;
    state.detailJSON = json;
    state.detailData = data;
    renderDetail(data);
  } catch (err) {
    if (!silent) toast(err.message, 'error');
    if (err.status === 404) closeDetail();
  }
}
function taskLink(t) {
  return el('button', { type: 'button', class: 'btn sm link', style: `--hue: var(--${t.status})`, title: t.title, onclick: () => openDetail(t.seq) },
    el('i', { class: 'status-dot' }), el('span', {}, `#${t.seq} ${t.title}`));
}
function renderDetail(data) {
  const { task, comments = [], links = {} } = data;
  const draft = dom.detailBody.querySelector('.comment-form textarea');
  const draftText = draft ? draft.value : '';
  const hadFocus = draft && document.activeElement === draft;

  dom.detailTitle.replaceChildren(el('span', { class: 'seq' }, '#' + task.seq), el('span', {}, task.emoji ? task.emoji + ' ' : '', task.title));

  const checks = task.checks || [];
  const doneCount = checks.filter((c) => c.done).length;
  const checkList = el('ul', { class: 'checks' }, ...checks.map((c, i) => el('li', {}, el('label', {},
    el('input', { type: 'checkbox', class: 'cb', checked: !!c.done, onchange: (e) => toggleCheck(task, i, e.target) }),
    el('span', { class: c.done ? 'done-text' : '' }, c.text)))));

  const commentNodes = comments.map((c) => el('div', { class: 'comment' },
    el('div', { class: 'comment-head' }, el('span', { class: 'author' }, c.author || 'default'), el('time', { datetime: c.createdAt, title: fmtDate(c.createdAt) }, relTime(c.createdAt)),
      el('button', { type: 'button', class: 'btn sm ghost danger', 'aria-label': 'Delete comment', onclick: () => deleteComment(task, c) }, 'Delete')),
    renderMarkdown(c.body, '')));
  const commentInput = el('textarea', { rows: '1', placeholder: 'Write a comment', title: 'Ctrl+Enter submits', 'aria-label': 'New comment', value: draftText,
    onkeydown: (e) => { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); addComment(task, commentInput); } } });
  const commentForm = el('form', { class: 'comment-form', onsubmit: (e) => { e.preventDefault(); addComment(task, commentInput); } },
    commentInput, el('button', { type: 'submit', class: 'btn' }, 'Comment'));

  const main = el('div', { class: 'detail-main' },
    el('div', { class: 'section' }, renderMarkdown(task.desc)),
    checks.length ? el('div', { class: 'section' }, el('h4', {}, 'Checklist ', el('span', { class: 'num' }, `${doneCount}/${checks.length}`)), checkList) : null,
    el('div', { class: 'section' }, el('h4', {}, 'Comments ', el('span', { class: 'num' }, comments.length)), el('div', { class: 'comments' }, ...commentNodes), commentForm));

  const due = task.due ? dueChip(task.due) : null;
  const tags = userTags(task);
  const facts = el('dl', {},
    el('dt', {}, 'Status'), el('dd', {}, el('span', { class: 'pill', style: `color: var(--${task.status})` }, STATUS_LABEL[task.status] || task.status)),
    el('dt', {}, 'Priority'), el('dd', {}, el('span', { class: `pill prio-${task.prio || 3}` }, priorityIcon(task.prio || 3), ' ', PRIO_LABEL[task.prio] || 'Low')),
    el('dt', {}, 'Due'), el('dd', { class: 'num' }, due ? el('span', { class: 'pill date ' + due.tone, title: task.due }, `${task.due} · ${due.label}`) : '—'),
    el('dt', {}, 'Effort'), el('dd', {}, task.effort || '—'),
    el('dt', {}, 'Blocked'), el('dd', {}, task.blocked ? el('span', { class: 'pill blocked-mark' }, 'yes') : 'no'),
    el('dt', {}, 'Project'), el('dd', {}, task.project || '—'),
    el('dt', {}, 'Tags'), el('dd', {}, tags.length ? el('div', { class: 'card-tags', style: 'margin:0' }, ...tags.map((t) => el('span', { class: 'chip hued', style: `--h:${labelHue(t)}` }, el('span', {}, t)))) : '—'),
    el('dt', {}, 'Created'), el('dd', { class: 'num' }, el('time', { datetime: task.createdAt, title: fmtDate(task.createdAt) }, relTime(task.createdAt))),
  );
  const linkNumber = el('input', { type: 'text', inputmode: 'numeric', placeholder: '#', 'aria-label': 'Task number', required: true, pattern: '#?\\d+' });
  const linkDir = el('select', { 'aria-label': 'Link direction' }, el('option', { value: 'blockedBy' }, 'is blocked by'), el('option', { value: 'blocks' }, 'blocks'));
  const linkForm = el('form', { class: 'link-form', onsubmit: (e) => { e.preventDefault(); addLink(task, linkDir.value, linkNumber.value); } },
    linkDir, linkNumber, el('button', { type: 'submit', class: 'btn sm', 'aria-label': 'Add link' }, 'Link'));
  const linkList = (items, label) => el('div', {}, el('h4', {}, label),
    el('ul', { class: 'link-list' }, ...(items.length ? items.map((t) => el('li', {}, taskLink(t),
      el('button', { type: 'button', class: 'btn sm ghost', 'aria-label': `Unlink #${t.seq}`, title: 'Unlink', onclick: () => removeLink(task, t) }, '×'))) : [el('li', { class: 'hint' }, 'none')])));
  const side = el('div', { class: 'detail-side' }, facts,
    el('div', { class: 'section' }, linkList(links.blockedBy || [], 'Blocked by'), linkList(links.blocks || [], 'Blocks'), linkForm));

  dom.detailBody.replaceChildren(main, side);
  if (hadFocus) commentInput.focus();

  const cancelled = task.status === 'cancelled';
  dom.detailActions.replaceChildren(el('span', { class: 'hint num' }, `#${task.seq}`), el('span', { class: 'grow' }), ...clean([
    el('button', { type: 'button', class: 'btn', onclick: () => openEdit(task) }, 'Edit'),
    isOpen(task) ? el('button', { type: 'button', class: 'btn primary', onclick: () => shipTask(task) }, 'Ship') : null,
    !cancelled ? el('button', { type: 'button', class: 'btn ghost', onclick: () => cancelTask(task) }, 'Cancel task') : null,
    cancelled ? el('button', { type: 'button', class: 'btn primary', onclick: () => restoreTask(task) }, 'Restore') : null,
    cancelled ? el('button', { type: 'button', class: 'btn ghost danger', onclick: () => deleteTask(task) }, 'Delete permanently') : null,
  ]));
}
async function toggleCheck(task, index, input) {
  const checks = (task.checks || []).map((c, i) => ({ text: c.text, done: i === index ? input.checked : !!c.done }));
  const label = input.nextElementSibling;
  if (label) label.className = input.checked ? 'done-text' : '';
  try {
    await api('PATCH', taskPath(task.id), { checks });
    invalidate();
    loadDetail(state.detail, true);
  } catch (err) {
    input.checked = !input.checked;
    if (label) label.className = input.checked ? 'done-text' : '';
    toast(err.message, 'error');
  }
}
async function addComment(task, input) {
  const body = input.value.trim();
  if (!body) return;
  input.disabled = true;
  try {
    await api('POST', taskPath(task.id) + '/comments', { body });
    input.value = '';
    await loadDetail(state.detail, true);
  } catch (err) { toast(err.message, 'error'); } finally { input.disabled = false; }
}
async function deleteComment(task, c) {
  try {
    await api('DELETE', `/api/comments/${encodeURIComponent(c.id)}`);
    loadDetail(state.detail, true);
    toast('Comment deleted', 'ok', { life: 6000, action: { label: 'Undo', run: async () => { try { await api('POST', taskPath(task.id) + '/comments', { body: c.body }); loadDetail(state.detail, true); } catch (err) { toast(err.message, 'error'); } } } });
  } catch (err) { toast(err.message, 'error'); }
}
async function addLink(task, direction, number) {
  const other = String(number).trim().replace(/^#/, '');
  if (!/^\d+$/.test(other)) { toast('Enter a task number', 'error'); return; }
  const body = direction === 'blocks' ? { blocker: String(task.seq), blocked: other } : { blocker: other, blocked: String(task.seq) };
  try {
    await api('POST', '/api/links', body);
    toast(`Linked #${task.seq} and #${other}`, 'ok');
    loadDetail(state.detail, true);
    invalidate();
  } catch (err) { toast(err.message, 'error'); }
}
async function removeLink(task, other) {
  const blocks = (state.detailData && state.detailData.links && state.detailData.links.blocks || []).some((t) => t.id === other.id);
  try {
    await api('DELETE', '/api/links', { a: task.id, b: other.id });
    loadDetail(state.detail, true);
    invalidate();
    const relink = blocks ? { blocker: task.id, blocked: other.id } : { blocker: other.id, blocked: task.id };
    toast(`Unlinked #${other.seq}`, 'ok', { life: 6000, action: { label: 'Undo', run: async () => { try { await api('POST', '/api/links', relink); loadDetail(state.detail, true); invalidate(); } catch (err) { toast(err.message, 'error'); } } } });
  } catch (err) { toast(err.message, 'error'); }
}
async function shipTask(task) {
  const groups = groupTasks();
  const prev = { status: task.status, index: groups[task.status].indexOf(findTask(task.id)) };
  const out = await mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody('done', undefined, force))), `Shipped #${task.seq}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody(prev.status, prev.index, force)))));
  if (out) { announce(`Moved #${task.seq} to Done`); if (state.detail) loadDetail(state.detail, true); }
}
async function cancelTask(task) {
  const reason = prompt(`Cancel #${task.seq}? Reason (optional):`, '');
  if (reason === null) return;
  const groups = groupTasks();
  const prev = { status: task.status, index: groups[task.status].indexOf(findTask(task.id)) };
  const out = await mutate(() => api('POST', taskPath(task.id) + '/cancel', reason.trim() ? { reason: reason.trim() } : {}), `Cancelled #${task.seq}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody(prev.status, prev.index, force)))));
  if (out) { announce(`Cancelled #${task.seq}`); if (state.detail) loadDetail(state.detail, true); }
}
async function restoreTask(task) {
  const out = await mutate(() => api('POST', taskPath(task.id) + '/restore'), `Restored #${task.seq}`,
    () => mutate(() => api('POST', taskPath(task.id) + '/cancel', {})));
  if (out) { announce(`Restored #${task.seq} to Todo`); if (state.detail) loadDetail(state.detail, true); }
}
async function deleteTask(task) {
  if (!confirm(`Delete #${task.seq} permanently? This cannot be undone.`)) return;
  const out = await mutate(() => api('DELETE', taskPath(task.id)), `Deleted #${task.seq}`);
  if (out) closeDetail();
}

/* ============================== dialogs: generic, edit ============================== */
function showDialog(d) {
  if (d.open) return;
  d.showModal();
  if (!discreteTransitions) animate(d, [{ opacity: 0, transform: 'scale(0.98)' }, { opacity: 1, transform: 'none' }], 180);
}
async function closeDialog(d) {
  if (!d.open) return;
  if (!discreteTransitions && dur(180)) {
    d.style.pointerEvents = 'none';
    await animate(d, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.98)' }], 180).finished.catch(() => {});
    d.style.pointerEvents = '';
  }
  d.close();
}
function parseChecks(text) {
  return text.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => (/^x\s+/i.test(l) ? { text: l.replace(/^x\s+/i, ''), done: true } : { text: l, done: false }));
}
const serializeChecks = (checks) => (checks || []).map((c) => (c.done ? 'x ' : '') + c.text).join('\n');
const parseTags = (text) => Array.from(new Set(text.split(/[\s,]+/).map((t) => t.trim()).filter((t) => t && !isProjectTag(t))));
function readForm() {
  const f = dom.editForm.elements;
  return {
    title: f.title.value.trim(), emoji: f.emoji.value.trim(), desc: f.desc.value, status: f.status.value,
    prio: Number(f.prio.value), due: f.due.value, effort: f.effort.value, tags: parseTags(f.tags.value),
    project: f.project.value, blocked: f.blocked.checked, checks: parseChecks(f.checks.value),
  };
}
function openEdit(task, preset = {}) {
  state.editing = task || null;
  const f = dom.editForm.elements;
  const projects = Array.from(new Set([...(state.meta.projects || []), state.project, task && task.project].filter(Boolean)));
  f.project.replaceChildren(...projects.map((p) => el('option', { value: p }, p)));
  f.title.value = task ? task.title : preset.title || '';
  f.emoji.value = task ? task.emoji || '' : '';
  f.desc.value = task ? task.desc || '' : '';
  f.status.value = task ? task.status : preset.status || 'todo';
  f.prio.value = String(task ? task.prio || 3 : 3);
  f.due.value = task ? task.due || '' : '';
  f.effort.value = task ? task.effort || '' : '';
  f.tags.value = task ? userTags(task).join(' ') : '';
  f.project.value = task ? task.project || state.project : state.project;
  f.blocked.checked = !!(task && task.blocked);
  f.checks.value = task ? serializeChecks(task.checks) : '';
  dom.editTitle.textContent = task ? `Edit #${task.seq}` : 'New task';
  state.editSnapshot = JSON.stringify(readForm());
  showDialog(dom.editDialog);
  f.title.focus();
}
const editDirty = () => JSON.stringify(readForm()) !== state.editSnapshot;
function requestCloseEdit() {
  if (editDirty() && !confirm('Discard changes?')) return;
  closeDialog(dom.editDialog);
}
async function saveEdit() {
  const form = readForm();
  if (!form.title) { toast('Title is required', 'error'); dom.editForm.elements.title.focus(); return; }
  const save = $('#edit-save');
  save.disabled = true;
  try {
    let saved;
    if (state.editing) {
      const prev = state.editing;
      const patch = {};
      const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
      if (form.title !== prev.title) patch.title = form.title;
      if (form.emoji !== (prev.emoji || '')) patch.emoji = form.emoji;
      if (form.desc !== (prev.desc || '')) patch.desc = form.desc;
      if (form.blocked !== !!prev.blocked) patch.blocked = form.blocked;
      if (form.prio !== (prev.prio || 3)) patch.prio = form.prio;
      if (form.due !== (prev.due || '')) patch.due = form.due;
      if (form.effort !== (prev.effort || '')) patch.effort = form.effort;
      if (!same(form.tags, userTags(prev))) patch.tags = form.tags;
      if (!same(form.checks, (prev.checks || []).map((c) => ({ text: c.text, done: !!c.done })))) patch.checks = form.checks;
      if (form.project !== (prev.project || '')) patch.project = form.project;
      if (form.status !== prev.status) patch.status = form.status;
      if (!Object.keys(patch).length) { closeDialog(dom.editDialog); return; }
      saved = await withForce((force) => api('PATCH', taskPath(prev.id), force ? Object.assign({ force: true }, patch) : patch));
    } else {
      const body = { title: form.title, status: form.status, prio: form.prio, blocked: form.blocked, tags: form.tags, checks: form.checks, project: form.project };
      for (const k of ['emoji', 'desc', 'due', 'effort']) if (form[k]) body[k] = form[k];
      saved = await withForce((force) => api('POST', '/api/tasks', force ? Object.assign({ force: true }, body) : body));
    }
    if (saved === undefined) return; // user declined the force
    const idx = state.tasks.findIndex((t) => t.id === saved.id);
    if (idx >= 0) state.tasks[idx] = saved; else state.tasks.push(saved);
    state.editSnapshot = JSON.stringify(readForm());
    closeDialog(dom.editDialog);
    renderBoard();
    toast(state.editing ? `Saved #${saved.seq}` : `Created #${saved.seq}`, 'ok');
    if (state.detail) loadDetail(state.detail, true);
    invalidate();
  } catch (err) {
    toast(err.message || 'Save failed', 'error');
  } finally {
    save.disabled = false;
  }
}

/* ============================== composer (inline quick add) ============================== */
const WEEKDAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
// "Fix login !high #auth #bug @fri ~M" -> {title, prio, tags, due, effort, chips}
function parseQuickAdd(text) {
  const out = { title: [], prio: null, tags: [], due: null, effort: null, chips: [] };
  for (const w of text.split(/\s+/)) {
    if (!w) continue;
    let m;
    if ((m = /^!(high|medium|med|low|[123])$/i.exec(w))) { out.prio = PRIO_WORDS[m[1].toLowerCase()]; out.chips.push({ text: 'Priority ' + PRIO_LABEL[out.prio], cls: `prio-${out.prio}` }); continue; }
    if ((m = /^#([\w:.-]+)$/.exec(w)) && !/^\d+$/.test(m[1])) { out.tags.push(m[1]); out.chips.push({ text: m[1], hue: labelHue(m[1]) }); continue; }
    if ((m = /^~([sml])$/i.exec(w))) { out.effort = m[1].toUpperCase(); out.chips.push({ text: 'Effort ' + out.effort }); continue; }
    if ((m = /^@(\d{4}-\d{2}-\d{2}|today|tomorrow|sun|mon|tue|wed|thu|fri|sat)$/i.exec(w))) {
      const v = m[1].toLowerCase();
      const today = localToday();
      let d = null;
      if (v === 'today') d = today;
      else if (v === 'tomorrow') d = new Date(today.getTime() + 86400000);
      else if (WEEKDAYS.includes(v)) d = new Date(today.getTime() + ((WEEKDAYS.indexOf(v) - today.getDay() + 7) % 7) * 86400000);
      else if (dueDays(v) !== null) out.due = v;
      if (d) out.due = isoDate(d);
      if (out.due) { const chip = dueChip(out.due); out.chips.push({ text: `Due ${out.due} (${chip.label})`, cls: 'date ' + chip.tone }); continue; }
    }
    out.title.push(w);
  }
  out.title = out.title.join(' ');
  return out;
}
function renderComposer(status, open = false) {
  const { composer } = cols[status];
  if (!open) {
    composer.replaceChildren(el('button', { type: 'button', class: 'btn sm add-task', onclick: () => openComposer(status) }, '+ Add task'));
    return;
  }
  const input = el('input', { type: 'text', placeholder: 'Title  !high #label @fri ~M', 'aria-label': `New task in ${STATUS_LABEL[status]}`, autocomplete: 'off', spellcheck: 'false' });
  const preview = el('div', { class: 'preview', 'aria-live': 'polite' });
  const hint = el('span', { class: 'hint' }, 'Enter adds · Esc closes');
  const refresh = () => {
    const p = parseQuickAdd(input.value);
    preview.replaceChildren(...p.chips.map((c) => el('span', { class: 'chip' + (c.hue !== undefined ? ' hued' : '') + (c.cls ? ' pill ' + c.cls : ''), style: c.hue !== undefined ? `--h:${c.hue}` : null }, el('span', {}, c.text))));
  };
  const submit = async () => {
    const p = parseQuickAdd(input.value);
    if (!p.title) { input.focus(); return; }
    const body = { title: p.title, status, project: state.project, tags: p.tags };
    if (p.prio) body.prio = p.prio;
    if (p.due) body.due = p.due;
    if (p.effort) body.effort = p.effort;
    input.disabled = true;
    try {
      const saved = await withForce((force) => api('POST', '/api/tasks', force ? Object.assign({ force: true }, body) : body));
      if (saved) {
        state.tasks.push(saved);
        input.value = '';
        refresh();
        renderBoard();
        announce(`Created #${saved.seq}`);
        cols[status].body.scrollTop = cols[status].body.scrollHeight;
        invalidate();
      }
    } catch (err) { toast(err.message, 'error'); } finally { input.disabled = false; input.focus(); }
  };
  input.addEventListener('input', refresh);
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); submit(); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); renderComposer(status); cols[status].composer.querySelector('button').focus(); }
  });
  const form = el('form', { onsubmit: (e) => { e.preventDefault(); submit(); } }, input, preview,
    el('div', { class: 'composer-foot' }, hint, el('button', { type: 'button', class: 'btn sm ghost', onclick: () => renderComposer(status) }, 'Close'), el('button', { type: 'submit', class: 'btn sm primary' }, 'Add')));
  composer.replaceChildren(form);
  animate(form, [{ opacity: 0, transform: 'translateY(4px)' }, { opacity: 1, transform: 'none' }], 150);
  input.focus();
}
function openComposer(status) {
  if (settings.collapsed[status]) toggleCollapse(status);
  for (const s of STATUSES) if (s !== status && cols[s].composer.querySelector('form')) renderComposer(s);
  renderComposer(status, true);
}

/* ============================== command palette ============================== */
function paletteCommands() {
  const items = [];
  const add = (group, label, run, kbd, keywords) => items.push({ group, label, run, kbd, keywords: keywords || '' });
  add('Action', 'New task', () => openEdit(null), 'n');
  add('Action', 'Keyboard help', () => showDialog(dom.helpDialog), '?');
  add('Action', 'Display options', () => toggleSettings(true), 's');
  add('Action', 'Toggle theme', toggleTheme, '', 'dark light');
  add('Action', `Density: ${settings.density === 'compact' ? 'comfortable' : 'compact'}`, () => { settings.density = settings.density === 'compact' ? 'comfortable' : 'compact'; applySettings(); }, '', 'compact comfortable');
  add('Action', `${settings.showCancelled ? 'Hide' : 'Show'} cancelled column`, () => { settings.showCancelled = !settings.showCancelled; applySettings(); }, '', 'column');
  add('Action', `${settings.hideEmpty ? 'Show' : 'Hide'} empty columns`, () => { settings.hideEmpty = !settings.hideEmpty; applySettings(); });
  if (anyFilter()) add('Action', 'Clear filters', clearFilters, 'X');
  for (const p of state.meta.projects || []) if (p !== state.project) add('Project', `Switch to ${p}`, () => setProject(p), '', 'project');
  for (const l of (state.meta.labels || []).filter((x) => !isProjectTag(x))) add('Label', `${state.tags.has(l) ? 'Remove' : 'Filter'} label ${l}`, () => toggleTag(l), '', 'tag');
  for (const q of QUICK) add('Filter', `${state.quick.has(q.id) ? 'Remove filter' : 'Filter'}: ${q.label}`, () => toggleQuick(q.id));
  for (const s of ['position', 'prio', 'due']) if (settings.sort !== s) add('Sort', `Sort columns by ${s === 'prio' ? 'priority' : s}`, () => { settings.sort = s; applySettings(); });
  for (const t of state.tasks) add('Task', `#${t.seq} ${t.title}`, () => openDetail(t.seq), '', (t.tags || []).join(' '));
  return items;
}
function openPalette() {
  dom.paletteInput.value = '';
  showDialog(dom.palette);
  renderPalette();
  dom.paletteInput.focus();
}
function renderPalette() {
  const query = dom.paletteInput.value.trim();
  const all = paletteCommands();
  let scored;
  if (!query) scored = all.filter((i) => i.group !== 'Task').concat(all.filter((i) => i.group === 'Task').slice(0, 8)).map((i) => ({ item: i, positions: [] }));
  else {
    const numeric = /^#?(\d+)$/.exec(query);
    scored = all.map((item) => {
      const m = fuzzy(query, item.label) || (item.keywords && fuzzy(query, item.keywords) ? { score: 1, positions: [] } : null);
      if (numeric && item.group === 'Task' && item.label.startsWith('#' + numeric[1] + ' ')) return { item, positions: [], score: 1000 };
      return m ? { item, positions: m.positions, score: m.score + (item.group === 'Task' ? -5 : 0) } : null;
    }).filter(Boolean).sort((a, b) => b.score - a.score).slice(0, 14);
  }
  state.paletteItems = scored;
  state.paletteIndex = 0;
  if (!scored.length) { dom.paletteList.replaceChildren(el('li', { class: 'none' }, 'No matches')); return; }
  dom.paletteList.replaceChildren(...scored.map((s, i) => {
    const label = el('span', { class: 'label' });
    const pos = new Set(s.positions);
    let run = '';
    for (let k = 0; k <= s.item.label.length; k++) {
      const ch = s.item.label[k];
      if (k < s.item.label.length && pos.has(k)) { if (run) { label.append(run); run = ''; } label.append(el('mark', {}, ch)); }
      else if (k < s.item.label.length) run += ch;
    }
    if (run) label.append(run);
    return el('li', { role: 'option', id: 'pal-' + i, 'aria-selected': i === 0 ? 'true' : 'false', onclick: () => runPalette(i), onmousemove: () => selectPalette(i) },
      el('span', { class: 'group' }, s.item.group), label, s.item.kbd ? el('kbd', {}, s.item.kbd) : null);
  }));
  dom.paletteInput.setAttribute('aria-activedescendant', 'pal-0');
}
function selectPalette(i) {
  if (i === state.paletteIndex || !state.paletteItems[i]) return;
  const lis = dom.paletteList.children;
  if (lis[state.paletteIndex]) lis[state.paletteIndex].setAttribute('aria-selected', 'false');
  state.paletteIndex = i;
  lis[i].setAttribute('aria-selected', 'true');
  lis[i].scrollIntoView({ block: 'nearest' });
  dom.paletteInput.setAttribute('aria-activedescendant', 'pal-' + i);
}
function runPalette(i) {
  const s = state.paletteItems[i];
  if (!s) return;
  closeDialog(dom.palette);
  s.item.run();
}

/* ============================== display options ============================== */
function renderSettings() {
  const seg = (label, options, current, onPick) => el('div', { class: 'row' }, el('span', { style: 'flex:1' }, label), el('div', { class: 'seg', role: 'group', 'aria-label': label },
    ...options.map(([value, text]) => el('button', { type: 'button', 'aria-pressed': current === value ? 'true' : 'false', onclick: () => { onPick(value); applySettings(); renderSettings(); } }, text))));
  const check = (label, get, set) => el('label', {}, el('input', { type: 'checkbox', class: 'cb', checked: get(), onchange: (e) => { set(e.target.checked); applySettings(); } }), label);
  const props = [['seq', '#seq'], ['emoji', 'Emoji'], ['tags', 'Labels'], ['due', 'Due'], ['effort', 'Effort'], ['checks', 'Checklist'], ['comments', 'Comments']];
  dom.settings.replaceChildren(
    el('div', { class: 'row', style: 'justify-content: space-between' }, el('h4', {}, 'Display'), el('button', { type: 'button', class: 'btn sm icon ghost', 'aria-label': 'Close', onclick: () => toggleSettings(false) }, '×')),
    seg('Theme', [['light', 'Light'], ['dark', 'Dark'], ['system', 'System']], settings.theme, (v) => { settings.theme = v; }),
    seg('Density', [['comfortable', 'Comfortable'], ['compact', 'Compact']], settings.density, (v) => { settings.density = v; }),
    el('h4', {}, 'Card properties'),
    el('div', { class: 'row' }, ...props.map(([key, label]) => check(label, () => settings.show[key], (v) => { settings.show[key] = v; }))),
    el('h4', {}, 'Columns'),
    el('div', { class: 'row' }, check('Hide empty columns', () => settings.hideEmpty, (v) => { settings.hideEmpty = v; }), check('Show cancelled', () => settings.showCancelled, (v) => { settings.showCancelled = v; })),
    el('div', { class: 'row' }, el('span', { style: 'flex:1' }, 'Sort (display only)'), el('select', { 'aria-label': 'Column sort', onchange: (e) => { settings.sort = e.target.value; applySettings(); } },
      ...[['position', 'Position'], ['prio', 'Priority'], ['due', 'Due date']].map(([v, t]) => el('option', { value: v, selected: settings.sort === v }, t)))),
    el('h4', {}, 'WIP limits'),
    el('div', { class: 'wip-grid' }, ...STATUSES.map((s) => el('label', {}, STATUS_LABEL[s], el('input', { type: 'number', min: '0', step: '1', value: settings.wip[s] || '', placeholder: '∞', 'aria-label': `WIP limit for ${STATUS_LABEL[s]}`,
      onchange: (e) => { const n = Number(e.target.value); if (n > 0) settings.wip[s] = n; else delete settings.wip[s]; applySettings(); } })))),
  );
}
function toggleSettings(open = dom.settings.hidden) {
  const btn = $('#settings-btn');
  if (open) {
    renderSettings();
    dom.settings.hidden = false;
    animate(dom.settings, [{ opacity: 0, transform: 'translateY(-4px) scale(0.98)' }, { opacity: 1, transform: 'none' }], 150);
    btn.setAttribute('aria-expanded', 'true');
    const first = dom.settings.querySelector('button[aria-pressed], input');
    if (first) first.focus();
  } else {
    if (dom.settings.hidden) return;
    dom.settings.hidden = true;
    btn.setAttribute('aria-expanded', 'false');
    if (dom.settings.contains(document.activeElement) || document.activeElement === document.body) btn.focus();
  }
}
function toggleTheme() {
  settings.theme = currentTheme() === 'dark' ? 'light' : 'dark';
  applySettings(false);
}

/* ============================== toasts / announcements ============================== */
function toast(message, kind = 'error', opts = {}) {
  const life = opts.life || (kind === 'error' ? 6000 : 3000);
  let gone = false;
  const dismiss = () => {
    if (gone) return;
    gone = true;
    const a = animate(node, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateY(8px)' }], 150);
    a.onfinish = a.oncancel = () => node.remove();
  };
  const node = el('div', { class: 'toast ' + kind, style: `--life:${life}ms` }, el('span', {}, message),
    opts.action ? el('button', { type: 'button', class: 'btn sm ghost action', onclick: () => { dismiss(); opts.action.run(); } }, opts.action.label) : null,
    el('button', { type: 'button', class: 'btn sm ghost', 'aria-label': 'Dismiss', onclick: dismiss }, '×'));
  dom.toasts.append(node);
  animate(node, [{ opacity: 0, transform: 'translateY(16px) scale(0.98)' }, { opacity: 1, transform: 'none' }], 180);
  setTimeout(dismiss, life);
  while (dom.toasts.children.length > 4) dom.toasts.firstElementChild.remove();
}
function announce(text) {
  dom.live.textContent = '';
  setTimeout(() => { dom.live.textContent = text; }, 30);
}

/* ============================== routing ============================== */
function applyRoute() {
  const m = /^#\/(t|p|f)\/(.+)$/.exec(decodeURIComponent(location.hash || ''));
  if (!m) { if (state.detail) closeDetail({ route: true }); return; }
  if (m[1] === 't') { openDetail(m[2].replace(/^#/, ''), { route: true }); return; }
  if (m[1] === 'p') { setProject(m[2]); return; }
  if (m[1] === 'f' && quickById(m[2])) { if (!state.quick.has(m[2])) toggleQuick(m[2]); }
}

/* ============================== handlers ============================== */
function setProject(name) {
  if (!name || name === state.project) return;
  state.project = name;
  state.selected.clear();
  renderHeader();
  invalidate();
}
function cycleProject(dir) {
  const list = state.meta.projects || [];
  if (list.length < 2) return;
  const i = list.indexOf(state.project);
  setProject(list[(i + dir + list.length) % list.length]);
}
function setFiltersOpen(open) {
  dom.filters.classList.toggle('open', open);
  $('#filters-toggle').setAttribute('aria-expanded', String(open));
  if (open) {
    dom.scrim.hidden = false;
    requestAnimationFrame(() => dom.scrim.classList.add('show'));
    setTimeout(() => dom.search.focus(), dur(180) + 20); // the sheet is visibility:hidden until its transition ends
  } else {
    dom.scrim.classList.remove('show');
    setTimeout(() => { dom.scrim.hidden = true; }, dur(180));
  }
}
const phoneFilters = () => getComputedStyle($('#filters-toggle')).display !== 'none';
function focusSearch() {
  if (phoneFilters()) { setFiltersOpen(true); return; }
  dom.search.focus();
  dom.search.select();
}
function focusLabels() {
  if (phoneFilters()) { setFiltersOpen(true); return; }
  const first = dom.labels.querySelector('button');
  if (first) first.focus(); else openPalette();
}
const isTyping = (target) => !!(target && target.closest && target.closest('input, textarea, select, [contenteditable="true"]'));

function onKeydown(e) {
  if (e.defaultPrevented) return;
  const openDlg = document.querySelector('dialog[open]');
  const mod = e.ctrlKey || e.metaKey;
  if (mod && e.key.toLowerCase() === 'k') { e.preventDefault(); if (openDlg === dom.palette) closeDialog(dom.palette); else { if (openDlg) closeDialog(openDlg); openPalette(); } return; }
  if (openDlg === dom.palette) {
    if (e.key === 'ArrowDown') { e.preventDefault(); selectPalette(Math.min(state.paletteItems.length - 1, state.paletteIndex + 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); selectPalette(Math.max(0, state.paletteIndex - 1)); }
    else if (e.key === 'Enter') { e.preventDefault(); runPalette(state.paletteIndex); }
    return;
  }
  if (e.key === 'Escape') {
    if (!dom.settings.hidden) { e.preventDefault(); toggleSettings(false); return; }
    if (openDlg) return; // the dialog's cancel handler closes it
    if (dom.filters.classList.contains('open')) { e.preventDefault(); setFiltersOpen(false); return; }
    if (state.lifted) { e.preventDefault(); cancelLift(); return; }
    if (isTyping(e.target)) { e.target.blur(); return; }
    if (state.selected.size) { e.preventDefault(); clearSelection(); return; }
    if (state.detail) { e.preventDefault(); closeDetail(); return; }
    return;
  }
  const onCard = !!(e.target.closest && e.target.closest('.card') && !isTyping(e.target));
  if (mod && e.key.toLowerCase() === 'a' && onCard) { e.preventDefault(); selectColumn(); return; }
  if (isTyping(e.target) || e.altKey || mod) return;
  if (openDlg && openDlg !== dom.detailDialog) return;
  const detailTask = state.detailData ? state.detailData.task : null;
  const focused = focusedCard() ? findTask(state.focusId) : null;
  const target = onCard || !detailTask ? focused : detailTask; // in the panel, actions apply to the shown task
  if (state.lifted) {
    const map = { j: [1, 0], k: [-1, 0], h: [0, -1], l: [0, 1], ArrowDown: [1, 0], ArrowUp: [-1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] };
    if (map[e.key]) { e.preventDefault(); moveLifted(...map[e.key]); return; }
    if (e.key === ' ') { e.preventDefault(); dropLifted(); return; }
    return;
  }
  switch (e.key) {
    case 'j': case 'ArrowDown': if (!openDlg) { e.preventDefault(); moveFocus(1, 0); } break;
    case 'k': case 'ArrowUp': if (!openDlg) { e.preventDefault(); moveFocus(-1, 0); } break;
    case 'h': case 'ArrowLeft': if (!openDlg) { e.preventDefault(); moveFocus(0, -1); } break;
    case 'l': case 'ArrowRight': if (!openDlg) { e.preventDefault(); moveFocus(0, 1); } break;
    case 'Tab': if (onCard) { e.preventDefault(); moveFocus(0, e.shiftKey ? -1 : 1); } break;
    case 'Enter': if (onCard && focused) { e.preventDefault(); openDetail(focused.seq); } break;
    case ' ': if (onCard) { e.preventDefault(); toggleLift(); } break;
    case 'n': if (!openDlg) { e.preventDefault(); openEdit(null); } break;
    case 'e': if (target) { e.preventDefault(); openEdit(target); } break;
    case 't': if (target && isOpen(target)) { e.preventDefault(); shipTask(target); } break;
    case 'x': if (target && target.status !== 'cancelled') { e.preventDefault(); cancelTask(target); } break;
    case 'r': if (target && target.status === 'cancelled') { e.preventDefault(); restoreTask(target); } break;
    case '1': case '2': case '3': if (target) { e.preventDefault(); setPriority(target, Number(e.key)); } break;
    case 'c': if (target) { e.preventDefault(); openDetail(target.seq, { focusComment: true }); } break;
    case 'v': if (focused) { e.preventDefault(); toggleSelect(focused.id); } break;
    case '/': if (!openDlg) { e.preventDefault(); focusSearch(); } break;
    case 'f': if (!openDlg) { e.preventDefault(); focusLabels(); } break;
    case 'X': if (!openDlg) { e.preventDefault(); clearFilters(); } break;
    case 'p': if (!openDlg) cycleProject(1); break;
    case 'P': if (!openDlg) cycleProject(-1); break;
    case 's': if (!openDlg) { e.preventDefault(); toggleSettings(); } break;
    case '?': e.preventDefault(); if (openDlg) closeDialog(openDlg); showDialog(dom.helpDialog); break;
    default: break;
  }
}
function trackActiveSegment() {
  let raf = 0;
  dom.board.addEventListener('scroll', () => {
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      let best = null, bestDist = Infinity;
      for (const status of STATUSES) {
        const d = Math.abs(cols[status].col.offsetLeft - dom.board.scrollLeft);
        if (d < bestDist) { bestDist = d; best = status; }
      }
      for (const btn of $$('[role="tab"]', dom.segments)) btn.setAttribute('aria-selected', String(btn.dataset.status === best));
    });
  }, { passive: true });
}
function bindPanelResize() {
  const handle = dom.panelHandle;
  handle.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    handle.setPointerCapture(e.pointerId);
    dom.panel.classList.add('resizing');
    const move = (ev) => { settings.panelWidth = Math.max(360, Math.min(window.innerWidth * 0.6, window.innerWidth - ev.clientX)); document.documentElement.style.setProperty('--panel-w', Math.round(settings.panelWidth) + 'px'); };
    const up = () => { handle.removeEventListener('pointermove', move); handle.removeEventListener('pointerup', up); dom.panel.classList.remove('resizing'); saveSettings(); };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', up);
  });
  handle.addEventListener('keydown', (e) => {
    const step = e.key === 'ArrowLeft' ? 24 : e.key === 'ArrowRight' ? -24 : 0;
    if (!step) return;
    e.preventDefault();
    settings.panelWidth = Math.max(360, Math.min(window.innerWidth * 0.6, settings.panelWidth + step));
    applySettings(false);
  });
}
function bind() {
  dom.search.addEventListener('input', () => {
    clearTimeout(dom.search._t);
    dom.search._t = setTimeout(() => applySearchText(dom.search.value.trim(), false), 200);
  });
  dom.search.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); clearTimeout(dom.search._t); applySearchText(dom.search.value.trim(), false); } });
  dom.project.addEventListener('change', () => setProject(dom.project.value));
  dom.clear.addEventListener('click', clearFilters);
  $('#new-task').addEventListener('click', () => openEdit(null));
  $('#help-btn').addEventListener('click', () => showDialog(dom.helpDialog));
  $('#theme-toggle').addEventListener('click', toggleTheme);
  $('#settings-btn').addEventListener('click', () => toggleSettings());
  $('#palette-btn').addEventListener('click', openPalette);
  $('#filters-toggle').addEventListener('click', () => setFiltersOpen(!dom.filters.classList.contains('open')));
  $('#filters-done').addEventListener('click', () => setFiltersOpen(false));
  dom.scrim.addEventListener('click', () => setFiltersOpen(false));
  $('#detail-close').addEventListener('click', () => closeDetail());
  dom.paletteInput.addEventListener('input', renderPalette);

  dom.editForm.addEventListener('submit', (e) => { e.preventDefault(); saveEdit(); });
  dom.editForm.addEventListener('keydown', (e) => { if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'Enter')) { e.preventDefault(); saveEdit(); } });
  dom.editDialog.addEventListener('cancel', (e) => { e.preventDefault(); requestCloseEdit(); });
  dom.editDialog.addEventListener('close', () => { state.editing = null; });

  for (const d of [dom.editDialog, dom.detailDialog, dom.helpDialog, dom.palette]) {
    const close = d === dom.editDialog ? requestCloseEdit : d === dom.detailDialog ? () => closeDetail() : () => closeDialog(d);
    for (const btn of $$('[data-close]', d)) btn.addEventListener('click', close);
    d.addEventListener('click', (e) => { if (e.target === d) close(); });
    if (d !== dom.editDialog) d.addEventListener('cancel', (e) => { e.preventDefault(); close(); });
  }
  dom.detailDialog.addEventListener('close', () => { if (state.detail && !wideMQ.matches) closeDetail(); });
  document.addEventListener('pointerdown', (e) => {
    if (!dom.settings.hidden && !dom.settings.contains(e.target) && !e.target.closest('#settings-btn')) toggleSettings(false);
  });
  document.addEventListener('keydown', onKeydown);
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') refreshTasks(); });
  window.addEventListener('online', () => refreshTasks());
  window.addEventListener('hashchange', applyRoute);
  wideMQ.addEventListener('change', mountDetail);
  trackActiveSegment();
  bindPanelResize();
}

/* ============================== init ============================== */
async function init() {
  applySettings(false);
  buildBoard();
  bind();
  mountDetail();
  await refreshMeta();
  const m = /^#\/p\/(.+)$/.exec(decodeURIComponent(location.hash || ''));
  if (m) state.project = m[1];
  await refreshTasks();
  if (!state.loaded) for (const status of STATUSES) cols[status].body.replaceChildren(el('div', { class: 'empty' }, 'Waiting for the server…'));
  applyRoute();
  schedulePoll();
}
init();
