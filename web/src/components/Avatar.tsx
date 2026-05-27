import * as RA from '@radix-ui/react-avatar';
import { cn } from '@/lib/cn';

interface AvatarProps {
  name?: string;
  src?: string;
  size?: 'sm' | 'md' | 'lg';
  className?: string;
}

const sizes = { sm: 'size-6 text-[10px]', md: 'size-8 text-xs', lg: 'size-10 text-sm' };

export function Avatar({ name, src, size = 'md', className }: AvatarProps) {
  const initials = (name || '?')
    .split(/\s+/).filter(Boolean).slice(0, 2)
    .map((s) => s[0]?.toUpperCase()).join('');
  return (
    <RA.Root
      className={cn(
        'inline-flex items-center justify-center rounded-full overflow-hidden font-medium',
        'bg-accent-soft text-accent shrink-0',
        sizes[size],
        className,
      )}
    >
      {src ? <RA.Image src={src} alt={name || ''} className="size-full object-cover" /> : null}
      <RA.Fallback delayMs={200}>{initials}</RA.Fallback>
    </RA.Root>
  );
}
