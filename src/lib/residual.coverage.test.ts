import { afterEach, describe, expect, it, vi } from 'vitest';
import { reducedMotion } from './confetti';
import { tagColor } from './labels';
import { cardLabel, isScoped, newDraft, progress } from './model';
import { coerceStoryDraft } from './storyDraft';
import { ageChip, ymd } from './urgency';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('residual deterministic utility branches', () => {
  it('uses the first palette color for an empty tag', () => {
    expect(tagColor('')).toBe('#ff7b54');
  });

  it('recognises scoped and unscoped labels', () => {
    expect(isScoped('env::prod')).toBe(true);
    expect(isScoped('backend')).toBe(false);
  });

  it('returns no checklist progress when a task has no checks', () => {
    const task = { ...newDraft({ title: 'no checks' }), id: 'a', createdAt: '', movedAt: '' };
    expect(progress(task)).toBeNull();
  });

  it('announces blocked checklist progress and lifted state', () => {
    const task = {
      ...newDraft({ title: 'checked' }),
      id: 'b',
      createdAt: '',
      movedAt: '',
      blocked: true,
      checks: [{ text: 'one', done: true }, { text: 'two', done: false }],
    };
    expect(cardLabel(task, 1, 3, true)).toContain(
      'blocked, 1 of 2 checklist items done, lifted',
    );
  });

  it('uses defaults before applying explicit draft fields', () => {
    expect(newDraft({ title: 'write tests', status: 'doing' })).toEqual({
      emoji: '',
      desc: '',
      status: 'doing',
      blocked: false,
      prio: 3,
      tags: [],
      checks: [],
      title: 'write tests',
    });
  });

  it('formats a local calendar date with padded components', () => {
    expect(ymd(new Date(2026, 0, 2, 12))).toBe('2026-01-02');
  });

  it('clamps negative task age to the current moment', () => {
    expect(ageChip('doing', '2026-08-02T00:00:00Z', '2026-08-02T00:00:00Z', 0))
      .toBe('1h here');
  });

  it('coerces a non-object story response to documented defaults', () => {
    expect(coerceStoryDraft(null)).toEqual({
      title: '', emoji: '', desc: '', prio: 3, due: '', effort: '', tags: [], checks: [],
    });
  });

  it('sanitises multiline story fields and rejects malformed collections', () => {
    expect(coerceStoryDraft({
      title: '  hello\nworld ',
      emoji: 'not emoji',
      desc: ' one\r\n two\u2028three ',
      prio: 9,
      due: 'tomorrow',
      effort: 'XL',
      tags: ['#ok', 'two words', 7, ''],
      checks: [null, {}, { text: '  do\tthis ', done: true }, { text: '  ' }],
    })).toEqual({
      title: 'hello world',
      emoji: '',
      desc: 'one\ntwo three',
      prio: 3,
      due: '',
      effort: '',
      tags: ['ok'],
      checks: [{ text: 'do this', done: true }],
    });
  });

  it('treats a throwing media-query implementation as no preference', () => {
    vi.stubGlobal('matchMedia', () => {
      throw new Error('unsupported');
    });
    expect(reducedMotion()).toBe(false);
  });

  it('honours reduced motion without creating particles', () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true }));
    expect(reducedMotion()).toBe(true);
  });
});
