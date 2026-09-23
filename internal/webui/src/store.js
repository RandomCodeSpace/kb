// One mutable state object shared by the logic modules, and one version
// counter that React subscribes to. Logic mutates `state` or `settings` and
// calls notify(); every subscribed view re-renders from the current values.
import { useSyncExternalStore } from 'react';

export const STATUSES = ['todo', 'doing', 'done', 'cancelled'];
export const STATUS_LABEL = { todo: 'Todo', doing: 'Doing', done: 'Done', cancelled: 'Cancelled' };
export const PRIO_LABEL = { 1: 'High', 2: 'Medium', 3: 'Low' };
export const EFFORTS = ['S', 'M', 'L'];
// Effort is mandatory. A card that names none takes DEFAULT_EFFORT, and every
// surface says so, so nobody mistakes an assumed value for a chosen one.
export const DEFAULT_EFFORT = 'S';
// The selected project is this browser's alone: the server keeps no active
// project, so every card created here names state.project in the request.
export const ALL_PROJECTS = '::all'; // client-side pseudo-project, like the TUI's "all" scope
// Column sorts. Position is the hand-dragged order; the rest derive from the
// card, newest first for the two time sorts.
export const SORTS = [['updated', 'Recently updated'], ['created', 'Recently created'], ['position', 'Position'], ['prio', 'Priority'], ['due', 'Due date']];

export const state = {
  meta: { version: '', projects: [], labels: [] },
  project: '', q: '', searchText: '', tokens: [], tags: new Set(), scopes: new Set(), quick: new Set(),
  tasks: [], all: [], loaded: false, etag: null, etagURL: '', online: true, firstPaint: true,
  dragging: null, dropTarget: null, touch: null, dragPos: null, autoScrollRAF: 0, dropped: null, pendingMoves: 0,
  focusId: null, focusRequest: 0, lifted: null, liftOrigin: null, selected: new Set(), anchorId: null,
  detail: null, detailJSON: '', detailData: null, commentCounts: {},
  edit: null, paletteQuery: '', paletteIndex: 0, paletteItems: [],
  mode: 'stream', stream: null, streamFails: 0, helloSeen: false, refreshTimer: 0, pollTimer: 0,
  actions: [], projects: [], ai: null, drift: {},
  dialogs: [], toasts: [], ask: null, displayOpen: false, filtersOpen: false, composer: null,
  shipToken: 0, cardEpoch: 0,
};

/* ============================== settings ============================== */
export const SETTINGS_KEY = 'kb-web-settings';
function defaultSettings() {
  return {
    density: 'comfortable',
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
export const settings = loadSettings();
export function saveSettings() {
  try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)); } catch (e) { /* ignore */ }
}
export function applySettings() {
  const root = document.documentElement;
  if (settings.density === 'compact') root.dataset.density = 'compact'; else delete root.dataset.density;
  state.cardEpoch++;
  saveSettings();
  notify();
}

/* ============================== subscription ============================== */
let version = 0;
const listeners = new Set();
function subscribe(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
const snapshot = () => version;
export function notify() {
  version++;
  for (const listener of listeners) listener();
}
// Subscribes the calling component to every store change.
export function useStore() {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
export const findTask = (id) => state.tasks.find((t) => t.id === id || String(t.seq) === String(id));
export const isOpen = (t) => t.status === 'todo' || t.status === 'doing';
