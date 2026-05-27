import { forwardRef } from 'react';
import * as LabelPrimitive from '@radix-ui/react-label';
import { cn } from '@/lib/cn';

type InputProps = React.InputHTMLAttributes<HTMLInputElement>;

/** 40px-tall text input, hairline border, mint focus ring. */
export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, ...props }, ref) => (
    <input ref={ref} className={cn('input-base', className)} {...props} />
  ),
);
Input.displayName = 'Input';

export const Label = forwardRef<
  React.ElementRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(({ className, ...props }, ref) => (
  <LabelPrimitive.Root ref={ref} className={cn('label-base', className)} {...props} />
));
Label.displayName = 'Label';

export function Field({ label, hint, error, children, htmlFor }: {
  label: string; hint?: string; error?: string; children: React.ReactNode; htmlFor?: string;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {error ? <p className="text-caption text-brand-error">{error}</p>
             : hint  ? <p className="text-caption text-stone">{hint}</p> : null}
    </div>
  );
}
