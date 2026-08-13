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
    // The markdown link never completes; its URL half still autolinks as a
    // bare URL. The point of this case is linear parsing, not the split.
    const url = `https://${'a'.repeat(10_000)}`;
    const prefix = `${'['.repeat(10_000)}label](`;
    expect(tokenizeInline(prefix + url)).toEqual([
      { kind: 'text', text: prefix },
      { kind: 'link', text: url, href: url },
    ]);
  });
});

describe('tokenizeInline extensions', () => {
  it('tokenizes underscore emphasis with word boundaries', () => {
    expect(tokenizeInline('an _emphasized_ word')).toEqual([
      { kind: 'text', text: 'an ' },
      { kind: 'italic', text: 'emphasized' },
      { kind: 'text', text: ' word' },
    ]);
    // snake_case identifiers keep their underscores.
    expect(tokenizeInline('use snake_case_names here')).toEqual([
      { kind: 'text', text: 'use snake_case_names here' },
    ]);
    expect(tokenizeInline('_x_')).toEqual([{ kind: 'italic', text: 'x' }]);
  });

  it('autolinks bare URLs and strips trailing sentence punctuation', () => {
    expect(tokenizeInline('see https://example.com/a?b=1.')).toEqual([
      { kind: 'text', text: 'see ' },
      { kind: 'link', text: 'https://example.com/a?b=1', href: 'https://example.com/a?b=1' },
      { kind: 'text', text: '.' },
    ]);
  });

  it('does not double-link the URL inside a markdown link', () => {
    expect(tokenizeInline('[docs](https://example.com/x)')).toEqual([
      { kind: 'link', text: 'docs', href: 'https://example.com/x' },
    ]);
  });

  it('leaves URLs inside code spans as code', () => {
    expect(tokenizeInline('`https://example.com`')).toEqual([
      { kind: 'code', text: 'https://example.com' },
    ]);
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

  it('detects headings, numbered items, and code fences', () => {
    const lines = parseDesc([
      '## Plan',
      '1. first',
      '12. twelfth',
      '```go',
      'func main() {}',
      '',
      '```',
      'after',
    ].join('\n'));
    expect(lines.map((l) => [l.heading, l.ordinal, l.code, l.bullet])).toEqual([
      [2, undefined, undefined, false],
      [undefined, '1', undefined, false],
      [undefined, '12', undefined, false],
      [undefined, undefined, 'func main() {}', false],
      [undefined, undefined, '', false],
      [undefined, undefined, undefined, false],
    ]);
    expect(lines[0].toks).toEqual([{ kind: 'text', text: 'Plan' }]);
    expect(lines[1].toks).toEqual([{ kind: 'text', text: 'first' }]);
  });

  it('treats an unterminated fence as code to the end', () => {
    const lines = parseDesc('```\nraw line');
    expect(lines).toEqual([{ bullet: false, code: 'raw line', toks: [] }]);
  });

  it('does not tokenize markdown inside fences', () => {
    const lines = parseDesc('```\n**not bold**\n```');
    expect(lines).toEqual([{ bullet: false, code: '**not bold**', toks: [] }]);
  });

  it('leaves #### and unspaced # lines as plain text', () => {
    const lines = parseDesc('#### too deep\n#nospace');
    expect(lines[0].heading).toBeUndefined();
    expect(lines[1].heading).toBeUndefined();
  });
});
