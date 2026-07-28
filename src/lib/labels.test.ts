import { describe, expect, it } from 'vitest';
import { addTags, boardLabels, filterLabels, parseTagInput, unionLabels } from './labels';
import type { Board, Task } from './model';

function task(tags: string[]): Task {
  return {
    id: tags.join(','),
    emoji: '',
    title: 't',
    desc: '',
    status: 'todo',
    blocked: false,
    prio: 3,
    tags,
    checks: [],
    createdAt: '2026-07-27T00:00:00Z',
    movedAt: '2026-07-27T00:00:00Z',
  };
}

function board(...tags: string[][]): Board {
  return { title: 'b', tasks: tags.map(task) };
}

describe('boardLabels', () => {
  it('collects distinct tags sorted', () => {
    expect(boardLabels(board(['infra', 'env::prod'], ['backend', 'infra']))).toEqual([
      'backend',
      'env::prod',
      'infra',
    ]);
  });

  it('returns [] for a board without tags', () => {
    expect(boardLabels(board([], []))).toEqual([]);
  });
});

describe('unionLabels', () => {
  it('merges, dedupes and sorts', () => {
    expect(unionLabels(['b', 'a'], ['c', 'a'])).toEqual(['a', 'b', 'c']);
  });
});

describe('filterLabels', () => {
  const ALL = ['backend', 'frontend', 'type::bug', 'type::feature', 'infra'];

  it('excludes already-selected labels', () => {
    expect(filterLabels(ALL, ['backend'], '')).toEqual([
      'frontend',
      'type::bug',
      'type::feature',
      'infra',
    ]);
  });

  it('matches case-insensitively', () => {
    expect(filterLabels(ALL, [], 'BACK')).toEqual(['backend']);
  });

  it('ranks prefix matches before substring matches', () => {
    expect(filterLabels(['grep-end', 'endgame'], [], 'end')).toEqual([
      'endgame',
      'grep-end',
    ]);
  });

  it('matches inside scoped labels', () => {
    expect(filterLabels(ALL, [], 'bug')).toEqual(['type::bug']);
  });

  it('caps results at the limit', () => {
    expect(filterLabels(ALL, [], '', 2)).toEqual(['backend', 'frontend']);
  });

  it('returns [] when nothing matches', () => {
    expect(filterLabels(ALL, [], 'zzz')).toEqual([]);
  });
});

describe('parseTagInput', () => {
  it('splits on whitespace and strips a leading #', () => {
    expect(parseTagInput('  #infra env::prod \n backend ')).toEqual([
      'infra',
      'env::prod',
      'backend',
    ]);
  });

  it('returns [] for blank input', () => {
    expect(parseTagInput('   ')).toEqual([]);
  });
});

describe('addTags', () => {
  it('appends new tags and skips duplicates', () => {
    expect(addTags(['infra'], 'infra backend')).toEqual(['infra', 'backend']);
  });

  it('keeps the current list for blank input', () => {
    expect(addTags(['infra'], '  ')).toEqual(['infra']);
  });
});
