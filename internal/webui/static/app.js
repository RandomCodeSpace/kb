/* kb web frontend. Plain ES2022, no modules, no bundler. Talks to /api/* (docs/web-api.md).
   Markdown: marked + DOMPurify (vendored, see VENDOR.md). Styling: Tailwind classes compiled by
   scripts/build-web-css.sh, plus the component classes in internal/webui/tailwind/app.css.
   Sections: utilities · labels · markdown · settings · state · api/polling · filters
   · render (header, filters, board, cards) · drag and drop · keyboard, lift, selection, bulk
   · markdown editor · label editor · detail panel · edit dialog · composer · palette
   · display options · toasts · routing · handlers · init */
'use strict';

/* ============================== utilities ============================== */
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const STATUSES = ['todo', 'doing', 'done', 'cancelled'];
const STATUS_LABEL = { todo: 'Todo', doing: 'Doing', done: 'Done', cancelled: 'Cancelled' };
const PRIO_LABEL = { 1: 'High', 2: 'Medium', 3: 'Low' };
const EFFORTS = ['S', 'M', 'L'];
const EASE = 'cubic-bezier(0.2, 0, 0, 1)';
const SVG_NS = 'http://www.w3.org/2000/svg';
const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
const wideMQ = matchMedia('(min-width: 1024px)');
const phoneMQ = matchMedia('(max-width: 767px)');
const dur = (ms) => (reducedMotion.matches ? 0 : ms);
const isMac = /Mac|iPhone|iPad/.test(navigator.platform || '');
const MOD = isMac ? '⌘' : 'Ctrl';

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
// 16px stroke icon on the text's optical center.
function icon(name, size = 16) {
  const d = ICONS[name];
  return svg('svg', { width: size, height: size, viewBox: '0 0 16 16', 'aria-hidden': 'true', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.5, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' },
    ...(Array.isArray(d) ? d : [d]).map((p) => svg('path', { d: p })));
}
const ICONS = {
  x: 'M4 4l8 8M12 4l-8 8', plus: 'M8 3.5v9M3.5 8h9', check: 'M3 8.5 6.5 12 13 4.5', open: 'M6 3.5h6.5V10M12.5 3.5 4 12',
  ship: 'M3 8.5 6.5 12 13 4.5', cancel: 'M4 4l8 8M12 4l-8 8', comment: 'M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z',
  chevL: 'M10 3 5 8l5 5', chevR: 'M6 3l5 5-5 5', chevD: 'M4 6l4 4 4-4', copy: ['M6 6h7v7H6z', 'M3 10V3h7'],
  link: ['M6.5 9.5 9.5 6.5', 'M7 4.5 8.5 3a2.5 2.5 0 0 1 3.5 3.5L10.5 8', 'M9 11.5 7.5 13A2.5 2.5 0 0 1 4 9.5L5.5 8'],
  bold: ['M5 3h4a2.5 2.5 0 0 1 0 5H5z', 'M5 8h4.5a2.5 2.5 0 0 1 0 5H5z'], italic: ['M7 3h5M4 13h5M9.5 3l-3 10'],
  strike: ['M3 8h10', 'M5.5 5.5c0-1.5 1.2-2.5 3-2.5 1.4 0 2.3.6 2.7 1.5', 'M10.5 10.5c0 1.5-1.4 2.5-3 2.5-1.5 0-2.6-.7-3-1.8'],
  code: ['M5.5 4.5 2 8l3.5 3.5', 'M10.5 4.5 14 8l-3.5 3.5'], codeblock: ['M2.5 3.5h11v9h-11z', 'M6 6.5 4.5 8 6 9.5', 'M10 6.5 11.5 8 10 9.5'],
  quote: ['M4 10.5c-1 0-1.5-.7-1.5-1.5V5.5h3v3.5c0 1-.5 1.5-1.5 1.5z', 'M11 10.5c-1 0-1.5-.7-1.5-1.5V5.5h3v3.5c0 1-.5 1.5-1.5 1.5z'],
  ul: ['M6 4h7M6 8h7M6 12h7', 'M3 4h.01M3 8h.01M3 12h.01'], ol: ['M6 4h7M6 8h7M6 12h7', 'M2.5 3.5h1v2M2.5 8h1.5l-1.5 1.5h1.5M2.5 11.5h1.5v1h-1.5v1h1.5'],
  task: ['M6 4h7M6 8h7M6 12h7', 'M2 8l1 1 2-2', 'M2 3.5h2v2H2zM2 11h2v2H2z'], h: ['M3 3v10M13 3v10M3 8h10'],
  image: ['M2.5 3.5h11v9h-11z', 'M5.5 7a1 1 0 1 0 0-.01', 'M2.5 11l3-3 2.5 2.5 2-2 3.5 3.5'], blocked: ['M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2z', 'M3.8 3.8l8.4 8.4'],
  calendar: ['M2.5 4.5h11v9h-11z', 'M2.5 7.5h11', 'M5.5 3v3M10.5 3v3'], trash: ['M3 4.5h10', 'M6.5 4.5V3h3v1.5', 'M4.5 4.5l.5 8.5h6l.5-8.5'],
  eye: ['M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8s-2.5 4.5-6.5 4.5S1.5 8 1.5 8z', 'M8 9.5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3z'], pencil: ['M11.5 2.5l2 2-8 8H3.5v-2z'],
  sun: ['M8 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6z', 'M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1'],
  grip: ['M6 4h.01M10 4h.01M6 8h.01M10 8h.01M6 12h.01M10 12h.01'],
};

function animate(node, frames, ms, extra) {
  if (!node.animate) return { finished: Promise.resolve(), onfinish: null };
  return node.animate(frames, Object.assign({ duration: dur(ms), easing: EASE, fill: 'none' }, extra || {}));
}
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
// Relative due label and tone: '' | 'soon' | 'overdue'.
function dueChip(due) {
  const days = dueDays(due);
  if (days === null) return { label: due, tone: '' };
  if (days === 0) return { label: 'Today', tone: 'soon' };
  if (days === 1) return { label: 'Tomorrow', tone: 'soon' };
  if (days > 1) return { label: `${days}d`, tone: days <= 3 ? 'soon' : '' };
  return { label: `${-days}d overdue`, tone: 'overdue' };
}
const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });
const dayFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });
const relFmt = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
function fmtDate(iso) {
  const d = new Date(iso);
  return isNaN(d) ? iso : dateFmt.format(d);
}
function relTime(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return iso;
  const s = Math.round((d - Date.now()) / 1000);
  if (Math.abs(s) < 45) return 'just now';
  const m = Math.round(s / 60);
  if (Math.abs(m) < 60) return relFmt.format(m, 'minute');
  const h = Math.round(m / 60);
  if (Math.abs(h) < 24) return relFmt.format(h, 'hour');
  const days = Math.round(h / 24);
  if (Math.abs(days) < 30) return relFmt.format(days, 'day');
  return dayFmt.format(d);
}
const isOpen = (t) => t.status === 'todo' || t.status === 'doing';
const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
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
function insertText(ta, text) {
  ta.focus();
  if (document.execCommand && document.execCommand('insertText', false, text)) return; // keeps native undo
  ta.setRangeText(text, ta.selectionStart, ta.selectionEnd, 'end');
  ta.dispatchEvent(new Event('input', { bubbles: true }));
}

/* ============================== labels ============================== */
// A label is `name` or `scope::value`. The project:: scope is data, never a chip on a card.
const isProjectTag = (tag) => tag.startsWith('project::');
const userTags = (t) => (t.tags || []).filter((tag) => !isProjectTag(tag));
function splitLabel(tag) {
  const i = tag.indexOf('::');
  return i > 0 ? { scope: tag.slice(0, i), value: tag.slice(i + 2) } : { scope: '', value: tag };
}
// Deterministic 12-hue wheel, hashed from the scope name so every type::* shares a hue.
function hueOf(name) {
  let h = 2166136261;
  for (let i = 0; i < name.length; i++) { h ^= name.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0; }
  return (h % 12) * 30;
}
const labelHue = (tag) => { const { scope } = splitLabel(tag); return hueOf(scope || tag); };
// One value per scope: adding type::feature drops type::bug.
function withLabel(tags, tag) {
  const { scope } = splitLabel(tag);
  const kept = tags.filter((t) => t !== tag && !(scope && splitLabel(t).scope === scope));
  return [...kept, tag];
}
const normalizeTags = (tags) => tags.reduce((acc, t) => (t && !isProjectTag(t) ? withLabel(acc, t) : acc), []);
// {scopes: Map<scope, tags[]>, plain: tags[]}
function groupLabels(tags) {
  const scopes = new Map(), plain = [];
  for (const tag of tags) {
    const { scope } = splitLabel(tag);
    if (!scope) { plain.push(tag); continue; }
    if (!scopes.has(scope)) scopes.set(scope, []);
    scopes.get(scope).push(tag);
  }
  return { scopes, plain };
}
// chipEl('type::bug', {onclick, pressed, remove}) -> two-tone pill; chipEl('docs') -> dot + name.
function chipEl(tag, opts = {}) {
  const { scope, value } = splitLabel(tag);
  const short = opts.short && scope; // inside a scope group: dot + value, the header names the scope
  const attrs = { class: 'chip ' + (scope && !short ? 'chip-scoped' : 'chip-dot') + (opts.class ? ' ' + opts.class : ''), style: `--h:${labelHue(tag)}`, title: opts.title || tag, 'data-tag': tag };
  const children = short ? [el('span', {}, value)] : scope ? [el('span', { class: 'k' }, scope), el('span', { class: 'v' }, value)] : [el('span', {}, tag)];
  if (opts.remove) children.push(el('button', { type: 'button', class: 'x', 'aria-label': `Remove ${tag}`, onclick: (e) => { e.stopPropagation(); opts.remove(); } }, icon('x', 10)));
  if (opts.onclick) {
    Object.assign(attrs, { type: 'button', onclick: (e) => { e.stopPropagation(); opts.onclick(e); }, 'aria-pressed': opts.pressed ? 'true' : 'false', tabindex: opts.tabindex });
    return el('button', attrs, ...children);
  }
  return el('span', attrs, ...children);
}
// Keyed re-render of a chip row: removed chips fade out, new ones fade in.
function renderChipRow(container, tags, make) {
  const existing = new Map($$('.chip[data-tag]', container).map((c) => [c.dataset.tag, c]));
  const want = new Set(tags);
  for (const [tag, node] of existing) {
    if (want.has(tag)) continue;
    if (dur(1)) { node.classList.add('leave'); node.addEventListener('animationend', () => node.remove(), { once: true }); } else node.remove();
    existing.delete(tag);
  }
  let anchor = null;
  for (const tag of tags) {
    let node = existing.get(tag);
    if (!node) { node = make(tag); if (dur(1)) node.classList.add('enter'); }
    const next = anchor ? anchor.nextElementSibling : container.firstElementChild;
    if (node !== next) container.insertBefore(node, next);
    anchor = node;
  }
}

