import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import Decimal from 'decimal.js';
import {
  Plus, Trash2, Send, Save, Ban, ArrowLeft, AlertCircle, Printer,
  Pause, Play, CircleStop, Lock, ChevronRight, Package,
} from 'lucide-react';
import { motion } from 'framer-motion';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Combobox } from '@/components/Combobox';
import { NumericInput } from '@/components/NumericInput';
import { DocstatusPill, StatusPill } from '@/components/StatusPill';
import { ApprovalWidget } from '@/components/ApprovalWidget';
import { Timeline } from '@/components/Timeline';
import { Kbd } from '@/components/Kbd';
import { api, apiBlob } from '@/lib/api';
import { money } from '@/lib/format';
import { toast } from '@/components/Toaster';

/* ---- types matching the PO backend ---- */
interface PurchaseOrder {
  id: string; name: string; company_id: string; supplier_id: string;
  transaction_date: string; required_by_date?: string;
  currency: string; exchange_rate: string;
  tax_template_id?: string;
  net_total: string; total_taxes_and_charges: string; grand_total: string;
  remarks?: string;
  terms_and_conditions?: string;
  payment_terms?: string;
  status: string;
  docstatus: 0 | 1 | 2;
  items: Array<{
    id: string; row_index: number; item_id?: string;
    item_code: string; item_name: string; description?: string;
    qty: string; uom: string; rate: string; amount: string;
    warehouse_id?: string;
    required_by_date?: string;
    tax_amount: string; total: string;
    received_qty: string; billed_qty: string;
  }>;
  taxes?: Array<{ id: string; description: string; rate: string; tax_amount: string }>;
}
interface Supplier  { id: string; name: string; display_name: string }
interface Item      { id: string; code: string; name: string; stock_uom: string; standard_rate: string }
interface Warehouse { id: string; code: string; name: string; is_group?: boolean }
interface TaxTpl    { id: string; name: string; is_sales: boolean }
interface DraftLine {
  rowId: string;
  item_id?: string;
  item_code: string;
  item_name: string;
  qty: string;
  rate: string;
  uom: string;
  warehouse_id: string;
  description: string;
  required_by_date: string;
}

const todayISO = () => new Date().toISOString().slice(0, 10);
const isoPlusDays = (d: number) => {
  const dt = new Date(); dt.setDate(dt.getDate() + d); return dt.toISOString().slice(0, 10);
};
const rid = () => Math.random().toString(36).slice(2);

