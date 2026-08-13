import type { Check, Effort, Prio, Status, TaskDraft } from './model';
import { STATUS_LABEL, STATUSES, newDraft } from './model';

/**
 * A board as the markdown codec sees it: a title and tasks with no ids or
 * timestamps. Serializing accepts real Tasks too — they carry every field the
 * wire has a place for, and the rest is server bookkeeping the file omits.
 */
export interface DraftBoard {
  title: string;
  tasks: TaskDraft[];
}

const EMOJI_RE = /^\p{Extended_Pictographic}(?:️)?/u;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
// Description lines that would re-parse as checklist items get one escaping
// backslash on the wire (stripped by parse). The leading \* keeps
// already-escaped lines stable across repeated round trips.
const DESC_CHECKBOX_RE = /^\\*- \[[ xX]\] /;
// Title-line flag for a blocked task. Serialized only when blocked is true.
const BLOCKED_TOKEN = '%blocked';

/**
 * Tasks in the order serialize() writes them and the Go parser receives them:
 * canonical column order, preserving board order within each column.
 */
export function wireTasks(
  board: Readonly<{ tasks: readonly TaskDraft[] }>,
): TaskDraft[] {
  return STATUSES.flatMap((status) =>
    board.tasks.filter((task) => task.status === status),
  );
}

function checkboxMark(done: boolean): string {
  return done ? 'x' : ' ';
}

function serializeTask(task: TaskDraft, status: Status): string {
  let out = `- [${checkboxMark(status === 'done')}] ${titleLine(task)}\n`;
  for (const line of task.desc.split('\n')) {
    const trimmed = line.trim();
    if (trimmed === '') continue;
    const description = DESC_CHECKBOX_RE.test(trimmed) ? `\\${trimmed}` : trimmed;
    out += `  ${description}\n`;
  }
  for (const check of task.checks) {
    out += `  - [${checkboxMark(check.done)}] ${check.text}\n`;
  }
  return out;
}

export function serialize(
  board: Readonly<{ title: string; tasks: readonly TaskDraft[] }>,
): string {
  let out = `# ${board.title}\n`;
  const ordered = wireTasks(board);
  for (const status of STATUSES) {
    const tasks = ordered.filter((task) => task.status === status);
    // Cancelled is a phase-3 addition: emitting its header only when it has
    // tasks keeps legacy three-section boards byte-identical on the wire.
    if (status === 'cancelled' && tasks.length === 0) continue;
    out += `\n## ${STATUS_LABEL[status]}\n\n`;
    for (const task of tasks) out += serializeTask(task, status);
  }
  return out;
}

/**
 * Render the single-line form of a task. Title words that parse() would lift
 * into metadata are backslash-escaped; whitespace runs collapse to single
 * spaces, matching what parse() does on read.
 */
export function titleLine(t: TaskDraft): string {
  const title = t.title.split(/\s+/).filter(Boolean).map(escapeToken).join(' ');
  let s = t.emoji ? `${t.emoji} ${title}` : title;
  if (t.prio !== 3) s += ` !${t.prio}`;
  if (t.due) s += ` @${t.due}`;
  if (t.effort) s += ` ~${t.effort}`;
  if (t.blocked) s += ` ${BLOCKED_TOKEN}`;
  for (const tag of t.tags) s += ` #${tag}`;
  return s;
}

/**
 * Escape a title word that parse() would otherwise consume as metadata
 * (!prio, @due, ~effort, #tag, %blocked) — or that starts with the escape
 * character itself — by prefixing one backslash, which parse() strips.
 */
function escapeToken(tok: string): string {
  const needsEscape =
    tok.startsWith('\\') ||
    tok === BLOCKED_TOKEN ||
    /^![1-4]$/.test(tok) ||
    /^~[SML]$/.test(tok) ||
    (tok.startsWith('@') && DATE_RE.test(tok.slice(1))) ||
    (tok.length > 1 && tok.startsWith('#'));
  return needsEscape ? `\\${tok}` : tok;
}

