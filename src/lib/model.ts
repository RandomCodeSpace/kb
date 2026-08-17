export type Status = 'todo' | 'doing' | 'done' | 'cancelled';
export type Effort = 'S' | 'M' | 'L';
export type Prio = 1 | 2 | 3 | 4;

export interface Check {
  text: string;
  done: boolean;
}

export interface Task {
  id: string;
  seq?: number; // server-assigned human-facing number (#12); absent on older data
  emoji: string;
  title: string;
  desc: string;
  status: Status;
  blocked: boolean;
  prio: Prio;
  due?: string; // YYYY-MM-DD
  effort?: Effort;
  tags: string[]; // plain ("backend") or scoped ("type::bug")
  checks: Check[];
  createdAt: string; // ISO
  movedAt: string; // ISO
}

export interface Board {
  title: string;
  tasks: Task[];
}

export const STATUSES: Status[] = ['todo', 'doing', 'done', 'cancelled'];
export const STATUS_LABEL: Record<Status, string> = {
  todo: 'To Do',
  doing: 'Doing',
  done: 'Done',
  cancelled: 'Cancelled',
};

/**
 * A task the server has not created yet. Ids and timestamps are the server's
 * to assign — the SPA never mints one — so everything that proposes a new card
 * (the editor, the ADR split, an issue import, a markdown file) speaks this
 * shape until POST /api/tasks answers with the real task.
 */
export type TaskDraft = Omit<Task, 'id' | 'seq' | 'createdAt' | 'movedAt'>;

export function newDraft(partial: Partial<TaskDraft> & { title: string }): TaskDraft {
  return {
    emoji: '',
    desc: '',
    status: 'todo',
    blocked: false,
    prio: 3,
    tags: [],
    checks: [],
    ...partial,
  };
}

export function progress(t: Task): { done: number; total: number } | null {
  if (t.checks.length === 0) return null;
  return { done: t.checks.filter((c) => c.done).length, total: t.checks.length };
}

export function isScoped(tag: string): boolean {
  return tag.includes('::');
}

/**
 * What a card announces to a screen reader: what it is, which column it is in,
 * where it sits in that column, and the state a sighted user reads off the
 * chips (blocked, checklist progress). `index` is 0-based; the name says
 * "2 of 4" because that is how a person counts.
 */
export function cardLabel(
  task: Task,
  index: number,
  total: number,
  lifted = false,
): string {
  const parts = [task.title, STATUS_LABEL[task.status], `${index + 1} of ${total}`];
  if (task.seq !== undefined) parts.splice(1, 0, `number ${task.seq}`);
  if (task.blocked) parts.push('blocked');
  const p = progress(task);
  if (p) parts.push(`${p.done} of ${p.total} checklist items done`);
  // Said last so the state a keyboard move puts the card in is the freshest
  // thing heard when focus returns to it mid-move.
  if (lifted) parts.push('lifted');
  return parts.join(', ');
}
