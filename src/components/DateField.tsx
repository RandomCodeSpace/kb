import { useEffect, useRef, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent } from 'react';

/**
 * A date in the board's wire form, YYYY-MM-DD, built without Date.toISOString
 * — that renders the UTC day, which west of Greenwich is yesterday evening.
 */
export function fmtISO(y: number, m: number, d: number): string {
  // The year pads too: the native input serializes a typed "999" as "0999-…",
  // and parseISO demands four digits — fmtISO must stay its exact inverse.
  const yy = String(y).padStart(4, '0');
  const mm = String(m + 1).padStart(2, '0');
  const dd = String(d).padStart(2, '0');
  return `${yy}-${mm}-${dd}`;
}

export function todayISO(now: Date = new Date()): string {
  return fmtISO(now.getFullYear(), now.getMonth(), now.getDate());
}

/** Parsed {y, m, d} (m is 0-based), or null for anything not YYYY-MM-DD. */
export function parseISO(iso: string): { y: number; m: number; d: number } | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!m) return null;
  const parts = { y: Number(m[1]), m: Number(m[2]) - 1, d: Number(m[3]) };
  // new Date() would silently roll 2026-02-31 into March; a due date that
  // moves itself is worse than one that is refused.
  const dt = new Date(parts.y, parts.m, parts.d);
  if (dt.getFullYear() !== parts.y || dt.getMonth() !== parts.m || dt.getDate() !== parts.d) {
    return null;
  }
  return parts;
}

/** `iso` moved by `days` (may be negative), across month/year edges. */
export function moveDays(iso: string, days: number): string {
  const p = parseISO(iso);
  if (!p) return iso;
  const dt = new Date(p.y, p.m, p.d + days);
  return fmtISO(dt.getFullYear(), dt.getMonth(), dt.getDate());
}

export interface CalendarCell {
  iso: string;
  day: number;
  inMonth: boolean;
}

/**
 * The 42 cells (six full weeks) of the month grid holding `iso`, weeks
 * starting on Monday. Always six weeks, so flipping between months never
 * changes the popover's height — a calendar that resizes while paging reads
 * as broken.
 */
export function monthGrid(iso: string): CalendarCell[] {
  const p = parseISO(iso) ?? parseISO(todayISO());
  if (!p) return [];
  const first = new Date(p.y, p.m, 1);
  // getDay is 0=Sunday; shift so Monday leads the week.
  const lead = (first.getDay() + 6) % 7;
  const cells: CalendarCell[] = [];
  for (let i = 0; i < 42; i++) {
    const dt = new Date(p.y, p.m, 1 - lead + i);
    cells.push({
      iso: fmtISO(dt.getFullYear(), dt.getMonth(), dt.getDate()),
      day: dt.getDate(),
      inMonth: dt.getMonth() === p.m,
    });
  }
  return cells;
}

/** `iso` moved by whole months, clamped to the target month's last day. */
export function moveMonths(iso: string, months: number): string {
  const p = parseISO(iso);
  if (!p) return iso;
  const lastDay = new Date(p.y, p.m + months + 1, 0).getDate();
  const target = new Date(p.y, p.m + months, Math.min(p.d, lastDay));
  return fmtISO(target.getFullYear(), target.getMonth(), target.getDate());
}

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];
const WEEKDAYS = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];

/** "July 2026" for the grid header and the trigger's accessible name. */
export function monthLabel(iso: string): string {
  const p = parseISO(iso);
  return p ? `${MONTHS[p.m]} ${p.y}` : '';
}

/** Full spoken form for a day cell: "Monday 27 July 2026". */
export function dayLabel(iso: string): string {
  const p = parseISO(iso);
  if (!p) return iso;
  const names = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
  const wd = names[new Date(p.y, p.m, p.d).getDay()];
  return `${wd} ${p.d} ${MONTHS[p.m]} ${p.y}`;
}

export interface DateFieldProps {
  inputId: string;
  /** YYYY-MM-DD, or '' for no date. */
  value: string;
  onChange: (iso: string) => void;
}

/**
 * The due-date field with kb's own calendar popover. The native panel that
 * `<input type="date">` opens is browser chrome — unthemable, and visibly a
 * different product inside this one — so the field keeps the native segmented
 * input (typing, arrow keys and the screen-reader grammar all stay the
 * browser's) while the calendar button opens this popover instead.
 *
 * Keyboard, in the popover: arrows move a day/week, PageUp/PageDown a month,
 * Enter or Space picks, Escape closes and returns to the trigger.
 */
