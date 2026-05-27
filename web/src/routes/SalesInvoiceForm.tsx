import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import Decimal from 'decimal.js';
import {
  Plus, Trash2, Send, Save, Printer, Ban, FileText, ArrowLeft, AlertCircle,
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
import { Kbd } from '@/components/Kbd';
import { api } from '@/lib/api';
import { money } from '@/lib/format';
import { toast } from '@/components/Toaster';

// ------- Types matching the backend payloads -------
interface SalesInvoice {
  id: string; name: string; company_id: string; customer_id: string;
  posting_date: string; due_date: string; currency: string; exchange_rate: string;
  tax_template_id?: string; tax_invoice_number?: string;
  net_total: string; total_taxes_and_charges: string; grand_total: string;
  paid_amount: string; outstanding_amount: string;
  receivable_account_id: string;
  docstatus: 0 | 1 | 2;
  items: Array<{
    id: string; row_index: number; item_id?: string;
    item_code: string; item_name: string; description?: string;
    qty: string; uom: string; rate: string; amount: string;
    income_account_id: string;
    tax_amount: string; total: string;
  }>;
  taxes?: Array<{ id: string; description: string; rate: string; tax_amount: string }>;
}
interface Item     { id: string; code: string; name: string; stock_uom: string; standard_rate: string; }
interface Customer { id: string; name: string; display_name: string; }
interface TaxTpl   { id: string; name: string; is_sales: boolean; }
interface DraftLine {
  rowId: string;          // local row identifier (uuid-ish), not the server id
  item_id?: string;
  item_code: string;
  item_name: string;
  qty: string;            // raw decimal string
  rate: string;
  uom: string;
}

const todayISO = () => new Date().toISOString().slice(0, 10);
const isoPlusDays = (d: number) => {
  const dt = new Date(); dt.setDate(dt.getDate() + d); return dt.toISOString().slice(0, 10);
};
const rid = () => Math.random().toString(36).slice(2);

// ------------------------------------------------------------------
//   PAGE
// ------------------------------------------------------------------
export function SalesInvoiceForm() {
  // /accounting/sales-invoices/:id  → :id may be 'new' or an actual si_*
  const params = useParams({ strict: false }) as { id?: string };
  const id = params.id;
  const isNew = !id || id === 'new';
  const navigate = useNavigate();
  const qc = useQueryClient();

  // Existing invoice (when editing).
  const { data: existing } = useQuery({
    queryKey: ['sales-invoice', id],
    enabled: !isNew,
    queryFn: () => api<SalesInvoice>(`/accounting/sales-invoices/${id}`),
  });

  // Pickers data
  const { data: customers } = useQuery({
    queryKey: ['customers'],
    queryFn: () => api<{ items: Customer[] }>('/accounting/customers'),
  });
  const { data: items } = useQuery({
    queryKey: ['items'],
    queryFn: () => api<{ items: Item[] }>('/accounting/items'),
  });
  const { data: taxTpls } = useQuery({
    queryKey: ['tax-templates'],
    queryFn: () => api<{ items: TaxTpl[] }>('/accounting/tax-templates'),
  });

  // Form state
  const [customerId, setCustomerId] = useState<string | null>(null);
  const [postingDate, setPostingDate] = useState(todayISO());
  const [dueDate, setDueDate] = useState(isoPlusDays(30));
  const [taxTemplateId, setTaxTemplateId] = useState<string | null>(null);
  const [taxInvoiceNumber, setTaxInvoiceNumber] = useState('');
  const [remarks, setRemarks] = useState('');
  const [lines, setLines] = useState<DraftLine[]>([
    { rowId: rid(), item_code: '', item_name: '', qty: '1', rate: '', uom: 'Unit' },
  ]);

  // Hydrate from existing invoice (read-only display when submitted; editable when draft)
  useEffect(() => {
    if (!existing) return;
    setCustomerId(existing.customer_id);
    setPostingDate(existing.posting_date.slice(0, 10));
    setDueDate(existing.due_date.slice(0, 10));
    setTaxTemplateId(existing.tax_template_id ?? null);
    setTaxInvoiceNumber(existing.tax_invoice_number ?? '');
    setLines(existing.items.map((it) => ({
      rowId: it.id,
      item_id: it.item_id ?? undefined,
      item_code: it.item_code,
      item_name: it.item_name,
      qty: it.qty,
      rate: it.rate,
      uom: it.uom,
    })));
  }, [existing]);

  // Auto-default tax template from selected customer's default if any (cheap heuristic)
  useEffect(() => {
    if (!isNew || taxTemplateId || !taxTpls) return;
    const defaultSales = taxTpls.items.find((t) => t.is_sales);
    if (defaultSales) setTaxTemplateId(defaultSales.id);
  }, [isNew, taxTpls, taxTemplateId]);

  // Live totals (client-side preview; the server is authoritative on submit)
  const totals = useMemo(() => {
    const net = lines.reduce((acc, l) => {
      const q = new Decimal(l.qty || '0');
      const r = new Decimal(l.rate || '0');
      return acc.plus(q.times(r));
    }, new Decimal(0));
    const rate = taxTpls?.items.find((t) => t.id === taxTemplateId);
    // Best-effort: lookup the rate sum of the template's lines; we don't have it
    // client-side, so assume 11% PPN when a sales template is selected (consistent
    // with what we ship in the seed). The server recomputes precisely.
    const taxPercent = rate ? new Decimal('11') : new Decimal('0');
    const tax = net.times(taxPercent).dividedBy(100);
    const grand = net.plus(tax);
    return { net, tax, grand };
  }, [lines, taxTemplateId, taxTpls]);

  const customerOptions = useMemo(
    () => (customers?.items ?? []).map((c) => ({ value: c.id, label: c.display_name, description: c.name })),
    [customers],
  );
  const taxOptions = useMemo(
    () => (taxTpls?.items ?? []).filter((t) => t.is_sales).map((t) => ({ value: t.id, label: t.name })),
    [taxTpls],
  );

  const editable = isNew || existing?.docstatus === 0;
  const submitted = existing?.docstatus === 1;
  const cancelled = existing?.docstatus === 2;

  // ----- Mutations -----
  const createMutation = useMutation({
    mutationFn: async () => {
      const body = {
        customer_id: customerId,
        posting_date: postingDate,
        due_date: dueDate,
        tax_template_id: taxTemplateId || undefined,
        tax_invoice_number: taxInvoiceNumber || undefined,
        remarks: remarks || undefined,
        items: lines
          .filter((l) => l.item_code || l.item_id)
          .map((l) => ({
            item_id: l.item_id,
            item_code: l.item_code,
            item_name: l.item_name,
            qty: l.qty,
            rate: l.rate,
            uom: l.uom,
          })),
      };
      return api<SalesInvoice>('/accounting/sales-invoices', { method: 'POST', body });
    },
    onSuccess: (si) => {
      toast.success('Draft saved', si.name);
      qc.invalidateQueries({ queryKey: ['doctype', '/accounting/sales-invoices'] });
      navigate({ to: `/accounting/sales-invoices/${si.id}` as never });
    },
    onError: (e: any) => toast.error('Save failed', e.message || 'See API logs'),
  });

  const submitMutation = useMutation({
    mutationFn: () => api<SalesInvoice>(`/accounting/sales-invoices/${id}/submit`, { method: 'POST' }),
    onSuccess: () => {
      toast.success('Submitted', 'Posted to the General Ledger.');
      qc.invalidateQueries({ queryKey: ['sales-invoice', id] });
      qc.invalidateQueries({ queryKey: ['doctype', '/accounting/sales-invoices'] });
    },
    onError: (e: any) => toast.error('Submit failed', e.message),
  });

  const cancelMutation = useMutation({
    mutationFn: () => api<SalesInvoice>(`/accounting/sales-invoices/${id}/cancel`, { method: 'POST' }),
    onSuccess: () => {
      toast.success('Cancelled', 'Posted reversing GL entries.');
      qc.invalidateQueries({ queryKey: ['sales-invoice', id] });
      qc.invalidateQueries({ queryKey: ['doctype', '/accounting/sales-invoices'] });
    },
    onError: (e: any) => toast.error('Cancel failed', e.message),
  });

  // ----- Line helpers -----
  const addLine = () => setLines((ls) => [...ls, { rowId: rid(), item_code: '', item_name: '', qty: '1', rate: '', uom: 'Unit' }]);
  const removeLine = (rowId: string) => setLines((ls) => ls.length > 1 ? ls.filter((l) => l.rowId !== rowId) : ls);
  const updateLine = (rowId: string, patch: Partial<DraftLine>) =>
    setLines((ls) => ls.map((l) => l.rowId === rowId ? { ...l, ...patch } : l));
  const pickItem = (rowId: string, itemId: string | null) => {
    if (!itemId) { updateLine(rowId, { item_id: undefined, item_code: '', item_name: '' }); return; }
    const it = (items?.items ?? []).find((i) => i.id === itemId);
    if (!it) return;
    updateLine(rowId, {
      item_id: it.id, item_code: it.code, item_name: it.name,
      uom: it.stock_uom,
      rate: it.standard_rate && it.standard_rate !== '0' ? it.standard_rate : '',
    });
  };

  const onPrint = () => {
    if (!id || isNew) return;
    window.open(`/api/v1/accounting/sales-invoices/${id}/print`, '_blank');
  };

  const title = isNew
    ? <>New Sales Invoice</>
    : <span className="flex items-center gap-3"><span>{existing?.name ?? '…'}</span></span>;

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Finance', to: '/accounting' },
          { label: 'Sales Invoices', to: '/accounting/sales-invoices' },
          { label: isNew ? 'New' : (existing?.name ?? '…') },
        ]}
        title={title}
        status={existing && <DocstatusPill docstatus={existing.docstatus} />}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/accounting/sales-invoices' as never}><ArrowLeft className="size-4" /> Back</Link>
            </Button>
            {!isNew && submitted && (
              <Button variant="secondary" onClick={onPrint}><Printer className="size-4" /> Print PDF</Button>
            )}
            {editable && isNew && (
              <Button onClick={() => createMutation.mutate()} loading={createMutation.isPending}>
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
        className="flex-1 px-6 lg:px-8 pt-6 pb-8 grid grid-cols-1 lg:grid-cols-[1fr,320px] gap-4 max-w-[1400px]"
      >
        {/* MAIN COLUMN */}
        <div className="space-y-4 min-w-0">
          {cancelled && (
            <Card className="!p-3 flex items-center gap-3 bg-danger/5 border-danger/30">
              <AlertCircle className="size-4 text-danger" />
              <span className="text-body text-danger">This invoice has been cancelled. The reversing GL entries are visible alongside the originals in reports.</span>
            </Card>
          )}

          {!isNew && existing && (
            <ApprovalWidget doctype="sales_invoice" documentId={existing.id} />
          )}

          <Card>
            <CardTitle>Bill to</CardTitle>
            <div className="mt-4 grid sm:grid-cols-2 gap-4">
              <Field label="Customer" htmlFor="customer">
                <Combobox
                  id="customer"
                  options={customerOptions}
                  value={customerId}
                  onChange={setCustomerId}
                  placeholder="Select customer…"
                  disabled={!editable}
                />
              </Field>
              <Field label="Tax template" htmlFor="tax">
                <Combobox
                  id="tax"
                  options={taxOptions}
                  value={taxTemplateId}
                  onChange={setTaxTemplateId}
                  placeholder="None"
                  disabled={!editable}
                />
              </Field>
              <Field label="Posting date" htmlFor="pd">
                <Input id="pd" type="date" value={postingDate} onChange={(e) => setPostingDate(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Due date" htmlFor="dd">
                <Input id="dd" type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Faktur Pajak number" hint="Indonesian tax invoice serial (optional in draft, required for e-Faktur export)">
                <Input value={taxInvoiceNumber} onChange={(e) => setTaxInvoiceNumber(e.target.value)} disabled={!editable} placeholder="0100000123456789" />
              </Field>
            </div>
          </Card>

          {/* Line items */}
          <Card padded={false}>
            <div className="flex items-center justify-between p-5 pb-3">
              <CardTitle>Items</CardTitle>
              {editable && (
                <Button variant="ghost" size="sm" onClick={addLine}>
                  <Plus className="size-4" /> Add row
                </Button>
              )}
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-dense">
                <thead className="border-y border-border bg-bg-subtle/50">
                  <tr>
                    <th className="text-left font-medium text-text-secondary px-4 py-2 w-10">#</th>
                    <th className="text-left font-medium text-text-secondary px-4 py-2 w-[280px]">Item</th>
                    <th className="text-left font-medium text-text-secondary px-4 py-2">Description</th>
                    <th className="text-right font-medium text-text-secondary px-4 py-2 w-24">Qty</th>
                    <th className="text-left font-medium text-text-secondary px-4 py-2 w-20">UOM</th>
                    <th className="text-right font-medium text-text-secondary px-4 py-2 w-36">Rate</th>
                    <th className="text-right font-medium text-text-secondary px-4 py-2 w-40">Amount</th>
                    {editable && <th className="w-10" />}
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l, idx) => {
                    const amount = new Decimal(l.qty || '0').times(l.rate || '0');
                    return (
                      <tr key={l.rowId} className="border-b border-border last:border-0">
                        <td className="px-4 py-1.5 text-text-tertiary">{idx + 1}</td>
                        <td className="px-2 py-1.5">
                          {editable
                            ? <Combobox
                                options={(items?.items ?? []).map((i) => ({ value: i.id, label: i.code, description: i.name }))}
                                value={l.item_id ?? null}
                                onChange={(v) => pickItem(l.rowId, v)}
                                placeholder="Pick item…"
                              />
                            : <div className="px-3 py-1.5"><div className="font-medium text-text-primary">{l.item_code}</div><div className="text-caption text-text-tertiary">{l.item_name}</div></div>}
                        </td>
                        <td className="px-2 py-1.5">
                          {editable ? <Input value={l.item_name} onChange={(e) => updateLine(l.rowId, { item_name: e.target.value })} />
                                    : <span className="text-text-secondary px-3">{l.item_name}</span>}
                        </td>
                        <td className="px-2 py-1.5">
                          {editable ? <NumericInput value={l.qty} onChange={(v) => updateLine(l.rowId, { qty: v })} decimalPlaces={0} />
                                    : <span className="num text-right block px-3">{l.qty}</span>}
                        </td>
                        <td className="px-2 py-1.5">
                          {editable ? <Input value={l.uom} onChange={(e) => updateLine(l.rowId, { uom: e.target.value })} />
                                    : <span className="text-text-secondary px-3">{l.uom}</span>}
                        </td>
                        <td className="px-2 py-1.5">
                          {editable ? <NumericInput value={l.rate} onChange={(v) => updateLine(l.rowId, { rate: v })} />
                                    : <span className="num text-right block px-3">{money(l.rate)}</span>}
                        </td>
                        <td className="px-4 py-1.5 text-right num font-medium">{money(amount.toString())}</td>
                        {editable && (
                          <td className="px-2 py-1.5">
                            <Button
                              variant="ghost" size="icon" aria-label="Remove row"
                              onClick={() => removeLine(l.rowId)}
                              disabled={lines.length <= 1}
                            >
                              <Trash2 className="size-3.5" />
                            </Button>
                          </td>
                        )}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            {/* totals strip */}
            <div className="px-5 py-4 border-t border-border flex justify-end">
              <table className="text-dense num">
                <tbody>
                  <tr><td className="text-text-secondary pr-4 py-1">Subtotal</td><td className="text-right py-1">{money(totals.net.toString())}</td></tr>
                  <tr><td className="text-text-secondary pr-4 py-1">PPN (~11% est.)</td><td className="text-right py-1">{money(totals.tax.toString())}</td></tr>
                  <tr className="border-t border-border-strong"><td className="font-semibold text-text-primary pr-4 py-1.5">Grand total</td><td className="text-right py-1.5 font-semibold text-page-title">{money(totals.grand.toString())}</td></tr>
                </tbody>
              </table>
            </div>
          </Card>

          <Card>
            <CardTitle>Notes</CardTitle>
            <textarea
              className="input-base mt-3 min-h-[80px]"
              placeholder="Internal remarks (visible on the invoice)"
              value={remarks}
              onChange={(e) => setRemarks(e.target.value)}
              disabled={!editable}
            />
          </Card>
        </div>

        {/* SIDE COLUMN */}
        <aside className="space-y-4">
          <Card>
            <CardTitle>Status</CardTitle>
            <div className="mt-3 space-y-3 text-body">
              <div className="flex items-center justify-between">
                <span className="text-text-secondary">Docstatus</span>
                {existing ? <DocstatusPill docstatus={existing.docstatus} /> : <StatusPill tone="neutral">Draft</StatusPill>}
              </div>
              {existing && (
                <>
                  <div className="flex items-center justify-between">
                    <span className="text-text-secondary">Outstanding</span>
                    <span className="num font-medium">{money(existing.outstanding_amount)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-text-secondary">Paid</span>
                    <span className="num">{money(existing.paid_amount)}</span>
                  </div>
                </>
              )}
            </div>
          </Card>

          {existing?.taxes && existing.taxes.length > 0 && (
            <Card>
              <CardTitle>Taxes</CardTitle>
              <ul className="mt-3 text-body space-y-1.5">
                {existing.taxes.map((tx) => (
                  <li key={tx.id} className="flex items-center justify-between">
                    <span className="text-text-secondary">{tx.description}</span>
                    <span className="num">{money(tx.tax_amount)}</span>
                  </li>
                ))}
              </ul>
            </Card>
          )}

          <Card>
            <CardTitle>Tips</CardTitle>
            <ul className="mt-2 space-y-2 text-caption text-text-secondary">
              <li className="flex gap-2"><FileText className="size-3.5 shrink-0 mt-0.5 text-text-tertiary" /> Server recomputes taxes on submit — preview here is best-effort.</li>
              <li className="flex gap-2"><Send  className="size-3.5 shrink-0 mt-0.5 text-text-tertiary" /> Submitting posts to the GL atomically.</li>
              <li className="flex gap-2"><Ban   className="size-3.5 shrink-0 mt-0.5 text-text-tertiary" /> Cancel posts reversing entries; can't cancel if any payment is applied.</li>
            </ul>
          </Card>
        </aside>
      </motion.div>
    </>
  );
}
