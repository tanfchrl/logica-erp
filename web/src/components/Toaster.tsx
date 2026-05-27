import * as RT from '@radix-ui/react-toast';
import { create } from 'zustand';
import { CheckCircle2, AlertTriangle, XCircle, Info, X } from 'lucide-react';
import { cn } from '@/lib/cn';

type Tone = 'info' | 'success' | 'warning' | 'danger';

interface ToastItem {
  id: number;
  title: string;
  description?: string;
  tone: Tone;
  duration: number;
}

interface ToastStore {
  toasts: ToastItem[];
  push: (t: Omit<ToastItem, 'id' | 'duration'> & { duration?: number }) => void;
  dismiss: (id: number) => void;
}

let nextId = 1;
export const useToasts = create<ToastStore>((set) => ({
  toasts: [],
  push: (t) => set((s) => ({
    toasts: [...s.toasts, { id: nextId++, duration: 4500, ...t }],
  })),
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}));

/** Convenience helpers. */
export const toast = {
  info:    (title: string, description?: string) => useToasts.getState().push({ title, description, tone: 'info' }),
  success: (title: string, description?: string) => useToasts.getState().push({ title, description, tone: 'success' }),
  warning: (title: string, description?: string) => useToasts.getState().push({ title, description, tone: 'warning' }),
  error:   (title: string, description?: string) => useToasts.getState().push({ title, description, tone: 'danger', duration: 7000 }),
};

const toneStyles: Record<Tone, { icon: typeof CheckCircle2; iconClass: string; ringClass: string }> = {
  info:    { icon: Info,         iconClass: 'text-info',    ringClass: 'border-info/30' },
  success: { icon: CheckCircle2, iconClass: 'text-success', ringClass: 'border-success/30' },
  warning: { icon: AlertTriangle,iconClass: 'text-warning', ringClass: 'border-warning/30' },
  danger:  { icon: XCircle,      iconClass: 'text-danger',  ringClass: 'border-danger/30' },
};

export function Toaster() {
  const { toasts, dismiss } = useToasts();
  return (
    <RT.Provider swipeDirection="right" duration={4500}>
      {toasts.map((t) => {
        const { icon: Icon, iconClass, ringClass } = toneStyles[t.tone];
        return (
          <RT.Root
            key={t.id}
            duration={t.duration}
            onOpenChange={(open) => { if (!open) dismiss(t.id); }}
            className={cn(
              'surface-card flex items-start gap-3 pr-3 pl-4 py-3 mb-2 min-w-[320px] max-w-[420px] animate-slide-in-right',
              'border-l-2', ringClass,
            )}
          >
            <Icon className={cn('size-5 shrink-0 mt-0.5', iconClass)} />
            <div className="flex-1 min-w-0">
              <RT.Title className="text-body font-medium text-text-primary">{t.title}</RT.Title>
              {t.description && (
                <RT.Description className="text-caption text-text-secondary mt-0.5">{t.description}</RT.Description>
              )}
            </div>
            <RT.Close className="text-text-tertiary hover:text-text-primary p-0.5 rounded-md hover:bg-bg-subtle">
              <X className="size-3.5" />
            </RT.Close>
          </RT.Root>
        );
      })}
      <RT.Viewport className="fixed top-4 right-4 z-[60] flex flex-col w-[420px] max-w-[100vw] outline-none" />
    </RT.Provider>
  );
}
