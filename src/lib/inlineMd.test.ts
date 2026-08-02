import { describe, expect, it } from 'vitest';
import { parseDesc, tokenizeInline } from './inlineMd';

describe('tokenizeInline', () => {
  it('passes plain text through as one token', () => {
    expect(tokenizeInline('just words')).toEqual([{ kind: 'text', text: 'just words' }]);
  });

  it('tokenizes bold, italic, and code', () => {
    expect(tokenizeInline('a **b** *c* `d`')).toEqual([
      { kind: 'text', text: 'a ' },
      { kind: 'bold', text: 'b' },
      { kind: 'text', text: ' ' },
      { kind: 'italic', text: 'c' },
      { kind: 'text', text: ' ' },
      { kind: 'code', text: 'd' },
    ]);
  });

  it('tokenizes http(s) links only', () => {
    expect(tokenizeInline('[docs](https://example.com/x)')).toEqual([
      { kind: 'link', text: 'docs', href: 'https://example.com/x' },
    ]);
  });

  it('continues after a malformed link candidate', () => {
    expect(tokenizeInline('[bad](ftp://example.com) [docs](https://example.com/x)')).toEqual([
      { kind: 'text', text: '[bad](ftp://example.com) ' },
      { kind: 'link', text: 'docs', href: 'https://example.com/x' },
    ]);
  });

  it('continues after incomplete and multi-line labels', () => {
    expect(tokenizeInline('[bad\nlabel] [] [docs](https://example.com/x)')).toEqual([
      { kind: 'text', text: '[bad\nlabel] [] ' },
      { kind: 'link', text: 'docs', href: 'https://example.com/x' },
    ]);
  });

  it('rejects javascript: urls — stays literal text', () => {
    const toks = tokenizeInline('[evil](javascript:alert(1))');
    expect(toks.every((t) => t.kind !== 'link')).toBe(true);
    expect(toks.map((t) => t.text).join('')).toContain('evil');
  });

  it('leaves unclosed markers literal', () => {
    expect(tokenizeInline('a **b and `c')).toEqual([{ kind: 'text', text: 'a **b and `c' }]);
  });

  it('does not treat "* " list stars as italic', () => {
    expect(tokenizeInline('2 * 3 * 4')).toEqual([{ kind: 'text', text: '2 * 3 * 4' }]);
  });

  it('keeps a long unterminated link candidate as plain text', () => {
    const input = `[${'a'.repeat(10_000)}`;
    expect(tokenizeInline(input)).toEqual([{ kind: 'text', text: input }]);
  });

  it('keeps repeated link starts with an unfinished URL linear', () => {
    const input = `${'['.repeat(10_000)}label](https://${'a'.repeat(10_000)}`;
    expect(tokenizeInline(input)).toEqual([{ kind: 'text', text: input }]);
  });
});

describe('parseDesc', () => {
  it('detects bullet lines and keeps inline tokens', () => {
    const lines = parseDesc('intro\n- **first** item\n- second');
    expect(lines).toHaveLength(3);
    expect(lines[0].bullet).toBe(false);
    expect(lines[1].bullet).toBe(true);
    expect(lines[1].toks[0]).toEqual({ kind: 'bold', text: 'first' });
    expect(lines[2].bullet).toBe(true);
  });
});
