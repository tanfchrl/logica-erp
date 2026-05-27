import type { LucideIcon } from 'lucide-react';
import { cn } from '@/lib/cn';

interface EmptyStateProps {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: React.ReactNode;
  className?: string;
}

export function EmptyState({ icon: Icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div className={cn('flex flex-col items-center text-center py-12 px-6', className)}>
      {Icon && (
        <div className="mb-3 inline-flex items-center justify-center size-12 rounded-full bg-accent-soft text-accent">
          <Icon className="size-6" />
        </div>
      )}
      <h3 className="text-section-head text-text-primary mb-1">{title}</h3>
      {description && <p className="text-body text-text-secondary max-w-md mb-4">{description}</p>}
      {action}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-md bg-bg-subtle', className)} />;
}
