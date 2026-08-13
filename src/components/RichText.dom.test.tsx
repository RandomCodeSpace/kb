// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { descBlocks, RichDesc } from './RichText';
import { parseDesc } from '../lib/inlineMd';

describe('RichDesc', () => {
  it('renders headings, lists, code fences, and inline markdown', () => {
    const text = [
      '## Plan',
      'intro with **bold** and https://example.com/x',
      '1. first step',
      '- a bullet',
      '```',
      'raw **verbatim** line',
      'second line',
      '```',
      'after',
    ].join('\n');
    const { container } = render(<RichDesc text={text} />);

    const head = container.querySelector('.dhead');
    expect(head?.textContent).toBe('Plan');
    expect(head?.querySelector('strong')).not.toBeNull();

    expect(screen.getByText('bold', { selector: 'strong' })).toBeTruthy();
    const link = screen.getByRole('link', { name: 'https://example.com/x' });
    expect(link.getAttribute('href')).toBe('https://example.com/x');
    expect(link.getAttribute('rel')).toContain('noopener');

    const markers = [...container.querySelectorAll('.bdot')].map((el) => el.textContent);
    expect(markers).toEqual(['1.', '•']);

    // One fence, one <pre>, verbatim content with no inline tokens.
    const pre = container.querySelectorAll('pre.dcode');
    expect(pre).toHaveLength(1);
    expect(pre[0].textContent).toBe('raw **verbatim** line\nsecond line');
    expect(pre[0].querySelector('strong')).toBeNull();

    expect(screen.getByText('after')).toBeTruthy();
  });

  it('renders under a caller-supplied class for comment bodies', () => {
    const { container } = render(<RichDesc text="plain" className="comment-body" />);
    expect(container.querySelector('.comment-body .dline')?.textContent).toBe('plain');
  });

  it('groups only consecutive code lines into one block', () => {
    const blocks = descBlocks(parseDesc('```\na\nb\n```\ntext\n```\nc\n```'));
    const kinds = blocks.map((b) => (b.kind === 'code' ? `code:${b.lines.join('|')}` : 'line'));
    expect(kinds).toEqual(['code:a|b', 'line', 'code:c']);
  });
});
