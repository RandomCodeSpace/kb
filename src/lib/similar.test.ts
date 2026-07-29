import { describe, expect, it } from 'vitest';
import { shouldQuery } from './similar';

describe('shouldQuery', () => {
  it('queries a new trimmed title of at least three characters', () => {
    expect(shouldQuery('  login redirect  ', '')).toBe(true);
  });

  it('does not query a short or already queried trimmed title', () => {
    expect(shouldQuery('  go ', '')).toBe(false);
    expect(shouldQuery(' login redirect ', 'login redirect')).toBe(false);
  });

  it('counts astral Unicode characters as code points', () => {
    expect(shouldQuery('😀😀', '')).toBe(false);
    expect(shouldQuery('😀😀😀', '')).toBe(true);
  });

  it('does not query malformed or short Unicode titles', () => {
    expect(shouldQuery('\ud83d\ud83d', '')).toBe(false);
    expect(shouldQuery('éé', '')).toBe(false);
  });
});
