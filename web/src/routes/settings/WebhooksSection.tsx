import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus, Webhook, Trash2, AlertCircle, Send, RotateCcw, Eye, Copy, Check,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface EventDef { key: string; label: string }
interface EventList { items: EventDef[] }
interface Subscription {
  id: string; name: string; url: string; has_secret: boolean;
  events: string[]; is_enabled: boolean; retry_max: number; created_at: string; updated_at: string;
}
interface SubList { items: Subscription[] }
interface Delivery {
  id: string; subscription_id: string; event: string; attempt: number;
  status: 'queued'|'succeeded'|'failed'; response_code?: number; error_message?: string;
  created_at: string; delivered_at?: string;
}
interface DelList { items: Delivery[] }

export function WebhooksSection() {
  const qc = useQueryClient();
  const { data: events }      = useQuery({ queryKey: ['webhook-events'], queryFn: () => api<EventList>('/admin/webhooks/events') });
  const { data, isLoading }   = useQuery({ queryKey: ['webhooks'],       queryFn: () => api<SubList>('/admin/webhooks') });
  const { data: deliveries }  = useQuery({ queryKey: ['webhook-deliveries'], queryFn: () => api<DelList>('/admin/webhooks/deliveries?limit=25') });

  const subs = data?.items ?? [];
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [editor, setEditor] = useState<Subscription | 'new' | null>(null);
  const selected = selectedId ? subs.find((s) => s.id === selectedId) ?? null : subs[0] ?? null;

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Webhooks</CardTitle>
          <CardDescription>
            Outbound HTTP subscriptions to your own services. Payloads are signed with HMAC-SHA256;
            see the <span className="font-mono text-ink">X-Logica-Signature</span> header.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setEditor('new')}>
          <Plus className="size-3.5" /> New subscription
        </Button>
      </div>

      {isLoading ? <Card><Skeleton className="h-32 w-full" /></Card> :
       subs.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Webhook className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No webhook subscriptions yet.</div>
            <Button size="sm" className="mt-4" onClick={() => setEditor('new')}>
              <Plus className="size-3.5" /> Create one
            </Button>
          </div>
        </Card>
       ) : (
        <div className="grid grid-cols-1 lg:grid-cols-[300px_1fr] gap-4">
          <Card padded={false}>
            <ul className="divide-y divide-hairline">
              {subs.map((s) => (
                <li key={s.id}>
                  <button type="button" onClick={() => setSelectedId(s.id)}
                    className={cn('w-full text-left px-3 py-2.5 transition-colors',
                      s.id === selected?.id ? 'bg-surface' : 'hover:bg-surface-soft')}>
                    <div className="flex items-center gap-2">
                      <span className="text-body-sm font-medium text-ink truncate">{s.name}</span>
                      {!s.is_enabled && <StatusPill tone="warning" withDot={false}>Disabled</StatusPill>}
                    </div>
                    <div className="text-caption text-stone font-mono truncate">{s.url}</div>
                    <div className="text-caption text-stone">{s.events.length} events</div>
                  </button>
                </li>
              ))}
            </ul>
          </Card>

          {selected && (
            <SubDetail
              sub={selected}
              deliveries={(deliveries?.items ?? []).filter((d) => d.subscription_id === selected.id)}
              onEdit={() => setEditor(selected)}
              onChanged={() => qc.invalidateQueries({ queryKey: ['webhook-deliveries'] })}
            />
          )}
        </div>
       )}

      {editor && (
        <SubDialog
          mode={editor === 'new' ? 'new' : 'edit'}
          sub={editor === 'new' ? null : editor}
          events={events?.items ?? []}
          onClose={() => setEditor(null)}
          onSaved={() => { qc.invalidateQueries({ queryKey: ['webhooks'] }); setEditor(null); }}
        />
      )}
    </div>
  );
}

