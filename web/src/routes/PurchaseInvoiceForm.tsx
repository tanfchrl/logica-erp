import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import Decimal from 'decimal.js';
import {
  Plus, Trash2, Send, Save, Ban, ArrowLeft, AlertCircle, Printer,
} from 'lucide-react';
import { motion } from 'framer-motion';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Combobox } from '@/components/Combobox';
import { NumericInput } from '@/components/NumericInput';
import { DocstatusPill } from '@/components/StatusPill';
import { ApprovalWidget } from '@/components/ApprovalWidget';
import { Kbd } from '@/components/Kbd';
import { api, apiBlob } from '@/lib/api';
import { money } from '@/lib/format';
import { toast } from '@/components/Toaster';

/* ---- types matching the PI backend ---- */
interface PurchaseInvoice {
  id: string; name: string; company_id: string; supplier_id: string;
  posting_date: string; due_date: string; currency: string; exchange_rate: string;
  tax_template_id?: string; supplier_invoice_no?: string; supplier_invoice_date?: string; bill_no?: string;
  net_total: string; total_taxes_and_charges: string; grand_total: string;
  paid_amount: string; outstanding_amount: string;
  payable_account_id: string; remarks?: string;
  docstatus: 0 | 1 | 2;
  items: Array<{
    id: string; row_index: number; item_id?: string;
    item_code: string; item_name: string; description?: string;
    qty: string; uom: string; rate: string; amount: string;
    expense_account_id: string;
    tax_amount: string; total: string;
  }>;
  taxes?: Array<{ id: string; description: string; rate: string; tax_amount: string }>;
}
interface Supplier  { id: string; name: string; display_name: string }
interface Item      { id: string; code: string; name: string; stock_uom: string; standard_rate: string }
interface Account   { id: string; name: string; account_number?: string; account_name?: string; root_type?: string; is_group?: boolean }
interface TaxTpl    { id: string; name: string; is_sales: boolean }
interface DraftLine {
  rowId: string;
  item_id?: string;
  item_code: string;
  item_name: string;
  qty: string;
  rate: string;
  uom: string;
  expense_account_id: string;
  description: string;
}

const todayISO = () => new Date().toISOString().slice(0, 10);
const isoPlusDays = (d: number) => {
  const dt = new Date(); dt.setDate(dt.getDate() + d); return dt.toISOString().slice(0, 10);
};
const rid = () => Math.random().toString(36).slice(2);

