import { describe, expect, it } from 'vitest';
import type { ImportDraft } from '../lib/api';
import { importRowsToTasks, toImportRows } from './ImportModal';

function draft(over: Partial<ImportDraft> = {}): ImportDraft {
  return {
    title: 'replace the legacy login',
    emoji: '🔐',
    desc: 'Keep the migration reversible.',
    prio: 2,
    due: '',
    effort: 'M',
    tags: ['team::auth', 'link::gitlab#42'],
    checks: [{ text: 'cover rollback', done: false }],
    link: 'gitlab#42',
    external_key: 'gitlab:gitlab.example.com/acme/app#42',
    url: 'https://gitlab.example.com/acme/app/-/issues/42',
    ...over,
  };
}

describe('toImportRows', () => {
  it('leaves an exact-link duplicate unchecked and names the existing card', () => {
    // An exact provenance match must not be imported twice by default.
    const [row] = toImportRows([
      draft({
        duplicate_of: {
          id: 'existing',
          title: 'replace login already',
          via: 'link',
        },
      }),
    ]);

    expect(row.selected).toBe(false);
    expect(row.duplicateChip).toBe(
      'already on the board as “replace login already”',
    );
  });

  it('keeps a similar-title proposal selected and labels it as advisory', () => {
    // Similarity is a review hint, not proof that the source issue was imported.
    const [row] = toImportRows([
      draft({
        duplicate_of: {
          title: 'modernise login',
          via: 'similar',
        },
      }),
    ]);

    expect(row.selected).toBe(true);
    expect(row.duplicateChip).toBe('similar: “modernise login”');
  });

  it('selects a proposal with no duplicate candidate', () => {
    expect(toImportRows([draft({ duplicate_of: undefined })])[0].selected).toBe(
      true,
    );
  });
});

describe('importRowsToTasks', () => {
  it('preserves the server-authored provenance tag while applying review edits', () => {
    // Dropping link:: here makes the durable duplicate check forget the import.
    const rows = toImportRows([draft()]);
    rows[0].title = '  ship the reviewed login change  ';

    const [task] = importRowsToTasks(rows, 'doing');

    expect(task.title).toBe('ship the reviewed login change');
    expect(task.emoji).toBe('🔐');
    expect(task.status).toBe('doing');
    expect(task.tags).toContain('link::gitlab#42');
    expect(task.checks).toEqual([{ text: 'cover rollback', done: false }]);
  });

  it('creates tasks only for selected rows with usable titles', () => {
    const rows = toImportRows([
      draft({ title: 'one' }),
      draft({ title: 'two', link: 'gitlab#43' }),
    ]);
    rows[0].selected = false;
    rows[1].title = ' ';

    expect(importRowsToTasks(rows, 'todo')).toEqual([]);
  });
});
