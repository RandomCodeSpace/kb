import { cn } from '@/lib/utils';

// shadcn/ui Spinner: one busy ring for every request the user waits on.
// Always paired with text, so it still reads as "working" when reduced
// motion freezes it.
function Spinner({ className, ...props }) {
  return <span data-slot="spinner" role="status" aria-label="Loading" className={cn('spinner', className)} {...props} />;
}

export { Spinner };
