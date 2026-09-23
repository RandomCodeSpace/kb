import { useEffect, useRef } from 'react';
import { clamp } from '@/lib/format';

// One floating tooltip for every [data-tip] on the page. It is a popover so it
// sits above open dialogs, and it waits 300ms like a native tip. Touch
// pointers never see it.
const POPOVER = 'showPopover' in HTMLElement.prototype;
export function TooltipHost() {
  const ref = useRef(null);
  useEffect(() => {
    const node = ref.current;
    const tip = { target: null, timer: 0 };
    const show = (target) => {
      const text = target.dataset.tip;
      clearTimeout(tip.timer);
      if (!text || matchMedia('(pointer: coarse)').matches) return;
      tip.target = target;
      tip.timer = setTimeout(() => {
        if (!target.isConnected || tip.target !== target) return;
        node.textContent = text;
        if (POPOVER) { if (!node.matches(':popover-open')) node.showPopover(); } else node.hidden = false;
        const r = target.getBoundingClientRect(), t = node.getBoundingClientRect();
        const x = clamp(r.left + r.width / 2 - t.width / 2, 8, Math.max(8, window.innerWidth - t.width - 8));
        const y = r.bottom + 6 + t.height <= window.innerHeight - 8 ? r.bottom + 6 : r.top - t.height - 6;
        node.style.left = Math.round(x) + 'px';
        node.style.top = Math.round(y) + 'px';
      }, 300);
    };
    const hide = () => {
      clearTimeout(tip.timer);
      tip.target = null;
      if (POPOVER) { if (node.matches(':popover-open')) node.hidePopover(); } else node.hidden = true;
    };
    const over = (e) => { const t = e.target.closest ? e.target.closest('[data-tip]') : null; if (!t) hide(); else if (t !== tip.target) show(t); };
    const focus = (e) => { const t = e.target.closest ? e.target.closest('[data-tip]') : null; if (t && t.matches(':focus-visible')) show(t); else hide(); };
    document.addEventListener('mouseover', over);
    document.addEventListener('focusin', focus);
    for (const type of ['mousedown', 'keydown', 'scroll', 'focusout']) document.addEventListener(type, hide, true);
    return () => {
      document.removeEventListener('mouseover', over);
      document.removeEventListener('focusin', focus);
      for (const type of ['mousedown', 'keydown', 'scroll', 'focusout']) document.removeEventListener(type, hide, true);
    };
  }, []);
  return <div ref={ref} className="tip" role="tooltip" hidden={!POPOVER} popover={POPOVER ? 'manual' : undefined} />;
}
