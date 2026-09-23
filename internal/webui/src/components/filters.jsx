import { useEffect, useState } from 'react';
import { cn } from '@/lib/utils';
import { state, STATUS_LABEL } from '@/store';
import { Button } from '@/components/ui/button';
import { Chip } from '@/components/ui/chip';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Counter } from '@/components/bits';
import { dur } from '@/lib/format';
import { QUICK, toggleQuick, activeFilters, resultCount, clearFilters, setFiltersOpen } from '@/lib/filters';
import { boardLayout } from '@/lib/board';

// The filter strip: quick filters, the active chips, the count. On phones it
// becomes a sheet behind a scrim.
export function FiltersBar() {
  const chips = activeFilters();
  const quick = [...state.quick];
  const onQuick = (next) => {
    for (const id of next) if (!state.quick.has(id)) toggleQuick(id);
    for (const id of quick) if (!next.includes(id)) toggleQuick(id);
  };
  return (
    <>
      <section id="filters" className={cn('filters flex flex-none items-center gap-2 overflow-hidden border-b border-line bg-canvas px-3', state.filtersOpen && 'open')} aria-label="Search and filters">
        <ToggleGroup type="multiple" size="sm" className="flex-none" value={quick} onValueChange={onQuick} aria-label="Quick filters">
          {QUICK.map((q) => <ToggleGroupItem key={q.id} value={q.id}>{q.label}</ToggleGroupItem>)}
        </ToggleGroup>
        <div className="flex flex-none items-center gap-1.5 empty:hidden phone:flex-wrap" aria-label="Active filters">
          {chips.map((c, i) => (c.kind === 'tag'
            ? <Chip key={c.tag} tag={c.tag} remove={c.remove} />
            : <Chip key={c.kind + i} mono={c.kind === 'token'} remove={c.remove} removeLabel={`Remove filter ${c.label}`}><span>{c.label}</span></Chip>))}
        </div>
        {chips.length > 0 && <Button variant="ghost" size="sm" className="flex-none" data-tip="Clear filters (X)" onClick={clearFilters}>Clear</Button>}
        <span className="ml-auto flex-none text-12 tabular-nums text-fg-3">{resultCount()}</span>
        <Button variant="default" className="md:hidden" onClick={() => setFiltersOpen(false)}>Done</Button>
      </section>
      <Scrim open={state.filtersOpen} onClick={() => setFiltersOpen(false)} />
    </>
  );
}
function Scrim({ open, onClick }) {
  const [mounted, setMounted] = useState(open);
  const [shown, setShown] = useState(open);
  useEffect(() => {
    if (open) {
      setMounted(true);
      const raf = requestAnimationFrame(() => setShown(true));
      return () => cancelAnimationFrame(raf);
    }
    setShown(false);
    const t = setTimeout(() => setMounted(false), dur(160));
    return () => clearTimeout(t);
  }, [open]);
  return <div className={cn('scrim', shown && 'show')} hidden={!mounted} onClick={onClick} />;
}

// The phone tab bar: one tab per visible column, following the board's scroll.
export function Segments() {
  const layout = boardLayout();
  const active = state.activeSegment || 'todo';
  const scrollTo = (status) => {
    const col = document.querySelector(`#board .col[data-status="${status}"]`);
    if (col) col.scrollIntoView({ behavior: dur(1) ? 'smooth' : 'auto', inline: 'start', block: 'nearest' });
  };
  return (
    <div id="segments" className="seg seg-grow m-2 mb-0 flex-none md:hidden" role="tablist" aria-label="Column">
      {layout.map((col) => (
        <Button key={col.status} variant="segment" size="none" role="tab" data-status={col.status} data-hue={col.status} aria-selected={active === col.status ? 'true' : 'false'} hidden={col.hidden} onClick={() => scrollTo(col.status)}>
          {STATUS_LABEL[col.status]} <Counter className="num text-fg-3" text={String(col.tasks.length)} />
        </Button>
      ))}
    </div>
  );
}

export function Banner() {
  return <div className="banner flex-none" role="status" hidden={state.online}>Connection lost. Retrying…</div>;
}
