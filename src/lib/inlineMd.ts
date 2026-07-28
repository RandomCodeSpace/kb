/**
 * Minimal, XSS-safe inline markdown for card descriptions and checklist text.
 * Tokens only — rendering maps tokens to React elements, never raw HTML.
 * Supported: `code`, **bold**, *italic*, [text](https://url), leading "- " bullets.
 */
export type InlineTok =
  | { kind: 'text' | 'bold' | 'italic' | 'code'; text: string }
  | { kind: 'link'; text: string; href: string };

const PATTERN =
  /`([^`]+)`|\*\*([^*]+)\*\*|\*([^*\s][^*]*)\*|\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g;

export function tokenizeInline(line: string): InlineTok[] {
  const out: InlineTok[] = [];
  let last = 0;
  for (const m of line.matchAll(PATTERN)) {
    const idx = m.index ?? 0;
    if (idx > last) out.push({ kind: 'text', text: line.slice(last, idx) });
    if (m[1] !== undefined) out.push({ kind: 'code', text: m[1] });
    else if (m[2] !== undefined) out.push({ kind: 'bold', text: m[2] });
    else if (m[3] !== undefined) out.push({ kind: 'italic', text: m[3] });
    else out.push({ kind: 'link', text: m[4], href: m[5] });
    last = idx + m[0].length;
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
