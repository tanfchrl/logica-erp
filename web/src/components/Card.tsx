import { cn } from '@/lib/cn';

/**
 * Card — white canvas with a single hairline border. No shadow by default.
 * Add `elevate` for the rare promoted surface that needs separation.
 */
interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  padded?: boolean;
  elevate?: boolean;
}

export function Card({ className, padded = true, elevate = false, ...props }: CardProps) {
  return (
    <div
      className={cn(
        'bg-canvas border border-hairline rounded-lg',
        padded && 'p-5',
        elevate && 'shadow-card',
        className,
      )}
      {...props}
    />
  );
}

export function CardHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('mb-4 flex items-start justify-between gap-3', className)} {...props} />;
}

export function CardTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <h3 className={cn('text-heading-5 text-ink', className)} {...props} />;
}

export function CardDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <p className={cn('text-caption text-stone mt-0.5', className)} {...props} />;
}
