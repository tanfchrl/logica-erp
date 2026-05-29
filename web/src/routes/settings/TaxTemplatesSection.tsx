import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, FileText, Trash2, Star, Pencil, Percent, Tag } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

/* ----------------------------- types ----------------------------------- */

interface Account     { id: string; account_name: string; account_number?: string }
interface AccountList { items: Account[] }

interface TaxTemplateLine {
  id: string;
  row_index: number;
  description: string;
  rate: string;
  account_id: string;
  charge_type: string;
  included_in_basic_rate: boolean;
  cost_center_id?: string;
}
interface TaxTemplate {
  id: string;
  name: string;
  company_id: string;
  is_sales: boolean;
  is_default: boolean;
  tax_category_id?: string;
  lines: TaxTemplateLine[];
  created_at: string;
}
interface TaxTemplateList { items: TaxTemplate[] }

interface TaxCategory     { id: string; name: string; description?: string }
interface TaxCategoryList { items: TaxCategory[] }

interface WithholdingType {
  id: string;
  name: string;
  category?: string;
  rate: string;
  threshold?: string;
  account_id: string;
}
interface WithholdingTypeList { items: WithholdingType[] }

type Tab = 'templates' | 'withholding' | 'categories';

const CHARGE_TYPES = [
  'On Net Total',
  'On Previous Row Amount',
  'On Previous Row Total',
  'Actual',
];

const WHT_CATEGORIES = [
  { value: '',          label: '— Uncategorized —' },
  { value: 'pph-21',    label: 'PPh 21 (employee)' },
  { value: 'pph-22',    label: 'PPh 22 (import / specific goods)' },
  { value: 'pph-23',    label: 'PPh 23 (services / royalty)' },
  { value: 'pph-26',    label: 'PPh 26 (foreign payee)' },
  { value: 'pph-4-2',   label: 'PPh 4(2) (final)' },
];

/* ----------------------------- section --------------------------------- */

export function TaxTemplatesSection() {
  const [tab, setTab] = useState<Tab>('templates');

  return (
    <div className="space-y-5">
      <div className="inline-flex items-center p-1 rounded-full bg-surface border border-hairline">
        <TabChip active={tab === 'templates'}   icon={Percent}  label="Templates"   onClick={() => setTab('templates')} />
        <TabChip active={tab === 'withholding'} icon={FileText} label="Withholding" onClick={() => setTab('withholding')} />
        <TabChip active={tab === 'categories'}  icon={Tag}      label="Categories"  onClick={() => setTab('categories')} />
      </div>

      {tab === 'templates'   && <TemplatesTab />}
      {tab === 'withholding' && <WithholdingTab />}
      {tab === 'categories'  && <CategoriesTab />}
    </div>
  );
}

function TabChip({
  active, icon: Icon, label, onClick,
}: { active: boolean; icon: React.ComponentType<{ className?: string }>; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 h-8 px-3 rounded-full text-body-sm transition-colors',
        active ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink',
      )}
    >
      <Icon className="size-3.5" />
      {label}
    </button>
  );
}

/* =========================== Templates tab ============================ */

function TemplatesTab() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['tax-templates'],
    queryFn: () => api<TaxTemplateList>('/accounting/tax-templates'),
  });
  const [expanded, setExpanded] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<TaxTemplate | null>(null);

  const items = data?.items ?? [];
  const salesCount    = items.filter((t) => t.is_sales).length;
  const purchaseCount = items.filter((t) => !t.is_sales).length;

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Tax templates</CardTitle>
          <CardDescription>
            Reusable tax rates that pre-fill on sales and purchase documents.
            Mark one default per direction.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New template
        </Button>
      </div>

      {!isLoading && items.length > 0 && (
        <div className="flex gap-2 text-caption text-stone">
          <span>{salesCount} sales</span><span>·</span>
          <span>{purchaseCount} purchase</span>
        </div>
      )}

      {isLoading ? (
        <Card><Skeleton className="h-24 w-full" /></Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Percent className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No tax templates yet.</div>
            <div className="text-caption text-stone mt-1">
              Add a PPN 11% template to start tracking output / input VAT.
            </div>
            <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> Create template
            </Button>
          </div>
        </Card>
      ) : (
        <div className="space-y-2">
          {items.map((t) => (
            <TemplateRow
              key={t.id}
              template={t}
              expanded={expanded === t.id}
              onToggle={() => setExpanded(expanded === t.id ? null : t.id)}
              onEdit={() => setEditing(t)}
            />
          ))}
        </div>
      )}

      {createOpen && (
        <TemplateDialog
          onClose={() => setCreateOpen(false)}
          onSaved={() => { void qc.invalidateQueries({ queryKey: ['tax-templates'] }); setCreateOpen(false); }}
        />
      )}

      {editing && (
        <TemplateDialog
          template={editing}
          onClose={() => setEditing(null)}
          onSaved={() => { void qc.invalidateQueries({ queryKey: ['tax-templates'] }); setEditing(null); }}
        />
      )}
    </div>
  );
}

