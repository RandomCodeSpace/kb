import { describe, expect, it } from 'vitest';

/**
 * WCAG 2.2 SC 1.4.4 Resize Text (AA) requires text to survive being scaled to
 * 200%. On a touch device the only way a user gets there is pinch zoom, and a
 * viewport meta that pins `maximum-scale` or sets `user-scalable=no` takes that
 * away — silently, and for the whole app at once.
 *
 * The board has a real reason to be tempted by the lock (a drag that must not
 * be mistaken for a pinch), which is why this is a test and not a comment: the
 * fix is `touch-action` on the grip, not a zoom lock, and the next person to
 * reach for the lock should fail here rather than in an audit.
 *
 * Read through the bundler so the guard needs no Node types and no new
 * dependency — the same trick egress.test.ts uses.
 */
const HTML = Object.values(
  import.meta.glob('../index.html', {
    query: '?raw',
    import: 'default',
    eager: true,
  }) as Record<string, string>,
)[0];

/** The content of `<meta name="viewport">`, or '' when there is no such tag. */
function viewportContent(html: string): string {
  const tag = html.match(/<meta\s+name=["']viewport["'][^>]*>/i)?.[0] ?? '';
  return tag.match(/content=["']([^"']*)["']/i)?.[1] ?? '';
}

describe('viewport meta', () => {
  it('is present and readable', () => {
    // A broken glob or a renamed file would make every assertion below
    // vacuously pass.
    expect(HTML).toContain('<div id="root">');
    expect(viewportContent(HTML)).toContain('width=device-width');
  });

  it('does not cap the zoom scale (WCAG 1.4.4)', () => {
    expect(viewportContent(HTML)).not.toMatch(/maximum-scale/i);
  });

  it('does not disable user scaling (WCAG 1.4.4)', () => {
    expect(viewportContent(HTML)).not.toMatch(/user-scalable\s*=\s*(no|0)/i);
  });

  it('keeps the drag gesture opt-out that made the zoom lock unnecessary', () => {
    // The grip is what a touch drag starts from; `touch-action: none` there is
    // why the board does not need to suppress zoom globally.
    const css = Object.values(
      import.meta.glob('./styles.css', {
        query: '?raw',
        import: 'default',
        eager: true,
      }) as Record<string, string>,
    )[0];
    expect(css).toMatch(/\.card\s+\.grip\s*\{[^}]*touch-action:\s*none/);
  });
});
