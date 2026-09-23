// The command palette and the keyboard help: one registry from the TUI (/api/actions)
// plus the board's own commands.
import { state, settings, notify, applySettings, ALL_PROJECTS, SORTS, isOpen } from '../store';
import { fuzzy, sentence, MOD } from './format';
import { openDialog, closeDialog } from './dialogs';
import { QUICK, allLabels, projectNames, toggleTag, toggleQuick } from './filters';
import { focusedTask, toggleLift, shipTask, cancelTask, restoreTask, deleteTask } from './board';
import { openDetail } from './detail';
import { openEdit } from './edit';
import { openSettings } from './settings';
import { openSplit } from './split';
import { openImport } from './import';
import { setProject, focusSearch, focusLabels, toggleDisplay, openProjectMenu } from './keys';
import { clearFilters } from './filters';

export const ACTION_GROUP = { navigate: 'Navigate', act: 'Actions', dismiss: 'Session' };
// What the web does differently from the terminal. null hides a row that a browser tab cannot honour.
export const ACTION_WEB = { quit: null, jumpColumn: { hint: 'alt+1-4' } };
const actionTarget = () => focusedTask() || (state.detailData ? state.detailData.task : null);
export const ACTION_RUN = {
  openCard: () => { const t = actionTarget(); if (t) openDetail(t.seq); },
  liftCard: () => toggleLift(),
  filterText: () => focusSearch(),
  filterLabel: () => focusLabels(),
  filterClear: () => clearFilters(),
  switchProject: () => openProjectMenu(),
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

export function paletteCommands() {
  const items = [];
  const add = (group, label, run, kbd, keywords) => items.push({ group, label, run, kbd, keywords: keywords || '' });
  // The terminal's own registry first, so ctrl+k lists what the TUI lists.
  for (const a of listedActions()) {
    const web = ACTION_WEB[a.id] || {};
    add(ACTION_GROUP[a.group] || 'Action', sentence(web.name || a.name), ACTION_RUN[a.id], web.hint || a.hint, a.id);
  }
  add('View', 'Keyboard shortcuts', () => openDialog('help'), '?', 'help');
  add('View', 'Display options', () => toggleDisplay(true), 'd');
  add('View', `Density: ${settings.density === 'compact' ? 'comfortable' : 'compact'}`, () => { settings.density = settings.density === 'compact' ? 'comfortable' : 'compact'; applySettings(); }, '', 'compact comfortable');
  add('View', `${settings.showCancelled ? 'Hide' : 'Show'} cancelled column`, () => { settings.showCancelled = !settings.showCancelled; applySettings(); }, '', 'column');
  add('View', `${settings.hideEmpty ? 'Show' : 'Hide'} empty columns`, () => { settings.hideEmpty = !settings.hideEmpty; applySettings(); });
  if (state.project !== ALL_PROJECTS) add('Project', 'Switch to all projects', () => setProject(ALL_PROJECTS), '', 'project all');
  for (const p of projectNames()) if (p !== state.project) add('Project', `Switch to ${p}`, () => setProject(p), '', 'project');
  for (const l of allLabels()) add('Label', `${state.tags.has(l) ? 'Remove' : 'Filter'} label ${l}`, () => toggleTag(l), '', 'tag');
  for (const q of QUICK) add('Filter', `${state.quick.has(q.id) ? 'Remove filter' : 'Filter'}: ${q.label}`, () => toggleQuick(q.id));
  for (const [s, label] of SORTS) if (settings.sort !== s) add('Sort', `Sort columns: ${label.toLowerCase()}`, () => { settings.sort = s; applySettings(); });
  for (const t of state.tasks) add('Task', `#${t.seq} ${t.title}`, () => openDetail(t.seq), '', (t.tags || []).join(' '));
  return items;
}
export function paletteResults(query) {
  const all = paletteCommands();
  if (!query) return all.filter((i) => i.group !== 'Task').concat(all.filter((i) => i.group === 'Task').slice(0, 8)).map((i) => ({ item: i, positions: [] }));
  const numeric = /^#?(\d+)$/.exec(query);
  return all.map((item) => {
    const m = fuzzy(query, item.label) || (item.keywords && fuzzy(query, item.keywords) ? { score: 1, positions: [] } : null);
    if (numeric && item.group === 'Task' && item.label.startsWith('#' + numeric[1] + ' ')) return { item, positions: [], score: 1000 };
    return m ? { item, positions: m.positions, score: m.score + (item.group === 'Task' ? -5 : 0) } : null;
  }).filter(Boolean).sort((a, b) => b.score - a.score).slice(0, 14);
}
export function openPalette() {
  state.paletteQuery = '';
  state.paletteItems = paletteResults('');
  state.paletteIndex = 0;
  openDialog('palette');
  notify();
}
export function setPaletteQuery(text) {
  state.paletteQuery = text;
  state.paletteItems = paletteResults(text.trim());
  state.paletteIndex = 0;
  notify();
}
export function selectPalette(i) {
  if (i === state.paletteIndex || !state.paletteItems[i]) return;
  state.paletteIndex = i;
  notify();
}
export function runPalette(i) {
  const s = state.paletteItems[i];
  if (!s) return;
  closeDialog('palette');
  s.item.run();
}

/* ============================== keyboard help ============================== */
// "j/k" -> two caps; "ctrl+k" -> one reading "Ctrl K"; "? or esc" -> two; a lone "/" stays one.
export function keyCaps(hint) {
  return String(hint || '').split(/\s+or\s+|(?<=.)\/(?=.)/).map((s) => s.trim()).filter(Boolean).map((part) => {
    const bits = part.split('+');
    return bits.map((k) => (k.length > 1 ? sentence(k) : bits.length > 1 ? k.toUpperCase() : k)).join(' ');
  });
}
// [{title, rows: [{keys: string[], name, plain?}]}], two columns filled row by row.
export function helpSections() {
  const groups = new Map();
  for (const a of state.actions) {
    if (ACTION_WEB[a.id] === null) continue;
    const g = ACTION_GROUP[a.group] || 'Other';
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g).push(Object.assign({}, a, ACTION_WEB[a.id] || {}));
  }
  const named = (title) => (groups.has(title) ? { title, rows: groups.get(title).map((a) => ({ keys: keyCaps(a.hint), name: sentence(a.name) })) } : null);
  const out = [named('Navigate'), named('Actions')].filter(Boolean);
  out.push({ title: 'This board only', rows: [
    { keys: ['d'], name: 'Display options' },
    { keys: ['1', '2', '3'], name: 'Set priority' },
    { keys: ['c'], name: 'Comment on the card' },
    { keys: ['v'], name: 'Select card' },
    { keys: [MOD + ' A'], name: 'Select the whole column' },
    { keys: ['Shift click'], name: 'Extend the selection' },
    { keys: [MOD + ' click'], name: 'Toggle one card' },
    { keys: [], plain: 'Drag', name: 'Move cards; on touch, long-press to lift' },
    { keys: [MOD + ' B', MOD + ' I'], name: 'Editor: bold, italic' },
    { keys: [MOD + ' K', MOD + ' E'], name: 'Editor: link, inline code' },
    { keys: [MOD + ' Enter'], name: 'Editor: save' },
    { keys: [MOD + ' ⇧ Enter'], name: 'Editor: draft with AI' },
    { keys: ['Esc'], name: 'Close, cancel a lift, clear the selection' },
  ] });
  for (const [title, rows] of groups) {
    if (title === 'Navigate' || title === 'Actions') continue;
    out.push({ title, rows: rows.map((a) => ({ keys: keyCaps(a.hint), name: sentence(a.name) })) });
  }
  return out;
}
