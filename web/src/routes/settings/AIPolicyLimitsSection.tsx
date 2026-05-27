import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, ShieldCheck, Trash2, AlertCircle, Globe2, Building2 } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api, getAccessToken } from '@/lib/api';

interface LimitRow {
  id: string;
  company_id?: string; // empty = global
  doctype: string;
  field: string;
  max_idr: string;
  label?: string;
  is_active: boolean;
  updated_at: string;
}
interface LimitsResp { items: LimitRow[] }

interface Company { id: string; name: string; abbreviation: string }
interface CompanyList { items: Company[] }

const DOCTYPE_FIELD_PRESETS: Array<{ doctype: string; field: string; nice: string }> = [
  { doctype: 'purchase_order',   field: 'grand_total',  nice: 'Purchase Order — total' },
  { doctype: 'sales_invoice',    field: 'grand_total',  nice: 'Sales Invoice — total' },
  { doctype: 'purchase_invoice', field: 'grand_total',  nice: 'Purchase Invoice — total' },
  { doctype: 'payment_entry',    field: 'paid_amount',  nice: 'Payment Entry — amount' },
  { doctype: 'journal_entry',    field: 'total_debit',  nice: 'Journal Entry — debit total' },
  { doctype: 'sales_order',      field: 'grand_total',  nice: 'Sales Order — total' },
  { doctype: 'stock_entry',      field: 'total_value',  nice: 'Stock Entry — value' },
];

async function agent<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...(init?.headers as Record<string, string> | undefined),
  };
  if (init?.body && !headers['Content-Type']) headers['Content-Type'] = 'application/json';
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const r = await fetch(`/api/agent/v1${path}`, { ...init, headers });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return t ? (JSON.parse(t) as T) : ({} as T);
}

function formatIDR(s: string): string {
  const n = Number(s);
  if (!Number.isFinite(n)) return s;
  return 'Rp ' + n.toLocaleString('id-ID', { minimumFractionDigits: 0, maximumFractionDigits: 0 });
}

export function AIPolicyLimitsSection() {
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({
    queryKey: ['agent-policy-limits'],
    queryFn:  () => agent<LimitsResp>('/admin/policy/limits'),
  });
  const { data: companies } = useQuery({
    queryKey: ['companies'],
    queryFn:  () => api<CompanyList>('/accounting/companies'),
  });

  const [createOpen, setCreateOpen] = useState(false);

  // Group by doctype/field so per-company overrides nest under their default.
  const groups = useMemo(() => {
    const byKey = new Map<string, LimitRow[]>();
    for (const r of data?.items ?? []) {
      const key = `${r.doctype}::${r.field}`;
      const arr = byKey.get(key) ?? [];
      arr.push(r);
      byKey.set(key, arr);
    }
    // Within each group: global first, then by company id alphabetically.
    for (const [, arr] of byKey) {
      arr.sort((a, b) => {
        if (!a.company_id && b.company_id) return -1;
        if (a.company_id && !b.company_id) return 1;
        return (a.company_id ?? '').localeCompare(b.company_id ?? '');
      });
    }
    return Array.from(byKey.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [data]);

  const companyName = (id?: string) => {
    if (!id) return null;
    const c = (companies?.items ?? []).find((x) => x.id === id);
    return c?.name ?? id;
  };

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>
            <ShieldCheck className="size-4 inline mr-1.5 text-accent" />
            AI policy limits
          </CardTitle>
          <CardDescription>
            Cap the IDR value the agent may draft per doctype before a policy_blocked event is
            raised. Per-company overrides beat the global default. Tier-2 auto-submit remains
            disabled regardless.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New cap
        </Button>
      </div>

      {error && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">{(error as Error).message}</div>
        </Card>
      )}

      {isLoading ? (
        <Card><Skeleton className="h-32 w-full" /></Card>
      ) : groups.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <ShieldCheck className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No caps configured.</div>
            <div className="text-caption text-stone mt-1 max-w-md mx-auto">
              Without caps, Tier-1 drafts (PO, SI, JE, payment) of any amount go through the
              gate. Set a global default first; add per-company overrides later if needed.
            </div>
            <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> Set a cap
            </Button>
          </div>
        </Card>
      ) : (
        <div className="space-y-4">
          {groups.map(([key, rows]) => {
            const [doctype, field] = key.split('::');
            const preset = DOCTYPE_FIELD_PRESETS.find((p) => p.doctype === doctype && p.field === field);
            return (
              <Card key={key} padded={false}>
                <div className="px-4 py-2.5 border-b border-hairline flex items-baseline justify-between">
                  <div className="text-body-sm font-medium text-ink">
                    {preset?.nice ?? doctype}
                  </div>
                  <div className="text-caption text-stone font-mono">{doctype}.{field}</div>
                </div>
                <ul className="divide-y divide-hairline">
                  {rows.map((r) => (
                    <LimitRowItem
                      key={r.id}
                      row={r}
                      companyName={companyName(r.company_id)}
                      onChanged={() => qc.invalidateQueries({ queryKey: ['agent-policy-limits'] })}
                    />
                  ))}
                </ul>
              </Card>
            );
          })}
        </div>
      )}

      <div className="rounded-lg border border-hairline bg-surface-soft p-3 text-caption text-stone flex items-start gap-2">
        <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
        <div>
          <div className="text-ink text-body-sm font-medium mb-0.5">How this is enforced</div>
          When the agent calls a Tier-1 tool, the gate reads the configured field
          (e.g. <span className="font-mono">grand_total</span>) from the proposed payload. If
          it exceeds the cap, the call is blocked and recorded as
          <span className="font-mono"> policy_blocked</span> in the AI audit log. Caps refresh
          every 60s on the agent service.
        </div>
      </div>

      {createOpen && (
        <CreateDialog
          companies={companies?.items ?? []}
          existing={data?.items ?? []}
          onClose={() => setCreateOpen(false)}
          onCreated={() => { void qc.invalidateQueries({ queryKey: ['agent-policy-limits'] }); setCreateOpen(false); }}
        />
      )}
    </div>
  );
}

