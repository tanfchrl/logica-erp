import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import {
  Plus, Trash2, Send, Save, Ban, ArrowLeft, AlertCircle, ShoppingBag,
  CircleStop, Play, ChevronRight,
} from 'lucide-react';
import { motion } from 'framer-motion';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Combobox } from '@/components/Combobox';
import { NumericInput } from '@/components/NumericInput';
import { DocstatusPill, StatusPill } from '@/components/StatusPill';
import { Dialog, DialogContent, DialogTitle, DialogDescription } from '@/components/Dialog';
import { Timeline } from '@/components/Timeline';
import { Kbd } from '@/components/Kbd';
import { api } from '@/lib/api';
import { toast } from '@/components/Toaster';

interface MaterialRequest {
  id: string; name: string; company_id: string;
  purpose: 'purchase' | 'material_transfer' | 'material_issue' | 'manufacture';
  transaction_date: string; required_by_date?: string;
  set_warehouse_id?: string; from_warehouse_id?: string;
  status: string;
  remarks?: string;
  docstatus: 0 | 1 | 2;
  items: Array<{
    id: string; row_index: number; item_id?: string;
    item_code: string; item_name: string; description?: string;
    qty: string; uom: string; rate: string;
    warehouse_id?: string; required_by_date?: string;
    ordered_qty: string; received_qty: string; issued_qty: string; transferred_qty: string;
  }>;
}
interface Item      { id: string; code: string; name: string; stock_uom: string; standard_rate: string }
interface Warehouse { id: string; code: string; name: string; is_group?: boolean }
interface Supplier  { id: string; name: string; display_name: string }
interface PO        { id: string; name: string }

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

const PURPOSES = [
  { value: 'purchase',          label: 'Purchase — raise PO to a supplier' },
  { value: 'material_transfer', label: 'Material transfer — move between warehouses' },
  { value: 'material_issue',    label: 'Material issue — consume from a warehouse' },
  { value: 'manufacture',       label: 'Manufacture — feed a Work Order' },
] as const;

const todayISO = () => new Date().toISOString().slice(0, 10);
const isoPlusDays = (d: number) => {
  const dt = new Date(); dt.setDate(dt.getDate() + d); return dt.toISOString().slice(0, 10);
};
const rid = () => Math.random().toString(36).slice(2);

