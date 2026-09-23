import { useEffect, useLayoutEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { cn } from '@/lib/utils';
import { state } from '@/store';
import { Button } from '@/components/ui/button';
import { Icon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { animate } from '@/lib/format';
import { dismissToast } from '@/lib/toast';
import { topDialogElement } from '@/lib/dialogs';

const POPOVER = 'showPopover' in HTMLElement.prototype;

function Toast({ t }) {
  const ref = useRef(null);
  useEffect(() => { animate(ref.current, [{ opacity: 0, transform: 'translateY(-8px) scale(0.98)' }, { opacity: 1, transform: 'none' }], 180); }, []);
  useEffect(() => { if (t.leaving) animate(ref.current, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateY(-8px)' }], 150, { fill: 'forwards' }); }, [t.leaving]);
  return (
    <div ref={ref} className={cn('toast', t.kind)}>
      <span className="min-w-0 flex-1 truncate"><Markdown text={t.message} mode="inline" /></span>
      {t.action && <Button variant="toast" size="sm" onClick={() => { dismissToast(t.id); t.action.run(); }}>{t.action.label}</Button>}
      <Button variant="toast" size="icon-sm" aria-label="Dismiss" onClick={() => dismissToast(t.id)}><Icon name="x" size={12} /></Button>
    </div>
  );
}

// A modal dialog makes everything outside it inert, top layer included, so the
// toast stack lives inside the newest open dialog while one is up.
export function Toasts() {
  const host = topDialogElement() || document.body;
  const ref = useRef(null);
  const count = state.toasts.length;
  useLayoutEffect(() => {
    const node = ref.current;
    if (!POPOVER || !node) return;
    if (count && !node.matches(':popover-open')) node.showPopover();
    else if (!count && node.matches(':popover-open')) node.hidePopover();
  }, [count, host]);
  return createPortal(
    <div ref={ref} id="toasts" className="toasts" popover={POPOVER ? 'manual' : undefined} role="status" aria-live="polite">
      {state.toasts.map((t) => <Toast key={t.id} t={t} />)}
    </div>,
    host,
  );
}

export function LiveRegion() {
  return <div id="live" className="sr-only" aria-live="polite">{state.liveText || ''}</div>;
}
