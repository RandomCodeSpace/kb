import { memo, useEffect, useRef, useState } from 'react';
import type { Prio, Task } from '../lib/model';
import { isScoped, progress } from '../lib/model';
import { ageChip, dueChip, ymd } from '../lib/urgency';
import { tagColor } from '../lib/labels';
import type { InlineTok } from '../lib/inlineMd';
import { parseDesc, tokenizeInline } from '../lib/inlineMd';

function InlineText({ toks }: { toks: InlineTok[] }) {
  return (
    <>
      {toks.map((t, i) => {
        switch (t.kind) {
          case 'code':
            return <code key={i}>{t.text}</code>;
          case 'bold':
            return <strong key={i}>{t.text}</strong>;
          case 'italic':
            return <em key={i}>{t.text}</em>;
          case 'link':
            return (
              <a
                key={i}
                href={t.href}
                target="_blank"
                rel="noopener noreferrer"
                onClick={(e) => e.stopPropagation()}
              >
                {t.text}
              </a>
            );
          default:
            return <span key={i}>{t.text}</span>;
        }
      })}
    </>
  );
}

function Desc({ text }: { text: string }) {
  return (
    <div className="desc">
      {parseDesc(text).map((l, i) => (
        <div key={i} className={l.bullet ? 'dline bullet' : 'dline'}>
          {l.bullet && <span className="bdot">•</span>}
          <span>
            <InlineText toks={l.toks} />
          </span>
        </div>
      ))}
    </div>
  );
}

/**
 * The handlers take the task id rather than closing over it, so a column can
 * hand every card the *same* function reference. That is what lets the memo
 * below actually bail out: a per-card arrow function would be new on every
 * render and re-render all of them anyway.
 */
export interface CardProps {
  task: Task;
  onTick?: (taskId: string, checkIdx: number, pos: { x: number; y: number }) => void;
  onEdit?: (taskId: string) => void;
  /** Cancelled column only: put the card back in To Do. */
  onRestore?: (taskId: string) => void;
  /** Cancelled column only: the one path to a real delete. */
  onPurge?: (taskId: string) => void;
}

const PRIO_COLOR: Record<Prio, string> = {
  1: '#ff5a48',
  2: '#ffb020',
  3: '#4f8ef7',
  4: '#b8bdc7',
};

/**
 * Memoised: a drag re-renders the board on every drop-target change, and an
 * edit re-renders it once per keystroke-committed change. Neither touches the
 * other cards' task objects, so they should not re-render at all.
 */
export const Card = memo(function Card({
  task,
  onTick,
  onEdit,
  onRestore,
  onPurge,
}: CardProps) {
  const [open, setOpen] = useState(false);
  const checksRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = checksRef.current;
    if (el) el.style.maxHeight = open ? `${el.scrollHeight}px` : '0px';
  }, [open, task.checks]);

  const p = progress(task);
  const due = task.due ? dueChip(task.due, ymd(new Date())) : null;

  const cls = ['card'];
  if (task.status === 'done') cls.push('done-card');
  if (task.status === 'cancelled') cls.push('cancelled-card');
  if (task.blocked) cls.push('blocked-card');
  if (open) cls.push('open');

  return (
    <div
      className={cls.join(' ')}
      data-task={task.id}
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
      {task.desc !== '' && <Desc text={task.desc} />}
      {p && (
        <>
          <div className="prog">
            <div className="bar">
              <i style={{ width: `${Math.round((100 * p.done) / p.total)}%` }} />
            </div>
            <span className="pct">
              {p.done}/{p.total}
            </span>
            <div
              className="chev"
              onPointerDown={(e) => {
                e.stopPropagation();
                setOpen((o) => !o);
              }}
            >
              ▾
            </div>
          </div>
          <div className="checks" ref={checksRef}>
            {task.checks.map((c, i) => (
              <div
                key={i}
                className={c.done ? 'check on' : 'check'}
                onPointerDown={(e) => {
                  e.stopPropagation();
                  const r = e.currentTarget.getBoundingClientRect();
                  onTick?.(task.id, i, { x: r.left + 8, y: r.top + 8 });
                }}
              >
                <div className="bx">✓</div>
                <span>
                  <InlineText toks={tokenizeInline(c.text)} />
                </span>
              </div>
            ))}
          </div>
        </>
      )}
      <div className="meta">
        <span className="pdot" style={{ background: PRIO_COLOR[task.prio] }} />
        {task.blocked && <span className="chip blk">⛔ blocked</span>}
        {due && (
          <span className={due.overdue ? 'chip ovd' : 'chip due'}>{due.label}</span>
        )}
        {task.effort && <span className="chip eff">{task.effort}</span>}
        {task.tags.map((tag) =>
          isScoped(tag) ? (
            <span key={tag} className="slabel">
              <span className="k">{tag.split('::')[0]}</span>
              <span className="v" style={{ background: tagColor(tag) }}>
                {tag.split('::').slice(1).join('::')}
              </span>
            </span>
          ) : (
            <span key={tag} className="tag" style={{ background: tagColor(tag) }}>
              #{tag}
            </span>
          ),
        )}
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
