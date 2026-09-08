/* kb web frontend. Plain ES2022, no modules, no bundler. Talks to /api/* (docs/web-api.md).
   Markdown: marked + DOMPurify (vendored, see VENDOR.md). Styling: Tailwind classes compiled by
   scripts/build-web-css.sh, plus the component classes in internal/webui/tailwind/app.css.
   Sections: utilities · labels · markdown · settings · state · api/live updates · filters
   · render (header, filters, board, cards) · drag and drop · keyboard, lift, selection, bulk
   · markdown editor · label editor · detail panel · edit dialog · composer · palette
   · display options · ask dialog · settings dialog · AI in the editor · split ADR
   · forge import · provenance and drift · actions and help · toasts · routing · handlers · init */
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
    else if (key === 'title') node.setAttribute('data-tip', value); // the board draws its own tooltips
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
// replaceChildren() stringifies null; this drops the empty slots first.
const setKids = (node, ...children) => node.replaceChildren(...clean(children.flat()));
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
  minus: 'M3.5 8h9', lock: ['M3.5 7.5h9v6h-9z', 'M5.5 7.5V5a2.5 2.5 0 0 1 5 0v2.5'],
  sparkle: ['M6.5 2.5 7.6 5.4 10.5 6.5 7.6 7.6 6.5 10.5 5.4 7.6 2.5 6.5 5.4 5.4z', 'M11.5 9.5l.6 1.4 1.4.6-1.4.6-.6 1.4-.6-1.4-1.4-.6 1.4-.6z'],
  gear: ['M8 5.9a2.1 2.1 0 1 0 0 4.2 2.1 2.1 0 0 0 0-4.2z', 'M8 1.7l.9 1.6 1.8-.4.6 1.8 1.8.6-.4 1.8L13.9 8l-1.2 1.4.4 1.8-1.8.6-.6 1.8-1.8-.4L8 14.3l-.9-1.6-1.8.4-.6-1.8-1.8-.6.4-1.8L2.1 8l1.2-1.4-.4-1.8 1.8-.6.6-1.8 1.8.4z'],
  upload: ['M8 11V3', 'M5 6l3-3 3 3', 'M2.5 11v2h11v-2'],
  sync: ['M13.5 8a5.5 5.5 0 0 1-9.6 3.6', 'M2.5 8a5.5 5.5 0 0 1 9.6-3.6', 'M2.5 4.8v3.4h3.4', 'M13.5 11.2V7.8h-3.4'],
  alert: ['M8 2.6 14.4 13.2H1.6z', 'M8 6.4v3', 'M8 11.3h.01'],
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
const clamp = (n, lo, hi) => Math.max(lo, Math.min(hi, n));
const sentence = (s) => (s ? s[0].toUpperCase() + s.slice(1) : '');
let uidN = 0;
const uid = (prefix) => `${prefix}-${++uidN}`;
// Swaps a button for a spinner while its request is in flight. Returns the undo.
// Disabling drops focus to the body, so remember which control to hand it back to:
// the re-render that usually follows reads this.
let busyFocusKey = '';
function busy(btn, label) {
  const kids = Array.from(btn.childNodes);
  const width = btn.offsetWidth;
  if (document.activeElement === btn) busyFocusKey = btn.dataset.k || '';
  btn.disabled = true;
  btn.style.minWidth = width + 'px';
  btn.replaceChildren(el('span', { class: 'spinner' }), label || 'Working…');
  return () => {
    btn.disabled = false;
    btn.style.minWidth = '';
    btn.replaceChildren(...kids);
    if (busyFocusKey && btn.isConnected && btn.dataset.k === busyFocusKey) { btn.focus(); busyFocusKey = ''; }
  };
}
// label over control over message, with the message wired up as the control's description.
// setMsg(group, text, tone) writes that line; an error tone also marks the control invalid.
function fieldGroup(label, control, opts = {}) {
  if (!control.id) control.id = uid('f');
  const msgID = control.id + '-msg';
  control.setAttribute('aria-describedby', msgID);
  const msg = el('p', { class: 'field-msg' + (opts.tone ? ' is-' + opts.tone : ''), id: msgID });
  if (opts.message) msg.textContent = opts.message;
  if (opts.tone === 'error') control.setAttribute('aria-invalid', 'true');
  const head = el('label', { class: 'label-11', for: control.id }, label, opts.hint ? el('span', { class: 'ml-1.5 font-normal tracking-normal text-fg-3' }, opts.hint) : null);
  return el('div', { class: 'field' + (opts.class ? ' ' + opts.class : '') },
    // An optional action (clear the saved key) sits on the label line, so the
    // control below keeps the full column width and lines up with its neighbours.
    opts.action ? el('div', { class: 'field-head' }, head, opts.action) : head,
    control, msg);
}
function setMsg(group, text, tone) {
  const p = group && group.querySelector('.field-msg');
  if (!p) return;
  p.className = 'field-msg' + (tone ? ' is-' + tone : '');
  p.textContent = text || '';
  const control = group.querySelector('input, textarea, select');
  if (control) { if (tone === 'error') control.setAttribute('aria-invalid', 'true'); else control.removeAttribute('aria-invalid'); }
}
/* ============================== controls ============================== */
// Every control is drawn by the board rather than the browser, so the UI reads
// the same on every OS. Selects and date fields keep a hidden native element
// for value and form semantics and get a button plus popover in front of it;
// tooltips are one floating node; confirms go through the ask dialog.
const POPOVER = 'showPopover' in HTMLElement.prototype;
// Puts pop under anchor in the top layer (absolutely positioned without the Popover API).
function showPop(pop, anchor) {
  if (!POPOVER) { pop.hidden = false; return; }
  pop.showPopover();
  const r = anchor.getBoundingClientRect();
  pop.style.minWidth = Math.round(r.width) + 'px';
  const h = pop.offsetHeight, w = pop.offsetWidth;
  const below = r.bottom + 4 + h <= window.innerHeight - 8;
  pop.style.left = Math.round(clamp(r.left, 8, Math.max(8, window.innerWidth - w - 8))) + 'px';
  pop.style.top = Math.round(below ? r.bottom + 4 : Math.max(8, r.top - 4 - h)) + 'px';
}
function hidePop(pop) {
  if (POPOVER) { if (pop.matches(':popover-open')) pop.hidePopover(); } else pop.hidden = true;
}
// Closes the pop when the pointer lands outside it or the page moves; returns the teardown.
function popDismiss(pop, anchor, close) {
  const down = (e) => { if (!pop.contains(e.target) && !anchor.contains(e.target)) close(); };
  const scroll = (e) => { if (!pop.contains(e.target)) close(); };
  document.addEventListener('pointerdown', down, true);
  document.addEventListener('scroll', scroll, true);
  window.addEventListener('resize', close);
  return () => { document.removeEventListener('pointerdown', down, true); document.removeEventListener('scroll', scroll, true); window.removeEventListener('resize', close); };
}
// Swaps the native element for a wrapper holding the trigger, the (hidden)
// native, and the pop. The native keeps its name, value and form membership;
// its classes move to the trigger so `input w-auto num` keep meaning.
function wrapControl(native, trigger, pop) {
  const wrap = el('div', { class: 'select' + (native.classList.contains('w-auto') ? ' w-auto' : '') });
  trigger.className = native.className + ' select-trigger';
  native.replaceWith(wrap);
  wrap.append(trigger, native, pop);
  native.className = 'select-native';
  native.tabIndex = -1;
  native.setAttribute('aria-hidden', 'true');
  if (native.id) {
    const lab = document.querySelector(`label[for="${native.id}"]`);
    if (lab) { if (!lab.id) lab.id = uid('lab'); trigger.setAttribute('aria-labelledby', lab.id); }
  }
  const mirror = () => {
    for (const a of ['aria-label', 'aria-invalid', 'aria-describedby']) { const v = native.getAttribute(a); if (v === null) trigger.removeAttribute(a); else trigger.setAttribute(a, v); }
    trigger.disabled = native.disabled;
  };
  native.focus = () => trigger.focus();
  native.addEventListener('focus', () => trigger.focus());
  if (document.activeElement === native) trigger.focus();
  return mirror;
}
// Instance-level value setter so `sel.value = x` repaints the trigger.
function hookValue(native, proto, sync) {
  const d = Object.getOwnPropertyDescriptor(proto, 'value');
  Object.defineProperty(native, 'value', { get: () => d.get.call(native), set: (v) => { d.set.call(native, v); sync(); } });
}
function enhanceSelect(sel) {
  if (sel.dataset.enhanced !== undefined) return;
  sel.dataset.enhanced = '';
  const label = el('span', { class: 'select-value' });
  const trigger = el('button', { type: 'button', 'aria-haspopup': 'listbox', 'aria-expanded': 'false' }, label, icon('chevD', 14));
  const pop = el('div', { class: 'pop select-pop', role: 'listbox', hidden: !POPOVER, popover: POPOVER ? 'manual' : null });
  const mirror = wrapControl(sel, trigger, pop);
  const sync = () => { const o = sel.selectedOptions[0]; label.textContent = o ? o.textContent : ''; label.classList.toggle('is-empty', !o || !o.value); mirror(); };
  hookValue(sel, HTMLSelectElement.prototype, sync);
  new MutationObserver(sync).observe(sel, { childList: true, subtree: true, attributes: true, attributeFilter: ['disabled', 'selected', 'aria-label', 'aria-invalid', 'aria-describedby'] });
  let off = null;
  const close = () => { if (!off) return; off(); off = null; hidePop(pop); trigger.setAttribute('aria-expanded', 'false'); };
  const pick = (v) => { const changed = v !== sel.value; close(); trigger.focus(); if (changed) { sel.value = v; sel.dispatchEvent(new Event('change', { bubbles: true })); } };
  const open = () => {
    if (off || trigger.disabled) return;
    const items = [...sel.options].map((o) => el('button', { type: 'button', role: 'option', class: 'menu-item', disabled: o.disabled, 'aria-selected': o.selected ? 'true' : 'false', onclick: () => pick(o.value), onmousemove: (e) => e.currentTarget.focus() },
      el('span', { class: 'flex-1 truncate' }, o.textContent), o.selected ? icon('check', 14) : null));
    pop.replaceChildren(...items);
    pop.classList.toggle('pop-sm', trigger.offsetHeight < 32);
    showPop(pop, trigger);
    trigger.setAttribute('aria-expanded', 'true');
    off = popDismiss(pop, trigger, close);
    const cur = items.find((b) => b.getAttribute('aria-selected') === 'true' && !b.disabled) || items.find((b) => !b.disabled);
    if (cur) cur.focus();
  };
  trigger.addEventListener('click', () => (off ? close() : open()));
  trigger.addEventListener('keydown', (e) => { if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); e.stopPropagation(); open(); } });
  pop.addEventListener('keydown', (e) => {
    e.stopPropagation();
    const items = [...pop.querySelectorAll('.menu-item:not(:disabled)')];
    const i = items.indexOf(document.activeElement);
    const go = (j) => { e.preventDefault(); items[clamp(j, 0, items.length - 1)].focus(); };
    if (e.key === 'ArrowDown') go(i + 1);
    else if (e.key === 'ArrowUp') go(i - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(items.length - 1);
    else if (e.key === 'Escape') { e.preventDefault(); close(); trigger.focus(); }
    else if (e.key === 'Tab') close();
    else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const k = e.key.toLowerCase();
      const next = items.slice(i + 1).concat(items.slice(0, i + 1)).find((b) => b.textContent.trim().toLowerCase().startsWith(k));
      if (next) { e.preventDefault(); next.focus(); }
    }
  });
  sync();
}

const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const DOW = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];
const isoDay = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
const parseDay = (iso) => { const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso || ''); return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null; };
// "14 Sep 2026": one fixed shape, whatever the OS locale says.
function fmtDay(iso) { const d = parseDay(iso); return d ? `${d.getDate()} ${MONTHS[d.getMonth()].slice(0, 3)} ${d.getFullYear()}` : ''; }
function enhanceDate(input) {
  if (input.dataset.enhanced !== undefined) return;
  input.dataset.enhanced = '';
  const label = el('span', { class: 'select-value' });
  const trigger = el('button', { type: 'button', 'aria-haspopup': 'dialog', 'aria-expanded': 'false' }, icon('calendar', 14), label);
  const pop = el('div', { class: 'pop cal-pop', role: 'dialog', 'aria-label': 'Choose a date', hidden: !POPOVER, popover: POPOVER ? 'manual' : null });
  const mirror = wrapControl(input, trigger, pop);
  trigger.classList.remove('num'); // the date reads in the UI face, with tabular figures
  const sync = () => { const t = fmtDay(input.value); label.textContent = t || 'No date'; label.classList.toggle('is-empty', !t); mirror(); };
  hookValue(input, HTMLInputElement.prototype, sync);
  new MutationObserver(sync).observe(input, { attributes: true, attributeFilter: ['disabled', 'aria-label', 'aria-invalid', 'aria-describedby'] });
  let off = null;
  let view = null; // {y, m} of the month on show
  const close = () => { if (!off) return; off(); off = null; hidePop(pop); trigger.setAttribute('aria-expanded', 'false'); };
  const pick = (iso) => { const changed = iso !== input.value; close(); trigger.focus(); if (changed) { input.value = iso; input.dispatchEvent(new Event('change', { bubbles: true })); } };
  const render = (focusIso) => {
    const today = isoDay(new Date());
    const first = new Date(view.y, view.m, 1);
    const start = new Date(view.y, view.m, 1 - ((first.getDay() + 6) % 7));
    const days = [];
    for (let i = 0; i < 42; i++) {
      const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
      const iso = isoDay(d);
      days.push(el('button', { type: 'button', class: 'day num' + (d.getMonth() !== view.m ? ' is-other' : '') + (iso === today ? ' is-today' : ''), 'data-iso': iso, tabindex: iso === focusIso ? '0' : '-1',
        'aria-pressed': iso === input.value ? 'true' : 'false', 'aria-label': fmtDay(iso), onclick: () => pick(iso), onmousemove: (e) => e.currentTarget.focus() }, String(d.getDate())));
    }
    const nav = (delta, lbl, ic) => el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': lbl, onclick: () => { move(delta); const b = pop.querySelector('.day[tabindex="0"]'); if (b) b.focus(); } }, icon(ic, 14));
    pop.replaceChildren(
      el('div', { class: 'cal-head' }, nav(-1, 'Previous month', 'chevL'), el('span', { class: 'cal-title' }, `${MONTHS[view.m]} ${view.y}`), nav(1, 'Next month', 'chevR')),
      el('div', { class: 'cal-grid' }, ...DOW.map((d) => el('span', { class: 'dow', 'aria-hidden': 'true' }, d)), ...days),
      el('div', { class: 'cal-foot' }, el('button', { type: 'button', class: 'btn btn-ghost btn-xs', onclick: () => pick(today) }, 'Today'), el('span', { class: 'flex-1' }),
        input.value ? el('button', { type: 'button', class: 'btn btn-ghost btn-xs', onclick: () => pick('') }, 'Clear') : null));
  };
  const move = (delta) => { const d = new Date(view.y, view.m + delta, 1); view = { y: d.getFullYear(), m: d.getMonth() }; render(isoDay(d)); };
  const open = () => {
    if (off || trigger.disabled) return;
    const cur = parseDay(input.value) || new Date();
    view = { y: cur.getFullYear(), m: cur.getMonth() };
    render(isoDay(cur));
    showPop(pop, trigger);
    trigger.setAttribute('aria-expanded', 'true');
    off = popDismiss(pop, trigger, close);
    const b = pop.querySelector('.day[tabindex="0"]');
    if (b) b.focus();
  };
  trigger.addEventListener('click', () => (off ? close() : open()));
  trigger.addEventListener('keydown', (e) => { if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); e.stopPropagation(); open(); } });
  pop.addEventListener('keydown', (e) => {
    e.stopPropagation();
    if (e.key === 'Escape') { e.preventDefault(); close(); trigger.focus(); return; }
    if (e.key === 'Tab') { close(); return; }
    const cur = parseDay(document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.iso : '');
    if (!cur) return;
    const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[e.key];
    let next = null;
    if (step) next = new Date(cur.getFullYear(), cur.getMonth(), cur.getDate() + step);
    else if (e.key === 'Home') next = new Date(cur.getFullYear(), cur.getMonth(), cur.getDate() - ((cur.getDay() + 6) % 7));
    else if (e.key === 'End') next = new Date(cur.getFullYear(), cur.getMonth(), cur.getDate() + 6 - ((cur.getDay() + 6) % 7));
    else if (e.key === 'PageUp') next = new Date(cur.getFullYear(), cur.getMonth() - 1, cur.getDate());
    else if (e.key === 'PageDown') next = new Date(cur.getFullYear(), cur.getMonth() + 1, cur.getDate());
    if (!next) return;
    e.preventDefault();
    if (next.getMonth() !== view.m || next.getFullYear() !== view.y) { view = { y: next.getFullYear(), m: next.getMonth() }; render(isoDay(next)); }
    for (const b of pop.querySelectorAll('.day')) b.tabIndex = b.dataset.iso === isoDay(next) ? 0 : -1;
    const b = pop.querySelector(`.day[data-iso="${isoDay(next)}"]`);
    if (b) b.focus();
  });
  sync();
}
// Enhances every select and date input under root, now and as they arrive.
function enhanceControls(root) {
  if (root.nodeType !== 1) return;
  if (root.matches('select')) enhanceSelect(root);
  else if (root.matches('input[type="date"]')) enhanceDate(root);
  for (const s of root.querySelectorAll('select')) enhanceSelect(s);
  for (const d of root.querySelectorAll('input[type="date"]')) enhanceDate(d);
}
function initControls() {
  enhanceControls(document.body);
  new MutationObserver((muts) => { for (const m of muts) for (const n of m.addedNodes) enhanceControls(n); }).observe(document.body, { childList: true, subtree: true });
  // Auto-grow plain textareas instead of showing the browser's resize grip.
  document.addEventListener('input', (e) => { if (e.target.matches && e.target.matches('textarea.input')) growTextarea(e.target); });
  document.addEventListener('focusin', (e) => { if (e.target.matches && e.target.matches('textarea.input')) growTextarea(e.target); });
}
function growTextarea(ta) { ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight + 2, 320) + 'px'; }

