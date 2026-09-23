import * as DropdownMenuPrimitive from '@radix-ui/react-dropdown-menu';
import { cn } from '@/lib/utils';
import { Icon } from '@/components/icon';
import { useDialogHost } from '@/components/ui/dialog';

// shadcn/ui DropdownMenu with kb's floating surface and menu rows.
const DropdownMenu = DropdownMenuPrimitive.Root;
const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger;
const DropdownMenuGroup = DropdownMenuPrimitive.Group;
const DropdownMenuRadioGroup = DropdownMenuPrimitive.RadioGroup;

function DropdownMenuContent({ className, sideOffset = 4, align = 'start', ...props }) {
  const host = useDialogHost();
  return (
    <DropdownMenuPrimitive.Portal container={host || undefined}>
      <DropdownMenuPrimitive.Content
        data-slot="dropdown-menu-content"
        sideOffset={sideOffset}
        align={align}
        onKeyDown={(e) => e.stopPropagation()}
        className={cn('z-50 min-w-50 overflow-hidden rounded-lg border border-line bg-overlay p-1 text-fg shadow-md', className)}
        {...props}
      />
    </DropdownMenuPrimitive.Portal>
  );
}

function DropdownMenuLabel({ className, ...props }) {
  return <DropdownMenuPrimitive.Label data-slot="dropdown-menu-label" className={cn('menu-head', className)} {...props} />;
}

function DropdownMenuItem({ className, ...props }) {
  return <DropdownMenuPrimitive.Item data-slot="dropdown-menu-item" className={cn('relative flex h-8 w-full cursor-default select-none items-center gap-2 rounded-md px-2 text-13 text-fg outline-none data-[disabled]:pointer-events-none data-[highlighted]:bg-raised data-[disabled]:text-fg-3', className)} {...props} />;
}

// A radio row: the tick sits in a fixed 16px slot so labels line up.
function DropdownMenuRadioItem({ className, children, ...props }) {
  return (
    <DropdownMenuPrimitive.RadioItem data-slot="dropdown-menu-radio-item" className={cn('relative flex h-8 w-full cursor-default select-none items-center gap-2 rounded-md px-2 text-13 text-fg outline-none data-[disabled]:pointer-events-none data-[highlighted]:bg-raised data-[state=checked]:font-semibold data-[disabled]:text-fg-3', className)} {...props}>
      <span className="grid size-4 place-items-center text-fg">
        <DropdownMenuPrimitive.ItemIndicator><Icon name="check" size={12} /></DropdownMenuPrimitive.ItemIndicator>
      </span>
      {children}
    </DropdownMenuPrimitive.RadioItem>
  );
}

export { DropdownMenu, DropdownMenuTrigger, DropdownMenuGroup, DropdownMenuRadioGroup, DropdownMenuContent, DropdownMenuLabel, DropdownMenuItem, DropdownMenuRadioItem };
