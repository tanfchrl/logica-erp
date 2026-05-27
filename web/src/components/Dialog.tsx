import * as RD from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import { cn } from '@/lib/cn';

export const Dialog = RD.Root;
export const DialogTrigger = RD.Trigger;
export const DialogClose = RD.Close;

interface ContentProps extends React.ComponentPropsWithoutRef<typeof RD.Content> {
  hideClose?: boolean;
}

export function DialogContent({ className, children, hideClose, ...props }: ContentProps) {
  return (
    <RD.Portal>
      <RD.Overlay
        className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px] animate-fade-in"
      />
      <RD.Content
        className={cn(
          'fixed z-50 left-1/2 top-[15%] -translate-x-1/2 w-full max-w-lg',
          'surface-card p-6 animate-scale-in',
          'focus:outline-none',
          className,
        )}
        {...props}
      >
        {children}
        {!hideClose && (
          <RD.Close
            className="absolute right-4 top-4 text-text-tertiary hover:text-text-primary transition-colors p-1 rounded-md hover:bg-bg-subtle"
            aria-label="Close"
          >
            <X className="size-4" />
          </RD.Close>
        )}
      </RD.Content>
    </RD.Portal>
  );
}

export function DialogTitle(props: React.ComponentPropsWithoutRef<typeof RD.Title>) {
  return <RD.Title className="text-section-head text-text-primary mb-1" {...props} />;
}
export function DialogDescription(props: React.ComponentPropsWithoutRef<typeof RD.Description>) {
  return <RD.Description className="text-body text-text-secondary mb-4" {...props} />;
}
