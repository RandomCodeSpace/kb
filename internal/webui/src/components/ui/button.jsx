import { Slot } from '@radix-ui/react-slot';
import { cva } from 'class-variance-authority';
import { cn } from '@/lib/utils';

// shadcn/ui Button on kb's control scale: 32px controls, 40px on touch and
// phone widths, flat fills, an 8px radius. Sizes with "icon" are square.
const buttonVariants = cva(
  'inline-flex flex-none items-center justify-center gap-1.5 whitespace-nowrap rounded-md border border-transparent text-13 font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 active:translate-y-px [&_svg]:pointer-events-none [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground hover:bg-primary/85',
        secondary: 'bg-raised text-fg hover:bg-band',
        ghost: 'bg-transparent text-fg-2 hover:bg-raised hover:text-fg',
        destructive: 'bg-raised text-danger hover:bg-danger/12',
        'ghost-destructive': 'bg-transparent text-danger hover:bg-danger/12',
        link: 'h-auto rounded-none border-0 px-0 font-normal text-link underline decoration-line-2 underline-offset-2 hover:decoration-current',
        stat: 'bg-transparent font-normal text-fg-2 hover:bg-raised hover:text-fg aria-pressed:bg-accent-soft aria-pressed:text-fg',
        segment: 'seg-btn border-0',
        'editor-tab': 'editor-tabs text-fg-2 hover:text-fg',
        toast: 'border-fg-3/40 bg-transparent text-12 text-current hover:bg-fg-3/25',
      },
      size: {
        default: 'h-8 px-2.5 phone:h-10 coarse:h-10',
        sm: 'h-7 px-2 text-12 phone:h-9 coarse:h-9',
        xs: 'h-6 rounded-sm px-1.5 text-12 phone:h-8 coarse:h-8',
        icon: 'size-8 phone:size-10 coarse:size-10',
        'icon-sm': 'size-7 phone:size-9 coarse:size-9',
        'icon-xs': 'size-6 rounded-sm phone:size-8 coarse:size-8',
        none: '',
      },
    },
    defaultVariants: { variant: 'secondary', size: 'default' },
  },
);

function Button({ className, variant, size, asChild = false, type = 'button', ...props }) {
  const Comp = asChild ? Slot : 'button';
  return <Comp data-slot="button" type={asChild ? undefined : type} className={cn(buttonVariants({ variant, size, className }))} {...props} />;
}

export { Button, buttonVariants };
