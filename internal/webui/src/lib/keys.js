// Global keyboard handling, the hash route, project switching and the small
// toggles the header owns (search, filters sheet, display panel, project menu).
import { state, settings, saveSettings, notify, findTask, isOpen, STATUSES, STATUS_LABEL, ALL_PROJECTS } from '../store';
import { dur, isTyping, phoneMQ } from './format';
import { invalidate } from './api';
import { announce } from './toast';
import { openDialog, closeDialog, topDialog } from './dialogs';
import { quickById, toggleQuick, clearFilters, projectList, setFiltersOpen } from './filters';
import { focusedTask, moveFocus, toggleLift, moveLifted, dropLifted, cancelLift, selectColumn, clearSelection, toggleSelect, setFocus, columnTasks, boardLayout, shipTask, cancelTask, restoreTask, deleteTask, setPriority } from './board';
import { openDetail, closeDetail } from './detail';
import { openEdit } from './edit';
import { openSettings, saveSection } from './settings';
import { openSplit } from './split';
import { openImport } from './import';
import { openPalette, selectPalette, runPalette } from './palette';

/* ============================== routing ============================== */
export function applyRoute() {
  const m = /^#\/(t|p|f)\/(.+)$/.exec(decodeURIComponent(location.hash || ''));
  if (!m) { if (state.detail) closeDetail({ route: true }); return; }
  if (m[1] === 't') { openDetail(m[2].replace(/^#/, ''), { route: true }); return; }
  if (m[1] === 'p') { setProject(m[2]); return; }
  if (m[1] === 'f' && quickById(m[2])) { if (!state.quick.has(m[2])) toggleQuick(m[2]); }
}

/* ============================== projects ============================== */
// Switching is this browser's business alone: the selection is remembered with
// the other display settings and never told to the server, which keeps no
// active project. The "all" scope is a client-side view.
export function setProject(name) {
  if (!name || name === state.project) return;
  state.project = name;
  settings.project = name;
  saveSettings();
  state.selected.clear();
  notify();
  invalidate();
  announce(name === ALL_PROJECTS ? 'All projects' : `Project ${name}`);
}
export function cycleProject(dir) {
  const list = [ALL_PROJECTS, ...projectList()];
  if (list.length < 2) return;
  const i = list.indexOf(state.project);
  setProject(list[(i + dir + list.length) % list.length]);
}
export function openProjectMenu() {
  state.projectMenuOpen = true;
  notify();
}
export function setProjectMenuOpen(open) {
  if (state.projectMenuOpen === open) return;
  state.projectMenuOpen = open;
  notify();
}

/* ============================== header toggles ============================== */
let searchFocusSeq = 0;
// The search field lives in a centred dialog (Spotlight-style) on every device.
// Typing filters the board behind it live; Enter or Escape closes and the
// query stays applied, marked by a dot on the header icon.
export function openSearch() {
  state.searchFocus = ++searchFocusSeq;
  openDialog('search');
  notify();
}
export function focusSearch() { openSearch(); }
export function focusLabels() {
  if (phoneMQ.matches) { setFiltersOpen(true); return; }
  openPalette();
}
export function toggleDisplay(open = !state.displayOpen) {
  if (state.displayOpen === open) return;
  state.displayOpen = open;
  notify();
}
export function openHelp() { openDialog('help'); }

/* ============================== keyboard ============================== */
export function onKeydown(e) {
  if (e.defaultPrevented) return;
  const openDlg = topDialog(); // the topmost dialog owns the keyboard
  const mod = e.ctrlKey || e.metaKey;
  if (mod && e.key.toLowerCase() === 's' && openDlg === 'settings') {
    e.preventDefault();
    const section = document.activeElement && document.activeElement.closest ? document.activeElement.closest('[data-save]') : null;
    saveSection(section ? section.dataset.save : 'ai');
    return;
  }
  if (mod && e.key.toLowerCase() === 'k') { e.preventDefault(); if (openDlg === 'palette') closeDialog('palette'); else { if (openDlg) closeDialog(openDlg); openPalette(); } return; }
  if (openDlg === 'palette') {
    if (e.key === 'ArrowDown') { e.preventDefault(); selectPalette(Math.min(state.paletteItems.length - 1, state.paletteIndex + 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); selectPalette(Math.max(0, state.paletteIndex - 1)); }
    else if (e.key === 'Enter') { e.preventDefault(); runPalette(state.paletteIndex); }
    return;
  }
  if (e.key === 'Escape') {
    if (state.displayOpen) { e.preventDefault(); toggleDisplay(false); return; }
    if (openDlg) return; // the dialog's cancel handler closes it
    if (state.filtersOpen) { e.preventDefault(); setFiltersOpen(false); return; }
    if (state.lifted) { e.preventDefault(); cancelLift(); return; }
    if (isTyping(e.target)) { e.target.blur(); return; }
    const openEditor = document.querySelector('#detail .editor[data-busy][data-view="preview"] .editor-bar [aria-pressed="true"]');
    if (openEditor) { e.preventDefault(); openEditor.click(); return; }
    if (state.selected.size) { e.preventDefault(); clearSelection(); return; }
    if (state.detail) { e.preventDefault(); closeDetail(); return; }
    return;
  }
  if (state.displayOpen && e.target.closest && e.target.closest('[data-display-panel]')) return;
  const onCard = !!(e.target.closest && e.target.closest('.card') && !isTyping(e.target));
  if (mod && e.key.toLowerCase() === 'a' && onCard) { e.preventDefault(); selectColumn(); return; }
  // alt+1-4 is the TUI's "jump to column"; the bare digits stay priority on the board.
  if (e.altKey && !mod && /^[1-4]$/.test(e.key) && !openDlg) {
    const status = STATUSES[Number(e.key) - 1];
    const col = boardLayout().find((c) => c.status === status);
    if (col && !col.hidden) {
      e.preventDefault();
      const node = document.querySelector(`#board .col[data-status="${status}"]`);
      if (node) node.scrollIntoView({ behavior: dur(1) ? 'smooth' : 'auto', inline: 'start', block: 'nearest' });
      const first = columnTasks(status)[0];
      if (first) setFocus(first.id); else announce(`${STATUS_LABEL[status]} is empty`);
    }
    return;
  }
  if (isTyping(e.target) || e.altKey || mod) return;
  if (openDlg && openDlg !== 'detail') return;
  const detailTask = state.detailData ? state.detailData.task : null;
  const focused = focusedTask();
  const target = onCard || !detailTask ? focused : detailTask; // in the panel, actions apply to the shown task
  if (state.lifted) {
    const map = { j: [1, 0], k: [-1, 0], h: [0, -1], l: [0, 1], ArrowDown: [1, 0], ArrowUp: [-1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] };
    if (map[e.key]) { e.preventDefault(); moveLifted(...map[e.key]); return; }
    if (e.key === ' ') { e.preventDefault(); dropLifted(); return; }
    return;
  }
  switch (e.key) {
    case 'j': case 'ArrowDown': if (!openDlg) { e.preventDefault(); moveFocus(1, 0); } break;
    case 'k': case 'ArrowUp': if (!openDlg) { e.preventDefault(); moveFocus(-1, 0); } break;
    case 'h': case 'ArrowLeft': if (!openDlg) { e.preventDefault(); moveFocus(0, -1); } break;
    case 'l': case 'ArrowRight': if (!openDlg) { e.preventDefault(); moveFocus(0, 1); } break;
    case 'Tab': if (onCard) { e.preventDefault(); moveFocus(0, e.shiftKey ? -1 : 1); } break;
    case 'Enter': if (onCard && focused) { e.preventDefault(); openDetail(focused.seq); } break;
    case ' ': if (onCard) { e.preventDefault(); toggleLift(); } break;
    case 'n': if (!openDlg) { e.preventDefault(); openEdit(null); } break;
    case 'e': if (target) { e.preventDefault(); openEdit(target); } break;
    case 't': if (target && isOpen(target)) { e.preventDefault(); shipTask(target); } break;
    case 'x': if (target && target.status !== 'cancelled') { e.preventDefault(); cancelTask(target); } break;
    case 'r': if (target && target.status === 'cancelled') { e.preventDefault(); restoreTask(target); } break;
    case 'D': if (target) { e.preventDefault(); deleteTask(target); } break;
    case '1': case '2': case '3': if (target) { e.preventDefault(); setPriority(target, Number(e.key)); } break;
    case 'c': if (target) { e.preventDefault(); openDetail(target.seq, { focusComment: true }); } break;
    case 'v': if (focused) { e.preventDefault(); toggleSelect(focused.id); } break;
    case '/': if (!openDlg) { e.preventDefault(); focusSearch(); } break;
    case 'f': if (!openDlg) { e.preventDefault(); focusLabels(); } break;
    case 'X': if (!openDlg) { e.preventDefault(); clearFilters(); } break;
    case 'p': if (!openDlg) cycleProject(1); break;
    case 'P': if (!openDlg) cycleProject(-1); break;
    case 'd': if (!openDlg) { e.preventDefault(); toggleDisplay(); } break;
    case 's': if (!openDlg) { e.preventDefault(); openSettings(); } break;
    case 'a': if (!openDlg) { e.preventDefault(); openSplit(); } break;
    case 'i': if (!openDlg) { e.preventDefault(); openImport(); } break;
    case '?': e.preventDefault(); if (openDlg) closeDialog(openDlg); openDialog('help'); break;
    default: break;
  }
}
export { findTask };
