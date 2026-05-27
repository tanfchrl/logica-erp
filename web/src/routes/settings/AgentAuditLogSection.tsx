import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Sparkles, X, RefreshCw, Download } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Combobox } from '@/components/Combobox';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogTitle } from '@/components/Dialog';
import { getAccessToken } from '@/lib/api';
import { cn } from '@/lib/cn';

/**
 * AgentAuditLogSection — Settings → AI Audit Log.
 *
 * System-administrators-only read of agent_audit_log. Surfaces every prompt
 * / tool_call / tool_result / proposal / human_approved / human_rejected /
 * policy_blocked / error captured by the agent service. This is the trust
 * foundation per spec §8 — required to justify enabling Tier 2 in a future
 * release.
 *
 * Backend: GET /api/agent/v1/admin/audit-log (filters: user_id, session_id,
 * event_type, since, until, limit). Non-system callers get 403.
 *
 * CSV export is client-side over the currently-fetched rows. For larger
 * exports the operator can re-run with a bigger limit (capped at 500
 * server-side; for "everything since X date" they'd run multiple windows).
 */

interface Entry {
  id: string;
  session_id: string;
  user_id: string;
  user_email: string;
  user_name: string;
  company_id: string;
  turn: number;
  event_type: string;
  payload?: unknown;
  model?: string;
  tokens_in: number;
  tokens_out: number;
  latency_ms: number;
  created_at: string;
}
interface ListResp { items: Entry[] }

const EVENT_TYPES = [
  '', 'prompt', 'tool_call', 'tool_result', 'proposal',
  'human_approved', 'human_rejected', 'policy_blocked', 'error',
] as const;

const TONE: Record<string, string> = {
  prompt:          'bg-accent/10 text-accent',
  tool_call:       'bg-info/10 text-info',
  tool_result:     'bg-info/10 text-info',
  proposal:        'bg-warning/10 text-warning',
  human_approved:  'bg-brand-success/10 text-brand-success',
  human_rejected:  'bg-brand-error/10 text-brand-error',
  policy_blocked:  'bg-brand-error/10 text-brand-error',
  error:           'bg-brand-error/15 text-brand-error',
};

async function fetchAgent<T>(path: string): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const r = await fetch(path, { headers });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return t ? (JSON.parse(t) as T) : ({} as T);
}

