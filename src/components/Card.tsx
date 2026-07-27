import { useEffect, useRef, useState } from 'react';
import type { Prio, Task } from '../lib/model';
import { isScoped, progress } from '../lib/model';
import { ageChip, dueChip, ymd } from '../lib/urgency';
import { CONFETTI_COLORS } from '../lib/confetti';

export interface CardProps {
  task: Task;
  ghost?: boolean;
  onTick?: (checkIdx: number, pos: { x: number; y: number }) => void;
  onEdit?: () => void;
}

const PRIO_COLOR: Record<Prio, string> = {
  1: '#ff5a48',
  2: '#ffb020',
  3: '#4f8ef7',
  4: '#b8bdc7',
};

/** Stable tag color: same hash the approved prototype used, over the shared palette. */
function tagColor(tag: string): string {
  return CONFETTI_COLORS[(tag.length + (tag.charCodeAt(0) || 0)) % CONFETTI_COLORS.length];
}

export function Card({ task, ghost, onTick, onEdit }: CardProps) {
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
  if (open) cls.push('open');
  if (ghost) cls.push('ghost');

  return (
    <div
      className={cls.join(' ')}
      data-task={task.id}
      onClick={(e) => {
        if ((e.target as Element).closest('.chev,.check,.bx,button,input')) return;
        onEdit?.();
      }}
    >
      <div className="head">
        <span className="emo">{task.emoji}</span>
        <span className="t">{task.title}</span>
        <span className="age">
          {ageChip(task.status, task.createdAt, task.movedAt, Date.now())}
        </span>
      </div>
      {task.desc !== '' && <div className="desc">{task.desc}</div>}
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
                  onTick?.(i, { x: r.left + 8, y: r.top + 8 });
                }}
              >
                <div className="bx">✓</div>
                <span>{c.text}</span>
              </div>
            ))}
          </div>
        </>
      )}
      <div className="meta">
        <span className="pdot" style={{ background: PRIO_COLOR[task.prio] }} />
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
    </div>
  );
}
