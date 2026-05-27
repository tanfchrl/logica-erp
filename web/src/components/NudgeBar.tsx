import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Sparkles, X, ChevronDown, ArrowRight } from 'lucide-react';
import { Button } from '@/components/Button';
import { cn } from '@/lib/cn';
import { useUI } from '@/store/ui';
import { getAccessToken, getActiveCompany } from '@/lib/api';

/**
 * NudgeBar — ambient suggestion bar that lives between the TopChrome and
 * each page's content. Per spec §6: one nudge at a time (highest priority),
 * "See all" expands to a list, each dismissible.
 *
 * Polls /api/agent/v1/nudges/active every 60s. The first call after a 15-
 * minute cooldown triggers a server-side evaluation; subsequent polls are
 * cheap reads.
 *
 * Clicking the CTA pre-fills the Copilot panel with the rule's cta_prompt
 * and opens it.
 */

interface Nudge {
  id: string;
  rule_id: string;
  user_id: string;
  company_id?: string;
  priority: 'low' | 'normal' | 'high' | 'urgent';
  message: string;
  cta_label?: string;
  cta_prompt?: string;
  created_at: string;
}
interface ActiveResp { items: Nudge[] }

async function agentFetch<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const co = getActiveCompany();
  if (co) headers['X-Company-Id'] = co;
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  const r = await fetch(path, {
    method: opts.method ?? 'GET', headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return t ? (JSON.parse(t) as T) : ({} as T);
}

export function NudgeBar() {
  const qc = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const openCopilotWith = useUI((s) => s.openCopilotWith);

  const { data } = useQuery({
    queryKey: ['agent-nudges-active'],
    queryFn:  () => agentFetch<ActiveResp>('/api/agent/v1/nudges/active'),
    refetchInterval: 60_000,
  });
  const dismiss = useMutation({
    mutationFn: (id: string) => agentFetch(`/api/agent/v1/nudges/${id}/dismiss`, { method: 'POST' }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['agent-nudges-active'] }); },
  });

  const items = data?.items ?? [];
  if (items.length === 0) return null;

  const top = items[0]!;
  const rest = items.slice(1);

  return (
    <div className="border-b border-hairline bg-accent/[0.04]">
      <div className="px-6 lg:px-8 py-2">
        <NudgeRow
          nudge={top}
          onDismiss={() => dismiss.mutate(top.id)}
          onCTA={() => openCopilotWith(top.cta_prompt ?? top.message)}
          dense={false}
        />

        {rest.length > 0 && (
          <div className="mt-1">
            <button
              type="button"
              onClick={() => setExpanded((e) => !e)}
              className="text-caption text-stone hover:text-ink inline-flex items-center gap-1"
            >
              <ChevronDown className={cn('size-3 transition-transform', expanded && 'rotate-180')} />
              {expanded ? 'Hide' : `+${rest.length} more`}
            </button>
            {expanded && (
              <div className="mt-2 space-y-1.5 pb-1">
                {rest.map((n) => (
                  <NudgeRow
                    key={n.id}
                    nudge={n}
                    onDismiss={() => dismiss.mutate(n.id)}
                    onCTA={() => openCopilotWith(n.cta_prompt ?? n.message)}
                    dense
                  />
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function NudgeRow({
  nudge, onDismiss, onCTA, dense,
}: {
  nudge: Nudge;
  onDismiss: () => void;
  onCTA: () => void;
  dense: boolean;
}) {
  return (
    <div className={cn(
      'flex items-center gap-3',
      dense && 'pl-2',
    )}>
      <span className={cn(
        'inline-flex items-center justify-center size-6 rounded-full shrink-0',
        priorityTone(nudge.priority),
      )}>
        <Sparkles className="size-3" />
      </span>
      <div className="text-body-sm text-ink min-w-0 flex-1 truncate">
        {nudge.message}
      </div>
      {nudge.cta_label && nudge.cta_prompt && (
        <Button size="sm" variant="secondary" onClick={onCTA}>
          {nudge.cta_label} <ArrowRight className="size-3" />
        </Button>
      )}
      <button
        type="button"
        onClick={onDismiss}
        className="text-stone hover:text-ink p-1 shrink-0"
        aria-label="Dismiss"
        title="Dismiss"
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}

function priorityTone(p: Nudge['priority']): string {
  switch (p) {
    case 'urgent': return 'bg-brand-error/15 text-brand-error';
    case 'high':   return 'bg-warning/15 text-warning';
    case 'low':    return 'bg-surface text-stone';
    default:       return 'bg-accent/15 text-accent';
  }
}
