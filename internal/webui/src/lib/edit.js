// The card editor: one form object in the store, read back as a task.
import { state, notify, DEFAULT_EFFORT } from '../store';
import { api, withForce, patchTask, invalidate } from './api';
import { toast, announce } from './toast';
import { openDialog, closeDialog, isDialogOpen, confirmDiscard } from './dialogs';
import { userTags, normalizeTags } from './labels';
import { projectList, writeProject } from './filters';
import { reloadDetail } from './detail';

export function parseChecks(text) {
  return text.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => (/^x\s+/i.test(l) ? { text: l.replace(/^x\s+/i, ''), done: true } : { text: l, done: false }));
}
export const serializeChecks = (checks) => (checks || []).map((c) => (c.done ? 'x ' : '') + c.text).join('\n');

export function openEdit(task, preset = {}) {
  const projects = projectList(task && task.project);
  const want = task ? task.project || writeProject() : writeProject();
  const form = {
    title: task ? task.title : preset.title || '',
    emoji: task ? task.emoji || '' : '',
    desc: task ? task.desc || '' : '',
    status: task ? task.status : preset.status || 'todo',
    prio: String(task ? task.prio || 3 : 3),
    due: task ? task.due || '' : '',
    effort: (task ? task.effort || '' : '') || DEFAULT_EFFORT,
    project: projects.includes(want) ? want : projects[0] || '',
    blocked: !!(task && task.blocked),
    tags: task ? userTags(task) : [],
    checks: task ? serializeChecks(task.checks) : '',
  };
  state.edit = {
    task: task || null, form, projects, snapshot: '', saving: false, similar: [],
    ai: { prompt: '', msg: '', tone: '', commentary: '', partial: false, busy: false },
    focusTitle: true,
  };
  state.edit.snapshot = JSON.stringify(readForm());
  openDialog('edit');
  notify();
  if (!task && preset.title) loadSimilar(preset.title.trim());
}
export function readForm() {
  const f = state.edit.form;
  return {
    title: f.title.trim(), emoji: f.emoji.trim(), desc: f.desc, status: f.status,
    prio: Number(f.prio), due: f.due, effort: f.effort || '', tags: f.tags,
    project: f.project, blocked: f.blocked, checks: parseChecks(f.checks),
  };
}
let similarTimer = 0;
export function setEditField(key, value) {
  if (!state.edit) return;
  state.edit.form[key] = value;
  notify();
  if (key === 'title') { clearTimeout(similarTimer); similarTimer = setTimeout(() => loadSimilar(value.trim()), 300); }
}
// The values the form still holds by default rather than by choice. A new card
// reports status, priority and effort; an existing card only an effort it
// never had, which saving will fill in.
export function editDefaults() {
  const form = readForm();
  if (state.edit.task) return !state.edit.task.effort && form.effort === DEFAULT_EFFORT ? [`effort ${DEFAULT_EFFORT}`] : [];
  const out = [];
  if (form.status === 'todo') out.push('status Todo');
  if (form.prio === 3) out.push('priority Low');
  if (form.effort === DEFAULT_EFFORT) out.push(`effort ${DEFAULT_EFFORT}`);
  return out;
}
export const editDirty = () => !!state.edit && isDialogOpen('edit') && JSON.stringify(readForm()) !== state.edit.snapshot;
export async function requestCloseEdit() {
  if (editDirty() && !(await confirmDiscard('The card has changes that have not been saved.'))) return;
  closeDialog('edit');
}
export async function saveEdit() {
  const edit = state.edit;
  const form = readForm();
  if (!form.title) { toast('Add a title before saving', 'error'); edit.focusTitle = true; notify(); return; }
  const assumed = editDefaults();
  edit.saving = true;
  notify();
  try {
    let saved;
    if (edit.task) {
      const prev = edit.task;
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
      if (!Object.keys(patch).length) { closeDialog('edit'); return; }
      saved = await patchTask(prev.id, patch);
    } else {
      const body = { title: form.title, status: form.status, prio: form.prio, blocked: form.blocked, tags: form.tags, checks: form.checks, project: form.project };
      for (const k of ['emoji', 'desc', 'due', 'effort']) if (form[k]) body[k] = form[k];
      saved = await withForce((force) => api('POST', '/api/tasks', force ? Object.assign({ force: true }, body) : body));
    }
    if (saved === undefined) return; // user declined the force
    const idx = state.tasks.findIndex((t) => t.id === saved.id);
    state.tasks = idx >= 0 ? state.tasks.map((t, i) => (i === idx ? saved : t)) : [...state.tasks, saved];
    edit.snapshot = JSON.stringify(readForm());
    closeDialog('edit');
    const done = edit.task ? `Saved #${saved.seq} ${saved.title}` : `Created #${saved.seq} ${saved.title}`;
    if (assumed.length) toast(`${done}. Assumed ${assumed.join(', ')}`, 'warn', { life: 6000 }); else toast(done, 'ok');
    reloadDetail();
    invalidate();
  } catch (err) {
    toast(err.message || 'Save failed', 'error');
  } finally {
    edit.saving = false;
    notify();
  }
}

