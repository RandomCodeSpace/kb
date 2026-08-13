import { describe, expect, it } from 'vitest';
import type { Check, Task } from '../lib/model';
import { makeTask } from '../test/task';
import { shipWarning, shipWarningLines } from './ShipDialog';

function task(checks: Check[], blocked = false): Task {
  return makeTask({ title: 'ship me', checks, blocked });
}

describe('shipWarning', () => {
  it('is silent for a card with nothing open and no flag', () => {
    expect(shipWarning(task([]))).toBeNull();
    expect(shipWarning(task([{ text: 'a', done: true }]))).toBeNull();
  });

  it('counts the open checklist items', () => {
    const w = shipWarning(
      task([
        { text: 'a', done: true },
        { text: 'b', done: false },
        { text: 'c', done: false },
      ]),
    );
    expect(w).toEqual({ open: 2, total: 3, blocked: false });
  });

  it('warns about a blocked card even with a clean checklist', () => {
    expect(shipWarning(task([{ text: 'a', done: true }], true))).toEqual({
      open: 0,
      total: 1,
      blocked: true,
    });
  });

  it('folds both reasons into one warning', () => {
    const w = shipWarning(task([{ text: 'a', done: false }], true));
    expect(w).toEqual({ open: 1, total: 1, blocked: true });
    expect(shipWarningLines(w!)).toEqual([
      '1 of 1 checklist item is still open.',
      'This card is flagged blocked.',
    ]);
  });
});

describe('shipWarningLines', () => {
  it('names the count and pluralises', () => {
    expect(shipWarningLines({ open: 3, total: 5, blocked: false })).toEqual([
      '3 of 5 checklist items are still open.',
    ]);
  });

  it('mentions only the blocked flag when nothing is open', () => {
    expect(shipWarningLines({ open: 0, total: 2, blocked: true })).toEqual([
      'This card is flagged blocked.',
    ]);
  });
});
