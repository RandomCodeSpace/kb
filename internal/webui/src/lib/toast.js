import { state, notify } from '../store';

let seq = 0;
// toast(message, 'ok' | 'warn' | 'error', {life, action: {label, run}})
export function toast(message, kind = 'error', opts = {}) {
  const life = opts.life || (kind === 'error' ? 6000 : 3000);
  const id = ++seq;
  state.toasts = [...state.toasts, { id, message, kind, action: opts.action || null, leaving: false }].slice(-4);
  notify();
  setTimeout(() => dismissToast(id), life);
}
export function dismissToast(id) {
  const t = state.toasts.find((x) => x.id === id);
  if (!t || t.leaving) return;
  state.toasts = state.toasts.map((x) => (x.id === id ? { ...x, leaving: true } : x));
  notify();
  setTimeout(() => { state.toasts = state.toasts.filter((x) => x.id !== id); notify(); }, 150);
}
export function announce(text) {
  state.liveText = '';
  notify();
  setTimeout(() => { state.liveText = text; notify(); }, 30);
}