/* ============================== AI in the card editor ============================== */
export function setAIPrompt(value) {
  state.edit.ai.prompt = value;
  notify();
}
export async function runAIDraft() {
  const edit = state.edit;
  const ai = edit.ai;
  const prompt = ai.prompt.trim();
  if (!prompt) { ai.msg = 'Say what the card should cover.'; ai.tone = 'error'; ai.focus = true; notify(); return; }
  const form = readForm();
  const body = { prompt };
  if (edit.task || form.title || form.desc) body.card = { title: form.title, desc: form.desc, prio: form.prio, due: form.due, effort: form.effort, tags: form.tags, checks: form.checks };
  ai.busy = true;
  ai.msg = '';
  ai.tone = '';
  ai.commentary = '';
  ai.partial = false;
  notify();
  try {
    const out = await api('POST', '/api/ai/draft', body, { timeout: 120000 });
    applyDraft(out.card || {});
    ai.msg = 'AI draft applied; review before saving';
    ai.tone = 'ok';
    ai.partial = !!out.partial;
    ai.commentary = out.commentary || '';
    announce('AI draft applied; review before saving');
  } catch (err) {
    if (err.status === 409 && err.body && err.body.aiUnconfigured) { state.ai = { configured: false }; }
    else { ai.msg = err.message; ai.tone = 'error'; }
  } finally { ai.busy = false; notify(); }
}
// Fills the open editor from a draft card. Nothing is written until the user saves.
export function applyDraft(card) {
  const f = state.edit.form;
  if (card.title) f.title = card.title;
  if (card.emoji) f.emoji = card.emoji;
  if (card.desc) f.desc = card.desc;
  if (card.prio) f.prio = String(card.prio);
  if (card.due) f.due = card.due;
  if (card.effort !== undefined) f.effort = card.effort || DEFAULT_EFFORT;
  if (card.tags && card.tags.length) f.tags = normalizeTags(card.tags);
  if (card.checks && card.checks.length) f.checks = serializeChecks(card.checks);
  notify();
  loadSimilar(f.title.trim());
}
// Possible duplicates while the title is being typed: the same store search the TUI runs.
export async function loadSimilar(q) {
  if (!isDialogOpen('edit') || !state.edit) return;
  const edit = state.edit;
  if (q.length < 3) { if (edit.similar.length) { edit.similar = []; notify(); } return; }
  let items = [];
  try { items = (await api('GET', `/api/similar?q=${encodeURIComponent(q)}&limit=10`)).items || []; } catch (err) { return; }
  if (!isDialogOpen('edit') || state.edit !== edit) return;
  // The search answers with card hits (via "card") and upstream import hits (via "import",
  // which carry a link instead of a task id). Resolve the second kind onto a card when one
  // carries the link, and never list the same card twice.
  const localTask = (id) => state.all.concat(state.tasks).find((t) => t.id === id);
  const seen = new Set(edit.task ? [edit.task.id] : []);
  const rows = [];
  for (const i of items) {
    const byLink = i.link ? state.tasks.find((t) => (t.tags || []).includes('link::' + i.link)) : null;
    const known = localTask(i.id) || byLink;
    const id = (known && known.id) || i.id;
    if (id && seen.has(id)) continue;
    if (id) seen.add(id);
    rows.push({ title: i.title, status: (known && known.status) || i.status || 'todo', seq: known && known.seq, id, link: i.via === 'import' ? i.link : '' });
  }
  edit.similar = rows;
  notify();
}
