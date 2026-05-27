import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ScrollText, X, RefreshCw, Search } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface Entry {
  id: string;
  doctype: string;
  document_id: string;
  action: string;
  changed_by: string;
  user_email: string;
  user_name: string;
  changed_at: string;
  diff?: unknown;
}
interface EntryList { items: Entry[] }
interface Facets    { doctypes: string[]; actions: string[] }

const ACTION_TONES: Record<string, string> = {
  create: 'bg-success/10 text-success',
  update: 'bg-info/10 text-info',
  submit: 'bg-brand-green-soft/40 text-brand-green-deep',
  cancel: 'bg-warning/10 text-warning',
  amend:  'bg-info/10 text-info',
  delete: 'bg-brand-error/10 text-brand-error',
};

export function AuditLogSection() {
  const [doctype, setDoctype]     = useState('');
  const [action, setAction]       = useState('');
  const [docId, setDocId]         = useState('');
  const [limit, setLimit]         = useState(100);
  const [selected, setSelected]   = useState<Entry | null>(null);

  const { data: facets } = useQuery({
    queryKey: ['audit-facets'],
    queryFn: () => api<Facets>('/admin/audit-log/facets'),
  });

  const params = useMemo(() => {
    const q = new URLSearchParams();
    if (doctype) q.set('doctype', doctype);
    if (action)  q.set('action', action);
    if (docId)   q.set('document_id', docId);
    q.set('limit', String(limit));
    return q.toString();
  }, [doctype, action, docId, limit]);

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['audit-log', params],
    queryFn: () => api<EntryList>(`/admin/audit-log?${params}`),
  });

  const rows = data?.items ?? [];
  const hasFilters = !!doctype || !!action || !!docId;

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Audit log</CardTitle>
          <CardDescription>
            Immutable record of every document create / update / submit / cancel / delete.
          </CardDescription>
        </div>
        <Button size="sm" variant="secondary" onClick={() => refetch()} loading={isFetching}>
          <RefreshCw className="size-3.5" /> Refresh
        </Button>
      </div>

      {/* Filters */}
      <Card padded={false}>
        <div className="flex flex-wrap items-end gap-3 p-3">
          <FilterSelect
            label="Doctype"
            value={doctype}
            onChange={setDoctype}
            options={[{ value: '', label: 'All doctypes' },
              ...((facets?.doctypes ?? []).map((d) => ({ value: d, label: d })))]}
          />
          <FilterSelect
            label="Action"
            value={action}
            onChange={setAction}
            options={[{ value: '', label: 'All actions' },
              ...((facets?.actions ?? []).map((a) => ({ value: a, label: a })))]}
          />
          <div className="flex flex-col gap-1 min-w-[220px]">
            <div className="text-micro-uppercase text-stone">Document ID</div>
            <div className="relative">
              <Search className="size-3.5 text-stone absolute left-2.5 top-1/2 -translate-y-1/2" />
              <Input className="!h-8 !text-[13px] pl-7 font-mono"
                value={docId} onChange={(e) => setDocId(e.target.value)}
                placeholder="si_01HX… or partial"
              />
            </div>
          </div>
          <FilterSelect
            label="Limit"
            value={String(limit)}
            onChange={(v) => setLimit(Number(v))}
            options={[
              { value: '50',  label: '50 rows' },
              { value: '100', label: '100 rows' },
              { value: '250', label: '250 rows' },
              { value: '500', label: '500 rows' },
            ]}
          />
          {hasFilters && (
            <Button variant="ghost" size="sm"
              onClick={() => { setDoctype(''); setAction(''); setDocId(''); }}>
              <X className="size-3.5" /> Clear
            </Button>
          )}
        </div>
      </Card>

      {/* Table */}
      {isLoading ? (
        <Card><Skeleton className="h-64 w-full" /></Card>
      ) : rows.length === 0 ? (
        <Card>
          <div className="text-center py-10 text-stone">
            <ScrollText className="mx-auto size-6 mb-2" />
            <div className="text-body-sm text-charcoal">No audit entries match.</div>
            {hasFilters && (
              <div className="text-caption mt-1">Try clearing filters.</div>
            )}
          </div>
        </Card>
      ) : (
        <Card padded={false}>
          <div className="overflow-x-auto">
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft border-b border-hairline">
                <tr className="text-micro-uppercase text-stone">
                  <th className="text-left  font-medium px-4 py-2.5">When</th>
                  <th className="text-left  font-medium px-4 py-2.5">Who</th>
                  <th className="text-left  font-medium px-4 py-2.5">Doctype</th>
                  <th className="text-left  font-medium px-4 py-2.5">Document</th>
                  <th className="text-left  font-medium px-4 py-2.5">Action</th>
                  <th className="text-right font-medium px-4 py-2.5"></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((e) => (
                  <tr key={e.id} className="border-b border-hairline last:border-0 hover:bg-surface-soft transition-colors">
                    <td className="px-4 py-2 text-stone num whitespace-nowrap">{formatTime(e.changed_at)}</td>
                    <td className="px-4 py-2 text-ink">
                      <div className="truncate max-w-[200px]">{e.user_name || e.user_email || '—'}</div>
                      {e.user_email && e.user_name && (
                        <div className="text-caption text-stone truncate max-w-[200px]">{e.user_email}</div>
                      )}
                    </td>
                    <td className="px-4 py-2 text-steel font-mono text-caption">{e.doctype}</td>
                    <td className="px-4 py-2 text-charcoal font-mono text-caption truncate max-w-[260px]">{e.document_id}</td>
                    <td className="px-4 py-2">
                      <span className={cn('inline-flex items-center px-2 py-0.5 rounded-full text-caption font-medium',
                        ACTION_TONES[e.action] ?? 'bg-surface text-stone')}>
                        {e.action}
                      </span>
                    </td>
                    <td className="px-4 py-2 text-right">
                      <Button size="sm" variant="ghost" onClick={() => setSelected(e)}>View diff</Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}

      {selected && <DiffDialog entry={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}

function FilterSelect({
  label, value, onChange, options,
}: { label: string; value: string; onChange: (v: string) => void; options: { value: string; label: string }[] }) {
  return (
    <div className="flex flex-col gap-1 min-w-[160px]">
      <div className="text-micro-uppercase text-stone">{label}</div>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={cn(
          'h-8 px-2.5 rounded-md border border-hairline bg-canvas text-[13px] text-ink',
          'appearance-none pr-8 bg-no-repeat bg-[right_0.5rem_center] bg-[length:1rem] cursor-pointer',
        )}
        style={{ backgroundImage: "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>\")" }}
      >
        {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
    </div>
  );
}

function DiffDialog({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>
          <span className="text-caption font-mono text-stone block mb-1">{entry.id}</span>
          {entry.action} · {entry.doctype}
        </DialogTitle>

        <div className="grid sm:grid-cols-2 gap-x-6 gap-y-2 mt-3 text-body-sm">
          <KV k="When" v={formatTime(entry.changed_at)} mono />
          <KV k="Who"  v={entry.user_name || entry.user_email || entry.changed_by} />
          <KV k="Document" v={entry.document_id} mono className="sm:col-span-2" />
        </div>

        <div className="mt-4">
          <div className="text-micro-uppercase text-stone mb-1.5">Diff</div>
          <pre className="bg-surface-code text-on-dark text-caption font-mono p-3 rounded-md overflow-auto max-h-[400px]">
            {JSON.stringify(entry.diff ?? {}, null, 2)}
          </pre>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function KV({ k, v, mono, className }: { k: string; v: string; mono?: boolean; className?: string }) {
  return (
    <div className={className}>
      <div className="text-micro-uppercase text-stone">{k}</div>
      <div className={cn('text-ink truncate', mono && 'font-mono text-caption')}>{v}</div>
    </div>
  );
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('id-ID', { hour12: false });
}