function headerStatus(line: string, headerIdx: number): Status {
  const label = line.slice(3).trim().toLowerCase();
  // Unknown headers fall back to their position, saturating at the last
  // column, so legacy three-section files keep their old mapping and a
  // fourth unknown section lands in cancelled.
  return (
    STATUSES.find((candidate) => STATUS_LABEL[candidate].toLowerCase() === label) ??
    STATUSES[Math.min(STATUSES.length - 1, headerIdx)]
  );
}

function descriptionText(line: string): string {
  const text = line.trim();
  if (text.startsWith('\\') && DESC_CHECKBOX_RE.test(text.slice(1))) {
    return text.slice(1);
  }
  return text;
}

function appendIndentedLine(
  current: TaskDraft,
  line: string,
  check: { done: boolean; rest: string } | null,
  descLines: Map<TaskDraft, string[]>,
): void {
  if (check) {
    current.checks.push({ text: check.rest, done: check.done } satisfies Check);
    return;
  }
  descLines.get(current)!.push(descriptionText(line));
}

export function parse(input: string): DraftBoard {
  const board: DraftBoard = { title: 'Board', tasks: [] };
  let sawTitle = false;
  let status: Status | null = null;
  let current: TaskDraft | null = null;
  let headerIdx = -1;
  // Keyed by the draft itself: parsed tasks have no id to key on, and none is
  // minted here — the server assigns one if the draft is ever created.
  const descLines = new Map<TaskDraft, string[]>();

  for (const raw of input.split('\n')) {
    const line = raw.replace(/\r$/, '');
    if (!sawTitle && line.startsWith('# ')) {
      board.title = line.slice(2).trim();
      sawTitle = true;
      continue;
    }
    if (line.startsWith('## ')) {
      headerIdx++;
      status = headerStatus(line, headerIdx);
      current = null;
      continue;
    }
    if (status === null || !line.trim()) continue;

    const indented = /^\s/.test(line);
    const check = stripCheckbox(line.trim());

    if (!indented && check) {
      current = parseTitleLine(check.rest, status, check.done);
      board.tasks.push(current);
      descLines.set(current, []);
      continue;
    }
    if (!current) continue;
    appendIndentedLine(current, line, check, descLines);
  }
  for (const t of board.tasks) {
    t.desc = (descLines.get(t) ?? []).join('\n');
  }
  return board;
}

function stripCheckbox(s: string): { done: boolean; rest: string } | null {
  if (s.startsWith('- [ ] ')) return { done: false, rest: s.slice(6) };
  if (s.startsWith('- [x] ') || s.startsWith('- [X] '))
    return { done: true, rest: s.slice(6) };
  return null;
}

export function parseTitleLine(
  raw: string,
  status: Status,
  done: boolean,
): TaskDraft {
  let rest = raw.trim();
  let emoji = '';
  const em = EMOJI_RE.exec(rest);
  if (em) {
    emoji = em[0];
    rest = rest.slice(em[0].length).trim();
  }
  const t = newDraft({ title: '', status: done ? 'done' : status, emoji });
  const words: string[] = [];
  for (const tok of rest.split(/\s+/)) {
    // Escaped word: strip one backslash, keep it as title text.
    if (tok.startsWith('\\')) words.push(tok.slice(1));
    else if (/^![1-4]$/.test(tok)) t.prio = Number(tok.slice(1)) as Prio;
    else if (tok.startsWith('@') && DATE_RE.test(tok.slice(1))) t.due = tok.slice(1);
    else if (/^~[SML]$/.test(tok)) t.effort = tok.slice(1) as Effort;
    else if (tok === BLOCKED_TOKEN) t.blocked = true;
    else if (tok.length > 1 && tok.startsWith('#')) t.tags.push(tok.slice(1));
    else words.push(tok);
  }
  t.title = words.join(' ');
  return t;
}
