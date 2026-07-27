import type { Board, Check, Effort, Prio, Status, Task } from './model';
import { STATUS_LABEL, STATUSES, newTask } from './model';

const EMOJI_RE = /^\p{Extended_Pictographic}(?:️)?/u;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

export function serialize(board: Board): string {
  let out = `# ${board.title}\n`;
  for (const status of STATUSES) {
    out += `\n## ${STATUS_LABEL[status]}\n\n`;
    for (const t of board.tasks.filter((x) => x.status === status)) {
      out += `- [${status === 'done' ? 'x' : ' '}] ${titleLine(t)}\n`;
      for (const line of t.desc.split('\n')) {
        if (line.trim()) out += `  ${line.trim()}\n`;
      }
      for (const c of t.checks) {
        out += `  - [${c.done ? 'x' : ' '}] ${c.text}\n`;
      }
    }
  }
  return out;
}

export function titleLine(t: Task): string {
  let s = t.emoji ? `${t.emoji} ${t.title}` : t.title;
  if (t.prio !== 3) s += ` !${t.prio}`;
  if (t.due) s += ` @${t.due}`;
  if (t.effort) s += ` ~${t.effort}`;
  for (const tag of t.tags) s += ` #${tag}`;
  return s;
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
      status =
        STATUSES.find((s) => STATUS_LABEL[s].toLowerCase() === label) ??
        STATUSES[Math.min(2, headerIdx)];
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
      descLines.get(current.id)!.push(line.trim());
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
    if (/^![1-4]$/.test(tok)) t.prio = Number(tok.slice(1)) as Prio;
    else if (tok.startsWith('@') && DATE_RE.test(tok.slice(1))) t.due = tok.slice(1);
    else if (/^~[SML]$/.test(tok)) t.effort = tok.slice(1) as Effort;
    else if (tok.length > 1 && tok.startsWith('#')) t.tags.push(tok.slice(1));
    else words.push(tok);
  }
  t.title = words.join(' ');
  return t;
}