/* ============================== markdown ============================== */
const MARKED_OPTS = { gfm: true, breaks: true, async: false };
const MD_INLINE_TAGS = ['strong', 'em', 'b', 'i', 'del', 's', 'code', 'a', 'span', 'br', 'kbd', 'sub', 'sup'];
const MD_BLOCK_TAGS = MD_INLINE_TAGS.concat(['p', 'pre', 'ul', 'ol', 'li', 'input', 'blockquote', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'img', 'div']);
const PURIFY_BASE = { ALLOWED_ATTR: ['href', 'title', 'alt', 'src', 'type', 'checked', 'disabled', 'align', 'start'], ALLOW_DATA_ATTR: false, KEEP_CONTENT: true };
const PURIFY_BLOCK = Object.assign({ ALLOWED_TAGS: MD_BLOCK_TAGS }, PURIFY_BASE);
const PURIFY_INLINE = Object.assign({ ALLOWED_TAGS: MD_INLINE_TAGS }, PURIFY_BASE);
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  const tag = node.tagName;
  if (tag === 'A') {
    const href = (node.getAttribute('href') || '').trim();
    if (!/^(https?:|mailto:)/i.test(href)) { node.removeAttribute('href'); return; }
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  } else if (tag === 'IMG') {
    if (!/^https?:/i.test((node.getAttribute('src') || '').trim())) node.parentNode && node.parentNode.removeChild(node);
    else node.setAttribute('loading', 'lazy');
  } else if (tag === 'INPUT') {
    if ((node.getAttribute('type') || '').toLowerCase() !== 'checkbox') { node.parentNode && node.parentNode.removeChild(node); return; }
    node.setAttribute('disabled', '');
    node.setAttribute('tabindex', '-1');
    node.className = 'cb';
  }
});
// renderMarkdown(md, {mode: 'block' | 'inline' | 'card', empty}) -> element. One sanitizer for every surface.
function renderMarkdown(md, opts = {}) {
  const mode = opts.mode || 'block';
  const text = String(md || '').replace(/\r\n?/g, '\n');
  if (mode === 'inline') {
    const span = el('span', { class: 'md md-inline' });
    if (text.trim()) span.innerHTML = DOMPurify.sanitize(marked.parseInline(text, MARKED_OPTS), PURIFY_INLINE);
    return span;
  }
  const root = el('div', { class: 'md' + (mode === 'card' ? ' md-card' : '') });
  if (!text.trim()) { if (opts.empty) root.append(el('span', { class: 'md-empty' }, opts.empty)); return root; }
  root.innerHTML = DOMPurify.sanitize(marked.parse(text, MARKED_OPTS), PURIFY_BLOCK);
  for (const box of $$('input[type="checkbox"]', root)) {
    const li = box.parentElement;
    if (!li || li.tagName !== 'LI' || box !== li.firstElementChild) continue;
    li.classList.add('task');
    if (box.checked) li.classList.add('done');
    const body = el('div', { class: 'task-text min-w-0 flex-1' });
    while (box.nextSibling) body.append(box.nextSibling);
    li.append(body);
  }
  if (mode === 'card') {
    for (const h of $$('h1,h2,h3,h4,h5,h6', root)) { const p = el('p', {}, el('strong', {})); p.firstChild.append(...h.childNodes); h.replaceWith(p); }
    for (const img of $$('img', root)) img.replaceWith(el('span', { class: 'img-alt' }, icon('image', 12), el('span', {}, img.getAttribute('alt') || 'image')));
    for (const a of $$('a[href]', root)) a.addEventListener('click', (e) => e.stopPropagation());
  } else {
    for (const pre of $$('pre', root)) {
      const btn = el('button', { type: 'button', class: 'btn btn-ghost btn-xs copy', 'aria-label': 'Copy code' }, icon('copy', 12));
      btn.addEventListener('click', async () => {
        try { await navigator.clipboard.writeText(pre.querySelector('code') ? pre.querySelector('code').textContent : pre.textContent); btn.replaceChildren('Copied'); setTimeout(() => btn.replaceChildren(icon('copy', 12)), 1200); } catch (e) { toast('Copy failed: clipboard unavailable', 'error'); }
      });
      pre.append(btn);
    }
  }
  return root;
}
const mdInline = (text) => renderMarkdown(text, { mode: 'inline' });

