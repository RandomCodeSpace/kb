import { describe, expect, it } from 'vitest';
import {
  emptyFilter,
  filterBoard,
  filterFromSearch,
  filterToSearch,
  isFilterActive,
  taskMatchesFilter,
  toggleTag,
} from './filter';
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

describe('filter URL persistence', () => {
  it('reads q and tags from the query string', () => {
    expect(filterFromSearch('?q=login&tags=auth,bug')).toEqual({
      text: 'login',
      tags: ['auth', 'bug'],
    });
  });

  it('is empty when the params are absent or blank', () => {
    expect(filterFromSearch('')).toEqual(emptyFilter());
    expect(filterFromSearch('?other=1')).toEqual(emptyFilter());
    expect(filterFromSearch('?q=&tags=')).toEqual(emptyFilter());
  });

  it('trims, drops empty entries, and dedupes tags', () => {
    expect(filterFromSearch('?tags=auth,%20,auth,,%20bug%20').tags).toEqual([
      'auth',
      'bug',
    ]);
  });

  it('decodes scoped labels and encoded text', () => {
    expect(filterFromSearch('?q=fix%20login&tags=env%3A%3Aprod')).toEqual({
      text: 'fix login',
      tags: ['env::prod'],
    });
  });

  it('writes the filter into the query string and round-trips', () => {
    const filter = { text: 'fix login', tags: ['auth', 'env::prod'] };
    const search = filterToSearch(filter, '');
    expect(filterFromSearch(search)).toEqual(filter);
  });

  it('removes its params when the filter empties but keeps foreign ones', () => {
    expect(filterToSearch(emptyFilter(), '?q=old&tags=auth&other=1')).toBe('?other=1');
    expect(filterToSearch(emptyFilter(), '?q=old')).toBe('');
    expect(filterToSearch({ text: '  ', tags: [] }, '?q=old')).toBe('');
  });

  it('preserves foreign params while writing its own', () => {
    const search = filterToSearch({ text: 'x', tags: ['a'] }, '?other=1');
    const params = new URLSearchParams(search);
    expect(params.get('other')).toBe('1');
    expect(params.get('q')).toBe('x');
    expect(params.get('tags')).toBe('a');
  });
});
