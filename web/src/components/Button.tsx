import { forwardRef } from 'react';
import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import { Loader2 } from 'lucide-react';
import { cn } from '@/lib/cn';

/**
 * Pill-shaped button. Mintlify aesthetic:
 *   primary    = BLACK pill, the dominant CTA on every surface.
 *   accent     = MINT pill, reserved for hero CTAs / featured tier — use sparingly.
 *   onDark     = WHITE pill, for placement on dark bands.
 *   secondary  = outlined transparent pill.
 *   ghost      = quiet rectangular ghost (sidebar, tertiary).
 *   accentSoft = light gray pill (rarely used).
 *   danger     = red pill for destructive actions.
 *   link       = plain underlined link styling.
 */
const button = cva(
  'inline-flex items-center justify-center gap-2 font-medium select-none whitespace-nowrap ' +
    'transition-colors duration-100 ease-gentle ' +
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-green/40 focus-visible:ring-offset-2 ' +
    'disabled:opacity-50 disabled:cursor-not-allowed',
  {
    variants: {
      variant: {
        primary:    'bg-primary text-primary-fg hover:bg-primary-pressed active:bg-primary-pressed rounded-full',
        accent:     'bg-brand-green text-ink hover:bg-brand-green-deep rounded-full',
        onDark:     'bg-white text-ink hover:bg-white/90 rounded-full',
        secondary:  'bg-transparent text-ink border border-hairline hover:bg-surface rounded-full',
        ghost:      'bg-transparent text-ink hover:bg-surface rounded-md',
        accentSoft: 'bg-surface text-ink hover:bg-hairline rounded-full',
        danger:     'bg-brand-error text-white hover:bg-brand-error/90 rounded-full',
        link:       'bg-transparent text-ink hover:underline underline-offset-4 rounded-none',
      },
      size: {
        sm:   'h-7  px-3 text-[13px]',
        md:   'h-9  px-5 text-[14px]',
        lg:   'h-11 px-6 text-[14px]',
        icon: 'h-9  w-9',
      },
    },
    defaultVariants: { variant: 'primary', size: 'md' },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof button> {
  asChild?: boolean;
  loading?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild, loading, children, disabled, ...props }, ref) => {
    if (asChild) {
      // Radix Slot requires exactly one child — pass through unchanged.
      return (
        <Slot ref={ref} className={cn(button({ variant, size }), className)} {...props}>
          {children}
        </Slot>
      );
    }
    return (
      <button
        ref={ref}
        className={cn(button({ variant, size }), className)}
        disabled={disabled ?? loading}
        {...props}
      >
        {loading ? <Loader2 className="size-4 animate-spin" /> : null}
        {children}
      </button>
    );
  },
);
Button.displayName = 'Button';
