import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router';
import Decimal from 'decimal.js';
import {
  Plus, Trash2, Send, Save, Ban, ArrowLeft, AlertCircle, Package,
} from 'lucide-react';
import { motion } from 'framer-motion';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Combobox } from '@/components/Combobox';
import { NumericInput } from '@/components/NumericInput';
import { DocstatusPill, StatusPill } from '@/components/StatusPill';
import { Timeline } from '@/components/Timeline';
import { Kbd } from '@/components/Kbd';
import { api } from '@/lib/api';
import { money } from '@/lib/format';
import { toast } from '@/components/Toaster';

interface PurchaseReceipt {
  id: string; name: string; company_id: string; supplier_id: string;
  posting_date: string;
  against_purchase_order_id?: string;
  supplier_delivery_note?: string;
  status: string;
  total_value: string;
  remarks?: string;
  docstatus: 0 | 1 | 2;
  items: Array<{
    id: string; row_index: number; item_id: string;
    item_code: string; item_name: string; description?: string;
    uom: string; rate: string;
    accepted_qty: string; rejected_qty: string;
    accepted_warehouse_id: string; rejected_warehouse_id?: string;
    against_po_id?: string; against_po_row_index?: number;
    valuation_rate: string; amount: string;
  }>;
}
interface Item      { id: string; code: string; name: string; stock_uom: string; standard_rate: string }
interface Warehouse { id: string; code: string; name: string; is_group?: boolean }
interface Supplier  { id: string; name: string; display_name: string }
interface PO {
  id: string; name: string; supplier_id: string; status: string;
  items: Array<{
    id: string; row_index: number; item_id?: string;
    item_code: string; item_name: string; uom: string; rate: string;
    qty: string; received_qty: string; warehouse_id?: string;
  }>;
}

interface DraftLine {
  rowId: string;
  item_id: string;
  item_code: string;
  item_name: string;
  uom: string;
  rate: string;
  accepted_qty: string;
  rejected_qty: string;
  accepted_warehouse_id: string;
  rejected_warehouse_id: string;
  against_po_row_index: number;
}

const todayISO = () => new Date().toISOString().slice(0, 10);
const rid = () => Math.random().toString(36).slice(2);

