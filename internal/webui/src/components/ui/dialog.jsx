import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { cva } from 'class-variance-authority';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Icon } from '@/components/icon';
import { animate, dur } from '@/lib/format';
import { dialogEls } from '@/lib/dialogs';

// A modal on the native <dialog> element: the top layer stacks nested
// dialogs and makes everything else inert. Popovers opened from inside one
// portal into it through DialogHost.
const DialogHost = createContext(null);
export const useDialogHost = () => useContext(DialogHost);

const dialogVariants = cva('dialog', {
  variants: {
    variant: {
      default: '',
      wide: 'dialog-wide',
      sheet: 'dialog-wide sheet',
      detail: 'dialog-detail sheet',
      overlay: 'mt-overlay',
      search: 'search-dialog mt-overlay',
    },
  },
  defaultVariants: { variant: 'default' },
});

let layerSeq = 0;
function Dialog({ name, open, onClose, variant, className, children, ...props }) {
  const ref = useRef(null);
  const [host, setHost] = useState(null);
  const closeRun = useRef(0);
  useLayoutEffect(() => {
    const el = ref.current;
    dialogEls[name] = el;
    setHost(el);
    return () => { if (dialogEls[name] === el) delete dialogEls[name]; };
  }, [name]);
  useEffect(() => {
    const el = ref.current;
    if (open) {
      closeRun.current++;
      el.inert = false;
      el.style.pointerEvents = '';
      if (el.open) return undefined;
      el.showModal();
      el.dataset.layer = String(++layerSeq);
      animate(el, [{ opacity: 0, transform: 'translateY(6px) scale(0.99)' }, { opacity: 1, transform: 'none' }], 160);
      return undefined;
    }
    if (!el.open) return undefined;
    const run = ++closeRun.current;
    // While it fades the dialog is inert as well as untouchable, so a click
    // aimed at the board behind it is not swallowed by the still-modal layer.
    const finish = () => { if (run === closeRun.current && el.open) el.close(); el.style.pointerEvents = ''; el.inert = false; };
    if (dur(160)) {
      el.style.pointerEvents = 'none';
      el.inert = true;
      animate(el, [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateY(4px) scale(0.99)' }], 120).finished.then(finish, finish);
    } else finish();
    return undefined;
  }, [open]);
  const onCancel = (e) => { e.preventDefault(); onClose(); };
  const onClick = (e) => { if (e.target === ref.current) onClose(); };
  // A close that did not come from the store (a form with method=dialog, a
  // browser gesture) still has to reach the owner.
  const onNativeClose = () => { if (open) onClose(); };
  return (
    <dialog ref={ref} id={`${name}-dialog`} data-slot="dialog" className={cn(dialogVariants({ variant, className }))} onCancel={onCancel} onClick={onClick} onClose={onNativeClose} {...props}>
      <DialogHost.Provider value={host}>{children}</DialogHost.Provider>
    </dialog>
  );
}

function DialogHeader({ className, children, onClose, closeLabel = 'Close', ...props }) {
  return (
    <header data-slot="dialog-header" className={cn('dialog-head', className)} {...props}>
      {children}
      {onClose && (
        <Button variant="ghost" size="icon" aria-label={closeLabel} onClick={onClose}><Icon name="x" /></Button>
      )}
    </header>
  );
}
function DialogTitle({ className, ...props }) {
  return <h2 data-slot="dialog-title" className={className} {...props} />;
}
function DialogBody({ className, ...props }) {
  return <div data-slot="dialog-body" className={cn('dialog-body', className)} {...props} />;
}
function DialogFooter({ className, ...props }) {
  return <footer data-slot="dialog-footer" className={cn('dialog-foot', className)} {...props} />;
}
// Full-height column for sheets whose body scrolls between a fixed head and foot.
function DialogColumn({ className, ...props }) {
  return <div data-slot="dialog-column" className={cn('sheet-col', className)} {...props} />;
}

export { Dialog, DialogHeader, DialogTitle, DialogBody, DialogFooter, DialogColumn, dialogVariants };
