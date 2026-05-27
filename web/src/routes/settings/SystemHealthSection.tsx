import { useQuery } from '@tanstack/react-query';
import { Server, AlertCircle, Sparkles, RefreshCw, Webhook, Mail, ShieldCheck, Database } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface FailureRow {
  source: 'webhook' | 'email' | 'approval' | 'import';
  subject: string;
  detail?: string;
  occurred_at: string;
}
interface Summary {
  webhook_failures_24h: number;
  email_failures_24h: number;
  approval_pending_over_24h: number;
  import_errors_24h: number;
  recent_failures: FailureRow[];
}

const SOURCE_META: Record<FailureRow['source'], { label: string; icon: React.ComponentType<{ className?: string }>; tone: string }> = {
  webhook:  { label: 'Webhook',  icon: Webhook,      tone: 'text-info' },
  email:    { label: 'Email',    icon: Mail,         tone: 'text-warning' },
  approval: { label: 'Approval', icon: ShieldCheck,  tone: 'text-charcoal' },
  import:   { label: 'Import',   icon: Database,     tone: 'text-brand-error' },
};

export function SystemHealthSection() {
  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['system-health'],
    queryFn:  () => api<Summary>('/admin/system/health'),
    refetchInterval: 30_000,
  });

  if (isLoading || !data) return <Card><Skeleton className="h-64 w-full" /></Card>;

  const allClean = data.webhook_failures_24h + data.email_failures_24h + data.approval_pending_over_24h + data.import_errors_24h === 0;

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>System health</CardTitle>
          <CardDescription>
            Things ops should know about: failed deliveries, stuck approvals, import errors over the last 24h.
          </CardDescription>
        </div>
        <Button size="sm" variant="secondary" onClick={() => refetch()} loading={isFetching}>
          <RefreshCw className="size-3.5" /> Refresh
        </Button>
      </div>

      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <HealthTile label="Webhook failures (24h)"  value={data.webhook_failures_24h}      icon={Webhook}     tone={data.webhook_failures_24h ? 'danger' : 'success'} />
        <HealthTile label="Email failures (24h)"    value={data.email_failures_24h}        icon={Mail}        tone={data.email_failures_24h   ? 'danger' : 'success'} />
        <HealthTile label="Approvals stuck > 24h"   value={data.approval_pending_over_24h} icon={ShieldCheck} tone={data.approval_pending_over_24h ? 'warning' : 'success'} />
        <HealthTile label="Import errors (24h)"     value={data.import_errors_24h}         icon={Database}    tone={data.import_errors_24h    ? 'danger' : 'success'} />
      </div>

      {allClean ? (
        <Card>
          <div className="text-center py-8">
            <Sparkles className="mx-auto size-6 text-brand-green mb-2" />
            <div className="text-body-sm text-charcoal">All clear — no failures in the last 24 hours.</div>
          </div>
        </Card>
      ) : (
        <Card padded={false}>
          <div className="px-4 py-3 border-b border-hairline">
            <div className="text-body-sm font-medium text-ink">Recent failures</div>
          </div>
          <ul className="divide-y divide-hairline">
            {data.recent_failures.map((f, i) => {
              const meta = SOURCE_META[f.source];
              const Icon = meta.icon;
              return (
                <li key={i} className="px-4 py-2.5 flex items-start gap-3">
                  <span className={cn('size-7 rounded-md bg-surface inline-flex items-center justify-center shrink-0', meta.tone)}>
                    <Icon className="size-3.5" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="text-caption text-stone uppercase font-medium">{meta.label}</span>
                      <span className="text-caption text-stone num">{new Date(f.occurred_at).toLocaleString('id-ID')}</span>
                    </div>
                    <div className="text-body-sm text-ink truncate">{f.subject}</div>
                    {f.detail && <div className="text-caption text-stone truncate">{f.detail}</div>}
                  </div>
                </li>
              );
            })}
          </ul>
        </Card>
      )}
    </div>
  );
}

function HealthTile({
  label, value, icon: Icon, tone,
}: { label: string; value: number; icon: React.ComponentType<{ className?: string }>; tone: 'success' | 'warning' | 'danger' }) {
  const toneCls = tone === 'danger' ? 'text-brand-error' : tone === 'warning' ? 'text-warning' : 'text-ink';
  return (
    <div className="rounded-lg border border-hairline bg-canvas p-4">
      <div className="flex items-center justify-between">
        <div className="text-micro-uppercase text-stone">{label}</div>
        <Icon className="size-3.5 text-stone" />
      </div>
      <div className={cn('mt-1 text-heading-3 num', toneCls)}>{value.toLocaleString('id-ID')}</div>
    </div>
  );
}
