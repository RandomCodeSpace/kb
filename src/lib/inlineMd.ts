/**
 * Minimal, XSS-safe inline markdown for card descriptions and checklist text.
 * Tokens only — rendering maps tokens to React elements, never raw HTML.
 * Supported: `code`, **bold**, *italic*, [text](https://url), leading "- " bullets.
 */
export type InlineTok =
  | { kind: 'text' | 'bold' | 'italic' | 'code'; text: string }
  | { kind: 'link'; text: string; href: string };

const CODE_PATTERN = /`([^`\r\n]+)`/g;
const BOLD_PATTERN = /\*\*([^*\r\n]+)\*\*/g;
const ITALIC_PATTERN = /\*([^*\s][^*\r\n]*)\*/g;
const LINK_PATTERN = /\[([^\]\r\n]+)\]\((https?:\/\/[^\s)]+)\)/g;

interface InlineMatch {
  start: number;
  end: number;
  priority: number;
  token: InlineTok;
}

function matches(
  line: string,
  pattern: RegExp,
  priority: number,
  token: (match: RegExpMatchArray) => InlineTok,
): InlineMatch[] {
  return [...line.matchAll(pattern)].map((match) => ({
    start: match.index,
    end: match.index + match[0].length,
    priority,
    token: token(match),
  }));
}

function inlineMatches(line: string): InlineMatch[] {
  return [
    ...matches(line, CODE_PATTERN, 0, (match) => ({ kind: 'code', text: match[1] })),
    ...matches(line, BOLD_PATTERN, 1, (match) => ({ kind: 'bold', text: match[1] })),
    ...matches(line, ITALIC_PATTERN, 2, (match) => ({ kind: 'italic', text: match[1] })),
    ...matches(line, LINK_PATTERN, 3, (match) => ({
      kind: 'link',
      text: match[1],
      href: match[2],
    })),
  ].sort((left, right) => left.start - right.start || left.priority - right.priority);
}

export function tokenizeInline(line: string): InlineTok[] {
  const out: InlineTok[] = [];
  let last = 0;
  for (const match of inlineMatches(line)) {
    if (match.start < last) continue;
    if (match.start > last) {
      out.push({ kind: 'text', text: line.slice(last, match.start) });
    }
    out.push(match.token);
    last = match.end;
  }
  if (last < line.length) out.push({ kind: 'text', text: line.slice(last) });
  return out;
}

export interface DescLine {
  bullet: boolean;
  toks: InlineTok[];
}

export function parseDesc(desc: string): DescLine[] {
  return desc.split('\n').map((raw) => {
    const bullet = raw.startsWith('- ');
    return { bullet, toks: tokenizeInline(bullet ? raw.slice(2) : raw) };
  });
}
