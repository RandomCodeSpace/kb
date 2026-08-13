import type { Task } from '../lib/model';
import { newDraft } from '../lib/model';

let seq = 0;

/**
 * A Task with the fields the server assigns already filled in. Production code
 * never builds one of these — it only ever reads them back from the API — so
 * the factory lives here rather than in the model.
 */
export function makeTask(partial: Partial<Task> & { title: string }): Task {
  seq += 1;
  return {
    ...newDraft(partial),
    id: `test-${seq}`,
    createdAt: '2026-08-01T00:00:00.000Z',
    movedAt: '2026-08-01T00:00:00.000Z',
    ...partial,
  };
}
