import * as PopoverPrimitive from '@radix-ui/react-popover';
import { cn } from '@/lib/utils';
import { useDialogHost } from '@/components/ui/dialog';

// shadcn/ui Popover with kb's floating surface. Inside a modal dialog the
// content portals into that dialog so it stays interactive.
const Popover = PopoverPrimitive.Root;
const PopoverTrigger = PopoverPrimitive.Trigger;
const PopoverAnchor = PopoverPrimitive.Anchor;

// `panel` is the display-options sheet: wider, scrollable, no padding.
function PopoverContent({ className, align = 'start', sideOffset = 4, container, variant = 'default', ...props }) {
  const host = useDialogHost();
  return (
    <PopoverPrimitive.Portal container={container || host || undefined}>
      <PopoverPrimitive.Content
        data-slot="popover-content"
        align={align}
        sideOffset={sideOffset}
        className={cn('z-50 rounded-lg border border-line bg-overlay p-1 text-fg shadow-md outline-none', variant === 'panel' && 'display-panel', className)}
        {...props}
      />
    </PopoverPrimitive.Portal>
  );
}

export { Popover, PopoverTrigger, PopoverAnchor, PopoverContent };
