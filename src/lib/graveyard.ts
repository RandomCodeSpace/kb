import type { Board } from './model';

export interface AcknowledgedTombstone {
  clientTaskId: string;
  serverTaskId: string;
  reason: string;
}

/**
 * A reason may leave the browser only after the cancelled board write tells
 * us which SQLite identity belongs to the browser-only card id. An older save
 * can settle after the kill was queued, so its still-live task is not ready.
 */
export function acknowledgedTombstones(
  pending: ReadonlyMap<string, string>,
  pushed: Board,
  taskIDs: ReadonlyMap<string, string>,
): AcknowledgedTombstone[] {
  const tasks = new Map(pushed.tasks.map((task) => [task.id, task]));
  const ready: AcknowledgedTombstone[] = [];
  for (const [clientTaskId, reason] of pending) {
    if (tasks.get(clientTaskId)?.status !== 'cancelled') continue;
    const serverTaskId = taskIDs.get(clientTaskId);
    if (!serverTaskId) continue;
    ready.push({ clientTaskId, serverTaskId, reason });
  }
  return ready;
}
