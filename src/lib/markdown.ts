import type { Board, Check, Effort, Prio, Status, Task } from './model';
import { STATUS_LABEL, STATUSES, newTask } from './model';

const EMOJI_RE = /^\p{Extended_Pictographic}(?:️)?/u;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
// Description lines that would re-parse as checklist items get one escaping
// backslash on the wire (stripped by parse). The leading \* keeps
// already-escaped lines stable across repeated round trips.
const DESC_CHECKBOX_RE = /^\\*- \[[ xX]\] /;
// Title-line flag for a blocked task. Serialized only when blocked is true.
const BLOCKED_TOKEN = '%blocked';

export function serialize(board: Board): string {
  let out = `# ${board.title}\n`;
  for (const status of STATUSES) {
    const tasks = board.tasks.filter((x) => x.status === status);
    // Cancelled is a phase-3 addition: emitting its header only when it has
    // tasks keeps legacy three-section boards byte-identical on the wire.
    if (status === 'cancelled' && tasks.length === 0) continue;
    out += `\n## ${STATUS_LABEL[status]}\n\n`;
    for (const t of tasks) {
      out += `- [${status === 'done' ? 'x' : ' '}] ${titleLine(t)}\n`;
      for (const line of t.desc.split('\n')) {
        const trimmed = line.trim();
        if (trimmed)
          out += `  ${DESC_CHECKBOX_RE.test(trimmed) ? `\\${trimmed}` : trimmed}\n`;
      }
      for (const c of t.checks) {
        out += `  - [${c.done ? 'x' : ' '}] ${c.text}\n`;
      }
    }
  }
  return out;
}

/**
 * Render the single-line form of a task. Title words that parse() would lift
 * into metadata are backslash-escaped; whitespace runs collapse to single
 * spaces, matching what parse() does on read.
 */
export function titleLine(t: Task): string {
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

export function parse(input: string): Board {
  const board: Board = { title: 'Board', tasks: [] };
  let sawTitle = false;
  let status: Status | null = null;
  let current: Task | null = null;
  let headerIdx = -1;
  const descLines = new Map<string, string[]>();

  for (const raw of input.split('\n')) {
    const line = raw.replace(/\r$/, '');
    if (!sawTitle && line.startsWith('# ')) {
      board.title = line.slice(2).trim();
      sawTitle = true;
      continue;
    }
    if (line.startsWith('## ')) {
      headerIdx++;
      const label = line.slice(3).trim().toLowerCase();
      // Unknown headers fall back to their position, saturating at the last
      // column, so legacy three-section files keep their old mapping and a
      // fourth unknown section lands in cancelled.
      status =
        STATUSES.find((s) => STATUS_LABEL[s].toLowerCase() === label) ??
        STATUSES[Math.min(STATUSES.length - 1, headerIdx)];
      current = null;
      continue;
    }
    if (status === null || !line.trim()) continue;

    const indented = /^\s/.test(line);
    const check = stripCheckbox(line.trim());

    if (!indented && check) {
      current = parseTitleLine(check.rest, status, check.done);
      board.tasks.push(current);
      descLines.set(current.id, []);
      continue;
    }
    if (!current) continue;
    if (check && indented) {
      current.checks.push({ text: check.rest, done: check.done } satisfies Check);
    } else {
      const text = line.trim();
      descLines
        .get(current.id)!
        .push(
          text.startsWith('\\') && DESC_CHECKBOX_RE.test(text.slice(1))
            ? text.slice(1)
            : text,
        );
    }
  }
  for (const t of board.tasks) {
    t.desc = (descLines.get(t.id) ?? []).join('\n');
  }
  return board;
}

function stripCheckbox(s: string): { done: boolean; rest: string } | null {
  if (s.startsWith('- [ ] ')) return { done: false, rest: s.slice(6) };
  if (s.startsWith('- [x] ') || s.startsWith('- [X] '))
    return { done: true, rest: s.slice(6) };
  return null;
}

export function parseTitleLine(raw: string, status: Status, done: boolean): Task {
  let rest = raw.trim();
  let emoji = '';
  const em = rest.match(EMOJI_RE);
  if (em) {
    emoji = em[0];
    rest = rest.slice(em[0].length).trim();
  }
  const t = newTask({ title: '', status: done ? 'done' : status, emoji });
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