export function PurchaseInvoiceForm() {
  const { id } = useParams({ strict: false }) as { id?: string };
  const navigate = useNavigate();
  const qc = useQueryClient();
  const isNew = !id || id === 'new';

  /* ---- queries ---- */
  const { data: existing } = useQuery({
    queryKey: ['pi', id],
    queryFn:  () => api<PurchaseInvoice>(`/accounting/purchase-invoices/${id}`),
    enabled:  !isNew,
  });
  // NOTE: queryFns return the raw {items: [...]} wrapper to stay cache-compatible
  // with other routes that use the same queryKeys (Dashboard, SI form). Unwrap at render.
  const { data: suppliersResp } = useQuery({
    queryKey: ['suppliers'],
    queryFn:  () => api<{ items: Supplier[] }>('/accounting/suppliers'),
  });
  const { data: itemsResp } = useQuery({
    queryKey: ['items'],
    queryFn:  () => api<{ items: Item[] }>('/accounting/items'),
  });
  const { data: taxResp } = useQuery({
    queryKey: ['tax-templates'],
    queryFn:  () => api<{ items: TaxTpl[] }>('/accounting/tax-templates'),
  });
  const { data: accountsResp } = useQuery({
    queryKey: ['accounts'],
    queryFn:  () => api<{ items: Account[] }>('/accounting/accounts'),
  });

  const suppliers = suppliersResp?.items;
  const items     = itemsResp?.items;
  const taxTpls   = useMemo(
    () => (taxResp?.items ?? []).filter((t) => !t.is_sales),
    [taxResp],
  );
  const accounts  = accountsResp?.items;

  // Only leaf accounts (not group/parent) can be posted to. Filter for postability.
  const expenseAccounts = useMemo(
    () => (accounts ?? []).filter((a) => a.root_type === 'expense' && !a.is_group),
    [accounts],
  );
  const payableAccounts = useMemo(
    () => (accounts ?? []).filter((a) => a.root_type === 'liability' && !a.is_group),
    [accounts],
  );

  /* ---- form state ---- */
  const [supplierID, setSupplierID]             = useState<string>('');
  const [postingDate, setPostingDate]           = useState(todayISO());
  const [dueDate, setDueDate]                   = useState(isoPlusDays(30));
  const [supplierInvoiceNo, setSIN]             = useState('');
  const [supplierInvoiceDate, setSID]           = useState('');
  const [billNo, setBillNo]                     = useState('');
  const [taxTemplateID, setTaxTemplateID]       = useState<string | null>(null);
  const [payableAccountID, setPayableAccountID] = useState<string>('');
  const [remarks, setRemarks]                   = useState('');
  const [lines, setLines]                       = useState<DraftLine[]>([
    { rowId: rid(), item_code: '', item_name: '', qty: '1', rate: '0', uom: 'Unit', expense_account_id: '', description: '' },
  ]);
  const [err, setErr] = useState<string | null>(null);

  /* ---- hydrate from existing ---- */
  useEffect(() => {
    if (!existing) return;
    setSupplierID(existing.supplier_id);
    setPostingDate(existing.posting_date.slice(0, 10));
    setDueDate(existing.due_date.slice(0, 10));
    setSIN(existing.supplier_invoice_no ?? '');
    setSID(existing.supplier_invoice_date ? existing.supplier_invoice_date.slice(0, 10) : '');
    setBillNo(existing.bill_no ?? '');
    setTaxTemplateID(existing.tax_template_id ?? null);
    setPayableAccountID(existing.payable_account_id);
    setRemarks(existing.remarks ?? '');
    setLines(existing.items.map((it) => ({
      rowId: rid(),
      item_id: it.item_id,
      item_code: it.item_code,
      item_name: it.item_name,
      qty: it.qty, rate: it.rate, uom: it.uom,
      expense_account_id: it.expense_account_id,
      description: it.description ?? '',
    })));
  }, [existing]);

  // Default payable account: first liability account when creating new
  useEffect(() => {
    if (!isNew || payableAccountID || !payableAccounts.length) return;
    setPayableAccountID(payableAccounts[0]!.id);
  }, [isNew, payableAccounts, payableAccountID]);

  // Default tax template: first purchase template
  useEffect(() => {
    if (!isNew || taxTemplateID || !taxTpls) return;
    if (taxTpls[0]) setTaxTemplateID(taxTpls[0].id);
  }, [isNew, taxTpls, taxTemplateID]);

  const editable  = isNew || existing?.docstatus === 0;
  const submitted = existing?.docstatus === 1;
  const cancelled = existing?.docstatus === 2;

  /* ---- totals (preview only; server computes on submit) ---- */
  const totals = useMemo(() => {
    let net = new Decimal(0);
    for (const l of lines) {
      const q = new Decimal(l.qty || '0');
      const r = new Decimal(l.rate || '0');
      net = net.plus(q.mul(r));
    }
    // For preview, just show net; tax + grand is computed server-side via template
    return { net, grand: net };
  }, [lines]);

  // Client-side validation. Returns a human list of issues, or [] if clean.
  function validate(): string[] {
    const issues: string[] = [];
    if (!supplierID)       issues.push('Supplier is required.');
    if (!payableAccountID) issues.push('Payable account is required.');
    if (!postingDate)      issues.push('Posting date is required.');
    if (lines.length === 0) issues.push('Add at least one line item.');
    lines.forEach((l, i) => {
      const n = i + 1;
      if (!l.item_code)          issues.push(`Line ${n}: item is required.`);
      if (!l.expense_account_id) issues.push(`Line ${n}: expense account is required.`);
      const qty = Number(l.qty);
      if (!Number.isFinite(qty) || qty <= 0) issues.push(`Line ${n}: quantity must be > 0.`);
      const rate = Number(l.rate);
      if (!Number.isFinite(rate) || rate < 0) issues.push(`Line ${n}: rate must be ≥ 0.`);
    });
    return issues;
  }

  /* ---- mutations ---- */
  const createMutation = useMutation({
    mutationFn: () => api<PurchaseInvoice>('/accounting/purchase-invoices', {
      method: 'POST',
      body: {
        supplier_id: supplierID,
        posting_date: postingDate,
        due_date: dueDate,
        supplier_invoice_no: supplierInvoiceNo || undefined,
        supplier_invoice_date: supplierInvoiceDate || undefined,
        bill_no: billNo || undefined,
        tax_template_id: taxTemplateID || undefined,
        payable_account_id: payableAccountID,
        remarks: remarks || undefined,
        items: lines.map((l) => ({
          item_id: l.item_id,
          item_code: l.item_code,
          item_name: l.item_name || l.item_code,
          qty: l.qty,
          rate: l.rate,
          uom: l.uom || 'Unit',
          expense_account_id: l.expense_account_id,
          description: l.description || undefined,
        })),
      },
    }),
    onSuccess: (pi) => {
      toast.success(`Saved ${pi.name}`);
      void qc.invalidateQueries({ queryKey: ['list', 'purchase-invoices'] });
      void navigate({ to: `/accounting/purchase-invoices/${pi.id}` as never });
    },
    onError: (e: { message?: string; fields?: Record<string, string> } & Error) => {
      const msg = e?.message ?? 'Server rejected the request.';
      setErr(msg);
      toast.error('Could not save draft', msg);
    },
  });

  function onSaveDraft() {
    setErr(null);
    const issues = validate();
    if (issues.length > 0) {
      setErr(issues.join(' '));
      toast.error('Fix these first', issues[0]);
      return;
    }
    createMutation.mutate();
  }

  const submitMutation = useMutation({
    mutationFn: () => api<PurchaseInvoice>(`/accounting/purchase-invoices/${id}/submit`, { method: 'POST' }),
    onSuccess: () => { toast.success('Submitted'); void qc.invalidateQueries({ queryKey: ['pi', id] }); },
    onError: (e: Error) => setErr(e.message),
  });

  const cancelMutation = useMutation({
    mutationFn: () => api<PurchaseInvoice>(`/accounting/purchase-invoices/${id}/cancel`, { method: 'POST' }),
    onSuccess: () => { toast.warning('Cancelled — reversing GL entries posted'); void qc.invalidateQueries({ queryKey: ['pi', id] }); },
    onError: (e: Error) => setErr(e.message),
  });

  async function onPrint() {
    if (!id || isNew) return;
    try {
      const blob = await apiBlob(`/accounting/purchase-invoices/${id}/print`);
      const url  = URL.createObjectURL(blob);
      const a    = document.createElement('a');
      a.href     = url;
      a.target   = '_blank';
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch (e) {
      setErr((e as Error).message);
    }
  }

  const title = isNew
    ? <>New Purchase Invoice</>
    : <span className="flex items-center gap-3"><span>{existing?.name ?? '…'}</span></span>;

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Finance', to: '/accounting' },
          { label: 'Purchase Invoices', to: '/accounting/purchase-invoices' },
          { label: isNew ? 'New' : (existing?.name ?? '…') },
        ]}
        title={title}
        status={existing && <DocstatusPill docstatus={existing.docstatus} />}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/accounting/purchase-invoices' as never}><ArrowLeft className="size-4" /> Back</Link>
            </Button>
            {!isNew && submitted && (
              <Button variant="secondary" onClick={onPrint}><Printer className="size-4" /> Print PDF</Button>
            )}
            {editable && isNew && (
              <Button onClick={onSaveDraft} loading={createMutation.isPending}>
                <Save className="size-4" /> Save draft <Kbd>⌘S</Kbd>
              </Button>
            )}
            {editable && !isNew && (
              <Button onClick={() => submitMutation.mutate()} loading={submitMutation.isPending}>
                <Send className="size-4" /> Submit
              </Button>
            )}
            {submitted && existing?.paid_amount === '0' && (
              <Button variant="danger" onClick={() => cancelMutation.mutate()} loading={cancelMutation.isPending}>
                <Ban className="size-4" /> Cancel
              </Button>
            )}
          </>
        }
      />

      <motion.div
        initial={{ opacity: 0, y: 4 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.15 }}
        className="flex-1 px-6 lg:px-8 pb-8 grid grid-cols-1 lg:grid-cols-[1fr,320px] gap-4 max-w-[1400px]"
      >
        <div className="space-y-4 min-w-0">
          {cancelled && (
            <Card className="!p-3 flex items-center gap-3 bg-danger/5 border-danger/30">
              <AlertCircle className="size-4 text-danger" />
              <span className="text-body text-danger">This invoice has been cancelled. The reversing GL entries are visible alongside the originals in reports.</span>
            </Card>
          )}

          {!isNew && existing && (
            <ApprovalWidget doctype="purchase_invoice" documentId={existing.id} />
          )}

          {err && (
            <Card className="!p-3 bg-brand-error/5 border-brand-error/30 flex items-start gap-2">
              <AlertCircle className="size-4 text-brand-error mt-0.5 shrink-0" />
              <div className="text-body-sm text-brand-error">{err}</div>
            </Card>
          )}

          <Card>
            <CardTitle>Bill from</CardTitle>
            <div className="mt-4 grid sm:grid-cols-2 gap-4">
              <Field label="Supplier" hint="Required.">
                <Combobox
                  value={supplierID || null}
                  options={(suppliers ?? []).map((s) => ({ value: s.id, label: s.display_name, hint: s.name }))}
                  onChange={(v) => setSupplierID(v ?? '')}
                  placeholder="Pick a supplier"
                  disabled={!editable}
                />
              </Field>
              <Field label="Tax template" hint="Defaults to first purchase template.">
                <Combobox
                  value={taxTemplateID}
                  options={(taxTpls ?? []).map((t) => ({ value: t.id, label: t.name }))}
                  onChange={setTaxTemplateID}
                  placeholder="None"
                  disabled={!editable}
                />
              </Field>
              <Field label="Supplier invoice no">
                <Input value={supplierInvoiceNo} onChange={(e) => setSIN(e.target.value)} disabled={!editable} placeholder="Supplier's ref" />
              </Field>
              <Field label="Supplier invoice date">
                <Input type="date" value={supplierInvoiceDate} onChange={(e) => setSID(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Bill no">
                <Input value={billNo} onChange={(e) => setBillNo(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Payable account" hint="Liability account to credit.">
                <Combobox
                  value={payableAccountID || null}
                  options={payableAccounts.map((a) => ({
                    value: a.id,
                    label: a.account_number ? `${a.account_number} — ${a.name}` : a.name,
                  }))}
                  onChange={(v) => setPayableAccountID(v ?? '')}
                  disabled={!editable}
                />
              </Field>
              <Field label="Posting date">
                <Input type="date" value={postingDate} onChange={(e) => setPostingDate(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Due date">
                <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} disabled={!editable} />
              </Field>
            </div>
          </Card>

          <Card>
            <div className="flex items-center justify-between mb-3">
              <CardTitle>Line items</CardTitle>
              {editable && (
                <Button size="sm" variant="secondary" onClick={() =>
                  setLines((prev) => [...prev, { rowId: rid(), item_code: '', item_name: '', qty: '1', rate: '0', uom: 'Unit', expense_account_id: '', description: '' }])}>
                  <Plus className="size-3.5" /> Add line
                </Button>
              )}
            </div>

            <div className="overflow-x-auto -mx-5">
              <table className="w-full text-body-sm">
                <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone">
                  <tr>
                    <th className="text-left font-medium px-3 py-2 w-[220px]">Item</th>
                    <th className="text-left font-medium px-3 py-2">Expense account</th>
                    <th className="text-right font-medium px-3 py-2 w-[80px]">Qty</th>
                    <th className="text-right font-medium px-3 py-2 w-[140px]">Rate</th>
                    <th className="text-right font-medium px-3 py-2 w-[140px]">Amount</th>
                    <th className="px-3 py-2 w-[40px]"></th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l, i) => {
                    const amount = new Decimal(l.qty || '0').mul(new Decimal(l.rate || '0'));
                    return (
                      <tr key={l.rowId} className="border-t border-hairline">
                        <td className="px-2 py-1">
                          <Combobox
                            value={l.item_id ?? null}
                            options={(items ?? []).map((it) => ({ value: it.id, label: it.code, hint: it.name }))}
                            onChange={(v) => {
                              const it = (items ?? []).find((x) => x.id === v);
                              setLines((prev) => prev.map((p, idx) => idx === i ? {
                                ...p,
                                item_id: it?.id, item_code: it?.code ?? '',
                                item_name: it?.name ?? '', uom: it?.stock_uom ?? p.uom,
                                rate: it?.standard_rate ?? p.rate,
                              } : p));
                            }}
                            placeholder="Pick an item"
                            disabled={!editable}
                          />
                          {l.description && (
                            <Input className="!h-7 !text-[12px] mt-1" value={l.description} disabled={!editable}
                              onChange={(e) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, description: e.target.value } : p))} />
                          )}
                        </td>
                        <td className="px-2 py-1">
                          <Combobox
                            value={l.expense_account_id || null}
                            options={expenseAccounts.map((a) => ({
                              value: a.id,
                              label: a.account_number ? `${a.account_number} — ${a.name}` : a.name,
                            }))}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, expense_account_id: v ?? '' } : p))}
                            disabled={!editable}
                          />
                        </td>
                        <td className="px-2 py-1">
                          <NumericInput value={l.qty} disabled={!editable}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, qty: v } : p))} />
                        </td>
                        <td className="px-2 py-1">
                          <NumericInput value={l.rate} disabled={!editable}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, rate: v } : p))} />
                        </td>
                        <td className="px-2 py-1 text-right num text-ink">{money(amount.toString())}</td>
                        <td className="px-2 py-1 text-center">
                          {editable && (
                            <button type="button"
                              onClick={() => setLines((prev) => prev.filter((_, idx) => idx !== i))}
                              className="inline-flex items-center justify-center size-7 rounded-md text-stone hover:bg-surface hover:text-brand-error">
                              <Trash2 className="size-3.5" />
                            </button>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </Card>

          <Card>
            <Field label="Remarks">
              <textarea className="input-base !h-auto !py-2" rows={3}
                value={remarks} onChange={(e) => setRemarks(e.target.value)} disabled={!editable} />
            </Field>
          </Card>
        </div>

        {/* Right rail */}
        <div className="space-y-4">
          <Card>
            <CardTitle>Summary</CardTitle>
            <div className="mt-3 space-y-2 text-body-sm">
              <Row label="Net total"   value={existing ? money(existing.net_total)             : money(totals.net.toString())} />
              <Row label="Taxes"       value={existing ? money(existing.total_taxes_and_charges) : '—'} />
              <Row label="Grand total" value={existing ? money(existing.grand_total)           : money(totals.grand.toString())} highlight />
              {existing && (
                <>
                  <Row label="Paid"        value={money(existing.paid_amount)} />
                  <Row label="Outstanding" value={money(existing.outstanding_amount)} highlight />
                </>
              )}
            </div>
            <div className="mt-2 text-caption text-stone">
              {!existing && 'Server computes taxes via the selected template at submit.'}
            </div>
          </Card>

        </div>
      </motion.div>
    </>
  );
}

function Row({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-stone">{label}</span>
      <span className={'num text-right ' + (highlight ? 'text-ink font-semibold' : 'text-charcoal')}>{value}</span>
    </div>
  );
}
