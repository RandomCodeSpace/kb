import { cn } from '@/lib/utils';

// shadcn/ui Skeleton with kb's shimmer.
function Skeleton({ className, ...props }) {
  return <div data-slot="skeleton" aria-hidden="true" className={cn('skeleton', className)} {...props} />;
}

export { Skeleton };
