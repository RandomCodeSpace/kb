import type { DescLine, InlineTok } from '../lib/inlineMd';
import { parseDesc } from '../lib/inlineMd';

/** Inline markdown tokens as React elements; never raw HTML. */
export function InlineText({ toks }: Readonly<{ toks: InlineTok[] }>) {
  return (
    <>
      {toks.map((t, i) => {
        switch (t.kind) {
          case 'code':
            return <code key={i}>{t.text}</code>;
          case 'bold':
            return <strong key={i}>{t.text}</strong>;
          case 'italic':
            return <em key={i}>{t.text}</em>;
          case 'link':
            return (
              <a
                key={i}
                href={t.href}
                target="_blank"
                rel="noopener noreferrer"
                onClick={(e) => e.stopPropagation()}
              >
                {t.text}
              </a>
            );
          default:
            return <span key={i}>{t.text}</span>;
        }
      })}
    </>
  );
}

type DescBlock =
  | { kind: 'code'; lines: string[] }
  | { kind: 'line'; line: DescLine };

/** Group consecutive fenced-code lines into one block per fence. */
export function descBlocks(lines: readonly DescLine[]): DescBlock[] {
  const blocks: DescBlock[] = [];
  for (const line of lines) {
    if (line.code === undefined) {
      blocks.push({ kind: 'line', line });
      continue;
    }
    const last = blocks[blocks.length - 1];
    if (last?.kind === 'code') last.lines.push(line.code);
    else blocks.push({ kind: 'code', lines: [line.code] });
  }
  return blocks;
}

function lineMarker(line: DescLine): string | null {
  if (line.bullet) return '•';
  if (line.ordinal !== undefined) return `${line.ordinal}.`;
  return null;
}

function Line({ line }: Readonly<{ line: DescLine }>) {
  if (line.heading !== undefined) {
    return (
      <div className="dline dhead">
        <strong>
          <InlineText toks={line.toks} />
        </strong>
      </div>
    );
  }
  const marker = lineMarker(line);
  return (
    <div className={marker === null ? 'dline' : 'dline bullet'}>
      {marker !== null && <span className="bdot">{marker}</span>}
      <span>
        <InlineText toks={line.toks} />
      </span>
    </div>
  );
}

/**
 * A description-flavored markdown body: the shared renderer for card
 * descriptions and comment bodies. Block structure (bullets, numbered
 * lists, headings, code fences) plus the inline tokens above.
 */
export function RichDesc({
  text,
  className = 'desc',
}: Readonly<{ text: string; className?: string }>) {
  return (
    <div className={className}>
      {descBlocks(parseDesc(text)).map((block, i) =>
        block.kind === 'code' ? (
          <pre key={i} className="dcode">
            <code>{block.lines.join('\n')}</code>
          </pre>
        ) : (
          <Line key={i} line={block.line} />
        ),
      )}
    </div>
  );
}