export function MaterialRequestForm() {
  const { id } = useParams({ strict: false }) as { id?: string };
  const navigate = useNavigate();
  const qc = useQueryClient();
  const isNew = !id || id === 'new';

  const { data: existing } = useQuery({
    queryKey: ['mr', id],
    queryFn:  () => api<MaterialRequest>(`/accounting/material-requests/${id}`),
    enabled:  !isNew,
  });
  const { data: itemsResp }      = useQuery({ queryKey: ['items'],      queryFn: () => api<{ items: Item[] }>('/accounting/items') });
  const { data: warehousesResp } = useQuery({ queryKey: ['warehouses'], queryFn: () => api<{ items: Warehouse[] }>('/stock/warehouses') });
  const { data: suppliersResp }  = useQuery({ queryKey: ['suppliers'],  queryFn: () => api<{ items: Supplier[] }>('/accounting/suppliers') });

  const items     = itemsResp?.items;
  const warehouses = useMemo(
    () => (warehousesResp?.items ?? []).filter((w) => !w.is_group),
    [warehousesResp],
  );
  const suppliers = suppliersResp?.items ?? [];

  const [purpose, setPurpose]               = useState<typeof PURPOSES[number]['value']>('purchase');
  const [transactionDate, setTxDate]        = useState(todayISO());
  const [requiredBy, setRequiredBy]         = useState(isoPlusDays(14));
  const [setWarehouseID, setSetWh]          = useState<string>('');
  const [fromWarehouseID, setFromWh]        = useState<string>('');
  const [remarks, setRemarks]               = useState('');
  const [lines, setLines] = useState<DraftLine[]>([
    { rowId: rid(), item_code: '', item_name: '', qty: '1', rate: '0', uom: 'Unit', warehouse_id: '', description: '', required_by_date: '' },
  ]);
  const [err, setErr] = useState<string | null>(null);
  const [poDlgOpen, setPODlgOpen] = useState(false);

  useEffect(() => {
    if (!existing) return;
    setPurpose(existing.purpose);
    setTxDate(existing.transaction_date.slice(0, 10));
    setRequiredBy(existing.required_by_date ? existing.required_by_date.slice(0, 10) : '');
    setSetWh(existing.set_warehouse_id ?? '');
    setFromWh(existing.from_warehouse_id ?? '');
    setRemarks(existing.remarks ?? '');
    setLines(existing.items.map((it) => ({
      rowId: rid(),
      item_id: it.item_id, item_code: it.item_code, item_name: it.item_name,
      qty: it.qty, rate: it.rate, uom: it.uom,
      warehouse_id: it.warehouse_id ?? '',
      description: it.description ?? '',
      required_by_date: it.required_by_date ? it.required_by_date.slice(0, 10) : '',
    })));
  }, [existing]);

  const editable  = isNew || existing?.docstatus === 0;
  const submitted = existing?.docstatus === 1;
  const cancelled = existing?.docstatus === 2;
  const status    = existing?.status ?? 'Draft';

  const canRaisePO = submitted && existing?.purpose === 'purchase' &&
    (status === 'Pending' || status === 'Partially Ordered');
  const canStop   = submitted && status !== 'Stopped' && status !== 'Cancelled';
  const canReopen = submitted && status === 'Stopped';

  function validate(): string[] {
    const issues: string[] = [];
    if (!transactionDate) issues.push('Requested date is required.');
    if (purpose === 'material_transfer' && !fromWarehouseID) {
      issues.push('From warehouse is required for transfers.');
    }
    if (lines.length === 0) issues.push('Add at least one line.');
    lines.forEach((l, i) => {
      const n = i + 1;
      if (!l.item_code) issues.push(`Line ${n}: item is required.`);
      const qty = Number(l.qty);
      if (!Number.isFinite(qty) || qty <= 0) issues.push(`Line ${n}: quantity must be > 0.`);
    });
    return issues;
  }

  const createMutation = useMutation({
    mutationFn: () => api<MaterialRequest>('/accounting/material-requests', {
      method: 'POST',
      body: {
        purpose,
        transaction_date: transactionDate,
        required_by_date: requiredBy || undefined,
        set_warehouse_id: setWarehouseID || undefined,
        from_warehouse_id: fromWarehouseID || undefined,
        remarks: remarks || undefined,
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
    onSuccess: (mr) => {
      toast.success(`Saved ${mr.name}`);
      void qc.invalidateQueries({ queryKey: ['list', 'material-requests'] });
      void navigate({ to: `/accounting/material-requests/${mr.id}` as never });
    },
    onError: (e: Error) => { setErr(e.message); toast.error('Could not save draft', e.message); },
  });

  function onSaveDraft() {
    setErr(null);
    const issues = validate();
    if (issues.length > 0) { setErr(issues.join(' ')); toast.error('Fix these first', issues[0]); return; }
    createMutation.mutate();
  }

  const lifecycle = (path: string, successToast: string) => useMutation({
    mutationFn: () => api<MaterialRequest>(`/accounting/material-requests/${id}/${path}`, { method: 'POST' }),
    onSuccess: () => { toast.success(successToast); void qc.invalidateQueries({ queryKey: ['mr', id] }); },
    onError:   (e: Error) => setErr(e.message),
  });
  const submitMutation = lifecycle('submit', 'Submitted');
  const cancelMutation = lifecycle('cancel', 'Cancelled');
  const stopMutation   = lifecycle('stop',   'Stopped');
  const reopenMutation = lifecycle('reopen', 'Reopened');

  const title = isNew
    ? <>New Material Request</>
    : <span className="flex items-center gap-3"><span>{existing?.name ?? '…'}</span></span>;

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Procurement', to: '/buying' },
          { label: 'Material Requests', to: '/accounting/material-requests' },
          { label: isNew ? 'New' : (existing?.name ?? '…') },
        ]}
        title={title}
        status={existing && (
          <div className="flex items-center gap-2">
            <DocstatusPill docstatus={existing.docstatus} />
            <MRStatusPill value={status} />
          </div>
        )}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/accounting/material-requests' as never}><ArrowLeft className="size-4" /> Back</Link>
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
            {canRaisePO && (
              <Button onClick={() => setPODlgOpen(true)}><ShoppingBag className="size-4" /> Create PO</Button>
            )}
            {canReopen && (
              <Button variant="secondary" onClick={() => reopenMutation.mutate()} loading={reopenMutation.isPending}>
                <Play className="size-4" /> Reopen
              </Button>
            )}
            {canStop && (
              <Button variant="secondary" onClick={() => stopMutation.mutate()} loading={stopMutation.isPending}>
                <CircleStop className="size-4" /> Stop
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
              <span className="text-body text-danger">This request has been cancelled.</span>
            </Card>
          )}
          {err && (
            <Card className="!p-3 bg-brand-error/5 border-brand-error/30 flex items-start gap-2">
              <AlertCircle className="size-4 text-brand-error mt-0.5 shrink-0" />
              <div className="text-body-sm text-brand-error">{err}</div>
            </Card>
          )}

          <Card>
            <CardTitle>What's needed</CardTitle>
            <div className="mt-4 grid sm:grid-cols-2 gap-4">
              <Field label="Purpose">
                <select className="input-base" value={purpose} disabled={!editable}
                  onChange={(e) => setPurpose(e.target.value as typeof purpose)}>
                  {PURPOSES.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
                </select>
              </Field>
              <Field label="Required by">
                <Input type="date" value={requiredBy} onChange={(e) => setRequiredBy(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Requested date">
                <Input type="date" value={transactionDate} onChange={(e) => setTxDate(e.target.value)} disabled={!editable} />
              </Field>
              <Field label={purpose === 'material_transfer' ? 'Default target warehouse' : 'Default warehouse'} hint="Copied to each line that doesn't override.">
                <Combobox
                  value={setWarehouseID || null}
                  options={warehouses.map((w) => ({ value: w.id, label: w.code, hint: w.name }))}
                  onChange={(v) => setSetWh(v ?? '')}
                  placeholder="No default"
                  disabled={!editable}
                />
              </Field>
              {purpose === 'material_transfer' && (
                <Field label="From warehouse" hint="Required for transfers.">
                  <Combobox
                    value={fromWarehouseID || null}
                    options={warehouses.map((w) => ({ value: w.id, label: w.code, hint: w.name }))}
                    onChange={(v) => setFromWh(v ?? '')}
                    placeholder="Pick a source"
                    disabled={!editable}
                  />
                </Field>
              )}
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
                    <th className="text-right font-medium px-3 py-2 w-[110px]">Est. rate</th>
                    {submitted && <th className="text-right font-medium px-3 py-2 w-[120px]">Fulfilled</th>}
                    <th className="px-3 py-2 w-[40px]"></th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l, i) => {
                    const existingLine = existing?.items[i];
                    const consumedCol = (() => {
                      if (!existingLine) return '—';
                      const total = Number(existingLine.qty);
                      const used = Number(existingLine.ordered_qty) || Number(existingLine.received_qty) ||
                        Number(existingLine.issued_qty) || Number(existingLine.transferred_qty);
                      if (used === 0) return '—';
                      return `${used} / ${total}`;
                    })();
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
                        {submitted && (
                          <td className="px-2 py-1 text-right text-caption text-stone num">{consumedCol}</td>
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
            <Field label="Remarks">
              <textarea className="input-base !h-auto !py-2" rows={3}
                value={remarks} onChange={(e) => setRemarks(e.target.value)} disabled={!editable} />
            </Field>
          </Card>
        </div>

        <div className="space-y-4">
          <Card>
            <CardTitle>Summary</CardTitle>
            <div className="mt-3 text-body-sm space-y-2">
              <Row label="Purpose" value={PURPOSES.find((p) => p.value === purpose)?.label.split(' — ')[0] ?? purpose} />
              <Row label="Lines" value={String(lines.length)} />
              <Row label="Total qty" value={lines.reduce((s, l) => s + (Number(l.qty) || 0), 0).toString()} />
            </div>
            {submitted && (
              <div className="mt-3 pt-3 border-t border-hairline text-caption text-stone">
                Status updates automatically as downstream POs, stock entries, and receipts are submitted.
              </div>
            )}
          </Card>

          {existing && <Timeline doctype="material_request" documentId={existing.id} />}
        </div>
      </motion.div>

      {poDlgOpen && existing && (
        <CreatePODialog mr={existing} suppliers={suppliers}
          onClose={() => setPODlgOpen(false)}
          onCreated={(po) => {
            setPODlgOpen(false);
            void qc.invalidateQueries({ queryKey: ['mr', id] });
            toast.success(`PO ${po.name} created`);
            void navigate({ to: `/accounting/purchase-orders/${po.id}` as never });
          }} />
      )}
    </>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-stone">{label}</span>
      <span className="text-charcoal text-right">{value}</span>
    </div>
  );
}

function MRStatusPill({ value }: { value: string }) {
  switch (value) {
    case 'Draft':              return <StatusPill tone="neutral" withDot={false}>Draft</StatusPill>;
    case 'Pending':            return <StatusPill tone="warning" withDot={false}>Pending</StatusPill>;
    case 'Partially Ordered':  return <StatusPill tone="info"    withDot={false}>Partially</StatusPill>;
    case 'Ordered':            return <StatusPill tone="info"    withDot={false}>Ordered</StatusPill>;
    case 'Issued':             return <StatusPill tone="success" withDot={false}>Issued</StatusPill>;
    case 'Transferred':        return <StatusPill tone="success" withDot={false}>Transferred</StatusPill>;
    case 'Received':           return <StatusPill tone="success" withDot={false}>Received</StatusPill>;
    case 'Stopped':            return <StatusPill tone="danger"  withDot={false}>Stopped</StatusPill>;
    case 'Cancelled':          return <StatusPill tone="danger"  withDot={false}>Cancelled</StatusPill>;
    default:                   return <StatusPill tone="neutral" withDot={false}>{value || '—'}</StatusPill>;
  }
}

function hasFulfilment(mr: MaterialRequest | undefined): boolean {
  if (!mr) return false;
  return mr.items.some((l) =>
    Number(l.ordered_qty) > 0 || Number(l.received_qty) > 0 ||
    Number(l.issued_qty) > 0 || Number(l.transferred_qty) > 0);
}

/* ---- Create PO dialog ---- */

function CreatePODialog({
  mr, suppliers, onClose, onCreated,
}: {
  mr: MaterialRequest;
  suppliers: Supplier[];
  onClose: () => void;
  onCreated: (po: PO) => void;
}) {
  const [supplierID, setSupplierID] = useState<string>('');
  const [err, setErr] = useState<string | null>(null);

  const remaining = useMemo(() => mr.items.map((l) => ({
    row_index: l.row_index,
    item_code: l.item_code,
    item_name: l.item_name,
    qty: l.qty,
    ordered: l.ordered_qty,
    remaining: (Number(l.qty) - Number(l.ordered_qty)).toString(),
  })), [mr]);

  const mut = useMutation({
    mutationFn: () => api<PO>(`/accounting/material-requests/${mr.id}/create-purchase-order`, {
      method: 'POST',
      body: { supplier_id: supplierID }, // empty lines slice = carry every remaining line
    }),
    onSuccess: (po) => onCreated(po),
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-xl">
        <DialogTitle>Create Purchase Order from {mr.name}</DialogTitle>
        <DialogDescription>
          Carries every line at its remaining qty. Fine-grained line selection lands later.
        </DialogDescription>

        <div className="mt-3 space-y-3">
          <Field label="Supplier">
            <Combobox
              value={supplierID || null}
              options={suppliers.map((s) => ({ value: s.id, label: s.display_name, hint: s.name }))}
              onChange={(v) => setSupplierID(v ?? '')}
              placeholder="Pick a supplier"
            />
          </Field>

          <div className="rounded-md border border-hairline bg-surface-soft">
            <table className="w-full text-body-sm">
              <thead className="text-micro-uppercase text-stone">
                <tr>
                  <th className="text-left px-3 py-2">Item</th>
                  <th className="text-right px-3 py-2 w-[80px]">Asked</th>
                  <th className="text-right px-3 py-2 w-[80px]">Already</th>
                  <th className="text-right px-3 py-2 w-[80px]">Remaining</th>
                </tr>
              </thead>
              <tbody>
                {remaining.map((r) => (
                  <tr key={r.row_index} className="border-t border-hairline">
                    <td className="px-3 py-1.5"><span className="font-mono text-caption text-ink">{r.item_code}</span> <span className="text-stone">— {r.item_name}</span></td>
                    <td className="px-3 py-1.5 text-right num">{r.qty}</td>
                    <td className="px-3 py-1.5 text-right num text-stone">{r.ordered}</td>
                    <td className="px-3 py-1.5 text-right num font-semibold text-ink">
                      {r.remaining}
                      {Number(r.remaining) === 0 && <ChevronRight className="inline size-3 ml-1 text-success" />}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {err && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{err}</div>}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button variant="ghost" onClick={onClose}>Cancel</Button>
            <Button onClick={() => mut.mutate()} loading={mut.isPending}
              disabled={!supplierID || remaining.every((r) => Number(r.remaining) === 0)}>
              Create draft PO
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
