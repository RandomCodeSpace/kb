import * as SelectPrimitive from '@radix-ui/react-select';
import { cva } from 'class-variance-authority';
import { cn } from '@/lib/utils';
import { Icon } from '@/components/icon';
import { useDialogHost } from '@/components/ui/dialog';

// shadcn/ui Select: the board draws the trigger and the list itself, so the
// picker reads the same on every OS. Inside a modal dialog the list portals
// into that dialog.
const Select = SelectPrimitive.Root;
const SelectGroup = SelectPrimitive.Group;
const SelectValue = SelectPrimitive.Value;

const triggerVariants = cva(
  'flex items-center justify-between gap-2 rounded-md border border-line-2 bg-surface text-left text-fg transition-colors hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft disabled:cursor-default disabled:opacity-50 data-[placeholder]:text-fg-3 data-[state=open]:border-focus [&>span]:min-w-0 [&>span]:truncate [&_svg]:shrink-0 [&_svg]:text-fg-3',
  {
    variants: {
      size: {
        default: 'h-8 w-full px-2.5 text-16 md:text-13 phone:h-10 coarse:h-10',
        sm: 'h-7 gap-1.5 pl-2 pr-1.5 text-12',
      },
      mono: { true: 'font-mono tabular-nums', false: '' },
    },
    defaultVariants: { size: 'default', mono: false },
  },
);

function SelectTrigger({ className, size, mono, children, ...props }) {
  return (
    <SelectPrimitive.Trigger data-slot="select-trigger" className={cn(triggerVariants({ size, mono, className }))} {...props}>
      {children}
      <SelectPrimitive.Icon asChild><Icon name="chevD" size={14} /></SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  );
}

function SelectContent({ className, children, position = 'popper', ...props }) {
  const host = useDialogHost();
  return (
    <SelectPrimitive.Portal container={host || undefined}>
      <SelectPrimitive.Content
        data-slot="select-content"
        position={position}
        onKeyDown={(e) => e.stopPropagation()}
        className={cn('relative z-50 max-h-80 min-w-[8rem] overflow-hidden rounded-lg border border-line bg-overlay p-1 text-fg shadow-md', position === 'popper' && 'data-[side=bottom]:translate-y-1 data-[side=top]:-translate-y-1', className)}
        {...props}
      >
        <SelectPrimitive.Viewport className={cn(position === 'popper' && 'w-full min-w-[var(--radix-select-trigger-width)]')}>
          {children}
        </SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  );
}

function SelectLabel({ className, ...props }) {
  return <SelectPrimitive.Label data-slot="select-label" className={cn('menu-head', className)} {...props} />;
}

function SelectItem({ className, children, ...props }) {
  return (
    <SelectPrimitive.Item data-slot="select-item" className={cn('relative flex h-8 w-full cursor-default select-none items-center gap-2 rounded-md pl-2 pr-8 text-13 text-fg outline-none data-[disabled]:pointer-events-none data-[highlighted]:bg-raised data-[state=checked]:font-medium data-[disabled]:text-fg-3', className)} {...props}>
      <SelectPrimitive.ItemText><span className="flex-1 truncate">{children}</span></SelectPrimitive.ItemText>
      <span className="absolute right-2 flex size-3.5 items-center justify-center text-fg-2">
        <SelectPrimitive.ItemIndicator><Icon name="check" size={14} /></SelectPrimitive.ItemIndicator>
      </span>
    </SelectPrimitive.Item>
  );
}

export { Select, SelectGroup, SelectValue, SelectTrigger, SelectContent, SelectLabel, SelectItem };
