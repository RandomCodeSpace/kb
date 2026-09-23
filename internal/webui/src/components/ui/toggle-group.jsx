import * as ToggleGroupPrimitive from '@radix-ui/react-toggle-group';
import { cva } from 'class-variance-authority';
import { cn } from '@/lib/utils';

// shadcn/ui ToggleGroup drawn as kb's segmented control. Every option stays a
// tab stop (no roving focus), matching the board's other segments.
const groupVariants = cva('seg', {
  variants: {
    size: { default: '', sm: 'seg-sm' },
    grow: { true: 'seg-grow', false: '' },
  },
  defaultVariants: { size: 'default', grow: false },
});

function ToggleGroup({ className, size, grow, rovingFocus = false, ...props }) {
  return <ToggleGroupPrimitive.Root data-slot="toggle-group" rovingFocus={rovingFocus} className={cn(groupVariants({ size, grow, className }))} {...props} />;
}

// hue names a status so the pressed option takes that column's colour.
function ToggleGroupItem({ className, hue, mono, ...props }) {
  return <ToggleGroupPrimitive.Item data-slot="toggle-group-item" data-hue={hue} className={cn('seg-btn focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring', mono && 'num', className)} {...props} />;
}

export { ToggleGroup, ToggleGroupItem };
