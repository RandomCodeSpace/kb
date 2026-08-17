import { Fragment, memo, useEffect, useId, useRef, useState } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent } from 'react';
import type { Prio, Task } from '../lib/model';
import { cardLabel, isScoped, progress } from '../lib/model';
import { ageChip, dueChip, ymd } from '../lib/urgency';
import { tagColor } from '../lib/labels';
import { tokenizeInline } from '../lib/inlineMd';
import { InlineText, RichDesc } from './RichText';

/**
 * The handlers take the task id rather than closing over it, so a column can
 * hand every card the *same* function reference. That is what lets the memo
 * below actually bail out: a per-card arrow function would be new on every
 * render and re-render all of them anyway.
 */
export interface CardProps {
  task: Task;
  /** Slot in its column and the column's size, for the accessible name. */
  index?: number;
  total?: number;
  /** Whether the keyboard has this card picked up. */
  lifted?: boolean;
  /** Element describing the keyboard move model (see BoardView). */
  keysHintId?: string;
  onTick?: (taskId: string, checkIdx: number, pos: { x: number; y: number }) => void;
  onEdit?: (taskId: string) => void;
  /** Space/Enter/arrows/Escape on the card itself; see BoardView. */
  onCardKey?: (taskId: string, e: ReactKeyboardEvent<HTMLElement>) => void;
  /** Cancelled column only: put the card back in To Do. */
  onRestore?: (taskId: string) => void;
  /** Cancelled column only: the one path to a real delete. */
  onPurge?: (taskId: string) => void;
  /** Click on a label chip: toggle it into the board filter. */
  onTagClick?: (tag: string) => void;
}

const PRIO_COLOR: Record<Prio, string> = {
  1: '#ff5a48',
  2: '#ffb020',
  3: '#4f8ef7',
  4: '#b8bdc7',
};

/** Priority is a colour on screen; this is what it is called out loud. */
const PRIO_NAME: Record<Prio, string> = {
  1: 'urgent',
  2: 'high',
  3: 'normal',
  4: 'low',
};

/**
 * Memoised: a drag re-renders the board on every drop-target change, and an
 * edit re-renders it once per keystroke-committed change. Neither touches the
 * other cards' task objects, so they should not re-render at all.
 */
