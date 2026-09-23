import { useMemo } from 'react';
import { cn } from '@/lib/utils';
import { renderHTML } from '@/lib/markdown';
import { toast } from '@/lib/toast';

// Rendered markdown. mode: 'block' | 'inline' | 'card'. The HTML is sanitized
// by DOMPurify in renderHTML; copy buttons on code blocks are delegated here.
export function Markdown({ text, mode = 'block', empty, className, ...props }) {
  const html = useMemo(() => renderHTML(text, mode), [text, mode]);
  if (mode === 'inline') {
    return <span className={cn('md md-inline', className)} dangerouslySetInnerHTML={{ __html: html }} {...props} />;
  }
  if (!html) {
    return <div className={cn('md', className)} {...props}>{empty ? <span className="md-empty">{empty}</span> : null}</div>;
  }
  const onClick = async (e) => {
    const btn = e.target.closest && e.target.closest('[data-copy]');
    if (!btn) return;
    e.stopPropagation();
    const pre = btn.closest('pre');
    const code = pre && (pre.querySelector('code') ? pre.querySelector('code').textContent : pre.textContent);
    try {
      await navigator.clipboard.writeText(code || '');
      const kids = Array.from(btn.childNodes);
      btn.replaceChildren('Copied');
      setTimeout(() => btn.replaceChildren(...kids), 1200);
    } catch (err) { toast('Copy failed: clipboard unavailable', 'error'); }
  };
  return <div className={cn('md', mode === 'card' && 'md-card', className)} onClick={onClick} dangerouslySetInnerHTML={{ __html: html }} {...props} />;
}