// One floating tooltip for every [data-tip]; el() maps `title` onto it. It is a
// popover so it sits above open dialogs, and it waits 300ms like a native tip.
const tip = { node: null, target: null, timer: 0 };
function showTip(target) {
  const text = target.dataset.tip;
  clearTimeout(tip.timer);
  if (!text || matchMedia('(pointer: coarse)').matches) return;
  if (!tip.node) { tip.node = el('div', { class: 'tip', role: 'tooltip', hidden: !POPOVER, popover: POPOVER ? 'manual' : null }); document.body.append(tip.node); }
  tip.target = target;
  tip.timer = setTimeout(() => {
    if (!target.isConnected || tip.target !== target) return;
    tip.node.textContent = text;
    if (POPOVER) { if (!tip.node.matches(':popover-open')) tip.node.showPopover(); } else tip.node.hidden = false;
    const r = target.getBoundingClientRect(), t = tip.node.getBoundingClientRect();
    const x = clamp(r.left + r.width / 2 - t.width / 2, 8, Math.max(8, window.innerWidth - t.width - 8));
    const y = r.bottom + 6 + t.height <= window.innerHeight - 8 ? r.bottom + 6 : r.top - t.height - 6;
    tip.node.style.left = Math.round(x) + 'px';
    tip.node.style.top = Math.round(y) + 'px';
  }, 300);
}
function hideTip() {
  clearTimeout(tip.timer);
  tip.target = null;
  if (!tip.node) return;
  if (POPOVER) { if (tip.node.matches(':popover-open')) tip.node.hidePopover(); } else tip.node.hidden = true;
}
function initTips() {
  document.addEventListener('mouseover', (e) => { const t = e.target.closest ? e.target.closest('[data-tip]') : null; if (!t) hideTip(); else if (t !== tip.target) showTip(t); });
  document.addEventListener('focusin', (e) => { const t = e.target.closest ? e.target.closest('[data-tip]') : null; if (t && t.matches(':focus-visible')) showTip(t); else hideTip(); });
  for (const type of ['mousedown', 'keydown', 'scroll', 'focusout']) document.addEventListener(type, hideTip, true);
}
// confirmDiscard(message, {title, ok}) -> Promise<boolean>; the board's own dialog, never window.confirm.
const confirmDiscard = (message, opts = {}) => ask({ title: opts.title || 'Discard changes?', message,
  actions: [{ value: null, label: opts.cancel || 'Keep editing' }, { value: 'ok', label: opts.ok || 'Discard', class: 'btn btn-danger' }] }).then((r) => !!(r && r.value));

// A bounded count control: two buttons and a numeric text field that always agree.
function stepper(label, value, onChange, min = 1, max = 20) {
  const input = el('input', { type: 'text', class: 'input num w-16 text-center', inputmode: 'numeric', autocomplete: 'off', value: String(value), 'aria-label': label,
    onchange: (e) => { const n = clamp(Math.round(Number(e.target.value) || min), min, max); e.target.value = String(n); onChange(n); bounds(); } });
  const down = el('button', { type: 'button', class: 'btn btn-icon', 'aria-label': `One fewer, ${label}`, onclick: () => step(-1) }, icon('minus', 14));
  const up = el('button', { type: 'button', class: 'btn btn-icon', 'aria-label': `One more, ${label}`, onclick: () => step(1) }, icon('plus', 14));
  const bounds = () => { const n = Number(input.value) || min; down.disabled = n <= min; up.disabled = n >= max; };
  const step = (d) => { const n = clamp((Number(input.value) || min) + d, min, max); input.value = String(n); onChange(n); bounds(); };
  bounds();
  return el('div', { class: 'flex items-center gap-1' }, down, input, up);
}
// Segmented control over [[value, label]…]. Same shape the board's other segments use:
// a group of toggles, so every option stays a tab stop and aria-pressed carries the state.
function segGroup(label, options, current, onPick, cls = 'seg seg-sm') {
  return el('div', { class: cls, role: 'group', 'aria-label': label }, ...options.map(([value, text]) => el('button', {
    type: 'button', class: 'seg-btn', 'aria-pressed': current === value ? 'true' : 'false', onclick: () => onPick(value),
  }, text)));
}
const utf8Bytes = (s) => new TextEncoder().encode(s).length;
const openLink = (href, text) => el('a', { class: 'text-link underline decoration-line-2 underline-offset-2 hover:decoration-current', href, target: '_blank', rel: 'noopener noreferrer' }, text || href, icon('open', 11));
// An inline "go to settings" button that reads as prose, not as a control.
const settingsLink = (text, section) => el('button', { type: 'button', class: 'text-link underline decoration-line-2 underline-offset-2 hover:decoration-current', onclick: () => openSettings(section) }, text);
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
// link:: and import:: are import provenance, not labels anyone filters by: the detail
// panel's Provenance section reads them. They stay in the editor so a save cannot drop them.
const isProvenanceTag = (tag) => tag.startsWith('link::') || tag.startsWith('import::');
const userTags = (t) => (t.tags || []).filter((tag) => !isProjectTag(tag));
const shownTags = (t) => userTags(t).filter((tag) => !isProvenanceTag(tag));
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
    hideEmpty: false, showCancelled: false, wip: {}, sort: 'updated', collapsed: {}, project: '', v: 3,
  };
}
function loadSettings() {
  const base = defaultSettings();
  try {
    const raw = JSON.parse(localStorage.getItem(SETTINGS_KEY) || '{}');
    if ((raw.v || 1) < 2) delete raw.showCancelled; // v2: the cancelled column starts hidden
    if ((raw.v || 1) < 3 && raw.sort === 'position') delete raw.sort; // v3: recently updated on top
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
  state.cardEpoch = (state.cardEpoch || 0) + 1;
  saveSettings();
  if (rerender && state.loaded) render();
}
const currentTheme = () => (settings.theme === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : settings.theme);

/* ============================== state ============================== */
const state = {
  meta: { version: '', projects: [], labels: [] },
  project: '', q: '', tokens: [], tags: new Set(), scopes: new Set(), quick: new Set(),
  tasks: [], all: [], loaded: false, etag: null, etagURL: '', online: true, firstPaint: true,
  dragging: null, touch: null, dragPos: null, autoScrollRAF: 0,
  focusId: null, lifted: null, liftOrigin: null, selected: new Set(), anchorId: null,
  detail: null, detailJSON: '', detailData: null, detailPending: null, commentCounts: {},
  editing: null, editSnapshot: '', paletteIndex: 0, paletteItems: [],
  mode: 'stream', stream: null, streamFails: 0, helloSeen: false, refreshTimer: 0, pollTimer: 0,
  actions: [], projects: [], shipped: null, ai: null, drift: {},
};
// The selected project is this browser's alone: the server keeps no active
// project, so every card created here names state.project in the request.
const ALL_PROJECTS = '::all'; // client-side pseudo-project, like the TUI's "all" scope
const dom = {
  board: $('#board'), detail: $('#detail'),
  labels: $('#labels'), quick: $('#quick'), active: $('#active'), resultCount: $('#result-count'), stats: $('#stats'),
  search: $('#search'), searchBtn: $('#search-btn'), searchDot: $('#search-dot'), searchDialog: $('#search-dialog'), searchClear: $('#search-clear'), searchSyntax: $('#search-syntax'), searchCount: $('#search-count'), projectBtn: $('#project-btn'), projectName: $('#project-name'), projectMenu: $('#project-menu'),
  clear: $('#clear-filters'), banner: $('#banner'), conn: $('#conn'), connDot: $('#conn-dot'), version: $('#version'),
  toasts: $('#toasts'), live: $('#live'), segments: $('#segments'), filters: $('#filters'), scrim: $('#filters-scrim'), filterCount: $('#filter-count'),
  bulkbar: $('#bulkbar'), display: $('#display'),
  editDialog: $('#edit-dialog'), editForm: $('#edit-form'), editTitle: $('#edit-title'), editDesc: $('#edit-desc'), editLabels: $('#edit-labels'), editEffort: $('#edit-effort'),
  detailDialog: $('#detail-dialog'), detailSeq: $('#detail-seq'), detailTitle: $('#detail-title'), detailBody: $('#detail-body'), detailActions: $('#detail-actions'),
  helpDialog: $('#help-dialog'), helpBody: $('#help-body'), palette: $('#palette'), paletteInput: $('#palette-search'), paletteList: $('#palette-list'),
  editSimilar: $('#edit-similar'), editAI: $('#edit-ai'),
  settingsDialog: $('#settings-dialog'), settingsBody: $('#settings-dialog-body'),
  splitDialog: $('#split-dialog'), splitBody: $('#split-dialog-body'), splitFoot: $('#split-dialog-foot'),
  importDialog: $('#import-dialog'), importBody: $('#import-dialog-body'), importFoot: $('#import-dialog-foot'),
  askDialog: $('#ask-dialog'), askForm: $('#ask-form'), askTitle: $('#ask-title'), askBody: $('#ask-body'), askFoot: $('#ask-foot'),
};
const cols = {}; // status -> {col, body, count, composer}
const dropSlot = el('div', { class: 'drop-slot', 'aria-hidden': 'true' });

/* ============================== api / live updates ============================== */
async function api(method, path, body, opts = {}) {
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
const taskPath = (id) => `/api/tasks/${encodeURIComponent(id)}`;
// Runs run(force). On a 409 completion guard, offers the TUI's three ways out — tick the open
// checks and retry, finish anyway, or back out — and returns undefined if the user backs out.
// task is the card the guard refused, when the caller knows it: it decides whether ticking is on offer.
async function withForce(run, task) {
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
        { value: 'force', label: 'Ship anyway', class: open ? 'btn btn-danger' : 'btn btn-primary' },
        open ? { value: 'tick', label: 'Tick everything', class: 'btn btn-primary' } : null,
      ]),
    });
    if (!choice || !choice.value) return undefined;
    if (choice.value === 'force') return run(true);
    const ticked = checks.map((c) => ({ text: c.text, done: true }));
    await api('PATCH', taskPath(card.id), { checks: ticked });
    return withForce(run, Object.assign({}, card, { checks: ticked }));
  }
}
const moveBody = (status, index, force) => Object.assign({ status }, index === undefined ? {} : { index }, force ? { force: true } : {});
const patchTask = (id, patch) => withForce((force) => api('PATCH', taskPath(id), force ? Object.assign({ force: true }, patch) : patch), findTask(id));
// Flips the detail panel's Blocked switch in place so its 160ms transition plays, and holds
// re-renders (loadDetail parks them while the panel is busy) until it has; reverts on failure.
async function setBlocked(task, on) {
  const sw = dom.detailBody.querySelector('.switch[aria-label="Blocked"]');
  if (sw) {
    sw.setAttribute('aria-checked', String(on));
    sw.dataset.animating = '';
    setTimeout(() => { delete sw.dataset.animating; detailIdle(); }, 200);
  }
  try { const out = await patchTask(task.id, { blocked: on }); task.blocked = on; return out; }
  catch (err) { if (sw) sw.setAttribute('aria-checked', String(!on)); throw err; }
}

