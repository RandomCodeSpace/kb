import { iconPaths } from '@/lib/icons';

// 16px stroke icon on the text's optical centre.
export function Icon({ name, size = 16, className }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" className={className}>
      {iconPaths(name).map((d, i) => <path key={i} d={d} />)}
    </svg>
  );
}
// The three priority bars; the CSS fills them by level.
export function PriorityIcon({ prio, label }) {
  return (
    <svg className={`prio-icon p${prio}`} width={12} height={10} viewBox="0 0 12 10" aria-label={`Priority ${label}`} role="img">
      <rect x={0} y={6} width={3} height={4} rx={1} /><rect x={4.5} y={3} width={3} height={7} rx={1} /><rect x={9} y={0} width={3} height={10} rx={1} />
    </svg>
  );
}
