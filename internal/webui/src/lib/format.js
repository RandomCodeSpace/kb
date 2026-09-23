// Formatting and small pure helpers shared by the views and the logic modules.
export const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
export const wideMQ = matchMedia('(min-width: 1024px)');
export const phoneMQ = matchMedia('(max-width: 767px)');
export const dur = (ms) => (reducedMotion.matches ? 0 : ms);
export const isMac = /Mac|iPhone|iPad/.test(navigator.platform || '');
export const MOD = isMac ? '⌘' : 'Ctrl';
export const EASE = 'cubic-bezier(0.2, 0, 0, 1)';

export function animate(node, frames, ms, extra) {
  if (!node || !node.animate) return { finished: Promise.resolve(), onfinish: null };
  return node.animate(frames, Object.assign({ duration: dur(ms), easing: EASE, fill: 'none' }, extra || {}));
}
export function localToday() {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), now.getDate());
}
export function isoDate(d) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
export function dueDays(due) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(due || '');
  if (!m) return null;
  return Math.round((new Date(+m[1], +m[2] - 1, +m[3]) - localToday()) / 86400000);
}
// Relative due label and tone: '' | 'soon' | 'overdue'.
export function dueChip(due) {
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
export function fmtDate(iso) {
  const d = new Date(iso);
  return isNaN(d) ? iso : dateFmt.format(d);
}
export function relTime(iso) {
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
export const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
export const DOW = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];
export const parseDay = (iso) => { const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso || ''); return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null; };
// "14 Sep 2026": one fixed shape, whatever the OS locale says.
export function fmtDay(iso) { const d = parseDay(iso); return d ? `${d.getDate()} ${MONTHS[d.getMonth()].slice(0, 3)} ${d.getFullYear()}` : ''; }

export const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
export const clamp = (n, lo, hi) => Math.max(lo, Math.min(hi, n));
export const sentence = (s) => (s ? s[0].toUpperCase() + s.slice(1) : '');
let uidN = 0;
export const uid = (prefix) => `${prefix}-${++uidN}`;
export const utf8Bytes = (s) => new TextEncoder().encode(s).length;
export const clean = (list) => list.filter(Boolean);

// Subsequence fuzzy match: returns {score, positions} or null.
export function fuzzy(query, text) {
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
export function insertText(ta, text) {
  ta.focus();
  if (document.execCommand && document.execCommand('insertText', false, text)) return; // keeps native undo
  ta.setRangeText(text, ta.selectionStart, ta.selectionEnd, 'end');
  ta.dispatchEvent(new Event('input', { bubbles: true }));
}
// Sizes a textarea to its content, up to max.
export function growTextarea(ta, min = 0, max = 320) {
  if (!ta) return;
  ta.style.height = 'auto';
  ta.style.height = Math.min(Math.max(ta.scrollHeight + 2, min), max) + 'px';
}
export const isTyping = (target) => !!(target && target.closest && target.closest('input, textarea, select, [contenteditable="true"], [role="combobox"], [data-slot="select-trigger"]'));
