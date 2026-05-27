import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Sparkles, RefreshCw, Download, AlertCircle, Info } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogTitle } from '@/components/Dialog';
import { getAccessToken } from '@/lib/api';
import { cn } from '@/lib/cn';

/**
 * AgentUsageSection — Settings → AI Usage.
 *
 * Aggregates token use + IDR cost from agent_audit_log grouped by
 * (day, user, model). System-admin only. Builds the per-client billing
 * picture so cost-justified pricing rolls up without external tooling.
 *
 * Pricing is configured via env (AGENT_PRICING_OVERRIDES_JSON +
 * AGENT_PRICING_USD_TO_IDR) — the "configured models" dialog shows the
 * effective table for transparency.
 */

interface UsageRow {
  day: string;
  user_id: string;
  user_email: string;
  model: string;
  calls: number;
  tokens_in: number;
  tokens_out: number;
  cost_idr: string;
}
interface UsageResp {
  rows: UsageRow[];
  total_calls: number;
  total_in: number;
  total_out: number;
  total_idr: string;
}
interface PricingResp {
  usd_to_idr: string;
  models: Array<{ model: string; input_per_million_usd: number; output_per_million_usd: number }>;
}

async function fetchAgent<T>(path: string): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const r = await fetch(path, { headers });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return t ? (JSON.parse(t) as T) : ({} as T);
}

function formatIDR(s: string): string {
  const n = Number(s);
  if (!Number.isFinite(n)) return s;
  return 'Rp ' + n.toLocaleString('id-ID', { minimumFractionDigits: 0, maximumFractionDigits: 2 });
}
function formatInt(n: number): string {
  return n.toLocaleString('id-ID');
}