function TemplateRow({
  template, expanded, onToggle, onEdit,
}: { template: TaxTemplate; expanded: boolean; onToggle: () => void; onEdit: () => void }) {
  const lines = template.lines ?? [];
  const total = lines.reduce((sum, l) => sum + Number(l.rate || 0), 0);
  return (
    <div className={cn(
      'bg-canvas border rounded-lg transition-colors',
      expanded ? 'border-ink' : 'border-hairline',
    )}>
      <button
        type="button"
        onClick={onToggle}
        className="w-full text-left p-3 flex items-center gap-3"
      >
        <span className={cn(
          'inline-flex items-center justify-center size-9 rounded-md',
          template.is_sales ? 'bg-brand-green-soft/40 text-brand-green-deep' : 'bg-info/10 text-info',
        )}>
          <Percent className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <div className="text-body-sm font-medium text-ink truncate">{template.name}</div>
            {template.is_default && <StatusPill tone="accent" withDot={false}><Star className="size-3" /> Default</StatusPill>}
          </div>
          <div className="text-caption text-stone">
            {template.is_sales ? 'Sales' : 'Purchase'} · {lines.length} {lines.length === 1 ? 'line' : 'lines'}
          </div>
        </div>
        <div className="text-body-sm num text-ink">{total.toFixed(2)}%</div>
      </button>

      {expanded && (
        <div className="border-t border-hairline p-3 pt-2">
          {lines.length === 0 ? (
            <div className="text-caption text-stone py-2">No lines on this template.</div>
          ) : (
            <table className="w-full text-body-sm">
              <thead>
                <tr className="text-micro-uppercase text-stone">
                  <th className="text-left font-medium py-1.5">Description</th>
                  <th className="text-left font-medium py-1.5">Charge type</th>
                  <th className="text-right font-medium py-1.5">Rate</th>
                  <th className="text-center font-medium py-1.5">Inclusive</th>
                </tr>
              </thead>
              <tbody>
                {lines.map((l) => (
                  <tr key={l.id} className="border-t border-hairline">
                    <td className="py-2 text-ink">{l.description}</td>
                    <td className="py-2 text-steel">{l.charge_type}</td>
                    <td className="py-2 text-right num text-ink">{l.rate}%</td>
                    <td className="py-2 text-center text-steel">{l.included_in_basic_rate ? 'Yes' : 'No'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <div className="mt-3 pt-3 border-t border-hairline flex justify-end">
            <Button type="button" variant="ghost" size="sm" onClick={onEdit}>
              <Pencil className="size-3.5" /> Edit template
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/* -------- Create template dialog -------- */

interface DraftLine {
  description: string;
  rate: string;
  account_id: string;
  charge_type: string;
  included_in_basic_rate: boolean;
}

function TemplateDialog({
  template, onClose, onSaved,
}: { template?: TaxTemplate; onClose: () => void; onSaved: () => void }) {
  const isEdit = !!template;
  const { data: acc } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api<AccountList>('/accounting/accounts'),
  });
  const { data: cats } = useQuery({
    queryKey: ['tax-categories'],
    queryFn: () => api<TaxCategoryList>('/accounting/tax-categories'),
  });

  const accountOpts = useMemo(
    () => (acc?.items ?? []).map((a) => ({
      value: a.id,
      label: a.account_number ? `${a.account_number} — ${a.account_name}` : a.account_name,
    })),
    [acc],
  );
  const catOpts = useMemo(
    () => [{ value: '', label: '— None —' }, ...(cats?.items ?? []).map((c) => ({ value: c.id, label: c.name }))],
    [cats],
  );

  const [name, setName]       = useState(template?.name ?? 'PPN 11% — Output');
  const [isSales, setIsSales] = useState(template?.is_sales ?? true);
  const [isDefault, setIsDefault] = useState(template?.is_default ?? false);
  const [categoryId, setCategoryId] = useState(template?.tax_category_id ?? '');
  const [lines, setLines] = useState<DraftLine[]>(
    template
      ? (template.lines ?? []).map((l) => ({
          description: l.description,
          rate: l.rate,
          account_id: l.account_id,
          charge_type: l.charge_type,
          included_in_basic_rate: l.included_in_basic_rate,
        }))
      : [{
          description: 'PPN 11%', rate: '11', account_id: '',
          charge_type: 'On Net Total', included_in_basic_rate: false,
        }],
  );
  const [error, setError] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => {
      const payload = {
        name,
        is_sales: isSales,
        is_default: isDefault,
        ...(categoryId ? { tax_category_id: categoryId } : {}),
        lines: lines.map((l) => ({
          description: l.description,
          rate: l.rate,
          account_id: l.account_id,
          charge_type: l.charge_type,
          included_in_basic_rate: l.included_in_basic_rate,
        })),
      };
      return isEdit
        ? api<TaxTemplate>(`/accounting/tax-templates/${template!.id}`, { method: 'PUT', body: payload })
        : api<TaxTemplate>('/accounting/tax-templates', { method: 'POST', body: payload });
    },
    onSuccess: () => onSaved(),
    onError: (e: Error) => setError(e.message),
  });

  function updateLine(i: number, patch: Partial<DraftLine>) {
    setLines((prev) => prev.map((l, idx) => idx === i ? { ...l, ...patch } : l));
  }
  function addLine() {
    setLines((prev) => [...prev, {
      description: '', rate: '0', account_id: '',
      charge_type: 'On Net Total', included_in_basic_rate: false,
    }]);
  }
  function removeLine(i: number) {
    setLines((prev) => prev.filter((_, idx) => idx !== i));
  }

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!name.trim()) return setError('Name is required.');
    if (lines.length === 0) return setError('Add at least one tax line.');
    for (const [i, l] of lines.entries()) {
      if (!l.description.trim()) return setError(`Line ${i + 1}: description is required.`);
      if (!l.account_id)         return setError(`Line ${i + 1}: pick a GL account.`);
      if (Number.isNaN(Number(l.rate))) return setError(`Line ${i + 1}: rate must be a number.`);
    }
    mut.mutate();
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>{isEdit ? 'Edit tax template' : 'New tax template'}</DialogTitle>
        <DialogDescription>
          Group one or more tax lines under a reusable name. Mark default to auto-fill on new documents.
          {isEdit && ' Saving replaces all existing lines on this template.'}
        </DialogDescription>

        <form onSubmit={submit} className="mt-4 space-y-4">
          <div className="grid sm:grid-cols-2 gap-3">
            <Field label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} required />
            </Field>
            <Field label="Category" hint="Optional grouping for reporting.">
              <NativeSelect value={categoryId} onChange={setCategoryId} options={catOpts} />
            </Field>
          </div>

          <div className="flex items-center gap-6">
            <ToggleField
              label="Direction"
              left="Sales" right="Purchase"
              value={isSales} onChange={setIsSales}
            />
            <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
              <input
                type="checkbox"
                className="accent-brand-green-deep"
                checked={isDefault}
                onChange={(e) => setIsDefault(e.target.checked)}
              />
              Mark as default for this direction
            </label>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <div className="label-base">Tax lines</div>
              <Button type="button" variant="ghost" size="sm" onClick={addLine}>
                <Plus className="size-3.5" /> Add line
              </Button>
            </div>

            <div className="rounded-lg border border-hairline overflow-hidden">
              <table className="w-full text-body-sm">
                <thead className="bg-surface text-micro-uppercase text-stone">
                  <tr>
                    <th className="text-left font-medium px-2 py-2">Description</th>
                    <th className="text-left font-medium px-2 py-2">GL account</th>
                    <th className="text-left font-medium px-2 py-2">Charge</th>
                    <th className="text-right font-medium px-2 py-2 w-[80px]">Rate %</th>
                    <th className="text-center font-medium px-2 py-2 w-[40px]"></th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l, i) => (
                    <tr key={i} className="border-t border-hairline">
                      <td className="p-1">
                        <Input className="!h-8 !text-[13px]" value={l.description}
                          onChange={(e) => updateLine(i, { description: e.target.value })} />
                      </td>
                      <td className="p-1">
                        <NativeSelect className="!h-8 !text-[13px]" value={l.account_id} options={accountOpts}
                          onChange={(v) => updateLine(i, { account_id: v })} placeholder="Select account…" />
                      </td>
                      <td className="p-1">
                        <NativeSelect className="!h-8 !text-[13px]" value={l.charge_type}
                          options={CHARGE_TYPES.map((c) => ({ value: c, label: c }))}
                          onChange={(v) => updateLine(i, { charge_type: v })} />
                      </td>
                      <td className="p-1">
                        <Input className="!h-8 !text-[13px] !text-right num" value={l.rate}
                          onChange={(e) => updateLine(i, { rate: e.target.value })} />
                      </td>
                      <td className="p-1 text-center">
                        <button type="button" onClick={() => removeLine(i)}
                          className="inline-flex items-center justify-center size-7 rounded-md text-stone hover:bg-surface hover:text-brand-error"
                          aria-label="Remove line">
                          <Trash2 className="size-3.5" />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>{isEdit ? 'Save changes' : 'Create template'}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* =========================== Withholding tab =========================== */

function WithholdingTab() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['withholding-tax-types'],
    queryFn: () => api<WithholdingTypeList>('/accounting/withholding-tax-types'),
  });
  const items = data?.items ?? [];
  const [createOpen, setCreateOpen] = useState(false);

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Withholding tax (PPh)</CardTitle>
          <CardDescription>
            PPh 21, 23, 26 and other Indonesian withholding types. Used on payment entries and supplier invoices.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New type
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-20 w-full" /></Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <FileText className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No withholding types yet.</div>
            <div className="text-caption text-stone mt-1">
              Add PPh 23 (2% services) and PPh 21 to get started.
            </div>
          </div>
        </Card>
      ) : (
        <Card padded={false}>
          <table className="w-full text-body-sm">
            <thead className="bg-surface-soft border-b border-hairline">
              <tr className="text-micro-uppercase text-stone">
                <th className="text-left font-medium px-4 py-2.5">Name</th>
                <th className="text-left font-medium px-4 py-2.5">Category</th>
                <th className="text-right font-medium px-4 py-2.5">Rate</th>
                <th className="text-right font-medium px-4 py-2.5">Threshold</th>
              </tr>
            </thead>
            <tbody>
              {items.map((w) => (
                <tr key={w.id} className="border-b border-hairline last:border-0">
                  <td className="px-4 py-2.5 text-ink font-medium">{w.name}</td>
                  <td className="px-4 py-2.5 text-steel font-mono text-caption">{w.category || '—'}</td>
                  <td className="px-4 py-2.5 text-right num text-ink">{w.rate}%</td>
                  <td className="px-4 py-2.5 text-right num text-stone">{w.threshold ? Number(w.threshold).toLocaleString('id-ID') : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}

      {createOpen && (
        <CreateWithholdingDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => { void qc.invalidateQueries({ queryKey: ['withholding-tax-types'] }); setCreateOpen(false); }}
        />
      )}
    </div>
  );
}

function CreateWithholdingDialog({
  onClose, onCreated,
}: { onClose: () => void; onCreated: () => void }) {
  const { data: acc } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api<AccountList>('/accounting/accounts'),
  });
  const accountOpts = useMemo(
    () => (acc?.items ?? []).map((a) => ({
      value: a.id,
      label: a.account_number ? `${a.account_number} — ${a.account_name}` : a.account_name,
    })),
    [acc],
  );

  const [name, setName]           = useState('PPh 23 — Services 2%');
  const [category, setCategory]   = useState('pph-23');
  const [rate, setRate]           = useState('2');
  const [threshold, setThreshold] = useState('');
  const [accountId, setAccountId] = useState('');
  const [error, setError]         = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<WithholdingType>('/accounting/withholding-tax-types', {
      method: 'POST',
      body: {
        name,
        ...(category ? { category } : {}),
        rate,
        ...(threshold ? { threshold } : {}),
        account_id: accountId,
      },
    }),
    onSuccess: () => onCreated(),
    onError: (e: Error) => setError(e.message),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!name.trim()) return setError('Name is required.');
    if (!accountId)   return setError('Pick a GL account.');
    if (Number.isNaN(Number(rate))) return setError('Rate must be a number.');
    mut.mutate();
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New withholding tax type</DialogTitle>
        <DialogDescription>
          The category drives reporting bucketing on PPh forms.
        </DialogDescription>

        <form onSubmit={submit} className="mt-4 grid sm:grid-cols-2 gap-3">
          <Field label="Name" htmlFor="wht-name">
            <Input id="wht-name" value={name} onChange={(e) => setName(e.target.value)} required />
          </Field>
          <Field label="Category">
            <NativeSelect value={category} onChange={setCategory} options={WHT_CATEGORIES} />
          </Field>
          <Field label="Rate (%)">
            <Input value={rate} onChange={(e) => setRate(e.target.value)} className="num text-right" />
          </Field>
          <Field label="Threshold" hint="Skip withholding below this amount. Leave empty for none.">
            <Input value={threshold} onChange={(e) => setThreshold(e.target.value)} className="num text-right" />
          </Field>
          <Field label="GL account" htmlFor="wht-acc">
            <NativeSelect value={accountId} onChange={setAccountId} options={accountOpts} placeholder="Select account…" />
          </Field>

          {error && (
            <div className="sm:col-span-2 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="sm:col-span-2 flex justify-end gap-2 mt-2">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create type</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* =========================== Categories tab =========================== */

function CategoriesTab() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['tax-categories'],
    queryFn: () => api<TaxCategoryList>('/accounting/tax-categories'),
  });
  const items = data?.items ?? [];
  const [createOpen, setCreateOpen] = useState(false);

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Tax categories</CardTitle>
          <CardDescription>
            Optional groupings — useful for reporting (e.g. PKP / Non-PKP, Domestic / Export).
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New category
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-20 w-full" /></Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Tag className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No tax categories yet.</div>
          </div>
        </Card>
      ) : (
        <Card padded={false}>
          <ul className="divide-y divide-hairline">
            {items.map((c) => (
              <li key={c.id} className="px-4 py-3">
                <div className="text-body-sm font-medium text-ink">{c.name}</div>
                {c.description && <div className="text-caption text-stone mt-0.5">{c.description}</div>}
              </li>
            ))}
          </ul>
        </Card>
      )}

      {createOpen && (
        <CreateCategoryDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => { void qc.invalidateQueries({ queryKey: ['tax-categories'] }); setCreateOpen(false); }}
        />
      )}
    </div>
  );
}

