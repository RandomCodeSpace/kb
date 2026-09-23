import { cva } from 'class-variance-authority';
import { cn } from '@/lib/utils';

// shadcn/ui Input on kb's 32px control with the surface fill and hairline.
const inputVariants = cva(
  'flex w-full min-w-0 text-fg transition-colors placeholder:text-fg-3 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive',
  {
    variants: {
      variant: {
        default: 'h-8 rounded-md border border-line-2 bg-surface px-2.5 text-16 hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft md:text-13 phone:h-10 coarse:h-10',
        ghost: 'h-(--control) rounded-md border border-transparent bg-transparent px-1.5 text-16 hover:border-line focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft md:text-13',
        // Inside a field that draws its own frame (the boxed label editor): the
        // control height minus that frame, so the box matches its neighbours.
        boxed: 'h-(--control-inner) border-0 bg-transparent px-0 text-16 focus-visible:outline-none md:text-13',
        'ghost-flush': 'h-(--control) rounded-md border border-transparent bg-transparent px-0 text-16 focus-visible:outline-none md:text-13',
        number: 'h-8 w-16 rounded-md border border-line-2 bg-surface px-2 text-center font-mono text-16 tabular-nums hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft md:text-13 phone:h-10 coarse:h-10',
        inline: 'h-7 rounded-md border border-line-2 bg-surface px-2 text-13 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft',
        emoji: 'h-8 rounded-md border border-line-2 bg-surface px-2 text-center text-16 hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft md:text-13 phone:h-10 coarse:h-10',
        search: 'h-10 flex-1 border-0 bg-transparent p-0 text-18 outline-none focus-visible:outline-none',
        palette: 'h-8 flex-1 border-0 bg-transparent p-0 text-14 outline-none focus-visible:outline-none',
      },
      mono: { true: 'font-mono tabular-nums', false: '' },
    },
    defaultVariants: { variant: 'default', mono: false },
  },
);

function Input({ className, variant, mono, type = 'text', ...props }) {
  return <input type={type} data-slot="input" className={cn(inputVariants({ variant, mono, className }))} {...props} />;
}

export { Input, inputVariants };