/* ============================== settings ============================== */
const SETTINGS_KEY = 'kb-web-settings';
function defaultSettings() {
  return {
    theme: 'system', density: 'comfortable',
    show: { seq: true, emoji: true, desc: true, tags: true, due: true, effort: true, checks: true, comments: true },
    hideEmpty: false, showCancelled: true, wip: {}, sort: 'position', collapsed: {}, panelWidth: 420,
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
  project: '', q: '', tokens: [], tags: new Set(), scopes: new Set(), quick: new Set(),
  tasks: [], all: [], loaded: false, etag: null, etagURL: '', online: true, firstPaint: true,
  dragging: null, touch: null, dragPos: null, autoScrollRAF: 0,
  focusId: null, lifted: null, liftOrigin: null, selected: new Set(), anchorId: null,
  detail: null, detailJSON: '', detailData: null, detailPending: null, commentCounts: {},
  editing: null, editSnapshot: '', pollTimer: 0, paletteIndex: 0, paletteItems: [],
};
const dom = {
  board: $('#board'), panel: $('#panel'), panelHandle: $('#panel-handle'), detail: $('#detail'),
  labels: $('#labels'), quick: $('#quick'), active: $('#active'), resultCount: $('#result-count'), stats: $('#stats'),
  search: $('#search'), searchWrap: $('#search-wrap'), searchSlot: $('#search-slot'), projectBtn: $('#project-btn'), projectName: $('#project-name'), projectMenu: $('#project-menu'),
  clear: $('#clear-filters'), banner: $('#banner'), conn: $('#conn'), connDot: $('#conn-dot'), version: $('#version'),
  toasts: $('#toasts'), live: $('#live'), segments: $('#segments'), filters: $('#filters'), scrim: $('#filters-scrim'), filterCount: $('#filter-count'),
  bulkbar: $('#bulkbar'), settings: $('#settings'),
  editDialog: $('#edit-dialog'), editForm: $('#edit-form'), editTitle: $('#edit-title'), editDesc: $('#edit-desc'), editLabels: $('#edit-labels'), editEffort: $('#edit-effort'),
  detailDialog: $('#detail-dialog'), detailSeq: $('#detail-seq'), detailTitle: $('#detail-title'), detailBody: $('#detail-body'), detailActions: $('#detail-actions'),
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
const patchTask = (id, patch) => withForce((force) => api('PATCH', taskPath(id), force ? Object.assign({ force: true }, patch) : patch));

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
    if (serverFiltered()) refreshAll(); else state.all = state.tasks;
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
  dom.connDot.classList.toggle('bg-ok', online);
  dom.connDot.classList.toggle('bg-danger', !online);
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
const taskRef = (t) => `#${t.seq}`;

/* ============================== filters ============================== */
const QUICK = [
  { id: 'overdue', label: 'Overdue', test: (t) => isOpen(t) && dueDays(t.due) !== null && dueDays(t.due) < 0 },
  { id: 'week', label: 'This week', test: (t) => isOpen(t) && dueDays(t.due) !== null && dueDays(t.due) >= 0 && dueDays(t.due) <= 7 },
  { id: 'high', label: 'High', test: (t) => t.prio === 1 },
  { id: 'blocked', label: 'Blocked', test: (t) => !!t.blocked },
  { id: 'checklist', label: 'Checklist', test: (t) => (t.checks || []).some((c) => !c.done) },
  { id: 'nolabels', label: 'Unlabelled', test: (t) => userTags(t).length === 0 },
];
const quickById = (id) => QUICK.find((q) => q.id === id);
const PRIO_WORDS = { high: 1, medium: 2, med: 2, low: 3, 1: 1, 2: 2, 3: 3 };

// "tag:web scope:type prio:high due:week is:blocked has:checklist effort:M #12 free words" -> {q, tokens}
function parseSearch(text) {
  const tokens = [], words = [];
  for (const w of text.split(/\s+/)) {
    if (!w) continue;
    const m = /^(tag|scope|prio|due|is|has|effort):(.+)$/i.exec(w);
    if (m) tokens.push({ key: m[1].toLowerCase(), value: m[2], raw: w });
    else if (/^#\d+$/.test(w)) tokens.push({ key: 'seq', value: w.slice(1), raw: w });
    else words.push(w);
  }
  return { q: words.join(' '), tokens };
}
const hasScope = (scope) => (t) => (t.tags || []).some((x) => splitLabel(x).scope.toLowerCase() === scope);
function tokenTest(tok) {
  const v = tok.value.toLowerCase();
  switch (tok.key) {
    case 'tag': return (t) => (t.tags || []).some((x) => x.toLowerCase() === v);
    case 'scope': return hasScope(v);
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
  const tests = clean([...state.quick].map((id) => quickById(id) && quickById(id).test)
    .concat([...state.scopes].map((s) => hasScope(s.toLowerCase())))
    .concat(state.tokens.map(tokenTest)));
  return tests.length ? state.tasks.filter((t) => tests.every((fn) => fn(t))) : state.tasks;
}
const anyFilter = () => !!(state.q || state.tags.size || state.scopes.size || state.quick.size || state.tokens.length);

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
function toggleScope(scope) {
  if (state.scopes.has(scope)) state.scopes.delete(scope); else state.scopes.add(scope);
  renderFilters();
  renderBoard();
}
function clearFilters() {
  state.tags.clear();
  state.scopes.clear();
  state.quick.clear();
  state.tokens = [];
  state.q = '';
  dom.search.value = '';
  renderFilters();
  invalidate();
}

/* ============================== render: header ============================== */
function renderHeader() {
  const { meta } = state;
  dom.version.textContent = meta.version ? 'v' + String(meta.version).replace(/^v/, '') : '';
  dom.projectName.textContent = state.project || 'No project';
  if (!dom.projectMenu.hidden) renderProjectMenu();
  if (!dom.editDialog.open) {
    const sel = dom.editForm.elements.project;
    sel.replaceChildren(...projectList().map((p) => el('option', { value: p, selected: p === state.project }, p)));
  }
  renderStats();
}
const projectList = (extra) => Array.from(new Set([...(state.meta.projects || []), state.project, extra].filter(Boolean)));
function renderProjectMenu() {
  const list = projectList();
  dom.projectMenu.replaceChildren(el('div', { class: 'menu-head' }, 'Projects'), ...list.map((p, i) => el('button', {
    type: 'button', role: 'option', class: 'menu-item', id: 'proj-' + i, 'aria-selected': p === state.project ? 'true' : 'false', 'aria-current': p === state.project ? 'true' : null,
    onclick: () => { setProject(p); toggleProjectMenu(false); },
  }, el('span', { class: 'grid size-4 place-items-center text-accent' }, p === state.project ? icon('check', 12) : null), el('span', { class: 'truncate' }, p),
    el('span', { class: 'num ml-auto text-11 text-fg-3' }, p === state.project ? String(state.all.length || state.tasks.length) : ''))));
}
function toggleProjectMenu(open = dom.projectMenu.hidden) {
  dom.projectMenu.hidden = !open;
  dom.projectBtn.setAttribute('aria-expanded', String(open));
  if (open) {
    renderProjectMenu();
    animate(dom.projectMenu, [{ opacity: 0, transform: 'translateY(-4px)' }, { opacity: 1, transform: 'none' }], 120);
    const cur = dom.projectMenu.querySelector('[aria-selected="true"]') || dom.projectMenu.querySelector('.menu-item');
    if (cur) cur.focus();
  } else if (dom.projectMenu.contains(document.activeElement)) dom.projectBtn.focus();
}
function renderStats() {
  const all = state.all.length || !serverFiltered() ? state.all : state.tasks;
  const open = all.filter(isOpen).length;
  const week = all.filter(quickById('week').test).length;
  const blocked = all.filter((t) => isOpen(t) && t.blocked).length;
  const item = (n, label, quick) => el('button', {
    type: 'button', class: 'btn btn-ghost btn-sm px-1.5 font-normal text-fg-2' + (quick && state.quick.has(quick) ? ' btn-on' : ''), 'aria-pressed': quick ? (state.quick.has(quick) ? 'true' : 'false') : null,
    title: quick ? `Filter: ${quickById(quick).label}` : null, onclick: quick ? () => toggleQuick(quick) : null, disabled: !quick,
  }, el('span', { class: 'num font-medium text-fg' }, n), label);
  dom.stats.replaceChildren(item(open, 'open'), item(week, 'this week', 'week'), item(blocked, 'blocked', 'blocked'));
}

/* ============================== render: filters ============================== */
function renderFilters() {
  renderLabels();
  dom.quick.replaceChildren(...QUICK.map((q) => el('button', { type: 'button', class: 'seg-btn', 'aria-pressed': state.quick.has(q.id) ? 'true' : 'false', onclick: () => toggleQuick(q.id) }, q.label)));
  const chips = [];
  const removable = (label, onRemove, cls) => el('span', { class: 'chip ' + (cls || '') }, el('span', {}, label), el('button', { type: 'button', class: 'x', 'aria-label': `Remove filter ${label}`, onclick: onRemove }, icon('x', 10)));
  if (state.q) chips.push(removable(`“${state.q}”`, () => applySearchText(state.tokens.map((t) => t.raw).join(' '))));
  for (const tok of state.tokens) chips.push(removable(tok.raw, () => removeToken(tok.raw), 'chip-mono'));
  for (const tag of state.tags) chips.push(chipEl(tag, { remove: () => toggleTag(tag) }));
  for (const scope of state.scopes) chips.push(chipEl(scope + '::*', { remove: () => toggleScope(scope) }));
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
  dom.resultCount.textContent = anyFilter() ? (total === null ? `${shown} shown` : `${shown} of ${total}`) : plural(shown, 'task');
}
function allLabels() {
  const labels = (state.meta.labels || []).filter((l) => !isProjectTag(l));
  for (const tag of state.tags) if (!labels.includes(tag)) labels.push(tag);
  for (const t of state.tasks) for (const tag of userTags(t)) if (!labels.includes(tag)) labels.push(tag);
  return labels.sort((a, b) => a.localeCompare(b));
}
// Label filter row: scoped labels grouped under a scope header (click = any scope::*), plain labels after.
function renderLabels() {
  const { scopes, plain } = groupLabels(allLabels());
  const nodes = [];
  for (const [scope, tags] of scopes) {
    nodes.push(el('div', { class: 'flex items-center gap-1', role: 'group', 'aria-label': `${scope} labels` },
      el('button', { type: 'button', class: 'scope-head', style: `--h:${hueOf(scope)}`, 'aria-pressed': state.scopes.has(scope) ? 'true' : 'false', title: `Any ${scope}::*`, onclick: () => toggleScope(scope) }, scope),
      ...tags.map((tag) => chipEl(tag, { short: true, pressed: state.tags.has(tag), onclick: () => toggleTag(tag) }))));
  }
  nodes.push(...plain.map((tag) => chipEl(tag, { pressed: state.tags.has(tag), onclick: () => toggleTag(tag) })));
  dom.labels.replaceChildren(...nodes);
}

/* ============================== render: board ============================== */
function buildBoard() {
  for (const status of STATUSES) {
    const count = el('span', { class: 'col-count' }, '');
    const body = el('div', { class: 'col-body', role: 'listbox', 'aria-multiselectable': 'true', 'aria-label': STATUS_LABEL[status] });
    for (let i = 0; i < 3; i++) body.append(el('div', { class: 'skeleton', style: `height:${i === 1 ? 96 : 76}px`, 'aria-hidden': 'true' }));
    const composer = el('div', { class: 'composer' });
    const col = el('section', { class: 'col', 'data-status': status, style: `--hue: var(--t-${status})`, 'aria-label': STATUS_LABEL[status] },
      el('div', { class: 'col-head' },
        el('h3', { class: 'col-name' }, STATUS_LABEL[status]), count,
        el('div', { class: 'col-tools' },
          el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': `Add task to ${STATUS_LABEL[status]}`, title: 'Add task', onclick: () => openComposer(status) }, icon('plus', 14)),
          el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon collapse-btn', 'aria-label': `Collapse ${STATUS_LABEL[status]}`, title: 'Collapse column', onclick: () => toggleCollapse(status) }, icon('chevL', 14)))),
      body, composer);
    col.addEventListener('dragover', onDragOver);
    col.addEventListener('dragleave', onDragLeave);
    col.addEventListener('drop', onDrop);
    cols[status] = { col, body, count, composer };
    dom.board.append(col);
    renderComposer(status);
  }
  for (const btn of $$('[role="tab"]', dom.segments)) {
    btn.style.setProperty('--hue', `var(--t-${btn.dataset.status})`);
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
const visibleStatuses = () => STATUSES.filter((s) => !cols[s].col.hidden);

function priorityIcon(prio) {
  return svg('svg', { class: `prio-icon p${prio}`, width: 12, height: 10, viewBox: '0 0 12 10', 'aria-label': `Priority ${PRIO_LABEL[prio]}`, role: 'img' },
    svg('rect', { x: 0, y: 6, width: 3, height: 4, rx: 1 }), svg('rect', { x: 4.5, y: 3, width: 3, height: 7, rx: 1 }), svg('rect', { x: 9, y: 0, width: 3, height: 10, rx: 1 }));
}
function progressRing(done, total) {
  const c = 2 * Math.PI * 5;
  return el('span', { class: 'chip ring' + (done === total ? ' complete' : ''), title: `${done} of ${total} checklist items done` },
    svg('svg', { viewBox: '0 0 14 14', 'aria-hidden': 'true' }, svg('circle', { class: 'track', cx: 7, cy: 7, r: 5 }), svg('circle', { class: 'fill', cx: 7, cy: 7, r: 5, 'stroke-dasharray': c.toFixed(2), 'stroke-dashoffset': (c * (1 - done / total)).toFixed(2), 'stroke-linecap': 'round' })),
    el('span', { class: 'num' }, `${done}/${total}`));
}
const TONE = { soon: 'var(--t-warn)', overdue: 'var(--t-danger)' };
function dueChipEl(due, long) {
  const d = dueChip(due);
  return el('span', { class: 'chip chip-tone chip-mono', style: d.tone ? `--tone:${TONE[d.tone]}` : null, title: 'Due ' + due }, icon('calendar', 11), el('span', {}, long ? `${due} · ${d.label}` : d.label));
}

function cardEl(t) {
  const show = settings.show;
  const checks = t.checks || [];
  const doneCount = checks.filter((c) => c.done).length;
  const tags = show.tags ? userTags(t) : [];
  const commentCount = show.comments ? state.commentCounts[t.id] : undefined;
  const selected = state.selected.has(t.id);
  const meta = clean([
    show.due && t.due ? dueChipEl(t.due) : null,
    show.checks && checks.length ? progressRing(doneCount, checks.length) : null,
    commentCount ? el('span', { class: 'chip', title: plural(commentCount, 'comment') }, icon('comment', 11), el('span', { class: 'num' }, commentCount)) : null,
    t.blocked ? el('span', { class: 'chip chip-tone', style: '--tone: var(--t-danger)', title: 'Blocked' }, icon('blocked', 11), el('span', {}, 'Blocked')) : null,
    show.effort && t.effort ? el('span', { class: 'chip chip-mono', title: 'Effort ' + t.effort }, el('span', {}, t.effort)) : null,
  ]);
  const quick = (name, label, run) => el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': label, title: label, tabindex: '-1', onclick: (e) => { e.stopPropagation(); run(); } }, icon(name, 14));
  const actions = clean([
    quick('open', 'Open', () => openDetail(t.seq)),
    isOpen(t) ? quick('ship', 'Ship', () => shipTask(t)) : null,
    t.status !== 'cancelled' ? quick('cancel', 'Cancel task', () => cancelTask(t)) : null,
  ]);
  const card = el('article', {
    class: 'card' + (selected ? ' is-selected' : '') + (state.focusId === t.id ? ' is-focused' : '') + (state.lifted === t.id ? ' is-lifted' : ''),
    draggable: 'true', tabindex: state.focusId === t.id ? '0' : '-1', role: 'option', 'aria-selected': selected ? 'true' : 'false',
    'data-id': t.id, 'data-seq': t.seq, 'data-status': t.status, 'aria-label': `#${t.seq} ${t.title}`,
    onclick: (e) => onCardClick(e, t),
    onfocus: () => { if (state.focusId !== t.id) setFocus(t.id, { focus: false }); },
    ondragstart: onDragStart, ondragend: cleanupDrag, onpointerdown: onCardPointerDown,
  },
    el('div', { class: 'flex items-start gap-2' },
      el('span', { class: 'grid h-5 w-3 flex-none place-items-center' }, priorityIcon(t.prio || 3)),
      show.seq ? el('span', { class: 'seq flex-none leading-5' }, '#' + t.seq) : null,
      el('div', { class: 'card-title min-w-0 flex-1' }, show.emoji && t.emoji ? t.emoji + ' ' : null, mdInline(t.title))),
    show.desc && t.desc && t.desc.trim() ? renderMarkdown(t.desc, { mode: 'card' }) : null,
    meta.length ? el('div', { class: 'flex flex-wrap items-center gap-1' }, ...meta) : null,
    tags.length ? el('div', { class: 'flex flex-wrap gap-1' }, ...tags.map((tag) => chipEl(tag, { tabindex: '-1', pressed: state.tags.has(tag), title: 'Filter by ' + tag, onclick: () => toggleTag(tag) }))) : null,
    el('div', { class: 'card-actions' }, ...actions),
  );
  return card;
}
function emptyEl(status) {
  const msg = anyFilter() ? ['No matches'] : status === 'todo' ? ['Press ', el('kbd', { class: 'kbd' }, 'n'), ' or click ', el('span', { class: 'font-medium text-fg-2' }, '+'), ' to add a task'] : status === 'doing' ? ['Drag a card here to start it'] : ['Nothing here yet'];
  return el('div', { class: 'col-empty' }, ...msg);
}
function leaveCard(card, rect) {
  card.classList.add('leaving');
  card.style.cssText = `left:${rect.left}px;top:${rect.top}px;width:${rect.width}px`;
  document.body.append(card);
  const a = animate(card, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.96)' }], 160);
  a.onfinish = a.oncancel = () => card.remove();
}
function tickCounter(node, text) {
  if (node.textContent === text) return;
  node.textContent = text;
  animate(node, [{ transform: 'translateY(6px)', opacity: 0 }, { transform: 'none', opacity: 1 }], 160);
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
    col.classList.toggle('is-over', over);
    tickCounter(count, limit ? `${tasks.length}/${limit}` : String(tasks.length));
    count.title = over ? `Over the WIP limit of ${limit}` : limit ? `WIP limit ${limit}` : '';
    const seg = dom.segments.querySelector(`[data-status="${status}"] .seg-count`);
    if (seg) tickCounter(seg, String(tasks.length));
    const hidden = (status === 'cancelled' && !settings.showCancelled) || (settings.hideEmpty && tasks.length === 0 && !anyFilter());
    col.hidden = hidden;
    const collapsed = !!settings.collapsed[status] && !phoneMQ.matches;
    col.classList.toggle('is-collapsed', collapsed);
    const cbtn = col.querySelector('.collapse-btn');
    cbtn.setAttribute('aria-label', `${collapsed ? 'Expand' : 'Collapse'} ${STATUS_LABEL[status]}`);
    cbtn.setAttribute('aria-expanded', String(!collapsed));
    cbtn.replaceChildren(icon(collapsed ? 'chevR' : 'chevL', 14));
    if (!hidden) template.push(collapsed ? '44px' : 'minmax(240px, 1fr)');
  }
  dom.board.style.gridTemplateColumns = wideMQ.matches ? template.join(' ') : '';
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
    } else animate(c, [{ opacity: 0, transform: 'translateY(4px)' }, { opacity: 1, transform: 'none' }], 160, { delay: state.firstPaint ? Math.min(i * 10, 200) : 0 });
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
  document.body.classList.add('select-none');
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
  setTimeout(() => { for (const id of ids) { const n = cardNode(id); if (n) { n.classList.add('is-dragging'); n.inert = true; } } }, 0);
}
function placeSlot(body, y) {
  if (!state.dragging) return;
  const cards = $$('.card[data-id]:not(.is-dragging)', body);
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
    if (node.classList.contains('card') && node.dataset.id && !node.classList.contains('is-dragging')) index++;
  }
  return index; // no slot (collapsed column): append
}
function hoverColumn(col, x, y) {
  for (const c of $$('.col.is-target', dom.board)) if (c !== col) c.classList.remove('is-target');
  if (!col) { dropSlot.remove(); return; }
  col.classList.add('is-target');
  if (!col.classList.contains('is-collapsed')) placeSlot(cols[col.dataset.status].body, y);
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
  col.classList.remove('is-target');
  if (dropSlot.parentNode === cols[col.dataset.status].body) dropSlot.remove();
}
function onDrop(e) {
  if (!state.dragging) return;
  e.preventDefault();
  dropOn(e.currentTarget);
}
function dropOn(col) {
  const status = col.dataset.status;
  const index = col.classList.contains('is-collapsed') ? undefined : slotIndex(cols[status].body);
  const ids = state.dragging.ids;
  cleanupDrag();
  moveMany(ids, status, index);
}
function cleanupDrag() {
  const was = !!state.dragging;
  state.dragging = null;
  state.dragPos = null;
  document.body.classList.remove('select-none');
  cancelAnimationFrame(state.autoScrollRAF);
  dropSlot.remove();
  for (const c of $$('.card.is-dragging', dom.board)) { c.classList.remove('is-dragging'); c.inert = false; }
  for (const c of $$('.col.is-target', dom.board)) c.classList.remove('is-target');
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
  if (ids.length > 1) { ghost.classList.add('stack'); ghost.dataset.count = ids.length; }
  ghost.style.width = r.width + 'px';
  ghost.style.left = r.left + 'px';
  ghost.style.top = r.top + 'px';
  document.body.append(ghost);
  animate(ghost, [{ transform: 'scale(1) rotate(0)' }, { transform: 'scale(1.03) rotate(2deg)' }], 150, { fill: 'forwards' });
  if (navigator.vibrate) navigator.vibrate(10);
  startDrag(ids, r.height);
  state.touch = { ghost, dx: start.x - r.left, dy: start.y - r.top, pointerId, col: null };
  try { card.setPointerCapture(pointerId); } catch (err) { /* ignore */ }
  for (const id of ids) { const n = cardNode(id); if (n) n.classList.add('is-dragging'); }
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
  const label = moved === 1 ? `#${findTask(ids[0]).seq}` : plural(moved, 'task');
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
  if (prev && prev.dataset.id !== id) { prev.classList.remove('is-focused'); prev.tabIndex = -1; }
  state.focusId = id;
  const node = id ? cardNode(id) : null;
  if (!node) return;
  node.classList.add('is-focused');
  node.tabIndex = 0;
  if (opts.focus !== false) node.focus({ preventScroll: true });
  node.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: dur(1) ? 'smooth' : 'auto' });
}
const columnCards = (status) => $$('.card[data-id]', cols[status].body);
function focusedCard() { return state.focusId ? cardNode(state.focusId) : null; }
function moveFocus(dRow, dCol) {
  const statuses = visibleStatuses().filter((s) => !cols[s].col.classList.contains('is-collapsed'));
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
  card.classList.add('is-lifted');
  announce(`Lifted #${t.seq}. Move with h, j, k, l; Space drops; Escape cancels.`);
}
function moveLifted(dRow, dCol) {
  const t = findTask(state.lifted);
  if (!t) return;
  const groups = groupTasks();
  let status = t.status, index = groups[status].indexOf(t);
  if (dCol) {
    const statuses = visibleStatuses().filter((s) => !cols[s].col.classList.contains('is-collapsed'));
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
  if (node) node.classList.remove('is-lifted');
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
  if (e.target.closest('a[href]')) return; // markdown links inside the card open in a new tab
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
  announce(`Selected ${plural(state.selected.size, 'task')}`);
}
function clearSelection() {
  if (!state.selected.size) return;
  state.selected.clear();
  renderBoard();
}
function renderBulkBar() {
  const n = state.selected.size;
  const was = !dom.bulkbar.hidden;
  dom.bulkbar.hidden = n === 0;
  if (!n) return;
  const ids = () => orderedSelection();
  const moveSel = el('select', { class: 'input w-auto', 'aria-label': 'Move selection to column', onchange: (e) => { if (e.target.value) { moveMany(ids(), e.target.value); clearSelection(); } } },
    el('option', { value: '' }, 'Move to…'), ...STATUSES.map((s) => el('option', { value: s }, STATUS_LABEL[s])));
  const prioSel = el('select', { class: 'input w-auto', 'aria-label': 'Set priority', onchange: (e) => { if (e.target.value) bulkPatch(ids(), () => ({ prio: Number(e.target.value) }), `Priority ${PRIO_LABEL[e.target.value]}`); } },
    el('option', { value: '' }, 'Priority…'), ...[1, 2, 3].map((p) => el('option', { value: p }, PRIO_LABEL[p])));
  const labelBox = el('div', { class: 'relative w-[200px]' });
  labelEditor(labelBox, { tags: [], placeholder: 'Add label…', single: true, onPick: (tag) => bulkPatch(ids(), (t) => ({ tags: withLabel(userTags(t), tag) }), `Label ${tag}`) });
  dom.bulkbar.replaceChildren(
    el('span', { class: 'text-13' }, el('span', { class: 'num font-semibold' }, n), ' selected'),
    moveSel, prioSel, labelBox,
    el('button', { type: 'button', class: 'btn', onclick: () => bulkCancel(ids()) }, 'Cancel tasks'),
    el('button', { type: 'button', class: 'btn btn-ghost btn-icon', 'aria-label': 'Clear selection', title: 'Clear selection (Esc)', onclick: clearSelection }, icon('x')),
  );
  if (!was) animate(dom.bulkbar, [{ opacity: 0, transform: 'translate(-50%, 8px)' }, { opacity: 1, transform: 'translate(-50%, 0)' }], 160);
}
async function bulkPatch(ids, patchFor, label) {
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
async function bulkCancel(ids) {
  if (!confirm(`Cancel ${plural(ids.length, 'task')}?`)) return;
  const done = [];
  for (const id of ids) {
    try { await api('POST', taskPath(id) + '/cancel', {}); done.push(id); } catch (err) { toast(err.message, 'error'); break; }
  }
  clearSelection();
  invalidate();
  if (done.length) toast(`Cancelled ${plural(done.length, 'task')}`, 'ok', { life: 6000, action: { label: 'Undo', run: async () => { for (const id of done) { try { await api('POST', taskPath(id) + '/restore'); } catch (err) { toast(err.message, 'error'); break; } } invalidate(); } } });
}
async function setPriority(t, prio) {
  if (t.prio === prio) return;
  await mutate(() => api('PATCH', taskPath(t.id), { prio }), `#${t.seq} priority ${PRIO_LABEL[prio]}`);
  if (state.detail) loadDetail(state.detail, true);
}

/* ============================== markdown editor ============================== */
const LIST_RE = /^(\s*)([-*+]|\d+[.)])\s(\[[ xX]\]\s)?/;
// mdEditor({value, placeholder, label, onSave, onCancel, saveLabel, rows}) -> {root, ta, value, set, focus, dirty}
function mdEditor(opts = {}) {
  const initial = opts.value || '';
  const ta = el('textarea', { class: 'editor-ta', placeholder: opts.placeholder || 'Write markdown…', 'aria-label': opts.label || 'Markdown editor', rows: String(opts.rows || 4), spellcheck: 'true', autocomplete: 'off' });
  ta.value = initial;
  const preview = el('div', { class: 'editor-preview', 'aria-live': 'polite', 'aria-label': 'Preview' });
  const root = el('div', { class: 'editor', 'data-view': 'write' });
  let timer = 0;
  const refresh = () => {
    clearTimeout(timer);
    timer = setTimeout(() => preview.replaceChildren(renderMarkdown(ta.value, { empty: 'Nothing to preview yet' })), 100);
    ta.style.height = 'auto';
    ta.style.height = Math.max(ta.scrollHeight, 120) + 'px';
  };
  const sel = () => ({ s: ta.selectionStart, e: ta.selectionEnd, v: ta.value });
  const replaceRange = (s, e, text, selStart, selEnd) => { ta.setSelectionRange(s, e); insertText(ta, text); ta.setSelectionRange(selStart, selEnd); refresh(); };
  const wrap = (before, after, placeholder) => {
    const { s, e, v } = sel();
    const inner = v.slice(s, e) || placeholder || '';
    if (v.slice(s - before.length, s) === before && v.slice(e, e + after.length) === after) { replaceRange(s - before.length, e + after.length, inner, s - before.length, s - before.length + inner.length); return; }
    replaceRange(s, e, before + inner + after, s + before.length, s + before.length + inner.length);
  };
  const lineRange = () => { const { s, e, v } = sel(); const ls = v.lastIndexOf('\n', s - 1) + 1; let le = v.indexOf('\n', e); if (le < 0) le = v.length; return { ls, le, text: v.slice(ls, le) }; };
  const mapLines = (fn) => { const { ls, le, text } = lineRange(); const out = text.split('\n').map(fn).join('\n'); replaceRange(ls, le, out, ls, ls + out.length); };
  const togglePrefix = (prefix, re) => {
    const all = lineRange().text.split('\n').every((l) => re.test(l));
    mapLines((l) => (all ? l.replace(re, '') : re.test(l) ? l : prefix + l));
  };
  const tools = {
    heading: () => mapLines((l) => { const m = /^(#{1,6})\s/.exec(l); if (!m) return '# ' + l; if (m[1].length >= 3) return l.slice(m[0].length); return '#' + l; }),
    bold: () => wrap('**', '**', 'bold'), italic: () => wrap('_', '_', 'italic'), strike: () => wrap('~~', '~~', 'text'), code: () => wrap('`', '`', 'code'),
    codeblock: () => { const { s, e, v } = sel(); const inner = v.slice(s, e); const lead = s > 0 && v[s - 1] !== '\n' ? '\n' : ''; replaceRange(s, e, `${lead}\`\`\`\n${inner}\n\`\`\``, s + lead.length + 4, s + lead.length + 4 + inner.length); },
    link: () => {
      const { s, e, v } = sel();
      const inner = v.slice(s, e);
      if (/^https?:\/\/\S+$/.test(inner)) { replaceRange(s, e, `[text](${inner})`, s + 1, s + 5); return; }
      const label = inner || 'text';
      const out = `[${label}](url)`;
      replaceRange(s, e, out, s + label.length + 3, s + label.length + 6);
    },
    quote: () => togglePrefix('> ', /^>\s?/), ul: () => togglePrefix('- ', /^\s*[-*+]\s(?!\[)/), task: () => togglePrefix('- [ ] ', /^\s*[-*+]\s\[[ xX]\]\s/),
    ol: () => { const all = lineRange().text.split('\n').every((l) => /^\s*\d+[.)]\s/.test(l)); mapLines((l, i) => (all ? l.replace(/^\s*\d+[.)]\s/, '') : /^\s*\d+[.)]\s/.test(l) ? l : `${i + 1}. ${l}`)); },
  };
  const tool = (name, label, kbd) => el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': label + (kbd ? ` (${kbd})` : ''), title: label + (kbd ? `  ${kbd}` : ''), tabindex: '-1', onmousedown: (e) => e.preventDefault(), onclick: () => tools[name]() }, icon(name === 'heading' ? 'h' : name, 14));
  // Below 640px the panes stack: one toggle switches between writing and the preview.
  const tabs = el('button', { type: 'button', class: 'editor-tabs btn btn-ghost btn-xs', 'aria-pressed': 'false', 'aria-label': 'Preview', title: 'Preview', onmousedown: (e) => e.preventDefault(), onclick: () => {
    const preview = root.dataset.view !== 'preview';
    root.dataset.view = preview ? 'preview' : 'write';
    tabs.setAttribute('aria-pressed', String(preview));
    tabs.replaceChildren(icon(preview ? 'pencil' : 'eye', 14), preview ? 'Write' : 'Preview');
    if (!preview) ta.focus();
  } }, icon('eye', 14), 'Preview');
  const sep = () => el('span', { class: 'sep', 'aria-hidden': 'true' });
  const bar = el('div', { class: 'editor-bar', role: 'toolbar', 'aria-label': 'Formatting' },
    tool('heading', 'Heading'), tool('bold', 'Bold', MOD + ' B'), tool('italic', 'Italic', MOD + ' I'), tool('strike', 'Strikethrough'), sep(),
    tool('code', 'Inline code', MOD + ' E'), tool('codeblock', 'Code block'), tool('link', 'Link', MOD + ' K'), tool('quote', 'Quote'), sep(),
    tool('ul', 'Bullet list'), tool('ol', 'Numbered list'), tool('task', 'Task list'), tabs);
  const foot = opts.onSave ? el('div', { class: 'editor-foot' },
    el('span', { class: 'phone-hide text-11 text-fg-3' }, el('kbd', { class: 'kbd' }, MOD), ' ', el('kbd', { class: 'kbd' }, 'Enter'), ' saves', opts.onCancel ? [' · ', el('kbd', { class: 'kbd' }, 'Esc'), ' cancels'] : null),
    el('span', { class: 'flex-1' }),
    opts.onCancel ? el('button', { type: 'button', class: 'btn btn-ghost btn-sm', onclick: () => cancel() }, 'Cancel') : null,
    el('button', { type: 'button', class: 'btn btn-primary btn-sm', onclick: () => save() }, opts.saveLabel || 'Save')) : null;
  root.append(...clean([bar, el('div', { class: 'editor-body' }, ta, preview), foot]));
  const api_ = { root, ta, get value() { return ta.value; }, set(v) { ta.value = v; refresh(); }, focus() { refresh(); ta.focus(); ta.setSelectionRange(0, 0); ta.scrollTop = 0; }, dirty: () => ta.value !== initial, refresh };
  const save = () => { if (opts.onSave) opts.onSave(ta.value, api_); };
  const cancel = () => { if (!opts.onCancel) return false; if (api_.dirty() && !confirm('Discard changes?')) return true; opts.onCancel(api_); return true; };
  ta.addEventListener('input', () => { refresh(); root.classList.toggle('is-dirty', ta.value !== initial); });
  ta.addEventListener('keydown', (e) => {
    const mod = e.ctrlKey || e.metaKey;
    if (mod && !e.shiftKey && !e.altKey) {
      const k = e.key.toLowerCase();
      if (k === 'b') { e.preventDefault(); e.stopPropagation(); tools.bold(); return; }
      if (k === 'i') { e.preventDefault(); e.stopPropagation(); tools.italic(); return; }
      if (k === 'k') { e.preventDefault(); e.stopPropagation(); tools.link(); return; }
      if (k === 'e') { e.preventDefault(); e.stopPropagation(); tools.code(); return; }
      if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); save(); return; }
      return;
    }
    if (e.key === 'Escape') { if (opts.onCancel) { e.preventDefault(); e.stopPropagation(); cancel(); } return; }
    if (e.key === 'Enter' && !e.shiftKey && !e.altKey) {
      const { s, e: end, v } = sel();
      if (s !== end) return;
      const ls = v.lastIndexOf('\n', s - 1) + 1;
      const line = v.slice(ls, s);
      const m = LIST_RE.exec(line);
      if (!m) return;
      e.preventDefault();
      if (line.trim() === m[0].trim()) { replaceRange(ls, s, '', ls, ls); return; } // empty item ends the list
      const marker = /^\d+/.test(m[2]) ? String(Number(m[2]) + 1) + m[2].slice(-1) : m[2];
      const next = `\n${m[1]}${marker} ${m[3] ? '[ ] ' : ''}`;
      replaceRange(s, s, next, s + next.length, s + next.length);
      return;
    }
    if (e.key === 'Tab' && !e.altKey && !mod) {
      const { text } = lineRange();
      if (!text.split('\n').every((l) => LIST_RE.test(l))) return;
      e.preventDefault();
      const { s, e: end } = sel();
      const single = s === end;
      const before = lineRange().text.length;
      mapLines((l) => (e.shiftKey ? l.replace(/^ {1,2}/, '') : '  ' + l));
      if (single) { const delta = lineRange().text.length - before; ta.setSelectionRange(s + delta, s + delta); }
      return;
    }
    if (e.key === '`' && !mod && !e.altKey) {
      const { s, e: end, v } = sel();
      if (s !== end) { e.preventDefault(); wrap('`', '`'); return; }
      if (v[s] === '`') { e.preventDefault(); ta.setSelectionRange(s + 1, s + 1); return; }
      if (v[s - 1] === '`' && v[s - 2] === '`') return; // typing a fence
      e.preventDefault();
      replaceRange(s, s, '``', s + 1, s + 1);
    }
  });
  refresh();
  requestAnimationFrame(refresh); // size again once mounted
  return api_;
}

/* ============================== label editor ============================== */
// labelEditor(container, {tags, onChange, onPick, placeholder, single}) renders removable chips, an input and a
// grouped picker fed by /api/meta labels. One value per scope: picking type::bug replaces type::feature.
function labelEditor(container, opts = {}) {
  let tags = normalizeTags(opts.tags || []);
  const row = el('div', { class: 'flex min-h-8 flex-wrap items-center gap-1' });
  const input = el('input', { type: 'text', class: 'input input-ghost h-7 min-w-[120px] flex-1 px-1.5', placeholder: opts.placeholder || 'Add label…', 'aria-label': opts.placeholder || 'Add label', autocomplete: 'off', spellcheck: 'false', role: 'combobox', 'aria-expanded': 'false', 'aria-autocomplete': 'list' });
  const pick = el('div', { class: 'pick', role: 'listbox', hidden: true, 'aria-label': 'Labels' });
  const box = el('div', { class: 'relative' }, row, pick);
  container.replaceChildren(box);
  row.append(input);
  let items = [], index = 0;
  const chip = (tag) => chipEl(tag, { remove: () => setTags(tags.filter((t) => t !== tag)) });
  const drawChips = () => renderChipRow(row, tags, chip);
  const setTags = (next) => { tags = normalizeTags(next); drawChips(); if (opts.onChange) opts.onChange(tags); };
  const choose = (tag) => {
    input.value = '';
    close();
    if (opts.single) { if (opts.onPick) opts.onPick(tag); return; }
    setTags(withLabel(tags, tag));
    input.focus();
  };
  const close = () => { pick.hidden = true; input.setAttribute('aria-expanded', 'false'); input.removeAttribute('aria-activedescendant'); };
  const candidates = () => {
    const q = input.value.trim();
    const ql = q.toLowerCase();
    const pool = allLabels().filter((l) => !tags.includes(l));
    let list;
    if (!ql) list = pool;
    else if (ql.includes('::')) { const [scope, value] = ql.split('::'); list = pool.filter((l) => { const p = splitLabel(l); return p.scope.toLowerCase() === scope && (!value || p.value.toLowerCase().includes(value)); }); }
    else list = pool.map((l) => ({ l, m: fuzzy(ql, l) })).filter((x) => x.m).sort((a, b) => b.m.score - a.m.score).map((x) => x.l);
    const out = [];
    const { scopes, plain } = groupLabels(list);
    for (const [scope, ts] of scopes) { out.push({ head: scope }); for (const t of ts) out.push({ tag: t }); }
    if (plain.length) { if (scopes.size) out.push({ head: 'Other' }); for (const t of plain) out.push({ tag: t }); }
    if (q && !/\s/.test(q) && !pool.includes(q) && !tags.includes(q) && !isProjectTag(q) && q !== '::' && !q.endsWith('::')) out.push({ create: q });
    return out;
  };
  const render = () => {
    items = candidates().filter((x) => !x.head);
    index = 0;
    const all = candidates();
    if (!all.length) { close(); return; }
    let i = 0;
    pick.replaceChildren(...all.map((x) => {
      if (x.head) return el('div', { class: 'menu-head' }, x.head);
      const k = i++;
      const node = el('div', { class: 'menu-item h-8', role: 'option', id: `${input.id || 'lbl'}-opt-${k}`, 'aria-selected': k === 0 ? 'true' : 'false', onmousedown: (e) => e.preventDefault(), onclick: () => choose(x.tag || x.create), onmousemove: () => highlight(k) },
        x.create ? [icon('plus', 12), el('span', {}, 'Create '), chipEl(x.create)] : chipEl(x.tag));
      return node;
    }));
    pick.hidden = false;
    input.setAttribute('aria-expanded', 'true');
    highlight(0);
  };
  const highlight = (k) => {
    if (!items.length) return;
    index = Math.max(0, Math.min(items.length - 1, k));
    const opt = $$('[role="option"]', pick);
    opt.forEach((o, i) => o.setAttribute('aria-selected', String(i === index)));
    if (opt[index]) { opt[index].scrollIntoView({ block: 'nearest' }); input.setAttribute('aria-activedescendant', opt[index].id); }
  };
  input.addEventListener('input', render);
  input.addEventListener('focus', render);
  input.addEventListener('blur', () => setTimeout(close, 120));
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); if (pick.hidden) render(); else highlight(index + 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); highlight(index - 1); }
    else if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); const it = items[index]; if (it) choose(it.tag || it.create); else if (input.value.trim()) choose(input.value.trim()); }
    else if (e.key === 'Escape') { if (!pick.hidden) { e.preventDefault(); e.stopPropagation(); close(); } else if (input.value) { e.preventDefault(); e.stopPropagation(); input.value = ''; } }
    else if (e.key === 'Backspace' && !input.value && tags.length && !opts.single) { e.preventDefault(); setTags(tags.slice(0, -1)); }
    else if (e.key === ',' ) { e.preventDefault(); if (input.value.trim()) choose(input.value.trim()); }
  });
  drawChips();
  return { get tags() { return tags; }, set: (next) => { tags = normalizeTags(next); drawChips(); }, input, focus: () => input.focus() };
}