function CreateCategoryDialog({
  onClose, onCreated,
}: { onClose: () => void; onCreated: () => void }) {
  const [name, setName]               = useState('');
  const [description, setDescription] = useState('');
  const [error, setError]             = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<TaxCategory>('/accounting/tax-categories', {
      method: 'POST',
      body: { name, ...(description ? { description } : {}) },
    }),
    onSuccess: () => onCreated(),
    onError: (e: Error) => setError(e.message),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!name.trim()) return setError('Name is required.');
    mut.mutate();
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New tax category</DialogTitle>

        <form onSubmit={submit} className="mt-4 space-y-3">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} required />
          </Field>
          <Field label="Description" hint="Optional. Shown to users picking a category.">
            <Input value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ----------------------------- shared --------------------------------- */

function NativeSelect({
  value, options, onChange, placeholder, className,
}: {
  value: string;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
  placeholder?: string;
  className?: string;
}) {
  return (
    <select
      className={cn(
        'input-base appearance-none pr-8 bg-no-repeat bg-[right_0.75rem_center] bg-[length:1.25rem] cursor-pointer',
        className,
      )}
      style={{ backgroundImage: "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>\")" }}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {placeholder && <option value="">{placeholder}</option>}
      {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  );
}

function ToggleField({
  label, left, right, value, onChange,
}: {
  label: string; left: string; right: string;
  value: boolean; onChange: (v: boolean) => void;
}) {
  return (
    <div>
      <div className="label-base mb-1">{label}</div>
      <div className="inline-flex items-center p-1 rounded-full bg-surface border border-hairline">
        <button type="button" onClick={() => onChange(true)} className={cn(
          'inline-flex items-center h-7 px-3 rounded-full text-caption transition-colors',
          value ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink',
        )}>{left}</button>
        <button type="button" onClick={() => onChange(false)} className={cn(
          'inline-flex items-center h-7 px-3 rounded-full text-caption transition-colors',
          !value ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink',
        )}>{right}</button>
      </div>
    </div>
  );
}