function tasksURL(unfiltered) {
  const params = new URLSearchParams();
  if (state.project && state.project !== ALL_PROJECTS) params.set('project', state.project);
  if (!unfiltered) {
    if (state.q) params.set('q', state.q);
    for (const tag of state.tags) params.append('tag', tag);
  }
  const qs = params.toString();
  return '/api/tasks' + (qs ? '?' + qs : '');
}
const serverFiltered = () => !!(state.q || state.tags.size);
// pendingMoves counts move requests in flight: a refresh that lands between the
// optimistic render and the server's answer would show the old order for a beat.
const interacting = () => !!(state.dragging || state.touch || state.lifted || state.pendingMoves);

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
    renderHeader();
    renderLabels();
  } catch (err) { /* polling reports connectivity */ }
}
// The project list the CLI and the TUI agree on, with per-project counts.
async function refreshProjects() {
  try {
    const out = await api('GET', '/api/projects');
    state.projects = out.projects || [];
    renderHeader();
  } catch (err) { /* the header falls back to /api/meta */ }
}
// The TUI keyboard registry: one source for the palette and the help sheet.
async function refreshActions() {
  try { state.actions = (await api('GET', '/api/actions')).actions || []; } catch (err) { state.actions = []; }
  renderHelp();
}
async function refreshAIStatus() {
  try { state.ai = await api('GET', '/api/ai/status'); } catch (err) { state.ai = { configured: false }; }
  return state.ai;
}
async function refreshShipped() {
  try { state.shipped = await api('GET', '/api/shipped'); renderStats(); } catch (err) { /* the tally stays stale */ }
}
// One beat of the DONE column's own hue, then a fresh shipped-today tally.
function celebrateShip() {
  const head = cols.done && cols.done.col.querySelector('.col-head');
  if (head && dur(1)) {
    head.classList.remove('shipped');
    void head.offsetWidth;
    head.classList.add('shipped');
    setTimeout(() => head.classList.remove('shipped'), 1000);
  }
  refreshShipped();
}
function invalidate() {
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
  renderHeader(); // the version tooltip names the mode
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
function startLive() {
  if (state.mode === 'poll') { schedulePoll(); return; }
  openStream();
}
// Mobile browsers keep a backgrounded page alive; nothing holds a socket open for it.
function stopLive() {
  closeStream();
  clearTimeout(state.pollTimer);
  state.pollTimer = 0;
}
function setOnline(online) {
  if (state.online === online) return;
  state.online = online;
  dom.banner.hidden = online;
  dom.connDot.classList.toggle('bg-ok', online);
  dom.connDot.classList.toggle('bg-danger', !online);
  dom.conn.dataset.tip = online ? 'Connected' : 'Connection lost';
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
  { id: 'nolabels', label: 'Unlabelled', test: (t) => shownTags(t).length === 0 },
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
  dom.version.dataset.tip = state.mode === 'stream' ? 'Live updates: server push' : 'Live updates: polling every 5 seconds';
  dom.projectName.textContent = state.project === ALL_PROJECTS ? 'All projects' : state.project || 'No project';
  if (!dom.projectMenu.hidden) renderProjectMenu();
  if (!dom.editDialog.open) {
    const sel = dom.editForm.elements.project;
    sel.replaceChildren(...projectList().map((p) => el('option', { value: p, selected: p === state.project }, p)));
  }
  renderStats();
}
// Every project on the board, as /api/projects reports it; ALL is client-side.
const projectNames = () => (state.projects.length ? state.projects.map((p) => p.name) : (state.meta.projects || []));
const projectList = (extra) => Array.from(new Set([...projectNames(), state.project, extra].filter((p) => p && p !== ALL_PROJECTS)));
// Where a new card lands: the selected project, or — in the "all" scope, which
// is a view rather than a project — the first project on the board, inbox on
// an empty one. The server has no default of its own, so every create names one.
const writeProject = () => (state.project && state.project !== ALL_PROJECTS ? state.project : projectNames()[0] || 'inbox');
const projectCount = (name) => {
  const p = state.projects.find((x) => x.name === name);
  if (!p) return null;
  return STATUSES.reduce((n, s) => n + (p.counts[s] || 0), 0);
};
function renderProjectMenu() {
  const list = [ALL_PROJECTS, ...projectList()];
  const total = state.projects.reduce((n, p) => n + STATUSES.reduce((m, s) => m + (p.counts[s] || 0), 0), 0);
  dom.projectMenu.replaceChildren(el('div', { class: 'menu-head' }, 'Projects'), ...list.map((p, i) => {
    const all = p === ALL_PROJECTS;
    const count = all ? total : projectCount(p);
    return el('button', {
      type: 'button', role: 'option', class: 'menu-item', id: 'proj-' + i, 'aria-selected': p === state.project ? 'true' : 'false', 'aria-current': p === state.project ? 'true' : null,
      title: all ? 'Every project on the board' : p,
      onclick: () => { setProject(p); toggleProjectMenu(false); },
    }, el('span', { class: 'grid size-4 place-items-center text-fg' }, p === state.project ? icon('check', 12) : null),
      el('span', { class: 'truncate' }, all ? 'All projects' : p),
      el('span', { class: 'num ml-auto text-11 text-fg-3' }, count === null ? '' : String(count)));
  }));
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
  const shipped = state.shipped ? state.shipped.count : null;
  dom.stats.replaceChildren(item(open, 'open'), item(week, 'this week', 'week'), item(blocked, 'blocked', 'blocked'),
    ...(shipped === null ? [] : [el('span', { class: 'mx-0.5 h-4 w-px bg-line', 'aria-hidden': 'true' }),
      el('span', { class: 'flex h-7 items-center gap-1 px-1.5 text-fg-2', title: shipped ? `Cards moved to Done today: ${(state.shipped.seqs || []).map((s) => '#' + s).join(' ')}` : 'No cards have reached Done today' },
        el('span', { class: 'num font-medium text-fg' }, shipped), 'shipped today')]));
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
  dom.searchCount.textContent = dom.resultCount.textContent;
  const q = !!dom.search.value.trim();
  dom.searchDot.hidden = !q;
  dom.searchClear.hidden = !q;
}
// The label pool is scoped like the board: with one project active it holds
// only the labels that project's cards carry, so another project's vocabulary
// never leaks into the filter row or the picker. All projects use the store's
// full list.
function allLabels() {
  const labels = state.project === ALL_PROJECTS ? (state.meta.labels || []).filter((l) => !isProjectTag(l) && !isProvenanceTag(l)) : [];
  for (const tag of state.tags) if (!labels.includes(tag)) labels.push(tag);
  const pool = state.project === ALL_PROJECTS || !state.all.length ? state.tasks : state.all;
  for (const t of pool) for (const tag of shownTags(t)) if (!labels.includes(tag)) labels.push(tag);
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
// Column sorts. Position is the hand-dragged order; the rest derive from the
// card, newest first for the two time sorts.
const SORTS = [['updated', 'Recently updated'], ['created', 'Recently created'], ['position', 'Position'], ['prio', 'Priority'], ['due', 'Due date']];
const updatedAt = (t) => t.updatedAt || t.movedAt || t.createdAt || '';
function sortTasks(list) {
  if (settings.sort === 'prio') return list.slice().sort((a, b) => (a.prio || 3) - (b.prio || 3) || a.position - b.position);
  if (settings.sort === 'due') return list.slice().sort((a, b) => (a.due ? 1 : 2) - (b.due ? 1 : 2) || (a.due || '').localeCompare(b.due || '') || a.position - b.position);
  if (settings.sort === 'created') return list.slice().sort((a, b) => (b.createdAt || '').localeCompare(a.createdAt || '') || a.position - b.position);
  if (settings.sort === 'updated') return list.slice().sort((a, b) => updatedAt(b).localeCompare(updatedAt(a)) || a.position - b.position);
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

// Everything a card's markup depends on. A node whose signature still matches
// is kept as is, so a refresh only rebuilds the cards that changed.
const cardSig = (t) => JSON.stringify(t) + '|' + [state.selected.has(t.id) ? 1 : 0, state.focusId === t.id ? 1 : 0, state.lifted === t.id ? 1 : 0, state.commentCounts[t.id] ?? '', state.cardEpoch || 0, [...state.tags].sort().join(' ')].join(',');
function cardNodeFor(t, existing) {
  const sig = cardSig(t);
  const old = existing.get(t.id);
  if (old && old.dataset.sig === sig && !old.classList.contains('leaving')) return old;
  const node = cardEl(t);
  node.dataset.sig = sig;
  return node;
}
function cardEl(t) {
  const show = settings.show;
  const checks = t.checks || [];
  const doneCount = checks.filter((c) => c.done).length;
  const tags = show.tags ? shownTags(t) : [];
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
    'data-id': t.id, 'data-seq': t.seq, 'data-status': t.status, 'data-prio': String(t.prio || 3), 'aria-label': `#${t.seq} ${t.title}`,
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
  const existing = new Map($$('.card[data-id]', dom.board).map((c) => [c.dataset.id, c]));
  const keepAll = new Set();
  for (const status of STATUSES) for (const t of groups[status]) keepAll.add(t.id);
  for (const status of STATUSES) {
    const { col, body, count } = cols[status];
    const tasks = sortTasks(groups[status]);
    // Only a card that leaves the board gets a ghost; one that changes column
    // slides there. A card in a hidden column has an empty rect and gets nothing.
    for (const c of $$('.card[data-id]', body)) if (!keepAll.has(c.dataset.id) && !(state.dropped && state.dropped.has(c.dataset.id)) && first.get(c.dataset.id).rect.width) leaveCard(c, first.get(c.dataset.id).rect);
    body.replaceChildren(...(tasks.length ? tasks.map((t) => cardNodeFor(t, existing)) : [emptyEl(status)]));
    const limit = Number(settings.wip[status]) || 0;
    const over = limit > 0 && tasks.length > limit;
    col.classList.toggle('is-over', over);
    tickCounter(count, limit ? `${tasks.length}/${limit}` : String(tasks.length));
    if (limit) count.dataset.tip = over ? `Over the WIP limit of ${limit}` : `WIP limit ${limit}`; else delete count.dataset.tip;
    const seg = dom.segments.querySelector(`[data-status="${status}"] .seg-count`);
    if (seg) tickCounter(seg, String(tasks.length));
    const hidden = (status === 'cancelled' && !settings.showCancelled) || (settings.hideEmpty && tasks.length === 0 && !anyFilter());
    col.hidden = hidden;
    const tab = dom.segments.querySelector(`[data-status="${status}"]`);
    if (tab) tab.hidden = hidden; // the phone tab bar follows the column
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
    if (state.dropped && state.dropped.has(c.dataset.id)) animate(c, [{ transform: 'scale(0.98)', opacity: 0.6 }, { transform: 'none', opacity: 1 }], 140);
    else if (prev && prev.rect.width) {
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
  const manual = settings.sort === 'position';
  const index = manual && !col.classList.contains('is-collapsed') ? slotIndex(cols[status].body) : undefined;
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
function cleanupDrag(dropped) {
  const was = !!state.dragging && dropped !== true;
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
  // The user just put these cards down at the target: they must appear there,
  // not slide over from where they were picked up.
  state.dropped = new Set(ids);
  renderBoard();
  state.dropped = null;
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
  const out = await mutate(() => withForce((force) => api('POST', taskPath(id) + '/move', moveBody(t.status, index, force)), t), `Moved #${t.seq} to ${STATUS_LABEL[t.status]}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(id) + '/move', moveBody(origin.status, origin.index, force)), t)));
  if (out && t.status === 'done') celebrateShip();
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
// The bar is centred on a whole pixel: a translate(-50%) of an odd width lands on a half.
function centreBulkBar() {
  if (dom.bulkbar.hidden) return;
  dom.bulkbar.style.left = Math.round((window.innerWidth - dom.bulkbar.offsetWidth) / 2) + 'px';
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
  centreBulkBar();
  if (!was) animate(dom.bulkbar, [{ opacity: 0, transform: 'translateY(8px)' }, { opacity: 1, transform: 'none' }], 160);
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
  const answer = await ask({
    title: `Cancel ${plural(ids.length, 'task')}?`,
    note: 'The cards move to Cancelled. You can restore them afterwards.',
    actions: [{ value: null, label: 'Keep cards' }, { value: 'cancel', label: 'Cancel tasks', class: 'btn btn-danger' }],
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
  // One pane at a time: the toggle swaps the textarea for the rendered preview
  // and back. The formatting tools stay visible but inert while previewing.
  const tabs = el('button', { type: 'button', class: 'editor-tabs btn btn-ghost btn-xs', 'aria-pressed': 'false', 'aria-label': 'Preview', title: 'Preview', onmousedown: (e) => e.preventDefault(), onclick: () => {
    const preview = root.dataset.view !== 'preview';
    root.dataset.view = preview ? 'preview' : 'write';
    tabs.setAttribute('aria-pressed', String(preview));
    tabs.replaceChildren(icon(preview ? 'pencil' : 'eye', 14), preview ? 'Write' : 'Preview');
    if (preview) refresh(); else ta.focus();
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
  const cancel = async () => { if (!opts.onCancel) return; if (api_.dirty() && !(await confirmDiscard('The text you typed here has not been saved.'))) return; opts.onCancel(api_); };
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
  const row = el('div', { class: 'label-row' });
  const input = el('input', { type: 'text', class: 'input input-ghost min-w-[120px] flex-1 px-1.5', placeholder: opts.placeholder || 'Add label…', 'aria-label': opts.placeholder || 'Add label', autocomplete: 'off', spellcheck: 'false', role: 'combobox', 'aria-expanded': 'false', 'aria-autocomplete': 'list' });
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
// The detail is a modal at every width: a centred dialog on wide screens, a
// full-height sheet on phones (the sheet class takes over below 768px).
function mountDetail() {
  const open = !!state.detail;
  dom.detail.hidden = !open;
  if (open && !dom.detailDialog.open) { showDialog(dom.detailDialog); dom.detailBody.focus({ preventScroll: true }); }
  if (!open && dom.detailDialog.open) closeDialog(dom.detailDialog);
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
  mountDetail();
  if (!opts.route && location.hash !== '#/t/' + key) location.hash = '#/t/' + key;
  loadDetail(key).then(() => { if (opts.focusComment) focusComposer(); });
}
async function closeDetail(opts = {}) {
  if (!state.detail) return;
  if (panelDirty() && !(await confirmDiscard('This card has edits that have not been saved.'))) return;
  if (!state.detail) return;
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
const panelBusy = () => !!dom.detail.querySelector('[data-busy], [data-animating]');
const panelDirty = () => !!dom.detail.querySelector('[data-busy]:not(.editor), .editor[data-busy].is-dirty');
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
async function loadDetail(ref, silent) {
  try {
    const data = await api('GET', taskPath(ref));
    if (state.detail !== String(ref)) return;
    Object.assign(data, await detailExtras(ref, data.task));
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
const sectionEl = (title, extra, ...children) => el('section', { class: 'section' },
  el('div', { class: 'section-head' }, el('h3', { class: 'section-title' }, title), ...clean([extra].flat())), ...children);

function renderDetail(data) {
  const { task, comments = [], links = {}, tombstone = null, provenance = [], siblings = [] } = data;
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
  const blockedSwitch = el('button', { type: 'button', role: 'switch', class: 'switch', 'aria-checked': task.blocked ? 'true' : 'false', 'aria-label': 'Blocked', onclick: () => mutate(() => setBlocked(task, !task.blocked), task.blocked ? `#${task.seq} unblocked` : `#${task.seq} marked blocked`).then((out) => { if (out) loadDetail(state.detail, true); }) });
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
      task.movedAt && task.movedAt !== task.createdAt ? el('span', { class: 'text-fg-3' }, ' · moved ', el('time', { class: 'num', datetime: task.movedAt, title: fmtDate(task.movedAt) }, relTime(task.movedAt))) : null,
      task.updatedAt && task.updatedAt !== task.createdAt && task.updatedAt !== task.movedAt ? el('span', { class: 'text-fg-3' }, ' · updated ', el('time', { class: 'num', datetime: task.updatedAt, title: fmtDate(task.updatedAt) }, relTime(task.updatedAt))) : null),
    ...(tombstone ? [
      el('dt', {}, 'Killed'), el('dd', { class: 'text-12 text-fg-2' }, el('time', { class: 'num', datetime: tombstone.killedAt, title: fmtDate(tombstone.killedAt) }, fmtDate(tombstone.killedAt))),
      el('dt', {}, 'Reason'), el('dd', { class: 'text-13' }, tombstone.reason ? mdInline(tombstone.reason) : el('span', { class: 'text-fg-3' }, 'None given')),
    ] : []),
  );

  // description: rendered markdown in a bordered box; the pencil opens the editor
  const descBox = el('div', {});
  const descEdit = el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon ml-auto', 'aria-label': 'Edit description', 'data-tip': 'Edit description', onclick: () => editDesc() }, icon('pencil', 12));
  const showDesc = () => {
    descEdit.hidden = false;
    const view = renderMarkdown(task.desc, { empty: 'No description yet.' });
    descBox.replaceChildren(el('div', { class: 'desc-box' }, view));
  };
  const editDesc = () => {
    descEdit.hidden = true;
    const ed = mdEditor({ value: task.desc || '', label: 'Description', placeholder: 'Describe the task in markdown…', rows: 6,
      onSave: async (v) => { if (v !== (task.desc || '')) { const out = await save({ desc: v }, `Saved #${task.seq}`); if (out === undefined) return; task.desc = v; } showDesc(); detailIdle(); },
      onCancel: () => { showDesc(); detailIdle(); } });
    ed.root.dataset.busy = '';
    descBox.replaceChildren(ed.root);
    ed.focus();
  };
  showDesc();
  const descSection = sectionEl('Description', descEdit, descBox);

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
  const addInput = el('input', { type: 'text', class: 'input input-ghost h-8 flex-1 px-0', placeholder: 'Add an item…', 'aria-label': 'New checklist item', autocomplete: 'off' });
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
    el('div', { class: 'check-add' }, el('span', { class: 'grid size-4 place-items-center text-fg-3' }, icon('plus', 12)), addInput));

  // blockers
  const linkNumber = el('input', { type: 'text', inputmode: 'numeric', class: 'input w-20 num', placeholder: '#12', 'aria-label': 'Task number', required: true, pattern: '#?\\d+', autocomplete: 'off' });
  const linkDir = el('select', { class: 'input w-auto', 'aria-label': 'Link direction' }, el('option', { value: 'blockedBy' }, 'Blocked by'), el('option', { value: 'blocks' }, 'Blocks'));
  const linkForm = el('form', { class: 'mt-3 flex items-center gap-2', novalidate: true, onsubmit: (e) => { e.preventDefault(); addLink(task, linkDir.value, linkNumber.value); } },
    linkDir, linkNumber, el('button', { type: 'submit', class: 'btn' }, icon('link', 14), 'Link'));
  // The two link lists share the property grid, so their labels sit in the same
  // 96px column as every other label in the modal.
  const linkItems = (items) => (items.length ? items.map((t) => el('span', { class: 'link-item' }, taskLink(t),
    el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': `Unlink #${t.seq}`, title: 'Unlink', onclick: () => removeLink(task, t) }, icon('x', 12)))) : [el('span', { class: 'text-12 text-fg-3' }, 'None')]);
  const blockSection = sectionEl('Blockers', null,
    el('dl', { class: 'props' }, el('dt', {}, 'Blocked by'), el('dd', {}, ...linkItems(links.blockedBy || [])), el('dt', {}, 'Blocks'), el('dd', {}, ...linkItems(links.blocks || []))),
    linkForm);

  // comments: each one reads as text with Edit and Delete revealed on hover;
  // Edit swaps the body for an inline editor. The composer stays collapsed
  // behind an "Add a comment" row until asked for.
  const commentNode = (c) => {
    const body = el('div', {}, renderMarkdown(c.body));
    const acts = el('span', { class: 'act ml-auto flex items-center' },
      el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': 'Edit comment', title: 'Edit comment', onclick: () => editComment() }, icon('pencil', 12)),
      el('button', { type: 'button', class: 'btn btn-ghost btn-xs btn-icon', 'aria-label': 'Delete comment', title: 'Delete comment', onclick: () => deleteComment(task, c) }, icon('trash', 12)));
    const node = el('article', { class: 'comment' },
      el('div', { class: 'comment-head' }, el('span', { class: 'text-12 font-medium' }, c.author || 'default'), el('time', { class: 'num text-11 text-fg-3', datetime: c.createdAt, title: fmtDate(c.createdAt) }, relTime(c.createdAt)), acts), body);
    function editComment() {
      acts.hidden = true;
      const close = () => { body.replaceChildren(renderMarkdown(c.body)); acts.hidden = false; detailIdle(); };
      const ed = mdEditor({ value: c.body, label: 'Edit comment', rows: 3,
        onSave: async (v) => {
          if (v.trim() === c.body) { close(); return; }
          const out = await updateComment(c, v);
          if (!out) return;
          c.body = out.body;
          close();
          loadDetail(state.detail, true);
        },
        onCancel: close });
      ed.root.dataset.busy = '';
      body.replaceChildren(ed.root);
      ed.focus();
    }
    return node;
  };
  const composer = el('div', {});
  const showAdd = () => composer.replaceChildren(el('button', { type: 'button', class: 'comment-add', onclick: () => openComposer('', true) },
    el('span', { class: 'grid size-4 place-items-center' }, icon('plus', 12)), 'Add a comment'));
  const openComposer = (text, focus) => {
    const ed = mdEditor({ value: text, label: 'New comment', placeholder: 'Write a comment…', rows: 3, saveLabel: 'Comment',
      onSave: async (v, e) => { if (!(await addComment(task, v, e))) return; showAdd(); detailIdle(); loadDetail(state.detail, true); },
      onCancel: () => { showAdd(); detailIdle(); } });
    ed.root.classList.add('comment-editor');
    ed.root.dataset.busy = '';
    composer.replaceChildren(ed.root);
    if (focus) ed.focus();
  };
  if (draftText) openComposer(draftText, hadFocus); else showAdd();
  const commentSection = sectionEl('Comments', comments.length ? el('span', { class: 'num text-11 text-fg-3' }, comments.length) : null,
    comments.length ? el('div', { class: 'mb-2 flex flex-col gap-2' }, ...comments.map(commentNode)) : null, composer);

  dom.detailBody.replaceChildren(...clean([props, descSection, checkSection, blockSection, provenance.length ? provenanceSection(provenance, siblings) : null, commentSection]));
  dom.detailBody.scrollTop = scrollTop;

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
// Opens the comment composer in the detail view and focuses it.
function focusComposer() {
  const add = dom.detailBody.querySelector('.comment-add');
  if (add) { add.click(); return; }
  const ta = dom.detailBody.querySelector('.comment-editor textarea');
  if (ta) ta.focus();
}
// Posts a comment; resolves true when it landed so the caller can close the composer.
async function addComment(task, body, ed) {
  const text = body.trim();
  if (!text) return false;
  ed.ta.disabled = true;
  try {
    await api('POST', taskPath(task.id) + '/comments', { body: text });
    announce('Comment added');
    return true;
  } catch (err) { toast(err.message, 'error'); return false; } finally { ed.ta.disabled = false; }
}
// Replaces a comment's body; resolves with the saved comment, or null on failure.
async function updateComment(c, body) {
  const text = body.trim();
  if (!text) { toast('A comment needs some text', 'error'); return null; }
  try {
    const out = await api('PUT', `/api/comments/${encodeURIComponent(c.id)}`, { body: text });
    announce('Comment saved');
    return out;
  } catch (err) { toast(err.message, 'error'); return null; }
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
    // A new blocker flips the card's blocked flag, so the toggle and the chip follow the link.
    if (direction === 'blockedBy' && !task.blocked) await setBlocked(task, true);
    toast(`Linked #${task.seq} and #${other}`, 'ok');
    loadDetail(state.detail, true);
    invalidate();
  } catch (err) { toast(err.message, 'error'); }
}
async function removeLink(task, other) {
  const blocks = (state.detailData && state.detailData.links && state.detailData.links.blocks || []).some((t) => t.id === other.id);
  try {
    await api('DELETE', '/api/links', { a: task.id, b: other.id });
    // The last blocker gone clears the flag it set.
    const left = (state.detailData && state.detailData.links && state.detailData.links.blockedBy || []).filter((t) => t.id !== other.id);
    if (!blocks && task.blocked && !left.length) await setBlocked(task, false);
    loadDetail(state.detail, true);
    invalidate();
    const relink = blocks ? { blocker: task.id, blocked: other.id } : { blocker: other.id, blocked: task.id };
    toast(`Unlinked #${other.seq}`, 'ok', { life: 6000, action: { label: 'Undo', run: async () => { try { await api('POST', '/api/links', relink); loadDetail(state.detail, true); invalidate(); } catch (err) { toast(err.message, 'error'); } } } });
  } catch (err) { toast(err.message, 'error'); }
}
async function shipTask(task) {
  const groups = groupTasks();
  const prev = { status: task.status, index: groups[task.status].indexOf(findTask(task.id)) };
  const out = await mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody('done', undefined, force)), task), `Shipped #${task.seq} ${task.title}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody(prev.status, prev.index, force)), task)));
  if (out) { celebrateShip(); announce(`Moved #${task.seq} to Done`); if (state.detail) loadDetail(state.detail, true); }
}
async function cancelTask(task) {
  const answer = await ask({
    title: `Cancel #${task.seq}?`,
    message: task.title,
    note: 'The card moves to Cancelled. The reason is optional and is kept with the card.',
    field: { label: 'Reason', max: 500, placeholder: 'duplicate of #1' },
    actions: [{ value: null, label: 'Keep card' }, { value: 'cancel', label: 'Cancel card', class: 'btn btn-danger' }],
  });
  if (!answer || !answer.value) return;
  const reason = answer.text.trim();
  const groups = groupTasks();
  const prev = { status: task.status, index: groups[task.status].indexOf(findTask(task.id)) };
  const out = await mutate(() => api('POST', taskPath(task.id) + '/cancel', reason ? { reason } : {}), `Cancelled #${task.seq}`,
    () => mutate(() => withForce((force) => api('POST', taskPath(task.id) + '/move', moveBody(prev.status, prev.index, force)), task)));
  if (out) { announce(`Cancelled #${task.seq}`); if (state.detail) loadDetail(state.detail, true); }
}
async function restoreTask(task) {
  const out = await mutate(() => api('POST', taskPath(task.id) + '/restore'), `Restored #${task.seq}`,
    () => mutate(() => api('POST', taskPath(task.id) + '/cancel', {})));
  if (out) { announce(`Restored #${task.seq} to Todo`); if (state.detail) loadDetail(state.detail, true); }
}
async function deleteTask(task) {
  if (task.status !== 'cancelled') { toast('Cancel the card before deleting it permanently', 'error'); return; }
  const answer = await ask({
    title: `Delete “${task.title}” permanently?`,
    note: 'The card, its comments, links and cancellation reason are removed for good. This cannot be undone.',
    actions: [{ value: null, label: 'Keep card' }, { value: 'delete', label: 'Delete permanently', class: 'btn btn-danger' }],
  });
  if (!answer || !answer.value) return;
  const out = await mutate(() => api('DELETE', taskPath(task.id)), `Deleted #${task.seq}`);
  if (out) closeDetail();
}

/* ============================== dialogs: generic, edit ============================== */
let dialogSeq = 0;
function showDialog(d) {
  if (d.open) return;
  d.showModal();
  d.dataset.layer = String(++dialogSeq);
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
  if (d.contains(dom.toasts)) hostToasts();
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
  dom.editLabels.firstElementChild.classList.add('input', 'h-auto', 'min-h-8', 'py-0');
  dom.editTitle.textContent = task ? `Edit #${task.seq}` : 'New task';
  state.editSnapshot = JSON.stringify(readForm());
  showDialog(dom.editDialog);
  mountEditAI();
  mountEditSimilar();
  f.title.focus();
  if (!task && preset.title) loadSimilar(preset.title.trim());
}
const editDirty = () => JSON.stringify(readForm()) !== state.editSnapshot;
async function requestCloseEdit() {
  if (editDirty() && !(await confirmDiscard('The card has changes that have not been saved.'))) return;
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
  // No footer row: the header's New task button and each column's + open the composer.
  composer.hidden = !open;
  if (!open) { composer.replaceChildren(); return; }
  const input = el('input', { type: 'text', class: 'input', placeholder: 'Title  !high #label #type::bug @fri ~M', 'aria-label': `New task in ${STATUS_LABEL[status]}`, autocomplete: 'off', spellcheck: 'false', enterkeyhint: 'done' });
  const preview = el('div', { class: 'flex flex-wrap gap-1 empty:hidden', 'aria-live': 'polite' });
  const refresh = () => preview.replaceChildren(...parseQuickAdd(input.value).chips);
  const submit = async () => {
    const p = parseQuickAdd(input.value);
    if (!p.title) { input.focus(); return; }
    const body = { title: p.title, status, project: writeProject(), tags: p.tags };
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
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); renderComposer(status); cols[status].col.querySelector('.col-tools button').focus(); }
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
  // The terminal's own registry first, so ctrl+k lists what the TUI lists.
  for (const a of listedActions()) {
    const web = ACTION_WEB[a.id] || {};
    add(ACTION_GROUP[a.group] || 'Action', sentence(web.name || a.name), ACTION_RUN[a.id], web.hint || a.hint, a.id);
  }
  add('View', 'Keyboard shortcuts', () => showDialog(dom.helpDialog), '?', 'help');
  add('View', 'Display options', () => toggleDisplay(true), 'd');
  add('View', `Switch to ${currentTheme() === 'dark' ? 'light' : 'dark'} theme`, toggleTheme, '', 'dark light theme');
  add('View', `Density: ${settings.density === 'compact' ? 'comfortable' : 'compact'}`, () => { settings.density = settings.density === 'compact' ? 'comfortable' : 'compact'; applySettings(); }, '', 'compact comfortable');
  add('View', `${settings.showCancelled ? 'Hide' : 'Show'} cancelled column`, () => { settings.showCancelled = !settings.showCancelled; applySettings(); }, '', 'column');
  add('View', `${settings.hideEmpty ? 'Show' : 'Hide'} empty columns`, () => { settings.hideEmpty = !settings.hideEmpty; applySettings(); });
  if (state.project !== ALL_PROJECTS) add('Project', 'Switch to all projects', () => setProject(ALL_PROJECTS), '', 'project all');
  for (const p of projectNames()) if (p !== state.project) add('Project', `Switch to ${p}`, () => setProject(p), '', 'project');
  for (const l of allLabels()) add('Label', `${state.tags.has(l) ? 'Remove' : 'Filter'} label ${l}`, () => toggleTag(l), '', 'tag');
  for (const q of QUICK) add('Filter', `${state.quick.has(q.id) ? 'Remove filter' : 'Filter'}: ${q.label}`, () => toggleQuick(q.id));
  for (const [s, label] of SORTS) if (settings.sort !== s) add('Sort', `Sort columns: ${label.toLowerCase()}`, () => { settings.sort = s; applySettings(); });
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
function renderDisplay() {
  const row = (label, control) => el('div', { class: 'flex min-h-8 items-center gap-3' }, el('span', { class: 'flex-1 text-13' }, label), control);
  const seg = (label, options, current, onPick) => row(label, el('div', { class: 'seg seg-sm', role: 'group', 'aria-label': label },
    ...options.map(([value, text]) => el('button', { type: 'button', class: 'seg-btn', 'aria-pressed': current === value ? 'true' : 'false', onclick: () => { onPick(value); applySettings(); renderDisplay(); } }, text))));
  const check = (label, get, set) => el('label', { class: 'flex h-8 cursor-pointer items-center gap-2 text-13' }, el('input', { type: 'checkbox', class: 'cb', checked: get(), onchange: (e) => { set(e.target.checked); applySettings(); } }), label);
  const head = (text) => el('h4', { class: 'label-11 mt-3 mb-1' }, text);
  const props = [['seq', '#seq'], ['emoji', 'Emoji'], ['desc', 'Description'], ['tags', 'Labels'], ['due', 'Due'], ['effort', 'Effort'], ['checks', 'Checklist'], ['comments', 'Comments']];
  dom.display.replaceChildren(
    el('div', { class: 'mb-1 flex h-8 items-center' }, el('h3', { class: 'text-14 font-semibold' }, 'Display'), el('button', { type: 'button', class: 'btn btn-ghost btn-sm btn-icon ml-auto', 'aria-label': 'Close', onclick: () => toggleDisplay(false) }, icon('x', 14))),
    seg('Theme', [['light', 'Light'], ['dark', 'Dark'], ['system', 'System']], settings.theme, (v) => { settings.theme = v; }),
    seg('Density', [['comfortable', 'Comfortable'], ['compact', 'Compact']], settings.density, (v) => { settings.density = v; }),
    head('Card properties'),
    el('div', { class: 'grid grid-cols-2 gap-x-3' }, ...props.map(([key, label]) => check(label, () => settings.show[key], (v) => { settings.show[key] = v; }))),
    head('Columns'),
    el('div', { class: 'grid grid-cols-2 gap-x-3' }, check('Hide empty', () => settings.hideEmpty, (v) => { settings.hideEmpty = v; }), check('Show cancelled', () => settings.showCancelled, (v) => { settings.showCancelled = v; })),
    row('Sort', el('select', { class: 'input w-auto', 'aria-label': 'Column sort', onchange: (e) => { settings.sort = e.target.value; applySettings(); } },
      ...SORTS.map(([v, t]) => el('option', { value: v, selected: settings.sort === v }, t)))),
    head('WIP limits'),
    el('div', { class: 'grid grid-cols-4 gap-2' }, ...STATUSES.map((s) => el('label', { class: 'flex flex-col gap-1 text-11 text-fg-3' }, STATUS_LABEL[s], el('input', { type: 'text', class: 'input num px-2', inputmode: 'numeric', autocomplete: 'off', value: settings.wip[s] || '', placeholder: '∞', 'aria-label': `WIP limit for ${STATUS_LABEL[s]}`,
      onchange: (e) => { const n = Number(e.target.value); if (n > 0) settings.wip[s] = n; else delete settings.wip[s]; applySettings(); } })))),
  );
}
function toggleDisplay(open = dom.display.hidden) {
  const btn = $('#display-btn');
  if (open) {
    renderDisplay();
    dom.display.hidden = false;
    animate(dom.display, [{ opacity: 0, transform: 'translateY(-4px) scale(0.98)' }, { opacity: 1, transform: 'none' }], 150);
    btn.setAttribute('aria-expanded', 'true');
    const first = dom.display.querySelector('button[aria-pressed], input');
    if (first) first.focus();
  } else {
    if (dom.display.hidden) return;
    dom.display.hidden = true;
    btn.setAttribute('aria-expanded', 'false');
    if (dom.display.contains(document.activeElement) || document.activeElement === document.body) btn.focus();
  }
}
function toggleTheme() {
  settings.theme = currentTheme() === 'dark' ? 'light' : 'dark';
  applySettings(false);
}

/* ============================== ask: one modal for confirmations and short prompts ============================== */
// ask({title, message, note, field, actions}) -> Promise<{value, text} | null>; dismissing resolves null.
// Actions render left to right, so the recommended one goes last.
function ask(opts = {}) {
  return new Promise((resolve) => {
    const d = dom.askDialog;
    let settled = false;
    let input = null;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      d.removeEventListener('close', onClose);
      resolve(value === null || value === undefined ? null : { value, text: input ? input.value : '' });
      closeDialog(d);
    };
    const onClose = () => finish(null);
    dom.askTitle.textContent = opts.title || 'Are you sure?';
    const parts = [];
    if (opts.message) parts.push(el('p', { class: 'text-13' }, mdInline(opts.message)));
    if (opts.note) parts.push(el('p', { class: 'mt-2 text-12 text-fg-2' }, opts.note));
    const actions = opts.actions && opts.actions.length ? opts.actions : [{ value: null, label: 'Cancel' }, { value: 'ok', label: 'OK', class: 'btn btn-primary' }];
    const primary = actions[actions.length - 1];
    let over = false;
    if (opts.field) {
      // Never block a paste: the text lands, and the count says it is too long.
      const max = opts.field.max || 500;
      input = el('textarea', { class: 'input', rows: '2', placeholder: opts.field.placeholder || '', id: uid('ask') });
      const group = fieldGroup(opts.field.label, input, { class: 'mt-4' });
      const check = () => {
        over = input.value.length > max;
        setMsg(group, `${input.value.length}/${max}` + (over ? ` — trim ${input.value.length - max} to save` : ''), over ? 'error' : '');
        group.querySelector('.field-msg').classList.add('num');
        const go = dom.askFoot.lastElementChild;
        if (go) go.disabled = over;
      };
      input.addEventListener('input', check);
      parts.push(group);
      queueMicrotask(check);
    }
    dom.askBody.replaceChildren(...parts);
    dom.askFoot.replaceChildren(el('span', { class: 'flex-1' }), ...actions.map((a) => el('button', {
      type: a === primary ? 'submit' : 'button', class: a.class || 'btn', onclick: a === primary ? null : () => finish(a.value),
    }, a.label)));
    dom.askForm.onsubmit = (e) => { e.preventDefault(); if (!over) finish(primary.value); };
    d.addEventListener('close', onClose);
    showDialog(d);
    const last = dom.askFoot.lastElementChild;
    if (input) input.focus(); else if (last) last.focus();
  });
}

/* ============================== settings dialog: AI endpoint and forge integrations ============================== */
// Mirrors internal/tui/settings_view.go: the AI row first, then one block per configured integration.
// Secrets are write-only everywhere: a read reports only whether one is stored.
const sset = { loaded: false, loadError: '', ai: null, rows: [], draft: null, snapshot: '', savers: {} };
const FORGE_KINDS = [['gitlab', 'GitLab'], ['github', 'GitHub']];
const newForgeDraft = () => ({ name: '', kind: 'gitlab', baseURL: '', project: '', token: '', msg: '', tone: '', draft: true, armed: false });
const ssetState = () => JSON.stringify([sset.ai && [sset.ai.baseURL, sset.ai.model, sset.ai.key, sset.ai.clearKey],
  sset.rows.map((r) => [r.name, r.kind, r.baseURL, r.project, r.token, r.clearToken]),
  sset.draft && [sset.draft.name, sset.draft.kind, sset.draft.baseURL, sset.draft.project, sset.draft.token]]);

async function openSettings(section) {
  showDialog(dom.settingsDialog);
  if (!sset.loaded) {
    sset.loadError = '';
    dom.settingsBody.replaceChildren(el('div', { class: 'flex items-center gap-2 py-8 text-13 text-fg-2' }, el('span', { class: 'spinner' }), 'Loading settings…'));
    try {
      const [ai, forge] = await Promise.all([api('GET', '/api/settings/ai'), api('GET', '/api/settings/forge')]);
      sset.ai = { baseURL: ai.ai.baseURL || '', model: ai.ai.model || '', key: '', hasKey: !!ai.ai.hasKey, clearKey: false, editKey: false, msg: '', tone: '' };
      sset.rows = (forge.sources || []).map(forgeRowOf);
      sset.draft = null;
      sset.loaded = true;
    } catch (err) {
      sset.loadError = err.message;
      sset.snapshot = ssetState(); // nothing was loaded, so nothing is dirty
      dom.settingsBody.replaceChildren(el('div', { class: 'py-8' }, el('p', { class: 'field-msg is-error', role: 'alert' }, sset.loadError),
        el('button', { type: 'button', class: 'btn mt-3', onclick: () => { sset.loaded = false; openSettings(section); } }, icon('sync', 14), 'Try again')));
      return;
    }
  }
  sset.snapshot = ssetState();
  renderSettingsDialog();
  const first = dom.settingsBody.querySelector(section === 'forge' ? '[data-k^="forge"]' : '[data-k="ai-base"]') || dom.settingsBody.querySelector('input');
  if (first) first.focus();
}
const forgeRowOf = (s) => ({ name: s.name, kind: s.kind, baseURL: s.baseURL || '', project: '', token: '', hasToken: !!s.hasToken, createdAt: s.createdAt, clearToken: false, editToken: false, msg: '', tone: '', armed: false });
async function closeSettings() {
  if (ssetState() !== sset.snapshot && !(await confirmDiscard('Settings you changed here have not been saved.', { title: 'Discard unsaved settings?' }))) return;
  sset.loaded = false;
  closeDialog(dom.settingsDialog);
  // A source may have been added while the import wizard sat behind this dialog.
  if (dom.importDialog.open) { importState.sources = null; openImport(); }
}
// Returns whether anything was actually disarmed, so the caller only repaints when it matters.
function disarmForge(except) {
  let changed = false;
  for (const r of sset.rows) if (r !== except && r.armed) { r.armed = false; r.msg = ''; changed = true; }
  return changed;
}
// Rebuilds the whole body and puts the caret back where it was: every control carries a stable data-k.
function renderSettingsDialog() {
  const active = document.activeElement;
  const key = (active && dom.settingsBody.contains(active) ? active.dataset.k : null) || busyFocusKey;
  busyFocusKey = '';
  const caret = active && typeof active.selectionStart === 'number' ? active.selectionStart : null;
  sset.savers = {};
  dom.settingsBody.replaceChildren(aiSection(), forgeSection());
  if (!key) return;
  const again = dom.settingsBody.querySelector(`[data-k="${CSS.escape(key)}"]`);
  if (!again) return;
  again.focus();
  if (caret !== null && again.setSelectionRange) { try { again.setSelectionRange(caret, caret); } catch (e) { /* not a text input */ } }
}
const sectionBlock = (title, note, ...children) => el('section', { class: 'settings-section' },
  el('div', { class: 'settings-head' }, el('h3', { class: 'text-14 font-semibold' }, title), note ? el('p', { class: 'text-12 text-fg-2' }, note) : null),
  ...children);

// A saved secret never comes back from the server, so the field shows its
// state (Saved) with Replace and Remove, and only opens an input once one of
// them is chosen. noun names the thing in messages: key, token.
function secretField(label, noun, input, s) {
  const xs = (text, key, onclick) => el('button', { type: 'button', class: 'btn btn-ghost btn-xs', 'data-k': key, onclick }, text);
  if (s.saved && !s.editing && !s.clearing) {
    const row = el('div', { class: 'secret' }, icon('lock', 14), el('span', {}, 'Saved'), el('span', { class: 'dots', 'aria-hidden': 'true' }, '••••••••'),
      xs('Replace', s.k + '-edit', s.onEdit), xs('Remove', s.k + '-clear', s.onClear));
    return el('div', { class: 'field' + (s.class ? ' ' + s.class : '') }, el('div', { class: 'field-head' }, el('span', { class: 'label-11' }, label)), row, el('p', { class: 'field-msg' }));
  }
  const back = s.saved ? xs(s.clearing ? `Keep the saved ${noun}` : 'Cancel', s.k + '-back', s.onBack) : null;
  const field = fieldGroup(label, input, { action: back, class: s.class });
  if (s.clearing) setMsg(field, `Removed when you save. Paste a ${noun} to keep one.`, 'warn');
  return field;
}

function aiSection() {
  const ai = sset.ai;
  const text = (k, attrs) => el('input', Object.assign({ class: 'input', name: k, autocomplete: 'off', spellcheck: 'false', 'data-k': k, id: uid('ai'), oninput: (e) => { ai[k.slice(3)] = e.target.value; } }, attrs));
  const base = text('ai-baseURL', { type: 'url', value: ai.baseURL, placeholder: 'https://api.openai.com/v1' });
  base.dataset.k = 'ai-base';
  const model = text('ai-model', { value: ai.model, placeholder: 'gpt-4o' });
  const keyInput = el('input', { type: 'password', class: 'input', name: 'ai-key', autocomplete: 'new-password', spellcheck: 'false', 'data-k': 'ai-key', id: uid('ai'), value: ai.key,
    placeholder: ai.hasKey ? 'paste the new key' : 'sk-…', oninput: (e) => { ai.key = e.target.value; } });
  const status = el('p', { class: 'field-msg' + (ai.tone ? ' is-' + ai.tone : ''), role: 'status' }, ai.msg || '');
  const say = (msg, tone) => { ai.msg = msg; ai.tone = tone; status.className = 'field-msg' + (tone ? ' is-' + tone : ''); status.replaceChildren(msg); };
  const test = el('button', { type: 'button', class: 'btn', 'data-k': 'ai-test', onclick: async () => {
    const stop = busy(test, 'Testing…');
    say('', '');
    try {
      await api('POST', '/api/settings/ai/test', { baseURL: ai.baseURL.trim(), model: ai.model.trim(), apiKey: ai.key }, { timeout: 20000 });
      say('connection ok', 'ok');
    } catch (err) { say(err.message, 'error'); } finally { stop(); }
  } }, icon('sync', 14), 'Test connection');
  const save = el('button', { type: 'submit', class: 'btn btn-primary', 'data-k': 'ai-save' }, 'Save AI settings');
  sset.savers.ai = () => saveAI(save, say);
  const keyField = secretField('API key', 'key', keyInput, {
    saved: ai.hasKey, editing: ai.editKey, clearing: ai.clearKey, k: 'ai-key',
    onEdit: () => { ai.editKey = true; busyFocusKey = 'ai-key'; renderSettingsDialog(); },
    onClear: () => { ai.clearKey = true; ai.key = ''; say('The saved key is removed when you save.', 'warn'); renderSettingsDialog(); },
    onBack: () => { ai.editKey = false; ai.clearKey = false; ai.key = ''; say('', ''); renderSettingsDialog(); },
  });
  const on = !!(state.ai && state.ai.configured);
  let host = '';
  try { host = new URL(state.ai && state.ai.baseURL).host; } catch (e) { host = (state.ai && state.ai.baseURL) || ''; }
  const stateRow = el('div', { class: 'ai-state' + (on ? ' is-on' : ''), role: 'status' }, el('span', { class: 'status-dot', 'aria-hidden': 'true' }),
    on ? [el('span', { class: 'k' }, state.ai.model || 'no model'), el('span', { class: 'num' }, host), el('span', { class: 'ml-auto text-fg-3' }, 'saved')] : el('span', {}, 'No AI endpoint yet. Drafting, splitting and duplicate checks stay off until one is saved.'));
  return sectionBlock('AI', 'An OpenAI-compatible endpoint. The model must support function calling. The key is stored sealed and is never sent back to this page.',
    stateRow,
    el('form', { class: 'grid gap-4 sm:grid-cols-2', 'data-save': 'ai', novalidate: true, onsubmit: (e) => { e.preventDefault(); saveAI(save, say); } },
      fieldGroup('Base URL', base, { class: 'sm:col-span-2' }),
      fieldGroup('Model', model),
      keyField,
      el('div', { class: 'settings-actions sm:col-span-2' }, test, save, status)));
}
async function saveAI(btn, say) {
  const ai = sset.ai;
  const stop = busy(btn, 'Saving…');
  say('', '');
  try {
    const body = { baseURL: ai.baseURL.trim(), model: ai.model.trim() };
    if (ai.clearKey) body.apiKey = ''; else if (ai.key) body.apiKey = ai.key;
    const out = await api('PUT', '/api/settings/ai', body);
    ai.baseURL = out.ai.baseURL || '';
    ai.model = out.ai.model || '';
    ai.hasKey = !!out.ai.hasKey;
    ai.key = '';
    ai.clearKey = false;
    ai.editKey = false;
    ai.msg = out.keyCleared ? 'saved; endpoint changed, re-enter the API key' : 'AI settings saved';
    ai.tone = out.keyCleared ? 'warn' : 'ok';
    sset.snapshot = ssetState();
    refreshAIStatus();
    renderSettingsDialog();
  } catch (err) { say(err.message, 'error'); stop(); }
}

function forgeSection() {
  const rows = sset.rows.map(forgeRow);
  const draft = sset.draft ? forgeRow(sset.draft) : null;
  const add = el('button', { type: 'button', class: 'btn', 'data-k': 'forge-add', onclick: () => { disarmForge(); sset.draft = newForgeDraft(); renderSettingsDialog(); const f = dom.settingsBody.querySelector('[data-k="forge-new-name"]'); if (f) f.focus(); } }, icon('plus', 14), 'Add integration');
  return sectionBlock('Integrations', 'GitLab and GitHub sources the issue import reads from. Tokens are write-only and never leave the server.',
    rows.length || draft ? el('div', { class: 'rows' }, ...rows, draft) : el('p', { class: 'rounded-md border border-dashed border-line-2 px-3 py-6 text-center text-13 text-fg-3' }, 'No integrations yet.'),
    el('div', { class: 'settings-actions' }, add));
}
function forgeRow(row) {
  const isDraft = !!row.draft;
  const k = (name) => (isDraft ? 'forge-new-' : `forge-${row.name}-`) + name;
  const text = (name, attrs) => el('input', Object.assign({ class: 'input', name: k(name), autocomplete: 'off', spellcheck: 'false', 'data-k': k(name), id: uid('fg'),
    oninput: (e) => { row[name] = e.target.value; if (disarmForge()) renderSettingsDialog(); } }, attrs));
  const status = el('p', { class: 'field-msg' + (row.tone ? ' is-' + row.tone : ''), role: 'status' }, row.msg || '');
  const say = (msg, tone) => { row.msg = msg; row.tone = tone; status.className = 'field-msg' + (tone ? ' is-' + tone : ''); status.replaceChildren(msg); };
  const head = isDraft
    ? el('div', { class: 'grid gap-4 sm:grid-cols-2' },
      fieldGroup('Name', text('name', { value: row.name, placeholder: 'work-gitlab' })),
      el('div', { class: 'field' }, el('span', { class: 'label-11' }, 'Kind'),
        segGroup('Kind', FORGE_KINDS, row.kind, (v) => { row.kind = v; renderSettingsDialog(); }, 'seg seg-grow'), el('p', { class: 'field-msg' })))
    : el('div', { class: 'row-head' },
      el('span', { class: 'chip chip-mono' }, el('span', {}, row.kind)),
      el('span', { class: 'text-13 font-semibold' }, row.name),
      el('span', { class: 'text-11 text-fg-3', title: 'The name and kind of a saved source cannot change' }, 'locked'),
      row.createdAt ? el('span', { class: 'num ml-auto text-11 text-fg-3', title: fmtDate(row.createdAt) }, 'added ', relTime(row.createdAt)) : null);
  const tokenInput = el('input', { type: 'password', class: 'input', name: k('token'), autocomplete: 'new-password', spellcheck: 'false', 'data-k': k('token'), id: uid('fg'), value: row.token,
    placeholder: row.hasToken ? 'paste the new token' : 'personal access token', oninput: (e) => { row.token = e.target.value; if (disarmForge()) renderSettingsDialog(); } });
  const tokenField = secretField('Token', 'token', tokenInput, {
    saved: row.hasToken, editing: row.editToken, clearing: row.clearToken, k: k('token'), class: 'sm:col-span-2',
    onEdit: () => { disarmForge(); row.editToken = true; busyFocusKey = k('token'); renderSettingsDialog(); },
    onClear: () => { disarmForge(); row.clearToken = true; row.token = ''; say('The saved token is removed when you save.', 'warn'); renderSettingsDialog(); },
    onBack: () => { row.editToken = false; row.clearToken = false; row.token = ''; say('', ''); renderSettingsDialog(); },
  });
  const test = el('button', { type: 'button', class: 'btn', 'data-k': k('test'), onclick: async () => {
    const stop = busy(test, 'Testing…');
    say('', '');
    try {
      await api('POST', `/api/settings/forge/${encodeURIComponent(row.name.trim().toLowerCase())}/test`,
        { kind: row.kind, baseURL: row.baseURL.trim(), project: row.project.trim(), token: row.token, saved: !isDraft }, { timeout: 30000 });
      say('connection ok', 'ok');
    } catch (err) { say(err.message, 'error'); } finally { stop(); }
  } }, 'Test');
  const save = el('button', { type: 'submit', class: 'btn btn-primary', 'data-k': k('save') }, 'Save');
  sset.savers[isDraft ? 'forge:new' : 'forge:' + row.name] = () => saveForge(row, save, say);
  const remove = el('button', { type: 'button', class: 'btn' + (row.armed ? ' btn-danger' : ' btn-ghost'), 'data-k': k('remove'), onclick: () => removeForge(row, say) }, row.armed ? 'Confirm remove' : 'Remove');
  return el('div', { class: 'row row-form' },
    el('form', { class: 'row-main', 'data-save': isDraft ? 'forge:new' : 'forge:' + row.name, novalidate: true, onsubmit: (e) => { e.preventDefault(); saveForge(row, save, say); } }, head,
      el('div', { class: 'grid gap-4 sm:grid-cols-2' },
        fieldGroup('Base URL', text('baseURL', { value: row.baseURL, placeholder: row.kind === 'github' ? 'github.com' : 'gitlab.example.com' })),
        fieldGroup('Project', text('project', { value: row.project, placeholder: 'owner/project (optional)' }), { hint: 'test only' }),
        tokenField),
      el('div', { class: 'settings-actions' }, test, save, remove, status)));
}
async function saveForge(row, btn, say) {
  const focusField = (n) => { const node = dom.settingsBody.querySelector(`[data-k="${CSS.escape((row.draft ? 'forge-new-' : `forge-${row.name}-`) + n)}"]`); if (node) node.focus(); };
  const name = row.name.trim().toLowerCase();
  if (!name) { say('integration name is required', 'error'); focusField('name'); return; }
  if (!/^[a-z0-9._-]{1,64}$/.test(name)) { say('name may use letters, digits, dot, dash and underscore only', 'error'); focusField('name'); return; }
  if (row.draft && sset.rows.some((r) => r.name.toLowerCase() === name)) { say('integration name already exists', 'error'); focusField('name'); return; }
  if (!row.baseURL.trim()) { say('forge base URL is required', 'error'); focusField('baseURL'); return; }
  const stop = busy(btn, 'Saving…');
  say('', '');
  try {
    const body = { kind: row.kind, baseURL: row.baseURL.trim() };
    if (row.clearToken) body.token = ''; else if (row.token) body.token = row.token;
    const out = await api('PUT', `/api/settings/forge/${encodeURIComponent(name)}`, body);
    const saved = forgeRowOf(out.source);
    saved.project = row.project;
    saved.msg = out.tokenCleared ? 'saved; endpoint changed, re-enter the token' : 'integration saved';
    saved.tone = out.tokenCleared ? 'warn' : 'ok';
    const at = sset.rows.findIndex((r) => r.name === saved.name);
    if (at >= 0) sset.rows[at] = saved; else sset.rows.push(saved);
    if (row.draft) sset.draft = null;
    sset.snapshot = ssetState();
    renderSettingsDialog();
  } catch (err) { say(err.message, 'error'); stop(); }
}
async function removeForge(row, say) {
  if (row.draft) { sset.draft = null; renderSettingsDialog(); return; }
  if (!row.armed) { disarmForge(row); row.armed = true; say(`click Confirm remove to remove ${row.name}`, 'warn'); renderSettingsDialog(); return; }
  try {
    await api('DELETE', `/api/settings/forge/${encodeURIComponent(row.name)}`);
    sset.rows = sset.rows.filter((r) => r !== row);
    sset.snapshot = ssetState();
    renderSettingsDialog();
    toast(`Removed the ${row.name} integration`, 'ok');
  } catch (err) { row.armed = false; say(err.message, 'error'); renderSettingsDialog(); }
}

/* ============================== AI in the card editor ============================== */
// The editor's AI field: a prompt, one round trip, and a filled-in form the user still has to save.
function mountEditAI() {
  const box = dom.editAI;
  box.hidden = false;
  if (!state.ai || !state.ai.configured) {
    box.replaceChildren(el('p', { class: 'text-12 text-fg-3' }, 'Drafting with AI is off. ', settingsLink('Add an AI endpoint', 'ai'), ' to write a card from a one-line prompt.'));
    return;
  }
  const ta = el('textarea', { rows: '1', id: uid('ai-prompt'), placeholder: 'Describe the card in one line', autocomplete: 'off', 'aria-label': 'AI draft prompt' });
  const status = el('p', { class: 'field-msg', role: 'status' });
  const commentary = el('div', { class: 'mt-2 text-12 text-fg-2 empty:hidden' });
  const run = el('button', { type: 'button', class: 'btn btn-primary', 'data-ai-draft': '', onclick: () => runAIDraft(ta, run, status, commentary) }, icon('sparkle', 14), 'Draft');
  // The prompt grows with its text up to a few lines; the action stays pinned to the pill's edge.
  const grow = () => { ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight, 160) + 'px'; };
  ta.addEventListener('input', grow);
  const group = el('div', { class: 'field' },
    el('div', { class: 'field-head' },
      el('label', { class: 'label-11 flex items-center gap-1.5', for: ta.id }, el('span', { class: 'ai-mark', 'aria-hidden': 'true' }, 'AI'), 'Draft'),
      el('span', { class: 'text-11 text-fg-3' }, el('kbd', { class: 'kbd' }, MOD), ' ', el('kbd', { class: 'kbd' }, '⇧'), ' ', el('kbd', { class: 'kbd' }, 'Enter'))),
    el('div', { class: 'ai-composer' }, icon('sparkle', 16), ta, run), status, commentary);
  ta.addEventListener('keydown', (e) => { if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); runAIDraft(ta, run, status, commentary); } });
  box.replaceChildren(group);
}
async function runAIDraft(ta, btn, status, commentary) {
  const prompt = ta.value.trim();
  if (!prompt) { status.className = 'field-msg is-error'; status.textContent = 'Say what the card should cover.'; ta.focus(); return; }
  const form = readForm();
  const body = { prompt };
  if (state.editing || form.title || form.desc) body.card = { title: form.title, desc: form.desc, prio: form.prio, due: form.due, effort: form.effort, tags: form.tags, checks: form.checks };
  const stop = busy(btn, 'Drafting…');
  status.className = 'field-msg';
  status.textContent = '';
  commentary.replaceChildren();
  try {
    const out = await api('POST', '/api/ai/draft', body, { timeout: 120000 });
    applyDraft(out.card || {});
    status.className = 'field-msg is-ok';
    status.replaceChildren(...clean(['AI draft applied; review before saving',
      out.partial ? el('span', { class: 'chip chip-tone ml-2', style: '--tone: var(--t-warn)' }, icon('alert', 11), el('span', {}, 'Partial run')) : null]));
    if (out.commentary) commentary.replaceChildren(renderMarkdown(out.commentary));
    announce('AI draft applied; review before saving');
  } catch (err) {
    if (err.status === 409 && err.body && err.body.aiUnconfigured) { state.ai = { configured: false }; mountEditAI(); return; }
    status.className = 'field-msg is-error';
    status.textContent = err.message;
  } finally { stop(); }
}
// Fills the open editor from a draft card. Nothing is written until the user saves.
function applyDraft(card) {
  const f = dom.editForm.elements;
  if (card.title) f.title.value = card.title;
  if (card.emoji) f.emoji.value = card.emoji;
  if (card.desc && editDescEditor) editDescEditor.set(card.desc);
  if (card.prio) f.prio.value = String(card.prio);
  if (card.due) f.due.value = card.due;
  if (card.effort !== undefined) renderEffortSeg(card.effort || '');
  if (card.tags && card.tags.length && editLabelEditor) editLabelEditor.set(normalizeTags(card.tags));
  if (card.checks && card.checks.length) f.checks.value = serializeChecks(card.checks);
  loadSimilar(f.title.value.trim());
}

// Possible duplicates while the title is being typed: the same store search the TUI runs.
let similarTimer = 0;
function mountEditSimilar() {
  dom.editSimilar.hidden = true;
  dom.editSimilar.replaceChildren();
  const title = dom.editForm.elements.title;
  if (title._similar) title.removeEventListener('input', title._similar);
  title._similar = () => { clearTimeout(similarTimer); similarTimer = setTimeout(() => loadSimilar(title.value.trim()), 300); };
  title.addEventListener('input', title._similar);
}
async function loadSimilar(q) {
  if (!dom.editDialog.open) return;
  if (q.length < 3) { dom.editSimilar.hidden = true; dom.editSimilar.replaceChildren(); return; }
  let items = [];
  try { items = (await api('GET', `/api/similar?q=${encodeURIComponent(q)}&limit=10`)).items || []; } catch (err) { return; }
  if (!dom.editDialog.open) return;
  // The search answers with card hits (via "card") and upstream import hits (via "import",
  // which carry a link instead of a task id). Resolve the second kind onto a card when one
  // carries the link, and never list the same card twice.
  const seen = new Set(state.editing ? [state.editing.id] : []);
  const rows = [];
  for (const i of items) {
    const byLink = i.link ? state.tasks.find((t) => (t.tags || []).includes('link::' + i.link)) : null;
    const known = localTask(i.id) || byLink;
    const id = (known && known.id) || i.id;
    if (id && seen.has(id)) continue;
    if (id) seen.add(id);
    rows.push({ title: i.title, status: (known && known.status) || i.status || 'todo', seq: known && known.seq, id, link: i.via === 'import' ? i.link : '' });
  }
  if (!rows.length) { dom.editSimilar.hidden = true; dom.editSimilar.replaceChildren(); return; }
  dom.editSimilar.hidden = false;
  dom.editSimilar.replaceChildren(
    el('p', { class: 'label-11 mb-1', role: 'status' }, `Possible duplicates (${rows.length})`),
    el('ul', { class: 'flex flex-col gap-0.5' }, ...rows.map((r) => el('li', { class: 'flex items-center gap-1.5' },
      r.id
        ? el('button', { type: 'button', class: 'task-link min-w-0', style: `--hue: var(--t-${r.status})`, title: `Open ${r.title}`,
          onclick: () => { closeDialog(dom.editDialog); openDetail(r.seq || r.id); } },
          el('i', { class: 'status-dot' }), r.seq ? el('span', { class: 'seq' }, '#' + r.seq) : null, el('span', { class: 't ' + r.status }, r.title))
        : el('span', { class: 'task-link min-w-0', style: `--hue: var(--t-${r.status})` }, el('i', { class: 'status-dot' }), el('span', { class: 't ' + r.status }, r.title)),
      r.link ? el('span', { class: 'chip chip-mono', title: 'Imported from this upstream issue' }, el('span', {}, r.link)) : null))));
}

/* ============================== split an ADR into stories ============================== */
const MAX_ADR_BYTES = 65536;
const splitState = { stage: 'input', text: '', max: 8, status: 'todo', cards: [], commentary: '', partial: false, error: '', created: [] };
function openSplit() {
  Object.assign(splitState, { stage: 'input', cards: [], commentary: '', partial: false, error: '', created: [] });
  if (splitState.status === 'cancelled') splitState.status = 'todo';
  showDialog(dom.splitDialog);
  renderSplit();
  const ta = dom.splitBody.querySelector('textarea');
  if (ta) ta.focus();
}
async function closeSplit() {
  if (splitState.stage === 'review' && splitState.cards.some((c) => c.include) && !(await confirmDiscard('The proposed stories have not been created yet.', { title: 'Discard the proposed stories?', cancel: 'Keep reviewing' }))) return;
  closeDialog(dom.splitDialog);
}
function renderSplit() {
  $('#split-dialog-title').textContent = splitState.stage === 'input' ? 'Split ADR into stories' : 'Review proposed stories';
  if (splitState.stage === 'input') renderSplitInput(); else renderSplitReview();
}
function renderSplitInput() {
  const bytes = utf8Bytes(splitState.text);
  const ta = el('textarea', { class: 'input font-mono text-12', rows: '12', id: uid('adr'), spellcheck: 'false',
    placeholder: '# ADR 0007: adopt SQLite for the board store\n\n## Context\n…', oninput: (e) => { splitState.text = e.target.value; counter.replaceChildren(byteLabel(utf8Bytes(e.target.value))); } });
  ta.value = splitState.text;
  const byteLabel = (n) => `UTF-8 bytes: ${n.toLocaleString()} / ${MAX_ADR_BYTES.toLocaleString()}`;
  const counter = el('span', {}, byteLabel(bytes));
  // The input stays out of the tab order; the visible button is the control.
  const file = el('input', { type: 'file', hidden: true, accept: '.md,.markdown,.txt,text/markdown,text/plain', tabindex: '-1', 'aria-hidden': 'true', onchange: (e) => {
    const f = e.target.files && e.target.files[0];
    e.target.value = '';
    if (!f) return;
    if (f.size > MAX_ADR_BYTES) { splitState.error = `${f.name} is ${Math.round(f.size / 1024)} KiB. The limit is 64 KiB — trim it first.`; renderSplit(); return; }
    const reader = new FileReader();
    reader.onload = () => { splitState.text = String(reader.result || ''); splitState.error = ''; renderSplit(); };
    reader.onerror = () => { splitState.error = `Could not read ${f.name}.`; renderSplit(); };
    reader.readAsText(f);
  } });
  const group = fieldGroup('ADR markdown', ta);
  group.querySelector('.field-msg').replaceChildren(counter);
  if (bytes > MAX_ADR_BYTES) setMsg(group, byteLabel(bytes) + ' — over the limit; trim the document', 'error');
  setKids(dom.splitBody,
    el('p', { class: 'mb-3 text-12 text-fg-2' }, 'Paste one decision or design document. Nothing is created until you review the stories it proposes.'),
    group,
    el('div', { class: 'mt-2 flex flex-wrap items-center gap-2' }, file,
      el('button', { type: 'button', class: 'btn', onclick: () => file.click() }, icon('upload', 14), 'Read a file…'),
      splitState.text ? el('button', { type: 'button', class: 'btn btn-ghost btn-sm', onclick: () => { splitState.text = ''; renderSplit(); ta.focus(); } }, 'Clear') : null),
    el('div', { class: 'mt-4 flex flex-wrap items-end gap-4' },
      el('div', { class: 'field' }, el('span', { class: 'label-11' }, 'Max stories', el('span', { class: 'ml-1.5 font-normal tracking-normal text-fg-3' }, '1–20')),
        stepper('Max stories', splitState.max, (n) => { splitState.max = n; }), el('p', { class: 'field-msg' })),
      fieldGroup('Destination', el('select', { class: 'input w-auto', id: uid('dest'), onchange: (e) => { splitState.status = e.target.value; } },
        ...STATUSES.filter((s) => s !== 'cancelled').map((s) => el('option', { value: s, selected: splitState.status === s }, STATUS_LABEL[s]))))),
    splitState.error ? el('p', { class: 'field-msg is-error mt-3', role: 'alert' }, splitState.error) : null,
    state.ai && !state.ai.configured ? el('p', { class: 'mt-3 text-12 text-fg-3' }, 'Splitting needs an AI endpoint. ', settingsLink('Set one up in Settings', 'ai'), '.') : null);
  if (splitState.focusText) { splitState.focusText = false; ta.focus(); }
  const propose = el('button', { type: 'button', class: 'btn btn-primary', onclick: () => runSplit(propose) }, icon('sparkle', 14), 'Propose stories');
  setKids(dom.splitFoot, el('span', { class: 'flex-1' }), el('button', { type: 'button', class: 'btn', onclick: closeSplit }, 'Cancel'), propose);
}
async function runSplit(btn) {
  const text = splitState.text.trim();
  if (!text) { splitState.error = 'Paste an ADR first.'; splitState.focusText = true; renderSplit(); return; }
  if (utf8Bytes(text) > MAX_ADR_BYTES) { splitState.error = 'The document is over 64 KiB. Trim it and try again.'; splitState.focusText = true; renderSplit(); return; }
  const stop = busy(btn, 'Proposing stories…');
  splitState.error = '';
  try {
    const out = await api('POST', '/api/ai/split', { text, max: splitState.max }, { timeout: 180000 });
    splitState.cards = (out.cards || []).map((c) => Object.assign({ include: true, created: null, error: '' }, c, { prio: c.prio || 3, effort: c.effort || '' }));
    splitState.commentary = out.commentary || '';
    splitState.partial = !!out.partial;
    if (!splitState.cards.length) { splitState.error = 'The model returned no usable stories.'; stop(); renderSplit(); return; }
    splitState.stage = 'review';
    renderSplit();
    announce(`${splitState.cards.length} stories ready; review before creating`);
  } catch (err) {
    splitState.error = err.message;
    stop();
    renderSplit();
  }
}
// One review row for a proposed card: include, title, priority, effort.
// onChange only refreshes the primary button's count; it never rebuilds the row.
function draftRow(card, onChange, extra, index, noun) {
  const n = (index || 0) + 1;
  const label = `${noun || 'Story'} ${n}`;
  const title = el('input', { class: 'input', value: card.title, 'aria-label': `${label} title`, oninput: (e) => { card.title = e.target.value; if (onChange) onChange(); } });
  const box = el('input', { type: 'checkbox', class: 'cb mt-2', checked: card.include, 'aria-label': `Include ${label}`, onchange: (e) => { card.include = e.target.checked; row.classList.toggle('is-off', !card.include); if (onChange) onChange(); } });
  const prio = el('select', { class: 'input w-auto', 'aria-label': `${label} priority`, onchange: (e) => { card.prio = Number(e.target.value); } },
    ...[1, 2, 3].map((p) => el('option', { value: p, selected: (card.prio || 3) === p }, PRIO_LABEL[p])));
  const effort = el('select', { class: 'input w-auto', 'aria-label': `${label} effort`, onchange: (e) => { card.effort = e.target.value; } },
    el('option', { value: '', selected: !card.effort }, 'No effort'), ...EFFORTS.map((x) => el('option', { value: x, selected: card.effort === x }, x)));
  const row = el('div', { class: 'row' + (card.include ? '' : ' is-off'), role: 'group', 'aria-label': label }, box,
    el('div', { class: 'row-main' }, title,
      el('div', { class: 'flex flex-wrap items-center gap-2' }, prio, effort,
        ...(card.tags || []).filter((t) => !isProjectTag(t) && !isProvenanceTag(t)).map((t) => chipEl(t)),
        ...clean([extra].flat())),
      card.error ? el('p', { class: 'field-msg is-error' }, card.error) : null,
      card.created ? el('p', { class: 'field-msg is-ok' }, `created #${card.created.seq}`) : null));
  return row;
}
function renderSplitReview() {
  const selected = () => splitState.cards.filter((c) => c.include && c.title.trim() && !c.created).length;
  const add = el('button', { type: 'button', class: 'btn btn-primary', onclick: () => createSplitCards(add) }, '');
  const count = () => { const n = selected(); add.disabled = n === 0; add.textContent = `Add selected (${n})`; };
  setKids(dom.splitFoot, el('span', { class: 'flex-1' }),
    el('button', { type: 'button', class: 'btn', onclick: () => { splitState.stage = 'input'; renderSplit(); } }, icon('chevL', 14), 'Back to source'),
    el('button', { type: 'button', class: 'btn', onclick: closeSplit }, 'Close'), add);
  setKids(dom.splitBody,
    el('p', { class: 'mb-3 text-12 text-fg-2' }, 'Nothing is created until you add the selected stories. They go to ',
      el('span', { class: 'font-medium text-fg' }, STATUS_LABEL[splitState.status]), ' in ', el('span', { class: 'font-medium text-fg' }, writeProject()), '.'),
    splitState.partial ? el('p', { class: 'mb-3 flex items-center gap-2 text-12 text-warn' }, icon('alert', 14), 'The run hit its budget: these stories are real, but the set may be incomplete.') : null,
    splitState.commentary ? el('div', { class: 'mb-4 rounded-md border border-line bg-surface px-3 py-2' }, renderMarkdown(splitState.commentary)) : null,
    el('div', { class: 'rows' }, ...splitState.cards.map((c, i) => draftRow(c, count, null, i, 'Story'))),
    splitState.error ? el('p', { class: 'field-msg is-error mt-3', role: 'alert' }, splitState.error) : null,
    el('div', { class: 'mt-3 empty:hidden', id: 'split-progress' }));
  count();
}
async function createSplitCards(btn) {
  const queue = splitState.cards.filter((c) => c.include && c.title.trim() && !c.created);
  if (!queue.length) return;
  const stop = busy(btn, 'Adding…');
  const bar = el('i', { style: 'width:0%' });
  const line = el('p', { class: 'field-msg', role: 'status' }, `creating card 1 of ${queue.length}…`);
  const track = el('div', { class: 'check-progress mb-1', role: 'progressbar', 'aria-label': 'Creating stories', 'aria-valuemin': '0', 'aria-valuemax': String(queue.length), 'aria-valuenow': '0' }, bar);
  $('#split-progress').replaceChildren(track, line);
  const made = [];
  for (let i = 0; i < queue.length; i++) {
    const card = queue[i];
    line.textContent = `creating card ${i + 1} of ${queue.length}…`;
    bar.style.width = Math.round((i / queue.length) * 100) + '%';
    track.setAttribute('aria-valuenow', String(i));
    try {
      const body = { title: card.title.trim(), status: splitState.status, prio: card.prio || 3, tags: normalizeTags(card.tags || []), checks: card.checks || [], project: writeProject() };
      for (const k of ['emoji', 'desc', 'due']) if (card[k]) body[k] = card[k];
      if (card.effort) body.effort = card.effort;
      card.created = await api('POST', '/api/tasks', body);
      card.error = '';
      made.push(card.created);
    } catch (err) { card.error = err.message; }
  }
  bar.style.width = '100%';
  track.setAttribute('aria-valuenow', String(queue.length));
  stop();
  splitState.created = splitState.created.concat(made);
  const failed = queue.filter((c) => c.error).length;
  splitState.error = failed ? `created ${made.length}; ${failed} failed — fix the reported rows and retry` : '';
  renderSplit();
  invalidate();
  if (!made.length) return;
  toast(`Created ${plural(made.length, 'card')} from the ADR`, 'ok', { life: 8000, action: { label: 'Undo', run: () => undoCreated(made) } });
  announce(`created ${made.length} cards`);
}
// Rolls a batch back the only way the store allows: cancel each card it just wrote.
async function undoCreated(cards) {
  for (const t of cards) {
    try { await api('POST', taskPath(t.id) + '/cancel', { reason: 'undone right after import' }); } catch (err) { toast(err.message, 'error'); break; }
  }
  invalidate();
}

/* ============================== import forge issues ============================== */
const importState = { stage: 'input', sources: null, source: '', ref: '', max: 8, preview: null, drafts: [], error: '', created: [], done: false };
async function openImport() {
  Object.assign(importState, { stage: 'input', preview: null, drafts: [], error: '', created: [], done: false });
  showDialog(dom.importDialog);
  renderImport();
  if (importState.sources === null) {
    try { importState.sources = (await api('GET', '/api/settings/forge')).sources || []; } catch (err) { importState.sources = []; importState.error = err.message; }
    if (!importState.source && importState.sources.length) importState.source = importState.sources[0].name;
    renderImport();
  }
  const first = dom.importBody.querySelector('select, input');
  if (first) first.focus();
}
async function closeImport() {
  if (importState.stage === 'review' && !importState.done && importState.drafts.some((d) => d.include) && !(await confirmDiscard('The fetched issues have not been imported yet.', { title: 'Discard the fetched issues?', cancel: 'Keep reviewing' }))) return;
  closeDialog(dom.importDialog);
}
function renderImport() {
  $('#import-dialog-title').textContent = importState.stage === 'input' ? 'Forge issue import' : 'Review fetched issues';
  if (importState.stage === 'input') renderImportInput(); else renderImportReview();
}
function renderImportInput() {
  const sources = importState.sources;
  if (sources === null) {
    setKids(dom.importBody, el('div', { class: 'flex items-center gap-2 py-8 text-13 text-fg-2' }, el('span', { class: 'spinner' }), 'Loading sources…'));
    setKids(dom.importFoot, el('span', { class: 'flex-1' }), el('button', { type: 'button', class: 'btn', onclick: closeImport }, 'Cancel'));
    return;
  }
  if (!sources.length) {
    setKids(dom.importBody,
      el('div', { class: 'rounded-md border border-dashed border-line-2 px-4 py-8 text-center' },
        el('p', { class: 'text-13 font-medium' }, 'No forge integration configured'),
        el('p', { class: 'mx-auto mt-1 max-w-[46ch] text-12 text-fg-2' }, 'Add a GitLab or GitHub source with a personal access token, then come back to import its issues.'),
        el('button', { type: 'button', class: 'btn btn-primary mt-4', onclick: () => openSettings('forge') }, icon('gear', 14), 'Open settings')));
    setKids(dom.importFoot, el('span', { class: 'flex-1' }), el('button', { type: 'button', class: 'btn', onclick: closeImport }, 'Cancel'));
    return;
  }
  const refInput = el('input', { class: 'input', id: uid('ref'), name: 'forge-ref', value: importState.ref, autocomplete: 'off', spellcheck: 'false',
    placeholder: 'owner/repo, or an issue, milestone or project URL', oninput: (e) => { importState.ref = e.target.value; } });
  refInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); const b = dom.importFoot.querySelector('.btn-primary'); if (b) b.click(); } });
  setKids(dom.importBody,
    el('div', { class: 'grid gap-3 sm:grid-cols-[minmax(0,200px)_minmax(0,1fr)]' },
      fieldGroup('Source', el('select', { class: 'input', id: uid('src'), onchange: (e) => { importState.source = e.target.value; } },
        ...sources.map((s) => el('option', { value: s.name, selected: s.name === importState.source }, `${s.name} · ${s.kind}`)))),
      fieldGroup('Reference', refInput)),
    el('div', { class: 'mt-4 field' }, el('span', { class: 'label-11' }, 'Max issues', el('span', { class: 'ml-1.5 font-normal tracking-normal text-fg-3' }, '1–20')),
      stepper('Max issues', importState.max, (n) => { importState.max = n; }), el('p', { class: 'field-msg' })),
    importState.error ? el('p', { class: 'field-msg is-error mt-3', role: 'alert' }, importState.error) : null,
    state.ai && !state.ai.configured
      ? el('p', { class: 'mt-4 text-12 text-fg-3' }, 'The preview turns fetched issues into cards with the AI endpoint, so it needs one configured. ', settingsLink('Set one up in Settings', 'ai'), '.')
      : el('p', { class: 'mt-4 text-12 text-fg-3' }, 'The preview fetches the issues and drafts cards from them. Nothing is written until you review them.'));
  const preview = el('button', { type: 'button', class: 'btn btn-primary', onclick: () => runPreview(preview) }, icon('sync', 14), 'Preview');
  setKids(dom.importFoot, el('span', { class: 'flex-1' }), el('button', { type: 'button', class: 'btn', onclick: closeImport }, 'Cancel'), preview);
  if (importState.focusRef) { importState.focusRef = false; refInput.focus(); }
}
async function runPreview(btn) {
  const ref = importState.ref.trim();
  if (!ref) { importState.error = 'reference required'; importState.focusRef = true; renderImport(); return; }
  const stop = busy(btn, 'Fetching and drafting…');
  importState.error = '';
  try {
    const out = await api('POST', '/api/forge/preview', { source: importState.source, ref, max: importState.max }, { timeout: 180000 });
    importState.preview = out;
    importState.drafts = (out.drafts || []).map((d) => Object.assign({ include: !(d.duplicate && d.duplicate.via === 'link'), created: null, error: '' }, d, { prio: d.prio || 3, effort: d.effort || '' }));
    if (!importState.drafts.length) { importState.error = 'no issues fetched'; stop(); renderImport(); return; }
    await resolveDuplicates(importState.drafts);
    importState.stage = 'review';
    renderImport();
    announce('review proposals; exact duplicates start unticked');
  } catch (err) { importState.error = err.message; stop(); renderImport(); }
}
// A duplicate marker names a card id; the reviewer needs its number. Cards outside the
// current project are not in memory, so look those up once per preview.
const localTask = (id) => state.all.concat(state.tasks).find((t) => t.id === id);
async function resolveDuplicates(drafts) {
  const wanted = [...new Set(drafts.map((d) => d.duplicate && d.duplicate.id).filter((id) => id && !localTask(id)))];
  await Promise.all(wanted.map(async (id) => {
    try {
      const seq = (await api('GET', taskPath(id))).task.seq;
      for (const d of drafts) if (d.duplicate && d.duplicate.id === id) d.duplicate.seq = seq;
    } catch (err) { /* the badge falls back to the title */ }
  }));
}
function duplicateBadge(dup) {
  if (!dup) return null;
  const known = localTask(dup.id);
  const seq = known ? known.seq : dup.seq;
  const label = dup.via === 'link' ? (seq ? `Already imported as #${seq}` : 'Already imported') : `Similar: ${dup.title}`;
  const tone = dup.via === 'link' ? 'var(--t-warn)' : 'var(--t-fg-2)';
  if (!seq) return el('span', { class: 'chip chip-tone', style: `--tone: ${tone}`, title: dup.title }, icon('alert', 11), el('span', {}, label));
  return el('button', { type: 'button', class: 'chip chip-tone', style: `--tone: ${tone}`, title: `Open #${seq} ${dup.title}`,
    onclick: () => { closeDialog(dom.importDialog); openDetail(seq); } }, icon('alert', 11), el('span', {}, label));
}
function renderImportReview() {
  const p = importState.preview || {};
  const selected = () => importState.drafts.filter((d) => d.include && d.title.trim() && !d.created).length;
  const go = el('button', { type: 'button', class: 'btn btn-primary', onclick: () => runImport(go) }, '');
  const count = () => { const n = selected(); go.disabled = n === 0; go.textContent = `Import selected (${n})`; };
  setKids(dom.importFoot, el('span', { class: 'flex-1' }),
    el('button', { type: 'button', class: 'btn', onclick: () => { importState.stage = 'input'; renderImport(); } }, icon('chevL', 14), 'Back'),
    el('button', { type: 'button', class: 'btn', onclick: closeImport }, 'Close'), go);
  const fetched = p.totalHint && p.totalHint > p.fetched
    ? `Fetched ${p.fetched} of about ${p.totalHint}${p.truncated ? '; results truncated' : ''}`
    : `Fetched ${p.fetched || importState.drafts.length}${p.truncated ? '; results truncated' : ''}`;
  setKids(dom.importBody,
    el('p', { class: 'text-12 text-fg-2' }, `${fetched} · ${p.kind || 'issue'} · ${importState.source}`),
    p.note ? el('p', { class: 'mt-2 flex items-start gap-2 text-12 text-warn' }, icon('alert', 14), p.note) : null,
    el('p', { class: 'mb-3 mt-2 text-12 text-fg-2' }, 'Cards already carrying the same upstream link start unticked. Nothing is written until you import.'),
    el('div', { class: 'rows' }, ...importState.drafts.map((d, i) => draftRow(d, count, duplicateBadge(d.duplicate), i, 'Issue'))),
    importState.error ? el('p', { class: 'field-msg is-error mt-3', role: 'alert' }, importState.error) : null,
    importState.created.length ? el('div', { class: 'mt-4' }, el('h3', { class: 'label-11 mb-1' }, `Imported (${importState.created.length})`),
      el('ul', { class: 'flex flex-col gap-0.5' }, ...importState.created.map((t) => el('li', {}, taskLink(t))))) : null,
    el('div', { class: 'mt-3 empty:hidden', id: 'import-progress' }));
  count();
}
async function runImport(btn) {
  const queue = importState.drafts.filter((d) => d.include && d.title.trim() && !d.created);
  if (!queue.length) return;
  const stop = busy(btn, 'Importing…');
  const bar = el('i', { style: 'width:0%' });
  const line = el('p', { class: 'field-msg', role: 'status' }, `writing 1/${queue.length}`);
  const track = el('div', { class: 'check-progress mb-1', role: 'progressbar', 'aria-label': 'Importing issues', 'aria-valuemin': '0', 'aria-valuemax': String(queue.length), 'aria-valuenow': '0' }, bar);
  $('#import-progress').replaceChildren(track, line);
  const made = [];
  for (let i = 0; i < queue.length; i++) {
    const d = queue[i];
    line.textContent = `writing ${i + 1}/${queue.length}`;
    bar.style.width = Math.round((i / queue.length) * 100) + '%';
    track.setAttribute('aria-valuenow', String(i));
    const task = { title: d.title.trim(), prio: d.prio || 3, tags: d.tags || [], checks: d.checks || [], project: writeProject() };
    for (const k of ['emoji', 'desc', 'due']) if (d[k]) task[k] = d[k];
    if (d.effort) task.effort = d.effort;
    try {
      d.created = await api('POST', '/api/forge/import', { source: d.source || importState.source, task, link: { externalKey: d.externalKey, link: d.link, url: d.url, title: d.title, baseline: d.baseline } }, { timeout: 120000 });
      d.error = '';
      made.push(d.created);
    } catch (err) { d.error = err.message; }
  }
  bar.style.width = '100%';
  track.setAttribute('aria-valuenow', String(queue.length));
  stop();
  importState.created = importState.created.concat(made);
  importState.done = importState.drafts.every((d) => d.created || !d.include);
  const failed = queue.filter((d) => d.error).length;
  importState.error = failed ? `imported ${made.length}; ${failed} failed — fix the reported rows and retry` : '';
  renderImport();
  invalidate();
  if (!made.length) return;
  toast(`Imported ${plural(made.length, 'card')} from ${importState.source}`, 'ok', { life: 8000, action: { label: 'Undo', run: () => undoCreated(made) } });
  announce('import complete');
}

