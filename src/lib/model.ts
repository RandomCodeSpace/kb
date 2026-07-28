export type Status = 'todo' | 'doing' | 'done' | 'cancelled';
export type Effort = 'S' | 'M' | 'L';
export type Prio = 1 | 2 | 3 | 4;

export interface Check {
  text: string;
  done: boolean;
}

export interface Task {
  id: string;
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

export function newTask(partial: Partial<Task> & { title: string }): Task {
  const now = new Date().toISOString();
  return {
    id: crypto.randomUUID(),
    emoji: '',
    desc: '',
    status: 'todo',
    blocked: false,
    prio: 3,
    tags: [],
    checks: [],
    createdAt: now,
    movedAt: now,
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
