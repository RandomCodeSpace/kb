// Settings dialog state: the AI endpoint and the forge integrations.
// Mirrors internal/tui/settings_view.go: the AI row first, then one block per configured integration.
// Secrets are write-only everywhere: a read reports only whether one is stored.
import { state, notify } from '../store';
import { api, refreshAIStatus } from './api';
import { toast } from './toast';
import { openDialog, closeDialog, isDialogOpen, confirmDiscard } from './dialogs';
import { importState, openImport } from './import';

export const sset = { loaded: false, loading: false, loadError: '', ai: null, rows: [], draft: null, snapshot: '', focusKey: '', section: '' };
export const FORGE_KINDS = [['gitlab', 'GitLab'], ['github', 'GitHub']];
const newForgeDraft = () => ({ name: '', kind: 'gitlab', baseURL: '', project: '', token: '', msg: '', tone: '', draft: true, armed: false, busy: '' });
const ssetState = () => JSON.stringify([sset.ai && [sset.ai.baseURL, sset.ai.model, sset.ai.key, sset.ai.clearKey],
  sset.rows.map((r) => [r.name, r.kind, r.baseURL, r.project, r.token, r.clearToken]),
  sset.draft && [sset.draft.name, sset.draft.kind, sset.draft.baseURL, sset.draft.project, sset.draft.token]]);
const forgeRowOf = (s) => ({ name: s.name, kind: s.kind, baseURL: s.baseURL || '', project: '', token: '', hasToken: !!s.hasToken, createdAt: s.createdAt, clearToken: false, editToken: false, msg: '', tone: '', armed: false, busy: '' });

