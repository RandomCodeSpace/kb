import { describe, expect, it } from 'vitest';
import { emptyFilter, filterBoard, isFilterActive, taskMatchesFilter, toggleTag } from './filter';
import { makeTask } from '../test/task';
import type { Board } from './model';

const login = makeTask({ title: 'Fix login timeout', desc: 'auth token expires', tags: ['bug', 'auth'] });
const landing = makeTask({ title: 'Design landing page', desc: '', tags: ['ui'] });
const rotate = makeTask({ title: 'Rotate keys', desc: 'quarterly', tags: ['auth', 'env::prod'] });
const board: Board = { title: 'B', tasks: [login, landing, rotate] };

describe('board filter', () => {
  it('is inactive when empty and returns the same board object', () => {
    expect(isFilterActive(emptyFilter())).toBe(false);
    expect(isFilterActive({ text: '   ', tags: [] })).toBe(false);
    expect(filterBoard(board, emptyFilter())).toBe(board);
  });

  it('matches text case-insensitively over title, desc, and tags', () => {
    expect(taskMatchesFilter(login, { text: 'LOGIN', tags: [] })).toBe(true);
    expect(taskMatchesFilter(login, { text: 'token', tags: [] })).toBe(true);
    expect(taskMatchesFilter(login, { text: 'bug', tags: [] })).toBe(true);
    expect(taskMatchesFilter(login, { text: 'landing', tags: [] })).toBe(false);
  });

  it('requires every selected tag (AND) with exact matching', () => {
    expect(filterBoard(board, { text: '', tags: ['auth'] }).tasks).toEqual([login, rotate]);
    expect(filterBoard(board, { text: '', tags: ['auth', 'bug'] }).tasks).toEqual([login]);
    // Scoped labels compare whole: 'prod' is not 'env::prod'.
    expect(filterBoard(board, { text: '', tags: ['prod'] }).tasks).toEqual([]);
    expect(filterBoard(board, { text: '', tags: ['env::prod'] }).tasks).toEqual([rotate]);
  });

  it('combines text and tags', () => {
    expect(filterBoard(board, { text: 'quarterly', tags: ['auth'] }).tasks).toEqual([rotate]);
    expect(filterBoard(board, { text: 'quarterly', tags: ['bug'] }).tasks).toEqual([]);
  });

  it('toggleTag adds then removes', () => {
    const one = toggleTag(emptyFilter(), 'auth');
    expect(one.tags).toEqual(['auth']);
    const two = toggleTag(one, 'bug');
    expect(two.tags).toEqual(['auth', 'bug']);
    expect(toggleTag(two, 'auth').tags).toEqual(['bug']);
  });
});
