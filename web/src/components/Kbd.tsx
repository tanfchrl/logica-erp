import { cn } from '@/lib/cn';

/** Inline keyboard shortcut hint, e.g. <Kbd>⌘K</Kbd>. */
export function Kbd({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn('kbd', className)}>{children}</span>;
}