/* ============================== provenance and upstream drift ============================== */
const DRIFT_STATE = { unchanged: 'unchanged', drifted: 'drifted', baseline_recorded: 'baseline recorded' };
function provenanceSection(links, siblings) {
  return sectionEl('Provenance', el('span', { class: 'text-11 text-fg-3' }, 'kb never syncs upstream changes into a card'),
    el('div', { class: 'rows' }, ...links.map((l) => provenanceRow(l, siblings.filter((s) => s.link === l.link)))));
}
function provenanceRow(link, siblings) {
  const drift = state.drift[link.externalKey];
  const body = el('div', { class: 'row-main' },
    el('div', { class: 'flex flex-wrap items-center gap-2' },
      el('span', { class: 'chip chip-mono' }, el('span', {}, link.kind || 'forge')),
      el('span', { class: 'text-13 font-medium' }, link.source),
      el('span', { class: 'seq' }, link.link)),
    link.title ? el('p', { class: 'text-13' }, link.title) : null,
    link.url ? el('p', { class: 'text-12' }, openLink(link.url)) : null,
    siblings && siblings.length ? el('p', { class: 'text-12 text-fg-2' }, `Also imported onto ${plural(siblings.length, 'other card')}: `,
      ...siblings.flatMap((s, i) => clean([i ? ', ' : null,
        el('button', { type: 'button', class: 'text-link underline decoration-line-2 underline-offset-2 hover:decoration-current', onclick: () => openDetail(s.id) }, s.title)]))) : null,
    driftBlock(link, drift));
  return el('div', { class: 'row' }, body);
}
function driftBlock(link, drift) {
  const check = el('button', { type: 'button', class: 'btn btn-sm', onclick: () => checkDrift(link, check) }, icon('sync', 14), 'Check drift');
  const wrap = el('div', { class: 'flex flex-col gap-2' });
  if (!drift) { wrap.append(el('div', { class: 'flex flex-wrap items-center gap-2' }, check)); return wrap; }
  const rows = [];
  const line = (label, value) => rows.push(el('dt', {}, label), el('dd', {}, value));
  line('State', el('span', { class: 'chip chip-tone', style: `--tone: var(--t-${drift.state === 'drifted' ? 'warn' : drift.state === 'unchanged' ? 'ok' : 'fg-2'})` }, el('span', {}, DRIFT_STATE[drift.state] || drift.state)));
  line('Title', drift.titleChanged ? el('span', { class: 'text-warn' }, 'changed upstream') : el('span', { class: 'text-fg-2' }, 'unchanged'));
  if (drift.upstreamTitle) line('Upstream', drift.upstreamTitle);
  if (drift.baselineTitle) line('Baseline', drift.baselineTitle);
  if (drift.checkedAt) line('Checked', el('time', { class: 'num', datetime: drift.checkedAt, title: fmtDate(drift.checkedAt) }, relTime(drift.checkedAt)));
  wrap.append(el('dl', { class: 'grid grid-cols-[72px_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1 text-12 [&_dd]:text-fg [&_dt]:text-fg-3' }, ...rows));
  if (drift.state === 'baseline_recorded') wrap.append(el('p', { class: 'field-msg' }, 'No import snapshot existed; the comparison starts from this check.'));
  if (drift.summary) wrap.append(el('div', { class: 'rounded-md border border-line bg-surface px-3 py-2' }, renderMarkdown(drift.summary)));
  const accept = el('button', { type: 'button', class: 'btn btn-sm btn-primary', onclick: () => acceptDrift(link, drift, accept) }, 'Update baseline');
  wrap.append(el('div', { class: 'flex flex-wrap items-center gap-2' }, check, drift.state === 'drifted' && drift.revision ? accept : null));
  return wrap;
}
async function checkDrift(link, btn) {
  const stop = busy(btn, 'Checking drift…');
  try {
    state.drift[link.externalKey] = await api('POST', '/api/forge/drift/check', { source: link.source, externalKey: link.externalKey }, { timeout: 60000 });
    announce(`Upstream ${link.link} is ${DRIFT_STATE[state.drift[link.externalKey].state] || 'checked'}`);
  } catch (err) { toast(err.message, 'error'); } finally {
    stop();
    if (state.detailData) renderDetail(state.detailData);
  }
}
async function acceptDrift(link, drift, btn) {
  const stop = busy(btn, 'Updating baseline…');
  try {
    await api('POST', '/api/forge/drift/accept', { source: link.source, externalKey: link.externalKey, revision: drift.revision });
    delete state.drift[link.externalKey];
    toast('Upstream baseline updated', 'ok');
    await loadDetail(state.detail, true);
  } catch (err) {
    toast(err.status === 409 ? 'Upstream changed again. Check it before updating the card.' : err.message, 'error');
  } finally {
    stop();
    if (state.detailData) renderDetail(state.detailData);
  }
}

