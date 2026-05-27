import * as RT from '@radix-ui/react-tooltip';
import { cn } from '@/lib/cn';

export const TooltipProvider = ({ children }: { children: React.ReactNode }) => (
  <RT.Provider delayDuration={250} skipDelayDuration={150}>{children}</RT.Provider>
);

interface TooltipProps {
  content: React.ReactNode;
  side?: 'top' | 'right' | 'bottom' | 'left';
  children: React.ReactNode;
}

export function Tooltip({ content, side = 'top', children }: TooltipProps) {
  return (
    <RT.Root>
      <RT.Trigger asChild>{children}</RT.Trigger>
      <RT.Portal>
        <RT.Content
          side={side}
          sideOffset={6}
          className={cn(
            'z-50 rounded-md bg-text-primary text-bg-surface',
            'px-2 py-1 text-caption font-medium animate-fade-in',
            'shadow-overlay',
          )}
        >
          {content}
          <RT.Arrow className="fill-text-primary" />
        </RT.Content>
      </RT.Portal>
    </RT.Root>
  );
}
