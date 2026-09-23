import * as SwitchPrimitive from '@radix-ui/react-switch';
import { cn } from '@/lib/utils';

// shadcn/ui Switch at kb's 32x18 size.
function Switch({ className, ...props }) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn('relative inline-flex h-[18px] w-8 shrink-0 cursor-pointer items-center rounded-full bg-line-2 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-default disabled:opacity-50 data-[state=checked]:bg-accent', className)}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block size-3.5 translate-x-0.5 rounded-full bg-canvas shadow-sm transition-transform data-[state=checked]:translate-x-4" />
    </SwitchPrimitive.Root>
  );
}

export { Switch };
