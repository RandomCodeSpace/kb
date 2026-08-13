/**
 * Minimal, XSS-safe inline markdown for card descriptions, comments, and
 * checklist text. Tokens only — rendering maps tokens to React elements,
 * never raw HTML. Supported: `code`, **bold**, *italic*, _italic_,
 * [text](https://url), bare http(s) URLs, leading "- " bullets, "1." lists,
 * #/##/### headings, and ``` code fences.
 */
export type InlineTok =
  | { kind: 'text' | 'bold' | 'italic' | 'code'; text: string }
  | { kind: 'link'; text: string; href: string };

const CODE_PATTERN = /`([^`\r\n]+)`/g;
const BOLD_PATTERN = /\*\*([^*\r\n]+)\*\*/g;
const ITALIC_PATTERN = /\*([^*\s][^*\r\n]*)\*/g;
// Word-bounded so snake_case identifiers keep their underscores.
const UNDERSCORE_ITALIC_PATTERN = /(?<![\w])_([^\s_](?:[^_\r\n]*[^\s_])?)_(?![\w])/g;
const BARE_URL_PATTERN = /https?:\/\/[^\s<>()"'`]+/g;
// Sentence punctuation after a pasted URL belongs to the prose, not the link.
const URL_TRAILING_PUNCT = /[.,;:!?]+$/;
const WHITESPACE_PATTERN = /\s/u;
const HTTP_PREFIX = 'http:' + '//';
const HTTPS_PREFIX = 'https:' + '//';

interface InlineMatch {
  start: number;
  end: number;
  priority: number;
  token: InlineTok;
}

interface LinkCandidate {
  match: InlineMatch | null;
  cursor: number;
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

function parseLinkCandidate(
  line: string,
  start: number,
  labelEnd: number,
): LinkCandidate {
  const lineBreak = line.slice(start + 1, labelEnd).search(/[\r\n]/u);
  if (lineBreak >= 0) {
    return { match: null, cursor: start + lineBreak + 2 };
  }
  if (labelEnd === start + 1 || line[labelEnd + 1] !== '(') {
    return { match: null, cursor: labelEnd + 1 };
  }

  const hrefStart = labelEnd + 2;
  if (!line.startsWith(HTTP_PREFIX, hrefStart) && !line.startsWith(HTTPS_PREFIX, hrefStart)) {
    return { match: null, cursor: labelEnd + 1 };
  }
  let hrefEnd = hrefStart;
  while (
    hrefEnd < line.length
    && line[hrefEnd] !== ')'
    && !WHITESPACE_PATTERN.test(line[hrefEnd])
  ) {
    hrefEnd += 1;
  }
  if (line[hrefEnd] !== ')') {
    return { match: null, cursor: hrefEnd + 1 };
  }

  return {
    match: {
      start,
      end: hrefEnd + 1,
      priority: 3,
      token: {
        kind: 'link',
        text: line.slice(start + 1, labelEnd),
        href: line.slice(hrefStart, hrefEnd),
      },
    },
    cursor: hrefEnd + 1,
  };
}

function linkMatches(line: string): InlineMatch[] {
  const found: InlineMatch[] = [];
  let cursor = 0;
  while (cursor < line.length) {
    const start = line.indexOf('[', cursor);
    if (start < 0) break;
    const labelEnd = line.indexOf(']', start + 1);
    if (labelEnd < 0) break;
    const candidate = parseLinkCandidate(line, start, labelEnd);
    if (candidate.match) found.push(candidate.match);
    cursor = candidate.cursor;
  }
  return found;
}

function bareURLMatches(line: string): InlineMatch[] {
  return [...line.matchAll(BARE_URL_PATTERN)].map((match) => {
    const url = match[0].replace(URL_TRAILING_PUNCT, '');
    return {
      start: match.index,
      end: match.index + url.length,
      priority: 4,
      token: { kind: 'link' as const, text: url, href: url },
    };
  });
}

function inlineMatches(line: string): InlineMatch[] {
  return [
    ...matches(line, CODE_PATTERN, 0, (match) => ({ kind: 'code', text: match[1] })),
    ...matches(line, BOLD_PATTERN, 1, (match) => ({ kind: 'bold', text: match[1] })),
    ...matches(line, ITALIC_PATTERN, 2, (match) => ({ kind: 'italic', text: match[1] })),
    ...matches(line, UNDERSCORE_ITALIC_PATTERN, 2, (match) => ({ kind: 'italic', text: match[1] })),
    ...linkMatches(line),
    ...bareURLMatches(line),
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
  /** List ordinal ("3") when the line is a "3. " numbered item. */
  ordinal?: string;
  /** Heading level 1-3 when the line is a #/##/### heading. */
  heading?: number;
  /** Verbatim line inside a ``` fence; toks stays empty for these. */
  code?: string;
  toks: InlineTok[];
}

const HEADING_PATTERN = /^(#{1,3})\s+(.*)$/;
const ORDERED_PATTERN = /^(\d{1,3})\.\s+(.*)$/;

function descLine(raw: string): DescLine {
  const heading = HEADING_PATTERN.exec(raw);
  if (heading) {
    return { bullet: false, heading: heading[1].length, toks: tokenizeInline(heading[2]) };
  }
  const ordered = ORDERED_PATTERN.exec(raw);
  if (ordered) {
    return { bullet: false, ordinal: ordered[1], toks: tokenizeInline(ordered[2]) };
  }
  const bullet = raw.startsWith('- ');
  return { bullet, toks: tokenizeInline(bullet ? raw.slice(2) : raw) };
}

export function parseDesc(desc: string): DescLine[] {
  const out: DescLine[] = [];
  let inFence = false;
  for (const raw of desc.split('\n')) {
    // A fence delimiter line (optionally carrying a language tag on open)
    // toggles code mode and is not rendered itself.
    if (raw.startsWith('```')) {
      inFence = !inFence;
      continue;
    }
    if (inFence) {
      out.push({ bullet: false, code: raw, toks: [] });
      continue;
    }
    out.push(descLine(raw));
  }
  return out;
}
