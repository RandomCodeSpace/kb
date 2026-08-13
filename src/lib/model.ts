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

export const CURRENT_SEED_SCHEMA = 1;
export const SEED_ID_FORMAT_VERSION = 1;

const SEED_CATALOG_RELEASED_AT = '2026-08-01T00:00:00.000Z';
const SEED_SLOTS = [
  'drag-to-doing',
  'checklist-on-board',
  'add-own-task',
] as const;
type SeedSlot = typeof SEED_SLOTS[number];

/** Frame seed identity fields so namespace and slot boundaries are injective. */
function seedTaskID(namespace: string, slot: SeedSlot): string {
  const encodedNamespace = encodeURIComponent(namespace);
  const encodedSlot = encodeURIComponent(slot);
  return `seed.v${SEED_ID_FORMAT_VERSION}.ns.${encodedNamespace.length}:${encodedNamespace}` +
    `.slot.${encodedSlot.length}:${encodedSlot}`;
}

function seedTask(
  namespace: string,
  slot: SeedSlot,
  partial: Omit<Task, 'id' | 'createdAt' | 'movedAt'>,
): Task {
  return {
    id: seedTaskID(namespace, slot),
    createdAt: SEED_CATALOG_RELEASED_AT,
    movedAt: SEED_CATALOG_RELEASED_AT,
    ...partial,
  };
}

/**
 * Return the virtual starter catalog. Seed identity is deliberately independent
 * from content schema so unchanged slots keep their identity across rollbacks.
 */
export function seedBoard(
  namespace = 'default',
  schemaVersion = CURRENT_SEED_SCHEMA,
): Board {
  // The parameter establishes the content-version contract before a second
  // catalog exists. Identity must not include it.
  void schemaVersion;
  return {
    title: 'kb',
    tasks: [
      seedTask(namespace, SEED_SLOTS[0], {
        emoji: '👋', title: 'Drag me to Doing', prio: 2, status: 'todo',
        desc: 'Drag the ⠿ grip (or anywhere with a mouse) and drop.',
        blocked: false,
        tags: ['start::here'],
        checks: [],
      }),
      seedTask(namespace, SEED_SLOTS[1], {
        emoji: '✅', title: 'Tick my checklist on the board', prio: 3, status: 'todo',
        desc: 'Expand with the ▾ button. Finishing every item ships the card.',
        blocked: false,
        checks: [
          { text: 'expand the checklist', done: false },
          { text: 'tick this one', done: false },
          { text: 'and this one — watch the card ship', done: false },
        ],
        tags: ['type::demo'], effort: 'S',
      }),
      seedTask(namespace, SEED_SLOTS[2], {
        emoji: '➕', title: 'Add your own task', prio: 3, status: 'todo',
        desc: 'The + button in any column. Tags support scoped labels like env::prod.',
        blocked: false,
        tags: [],
        checks: [],
      }),
    ],
  };
}

/**
 * crypto.randomUUID is a secure-context API, so a board served over plain
 * HTTP on a LAN host does not have it. getRandomValues is available in every
 * context kb runs in; fall back to a spec-shaped v4 built from it.
 */
export function newId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6]! & 0x0f) | 0x40;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function newTask(partial: Partial<Task> & { title: string }): Task {
  const now = new Date().toISOString();
  return {
    id: newId(),
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
  if (task.blocked) parts.push('blocked');
  const p = progress(task);
  if (p) parts.push(`${p.done} of ${p.total} checklist items done`);
  // Said last so the state a keyboard move puts the card in is the freshest
  // thing heard when focus returns to it mid-move.
  if (lifted) parts.push('lifted');
  return parts.join(', ');
}
