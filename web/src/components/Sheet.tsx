import * as RD from '@radix-ui/react-dialog';
import { motion, AnimatePresence } from 'framer-motion';
import { X } from 'lucide-react';
import { cn } from '@/lib/cn';

interface SheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  side?: 'right' | 'left';
  width?: 'sm' | 'md' | 'lg' | 'xl';
  children: React.ReactNode;
  footer?: React.ReactNode;
  hideClose?: boolean;
}

const widths = { sm: 'w-[420px]', md: 'w-[560px]', lg: 'w-[760px]', xl: 'w-[920px]' };

export function Sheet({
  open, onOpenChange, title, description, side = 'right', width = 'md',
  children, footer, hideClose,
}: SheetProps) {
  return (
    <RD.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <RD.Portal forceMount>
            <RD.Overlay asChild>
              <motion.div
                initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
                transition={{ duration: 0.12 }}
                className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px]"
              />
            </RD.Overlay>
            <RD.Content asChild>
              <motion.div
                initial={{ x: side === 'right' ? '100%' : '-100%' }}
                animate={{ x: 0 }}
                exit={{ x: side === 'right' ? '100%' : '-100%' }}
                transition={{ duration: 0.2, ease: [0.4, 0, 0.2, 1] }}
                className={cn(
                  'fixed inset-y-0 z-50 bg-bg-surface shadow-overlay flex flex-col',
                  widths[width],
                  side === 'right' ? 'right-0 border-l border-border' : 'left-0 border-r border-border',
                )}
              >
                <header className="px-6 py-4 border-b border-border flex items-start justify-between gap-3 shrink-0">
                  <div className="min-w-0">
                    <RD.Title className="text-section-head text-text-primary">{title}</RD.Title>
                    {description && (
                      <RD.Description className="text-caption text-text-secondary mt-0.5">{description}</RD.Description>
                    )}
                  </div>
                  {!hideClose && (
                    <RD.Close
                      className="text-text-tertiary hover:text-text-primary transition-colors p-1 rounded-md hover:bg-bg-subtle"
                      aria-label="Close"
                    >
                      <X className="size-4" />
                    </RD.Close>
                  )}
                </header>
                <div className="flex-1 overflow-y-auto px-6 py-5">{children}</div>
                {footer && (
                  <footer className="px-6 py-3 border-t border-border bg-bg-app/50 shrink-0">{footer}</footer>
                )}
              </motion.div>
            </RD.Content>
          </RD.Portal>
        )}
      </AnimatePresence>
    </RD.Root>
  );
}
