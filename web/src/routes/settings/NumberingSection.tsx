import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus, Hash, Star, RotateCcw, Trash2, AlertCircle, Pencil, X, Check,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface Series {
  id: string;
  doctype: string;
  company_id?: string;
  pattern: string;
  is_default: boolean;
  current_value: number;
  created_at: string;
}
interface SeriesList { items: Series[] }

/** Doctypes the user is most likely to want to manage. Free-text input is
 *  still allowed in the create dialog for the long tail. */
const COMMON_DOCTYPES = [
  'sales_invoice', 'purchase_invoice', 'payment_entry', 'journal_entry',
  'sales_order', 'purchase_order',
  'stock_entry', 'period_closing_voucher',
  'lead', 'project', 'task', 'timesheet',
  'bom', 'work_order', 'asset',
  'employee', 'payroll_entry', 'salary_slip',
  'pos_invoice', 'issue',
];

const DOCTYPE_LABELS: Record<string, string> = {
  sales_invoice: 'Sales Invoice (SI)',
  purchase_invoice: 'Purchase Invoice (PI)',
  payment_entry: 'Payment Entry (PE)',
  journal_entry: 'Journal Entry (JE)',
  sales_order: 'Sales Order (SO)',
  purchase_order: 'Purchase Order (PO)',
  stock_entry: 'Stock Entry',
  period_closing_voucher: 'Period Closing Voucher',
  lead: 'Lead',
  project: 'Project',
  task: 'Task',
  timesheet: 'Timesheet',
  bom: 'BOM',
  work_order: 'Work Order',
  asset: 'Asset',
  employee: 'Employee',
  payroll_entry: 'Payroll Entry',
  salary_slip: 'Salary Slip',
  pos_invoice: 'POS Invoice',
  issue: 'Issue / Ticket',
};

const PATTERN_HINTS = [
  { token: '.YYYY.',         label: 'Four-digit year' },
  { token: '.YY.',           label: 'Two-digit year' },
  { token: '.MM.',           label: 'Two-digit month' },
  { token: '.DD.',           label: 'Two-digit day' },
  { token: '.####',          label: 'Counter, padded to # width' },
  { token: '.company_abbr.', label: 'Company abbreviation' },
];

export function NumberingSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['naming-series'],
    queryFn: () => api<SeriesList>('/admin/naming-series'),
  });
  const items = data?.items ?? [];
  const [createOpen, setCreateOpen] = useState(false);

  // Group by doctype for the UI.
  const groups = useMemo(() => {
    const byDoc = new Map<string, Series[]>();
    for (const s of items) {
      const arr = byDoc.get(s.doctype) ?? [];
      arr.push(s);
      byDoc.set(s.doctype, arr);
    }
    return Array.from(byDoc.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [items]);

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Numbering series</CardTitle>
          <CardDescription>
            Patterns generate document numbers like <span className="font-mono text-ink">SI-2026-0001</span>.
            One default per (doctype + company); counters reset by changing the pattern's date scope.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New series
        </Button>
      </div>

      <PatternLegend />

      {isLoading ? (
        <Card><Skeleton className="h-32 w-full" /></Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Hash className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No naming series yet.</div>
            <div className="text-caption text-stone mt-1">
              Add one per doctype you intend to post documents under.
            </div>
            <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> Create series
            </Button>
          </div>
        </Card>
      ) : (
        <div className="space-y-4">
          {groups.map(([doctype, series]) => (
            <DoctypeGroup key={doctype} doctype={doctype} series={series} onChanged={() => qc.invalidateQueries({ queryKey: ['naming-series'] })} />
          ))}
        </div>
      )}

      {createOpen && (
        <CreateDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => { void qc.invalidateQueries({ queryKey: ['naming-series'] }); setCreateOpen(false); }}
        />
      )}
    </div>
  );
}

