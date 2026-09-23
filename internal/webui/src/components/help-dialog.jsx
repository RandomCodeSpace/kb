import { Dialog, DialogHeader, DialogTitle, DialogBody } from '@/components/ui/dialog';
import { Kbd } from '@/components/ui/kbd';
import { isDialogOpen, closeDialog } from '@/lib/dialogs';
import { helpSections } from '@/lib/palette';

export function HelpDialog() {
  const open = isDialogOpen('help');
  const close = () => closeDialog('help');
  return (
    <Dialog name="help" open={open} onClose={close} variant="wide" aria-labelledby="help-title">
      <DialogHeader onClose={close}><DialogTitle id="help-title">Keyboard shortcuts</DialogTitle></DialogHeader>
      <DialogBody><div className="grid grid-cols-1 gap-x-8 gap-y-4 sm:grid-cols-2">
        {open && helpSections().map((section) => (
          <section key={section.title}>
            <h3 className="label-11 mb-2">{section.title}</h3>
            <dl className="help-grid">
              {section.rows.map((row, i) => (
                <div key={i} className="contents">
                  <dt className="flex flex-wrap items-center gap-1">{row.plain ? <span className="text-12 text-fg-3">{row.plain}</span> : row.keys.map((k) => <Kbd key={k}>{k}</Kbd>)}</dt>
                  <dd className="text-13 text-fg-2">{row.name}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div></DialogBody>
    </Dialog>
  );
}