function LimitRowItem({
  row, companyName, onChanged,
}: { row: LimitRow; companyName: string | null; onChanged: () => void }) {
  const qc = useQueryClient();
  const [err, setErr] = useState<string | null>(null);

  const toggle = useMutation({
    mutationFn: () => agent<LimitRow>('/admin/policy/limits', {
      method: 'POST',
      body: JSON.stringify({
        company_id: row.company_id ?? '',
        doctype:    row.doctype,
        field:      row.field,
        max_idr:    row.max_idr,
        label:      row.label ?? '',
        is_active:  !row.is_active,
      }),
    }),
    onSuccess: () => { setErr(null); void qc.invalidateQueries({ queryKey: ['agent-policy-limits'] }); onChanged(); },
    onError:   (e: Error) => setErr(e.message),
  });
  const del = useMutation({
    mutationFn: () => agent<void>(`/admin/policy/limits/${row.id}`, { method: 'DELETE' }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['agent-policy-limits'] }); onChanged(); },
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <li className="px-4 py-2.5">
      <div className="flex items-center gap-3 flex-wrap">
        <div className="min-w-0 flex-1 flex items-baseline gap-3 flex-wrap">
          {row.company_id ? (
            <StatusPill tone="info" withDot={false}>
              <Building2 className="size-3" /> {companyName ?? row.company_id}
            </StatusPill>
          ) : (
            <StatusPill tone="accent" withDot={false}>
              <Globe2 className="size-3" /> Global default
            </StatusPill>
          )}
          <span className="font-mono text-body-sm text-ink num">{formatIDR(row.max_idr)}</span>
          {row.label && (
            <span className="text-caption text-stone">— {row.label}</span>
          )}
          {!row.is_active && (
            <StatusPill tone="neutral" withDot={false}>Inactive</StatusPill>
          )}
        </div>

        <div className="flex items-center gap-1">
          <Button size="sm" variant="ghost" onClick={() => toggle.mutate()} loading={toggle.isPending}>
            {row.is_active ? 'Disable' : 'Enable'}
          </Button>
          <Button size="sm" variant="ghost"
            onClick={() => { if (confirm(`Delete cap for ${row.doctype}.${row.field}${row.company_id ? ` (company ${row.company_id})` : ' (global)'}?`)) del.mutate(); }}>
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>
      {err && (
        <div className="mt-2 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{err}</div>
      )}
    </li>
  );
}

