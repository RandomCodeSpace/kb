import * as ProgressPrimitive from '@radix-ui/react-progress';
import { cn } from '@/lib/utils';

// shadcn/ui Progress as kb's 4px track with the Done green fill.
function Progress({ className, value = 0, max = 100, ...props }) {
  const pct = max > 0 ? Math.round((Math.min(value, max) / max) * 100) : 0;
  return (
    <ProgressPrimitive.Root data-slot="progress" className={cn('h-1 w-full overflow-hidden rounded-sm bg-line', className)} value={value} max={max} {...props}>
      <ProgressPrimitive.Indicator className="block h-full bg-done transition-[width]" style={{ width: `${pct}%` }} />
    </ProgressPrimitive.Root>
  );
}

export { Progress };
