import { Check, Sparkles, Server, AlertCircle } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { StatusPill } from '@/components/StatusPill';

export interface ComingSoonProps {
  title: string;
  description: string;
  checklist: string[];
  /**
   * Backend readiness — drives the status pill so the user (and we) know
   * whether the API is ready or needs to be built first.
   *   ready        — API exists, just needs UI
   *   partial      — partial API; some endpoints to build
   *   needs-backend— API not built yet
   */
  backend: 'ready' | 'partial' | 'needs-backend';
}

const STATUS = {
  'ready':         { tone: 'success' as const, label: 'API ready', icon: Sparkles },
  'partial':       { tone: 'warning' as const, label: 'Partial API',  icon: AlertCircle },
  'needs-backend': { tone: 'neutral' as const, label: 'Backend pending', icon: Server },
};

/**
 * Generic placeholder for a Settings section that isn't built yet. Each one
 * lists what's coming so the user can sanity-check scope, and a status pill
 * shows whether we're blocked on backend or just on UI.
 */
export function ComingSoonSection({ title, description, checklist, backend }: ComingSoonProps) {
  const status = STATUS[backend];
  return (
    <div className="space-y-4">
      <Card>
        <div className="flex items-start justify-between gap-3 mb-4">
          <div>
            <CardTitle>{title}</CardTitle>
            <CardDescription>{description}</CardDescription>
          </div>
          <StatusPill tone={status.tone}>{status.label}</StatusPill>
        </div>

        <div className="rounded-lg bg-surface-soft border border-hairline p-4">
          <div className="text-micro-uppercase text-stone mb-2">Coming in this section</div>
          <ul className="space-y-1.5">
            {checklist.map((item) => (
              <li key={item} className="flex items-start gap-2 text-body-sm text-charcoal">
                <Check className="size-4 text-stone mt-0.5 shrink-0" />
                <span>{item}</span>
              </li>
            ))}
          </ul>
        </div>
      </Card>
    </div>
  );
}
