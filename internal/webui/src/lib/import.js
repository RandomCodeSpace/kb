// Import forge issues: pick a source and reference, preview drafts, import the ticked ones.
import { state, notify } from '../store';
import { api, taskPath, invalidate } from './api';
import { toast, announce } from './toast';
import { openDialog, closeDialog, confirmDiscard } from './dialogs';
import { plural } from './format';
import { writeProject } from './filters';
import { undoCreated, selectedDrafts } from './split';

export const importState = { stage: 'input', sources: null, source: '', ref: '', max: 8, preview: null, drafts: [], error: '', created: [], done: false, busy: false, progress: null, focusRef: false };
export async function openImport() {
  Object.assign(importState, { stage: 'input', preview: null, drafts: [], error: '', created: [], done: false, busy: false, progress: null, focusRef: false });
  openDialog('import');
  notify();
  if (importState.sources === null) {
    try { importState.sources = (await api('GET', '/api/settings/forge')).sources || []; } catch (err) { importState.sources = []; importState.error = err.message; }
    if (!importState.source && importState.sources.length) importState.source = importState.sources[0].name;
    notify();
  }
}
export async function closeImport() {
  if (importState.stage === 'review' && !importState.done && importState.drafts.some((d) => d.include) && !(await confirmDiscard('The fetched issues have not been imported yet.', { title: 'Discard the fetched issues?', cancel: 'Keep reviewing' }))) return;
  closeDialog('import');
}
export function setImport(patch) { Object.assign(importState, patch); notify(); }
export async function runPreview() {
  const ref = importState.ref.trim();
  if (!ref) { setImport({ error: 'reference required', focusRef: true }); return; }
  setImport({ busy: true, error: '' });
  try {
    const out = await api('POST', '/api/forge/preview', { source: importState.source, ref, max: importState.max }, { timeout: 180000 });
    importState.preview = out;
    importState.drafts = (out.drafts || []).map((d) => Object.assign({ include: !(d.duplicate && d.duplicate.via === 'link'), created: null, error: '' }, d, { prio: d.prio || 3, effort: d.effort || '' }));
    if (!importState.drafts.length) { setImport({ error: 'no issues fetched', busy: false }); return; }
    await resolveDuplicates(importState.drafts);
    setImport({ stage: 'review', busy: false });
    announce('review proposals; exact duplicates start unticked');
  } catch (err) { setImport({ error: err.message, busy: false }); }
}
// A duplicate marker names a card id; the reviewer needs its number. Cards outside the
// current project are not in memory, so look those up once per preview.
export const localTask = (id) => state.all.concat(state.tasks).find((t) => t.id === id);
async function resolveDuplicates(drafts) {
  const wanted = [...new Set(drafts.map((d) => d.duplicate && d.duplicate.id).filter((id) => id && !localTask(id)))];
  await Promise.all(wanted.map(async (id) => {
    try {
      const seq = (await api('GET', taskPath(id))).task.seq;
      for (const d of drafts) if (d.duplicate && d.duplicate.id === id) d.duplicate.seq = seq;
    } catch (err) { /* the badge falls back to the title */ }
  }));
}
export async function runImport() {
  const queue = selectedDrafts(importState.drafts);
  if (!queue.length) return;
  setImport({ busy: true, progress: { i: 0, n: queue.length } });
  const made = [];
  for (let i = 0; i < queue.length; i++) {
    const d = queue[i];
    setImport({ progress: { i, n: queue.length } });
    const task = { title: d.title.trim(), prio: d.prio || 3, tags: d.tags || [], checks: d.checks || [], project: writeProject() };
    for (const k of ['emoji', 'desc', 'due']) if (d[k]) task[k] = d[k];
    if (d.effort) task.effort = d.effort;
    try {
      d.created = await api('POST', '/api/forge/import', { source: d.source || importState.source, task, link: { externalKey: d.externalKey, link: d.link, url: d.url, title: d.title, baseline: d.baseline } }, { timeout: 120000 });
      d.error = '';
      made.push(d.created);
    } catch (err) { d.error = err.message; }
  }
  importState.created = importState.created.concat(made);
  importState.done = importState.drafts.every((d) => d.created || !d.include);
  const failed = queue.filter((d) => d.error).length;
  setImport({ busy: false, progress: { i: queue.length, n: queue.length }, error: failed ? `imported ${made.length}; ${failed} failed — fix the reported rows and retry` : '' });
  invalidate();
  if (!made.length) return;
  toast(`Imported ${plural(made.length, 'card')} from ${importState.source}`, 'ok', { life: 8000, action: { label: 'Undo', run: () => undoCreated(made) } });
  announce('import complete');
}
