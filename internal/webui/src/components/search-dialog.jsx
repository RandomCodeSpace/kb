import { useEffect, useRef } from 'react';
import { state } from '@/store';
import { Button } from '@/components/ui/button';
import { Chip } from '@/components/ui/chip';
import { Dialog } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Icon } from '@/components/icon';
import { isDialogOpen, closeDialog } from '@/lib/dialogs';
import { applySearchText, setSearchText, insertSearchToken, resultCount, SEARCH_SYNTAX } from '@/lib/filters';

// The search field lives in a centred dialog (Spotlight-style) on every device.
// Typing filters the board behind it live; Enter or Escape closes and the
// query stays applied, marked by a dot on the header icon.
export function SearchDialog() {
  const open = isDialogOpen('search');
  const ref = useRef(null);
  const timer = useRef(0);
  const focus = state.searchFocus;
  useEffect(() => {
    if (!open || !ref.current) return;
    ref.current.focus();
    ref.current.select();
  }, [open, focus]);
  const apply = () => { clearTimeout(timer.current); applySearchText(state.searchText.trim(), false); };
  const onChange = (e) => {
    setSearchText(e.target.value);
    clearTimeout(timer.current);
    timer.current = setTimeout(apply, 200);
  };
  const hasQuery = !!state.searchText.trim();
  return (
    <Dialog name="search" open={open} onClose={() => closeDialog('search')} variant="search" aria-label="Search tasks">
      <form id="search-form" noValidate onSubmit={(e) => { e.preventDefault(); apply(); closeDialog('search'); }}>
        <div className="search-box">
          <Icon name="search" size={20} className="text-fg-3" />
          <label className="sr-only" htmlFor="search">Search tasks. Supports tag:, scope:, prio:, due:, is:blocked, has:checklist, effort:, #number</label>
          <Input ref={ref} variant="search" id="search" name="q" inputMode="search" placeholder="Search tasks…" autoComplete="off" spellCheck={false} enterKeyHint="search" value={state.searchText} onChange={onChange}
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); apply(); closeDialog('search'); }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeDialog('search'); } // keep the query; a search-type input would clear it
            }} />
          {hasQuery && <Button variant="ghost" size="icon-xs" aria-label="Clear search" onClick={() => { applySearchText(''); ref.current.focus(); }}><Icon name="x" size={14} /></Button>}
          <Kbd className="hidden md:inline-flex">Esc</Kbd>
        </div>
        <div className="search-hints">
          <span className="phone-hide text-11 text-fg-3">Try</span>
          <div className="search-hint-scroll flex min-w-0 flex-1 items-center gap-1 overflow-x-auto pr-6">
            {SEARCH_SYNTAX.map(([token, label]) => (
              <Chip key={token} mono title={`${label} filter`} onMouseDown={(e) => e.preventDefault()} onClick={() => { insertSearchToken(token); ref.current.focus(); }}><span>{token}</span></Chip>
            ))}
          </div>
          <span className="flex-none text-12 tabular-nums text-fg-3">{resultCount()}</span>
        </div>
      </form>
    </Dialog>
  );
}
