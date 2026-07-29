import { describe, expect, it } from 'vitest';
import type { Board, Status } from './model';
import { newTask } from './model';
import { acknowledgedTombstones } from './graveyard';

function oneTask(status: Status): Board {
  return {
    title: 'kb',
    tasks: [
      {
        ...newTask({ title: 'Add SSO login' }),
        id: 'client-1',
        status,
      },
    ],
  };
}

describe('acknowledgedTombstones', () => {
  const pending = new Map([['client-1', 'superseded by SAML']]);
  const taskIDs = new Map([['client-1', 'server-1']]);

  it('waits through an older acknowledgement made before cancellation', () => {
    expect(
      acknowledgedTombstones(pending, oneTask('todo'), taskIDs),
    ).toEqual([]);
  });

  it('pairs a cancelled client card with its acknowledged server id', () => {
    expect(
      acknowledgedTombstones(pending, oneTask('cancelled'), taskIDs),
    ).toEqual([
      {
        clientTaskId: 'client-1',
        serverTaskId: 'server-1',
        reason: 'superseded by SAML',
      },
    ]);
  });

  it('keeps waiting when the acknowledgement has no identity mapping', () => {
    expect(
      acknowledgedTombstones(
        pending,
        oneTask('cancelled'),
        new Map(),
      ),
    ).toEqual([]);
  });

  it('does not revive a queued reason after its card was purged', () => {
    expect(
      acknowledgedTombstones(
        pending,
        { title: 'kb', tasks: [] },
        taskIDs,
      ),
    ).toEqual([]);
  });
});
