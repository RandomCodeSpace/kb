import { describe, expect, it } from 'vitest';
import type { DriftResult, SimilarItem } from '../lib/api';
import {
  driftMessage,
  killedChipText,
  similarKey,
  taskImportLinks,
  visibleSimilarItems,
} from './CardModal';
import type { Task } from '../lib/model';

function killed(overrides: Partial<SimilarItem> = {}): SimilarItem {
  return {
    title: 'Add SSO login',
    via: 'killed',
    ...overrides,
  };
}

describe('killedChipText', () => {
  it('carries a human month and the rejection reason', () => {
    expect(
      killedChipText(
        killed({
          reason: 'superseded by the SSO work',
          killedAt: '2026-03-14T09:12:00Z',
        }),
        new Date('2026-07-29T00:00:00Z'),
      ),
    ).toBe('rejected March 2026 \u2014 superseded by the SSO work');
  });

  it('uses now when the killed timestamp is absent or invalid', () => {
    const now = new Date('2026-07-29T00:00:00Z');
    expect(killedChipText(killed(), now)).toBe('rejected July 2026');
    expect(killedChipText(killed({ killedAt: 'not-a-date' }), now)).toBe(
      'rejected July 2026',
    );
  });

  it('collapses whitespace and truncates the full chip without splitting Unicode', () => {
    const text = killedChipText(
      killed({
        reason: `first line\n${'\u{1FAA6}'.repeat(100)}`,
        killedAt: '2026-03-14T09:12:00Z',
      }),
      new Date('2026-07-29T00:00:00Z'),
    );

    expect(text).not.toContain('\n');
    expect(Array.from(text)).toHaveLength(120);
    expect(text).toMatch(/^rejected March 2026 \u2014 first line /);
    expect(text).toMatch(/\u2026$/);
  });
});

function drift(overrides: Partial<DriftResult> = {}): DriftResult {
  return {
    state: 'unchanged',
    link: 'gitlab#42',
    url: 'https://gitlab.example.com/acme/app/-/issues/42',
    title_changed: false,
    upstream_title: 'ship login',
    baseline_title: 'draft login',
    baseline_at: '2026-03-14T09:12:00Z',
    checked_at: '2026-07-29T18:00:00Z',
    summary: '',
    ...overrides,
  };
}

describe('driftMessage', () => {
  it('states honestly that the first check only recorded a baseline', () => {
    expect(driftMessage(drift({ state: 'baseline_recorded' }))).toBe(
      'Recorded what this issue looks like now. Future checks will show what changed.',
    );
  });

  it('names the comparison date when upstream is unchanged', () => {
    expect(driftMessage(drift())).toBe(
      'No change upstream since March 14, 2026.',
    );
  });

  it('distinguishes a title change from a body-only change', () => {
    expect(
      driftMessage(drift({ state: 'drifted', title_changed: true })),
    ).toBe('Upstream changed since March 14, 2026, including the title.');
    expect(
      driftMessage(drift({ state: 'drifted', title_changed: false })),
    ).toBe('Upstream content changed since March 14, 2026.');
  });
});

describe('taskImportLinks', () => {
  it('extracts distinct non-empty short links without deriving provenance', () => {
    const task = {
      tags: [
        'team::auth',
        'link::gitlab#42',
        'link::gitlab#42',
        'link:: github#7 ',
        'link::',
      ],
    } as Task;

    expect(taskImportLinks(task)).toEqual(['gitlab#42', 'github#7']);
  });
});

// Index-based React keys reused the wrong row after a removal, especially when
// distinct cards shared the same title.
describe('similarKey', () => {
  it('prefers a card id over a link and a title', () => {
    expect(
      similarKey({
        id: 'card-1',
        link: 'gitlab#1',
        title: 'Duplicate',
        via: 'card',
      }),
    ).toBe('id:card-1');
  });

  it('uses a link before falling back to the title', () => {
    expect(
      similarKey({
        link: 'gitlab#1',
        title: 'Imported duplicate',
        via: 'import',
      }),
    ).toBe('link:gitlab#1');
    expect(
      similarKey({ title: 'Rejected duplicate', via: 'killed' }),
    ).toBe('title:Rejected duplicate');
  });

  it('keeps equal-title cards distinct when their ids differ', () => {
    const first: SimilarItem = {
      id: 'card-1',
      title: 'Duplicate',
      via: 'card',
    };
    const second: SimilarItem = {
      id: 'card-2',
      title: 'Duplicate',
      via: 'card',
    };

    expect(similarKey(first)).not.toBe(similarKey(second));
  });
});

// Per-row dismissal must neither hide surviving candidates nor leak into a
// later result set whose keys happen to repeat.
describe('visibleSimilarItems', () => {
  const items: SimilarItem[] = [
    { id: 'card-1', title: 'First', via: 'card' },
    { id: 'card-2', title: 'Second', via: 'card' },
    { link: 'gitlab#3', title: 'Third', via: 'import' },
  ];

  it('removes one key while leaving the other rows visible', () => {
    expect(
      visibleSimilarItems(items, new Set([similarKey(items[1]!)])),
    ).toEqual([items[0], items[2]]);
  });

  it('returns an empty list after every row is removed', () => {
    expect(
      visibleSimilarItems(items, new Set(items.map(similarKey))),
    ).toEqual([]);
  });

  it('shows a fresh result set after stale removal keys are reset', () => {
    const fresh: SimilarItem[] = [
      { id: 'card-1', title: 'First result for another query', via: 'card' },
    ];
    const stale = new Set([similarKey(fresh[0]!)]);

    expect(visibleSimilarItems(fresh, stale)).toEqual([]);
    expect(visibleSimilarItems(fresh, new Set())).toEqual(fresh);
  });
});