export function AgentUsageSection() {
  const [since, setSince] = useState('');
  const [until, setUntil] = useState('');
  const [showPricing, setShowPricing] = useState(false);

  const params = useMemo(() => {
    const q = new URLSearchParams();
    if (since) q.set('since', new Date(since).toISOString());
    if (until) q.set('until', new Date(until).toISOString());
    return q.toString();
  }, [since, until]);

  const { data, isLoading, refetch, isFetching, error } = useQuery({
    queryKey: ['agent-usage', params],
    queryFn:  () => fetchAgent<UsageResp>(`/api/agent/v1/admin/usage${params ? `?${params}` : ''}`),
  });
  const { data: pricing } = useQuery({
    queryKey: ['agent-pricing'],
    queryFn:  () => fetchAgent<PricingResp>('/api/agent/v1/admin/usage/pricing'),
    staleTime: 30 * 60_000,
  });

  // Roll the rows into per-day + per-model breakdowns for the side cards.
  const { byDay, byModel, byUser } = useMemo(() => {
    const day = new Map<string, { calls: number; idr: number }>();
    const model = new Map<string, { calls: number; idr: number; in: number; out: number }>();
    const user = new Map<string, { calls: number; idr: number; email: string }>();
    for (const r of data?.rows ?? []) {
      const cost = Number(r.cost_idr);
      const d = day.get(r.day) ?? { calls: 0, idr: 0 };
      day.set(r.day, { calls: d.calls + r.calls, idr: d.idr + cost });
      const m = model.get(r.model) ?? { calls: 0, idr: 0, in: 0, out: 0 };
      model.set(r.model, { calls: m.calls + r.calls, idr: m.idr + cost, in: m.in + r.tokens_in, out: m.out + r.tokens_out });
      const uKey = r.user_email || r.user_id;
      const u = user.get(uKey) ?? { calls: 0, idr: 0, email: uKey };
      user.set(uKey, { calls: u.calls + r.calls, idr: u.idr + cost, email: uKey });
    }
    return {
      byDay: [...day.entries()].sort((a, b) => a[0].localeCompare(b[0])),
      byModel: [...model.entries()].sort((a, b) => b[1].idr - a[1].idr),
      byUser: [...user.entries()].sort((a, b) => b[1].idr - a[1].idr),
    };
  }, [data]);

  const maxDayCost = Math.max(1, ...byDay.map(([, v]) => v.idr));

  function exportCSV() {
    if (!data?.rows.length) return;
    const lines = ['day,user_email,model,calls,tokens_in,tokens_out,cost_idr'];
    for (const r of data.rows) {
      lines.push([r.day, csv(r.user_email), csv(r.model), r.calls, r.tokens_in, r.tokens_out, r.cost_idr].join(','));
    }
    const blob = new Blob([lines.join('\n')], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `agent-usage-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>
            <Sparkles className="size-4 inline mr-1.5 text-accent" />
            AI Usage &amp; Cost
          </CardTitle>
          <CardDescription>
            Token use + IDR cost from the agent audit log, grouped by day, user, and model.
            Pricing comes from a built-in catalog; override via{' '}
            <span className="font-mono">AGENT_PRICING_OVERRIDES_JSON</span>.
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="ghost" onClick={() => setShowPricing(true)}>
            <Info className="size-3.5" /> Pricing
          </Button>
          <Button size="sm" variant="secondary" onClick={exportCSV} disabled={!data?.rows.length}>
            <Download className="size-3.5" /> Export CSV
          </Button>
          <Button size="sm" variant="secondary" onClick={() => refetch()} loading={isFetching}>
            <RefreshCw className="size-3.5" /> Refresh
          </Button>
        </div>
      </div>

      {/* Window picker */}
      <Card padded={false}>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 p-3">
          <Field label="Since" hint="Defaults to last 30 days">
            <Input type="datetime-local" value={since} onChange={(e) => setSince(e.target.value)} />
          </Field>
          <Field label="Until">
            <Input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} />
          </Field>
        </div>
      </Card>

      {error && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">{(error as Error).message}</div>
        </Card>
      )}

      {/* Total + summaries */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <Card>
          <CardDescription>Total cost</CardDescription>
          <div className="mt-1 text-heading-3 num text-ink">
            {isLoading ? <Skeleton className="h-8 w-32" /> : formatIDR(data?.total_idr ?? '0')}
          </div>
        </Card>
        <Card>
          <CardDescription>Total calls</CardDescription>
          <div className="mt-1 text-heading-3 num text-ink">
            {isLoading ? <Skeleton className="h-8 w-32" /> : formatInt(data?.total_calls ?? 0)}
          </div>
        </Card>
        <Card>
          <CardDescription>Tokens (in / out)</CardDescription>
          <div className="mt-1 text-heading-3 num text-ink">
            {isLoading
              ? <Skeleton className="h-8 w-48" />
              : <>{formatInt(data?.total_in ?? 0)} <span className="text-stone">/</span> {formatInt(data?.total_out ?? 0)}</>}
          </div>
        </Card>
      </div>

      {/* Per-day mini-chart */}
      <Card>
        <CardTitle>Per day</CardTitle>
        <div className="mt-3">
          {byDay.length === 0 && !isLoading && (
            <div className="text-caption text-stone py-2">No billable activity in this window.</div>
          )}
          <ul className="space-y-1.5">
            {byDay.map(([day, v]) => (
              <li key={day} className="flex items-center gap-3 text-body-sm">
                <span className="text-stone font-mono w-24 shrink-0">{day}</span>
                <div className="flex-1 h-2 rounded-full bg-surface overflow-hidden">
                  <div
                    className="h-full bg-accent"
                    style={{ width: `${(v.idr / maxDayCost) * 100}%` }}
                  />
                </div>
                <span className="font-mono text-caption text-stone shrink-0 w-20 text-right">{v.calls}</span>
                <span className="font-mono text-caption text-ink shrink-0 w-28 text-right">{formatIDR(String(v.idr))}</span>
              </li>
            ))}
          </ul>
        </div>
      </Card>

      {/* Per-model and per-user side by side */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        <Card padded={false}>
          <div className="px-5 py-3 border-b border-hairline"><CardTitle>By model</CardTitle></div>
          <BreakdownTable
            rows={byModel.map(([k, v]) => ({ label: k, calls: v.calls, idr: v.idr, extra: `${formatInt(v.in)} / ${formatInt(v.out)}` }))}
            extraHeader="In / Out"
          />
        </Card>
        <Card padded={false}>
          <div className="px-5 py-3 border-b border-hairline"><CardTitle>By user</CardTitle></div>
          <BreakdownTable
            rows={byUser.map(([k, v]) => ({ label: k, calls: v.calls, idr: v.idr }))}
          />
        </Card>
      </div>

      <Dialog open={showPricing} onOpenChange={setShowPricing}>
        <DialogContent className="max-w-xl">
          <DialogTitle>Configured pricing</DialogTitle>
          <div className="mt-2 text-caption text-stone">
            USD → IDR: <span className="font-mono">{pricing?.usd_to_idr}</span>. Per million tokens.
          </div>
          <div className="mt-3 overflow-y-auto max-h-[420px]">
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft text-micro-uppercase text-stone">
                <tr>
                  <th className="text-left font-medium px-3 py-2">Model</th>
                  <th className="text-right font-medium px-3 py-2">Input $/M</th>
                  <th className="text-right font-medium px-3 py-2">Output $/M</th>
                </tr>
              </thead>
              <tbody>
                {(pricing?.models ?? []).map((m) => (
                  <tr key={m.model} className="border-t border-hairline">
                    <td className="px-3 py-1.5 font-mono text-caption">{m.model}</td>
                    <td className="px-3 py-1.5 text-right font-mono">{m.input_per_million_usd.toFixed(2)}</td>
                    <td className="px-3 py-1.5 text-right font-mono">{m.output_per_million_usd.toFixed(2)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="mt-3 text-caption text-stone flex items-start gap-1.5">
            <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
            <span>Unknown models (not in this table) cost zero. Add overrides via the env var to capture them.</span>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function BreakdownTable({ rows, extraHeader }: {
  rows: Array<{ label: string; calls: number; idr: number; extra?: string }>;
  extraHeader?: string;
}) {
  return (
    <div className="max-h-[280px] overflow-y-auto">
      <table className="w-full text-body-sm">
        <thead className="bg-surface-soft text-micro-uppercase text-stone">
          <tr>
            <th className="text-left font-medium px-3 py-2"></th>
            <th className="text-right font-medium px-3 py-2">Calls</th>
            {extraHeader && <th className="text-right font-medium px-3 py-2">{extraHeader}</th>}
            <th className="text-right font-medium px-3 py-2">Cost</th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr><td colSpan={extraHeader ? 4 : 3} className="text-center text-stone py-6">No data.</td></tr>
          )}
          {rows.map((r) => (
            <tr key={r.label} className="border-t border-hairline">
              <td className="px-3 py-1.5 truncate max-w-[180px]" title={r.label}>{r.label}</td>
              <td className="px-3 py-1.5 text-right font-mono text-caption">{r.calls.toLocaleString('id-ID')}</td>
              {extraHeader && <td className="px-3 py-1.5 text-right font-mono text-caption text-stone">{r.extra}</td>}
              <td className={cn('px-3 py-1.5 text-right font-mono', r.idr === 0 ? 'text-stone' : 'text-ink')}>{formatIDR(String(r.idr))}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function csv(s: string): string {
  if (s.includes(',') || s.includes('"') || s.includes('\n')) return `"${s.replace(/"/g, '""')}"`;
  return s;
}
