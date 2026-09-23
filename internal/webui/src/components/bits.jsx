import { useLayoutEffect, useRef } from 'react';
import { cn } from '@/lib/utils';
import { Chip } from '@/components/ui/chip';
import { Icon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { animate, dueChip, fmtDate, relTime } from '@/lib/format';
import { openDetail } from '@/lib/detail';

// Small presentational pieces shared by the board and the detail view.
const TONE = { soon: 'warn', overdue: 'danger' };
export function DueChip({ due, long }) {
  const d = dueChip(due);
  return <Chip tone={d.tone ? TONE[d.tone] : ''} mono icon="calendar" title={'Due ' + due}>{long ? `${due} · ${d.label}` : d.label}</Chip>;
}
export function ProgressRing({ done, total }) {
  const c = 2 * Math.PI * 5;
  return (
    <span className={cn('chip progress-ring', done === total ? 'complete' : done > 0 ? 'pending' : '')} data-tip={`${done} of ${total} checklist items done`}>
      <svg viewBox="0 0 14 14" aria-hidden="true">
        <circle className="track" cx={7} cy={7} r={5} />
        <circle className="fill" cx={7} cy={7} r={5} strokeDasharray={c.toFixed(2)} strokeDashoffset={(c * (1 - done / total)).toFixed(2)} strokeLinecap="round" />
      </svg>
      <span className="num">{done}/{total}</span>
    </span>
  );
}
// A link to another card: status dot, number, title.
export function TaskLink({ task, className, onClick }) {
  const status = task.status || 'todo';
  const inner = (
    <>
      <i className="status-dot" data-hue={status} />
      {task.seq ? <span className="seq">#{task.seq}</span> : null}
      <span className="t" data-status={status}><Markdown text={task.title} mode="inline" /></span>
    </>
  );
  if (!task.id && !task.seq) return <span className={cn('task-link', className)}>{inner}</span>;
  return (
    <button type="button" className={cn('task-link', className)} data-tip={task.seq ? `#${task.seq} ${task.title}` : `Open ${task.title}`}
      onClick={onClick || (() => openDetail(task.seq || task.id))}>{inner}</button>
  );
}
// A count that slides in when it changes.
export function Counter({ text, className, ...props }) {
  const ref = useRef(null);
  const prev = useRef(text);
  useLayoutEffect(() => {
    if (prev.current === text) return;
    prev.current = text;
    animate(ref.current, [{ transform: 'translateY(6px)', opacity: 0 }, { transform: 'none', opacity: 1 }], 160);
  }, [text]);
  return <span ref={ref} className={className} {...props}>{text}</span>;
}
export function Time({ iso, className }) {
  return <time className={cn('tabular-nums', className)} dateTime={iso} data-tip={fmtDate(iso)}>{relTime(iso)}</time>;
}
export function OpenLink({ href, children }) {
  return <a className="text-link underline decoration-line-2 underline-offset-2 hover:decoration-current" href={href} target="_blank" rel="noopener noreferrer">{children || href}<Icon name="open" size={11} /></a>;
}
export function Section({ title, extra, children }) {
  return (
    <section className="section">
      <div className="section-head"><h3 className="section-title">{title}</h3>{extra}</div>
      {children}
    </section>
  );
}
