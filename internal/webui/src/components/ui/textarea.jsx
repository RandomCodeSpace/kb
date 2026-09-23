import { cva } from 'class-variance-authority';
import { cn } from '@/lib/utils';

// shadcn/ui Textarea with kb's surfaces. No resize grip: the views size the
// field to its content.
const textareaVariants = cva(
  'flex w-full min-w-0 resize-none text-fg transition-colors placeholder:text-fg-3 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive',
  {
    variants: {
      variant: {
        default: 'min-h-16 max-h-80 rounded-md border border-line-2 bg-surface px-2.5 py-1.5 text-16 hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft md:text-13',
        code: 'min-h-16 max-h-80 rounded-md border border-line-2 bg-surface px-2.5 py-1.5 font-mono text-16 hover:border-fg-3 focus-visible:border-focus focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-accent-soft md:text-12',
        editor: 'editor-ta',
        title: 'title-edit resize-none',
        bare: 'border-0 bg-transparent p-0 outline-none focus-visible:outline-none',
      },
    },
    defaultVariants: { variant: 'default' },
  },
);

function Textarea({ className, variant, ...props }) {
  return <textarea data-slot="textarea" className={cn(textareaVariants({ variant, className }))} {...props} />;
}

export { Textarea, textareaVariants };