/* ============================== detail panel ============================== */
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
    if (open && !dom.detailDialog.open) { showDialog(dom.detailDialog); dom.detailBody.focus({ preventScroll: true }); }
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
    state.detailPending = null;
    dom.detailSeq.replaceChildren('#' + key);
    dom.detailTitle.replaceChildren(el('span', { class: 'skeleton inline-block h-5 w-2/3 align-middle' }));
    dom.detailBody.replaceChildren(el('div', { class: 'skeleton h-8 w-full' }), el('div', { class: 'skeleton mt-2 h-8 w-4/5' }), el('div', { class: 'skeleton mt-6 h-24 w-full' }));
    dom.detailActions.replaceChildren();
  }
  const wasHidden = dom.detail.hidden;
  mountDetail();
  if (wasHidden && wideMQ.matches) animate(dom.panel, [{ opacity: 0, transform: 'translateX(12px)' }, { opacity: 1, transform: 'none' }], 180);
  if (!opts.route && location.hash !== '#/t/' + key) location.hash = '#/t/' + key;
  loadDetail(key).then(() => { if (opts.focusComment) { const ta = dom.detailBody.querySelector('.comment-editor textarea'); if (ta) ta.focus(); } });
}
function closeDetail(opts = {}) {
  if (!state.detail) return;
  if (panelDirty() && !confirm('Discard unsaved changes?')) return;
  const t = state.detailData && state.detailData.task;
  state.detail = null;
  state.detailJSON = '';
  state.detailData = null;
  state.detailPending = null;
  mountDetail();
  if (!opts.route && /^#\/t\//.test(location.hash)) history.replaceState(null, '', location.pathname + location.search + '#/');
  const back = t ? cardNode(t.id) : null;
  if (back) setFocus(t.id); else dom.board.focus({ preventScroll: true });
}
// busy: an inline editor is open (do not repaint under it). dirty: it holds unsaved changes (confirm before closing).
const panelBusy = () => !!dom.detail.querySelector('[data-busy]');
const panelDirty = () => !!dom.detail.querySelector('[data-busy]:not(.editor), .editor[data-busy].is-dirty');
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
    if (panelBusy()) { state.detailPending = data; return; } // an editor is open: re-render after it closes
    renderDetail(data);
  } catch (err) {
    if (!silent) toast(err.message, 'error');
    if (err.status === 404) closeDetail();
  }
}
// Called when an inline editor closes: paint whatever arrived meanwhile.
function detailIdle() {
  if (state.detailPending && !panelBusy()) { const d = state.detailPending; state.detailPending = null; renderDetail(d); }
}
function taskLink(t) {
  return el('button', { type: 'button', class: 'task-link', style: `--hue: var(--t-${t.status})`, title: `#${t.seq} ${t.title}`, onclick: () => openDetail(t.seq) },
    el('i', { class: 'status-dot' }), el('span', { class: 'seq' }, '#' + t.seq), el('span', { class: 't ' + t.status }, mdInline(t.title)));
}
const sectionEl = (title, extra, ...children) => el('section', { class: 'mt-5' },
  el('div', { class: 'mb-2 flex h-6 items-center gap-2' }, el('h3', { class: 'label-11' }, title), ...clean([extra].flat())), ...children);