export const Card = memo(function Card({
  task,
  index = 0,
  total = 1,
  lifted = false,
  keysHintId,
  onTick,
  onEdit,
  onCardKey,
  onRestore,
  onPurge,
  onTagClick,
}: CardProps) {
  const [open, setOpen] = useState(false);
  const checksRef = useRef<HTMLDivElement>(null);
  const checksId = useId();

  useEffect(() => {
    const el = checksRef.current;
    if (!el) return;
    if (!open) {
      el.style.maxHeight = '0px';
      return;
    }
    // The open height is a measurement, and a measurement goes stale: a
    // narrower viewport, a zoom step or a rotation re-wraps the items, and a
    // panel still pinned to the height they had at the old width clips the
    // overflow with no scrollbar to get it back.
    const pin = () => {
      el.style.maxHeight = `${el.scrollHeight}px`;
    };
    pin();
    window.addEventListener('resize', pin);
    return () => window.removeEventListener('resize', pin);
  }, [open, task.checks]);

  const p = progress(task);
  const due = task.due ? dueChip(task.due, ymd(new Date())) : null;

  const cls = ['card'];
  if (task.status === 'done') cls.push('done-card');
  if (task.status === 'cancelled') cls.push('cancelled-card');
  if (task.blocked) cls.push('blocked-card');
  if (open) cls.push('open');
  if (lifted) cls.push('lifted');

  return (
    // A group rather than a button: the card is a container of its own
    // controls (links, the chevron, checklist items, Restore/Delete), which a
    // widget role may not own. tabindex + a name + a roledescription make it a
    // real, findable, keyboard-operable object all the same.
    <div
      className={cls.join(' ')}
      data-task={task.id}
      role="group"
      aria-roledescription="draggable card"
      aria-label={cardLabel(task, index, total, lifted)}
      aria-describedby={keysHintId}
      aria-keyshortcuts="Space Enter Escape"
      tabIndex={0}
      onKeyDown={(e) => onCardKey?.(task.id, e)}
      onClick={(e) => {
        if ((e.target as Element).closest('.chev,.check,.bx,.grip,button,input'))
          return;
        onEdit?.(task.id);
      }}
    >
      <div className="head">
        <span className="emo">{task.emoji}</span>
        <span className="t">
          <InlineText toks={tokenizeInline(task.title)} />
        </span>
        {/* The number is in the group's aria-label (cardLabel); the visible
            chip is decoration to a screen reader. */}
        {task.seq !== undefined && (
          <span className="seq" aria-hidden="true" title={`Task #${task.seq}`}>
            #{task.seq}
          </span>
        )}
        <span className="age">
          {ageChip(task.status, task.createdAt, task.movedAt, Date.now())}
        </span>
        {/* Drag handle. The card body stays pannable so a finger can scroll
            the column; this opts out of that (touch-action: none) so a touch
            drag reorders instead of scrolling. */}
        <span className="grip" aria-hidden="true" title="Drag to move">
          ⠿
        </span>
      </div>
      {task.desc !== '' && <RichDesc text={task.desc} />}
      {p && (
        <>
          <div className="prog">
            <div
              className="bar"
              role="progressbar"
              aria-label="Checklist progress"
              aria-valuemin={0}
              aria-valuemax={p.total}
              aria-valuenow={p.done}
              aria-valuetext={`${p.done} of ${p.total} items done`}
            >
              <i style={{ width: `${Math.round((100 * p.done) / p.total)}%` }} />
            </div>
            {/* The same fraction the progressbar already carries. */}
            <span className="pct" aria-hidden="true">
              {p.done}/{p.total}
            </span>
            <button
              type="button"
              className="chev"
              aria-expanded={open}
              aria-controls={checksId}
              aria-label={open ? 'Collapse checklist' : 'Expand checklist'}
              onPointerDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation();
                setOpen((o) => !o);
              }}
            >
              ▾
            </button>
          </div>
          {/* Collapsed is `max-height: 0`, which hides it visually but leaves
              it readable and tabbable — `inert` takes it out of both. */}
          <div
            className="checks"
            id={checksId}
            ref={checksRef}
            role="group"
            aria-label={`Checklist, ${p.done} of ${p.total} done`}
            inert={!open}
            aria-hidden={open ? undefined : true}
          >
            {task.checks.map((c, i) => (
              <div
                key={i}
                className={c.done ? 'check on' : 'check'}
                role="checkbox"
                aria-checked={c.done}
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key !== ' ' && e.key !== 'Enter') return;
                  e.preventDefault();
                  e.stopPropagation();
                  const r = e.currentTarget.getBoundingClientRect();
                  onTick?.(task.id, i, { x: r.left + 8, y: r.top + 8 });
                }}
                onPointerDown={(e) => {
                  e.stopPropagation();
                  const r = e.currentTarget.getBoundingClientRect();
                  onTick?.(task.id, i, { x: r.left + 8, y: r.top + 8 });
                }}
              >
                {/* The glyph is the checked state drawn; aria-checked says it. */}
                <div className="bx" aria-hidden="true">
                  ✓
                </div>
                <span>
                  <InlineText toks={tokenizeInline(c.text)} />
                </span>
              </div>
            ))}
          </div>
        </>
      )}
      <div className="meta">
        {/* Colour alone said nothing to a screen reader; the name and the
            tooltip carry the level in words. */}
        <span
          className="pdot"
          role="img"
          aria-label={`Priority ${task.prio} · ${PRIO_NAME[task.prio]}`}
          title={`Priority ${task.prio} · ${PRIO_NAME[task.prio]}`}
          data-prio={task.prio}
          style={{ background: PRIO_COLOR[task.prio] }}
        />
        {task.blocked && <span className="chip blk">⛔ blocked</span>}
        {due && (
          <span className={due.overdue ? 'chip ovd' : 'chip due'}>{due.label}</span>
        )}
        {task.effort && (
          <span
            className="chip eff"
            role="img"
            aria-label={`Effort ${task.effort}`}
            title={`Effort ${task.effort}`}
          >
            {task.effort}
          </span>
        )}
        {task.tags.map((tag) => {
          const chip = isScoped(tag) ? (
            // The two halves are one label; read apart they are two unrelated
            // words, so the pill names itself and its pieces stay decoration.
            <span
              className="slabel"
              role="img"
              aria-label={`Label ${tag.split('::')[0]}: ${tag.split('::').slice(1).join('::')}`}
            >
              <span className="k">{tag.split('::')[0]}</span>
              <span className="v" style={{ background: tagColor(tag) }}>
                {tag.split('::').slice(1).join('::')}
              </span>
            </span>
          ) : (
            <span
              className="tag"
              role="img"
              aria-label={`Label ${tag}`}
              style={{ background: tagColor(tag) }}
            >
              #{tag}
            </span>
          );
          // Buttons are excluded from the card's open-editor click, so a
          // label press filters the board instead of opening the card.
          return onTagClick ? (
            <button
              key={tag}
              type="button"
              className="tagbtn"
              aria-label={`Filter by label ${tag}`}
              onClick={() => onTagClick(tag)}
            >
              {chip}
            </button>
          ) : (
            <Fragment key={tag}>{chip}</Fragment>
          );
        })}
      </div>
      {(onRestore || onPurge) && (
        <div className="cardacts">
          {onRestore && (
            <button type="button" onClick={() => onRestore(task.id)}>
              Restore
            </button>
          )}
          {onPurge && (
            <button type="button" className="purge" onClick={() => onPurge(task.id)}>
              Delete permanently
            </button>
          )}
        </div>
      )}
    </div>
  );
});