/* ============================== keyboard registry: palette rows and the help sheet ============================== */
const ACTION_GROUP = { navigate: 'Navigate', act: 'Actions', dismiss: 'Session' };
// What the web does differently from the terminal. null hides a row that a browser tab cannot honour.
const ACTION_WEB = { quit: null, jumpColumn: { hint: 'alt+1-4' } };
const actionTarget = () => (focusedCard() ? findTask(state.focusId) : null) || (state.detailData ? state.detailData.task : null);
const ACTION_RUN = {
  openCard: () => { const t = actionTarget(); if (t) openDetail(t.seq); },
  liftCard: () => toggleLift(),
  filterText: () => focusSearch(),
  filterLabel: () => focusLabels(),
  filterClear: () => clearFilters(),
  switchProject: () => toggleProjectMenu(true),
  shipCard: () => { const t = actionTarget(); if (t && isOpen(t)) shipTask(t); },
  cancelCard: () => { const t = actionTarget(); if (t && t.status !== 'cancelled') cancelTask(t); },
  restoreCard: () => { const t = actionTarget(); if (t && t.status === 'cancelled') restoreTask(t); },
  purgeCard: () => { const t = actionTarget(); if (t) deleteTask(t); },
  newCard: () => openEdit(null),
  editCard: () => { const t = actionTarget(); if (t) openEdit(t); },
  openSettings: () => openSettings(),
  splitADR: () => openSplit(),
  importIssue: () => openImport(),
};
const listedActions = () => state.actions.filter((a) => a.enabled && a.group !== 'dismiss' && a.id !== 'openPalette' && ACTION_RUN[a.id] && ACTION_WEB[a.id] !== null);
// "j/k" -> two caps; "ctrl+k" -> one reading "Ctrl K"; "? or esc" -> two; a lone "/" stays one.
function keyCaps(hint) {
  return String(hint || '').split(/\s+or\s+|(?<=.)\/(?=.)/).map((s) => s.trim()).filter(Boolean).map((part) => {
    const bits = part.split('+');
    return el('kbd', { class: 'kbd' }, bits.map((k) => (k.length > 1 ? sentence(k) : bits.length > 1 ? k.toUpperCase() : k)).join(' '));
  });
}
function renderHelp() {
  const groups = new Map();
  for (const a of state.actions) {
    if (ACTION_WEB[a.id] === null) continue;
    const g = ACTION_GROUP[a.group] || 'Other';
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g).push(Object.assign({}, a, ACTION_WEB[a.id] || {}));
  }
  const block = (title, rows) => el('section', {},
    el('h3', { class: 'label-11 mb-2' }, title),
    el('dl', { class: 'grid grid-cols-[minmax(0,124px)_minmax(0,1fr)] items-center gap-x-3 gap-y-2' },
      ...rows.flatMap(([keys, name]) => [el('dt', { class: 'flex flex-wrap items-center gap-1' }, ...keys), el('dd', { class: 'text-13 text-fg-2' }, name)])));
  const cap = (...parts) => parts.map((p) => el('kbd', { class: 'kbd' }, p));
  const named = (title) => (groups.has(title) ? block(title, groups.get(title).map((a) => [keyCaps(a.hint), sentence(a.name)])) : null);
  // Two columns, filled row by row: the long lists pair up so neither column runs away.
  const nodes = clean([named('Navigate'), named('Actions')]);
  nodes.push(block('This board only', [
    [cap('d'), 'Display options'],
    [cap('1', '2', '3'), 'Set priority'],
    [cap('c'), 'Comment on the card'],
    [cap('v'), 'Select card'],
    [cap(MOD + ' A'), 'Select the whole column'],
    [cap('Shift click'), 'Extend the selection'],
    [cap(MOD + ' click'), 'Toggle one card'],
    [[el('span', { class: 'text-12 text-fg-3' }, 'Drag')], 'Move cards; on touch, long-press to lift'],
    [cap(MOD + ' B', MOD + ' I'), 'Editor: bold, italic'],
    [cap(MOD + ' K', MOD + ' E'), 'Editor: link, inline code'],
    [cap(MOD + ' Enter'), 'Editor: save'],
    [cap(MOD + ' ⇧ Enter'), 'Editor: draft with AI'],
    [cap('Esc'), 'Close, cancel a lift, clear the selection'],
  ]));
  for (const [title, rows] of groups) {
    if (title === 'Navigate' || title === 'Actions') continue;
    nodes.push(block(title, rows.map((a) => [keyCaps(a.hint), sentence(a.name)])));
  }
  dom.helpBody.replaceChildren(...nodes);
}

