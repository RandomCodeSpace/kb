// Split an ADR into stories: paste a document, review the proposals, create the ticked ones.
import { notify } from '../store';
import { api, taskPath, invalidate } from './api';
import { toast, announce } from './toast';
import { openDialog, closeDialog, confirmDiscard } from './dialogs';
import { utf8Bytes, plural } from './format';
import { normalizeTags } from './labels';
import { writeProject } from './filters';

export const MAX_ADR_BYTES = 65536;
export const splitState = { stage: 'input', text: '', max: 8, status: 'todo', cards: [], commentary: '', partial: false, error: '', created: [], busy: false, progress: null, focusText: false };
export function openSplit() {
  Object.assign(splitState, { stage: 'input', cards: [], commentary: '', partial: false, error: '', created: [], busy: false, progress: null, focusText: true });
  if (splitState.status === 'cancelled') splitState.status = 'todo';
  openDialog('split');
  notify();
}
export async function closeSplit() {
  if (splitState.stage === 'review' && splitState.cards.some((c) => c.include) && !(await confirmDiscard('The proposed stories have not been created yet.', { title: 'Discard the proposed stories?', cancel: 'Keep reviewing' }))) return;
  closeDialog('split');
}
export function setSplit(patch) { Object.assign(splitState, patch); notify(); }
export function readSplitFile(f) {
  if (!f) return;
  if (f.size > MAX_ADR_BYTES) { setSplit({ error: `${f.name} is ${Math.round(f.size / 1024)} KiB. The limit is 64 KiB — trim it first.` }); return; }
  const reader = new FileReader();
  reader.onload = () => setSplit({ text: String(reader.result || ''), error: '' });
  reader.onerror = () => setSplit({ error: `Could not read ${f.name}.` });
  reader.readAsText(f);
}
export async function runSplit() {
  const text = splitState.text.trim();
  if (!text) { setSplit({ error: 'Paste an ADR first.', focusText: true }); return; }
  if (utf8Bytes(text) > MAX_ADR_BYTES) { setSplit({ error: 'The document is over 64 KiB. Trim it and try again.', focusText: true }); return; }
  setSplit({ busy: true, error: '' });
  try {
    const out = await api('POST', '/api/ai/split', { text, max: splitState.max }, { timeout: 180000 });
    splitState.cards = (out.cards || []).map((c) => Object.assign({ include: true, created: null, error: '' }, c, { prio: c.prio || 3, effort: c.effort || '' }));
    splitState.commentary = out.commentary || '';
    splitState.partial = !!out.partial;
    if (!splitState.cards.length) { setSplit({ error: 'The model returned no usable stories.', busy: false }); return; }
    setSplit({ stage: 'review', busy: false });
    announce(`${splitState.cards.length} stories ready; review before creating`);
  } catch (err) {
    setSplit({ error: err.message, busy: false });
  }
}
export const selectedDrafts = (cards) => cards.filter((c) => c.include && c.title.trim() && !c.created);
export async function createSplitCards() {
  const queue = selectedDrafts(splitState.cards);
  if (!queue.length) return;
  setSplit({ busy: true, progress: { i: 0, n: queue.length } });
  const made = [];
  for (let i = 0; i < queue.length; i++) {
    const card = queue[i];
    setSplit({ progress: { i, n: queue.length } });
    try {
      const body = { title: card.title.trim(), status: splitState.status, prio: card.prio || 3, tags: normalizeTags(card.tags || []), checks: card.checks || [], project: writeProject() };
      for (const k of ['emoji', 'desc', 'due']) if (card[k]) body[k] = card[k];
      if (card.effort) body.effort = card.effort;
      card.created = await api('POST', '/api/tasks', body);
      card.error = '';
      made.push(card.created);
    } catch (err) { card.error = err.message; }
  }
  splitState.created = splitState.created.concat(made);
  const failed = queue.filter((c) => c.error).length;
  setSplit({ busy: false, progress: { i: queue.length, n: queue.length }, error: failed ? `created ${made.length}; ${failed} failed — fix the reported rows and retry` : '' });
  invalidate();
  if (!made.length) return;
  toast(`Created ${plural(made.length, 'card')} from the ADR`, 'ok', { life: 8000, action: { label: 'Undo', run: () => undoCreated(made) } });
  announce(`created ${made.length} cards`);
}
// Rolls a batch back the only way the store allows: cancel each card it just wrote.
export async function undoCreated(cards) {
  for (const t of cards) {
    try { await api('POST', taskPath(t.id) + '/cancel', { reason: 'undone right after import' }); } catch (err) { toast(err.message, 'error'); break; }
  }
  invalidate();
}
