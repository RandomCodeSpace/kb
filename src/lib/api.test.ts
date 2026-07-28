import { describe, expect, it } from 'vitest';
import { coerceStoryDraft } from './api';

describe('coerceStoryDraft', () => {
  it('passes a well-formed draft through', () => {
    const d = coerceStoryDraft({
      title: '  Ship it ',
      desc: 'do the thing',
      prio: 2,
      due: '2026-08-01',
      effort: 'M',
      tags: ['infra', ' type::bug '],
      checks: [{ text: ' step one ', done: true }, { text: 'step two', done: false }],
    });
    expect(d).toEqual({
      title: 'Ship it',
      desc: 'do the thing',
      prio: 2,
      due: '2026-08-01',
      effort: 'M',
      tags: ['infra', 'type::bug'],
      checks: [
        { text: 'step one', done: true },
        { text: 'step two', done: false },
      ],
    });
  });

  it('defaults every field for junk input', () => {
    expect(coerceStoryDraft('nope')).toEqual({
      title: '',
      desc: '',
      prio: 3,
      due: '',
      effort: '',
      tags: [],
      checks: [],
    });
  });

  it('clamps out-of-range prio to 3', () => {
    expect(coerceStoryDraft({ prio: 9 }).prio).toBe(3);
    expect(coerceStoryDraft({ prio: 0 }).prio).toBe(3);
    expect(coerceStoryDraft({ prio: '2' }).prio).toBe(3);
  });

  it('rejects a malformed due date and effort', () => {
    const d = coerceStoryDraft({ due: 'next week', effort: 'XL' });
    expect(d.due).toBe('');
    expect(d.effort).toBe('');
  });

  it('drops non-string tags and empty check texts', () => {
    const d = coerceStoryDraft({
      tags: ['ok', 7, '  '],
      checks: [{ text: '', done: true }, { text: 'keep' }, 'junk'],
    });
    expect(d.tags).toEqual(['ok']);
    expect(d.checks).toEqual([{ text: 'keep', done: false }]);
  });
});
