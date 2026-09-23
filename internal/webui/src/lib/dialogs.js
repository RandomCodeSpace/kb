// The dialog stack: names of the open dialogs, bottom to top. Each Dialog view
// registers its element so toasts and popovers can land inside the topmost one
// (a modal makes everything outside it inert, top layer included).
import { state, notify } from '../store';

export const dialogEls = {};
export function openDialog(name) {
  if (state.dialogs.includes(name)) return;
  state.dialogs = [...state.dialogs, name];
  notify();
}
export function closeDialog(name) {
  if (!state.dialogs.includes(name)) return;
  state.dialogs = state.dialogs.filter((n) => n !== name);
  notify();
}
export const topDialog = () => state.dialogs[state.dialogs.length - 1] || null;
export const isDialogOpen = (name) => state.dialogs.includes(name);
export const topDialogElement = () => (topDialog() && dialogEls[topDialog()]) || null;

// ask({title, message, note, field, actions}) -> Promise<{value, text} | null>; dismissing resolves null.
// Actions render left to right, so the recommended one goes last.
export function ask(opts = {}) {
  return new Promise((resolve) => {
    if (state.ask) state.ask.finish(null);
    let settled = false;
    state.ask = {
      opts,
      finish(value, text = '') {
        if (settled) return;
        settled = true;
        state.ask = null;
        closeDialog('ask');
        resolve(value === null || value === undefined ? null : { value, text });
      },
    };
    openDialog('ask');
  });
}
// confirmDiscard(message, {title, ok, cancel}) -> Promise<boolean>; the board's own dialog, never window.confirm.
export const confirmDiscard = (message, opts = {}) => ask({ title: opts.title || 'Discard changes?', message,
  actions: [{ value: null, label: opts.cancel || 'Keep editing' }, { value: 'ok', label: opts.ok || 'Discard', variant: 'destructive' }] }).then((r) => !!(r && r.value));
