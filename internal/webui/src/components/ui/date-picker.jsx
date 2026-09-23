import { useRef, useState } from 'react';
import { cva } from 'class-variance-authority';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover';
import { Icon } from '@/components/icon';
import { MONTHS, DOW, isoDate, parseDay, fmtDay } from '@/lib/format';

// A date field the board draws itself: a trigger reading "14 Sep 2026" and a
// month grid in a popover. Weeks start on Monday; arrows, Home/End and
// PageUp/PageDown walk the grid.
const triggerVariants = cva(
  'flex items-center gap-2 rounded-md border border-line-2 bg-surface text-left text-fg tabular-nums transition-colors hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft disabled:cursor-default disabled:opacity-50 data-[state=open]:border-focus [&>svg]:shrink-0 [&>svg]:text-fg-3',
  {
    variants: {
      size: {
        default: 'h-8 w-full px-2.5 text-16 md:text-13 phone:h-10 coarse:h-10',
        sm: 'h-7 gap-1.5 pl-2 pr-2 text-12',
      },
    },
    defaultVariants: { size: 'default' },
  },
);

function DatePicker({ value = '', onChange, size, className, disabled, children, ...props }) {
  const [open, setOpen] = useState(false);
  const [view, setView] = useState(null); // {y, m} of the month on show
  const gridRef = useRef(null);
  const shown = fmtDay(value);
  const openTo = (o) => {
    if (o) { const cur = parseDay(value) || new Date(); setView({ y: cur.getFullYear(), m: cur.getMonth() }); setFocusIso(isoDate(cur)); }
    setOpen(o);
  };
  const [focusIso, setFocusIso] = useState('');
  const pick = (iso) => { setOpen(false); if (iso !== value) onChange(iso); };
  const move = (delta) => { const d = new Date(view.y, view.m + delta, 1); setView({ y: d.getFullYear(), m: d.getMonth() }); setFocusIso(isoDate(d)); queueMicrotask(focusCurrent); };
  const focusCurrent = () => { const b = gridRef.current && gridRef.current.querySelector('.day[tabindex="0"]'); if (b) b.focus(); };
  const onKeyDown = (e) => {
    const cur = parseDay(e.target.dataset ? e.target.dataset.iso : '');
    if (!cur) return;
    const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[e.key];
    let next = null;
    if (step) next = new Date(cur.getFullYear(), cur.getMonth(), cur.getDate() + step);
    else if (e.key === 'Home') next = new Date(cur.getFullYear(), cur.getMonth(), cur.getDate() - ((cur.getDay() + 6) % 7));
    else if (e.key === 'End') next = new Date(cur.getFullYear(), cur.getMonth(), cur.getDate() + 6 - ((cur.getDay() + 6) % 7));
    else if (e.key === 'PageUp') next = new Date(cur.getFullYear(), cur.getMonth() - 1, cur.getDate());
    else if (e.key === 'PageDown') next = new Date(cur.getFullYear(), cur.getMonth() + 1, cur.getDate());
    if (!next) return;
    e.preventDefault();
    if (next.getMonth() !== view.m || next.getFullYear() !== view.y) setView({ y: next.getFullYear(), m: next.getMonth() });
    setFocusIso(isoDate(next));
    queueMicrotask(() => { const b = gridRef.current && gridRef.current.querySelector(`.day[data-iso="${isoDate(next)}"]`); if (b) b.focus(); });
  };
  const today = isoDate(new Date());
  const days = [];
  if (view) {
    const first = new Date(view.y, view.m, 1);
    const start = new Date(view.y, view.m, 1 - ((first.getDay() + 6) % 7));
    for (let i = 0; i < 42; i++) {
      const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
      days.push({ iso: isoDate(d), day: d.getDate(), other: d.getMonth() !== view.m });
    }
  }
  return (
    <Popover open={open} onOpenChange={openTo}>
      <PopoverTrigger asChild>
        <button type="button" data-slot="date-picker" disabled={disabled} aria-haspopup="dialog" className={cn(triggerVariants({ size, className }))} {...props}>
          <Icon name="calendar" size={14} />
          <span className={cn('min-w-0 flex-1 truncate', !shown && 'text-fg-3')}>{shown || 'No date'}</span>
          {children}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-63 p-2" role="dialog" aria-label="Choose a date" onKeyDown={(e) => { if (!e.ctrlKey && !e.metaKey) e.stopPropagation(); }} onOpenAutoFocus={(e) => { e.preventDefault(); focusCurrent(); }}>
        {view && (
          <>
            <div className="cal-head">
              <Button variant="ghost" size="icon-xs" aria-label="Previous month" onClick={() => move(-1)}><Icon name="chevL" size={14} /></Button>
              <span className="cal-title">{MONTHS[view.m]} {view.y}</span>
              <Button variant="ghost" size="icon-xs" aria-label="Next month" onClick={() => move(1)}><Icon name="chevR" size={14} /></Button>
            </div>
            <div className="cal-grid" ref={gridRef} onKeyDown={onKeyDown}>
              {DOW.map((d) => <span key={d} className="dow" aria-hidden="true">{d}</span>)}
              {days.map((d) => (
                <button key={d.iso} type="button" className={cn('day num', d.other && 'is-other', d.iso === today && 'is-today')} data-iso={d.iso}
                  tabIndex={d.iso === focusIso ? 0 : -1} aria-pressed={d.iso === value ? 'true' : 'false'} aria-label={fmtDay(d.iso)}
                  onClick={() => pick(d.iso)} onMouseMove={(e) => e.currentTarget.focus()}>{d.day}</button>
              ))}
            </div>
            <div className="cal-foot">
              <Button variant="ghost" size="xs" onClick={() => pick(today)}>Today</Button>
              <span className="flex-1" />
              {value && <Button variant="ghost" size="xs" onClick={() => pick('')}>Clear</Button>}
            </div>
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}

export { DatePicker };