export function PurchaseReceiptForm() {
  const { id } = useParams({ strict: false }) as { id?: string };
  // Support `?po=<id>` deep-link from the PO form so "Create GRN from PO" can
  // prefill the form.
  const search = useSearch({ strict: false }) as { po?: string };
  const navigate = useNavigate();
  const qc = useQueryClient();
  const isNew = !id || id === 'new';

  const { data: existing } = useQuery({
    queryKey: ['pr', id],
    queryFn:  () => api<PurchaseReceipt>(`/stock/purchase-receipts/${id}`),
    enabled:  !isNew,
  });
  const { data: itemsResp }      = useQuery({ queryKey: ['items'],      queryFn: () => api<{ items: Item[] }>('/accounting/items') });
  const { data: warehousesResp } = useQuery({ queryKey: ['warehouses'], queryFn: () => api<{ items: Warehouse[] }>('/stock/warehouses') });
  const { data: suppliersResp }  = useQuery({ queryKey: ['suppliers'],  queryFn: () => api<{ items: Supplier[] }>('/accounting/suppliers') });

  // When ?po=<id>, fetch the PO and prefill on first render.
  const { data: sourcePO } = useQuery({
    queryKey: ['po', search.po],
    queryFn:  () => api<PO>(`/accounting/purchase-orders/${search.po}`),
    enabled:  Boolean(isNew && search.po),
  });

  const items     = itemsResp?.items;
  const warehouses = useMemo(
    () => (warehousesResp?.items ?? []).filter((w) => !w.is_group),
    [warehousesResp],
  );
  const suppliers = suppliersResp?.items ?? [];

  const [supplierID, setSupplierID]         = useState<string>('');
  const [postingDate, setPostingDate]       = useState(todayISO());
  const [againstPOID, setAgainstPOID]       = useState<string>('');
  const [supplierDN, setSupplierDN]         = useState('');
  const [remarks, setRemarks]               = useState('');
  const [lines, setLines] = useState<DraftLine[]>([
    { rowId: rid(), item_id: '', item_code: '', item_name: '', uom: 'Unit', rate: '0',
      accepted_qty: '1', rejected_qty: '0', accepted_warehouse_id: '', rejected_warehouse_id: '',
      against_po_row_index: 0 },
  ]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!existing) return;
    setSupplierID(existing.supplier_id);
    setPostingDate(existing.posting_date.slice(0, 10));
    setAgainstPOID(existing.against_purchase_order_id ?? '');
    setSupplierDN(existing.supplier_delivery_note ?? '');
    setRemarks(existing.remarks ?? '');
    setLines(existing.items.map((it) => ({
      rowId: rid(),
      item_id: it.item_id, item_code: it.item_code, item_name: it.item_name,
      uom: it.uom, rate: it.rate,
      accepted_qty: it.accepted_qty, rejected_qty: it.rejected_qty,
      accepted_warehouse_id: it.accepted_warehouse_id,
      rejected_warehouse_id: it.rejected_warehouse_id ?? '',
      against_po_row_index: it.against_po_row_index ?? 0,
    })));
  }, [existing]);

  // Prefill from `?po=<id>` once the PO loads. Carries remaining qty per line.
  useEffect(() => {
    if (!sourcePO || !isNew) return;
    if (againstPOID === sourcePO.id) return; // already prefilled
    setSupplierID(sourcePO.supplier_id);
    setAgainstPOID(sourcePO.id);
    const newLines: DraftLine[] = sourcePO.items
      .map((it) => {
        const remaining = new Decimal(it.qty).minus(new Decimal(it.received_qty));
        if (!remaining.gt(0)) return null;
        return {
          rowId: rid(),
          item_id: it.item_id ?? '',
          item_code: it.item_code, item_name: it.item_name, uom: it.uom, rate: it.rate,
          accepted_qty: remaining.toString(),
          rejected_qty: '0',
          accepted_warehouse_id: it.warehouse_id ?? '',
          rejected_warehouse_id: '',
          against_po_row_index: it.row_index,
        };
      })
      .filter((l): l is DraftLine => l !== null);
    if (newLines.length > 0) setLines(newLines);
  }, [sourcePO, isNew, againstPOID]);

  const editable  = isNew || existing?.docstatus === 0;
  const submitted = existing?.docstatus === 1;
  const cancelled = existing?.docstatus === 2;
  const status    = existing?.status ?? 'Draft';

  const totals = useMemo(() => {
    let v = new Decimal(0);
    for (const l of lines) {
      const r = new Decimal(l.rate || '0');
      v = v.plus(new Decimal(l.accepted_qty || '0').plus(new Decimal(l.rejected_qty || '0')).mul(r));
    }
    return v;
  }, [lines]);

  function validate(): string[] {
    const issues: string[] = [];
    if (!supplierID) issues.push('Supplier is required.');
    if (!postingDate) issues.push('Posting date is required.');
    if (lines.length === 0) issues.push('Add at least one line.');
    lines.forEach((l, i) => {
      const n = i + 1;
      if (!l.item_id) issues.push(`Line ${n}: pick an item.`);
      const acc = Number(l.accepted_qty || '0');
      const rej = Number(l.rejected_qty || '0');
      if (!Number.isFinite(acc) || acc < 0) issues.push(`Line ${n}: accepted qty must be ≥ 0.`);
      if (!Number.isFinite(rej) || rej < 0) issues.push(`Line ${n}: rejected qty must be ≥ 0.`);
      if (acc + rej <= 0) issues.push(`Line ${n}: must have accepted or rejected qty > 0.`);
      if (!l.accepted_warehouse_id) issues.push(`Line ${n}: accepted warehouse required.`);
      if (rej > 0 && !l.rejected_warehouse_id) issues.push(`Line ${n}: rejected warehouse required when rejected qty > 0.`);
      if (Number(l.rate || '0') <= 0) issues.push(`Line ${n}: rate must be > 0 (used to value the receipt).`);
    });
    return issues;
  }

  const createMutation = useMutation({
    mutationFn: () => api<PurchaseReceipt>('/stock/purchase-receipts', {
      method: 'POST',
      body: {
        supplier_id: supplierID,
        posting_date: postingDate,
        against_purchase_order_id: againstPOID || undefined,
        supplier_delivery_note: supplierDN || undefined,
        remarks: remarks || undefined,
        items: lines.map((l) => ({
          item_id: l.item_id,
          item_code: l.item_code,
          item_name: l.item_name,
          uom: l.uom || 'Unit',
          rate: l.rate,
          accepted_qty: l.accepted_qty,
          rejected_qty: l.rejected_qty || '0',
          accepted_warehouse_id: l.accepted_warehouse_id,
          rejected_warehouse_id: l.rejected_warehouse_id || undefined,
          against_po_row_index: l.against_po_row_index || undefined,
        })),
      },
    }),
    onSuccess: (pr) => {
      toast.success(`Saved ${pr.name}`);
      void qc.invalidateQueries({ queryKey: ['list', 'purchase-receipts'] });
      void navigate({ to: `/stock/purchase-receipts/${pr.id}` as never });
    },
    onError: (e: Error) => { setErr(e.message); toast.error('Could not save draft', e.message); },
  });

  function onSaveDraft() {
    setErr(null);
    const issues = validate();
    if (issues.length > 0) { setErr(issues.join(' ')); toast.error('Fix these first', issues[0]); return; }
    createMutation.mutate();
  }

  const submitMutation = useMutation({
    mutationFn: () => api<PurchaseReceipt>(`/stock/purchase-receipts/${id}/submit`, { method: 'POST' }),
    onSuccess: () => { toast.success('Submitted — stock posted'); void qc.invalidateQueries({ queryKey: ['pr', id] }); void qc.invalidateQueries({ queryKey: ['po'] }); },
    onError:   (e: Error) => setErr(e.message),
  });
  const cancelMutation = useMutation({
    mutationFn: () => api<PurchaseReceipt>(`/stock/purchase-receipts/${id}/cancel`, { method: 'POST' }),
    onSuccess: () => { toast.warning('Cancelled — stock ledger reversed'); void qc.invalidateQueries({ queryKey: ['pr', id] }); void qc.invalidateQueries({ queryKey: ['po'] }); },
    onError:   (e: Error) => setErr(e.message),
  });

  const title = isNew
    ? <>New Purchase Receipt</>
    : <span className="flex items-center gap-3"><span>{existing?.name ?? '…'}</span></span>;

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Stock', to: '/stock' },
          { label: 'Purchase Receipts', to: '/stock/purchase-receipts' },
          { label: isNew ? 'New' : (existing?.name ?? '…') },
        ]}
        title={title}
        status={existing && (
          <div className="flex items-center gap-2">
            <DocstatusPill docstatus={existing.docstatus} />
            <GRNStatusPill value={status} />
          </div>
        )}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/stock/purchase-receipts' as never}><ArrowLeft className="size-4" /> Back</Link>
            </Button>
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
            {submitted && (
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
              <span className="text-body text-danger">This receipt has been cancelled. Stock ledger entries are flagged cancelled.</span>
            </Card>
          )}
          {err && (
            <Card className="!p-3 bg-brand-error/5 border-brand-error/30 flex items-start gap-2">
              <AlertCircle className="size-4 text-brand-error mt-0.5 shrink-0" />
              <div className="text-body-sm text-brand-error">{err}</div>
            </Card>
          )}

          <Card>
            <CardTitle>Receipt from</CardTitle>
            <div className="mt-4 grid sm:grid-cols-2 gap-4">
              <Field label="Supplier" hint="Required.">
                <Combobox
                  value={supplierID || null}
                  options={suppliers.map((s) => ({ value: s.id, label: s.display_name, hint: s.name }))}
                  onChange={(v) => setSupplierID(v ?? '')}
                  placeholder="Pick a supplier"
                  disabled={!editable || !!againstPOID}
                />
              </Field>
              <Field label="Posting date">
                <Input type="date" value={postingDate} onChange={(e) => setPostingDate(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Against PO" hint="Receipts not tied to a PO are allowed; tie them for cleaner tracking.">
                <Input value={againstPOID} disabled placeholder="—" className="font-mono text-caption" />
              </Field>
              <Field label="Supplier delivery note" hint="Their own DN ref, for paper-trail matching.">
                <Input value={supplierDN} onChange={(e) => setSupplierDN(e.target.value)} disabled={!editable} />
              </Field>
            </div>
          </Card>

          <Card>
            <div className="flex items-center justify-between mb-3">
              <CardTitle>Line items</CardTitle>
              {editable && (
                <Button size="sm" variant="secondary" onClick={() =>
                  setLines((prev) => [...prev, { rowId: rid(), item_id: '', item_code: '', item_name: '', uom: 'Unit', rate: '0',
                    accepted_qty: '1', rejected_qty: '0', accepted_warehouse_id: '', rejected_warehouse_id: '', against_po_row_index: 0 }])}>
                  <Plus className="size-3.5" /> Add line
                </Button>
              )}
            </div>

            <div className="overflow-x-auto -mx-5">
              <table className="w-full text-body-sm">
                <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone">
                  <tr>
                    <th className="text-left font-medium px-3 py-2 w-[180px]">Item</th>
                    <th className="text-right font-medium px-3 py-2 w-[90px]">Accepted</th>
                    <th className="text-left font-medium px-3 py-2 w-[140px]">→ Warehouse</th>
                    <th className="text-right font-medium px-3 py-2 w-[90px]">Rejected</th>
                    <th className="text-left font-medium px-3 py-2 w-[140px]">→ Warehouse</th>
                    <th className="text-right font-medium px-3 py-2 w-[110px]">Rate</th>
                    <th className="text-right font-medium px-3 py-2 w-[110px]">Amount</th>
                    <th className="px-3 py-2 w-[40px]"></th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l, i) => {
                    const amount = (new Decimal(l.accepted_qty || '0').plus(new Decimal(l.rejected_qty || '0'))).mul(new Decimal(l.rate || '0'));
                    return (
                      <tr key={l.rowId} className="border-t border-hairline">
                        <td className="px-2 py-1">
                          <Combobox
                            value={l.item_id || null}
                            options={(items ?? []).map((it) => ({ value: it.id, label: it.code, hint: it.name }))}
                            onChange={(v) => {
                              const it = (items ?? []).find((x) => x.id === v);
                              setLines((prev) => prev.map((p, idx) => idx === i ? {
                                ...p,
                                item_id: it?.id ?? '', item_code: it?.code ?? '',
                                item_name: it?.name ?? '', uom: it?.stock_uom ?? p.uom,
                                rate: it?.standard_rate ?? p.rate,
                              } : p));
                            }}
                            placeholder="Pick an item"
                            disabled={!editable}
                          />
                          {l.against_po_row_index > 0 && (
                            <div className="text-caption text-stone mt-0.5">From PO row {l.against_po_row_index}</div>
                          )}
                        </td>
                        <td className="px-2 py-1">
                          <NumericInput value={l.accepted_qty} disabled={!editable}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, accepted_qty: v } : p))} />
                        </td>
                        <td className="px-2 py-1">
                          <Combobox
                            value={l.accepted_warehouse_id || null}
                            options={warehouses.map((w) => ({ value: w.id, label: w.code, hint: w.name }))}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, accepted_warehouse_id: v ?? '' } : p))}
                            disabled={!editable}
                          />
                        </td>
                        <td className="px-2 py-1">
                          <NumericInput value={l.rejected_qty} disabled={!editable}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, rejected_qty: v } : p))} />
                        </td>
                        <td className="px-2 py-1">
                          <Combobox
                            value={l.rejected_warehouse_id || null}
                            options={warehouses.map((w) => ({ value: w.id, label: w.code, hint: w.name }))}
                            onChange={(v) => setLines((prev) => prev.map((p, idx) => idx === i ? { ...p, rejected_warehouse_id: v ?? '' } : p))}
                            placeholder={Number(l.rejected_qty) > 0 ? 'Required' : '—'}
                            disabled={!editable || Number(l.rejected_qty) === 0}
                          />
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

        <div className="space-y-4">
          <Card>
            <CardTitle>Summary</CardTitle>
            <div className="mt-3 space-y-2 text-body-sm">
              <Row label="Lines" value={String(lines.length)} />
              <Row label="Total accepted" value={String(lines.reduce((s, l) => s + (Number(l.accepted_qty) || 0), 0))} />
              <Row label="Total rejected" value={String(lines.reduce((s, l) => s + (Number(l.rejected_qty) || 0), 0))} />
              <Row label="Value" value={existing ? money(existing.total_value) : money(totals.toString())} highlight />
            </div>
            <div className="mt-2 text-caption text-stone flex items-start gap-1.5">
              <Package className="size-3.5 shrink-0 mt-0.5" />
              <span>Submit posts stock-ledger entries; GL posting waits for the linked Purchase Invoice.</span>
            </div>
          </Card>

          {existing && <Timeline doctype="purchase_receipt" documentId={existing.id} />}
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

function GRNStatusPill({ value }: { value: string }) {
  switch (value) {
    case 'Draft':         return <StatusPill tone="neutral" withDot={false}>Draft</StatusPill>;
    case 'To Bill':       return <StatusPill tone="warning" withDot={false}>To Bill</StatusPill>;
    case 'Completed':     return <StatusPill tone="success" withDot={false}>Completed</StatusPill>;
    case 'Return Issued': return <StatusPill tone="info"    withDot={false}>Return Issued</StatusPill>;
    case 'Cancelled':     return <StatusPill tone="danger"  withDot={false}>Cancelled</StatusPill>;
    default:              return <StatusPill tone="neutral" withDot={false}>{value || '—'}</StatusPill>;
  }
}