export function PurchaseOrderForm() {
  const { id } = useParams({ strict: false }) as { id?: string };
  const navigate = useNavigate();
  const qc = useQueryClient();
  const isNew = !id || id === 'new';

  /* ---- queries ---- */
  const { data: existing } = useQuery({
    queryKey: ['po', id],
    queryFn:  () => api<PurchaseOrder>(`/accounting/purchase-orders/${id}`),
    enabled:  !isNew,
  });
  const { data: suppliersResp } = useQuery({
    queryKey: ['suppliers'],
    queryFn:  () => api<{ items: Supplier[] }>('/accounting/suppliers'),
  });
  const { data: itemsResp } = useQuery({
    queryKey: ['items'],
    queryFn:  () => api<{ items: Item[] }>('/accounting/items'),
  });
  const { data: warehousesResp } = useQuery({
    queryKey: ['warehouses'],
    queryFn:  () => api<{ items: Warehouse[] }>('/stock/warehouses'),
  });
  const { data: taxResp } = useQuery({
    queryKey: ['tax-templates'],
    queryFn:  () => api<{ items: TaxTpl[] }>('/accounting/tax-templates'),
  });

  const suppliers   = suppliersResp?.items;
  const items       = itemsResp?.items;
  const warehouses  = useMemo(
    () => (warehousesResp?.items ?? []).filter((w) => !w.is_group),
    [warehousesResp],
  );
  const taxTpls     = useMemo(
    () => (taxResp?.items ?? []).filter((t) => !t.is_sales),
    [taxResp],
  );

  /* ---- form state ---- */
  const [supplierID, setSupplierID]         = useState<string>('');
  const [transactionDate, setTxDate]        = useState(todayISO());
  const [requiredBy, setRequiredBy]         = useState(isoPlusDays(14));
  const [taxTemplateID, setTaxTemplateID]   = useState<string | null>(null);
  const [remarks, setRemarks]               = useState('');
  const [terms, setTerms]                   = useState('');
  const [paymentTerms, setPaymentTerms]     = useState('');
  const [lines, setLines] = useState<DraftLine[]>([
    { rowId: rid(), item_code: '', item_name: '', qty: '1', rate: '0', uom: 'Unit', warehouse_id: '', description: '', required_by_date: '' },
  ]);
  const [err, setErr] = useState<string | null>(null);

  /* ---- hydrate from existing ---- */
  useEffect(() => {
    if (!existing) return;
    setSupplierID(existing.supplier_id);
    setTxDate(existing.transaction_date.slice(0, 10));
    setRequiredBy(existing.required_by_date ? existing.required_by_date.slice(0, 10) : '');
    setTaxTemplateID(existing.tax_template_id ?? null);
    setRemarks(existing.remarks ?? '');
    setTerms(existing.terms_and_conditions ?? '');
    setPaymentTerms(existing.payment_terms ?? '');
    setLines(existing.items.map((it) => ({
      rowId: rid(),
      item_id: it.item_id,
      item_code: it.item_code,
      item_name: it.item_name,
      qty: it.qty, rate: it.rate, uom: it.uom,
      warehouse_id: it.warehouse_id ?? '',
      description: it.description ?? '',
      required_by_date: it.required_by_date ? it.required_by_date.slice(0, 10) : '',
    })));
  }, [existing]);

  // First purchase template as default when creating new.
  useEffect(() => {
    if (!isNew || taxTemplateID || !taxTpls) return;
    if (taxTpls[0]) setTaxTemplateID(taxTpls[0].id);
  }, [isNew, taxTpls, taxTemplateID]);

  const editable  = isNew || existing?.docstatus === 0;
  const submitted = existing?.docstatus === 1;
  const cancelled = existing?.docstatus === 2;
  const status    = existing?.status ?? 'Draft';

  // Hold/Close/Stop only make sense in active submitted states.
  // GRN action only shows when there's something still left to receive.
  const canRaiseGRN = submitted && existing != null
    && (status === 'To Receive and Bill' || status === 'To Receive')
    && existing.items.some((l) => Number(l.received_qty) < Number(l.qty));
  const canHold   = submitted && (status === 'To Receive and Bill' || status === 'To Receive' || status === 'To Bill');
  const canClose  = submitted && status !== 'Closed'  && status !== 'Completed' && status !== 'Cancelled';
  const canStop   = submitted && status !== 'Stopped' && status !== 'Completed' && status !== 'Cancelled';
  const canReopen = submitted && (status === 'On Hold' || status === 'Closed' || status === 'Stopped');

  /* ---- totals preview (server is authoritative — tax template runs there) ---- */
  const totals = useMemo(() => {
    let net = new Decimal(0);
    for (const l of lines) {
      const q = new Decimal(l.qty || '0');
      const r = new Decimal(l.rate || '0');
      net = net.plus(q.mul(r));
    }
    return { net, grand: net };
  }, [lines]);

  function validate(): string[] {
    const issues: string[] = [];
    if (!supplierID) issues.push('Supplier is required.');
    if (!transactionDate) issues.push('Transaction date is required.');
    if (lines.length === 0) issues.push('Add at least one line item.');
    lines.forEach((l, i) => {
      const n = i + 1;
      if (!l.item_code) issues.push(`Line ${n}: item is required.`);
      const qty = Number(l.qty);
      if (!Number.isFinite(qty) || qty <= 0) issues.push(`Line ${n}: quantity must be > 0.`);
      const rate = Number(l.rate);
      if (!Number.isFinite(rate) || rate < 0) issues.push(`Line ${n}: rate must be ≥ 0.`);
    });
    return issues;
  }

  /* ---- mutations ---- */
  const createMutation = useMutation({
    mutationFn: () => api<PurchaseOrder>('/accounting/purchase-orders', {
      method: 'POST',
      body: {
        supplier_id: supplierID,
        transaction_date: transactionDate,
        required_by_date: requiredBy || undefined,
        tax_template_id: taxTemplateID || undefined,
        remarks: remarks || undefined,
        terms_and_conditions: terms || undefined,
        payment_terms: paymentTerms || undefined,
        items: lines.map((l) => ({
          item_id: l.item_id,
          item_code: l.item_code,
          item_name: l.item_name || l.item_code,
          qty: l.qty,
          rate: l.rate,
          uom: l.uom || 'Unit',
          warehouse_id: l.warehouse_id || undefined,
          description: l.description || undefined,
          required_by_date: l.required_by_date || undefined,
        })),
      },
    }),
    onSuccess: (po) => {
      toast.success(`Saved ${po.name}`);
      void qc.invalidateQueries({ queryKey: ['list', 'purchase-orders'] });
      void navigate({ to: `/accounting/purchase-orders/${po.id}` as never });
    },
    onError: (e: Error) => {
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

  // Lifecycle mutations are deliberately small — they POST and refetch.
  const lifecycle = (path: string, successToast: string) => useMutation({
    mutationFn: () => api<PurchaseOrder>(`/accounting/purchase-orders/${id}/${path}`, { method: 'POST' }),
    onSuccess: () => { toast.success(successToast); void qc.invalidateQueries({ queryKey: ['po', id] }); },
    onError:   (e: Error) => setErr(e.message),
  });

  const submitMutation = lifecycle('submit', 'Submitted — commitment recorded');
  const cancelMutation = lifecycle('cancel', 'Cancelled');
  const holdMutation   = lifecycle('hold',   'On hold');
  const closeMutation  = lifecycle('close',  'Closed');
  const stopMutation   = lifecycle('stop',   'Stopped');
  const reopenMutation = lifecycle('reopen', 'Reopened');

  async function onPrint() {
    if (!id || isNew) return;
    try {
      const blob = await apiBlob(`/accounting/purchase-orders/${id}/print`);
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
    ? <>New Purchase Order</>
    : <span className="flex items-center gap-3"><span>{existing?.name ?? '…'}</span></span>;

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Procurement', to: '/buying' },
          { label: 'Purchase Orders', to: '/accounting/purchase-orders' },
          { label: isNew ? 'New' : (existing?.name ?? '…') },
        ]}
        title={title}
        status={existing && (
          <div className="flex items-center gap-2">
            <DocstatusPill docstatus={existing.docstatus} />
            <POStatusPill value={status} />
          </div>
        )}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/accounting/purchase-orders' as never}><ArrowLeft className="size-4" /> Back</Link>
            </Button>
            {!isNew && submitted && (
              <Button variant="secondary" onClick={onPrint}><Printer className="size-4" /> Print PDF</Button>
            )}
            {canRaiseGRN && (
              <Button asChild>
                <Link to={'/stock/purchase-receipts/new' as never} search={{ po: existing!.id } as never}>
                  <Package className="size-4" /> Receive
                </Link>
              </Button>
            )}
            {submitted && existing && existing.items.some((l) => Number(l.billed_qty) < Number(l.qty)) && (
              <Button asChild variant="secondary">
                <Link to={'/accounting/purchase-invoices/new' as never} search={{ po: existing.id } as never}>
                  Bill
                </Link>
              </Button>
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
            {canHold && (
              <Button variant="secondary" onClick={() => holdMutation.mutate()} loading={holdMutation.isPending}>
                <Pause className="size-4" /> Hold
              </Button>
            )}
            {canReopen && (
              <Button variant="secondary" onClick={() => reopenMutation.mutate()} loading={reopenMutation.isPending}>
                <Play className="size-4" /> Reopen
              </Button>
            )}
            {canStop && status !== 'On Hold' && (
              <Button variant="secondary" onClick={() => stopMutation.mutate()} loading={stopMutation.isPending}>
                <CircleStop className="size-4" /> Stop
              </Button>
            )}
            {canClose && status !== 'On Hold' && (
              <Button variant="secondary" onClick={() => closeMutation.mutate()} loading={closeMutation.isPending}>
                <Lock className="size-4" /> Close
              </Button>
            )}
            {submitted && !hasFulfilment(existing) && (
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
        className="flex-1 px-6 lg:px-8 pt-6 pb-8 grid grid-cols-1 lg:grid-cols-[1fr,320px] gap-4 max-w-[1400px]"
      >
        <div className="space-y-4 min-w-0">
          {cancelled && (
            <Card className="!p-3 flex items-center gap-3 bg-danger/5 border-danger/30">
              <AlertCircle className="size-4 text-danger" />
              <span className="text-body text-danger">This order has been cancelled.</span>
            </Card>
          )}

          {!isNew && existing && (
            <ApprovalWidget doctype="purchase_order" documentId={existing.id} />
          )}

          {err && (
            <Card className="!p-3 bg-brand-error/5 border-brand-error/30 flex items-start gap-2">
              <AlertCircle className="size-4 text-brand-error mt-0.5 shrink-0" />
              <div className="text-body-sm text-brand-error">{err}</div>
            </Card>
          )}

          <Card>
            <CardTitle>Order to</CardTitle>
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
              <Field label="Tax template" hint="Optional — defaults to the supplier's purchase template.">
                <Combobox
                  value={taxTemplateID}
                  options={(taxTpls ?? []).map((t) => ({ value: t.id, label: t.name }))}
                  onChange={setTaxTemplateID}
                  placeholder="None"
                  disabled={!editable}
                />
              </Field>
              <Field label="Order date">
                <Input type="date" value={transactionDate} onChange={(e) => setTxDate(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Required by" hint="When you need the goods delivered.">
                <Input type="date" value={requiredBy} onChange={(e) => setRequiredBy(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Payment terms">
                <Input value={paymentTerms} onChange={(e) => setPaymentTerms(e.target.value)} disabled={!editable} placeholder="e.g. NET 30, COD" />
              </Field>
            </div>
          </Card>

          <Card>
            <div className="flex items-center justify-between mb-3">
              <CardTitle>Line items</CardTitle>
              {editable && (
                <Button size="sm" variant="secondary" onClick={() =>
                  setLines((prev) => [...prev, { rowId: rid(), item_code: '', item_name: '', qty: '1', rate: '0', uom: 'Unit', warehouse_id: '', description: '', required_by_date: '' }])}>
                  <Plus className="size-3.5" /> Add line
                </Button>
              )}
            </div>

            <div className="overflow-x-auto -mx-5">
              <table className="w-full text-body-sm">
                <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone">
                  <tr>
                    <th className="text-left font-medium px-3 py-2 w-[220px]">Item</th>
                    <th className="text-left font-medium px-3 py-2 w-[160px]">Warehouse</th>
                    <th className="text-right font-medium px-3 py-2 w-[80px]">Qty</th>
                    <th className="text-right font-medium px-3 py-2 w-[140px]">Rate</th>
                    <th className="text-right font-medium px-3 py-2 w-[140px]">Amount</th>
                    {submitted && <th className="text-right font-medium px-3 py-2 w-[110px]">Recv / Bill</th>}
                    <th className="px-3 py-2 w-[40px]"></th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l, i) => {
                    const amount = new Decimal(l.qty || '0').mul(new Decimal(l.rate || '0'));
                    const existingLine = existing?.items[i];
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
                            value={l.warehouse_id || null}
                            options={warehouses.map((w) => ({ value: w.id, label: w.code, hint: w.name }))}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, warehouse_id: v ?? '' } : p))}
                            placeholder="Default"
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
                        {submitted && (
                          <td className="px-2 py-1 text-right text-caption text-stone num">
                            {existingLine
                              ? <>{existingLine.received_qty} <ChevronRight className="inline size-2.5" /> {existingLine.billed_qty}</>
                              : '—'}
                          </td>
                        )}
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
            <Field label="Terms &amp; conditions">
              <textarea className="input-base !h-auto !py-2 font-mono text-[12px]" rows={4}
                value={terms} onChange={(e) => setTerms(e.target.value)} disabled={!editable}
                placeholder="Delivery terms, warranty, return policy, etc." />
            </Field>
            <Field label="Remarks">
              <textarea className="input-base !h-auto !py-2" rows={2}
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
            </div>
            <div className="mt-2 text-caption text-stone">
              {!existing && 'Server computes taxes via the selected template at submit.'}
              {existing && submitted && 'Status updates automatically as receipts and invoices are recorded.'}
            </div>
          </Card>

          {existing && <Timeline doctype="purchase_order" documentId={existing.id} />}
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

function POStatusPill({ value }: { value: string }) {
  switch (value) {
    case 'Draft':                return <StatusPill tone="neutral" withDot={false}>Draft</StatusPill>;
    case 'To Receive and Bill':  return <StatusPill tone="warning" withDot={false}>To Receive &amp; Bill</StatusPill>;
    case 'To Receive':           return <StatusPill tone="info"    withDot={false}>To Receive</StatusPill>;
    case 'To Bill':              return <StatusPill tone="info"    withDot={false}>To Bill</StatusPill>;
    case 'Completed':            return <StatusPill tone="success" withDot={false}>Completed</StatusPill>;
    case 'On Hold':              return <StatusPill tone="warning" withDot={false}>On Hold</StatusPill>;
    case 'Closed':               return <StatusPill tone="neutral" withDot={false}>Closed</StatusPill>;
    case 'Stopped':              return <StatusPill tone="danger"  withDot={false}>Stopped</StatusPill>;
    case 'Cancelled':            return <StatusPill tone="danger"  withDot={false}>Cancelled</StatusPill>;
    default:                     return <StatusPill tone="neutral" withDot={false}>{value || '—'}</StatusPill>;
  }
}

// True if any line on the PO already has receipt or bill qty against it.
// The Cancel button is hidden in that case — fulfilment must be reversed
// downstream first.
function hasFulfilment(po: PurchaseOrder | undefined): boolean {
  if (!po) return false;
  return po.items.some((l) => Number(l.received_qty) > 0 || Number(l.billed_qty) > 0);
}
