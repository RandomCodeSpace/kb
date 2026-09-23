import { cn } from '@/lib/utils';

// shadcn/ui Kbd: one key cap in the mono face.
function Kbd({ className, ...props }) {
  return <kbd data-slot="kbd" className={cn('kbd', className)} {...props} />;
}

export { Kbd };
