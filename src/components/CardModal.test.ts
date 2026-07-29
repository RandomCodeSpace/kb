import { describe, expect, it } from 'vitest';
import type { SimilarItem } from '../lib/api';
import { killedChipText } from './CardModal';

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