/* ============================== toasts / announcements ============================== */
// A modal dialog makes everything outside it inert, top layer included, so the
// toast stack lives inside the newest open dialog while one is up.
function hostToasts() {
  const open = $$('dialog[open]').sort((a, b) => Number(b.dataset.layer || 0) - Number(a.dataset.layer || 0));
  const host = open[0] || document.body;
  if (dom.toasts.parentElement !== host) host.append(dom.toasts); // moving a popover hides it
  if (POPOVER && dom.toasts.children.length && !dom.toasts.matches(':popover-open')) dom.toasts.showPopover();
}
function toast(message, kind = 'error', opts = {}) {
  const life = opts.life || (kind === 'error' ? 6000 : 3000);
  let gone = false;
  const dismiss = () => {
    if (gone) return;
    gone = true;
    const a = animate(node, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateY(-8px)' }], 150);
    a.onfinish = a.oncancel = () => { node.remove(); if (POPOVER && !dom.toasts.children.length && dom.toasts.matches(':popover-open')) dom.toasts.hidePopover(); };
  };
  const node = el('div', { class: 'toast ' + kind }, el('span', { class: 'min-w-0 flex-1 truncate' }, mdInline(message)),
    opts.action ? el('button', { type: 'button', class: 'btn', onclick: () => { dismiss(); opts.action.run(); } }, opts.action.label) : null,
    el('button', { type: 'button', class: 'btn btn-icon', 'aria-label': 'Dismiss', onclick: dismiss }, icon('x', 12)));
  dom.toasts.append(node);
  hostToasts();
  animate(node, [{ opacity: 0, transform: 'translateY(-8px) scale(0.98)' }, { opacity: 1, transform: 'none' }], 180);
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
// Switching is this browser's business alone: the selection is remembered with
// the other display settings and never told to the server, which keeps no
// active project. The "all" scope is a client-side view.
function setProject(name) {
  if (!name || name === state.project) return;
  state.project = name;
  settings.project = name;
  saveSettings();
  state.selected.clear();
  renderHeader();
  invalidate();
  announce(name === ALL_PROJECTS ? 'All projects' : `Project ${name}`);
}
function cycleProject(dir) {
  const list = [ALL_PROJECTS, ...projectList()];
  if (list.length < 2) return;
  const i = list.indexOf(state.project);
  setProject(list[(i + dir + list.length) % list.length]);
}
// The search field lives in a centred dialog (Spotlight-style) on every device.
// Typing filters the board behind it live; Enter or Escape closes and the
// query stays applied, marked by a dot on the header icon.
const SEARCH_SYNTAX = [['tag:web', 'Label'], ['prio:high', 'Priority'], ['due:week', 'Due'], ['is:blocked', 'Blocked'], ['has:checklist', 'Checklist'], ['effort:S', 'Effort'], ['#12', 'Number']];
function mountSearch() {
  dom.searchSyntax.replaceChildren(...SEARCH_SYNTAX.map(([token, label]) => el('button', { type: 'button', class: 'chip chip-mono', title: `${label} filter`, onmousedown: (e) => e.preventDefault(), onclick: () => insertSearchToken(token) }, token)));
  dom.searchClear.addEventListener('click', () => { dom.search.value = ''; applySearchText('', false); dom.search.focus(); });
  dom.searchBtn.addEventListener('click', () => (dom.searchDialog.open ? closeDialog(dom.searchDialog) : openSearch()));
  $('#search-form').addEventListener('submit', (e) => { e.preventDefault(); clearTimeout(dom.search._t); applySearchText(dom.search.value.trim(), false); closeDialog(dom.searchDialog); });
}
function insertSearchToken(token) {
  const cur = dom.search.value.trim();
  if (cur.split(/\s+/).includes(token)) return;
  dom.search.value = (cur ? cur + ' ' : '') + token + ' ';
  applySearchText(dom.search.value.trim(), false);
  dom.search.focus();
}
function openSearch() {
  if (dom.searchDialog.open) { dom.search.focus(); dom.search.select(); return; }
  showDialog(dom.searchDialog);
  dom.search.focus();
  dom.search.select();
}
function setFiltersOpen(open) {
  dom.filters.classList.toggle('open', open);
  $('#filters-toggle').setAttribute('aria-expanded', String(open));
  if (open) {
    dom.scrim.hidden = false;
    requestAnimationFrame(() => dom.scrim.classList.add('show'));
  } else {
    dom.scrim.classList.remove('show');
    setTimeout(() => { dom.scrim.hidden = true; }, dur(160));
  }
}
function focusSearch() { openSearch(); }
function focusLabels() {
  if (phoneMQ.matches) { setFiltersOpen(true); return; }
  const first = dom.labels.querySelector('button');
  if (first) first.focus(); else openPalette();
}
const isTyping = (target) => !!(target && target.closest && target.closest('input, textarea, select, [contenteditable="true"]'));

function onKeydown(e) {
  if (e.defaultPrevented) return;
  const stack = $$('dialog[open]');
  const openDlg = stack[stack.length - 1] || null; // the topmost dialog owns the keyboard
  const mod = e.ctrlKey || e.metaKey;
  if (mod && e.key.toLowerCase() === 's' && openDlg === dom.settingsDialog) {
    e.preventDefault();
    const section = document.activeElement && document.activeElement.closest ? document.activeElement.closest('[data-save]') : null;
    const save = sset.savers[section ? section.dataset.save : 'ai'] || sset.savers.ai;
    if (save) save();
    return;
  }
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
    if (!dom.display.hidden) { e.preventDefault(); toggleDisplay(false); return; }
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
  // alt+1-4 is the TUI's "jump to column"; the bare digits stay priority on the board.
  if (e.altKey && !mod && /^[1-4]$/.test(e.key) && !openDlg) {
    const status = STATUSES[Number(e.key) - 1];
    if (status && !cols[status].col.hidden) {
      e.preventDefault();
      cols[status].col.scrollIntoView({ behavior: dur(1) ? 'smooth' : 'auto', inline: 'start', block: 'nearest' });
      const first = columnCards(status)[0];
      if (first) setFocus(first.dataset.id); else announce(`${STATUS_LABEL[status]} is empty`);
    }
    return;
  }
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
    case 'D': if (target) { e.preventDefault(); deleteTask(target); } break;
    case '1': case '2': case '3': if (target) { e.preventDefault(); setPriority(target, Number(e.key)); } break;
    case 'c': if (target) { e.preventDefault(); openDetail(target.seq, { focusComment: true }); } break;
    case 'v': if (focused) { e.preventDefault(); toggleSelect(focused.id); } break;
    case '/': if (!openDlg) { e.preventDefault(); focusSearch(); } break;
    case 'f': if (!openDlg) { e.preventDefault(); focusLabels(); } break;
    case 'X': if (!openDlg) { e.preventDefault(); clearFilters(); } break;
    case 'p': if (!openDlg) cycleProject(1); break;
    case 'P': if (!openDlg) cycleProject(-1); break;
    case 'd': if (!openDlg) { e.preventDefault(); toggleDisplay(); } break;
    case 's': if (!openDlg) { e.preventDefault(); openSettings(); } break;
    case 'a': if (!openDlg) { e.preventDefault(); openSplit(); } break;
    case 'i': if (!openDlg) { e.preventDefault(); openImport(); } break;
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
function bind() {
  dom.search.addEventListener('input', () => {
    clearTimeout(dom.search._t);
    dom.search._t = setTimeout(() => applySearchText(dom.search.value.trim(), false), 200);
  });
  dom.search.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); clearTimeout(dom.search._t); applySearchText(dom.search.value.trim(), false); closeDialog(dom.searchDialog); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeDialog(dom.searchDialog); } // keep the query; a search-type input would clear it
  });
  dom.projectBtn.addEventListener('click', () => toggleProjectMenu());
  dom.clear.addEventListener('click', clearFilters);
  $('#new-task').addEventListener('click', () => openEdit(null));
  $('#help-btn').addEventListener('click', () => showDialog(dom.helpDialog));
  $('#theme-toggle').addEventListener('click', toggleTheme);
  $('#display-btn').addEventListener('click', () => toggleDisplay());
  $('#settings-btn').addEventListener('click', () => openSettings());
  $('#palette-btn').addEventListener('click', openPalette);
  $('#filters-toggle').addEventListener('click', () => setFiltersOpen(!dom.filters.classList.contains('open')));
  $('#filters-done').addEventListener('click', () => setFiltersOpen(false));
  dom.scrim.addEventListener('click', () => setFiltersOpen(false));
  $('#detail-close').addEventListener('click', () => closeDetail());
  dom.paletteInput.addEventListener('input', renderPalette);

  dom.editForm.addEventListener('submit', (e) => { e.preventDefault(); saveEdit(); });
  dom.editForm.addEventListener('keydown', (e) => {
    if (!(e.ctrlKey || e.metaKey)) return;
    if (e.shiftKey && e.key === 'Enter') {
      const run = dom.editAI.querySelector('[data-ai-draft]');
      if (run && !run.disabled) { e.preventDefault(); run.click(); }
      return;
    }
    if (!e.shiftKey && (e.key === 's' || e.key === 'Enter')) { e.preventDefault(); saveEdit(); }
  });
  dom.editDialog.addEventListener('cancel', (e) => { e.preventDefault(); requestCloseEdit(); });
  dom.editDialog.addEventListener('close', () => { state.editing = null; });

  const closers = new Map([[dom.editDialog, requestCloseEdit], [dom.detailDialog, () => closeDetail()],
    [dom.settingsDialog, closeSettings], [dom.splitDialog, closeSplit], [dom.importDialog, closeImport]]);
  for (const d of [dom.editDialog, dom.detailDialog, dom.helpDialog, dom.palette, dom.searchDialog, dom.settingsDialog, dom.splitDialog, dom.importDialog, dom.askDialog]) {
    const close = closers.get(d) || (() => closeDialog(d));
    for (const btn of $$('[data-close]', d)) btn.addEventListener('click', close);
    d.addEventListener('click', (e) => { if (e.target === d) close(); });
    if (d !== dom.editDialog) d.addEventListener('cancel', (e) => { e.preventDefault(); close(); });
  }
  dom.detailDialog.addEventListener('close', () => { if (state.detail) closeDetail(); });
  document.addEventListener('pointerdown', (e) => {
    if (!dom.display.hidden && !dom.display.contains(e.target) && !e.target.closest('#display-btn')) toggleDisplay(false);
    if (!dom.projectMenu.hidden && !dom.projectMenu.contains(e.target) && !e.target.closest('#project-btn')) toggleProjectMenu(false);
  });
  document.addEventListener('keydown', onKeydown);
  window.addEventListener('resize', centreBulkBar);
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState !== 'visible') return;
    startLive();
    refreshTasks();
  });
  window.addEventListener('pagehide', stopLive);
  window.addEventListener('pageshow', startLive);
  window.addEventListener('online', () => refreshTasks());
  window.addEventListener('hashchange', applyRoute);
  window.addEventListener('beforeunload', (e) => { if ((dom.editDialog.open && editDirty()) || panelDirty()) e.preventDefault(); });
  wideMQ.addEventListener('change', () => { renderBoard(); });
  phoneMQ.addEventListener('change', () => { renderBoard(); });
  trackActiveSegment();
  }

/* ============================== init ============================== */
async function init() {
  $('#settings-mod').textContent = MOD;
  initControls();
  initTips();
  applySettings(false);
  buildBoard();
  mountSearch();
  bind();
  mountDetail();
  await Promise.all([refreshMeta(), refreshProjects()]);
  // The route names the project, else the one this browser last chose, else
  // the first on the board — inbox when there is none yet.
  const m = /^#\/p\/(.+)$/.exec(decodeURIComponent(location.hash || ''));
  state.project = m ? m[1] : settings.project || writeProject();
  renderHeader();
  await refreshTasks();
  if (!state.loaded) for (const status of STATUSES) cols[status].body.replaceChildren(el('div', { class: 'col-empty' }, 'Waiting for the server…'));
  applyRoute();
  startLive();
  refreshActions();
  refreshAIStatus();
  refreshShipped();
}
init();