export async function openSettings(section) {
  sset.section = section || '';
  openDialog('settings');
  if (!sset.loaded) {
    sset.loadError = '';
    sset.loading = true;
    notify();
    try {
      const [ai, forge] = await Promise.all([api('GET', '/api/settings/ai'), api('GET', '/api/settings/forge')]);
      sset.ai = { baseURL: ai.ai.baseURL || '', model: ai.ai.model || '', key: '', hasKey: !!ai.ai.hasKey, clearKey: false, editKey: false, msg: '', tone: '', busy: '' };
      sset.rows = (forge.sources || []).map(forgeRowOf);
      sset.draft = null;
      sset.loaded = true;
    } catch (err) {
      sset.loadError = err.message;
      sset.snapshot = ssetState(); // nothing was loaded, so nothing is dirty
      sset.loading = false;
      notify();
      return;
    }
    sset.loading = false;
  }
  sset.snapshot = ssetState();
  sset.focusKey = section === 'forge' ? 'forge-first' : 'ai-base';
  notify();
}
export async function closeSettings() {
  if (ssetState() !== sset.snapshot && !(await confirmDiscard('Settings you changed here have not been saved.', { title: 'Discard unsaved settings?' }))) return;
  sset.loaded = false;
  closeDialog('settings');
  // A source may have been added while the import wizard sat behind this dialog.
  if (isDialogOpen('import')) { importState.sources = null; openImport(); }
}
export const settingsDirty = () => isDialogOpen('settings') && ssetState() !== sset.snapshot;
export function refreshSettingsView() { notify(); }
// Returns whether anything was actually disarmed, so the caller only repaints when it matters.
function disarmForge(except) {
  let changed = false;
  for (const r of sset.rows) if (r !== except && r.armed) { r.armed = false; r.msg = ''; changed = true; }
  return changed;
}
export function setAIField(key, value) { sset.ai[key] = value; notify(); }
export function setForgeField(row, key, value) { row[key] = value; disarmForge(); notify(); }
export function editAIKey() { sset.ai.editKey = true; sset.focusKey = 'ai-key'; notify(); }
export function clearAIKey() { sset.ai.clearKey = true; sset.ai.key = ''; sset.ai.msg = 'The saved key is removed when you save.'; sset.ai.tone = 'warn'; notify(); }
export function keepAIKey() { sset.ai.editKey = false; sset.ai.clearKey = false; sset.ai.key = ''; sset.ai.msg = ''; sset.ai.tone = ''; notify(); }
export async function testAI() {
  const ai = sset.ai;
  ai.busy = 'test';
  ai.msg = '';
  ai.tone = '';
  notify();
  try {
    await api('POST', '/api/settings/ai/test', { baseURL: ai.baseURL.trim(), model: ai.model.trim(), apiKey: ai.key }, { timeout: 20000 });
    ai.msg = 'connection ok';
    ai.tone = 'ok';
  } catch (err) { ai.msg = err.message; ai.tone = 'error'; } finally { ai.busy = ''; sset.focusKey = 'ai-test'; notify(); }
}
export async function saveAI() {
  const ai = sset.ai;
  ai.busy = 'save';
  ai.msg = '';
  ai.tone = '';
  notify();
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
  } catch (err) { ai.msg = err.message; ai.tone = 'error'; } finally { ai.busy = ''; sset.focusKey = 'ai-save'; notify(); }
}
export function addForge() {
  disarmForge();
  sset.draft = newForgeDraft();
  sset.focusKey = 'forge-new-name';
  notify();
}
export function setForgeKind(row, kind) { row.kind = kind; notify(); }
export function editForgeToken(row) { disarmForge(); row.editToken = true; sset.focusKey = forgeKey(row, 'token'); notify(); }
export function clearForgeToken(row) { disarmForge(); row.clearToken = true; row.token = ''; row.msg = 'The saved token is removed when you save.'; row.tone = 'warn'; notify(); }
export function keepForgeToken(row) { row.editToken = false; row.clearToken = false; row.token = ''; row.msg = ''; row.tone = ''; notify(); }
export const forgeKey = (row, name) => (row.draft ? 'forge-new-' : `forge-${row.name}-`) + name;
export async function testForge(row) {
  row.busy = 'test';
  row.msg = '';
  row.tone = '';
  notify();
  try {
    await api('POST', `/api/settings/forge/${encodeURIComponent(row.name.trim().toLowerCase())}/test`,
      { kind: row.kind, baseURL: row.baseURL.trim(), project: row.project.trim(), token: row.token, saved: !row.draft }, { timeout: 30000 });
    row.msg = 'connection ok';
    row.tone = 'ok';
  } catch (err) { row.msg = err.message; row.tone = 'error'; } finally { row.busy = ''; sset.focusKey = forgeKey(row, 'test'); notify(); }
}
export async function saveForge(row) {
  const fail = (msg, field) => { row.msg = msg; row.tone = 'error'; sset.focusKey = forgeKey(row, field); notify(); };
  const name = row.name.trim().toLowerCase();
  if (!name) return fail('integration name is required', 'name');
  if (!/^[a-z0-9._-]{1,64}$/.test(name)) return fail('name may use letters, digits, dot, dash and underscore only', 'name');
  if (row.draft && sset.rows.some((r) => r.name.toLowerCase() === name)) return fail('integration name already exists', 'name');
  if (!row.baseURL.trim()) return fail('forge base URL is required', 'baseURL');
  row.busy = 'save';
  row.msg = '';
  row.tone = '';
  notify();
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
    sset.focusKey = forgeKey(saved, 'save');
  } catch (err) { row.msg = err.message; row.tone = 'error'; row.busy = ''; sset.focusKey = forgeKey(row, 'save'); }
  notify();
}
export async function removeForge(row) {
  if (row.draft) { sset.draft = null; notify(); return; }
  if (!row.armed) { disarmForge(row); row.armed = true; row.msg = `click Confirm remove to remove ${row.name}`; row.tone = 'warn'; notify(); return; }
  try {
    await api('DELETE', `/api/settings/forge/${encodeURIComponent(row.name)}`);
    sset.rows = sset.rows.filter((r) => r !== row);
    sset.snapshot = ssetState();
    notify();
    toast(`Removed the ${row.name} integration`, 'ok');
  } catch (err) { row.armed = false; row.msg = err.message; row.tone = 'error'; notify(); }
}
// Ctrl+S saves the section the focus is in: 'ai', 'forge:new' or 'forge:<name>'.
export function saveSection(key) {
  if (key === 'forge:new' && sset.draft) return saveForge(sset.draft);
  if (key && key.startsWith('forge:')) { const row = sset.rows.find((r) => 'forge:' + r.name === key); if (row) return saveForge(row); }
  if (sset.ai) return saveAI();
  return undefined;
}
export const isAIConfigured = () => !!(state.ai && state.ai.configured);