export function AgentAuditLogSection() {
  const [userID, setUserID]       = useState('');
  const [sessionID, setSessionID] = useState('');
  const [eventType, setEventType] = useState<string>('');
  const [since, setSince]         = useState('');
  const [until, setUntil]         = useState('');
  const [limit, setLimit]         = useState(100);
  const [selected, setSelected]   = useState<Entry | null>(null);

  const params = useMemo(() => {
    const q = new URLSearchParams();
    if (userID)    q.set('user_id', userID);
    if (sessionID) q.set('session_id', sessionID);
    if (eventType) q.set('event_type', eventType);
    if (since)     q.set('since', new Date(since).toISOString());
    if (until)     q.set('until', new Date(until).toISOString());
    q.set('limit', String(limit));
    return q.toString();
  }, [userID, sessionID, eventType, since, until, limit]);

  const { data, isLoading, refetch, isFetching, error } = useQuery({
    queryKey: ['agent-audit-log', params],
    queryFn:  () => fetchAgent<ListResp>(`/api/agent/v1/admin/audit-log?${params}`),
  });

  const rows = data?.items ?? [];
  const hasFilters = !!userID || !!sessionID || !!eventType || !!since || !!until;

  function exportCSV() {
    const header = [
      'id', 'created_at', 'user_email', 'session_id', 'turn', 'event_type',
      'model', 'tokens_in', 'tokens_out', 'latency_ms',
    ];
    const lines = [header.join(',')];
    for (const e of rows) {
      lines.push([
        e.id,
        e.created_at,
        csvEscape(e.user_email || e.user_id),
        e.session_id,
        e.turn,
        e.event_type,
        e.model ?? '',
        e.tokens_in,
        e.tokens_out,
        e.latency_ms,
      ].join(','));
    }
    const blob = new Blob([lines.join('\n')], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `agent-audit-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>
            <Sparkles className="size-4 inline mr-1.5 text-accent" />
            AI Audit Log
          </CardTitle>
          <CardDescription>
            Append-only record of every agent prompt, tool call, proposal, approval,
            policy block, and error. System administrators only.
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="secondary" onClick={exportCSV} disabled={rows.length === 0}>
            <Download className="size-3.5" /> Export CSV
          </Button>
          <Button size="sm" variant="secondary" onClick={() => refetch()} loading={isFetching}>
            <RefreshCw className="size-3.5" /> Refresh
          </Button>
        </div>
      </div>

      <Card padded={false}>
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-3 p-3">
          <Field label="User ID or email substring">
            <Input value={userID} onChange={(e) => setUserID(e.target.value)} placeholder="usr_..." />
          </Field>
          <Field label="Session ID">
            <Input value={sessionID} onChange={(e) => setSessionID(e.target.value)} placeholder="ses_..." />
          </Field>
          <Field label="Event type">
            <Combobox
              options={EVENT_TYPES.map((t) => ({ value: t, label: t || '(any)' }))}
              value={eventType}
              onChange={(v) => setEventType(v ?? '')}
              placeholder="(any)"
            />
          </Field>
          <Field label="Since">
            <Input type="datetime-local" value={since} onChange={(e) => setSince(e.target.value)} />
          </Field>
          <Field label="Until">
            <Input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} />
          </Field>
          <Field label="Limit">
            <Input
              type="number"
              min={1}
              max={500}
              value={limit}
              onChange={(e) => setLimit(Math.max(1, Math.min(500, parseInt(e.target.value || '100', 10))))}
            />
          </Field>
        </div>
        {hasFilters && (
          <div className="px-3 pb-3">
            <button
              type="button"
              onClick={() => { setUserID(''); setSessionID(''); setEventType(''); setSince(''); setUntil(''); }}
              className="text-caption text-stone hover:text-ink inline-flex items-center gap-1"
            >
              <X className="size-3" /> Clear filters
            </button>
          </div>
        )}
      </Card>

      {error && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">{(error as Error).message}</div>
        </Card>
      )}

      <Card padded={false}>
        <div className="overflow-x-auto">
          <table className="w-full text-body-sm">
            <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone">
              <tr>
                <th className="text-left font-medium px-3 py-2">When</th>
                <th className="text-left font-medium px-3 py-2">User</th>
                <th className="text-left font-medium px-3 py-2">Session</th>
                <th className="text-left font-medium px-3 py-2">Turn</th>
                <th className="text-left font-medium px-3 py-2">Event</th>
                <th className="text-left font-medium px-3 py-2">Model</th>
                <th className="text-right font-medium px-3 py-2">In / Out</th>
                <th className="text-right font-medium px-3 py-2">Latency</th>
              </tr>
            </thead>
            <tbody>
              {isLoading && Array.from({ length: 4 }).map((_, i) => (
                <tr key={i} className="border-t border-hairline">
                  <td colSpan={8} className="px-3 py-2"><Skeleton className="h-5 w-full" /></td>
                </tr>
              ))}
              {!isLoading && rows.length === 0 && (
                <tr><td colSpan={8} className="text-center text-stone py-10">No matching audit entries.</td></tr>
              )}
              {!isLoading && rows.map((e) => (
                <tr
                  key={e.id}
                  onClick={() => setSelected(e)}
                  className="border-t border-hairline cursor-pointer hover:bg-surface-soft"
                >
                  <td className="px-3 py-1.5 text-caption text-charcoal">{relativeTime(e.created_at)}</td>
                  <td className="px-3 py-1.5 truncate max-w-[180px]" title={e.user_email}>{e.user_email || e.user_id}</td>
                  <td className="px-3 py-1.5 font-mono text-caption truncate max-w-[140px]" title={e.session_id}>{e.session_id.slice(0, 16)}…</td>
                  <td className="px-3 py-1.5 font-mono text-caption text-stone">{e.turn}</td>
                  <td className="px-3 py-1.5">
                    <span className={cn(
                      'inline-flex items-center px-2 py-0.5 rounded-full text-caption font-medium',
                      TONE[e.event_type] ?? 'bg-stone/10 text-stone',
                    )}>{e.event_type}</span>
                  </td>
                  <td className="px-3 py-1.5 text-caption text-stone truncate max-w-[160px]">{e.model || '—'}</td>
                  <td className="px-3 py-1.5 text-right font-mono text-caption">{e.tokens_in}/{e.tokens_out}</td>
                  <td className="px-3 py-1.5 text-right font-mono text-caption text-stone">{e.latency_ms}ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(null)}>
        <DialogContent className="max-w-2xl">
          <DialogTitle>Audit entry</DialogTitle>
          {selected && (
            <div className="mt-3 space-y-3 text-body-sm">
              <Row label="Event"      value={selected.event_type} mono />
              <Row label="When"       value={new Date(selected.created_at).toISOString()} mono />
              <Row label="User"       value={`${selected.user_email || '—'} (${selected.user_id})`} />
              <Row label="Session"    value={selected.session_id} mono />
              <Row label="Turn"       value={String(selected.turn)} mono />
              <Row label="Model"      value={selected.model || '—'} />
              <Row label="Tokens"     value={`${selected.tokens_in} in / ${selected.tokens_out} out`} mono />
              <Row label="Latency"    value={`${selected.latency_ms}ms`} mono />
              {selected.payload !== undefined && selected.payload !== null && (
                <div>
                  <div className="text-caption text-stone mb-1">Payload</div>
                  <pre className="text-caption text-charcoal bg-surface-soft rounded-md px-2 py-2 overflow-auto max-h-[400px]">
{JSON.stringify(selected.payload, null, 2)}
                  </pre>
                </div>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline gap-3">
      <span className="text-stone shrink-0 min-w-[110px]">{label}</span>
      <span className={cn('text-ink break-all', mono && 'font-mono text-caption')}>{value}</span>
    </div>
  );
}

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return '';
  const sec = Math.round((Date.now() - t) / 1000);
  if (sec < 60) return 'just now';
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.round(hr / 24);
  if (d < 30) return `${d}d ago`;
  return new Date(iso).toISOString().slice(0, 10);
}

function csvEscape(s: string): string {
  if (s.includes(',') || s.includes('"') || s.includes('\n')) {
    return `"${s.replace(/"/g, '""')}"`;
  }
  return s;
}