function SubDetail({
  sub, deliveries, onEdit, onChanged,
}: { sub: Subscription; deliveries: Delivery[]; onEdit: () => void; onChanged: () => void }) {
  const qc = useQueryClient();
  const test = useMutation({
    mutationFn: () => api(`/admin/webhooks/${sub.id}/test`, { method: 'POST' }),
    onSuccess: () => { onChanged(); qc.invalidateQueries({ queryKey: ['webhook-deliveries'] }); },
  });
  const del = useMutation({
    mutationFn: () => api(`/admin/webhooks/${sub.id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhooks'] }),
  });
  const replay = useMutation({
    mutationFn: (id: string) => api(`/admin/webhooks/deliveries/${id}/replay`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhook-deliveries'] }),
  });

  return (
    <Card padded={false}>
      <div className="px-5 py-4 border-b border-hairline flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>{sub.name}</CardTitle>
          <CardDescription>
            <span className="font-mono text-ink">{sub.url}</span> · retries up to {sub.retry_max}
          </CardDescription>
          <div className="mt-2 flex items-center gap-1.5 flex-wrap">
            {sub.events.map((e) => <span key={e} className="px-2 py-0.5 rounded-full bg-surface text-caption text-charcoal font-mono">{e}</span>)}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="secondary" onClick={() => test.mutate()} loading={test.isPending}>
            <Send className="size-3.5" /> Test
          </Button>
          <Button size="sm" variant="secondary" onClick={onEdit}>Edit</Button>
          <Button size="sm" variant="ghost"
            onClick={() => { if (confirm(`Delete "${sub.name}"?`)) del.mutate(); }}>
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>

      <div className="p-5">
        <div className="text-body-sm font-medium text-ink mb-2">Recent deliveries</div>
        {deliveries.length === 0 ? (
          <div className="text-caption text-stone py-3 text-center">No deliveries yet — hit Test or wait for an event.</div>
        ) : (
          <table className="w-full text-body-sm">
            <thead className="bg-surface-soft border-b border-hairline">
              <tr className="text-micro-uppercase text-stone">
                <th className="text-left  font-medium px-3 py-2">When</th>
                <th className="text-left  font-medium px-3 py-2">Event</th>
                <th className="text-left  font-medium px-3 py-2">Attempt</th>
                <th className="text-left  font-medium px-3 py-2">Status</th>
                <th className="text-right font-medium px-3 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {deliveries.map((d) => (
                <tr key={d.id} className="border-t border-hairline">
                  <td className="px-3 py-2 text-stone num">{new Date(d.created_at).toLocaleString('id-ID')}</td>
                  <td className="px-3 py-2 text-charcoal font-mono text-caption">{d.event}</td>
                  <td className="px-3 py-2 text-stone num">#{d.attempt}</td>
                  <td className="px-3 py-2">
                    {d.status === 'succeeded'
                      ? <StatusPill tone="success">{d.status} {d.response_code ? `· ${d.response_code}`:''}</StatusPill>
                      : <StatusPill tone="danger">{d.status} {d.response_code ? `· ${d.response_code}`:''}</StatusPill>}
                    {d.error_message && <div className="text-caption text-brand-error mt-1 truncate max-w-[280px]">{d.error_message}</div>}
                  </td>
                  <td className="px-3 py-2 text-right">
                    <Button size="sm" variant="ghost" onClick={() => replay.mutate(d.id)}>
                      <RotateCcw className="size-3.5" /> Replay
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </Card>
  );
}

function SubDialog({
  mode, sub, events, onClose, onSaved,
}: { mode: 'new'|'edit'; sub: Subscription | null; events: EventDef[]; onClose: () => void; onSaved: () => void }) {
  const [name, setName]           = useState(sub?.name ?? '');
  const [url, setUrl]             = useState(sub?.url ?? 'https://');
  const [retry, setRetry]         = useState(sub?.retry_max ?? 5);
  const [isEnabled, setIsEnabled] = useState(sub?.is_enabled ?? true);
  const [picked, setPicked]       = useState<Set<string>>(new Set(sub?.events ?? []));
  const [regenSecret, setRegen]   = useState(false);
  const [error, setError]         = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = {
        name, url, events: Array.from(picked), is_enabled: isEnabled, retry_max: retry,
      };
      if (mode === 'new' || regenSecret) body.secret = ''; // empty -> backend regenerates
      return mode === 'new'
        ? api('/admin/webhooks',           { method: 'POST', body })
        : api(`/admin/webhooks/${sub!.id}`,{ method: 'PUT',  body });
    },
    onSuccess: () => onSaved(),
    onError:   (e: Error) => setError(e.message),
  });

  function toggleEvent(k: string) {
    const n = new Set(picked);
    n.has(k) ? n.delete(k) : n.add(k);
    setPicked(n);
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>{mode === 'new' ? 'New webhook subscription' : `Edit ${sub?.name}`}</DialogTitle>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); if (!name.trim() || !url.trim()) return setError('Name and URL required'); save.mutate(); }}
          className="mt-4 space-y-3">
          <div className="grid sm:grid-cols-2 gap-3">
            <Field label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} required />
            </Field>
            <Field label="Retry max"><Input type="number" value={retry} onChange={(e) => setRetry(Number(e.target.value))} className="num text-right" /></Field>
          </div>
          <Field label="Endpoint URL" hint="POST requests will be sent here.">
            <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://api.yourapp.com/hooks" />
          </Field>
          <div>
            <div className="label-base mb-1.5">Events</div>
            <div className="grid grid-cols-2 gap-1.5">
              {events.map((e) => (
                <label key={e.key} className="flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
                  <input type="checkbox" className="accent-brand-green-deep" checked={picked.has(e.key)} onChange={() => toggleEvent(e.key)} />
                  <span>{e.label}</span>
                  <span className="text-caption text-stone font-mono">{e.key}</span>
                </label>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-5 flex-wrap">
            <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
              <input type="checkbox" className="accent-brand-green-deep" checked={isEnabled} onChange={(e) => setIsEnabled(e.target.checked)} />
              Enabled
            </label>
            {mode === 'edit' && (
              <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
                <input type="checkbox" className="accent-brand-green-deep" checked={regenSecret} onChange={(e) => setRegen(e.target.checked)} />
                Rotate signing secret on save
              </label>
            )}
          </div>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={save.isPending}>{mode === 'new' ? 'Create subscription' : 'Save'}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