export function DateField({
  inputId,
  value,
  onChange,
}: Readonly<DateFieldProps>) {
  const [open, setOpen] = useState(false);
  // The day the grid keys move; also decides the visible month.
  const [focusISO, setFocusISO] = useState(todayISO());
  const wrapRef = useRef<HTMLDivElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const gridRef = useRef<HTMLDivElement>(null);
  // True between opening and the first grid focus; see the roving effect.
  const enterGridRef = useRef(false);

  const close = () => {
    setOpen(false);
    btnRef.current?.focus();
  };

  const pick = (iso: string) => {
    onChange(iso);
    close();
  };

  const openPicker = () => {
    setFocusISO(parseISO(value) ? value : todayISO());
    enterGridRef.current = true;
    setOpen(true);
  };

  // Same capture-phase pair as the emoji popover: Escape must not also close
  // the card modal behind it, an outside press must not reach the backdrop.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.stopPropagation();
      close();
    };
    const onDown = (e: PointerEvent) => {
      if (wrapRef.current?.contains(e.target as Node)) return;
      e.stopPropagation();
      setOpen(false);
    };
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('pointerdown', onDown, true);
    return () => {
      window.removeEventListener('keydown', onKey, true);
      window.removeEventListener('pointerdown', onDown, true);
    };
  }, [open]);

  // Roving focus: the grid re-renders on every arrow press, so focus follows
  // the active day after the commit rather than being set in the handler —
  // but only on opening or while focus is already in the grid. The ‹ ›
  // month buttons also change focusISO, and yanking focus off them meant the
  // second Enter picked a date instead of paging again.
  useEffect(() => {
    if (!open) return;
    const grid = gridRef.current;
    if (!grid) return;
    if (!enterGridRef.current && !grid.contains(document.activeElement)) return;
    enterGridRef.current = false;
    grid.querySelector<HTMLButtonElement>(`[data-iso="${focusISO}"]`)?.focus();
  }, [open, focusISO]);

  const onGridKey = (e: ReactKeyboardEvent) => {
    const jump: Record<string, number> = {
      ArrowLeft: -1,
      ArrowRight: 1,
      ArrowUp: -7,
      ArrowDown: 7,
    };
    if (e.key !== 'PageUp' && e.key !== 'PageDown' && !(e.key in jump)) return;
    e.preventDefault();
    // The move may land in a month whose grid no longer contains the cell
    // that has focus (PageUp from Aug 12 renders a July grid ending Aug 9),
    // which unmounts it and drops focus to <body> — where the roving effect's
    // containment check would decline to follow and every further grid key
    // would go nowhere. The key came from inside the grid, so re-arm the
    // effect unconditionally; the ‹ › month buttons never pass through here
    // and keep their focus exactly as before.
    enterGridRef.current = true;
    if (e.key in jump) {
      setFocusISO((f) => moveDays(f, jump[e.key]));
    } else {
      setFocusISO((f) => moveMonths(f, e.key === 'PageUp' ? -1 : 1));
    }
  };

  const today = todayISO();

  return (
    <div className="datefield" ref={wrapRef}>
      <input
        id={inputId}
        type="date"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <button
        ref={btnRef}
        type="button"
        className="datebtn"
        aria-label={value ? `Change due date, ${dayLabel(value)}` : 'Pick a due date'}
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={() => (open ? setOpen(false) : openPicker())}
      >
        ▦
      </button>
      {open && (
        <div className="datepop" role="dialog" aria-label="Choose a due date">
          <div className="dhead">
            <button
              type="button"
              aria-label="Previous month"
              onClick={() => setFocusISO((f) => moveMonths(f, -1))}
            >
              ‹
            </button>
            {/* aria-live: paging with ‹ › keeps focus on the button, so the
                new month is otherwise never spoken. */}
            <strong aria-live="polite">{monthLabel(focusISO)}</strong>
            <button
              type="button"
              aria-label="Next month"
              onClick={() => setFocusISO((f) => moveMonths(f, 1))}
            >
              ›
            </button>
          </div>
          <div className="dgrid" ref={gridRef} onKeyDown={onGridKey}>
            {WEEKDAYS.map((w) => (
              <span key={w} className="dwd" aria-hidden="true">
                {w}
              </span>
            ))}
            {monthGrid(focusISO).map((c) => (
              <button
                key={c.iso}
                type="button"
                data-iso={c.iso}
                tabIndex={c.iso === focusISO ? 0 : -1}
                className={[
                  'dday',
                  c.inMonth ? '' : 'out',
                  c.iso === today ? 'today' : '',
                  c.iso === value ? 'sel' : '',
                ]
                  .filter(Boolean)
                  .join(' ')}
                aria-label={dayLabel(c.iso)}
                // Only the picked day carries the pressed state: stamping
                // aria-pressed={false} on all 42 cells announced every day
                // as a toggle button, which picking a date is not.
                aria-pressed={c.iso === value || undefined}
                onClick={() => pick(c.iso)}
              >
                {c.day}
              </button>
            ))}
          </div>
          <div className="dfoot">
            <button type="button" onClick={() => pick(today)}>
              Today
            </button>
            {value && (
              <button type="button" onClick={() => pick('')}>
                Clear
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
