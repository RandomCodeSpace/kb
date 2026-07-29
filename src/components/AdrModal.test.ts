import { describe, expect, it } from 'vitest';
import type { StoryDraft } from '../lib/api';
import {
  ADR_MAX_BYTES,
  adrBytes,
  clampMax,
  rowsToTasks,
  splitRequest,
  toRows,
} from './AdrModal';

function draft(over: Partial<StoryDraft> = {}): StoryDraft {
  return {
    title: 'add a health endpoint',
    desc: 'so the SPA can detect the server',
    prio: 3,
    due: '',
    effort: '',
    tags: ['type::chore'],
    checks: [{ text: 'write the handler', done: false }],
    ...over,
  };
}

describe('adrBytes', () => {
  it('counts UTF-8 bytes, not UTF-16 units, like the server does', () => {
    expect(adrBytes('abc')).toBe(3);
    expect(adrBytes('é')).toBe(2);
    expect(adrBytes('🔧')).toBe(4);
  });

  it('agrees with the 64 KiB ceiling', () => {
    expect(adrBytes('a'.repeat(ADR_MAX_BYTES))).toBe(ADR_MAX_BYTES);
    expect(adrBytes('a'.repeat(ADR_MAX_BYTES + 1))).toBeGreaterThan(
      ADR_MAX_BYTES,
    );
  });
});

describe('clampMax', () => {
  it('clamps to the range the server accepts', () => {
    expect(clampMax(0)).toBe(1);
    expect(clampMax(-4)).toBe(1);
    expect(clampMax(100)).toBe(20);
    expect(clampMax(7)).toBe(7);
    expect(clampMax(7.6)).toBe(8);
  });

  it('falls back to the default for junk', () => {
    expect(clampMax(Number.NaN)).toBe(8);
  });
});

describe('splitRequest', () => {
  it('builds an ADR-only request without leaking the selected source', () => {
    expect(splitRequest('  # ADR\n', '', 'work')).toEqual({
      adr: '  # ADR\n',
    });
  });

  it('builds a URL request with its selected forge source', () => {
    expect(
      splitRequest('', '  gitlab.example.com/group/project/-/issues/42  ', 'work'),
    ).toEqual({
      url: 'gitlab.example.com/group/project/-/issues/42',
      source: 'work',
    });
  });

  it('rejects both inputs and neither input', () => {
    expect(splitRequest('ADR', 'github.com/org/repo/issues/1', 'github')).toEqual({
      error: 'provide adr or url',
    });
    expect(splitRequest('  ', '  ', 'github')).toEqual({
      error: 'provide adr or url',
    });
  });

  it('requires a selected source for a forge issue URL', () => {
    expect(splitRequest('', 'github.com/org/repo/issues/1', '  ')).toEqual({
      error: 'select a forge source',
    });
  });
});

describe('toRows', () => {
  it('selects everything and seeds the inline edits from the draft', () => {
    const rows = toRows([draft({ prio: 1, effort: 'L' })]);
    expect(rows).toHaveLength(1);
    expect(rows[0].selected).toBe(true);
    expect(rows[0].title).toBe('add a health endpoint');
    expect(rows[0].prio).toBe(1);
    expect(rows[0].effort).toBe('L');
  });
});

describe('rowsToTasks', () => {
  it('creates only the selected rows, in the chosen column', () => {
    const rows = toRows([draft({ title: 'one' }), draft({ title: 'two' })]);
    rows[1].selected = false;
    const tasks = rowsToTasks(rows, 'doing');
    expect(tasks.map((t) => t.title)).toEqual(['one']);
    expect(tasks[0].status).toBe('doing');
  });

  it('applies the inline edits over the draft', () => {
    const rows = toRows([draft()]);
    rows[0].title = '  renamed  ';
    rows[0].prio = 1;
    rows[0].effort = 'S';
    const [t] = rowsToTasks(rows, 'todo');
    expect(t.title).toBe('renamed');
    expect(t.prio).toBe(1);
    expect(t.effort).toBe('S');
    // Untouched fields still come from the draft.
    expect(t.desc).toBe('so the SPA can detect the server');
    expect(t.tags).toEqual(['type::chore']);
    expect(t.checks).toEqual([{ text: 'write the handler', done: false }]);
  });

  it('drops a row whose title was emptied — it could not round-trip', () => {
    const rows = toRows([draft()]);
    rows[0].title = '   ';
    expect(rowsToTasks(rows, 'todo')).toEqual([]);
  });

  it('creates nothing until rows are selected', () => {
    const rows = toRows([draft(), draft()]);
    for (const r of rows) r.selected = false;
    expect(rowsToTasks(rows, 'todo')).toEqual([]);
  });
});
