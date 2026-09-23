// Search, quick filters, label filters and the project list.
import { state, notify, isOpen, STATUSES, ALL_PROJECTS } from '../store';
import { dueDays } from './format';
import { splitLabel, shownTags, isProjectTag, isProvenanceTag } from './labels';
import { invalidate, serverFiltered } from './api';

export const QUICK = [
  { id: 'overdue', label: 'Overdue', test: (t) => isOpen(t) && dueDays(t.due) !== null && dueDays(t.due) < 0 },
  { id: 'week', label: 'This week', test: (t) => isOpen(t) && dueDays(t.due) !== null && dueDays(t.due) >= 0 && dueDays(t.due) <= 7 },
  { id: 'high', label: 'High', test: (t) => t.prio === 1 },
  { id: 'blocked', label: 'Blocked', test: (t) => !!t.blocked },
  { id: 'checklist', label: 'Checklist', test: (t) => (t.checks || []).some((c) => !c.done) },
  { id: 'nolabels', label: 'Unlabelled', test: (t) => shownTags(t).length === 0 },
];
export const quickById = (id) => QUICK.find((q) => q.id === id);
export const PRIO_WORDS = { high: 1, medium: 2, med: 2, low: 3, 1: 1, 2: 2, 3: 3 };

// "tag:web scope:type prio:high due:week is:blocked has:checklist effort:M #12 free words" -> {q, tokens}
export function parseSearch(text) {
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
export function tokenTest(tok) {
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
export function visibleTasks() {
  const tests = [...state.quick].map((id) => quickById(id) && quickById(id).test)
    .concat([...state.scopes].map((s) => hasScope(s.toLowerCase())))
    .concat(state.tokens.map(tokenTest)).filter(Boolean);
  return tests.length ? state.tasks.filter((t) => tests.every((fn) => fn(t))) : state.tasks;
}
export const anyFilter = () => !!(state.q || state.tags.size || state.scopes.size || state.quick.size || state.tokens.length);

// The text in the search field is the source of the free words and tokens.
export function applySearchText(text, push = true) {
  const { q, tokens } = parseSearch(text);
  const changed = q !== state.q;
  state.q = q;
  state.tokens = tokens;
  if (push) state.searchText = text;
  if (changed) invalidate();
  notify();
}
export function setSearchText(text) {
  state.searchText = text;
  notify();
}
export function removeToken(raw) {
  applySearchText(state.searchText.split(/\s+/).filter((w) => w && w !== raw).join(' '));
}
export function toggleQuick(id) {
  if (state.quick.has(id)) state.quick.delete(id); else state.quick.add(id);
  notify();
}
export function toggleTag(tag) {
  if (state.tags.has(tag)) state.tags.delete(tag); else state.tags.add(tag);
  notify();
  invalidate();
}
export function toggleScope(scope) {
  if (state.scopes.has(scope)) state.scopes.delete(scope); else state.scopes.add(scope);
  notify();
}
export function clearFilters() {
  state.tags.clear();
  state.scopes.clear();
  state.quick.clear();
  state.tokens = [];
  state.q = '';
  state.searchText = '';
  notify();
  invalidate();
}
// The active filter chips, in display order.
export function activeFilters() {
  const chips = [];
  if (state.q) chips.push({ kind: 'text', label: `“${state.q}”`, remove: () => applySearchText(state.tokens.map((t) => t.raw).join(' ')) });
  for (const tok of state.tokens) chips.push({ kind: 'token', label: tok.raw, remove: () => removeToken(tok.raw) });
  for (const tag of state.tags) chips.push({ kind: 'tag', tag, remove: () => toggleTag(tag) });
  for (const scope of state.scopes) chips.push({ kind: 'tag', tag: scope + '::*', remove: () => toggleScope(scope) });
  for (const id of state.quick) chips.push({ kind: 'quick', label: quickById(id).label, remove: () => toggleQuick(id) });
  return chips;
}
export function resultCount() {
  if (!state.loaded) return '';
  const shown = visibleTasks().length;
  const total = serverFiltered() && state.all.length ? state.all.length : (serverFiltered() ? null : state.tasks.length);
  return anyFilter() ? (total === null ? `${shown} shown` : `${shown} of ${total}`) : `${shown} task${shown === 1 ? '' : 's'}`;
}
// The label pool is scoped like the board: with one project active it holds
// only the labels that project's cards carry, so another project's vocabulary
// never leaks into the picker. All projects use the store's full list.
export function allLabels() {
  const labels = state.project === ALL_PROJECTS ? (state.meta.labels || []).filter((l) => !isProjectTag(l) && !isProvenanceTag(l)) : [];
  for (const tag of state.tags) if (!labels.includes(tag)) labels.push(tag);
  const pool = state.project === ALL_PROJECTS || !state.all.length ? state.tasks : state.all;
  for (const t of pool) for (const tag of shownTags(t)) if (!labels.includes(tag)) labels.push(tag);
  return labels.sort((a, b) => a.localeCompare(b));
}
export const SEARCH_SYNTAX = [['tag:web', 'Label'], ['prio:high', 'Priority'], ['due:week', 'Due'], ['is:blocked', 'Blocked'], ['has:checklist', 'Checklist'], ['effort:S', 'Effort'], ['#12', 'Number']];
export function insertSearchToken(token) {
  const cur = state.searchText.trim();
  if (cur.split(/\s+/).includes(token)) return;
  applySearchText((cur ? cur + ' ' : '') + token + ' ');
}
export function setFiltersOpen(open) {
  if (state.filtersOpen === open) return;
  state.filtersOpen = open;
  notify();
}

/* ============================== projects ============================== */
// Every project on the board, as /api/projects reports it; ALL is client-side.
export const projectNames = () => (state.projects.length ? state.projects.map((p) => p.name) : (state.meta.projects || []));
export const projectList = (extra) => Array.from(new Set([...projectNames(), state.project, extra].filter((p) => p && p !== ALL_PROJECTS)));
// Where a new card lands: the selected project, or — in the "all" scope, which
// is a view rather than a project — the first project on the board, inbox on
// an empty one. The server has no default of its own, so every create names one.
export const writeProject = () => (state.project && state.project !== ALL_PROJECTS ? state.project : projectNames()[0] || 'inbox');
export const projectCount = (name) => {
  const p = state.projects.find((x) => x.name === name);
  if (!p) return null;
  return STATUSES.reduce((n, s) => n + (p.counts[s] || 0), 0);
};
export const projectTotal = () => state.projects.reduce((n, p) => n + STATUSES.reduce((m, s) => m + (p.counts[s] || 0), 0), 0);
export function boardStats() {
  const all = state.all.length || !serverFiltered() ? state.all : state.tasks;
  return {
    open: all.filter(isOpen).length,
    week: all.filter(quickById('week').test).length,
    blocked: all.filter((t) => isOpen(t) && t.blocked).length,
  };
}