function CreateDialog({
  companies, existing, onClose, onCreated,
}: {
  companies: Company[];
  existing: LimitRow[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const [presetIdx, setPresetIdx]   = useState(0);
  const [customDoc, setCustomDoc]   = useState('');
  const [customField, setCustomField] = useState('');
  const [companyId, setCompanyId]   = useState('');
  const [maxIDR, setMaxIDR]         = useState('50000000'); // 50 juta default — matches spec §2
  const [label, setLabel]           = useState('Rp 50 juta');
  const [error, setError]           = useState<string | null>(null);

  const isCustom = presetIdx === DOCTYPE_FIELD_PRESETS.length;
  const doctype = isCustom ? customDoc.trim() : DOCTYPE_FIELD_PRESETS[presetIdx]!.doctype;
  const field   = isCustom ? customField.trim() : DOCTYPE_FIELD_PRESETS[presetIdx]!.field;

  // Warn if this (company, doctype, field) already exists — POST will upsert,
  // so we want operators to do that on purpose.
  const conflict = existing.find((r) =>
    r.doctype === doctype && r.field === field && (r.company_id ?? '') === companyId);

  const mut = useMutation({
    mutationFn: () => agent<LimitRow>('/admin/policy/limits', {
      method: 'POST',
      body: JSON.stringify({
        company_id: companyId,
        doctype,
        field,
        max_idr:    maxIDR,
        label,
        is_active:  true,
      }),
    }),
    onSuccess: () => onCreated(),
    onError:   (e: Error) => setError(e.message),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!doctype) return setError('Doctype is required.');
    if (!field) return setError('Field is required.');
    if (!/^\d+(\.\d+)?$/.test(maxIDR)) return setError('Max IDR must be a number.');
    mut.mutate();
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New value cap</DialogTitle>
        <DialogDescription>
          Cap the amount the agent may draft on a doctype. Apply globally, or to one company.
        </DialogDescription>

        <form onSubmit={submit} className="mt-4 space-y-3">
          <Field label="Doctype + field">
            <select
              value={presetIdx}
              onChange={(e) => setPresetIdx(Number(e.target.value))}
              className="w-full h-9 px-3 rounded-md border border-hairline bg-surface text-body-sm focus:outline-none focus:ring-2 focus:ring-accent">
              {DOCTYPE_FIELD_PRESETS.map((p, i) => (
                <option key={`${p.doctype}.${p.field}`} value={i}>{p.nice}</option>
              ))}
              <option value={DOCTYPE_FIELD_PRESETS.length}>— Other (free text) —</option>
            </select>
            {isCustom && (
              <div className="mt-2 grid grid-cols-2 gap-2">
                <Input placeholder="doctype (e.g. asset)" value={customDoc} onChange={(e) => setCustomDoc(e.target.value)} className="font-mono" />
                <Input placeholder="field (e.g. gross_purchase_amount)" value={customField} onChange={(e) => setCustomField(e.target.value)} className="font-mono" />
              </div>
            )}
          </Field>

          <Field label="Scope" hint="Leave as global to set a default; pick a company to override it.">
            <select
              value={companyId}
              onChange={(e) => setCompanyId(e.target.value)}
              className="w-full h-9 px-3 rounded-md border border-hairline bg-surface text-body-sm focus:outline-none focus:ring-2 focus:ring-accent">
              <option value="">Global default</option>
              {companies.map((c) => (
                <option key={c.id} value={c.id}>{c.name} ({c.abbreviation})</option>
              ))}
            </select>
          </Field>

          <Field label="Max IDR" hint="Numeric, no separators (e.g. 50000000 for 50 juta).">
            <Input value={maxIDR} onChange={(e) => setMaxIDR(e.target.value)} className="font-mono num" inputMode="numeric" required />
            <div className="mt-1 text-caption text-stone">
              {/^\d+(\.\d+)?$/.test(maxIDR) ? formatIDR(maxIDR) : '—'}
            </div>
          </Field>

          <Field label="Label" hint="Shown to the user in the policy_blocked reason. Optional.">
            <Input value={label} onChange={(e) => setLabel(e.target.value)} />
          </Field>

          {conflict && (
            <div className="rounded-md bg-amber-50 border border-amber-200 text-amber-900 text-caption px-3 py-2 flex items-start gap-2">
              <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
              <span>
                A cap already exists for this scope ({formatIDR(conflict.max_idr)}). Submitting
                will overwrite it.
              </span>
            </div>
          )}

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>{conflict ? 'Overwrite cap' : 'Create cap'}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