function PatternLegend() {
  return (
    <div className="rounded-lg border border-hairline bg-surface-soft p-3 text-caption">
      <div className="text-micro-uppercase text-stone mb-2">Pattern placeholders</div>
      <div className="grid grid-cols-2 md:grid-cols-3 gap-x-4 gap-y-1.5">
        {PATTERN_HINTS.map((h) => (
          <div key={h.token} className="flex items-baseline gap-2">
            <span className="font-mono text-ink">{h.token}</span>
            <span className="text-stone">{h.label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

/* ------------------------- per-doctype group ------------------------- */

function DoctypeGroup({
  doctype, series, onChanged,
}: { doctype: string; series: Series[]; onChanged: () => void }) {
  return (
    <Card padded={false}>
      <div className="px-4 py-2.5 border-b border-hairline flex items-baseline justify-between">
        <div className="text-body-sm font-medium text-ink">
          {DOCTYPE_LABELS[doctype] ?? doctype}
        </div>
        <div className="text-caption text-stone font-mono">{doctype}</div>
      </div>
      <ul className="divide-y divide-hairline">
        {series.map((s) => <SeriesRow key={s.id} series={s} onChanged={onChanged} />)}
      </ul>
    </Card>
  );
}

function SeriesRow({ series, onChanged }: { series: Series; onChanged: () => void }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [pattern, setPattern] = useState(series.pattern);
  const [err, setErr] = useState<string | null>(null);

  const preview = usePatternPreview(editing ? pattern : series.pattern, 'L');

  const update = useMutation({
    mutationFn: (body: { pattern?: string; is_default?: boolean }) =>
      api<Series>(`/admin/naming-series/${series.id}`, { method: 'PUT', body }),
    onSuccess: () => { setEditing(false); setErr(null); void qc.invalidateQueries({ queryKey: ['naming-series'] }); onChanged(); },
    onError:   (e: Error) => setErr(e.message),
  });
  const reset = useMutation({
    mutationFn: () => api<void>(`/admin/naming-series/${series.id}/reset`, { method: 'POST' }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['naming-series'] }); onChanged(); },
  });
  const del = useMutation({
    mutationFn: () => api<void>(`/admin/naming-series/${series.id}`, { method: 'DELETE' }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['naming-series'] }); onChanged(); },
  });

  return (
    <li className="px-4 py-2.5">
      <div className="flex items-center gap-3 flex-wrap">
        <div className="min-w-0 flex-1">
          {editing ? (
            <div className="space-y-1">
              <Input className="!h-8 !text-[13px] font-mono"
                value={pattern} onChange={(e) => setPattern(e.target.value)} autoFocus />
              <div className="text-caption text-stone">
                Next: <span className="font-mono text-ink">{preview ?? '…'}</span>
              </div>
            </div>
          ) : (
            <div className="flex items-baseline gap-3 flex-wrap">
              <span className="font-mono text-body-sm text-ink">{series.pattern}</span>
              {series.is_default && (
                <StatusPill tone="accent" withDot={false}>
                  <Star className="size-3" /> Default
                </StatusPill>
              )}
              {series.company_id && (
                <StatusPill tone="info" withDot={false}>Company-scoped</StatusPill>
              )}
              {!series.company_id && (
                <span className="text-caption text-stone">All companies</span>
              )}
              <span className="text-caption text-stone ml-auto">
                Next: <span className="font-mono text-ink">{preview ?? '…'}</span>
              </span>
            </div>
          )}
        </div>

        {editing ? (
          <div className="flex items-center gap-1">
            <Button size="sm" variant="ghost" onClick={() => { setEditing(false); setPattern(series.pattern); setErr(null); }}>
              <X className="size-3.5" />
            </Button>
            <Button size="sm" onClick={() => update.mutate({ pattern })} loading={update.isPending}>
              <Check className="size-3.5" /> Save
            </Button>
          </div>
        ) : (
          <div className="flex items-center gap-1">
            {!series.is_default && (
              <Button size="sm" variant="ghost" onClick={() => update.mutate({ is_default: true })}>
                <Star className="size-3.5" /> Default
              </Button>
            )}
            <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
              <Pencil className="size-3.5" /> Edit
            </Button>
            <Button size="sm" variant="ghost"
              onClick={() => { if (confirm(`Reset counter for "${series.pattern}"? Next allocation starts at 1.`)) reset.mutate(); }}>
              <RotateCcw className="size-3.5" /> Reset
            </Button>
            <Button size="sm" variant="ghost"
              onClick={() => { if (confirm(`Delete series "${series.pattern}"?`)) del.mutate(); }}>
              <Trash2 className="size-3.5" />
            </Button>
          </div>
        )}
      </div>

      <div className="mt-1 flex items-center gap-3 text-caption text-stone">
        <span>Counter at <span className="font-mono text-ink num">{series.current_value}</span></span>
        {series.current_value === 0 && <span>· never used</span>}
      </div>

      {err && (
        <div className="mt-2 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{err}</div>
      )}
    </li>
  );
}

/* ------------------------- create dialog ------------------------- */

function CreateDialog({
  onClose, onCreated,
}: { onClose: () => void; onCreated: () => void }) {
  const [doctype, setDoctype]     = useState('sales_invoice');
  const [pattern, setPattern]     = useState('SI-.YYYY.-.####');
  const [companyId, setCompanyId] = useState(''); // empty = all companies
  const [isDefault, setIsDefault] = useState(true);
  const [error, setError]         = useState<string | null>(null);

  const preview = usePatternPreview(pattern, 'L');

  const mut = useMutation({
    mutationFn: () => api<Series>('/admin/naming-series', {
      method: 'POST',
      body: { doctype, pattern, ...(companyId ? { company_id: companyId } : {}), is_default: isDefault },
    }),
    onSuccess: () => onCreated(),
    onError:   (e: Error) => setError(e.message),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!doctype.trim()) return setError('Doctype is required.');
    if (!pattern.trim()) return setError('Pattern is required.');
    mut.mutate();
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New naming series</DialogTitle>
        <DialogDescription>
          Define how document numbers are generated for a doctype.
        </DialogDescription>

        <form onSubmit={submit} className="mt-4 space-y-3">
          <Field label="Doctype">
            <NativeSelect
              value={doctype}
              onChange={(v) => {
                setDoctype(v);
                // Suggest a sensible pattern.
                const upper = v.split('_').map((w) => w[0]).join('').toUpperCase();
                setPattern(`${upper}-.YYYY.-.####`);
              }}
              options={[
                ...COMMON_DOCTYPES.map((d) => ({ value: d, label: DOCTYPE_LABELS[d] ?? d })),
                { value: '__other__', label: '— Other (free text) —' },
              ]}
            />
            {doctype === '__other__' && (
              <Input className="mt-2" placeholder="custom_doctype_name"
                value="" onChange={(e) => setDoctype(e.target.value)} />
            )}
          </Field>

          <Field label="Pattern" hint="Use the placeholders above. Counter width is the number of #.">
            <Input className="font-mono" value={pattern} onChange={(e) => setPattern(e.target.value)} required />
          </Field>

          <div className="rounded-md bg-surface px-3 py-2 text-body-sm">
            Next number: <span className="font-mono text-ink">{preview ?? '…'}</span>
          </div>

          <Field label="Scope" hint="Leave empty for an all-company series.">
            <Input placeholder="company_id (optional)" value={companyId} onChange={(e) => setCompanyId(e.target.value)} className="font-mono" />
          </Field>

          <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
            <input type="checkbox" className="accent-brand-green-deep" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} />
            Make default for this doctype
          </label>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create series</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ------------------------- shared ------------------------- */

function usePatternPreview(pattern: string, abbr: string): string | null {
  const { data } = useQuery({
    queryKey: ['naming-series', 'preview', pattern, abbr],
    queryFn: () => api<{ preview: string }>(`/admin/naming-series/preview?pattern=${encodeURIComponent(pattern)}&abbr=${encodeURIComponent(abbr)}`),
    enabled: !!pattern && pattern.length > 0,
    retry: false,
    staleTime: 60_000,
  });
  return data?.preview ?? null;
}

function NativeSelect({
  value, options, onChange,
}: { value: string; options: { value: string; label: string }[]; onChange: (v: string) => void }) {
  return (
    <select
      className={cn('input-base appearance-none pr-8 bg-no-repeat bg-[right_0.75rem_center] bg-[length:1.25rem] cursor-pointer')}
      style={{ backgroundImage: "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>\")" }}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  );
}