function renderDetail(data) {
  const { task, comments = [], links = {} } = data;
  const draft = dom.detailBody.querySelector('.comment-editor textarea');
  const draftText = draft ? draft.value : '';
  const hadFocus = draft && document.activeElement === draft;
  const scrollTop = dom.detailBody.scrollTop;
  const save = (patch, msg) => mutate(() => patchTask(task.id, patch), msg).then((out) => { if (out) loadDetail(state.detail, true); return out; });

  // header: #seq, status, title (click to edit)
  dom.detailSeq.replaceChildren(...clean([el('span', {}, '#' + task.seq),
    el('span', { class: 'chip chip-dot', style: `--dot: var(--t-${task.status})` }, el('span', {}, STATUS_LABEL[task.status] || task.status)),
    task.blocked ? el('span', { class: 'chip chip-tone', style: '--tone: var(--t-danger)' }, icon('blocked', 11), el('span', {}, 'Blocked')) : null]));
  const titleView = el('button', { type: 'button', class: 'title-edit text-left', title: 'Edit title', onclick: () => editTitle() }, task.emoji ? task.emoji + ' ' : null, mdInline(task.title));
  dom.detailTitle.replaceChildren(titleView);
  function editTitle() {
    const ta = el('textarea', { class: 'title-edit resize-none', rows: '1', 'aria-label': 'Title', 'data-busy': '', value: task.title, spellcheck: 'true' });
    const size = () => { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 'px'; };
    const done = async (commit) => {
      const v = ta.value.trim();
      dom.detailTitle.replaceChildren(titleView);
      if (commit && v && v !== task.title) await save({ title: v }, `Saved #${task.seq}`);
      detailIdle();
    };
    ta.addEventListener('input', size);
    ta.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); done(true); } else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); done(false); } });
    ta.addEventListener('blur', () => done(true));
    dom.detailTitle.replaceChildren(ta);
    size();
    ta.focus();
    ta.select();
  }

  // property grid
  const statusSeg = el('div', { class: 'seg seg-sm', role: 'group', 'aria-label': 'Status' }, ...STATUSES.map((s) => el('button', {
    type: 'button', class: 'seg-btn', 'data-hue': '', style: `--hue: var(--t-${s})`, 'aria-pressed': task.status === s ? 'true' : 'false', onclick: () => { if (s !== task.status) moveMany([task.id], s); },
  }, STATUS_LABEL[s])));
  const prioSel = el('select', { class: 'input w-auto', 'aria-label': 'Priority', onchange: (e) => save({ prio: Number(e.target.value) }, `#${task.seq} priority ${PRIO_LABEL[e.target.value]}`) },
    ...[1, 2, 3].map((p) => el('option', { value: p, selected: (task.prio || 3) === p }, PRIO_LABEL[p])));
  const dueInput = el('input', { type: 'date', class: 'input num w-auto', 'aria-label': 'Due date', value: task.due || '', onchange: (e) => save({ due: e.target.value }, e.target.value ? `#${task.seq} due ${e.target.value}` : `#${task.seq} due date cleared`) });
  const effortSeg = el('div', { class: 'seg seg-sm', role: 'group', 'aria-label': 'Effort' }, ...[['', '—'], ...EFFORTS.map((x) => [x, x])].map(([v, label]) => el('button', {
    type: 'button', class: 'seg-btn num', 'aria-pressed': (task.effort || '') === v ? 'true' : 'false', onclick: () => { if ((task.effort || '') !== v) save({ effort: v }, v ? `#${task.seq} effort ${v}` : `#${task.seq} effort cleared`); },
  }, label)));
  const labelsBox = el('div', { class: 'w-full' });
  labelEditor(labelsBox, { tags: userTags(task), onChange: (tags) => save({ tags }, `Labels saved on #${task.seq}`) });
  const blockedSwitch = el('button', { type: 'button', role: 'switch', class: 'switch', 'aria-checked': task.blocked ? 'true' : 'false', 'aria-label': 'Blocked', onclick: () => save({ blocked: !task.blocked }, task.blocked ? `#${task.seq} unblocked` : `#${task.seq} marked blocked`) });
  const projectSel = el('select', { class: 'input w-auto', 'aria-label': 'Project', onchange: (e) => save({ project: e.target.value }, `#${task.seq} moved to ${e.target.value}`) },
    ...projectList(task.project).map((p) => el('option', { value: p, selected: p === task.project }, p)));
  const props = el('dl', { class: 'props' },
    el('dt', {}, 'Status'), el('dd', {}, statusSeg),
    el('dt', {}, 'Priority'), el('dd', {}, el('span', { class: 'grid h-5 w-3 place-items-center' }, priorityIcon(task.prio || 3)), prioSel),
    el('dt', {}, 'Due'), el('dd', {}, dueInput, task.due ? dueChipEl(task.due) : null),
    el('dt', {}, 'Effort'), el('dd', {}, effortSeg),
    el('dt', {}, 'Labels'), el('dd', {}, labelsBox),
    el('dt', {}, 'Blocked'), el('dd', {}, blockedSwitch, el('span', { class: 'text-12 text-fg-3' }, task.blocked ? 'Finishing needs --force' : 'No')),
    el('dt', {}, 'Project'), el('dd', {}, projectSel),
    el('dt', {}, 'Created'), el('dd', { class: 'text-12 text-fg-2' }, el('time', { class: 'num', datetime: task.createdAt, title: fmtDate(task.createdAt) }, relTime(task.createdAt)),
      task.movedAt && task.movedAt !== task.createdAt ? el('span', { class: 'text-fg-3' }, ' · moved ', el('time', { class: 'num', datetime: task.movedAt, title: fmtDate(task.movedAt) }, relTime(task.movedAt))) : null),
  );

  // description: rendered markdown, click to edit
  const descBox = el('div', {});
  const showDesc = () => {
    const view = renderMarkdown(task.desc, { empty: 'No description. Click to add one.' });
    view.classList.add('cursor-text', 'rounded-md', '-mx-2', 'px-2', 'py-1', 'hover:bg-raised');
    view.addEventListener('click', (e) => { if (!e.target.closest('a, button')) editDesc(); });
    descBox.replaceChildren(view);
  };
  const editDesc = () => {
    const ed = mdEditor({ value: task.desc || '', label: 'Description', placeholder: 'Describe the task in markdown…', rows: 6,
      onSave: async (v) => { if (v !== (task.desc || '')) { const out = await save({ desc: v }, `Saved #${task.seq}`); if (out === undefined) return; task.desc = v; } showDesc(); detailIdle(); },
      onCancel: () => { showDesc(); detailIdle(); } });
    ed.root.dataset.busy = '';
    descBox.replaceChildren(ed.root);
    ed.focus();
  };
  showDesc();
  const descSection = sectionEl('Description', el('button', { type: 'button', class: 'btn btn-ghost btn-xs ml-auto', onclick: editDesc }, icon('pencil', 12), 'Edit'), descBox);

  // checklist
  const checks = task.checks || [];
  const doneCount = checks.filter((c) => c.done).length;
  const saveChecks = (next, msg) => save({ checks: next.map((c) => ({ text: c.text, done: !!c.done })) }, msg);
  const checkRow = (c, i) => {
    const cb = el('input', { type: 'checkbox', class: 'cb', checked: !!c.done, 'aria-label': c.text, onchange: (e) => { row.classList.toggle('done', e.target.checked); saveChecks(checks.map((x, j) => (j === i ? { text: x.text, done: e.target.checked } : x)), ''); } });
    const text = el('button', { type: 'button', class: 'text min-w-0 text-left', title: 'Edit item', onclick: () => editItem() }, mdInline(c.text));
    const row = el('li', { class: 'check-row' + (c.done ? ' done' : '') }, cb, text,
      el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon rm', 'aria-label': `Remove “${c.text}”`, onclick: () => saveChecks(checks.filter((_, j) => j !== i), 'Checklist item removed') }, icon('x', 12)));
    function editItem() {
      const inp = el('input', { type: 'text', class: 'input h-7 flex-1 px-2', value: c.text, 'aria-label': 'Checklist item', 'data-busy': '' });
      const done = (commit) => { const v = inp.value.trim(); inp.replaceWith(text); if (commit && v && v !== c.text) saveChecks(checks.map((x, j) => (j === i ? { text: v, done: x.done } : x)), ''); else if (commit && !v) saveChecks(checks.filter((_, j) => j !== i), 'Checklist item removed'); detailIdle(); };
      inp.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); done(true); } else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); done(false); } });
      inp.addEventListener('blur', () => done(true));
      text.replaceWith(inp);
      inp.focus();
      inp.select();
    }
    return row;
  };
  const addInput = el('input', { type: 'text', class: 'input input-ghost h-7 flex-1 px-1.5', placeholder: 'Add an item…', 'aria-label': 'New checklist item', autocomplete: 'off' });
  const addItem = async () => {
    const v = addInput.value.trim();
    if (!v) return;
    addInput.value = '';
    addInput.dataset.busy = '';
    await saveChecks(checks.concat([{ text: v, done: false }]), '');
    delete addInput.dataset.busy;
    const again = dom.detailBody.querySelector('.check-add input');
    if (again) again.focus();
  };
  addInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); addItem(); } else if (e.key === 'Escape' && addInput.value) { e.preventDefault(); e.stopPropagation(); addInput.value = ''; } });
  const checkSection = sectionEl('Checklist', [checks.length ? el('span', { class: 'num text-11 text-fg-3' }, `${doneCount}/${checks.length}`) : null],
    checks.length ? el('div', { class: 'check-progress mb-2' }, el('i', { style: `width:${Math.round((doneCount / checks.length) * 100)}%` })) : null,
    el('ul', { class: 'flex flex-col' }, ...checks.map(checkRow)),
    el('div', { class: 'check-add flex items-center gap-2 px-1' }, el('span', { class: 'grid size-4 place-items-center text-fg-3' }, icon('plus', 12)), addInput));

  // blockers
  const linkNumber = el('input', { type: 'text', inputmode: 'numeric', class: 'input w-20 num', placeholder: '#12', 'aria-label': 'Task number', required: true, pattern: '#?\\d+', autocomplete: 'off' });
  const linkDir = el('select', { class: 'input w-auto', 'aria-label': 'Link direction' }, el('option', { value: 'blockedBy' }, 'Blocked by'), el('option', { value: 'blocks' }, 'Blocks'));
  const linkForm = el('form', { class: 'mt-2 flex items-center gap-2', onsubmit: (e) => { e.preventDefault(); addLink(task, linkDir.value, linkNumber.value); } },
    linkDir, linkNumber, el('button', { type: 'submit', class: 'btn' }, icon('link', 14), 'Link'));
  const linkList = (items, label) => el('div', { class: 'mb-2' }, el('div', { class: 'mb-1 text-12 text-fg-3' }, label),
    el('ul', { class: 'flex flex-col gap-0.5' }, ...(items.length ? items.map((t) => el('li', { class: 'flex items-center gap-1' }, taskLink(t),
      el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': `Unlink #${t.seq}`, title: 'Unlink', onclick: () => removeLink(task, t) }, icon('x', 12)))) : [el('li', { class: 'px-2 text-12 text-fg-3' }, 'None')])));
  const blockSection = sectionEl('Blockers', null, linkList(links.blockedBy || [], 'Blocked by'), linkList(links.blocks || [], 'Blocks'), linkForm);

  // comments
  const commentNodes = comments.map((c) => el('article', { class: 'comment' },
    el('div', { class: 'mb-1 flex h-6 items-center gap-2' }, el('span', { class: 'text-12 font-medium' }, c.author || 'default'), el('time', { class: 'num text-11 text-fg-3', datetime: c.createdAt, title: fmtDate(c.createdAt) }, relTime(c.createdAt)),
      el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon rm ml-auto', 'aria-label': 'Delete comment', title: 'Delete comment', onclick: () => deleteComment(task, c) }, icon('trash', 12))),
    renderMarkdown(c.body)));
  const commentEd = mdEditor({ value: draftText, label: 'New comment', placeholder: 'Write a comment…', rows: 3, saveLabel: 'Comment', onSave: (v, ed) => addComment(task, v, ed) });
  commentEd.root.classList.add('comment-editor');
  const commentSection = sectionEl('Comments', comments.length ? el('span', { class: 'num text-11 text-fg-3' }, comments.length) : null,
    el('div', { class: 'mb-3 flex flex-col gap-2' }, ...commentNodes), commentEd.root);

  dom.detailBody.replaceChildren(props, descSection, checkSection, blockSection, commentSection);
  dom.detailBody.scrollTop = scrollTop;
  if (hadFocus) commentEd.focus();

  const cancelled = task.status === 'cancelled';
  dom.detailActions.replaceChildren(
    el('button', { type: 'button', class: 'btn', onclick: () => openEdit(task) }, icon('pencil', 14), 'Edit'),
    el('span', { class: 'flex-1' }),
    ...clean([
      !cancelled ? el('button', { type: 'button', class: 'btn btn-ghost', onclick: () => cancelTask(task) }, 'Cancel task') : null,
      cancelled ? el('button', { type: 'button', class: 'btn btn-ghost btn-danger', onclick: () => deleteTask(task) }, 'Delete') : null,
      cancelled ? el('button', { type: 'button', class: 'btn btn-primary', onclick: () => restoreTask(task) }, 'Restore') : null,
      isOpen(task) ? el('button', { type: 'button', class: 'btn btn-primary', onclick: () => shipTask(task) }, icon('ship', 14), 'Ship') : null,
    ]));
}
async function addComment(task, body, ed) {
  const text = body.trim();
  if (!text) return;
  ed.ta.disabled = true;
  try {
    await api('POST', taskPath(task.id) + '/comments', { body: text });
    ed.set('');
    await loadDetail(state.detail, true);
    announce('Comment added');
  } catch (err) { toast(err.message, 'error'); } finally { ed.ta.disabled = false; }
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
  if (!/^\d+$/.test(other)) { toast('Enter a task number, like #12', 'error'); return; }
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
  const out = await mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody('done', undefined, force))), `Shipped #${task.seq} ${task.title}`,
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
  animate(d, [{ opacity: 0, transform: 'translateY(6px) scale(0.99)' }, { opacity: 1, transform: 'none' }], 160);
}
async function closeDialog(d) {
  if (!d.open) return;
  if (dur(160)) {
    d.style.pointerEvents = 'none';
    await animate(d, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateY(4px) scale(0.99)' }], 120).finished.catch(() => {});
    d.style.pointerEvents = '';
  }
  d.close();
}
function parseChecks(text) {
  return text.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => (/^x\s+/i.test(l) ? { text: l.replace(/^x\s+/i, ''), done: true } : { text: l, done: false }));
}
const serializeChecks = (checks) => (checks || []).map((c) => (c.done ? 'x ' : '') + c.text).join('\n');
let editDescEditor = null, editLabelEditor = null;
function readForm() {
  const f = dom.editForm.elements;
  return {
    title: f.title.value.trim(), emoji: f.emoji.value.trim(), desc: editDescEditor ? editDescEditor.value : '', status: f.status.value,
    prio: Number(f.prio.value), due: f.due.value, effort: dom.editEffort.dataset.value || '', tags: editLabelEditor ? editLabelEditor.tags : [],
    project: f.project.value, blocked: f.blocked.checked, checks: parseChecks(f.checks.value),
  };
}
function renderEffortSeg(value) {
  dom.editEffort.dataset.value = value || '';
  dom.editEffort.replaceChildren(...[['', 'None'], ...EFFORTS.map((x) => [x, x])].map(([v, label]) => el('button', {
    type: 'button', class: 'seg-btn', role: 'radio', 'aria-checked': (value || '') === v ? 'true' : 'false', onclick: () => renderEffortSeg(v),
  }, label)));
}
function openEdit(task, preset = {}) {
  state.editing = task || null;
  const f = dom.editForm.elements;
  f.project.replaceChildren(...projectList(task && task.project).map((p) => el('option', { value: p }, p)));
  f.title.value = task ? task.title : preset.title || '';
  f.emoji.value = task ? task.emoji || '' : '';
  f.status.value = task ? task.status : preset.status || 'todo';
  f.prio.value = String(task ? task.prio || 3 : 3);
  f.due.value = task ? task.due || '' : '';
  f.project.value = task ? task.project || state.project : state.project;
  f.blocked.checked = !!(task && task.blocked);
  f.checks.value = task ? serializeChecks(task.checks) : '';
  renderEffortSeg(task ? task.effort || '' : '');
  editDescEditor = mdEditor({ value: task ? task.desc || '' : '', label: 'Description', placeholder: 'Describe the task in markdown…', rows: 5 });
  dom.editDesc.replaceChildren(editDescEditor.root);
  editLabelEditor = labelEditor(dom.editLabels, { tags: task ? userTags(task) : [] });
  dom.editLabels.firstElementChild.classList.add('input', 'h-auto', 'min-h-8', 'py-0.5');
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
  if (!form.title) { toast('Add a title before saving', 'error'); dom.editForm.elements.title.focus(); return; }
  const save = $('#edit-save');
  save.disabled = true;
  save.textContent = 'Saving…';
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
      saved = await patchTask(prev.id, patch);
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
    toast(state.editing ? `Saved #${saved.seq} ${saved.title}` : `Created #${saved.seq} ${saved.title}`, 'ok');
    if (state.detail) loadDetail(state.detail, true);
    invalidate();
  } catch (err) {
    toast(err.message || 'Save failed', 'error');
  } finally {
    save.disabled = false;
    save.textContent = 'Save';
  }
}

/* ============================== composer (inline quick add) ============================== */
const WEEKDAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
// "Fix login !high #auth #type::bug @fri ~M" -> {title, prio, tags, due, effort, chips}
function parseQuickAdd(text) {
  const out = { title: [], prio: null, tags: [], due: null, effort: null, chips: [] };
  for (const w of text.split(/\s+/)) {
    if (!w) continue;
    let m;
    if ((m = /^!(high|medium|med|low|[123])$/i.exec(w))) { out.prio = PRIO_WORDS[m[1].toLowerCase()]; out.chips.push(el('span', { class: 'chip' }, priorityIcon(out.prio), el('span', {}, PRIO_LABEL[out.prio]))); continue; }
    if ((m = /^#([\w:.-]+)$/.exec(w)) && !/^\d+$/.test(m[1]) && !isProjectTag(m[1])) { out.tags = withLabel(out.tags, m[1]); continue; }
    if ((m = /^~([sml])$/i.exec(w))) { out.effort = m[1].toUpperCase(); out.chips.push(el('span', { class: 'chip chip-mono' }, el('span', {}, 'Effort ' + out.effort))); continue; }
    if ((m = /^@(\d{4}-\d{2}-\d{2}|today|tomorrow|sun|mon|tue|wed|thu|fri|sat)$/i.exec(w))) {
      const v = m[1].toLowerCase();
      const today = localToday();
      let d = null;
      if (v === 'today') d = today;
      else if (v === 'tomorrow') d = new Date(today.getTime() + 86400000);
      else if (WEEKDAYS.includes(v)) d = new Date(today.getTime() + (((WEEKDAYS.indexOf(v) - today.getDay() + 7) % 7) || 7) * 86400000);
      else if (dueDays(v) !== null) out.due = v;
      if (d) out.due = isoDate(d);
      if (out.due) { out.chips.push(dueChipEl(out.due, true)); continue; }
    }
    out.title.push(w);
  }
  out.title = out.title.join(' ');
  out.chips.push(...out.tags.map((t) => chipEl(t)));
  return out;
}
function renderComposer(status, open = false) {
  const { composer } = cols[status];
  if (!open) {
    composer.replaceChildren(el('button', { type: 'button', class: 'btn btn-ghost w-full justify-start px-2 text-fg-3', onclick: () => openComposer(status) }, icon('plus', 14), 'Add task'));
    return;
  }
  const input = el('input', { type: 'text', class: 'input', placeholder: 'Title  !high #label #type::bug @fri ~M', 'aria-label': `New task in ${STATUS_LABEL[status]}`, autocomplete: 'off', spellcheck: 'false', enterkeyhint: 'done' });
  const preview = el('div', { class: 'flex flex-wrap gap-1 empty:hidden', 'aria-live': 'polite' });
  const refresh = () => preview.replaceChildren(...parseQuickAdd(input.value).chips);
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
  const form = el('form', { class: 'composer-open', onsubmit: (e) => { e.preventDefault(); submit(); } }, input, preview,
    el('div', { class: 'flex items-center gap-2' }, el('span', { class: 'text-11 text-fg-3' }, el('kbd', { class: 'kbd' }, 'Enter'), ' adds · ', el('kbd', { class: 'kbd' }, 'Esc'), ' closes'), el('span', { class: 'flex-1' }),
      el('button', { type: 'button', class: 'btn btn-ghost btn-sm', onclick: () => renderComposer(status) }, 'Close'), el('button', { type: 'submit', class: 'btn btn-primary btn-sm' }, 'Add')));
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
  add('Action', 'Keyboard shortcuts', () => showDialog(dom.helpDialog), '?');
  add('Action', 'Display options', () => toggleSettings(true), 's');
  add('Action', `Switch to ${currentTheme() === 'dark' ? 'light' : 'dark'} theme`, toggleTheme, '', 'dark light theme');
  add('Action', `Density: ${settings.density === 'compact' ? 'comfortable' : 'compact'}`, () => { settings.density = settings.density === 'compact' ? 'comfortable' : 'compact'; applySettings(); }, '', 'compact comfortable');
  add('Action', `${settings.showCancelled ? 'Hide' : 'Show'} cancelled column`, () => { settings.showCancelled = !settings.showCancelled; applySettings(); }, '', 'column');
  add('Action', `${settings.hideEmpty ? 'Show' : 'Hide'} empty columns`, () => { settings.hideEmpty = !settings.hideEmpty; applySettings(); });
  if (anyFilter()) add('Action', 'Clear filters', clearFilters, 'X');
  for (const p of state.meta.projects || []) if (p !== state.project) add('Project', `Switch to ${p}`, () => setProject(p), '', 'project');
  for (const l of allLabels()) add('Label', `${state.tags.has(l) ? 'Remove' : 'Filter'} label ${l}`, () => toggleTag(l), '', 'tag');
  for (const q of QUICK) add('Filter', `${state.quick.has(q.id) ? 'Remove filter' : 'Filter'}: ${q.label}`, () => toggleQuick(q.id));
  for (const s of ['position', 'prio', 'due']) if (settings.sort !== s) add('Sort', `Sort columns by ${s === 'prio' ? 'priority' : s}`, () => { settings.sort = s; applySettings(); });
  for (const t of state.tasks) add('Task', `#${t.seq} ${t.title}`, () => openDetail(t.seq), '', (t.tags || []).join(' '), t);
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
  if (!scored.length) { dom.paletteList.replaceChildren(el('li', { class: 'px-3 py-6 text-center text-13 text-fg-3' }, 'No matches. Try a task number or a label.')); return; }
  dom.paletteList.replaceChildren(...scored.map((s, i) => {
    let label;
    if (s.item.group === 'Task') {
      const m = /^(#\d+)\s(.*)$/s.exec(s.item.label);
      label = el('span', { class: 'flex min-w-0 flex-1 items-center gap-2' }, el('span', { class: 'seq' }, m[1]), el('span', { class: 'truncate' }, mdInline(m[2])));
    } else {
      label = el('span', { class: 'min-w-0 flex-1 truncate' });
      const pos = new Set(s.positions);
      let run = '';
      for (let k = 0; k <= s.item.label.length; k++) {
        const ch = s.item.label[k];
        if (k < s.item.label.length && pos.has(k)) { if (run) { label.append(run); run = ''; } label.append(el('mark', {}, ch)); }
        else if (k < s.item.label.length) run += ch;
      }
      if (run) label.append(run);
    }
    return el('li', { class: 'pal-item', role: 'option', id: 'pal-' + i, 'aria-selected': i === 0 ? 'true' : 'false', onclick: () => runPalette(i), onmousemove: () => selectPalette(i) },
      el('span', { class: 'group' }, s.item.group), label, s.item.kbd ? el('kbd', { class: 'kbd' }, s.item.kbd) : null);
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
  const row = (label, control) => el('div', { class: 'flex min-h-8 items-center gap-3' }, el('span', { class: 'flex-1 text-13' }, label), control);
  const seg = (label, options, current, onPick) => row(label, el('div', { class: 'seg seg-sm', role: 'group', 'aria-label': label },
    ...options.map(([value, text]) => el('button', { type: 'button', class: 'seg-btn', 'aria-pressed': current === value ? 'true' : 'false', onclick: () => { onPick(value); applySettings(); renderSettings(); } }, text))));
  const check = (label, get, set) => el('label', { class: 'flex h-8 cursor-pointer items-center gap-2 text-13' }, el('input', { type: 'checkbox', class: 'cb', checked: get(), onchange: (e) => { set(e.target.checked); applySettings(); } }), label);
  const head = (text) => el('h4', { class: 'label-11 mt-3 mb-1' }, text);
  const props = [['seq', '#seq'], ['emoji', 'Emoji'], ['desc', 'Description'], ['tags', 'Labels'], ['due', 'Due'], ['effort', 'Effort'], ['checks', 'Checklist'], ['comments', 'Comments']];
  dom.settings.replaceChildren(
    el('div', { class: 'mb-1 flex h-8 items-center' }, el('h3', { class: 'text-14 font-semibold' }, 'Display'), el('button', { type: 'button', class: 'btn btn-ghost btn-sm btn-icon ml-auto', 'aria-label': 'Close', onclick: () => toggleSettings(false) }, icon('x', 14))),
    seg('Theme', [['light', 'Light'], ['dark', 'Dark'], ['system', 'System']], settings.theme, (v) => { settings.theme = v; }),
    seg('Density', [['comfortable', 'Comfortable'], ['compact', 'Compact']], settings.density, (v) => { settings.density = v; }),
    head('Card properties'),
    el('div', { class: 'grid grid-cols-2 gap-x-3' }, ...props.map(([key, label]) => check(label, () => settings.show[key], (v) => { settings.show[key] = v; }))),
    head('Columns'),
    el('div', { class: 'grid grid-cols-2 gap-x-3' }, check('Hide empty', () => settings.hideEmpty, (v) => { settings.hideEmpty = v; }), check('Show cancelled', () => settings.showCancelled, (v) => { settings.showCancelled = v; })),
    row('Sort', el('select', { class: 'input w-auto', 'aria-label': 'Column sort', onchange: (e) => { settings.sort = e.target.value; applySettings(); } },
      ...[['position', 'Position'], ['prio', 'Priority'], ['due', 'Due date']].map(([v, t]) => el('option', { value: v, selected: settings.sort === v }, t)))),
    head('WIP limits'),
    el('div', { class: 'grid grid-cols-4 gap-2' }, ...STATUSES.map((s) => el('label', { class: 'flex flex-col gap-1 text-11 text-fg-3' }, STATUS_LABEL[s], el('input', { type: 'number', class: 'input num px-2', min: '0', step: '1', inputmode: 'numeric', value: settings.wip[s] || '', placeholder: '∞', 'aria-label': `WIP limit for ${STATUS_LABEL[s]}`,
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
  const node = el('div', { class: 'toast ' + kind }, el('span', { class: 'min-w-0 flex-1 truncate' }, mdInline(message)),
    opts.action ? el('button', { type: 'button', class: 'btn', onclick: () => { dismiss(); opts.action.run(); } }, opts.action.label) : null,
    el('button', { type: 'button', class: 'btn btn-icon', 'aria-label': 'Dismiss', onclick: dismiss }, icon('x', 12)));
  dom.toasts.append(node);
  animate(node, [{ opacity: 0, transform: 'translateY(12px) scale(0.98)' }, { opacity: 1, transform: 'none' }], 180);
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
  announce(`Project ${name}`);
}
function cycleProject(dir) {
  const list = state.meta.projects || [];
  if (list.length < 2) return;
  const i = list.indexOf(state.project);
  setProject(list[(i + dir + list.length) % list.length]);
}
function mountSearch() {
  const phone = phoneMQ.matches;
  const target = phone ? dom.searchSlot : null;
  if (phone && dom.searchWrap.parentNode !== dom.searchSlot) { dom.searchSlot.append(dom.searchWrap); dom.searchWrap.className = 'relative w-full'; }
  if (!phone && dom.searchWrap.parentNode === dom.searchSlot) { $('header').insertBefore(dom.searchWrap, $('#filters-toggle')); dom.searchWrap.className = 'relative hidden w-[260px] md:block xl:w-[320px]'; }
  void target;
}
function setFiltersOpen(open) {
  dom.filters.classList.toggle('open', open);
  $('#filters-toggle').setAttribute('aria-expanded', String(open));
  if (open) {
    dom.scrim.hidden = false;
    requestAnimationFrame(() => dom.scrim.classList.add('show'));
    setTimeout(() => dom.search.focus(), dur(160) + 20); // the sheet is visibility:hidden until its transition ends
  } else {
    dom.scrim.classList.remove('show');
    setTimeout(() => { dom.scrim.hidden = true; }, dur(160));
  }
}
function focusSearch() {
  if (phoneMQ.matches) { setFiltersOpen(true); return; }
  dom.search.focus();
  dom.search.select();
}
function focusLabels() {
  if (phoneMQ.matches) { setFiltersOpen(true); return; }
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
  if (!dom.projectMenu.hidden) {
    const items = $$('.menu-item', dom.projectMenu);
    const i = items.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') { e.preventDefault(); (items[i + 1] || items[0]).focus(); return; }
    if (e.key === 'ArrowUp') { e.preventDefault(); (items[i - 1] || items[items.length - 1]).focus(); return; }
    if (e.key === 'Escape') { e.preventDefault(); toggleProjectMenu(false); return; }
    if (e.key === 'Tab') { toggleProjectMenu(false); return; }
  }
  if (e.key === 'Escape') {
    if (!dom.settings.hidden) { e.preventDefault(); toggleSettings(false); return; }
    if (openDlg) return; // the dialog's cancel handler closes it
    if (dom.filters.classList.contains('open')) { e.preventDefault(); setFiltersOpen(false); return; }
    if (state.lifted) { e.preventDefault(); cancelLift(); return; }
    if (isTyping(e.target)) { e.target.blur(); return; }
    const openEditor = dom.detail.querySelector('.editor[data-busy][data-view="preview"] .editor-tabs');
    if (openEditor) { e.preventDefault(); openEditor.click(); return; }
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
const clampPanel = (w) => Math.max(360, Math.min(Math.min(720, window.innerWidth * 0.6), w));
function bindPanelResize() {
  const handle = dom.panelHandle;
  const apply = () => { document.documentElement.style.setProperty('--panel-w', Math.round(settings.panelWidth) + 'px'); handle.setAttribute('aria-valuenow', String(Math.round(settings.panelWidth))); };
  handle.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    handle.setPointerCapture(e.pointerId);
    dom.panel.classList.add('is-resizing');
    const move = (ev) => { settings.panelWidth = clampPanel(window.innerWidth - ev.clientX); apply(); };
    const up = () => { handle.removeEventListener('pointermove', move); handle.removeEventListener('pointerup', up); dom.panel.classList.remove('is-resizing'); saveSettings(); };
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', up);
  });
  handle.addEventListener('keydown', (e) => {
    const step = e.key === 'ArrowLeft' ? 24 : e.key === 'ArrowRight' ? -24 : 0;
    if (!step) return;
    e.preventDefault();
    settings.panelWidth = clampPanel(settings.panelWidth + step);
    apply();
    saveSettings();
  });
  apply();
}
function bind() {
  dom.search.addEventListener('input', () => {
    clearTimeout(dom.search._t);
    dom.search._t = setTimeout(() => applySearchText(dom.search.value.trim(), false), 200);
  });
  dom.search.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); clearTimeout(dom.search._t); applySearchText(dom.search.value.trim(), false); } });
  dom.projectBtn.addEventListener('click', () => toggleProjectMenu());
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
    if (!dom.projectMenu.hidden && !dom.projectMenu.contains(e.target) && !e.target.closest('#project-btn')) toggleProjectMenu(false);
  });
  document.addEventListener('keydown', onKeydown);
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') refreshTasks(); });
  window.addEventListener('online', () => refreshTasks());
  window.addEventListener('hashchange', applyRoute);
  window.addEventListener('beforeunload', (e) => { if ((dom.editDialog.open && editDirty()) || panelDirty()) e.preventDefault(); });
  wideMQ.addEventListener('change', () => { mountDetail(); renderBoard(); });
  phoneMQ.addEventListener('change', () => { mountSearch(); renderBoard(); });
  trackActiveSegment();
  bindPanelResize();
}

/* ============================== init ============================== */
async function init() {
  applySettings(false);
  buildBoard();
  mountSearch();
  bind();
  mountDetail();
  await refreshMeta();
  const m = /^#\/p\/(.+)$/.exec(decodeURIComponent(location.hash || ''));
  if (m) state.project = m[1];
  await refreshTasks();
  if (!state.loaded) for (const status of STATUSES) cols[status].body.replaceChildren(el('div', { class: 'col-empty' }, 'Waiting for the server…'));
  applyRoute();
  schedulePoll();
}
init();
