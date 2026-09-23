import * as CheckboxPrimitive from '@radix-ui/react-checkbox';
import { Check, Minus } from 'lucide-react';
import { cn } from '@/lib/utils';

// shadcn/ui Checkbox drawn as kb's 16px box: surface fill, ink when checked.
function Checkbox({ className, ...props }) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn('peer inline-grid size-4 shrink-0 cursor-pointer place-content-center rounded-[4px] border border-line-2 bg-surface transition-colors hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft disabled:cursor-default disabled:opacity-50 data-[state=checked]:border-accent data-[state=checked]:bg-accent data-[state=checked]:text-on-accent data-[state=indeterminate]:border-accent data-[state=indeterminate]:bg-accent data-[state=indeterminate]:text-on-accent', className)}
      {...props}
    >
      <CheckboxPrimitive.Indicator className="grid place-content-center text-current">
        {props.checked === 'indeterminate' ? <Minus className="size-2.5" strokeWidth={3} /> : <Check className="size-2.5" strokeWidth={3} />}
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

export { Checkbox };
