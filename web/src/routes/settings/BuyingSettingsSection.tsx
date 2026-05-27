import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ShoppingBag, Save, AlertCircle } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';

interface BuyingSettings {
  id: string;
  company_id: string;
  po_required_for_pi: boolean;
  pr_required_for_pi: boolean;
  over_billing_tolerance_pct: string;
  over_receipt_tolerance_pct: string;
  maintain_same_rate: boolean;
  allow_item_multiple_times: boolean;
  disable_last_purchase_rate: boolean;
  bill_for_rejected_qty: boolean;
  default_supplier_group_id?: string;
}

export function BuyingSettingsSection() {
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({
    queryKey: ['buying-settings'],
    queryFn:  () => api<BuyingSettings>('/admin/buying-settings'),
  });

  const [form, setForm] = useState<BuyingSettings | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (data) setForm(data);
  }, [data]);

  const save = useMutation({
    mutationFn: () => api<BuyingSettings>('/admin/buying-settings', {
      method: 'POST',
      body: {
        po_required_for_pi:         form?.po_required_for_pi ?? false,
        pr_required_for_pi:         form?.pr_required_for_pi ?? false,
        over_billing_tolerance_pct: form?.over_billing_tolerance_pct ?? '0',
        over_receipt_tolerance_pct: form?.over_receipt_tolerance_pct ?? '0',
        maintain_same_rate:         form?.maintain_same_rate ?? false,
        allow_item_multiple_times:  form?.allow_item_multiple_times ?? true,
        disable_last_purchase_rate: form?.disable_last_purchase_rate ?? false,
        bill_for_rejected_qty:      form?.bill_for_rejected_qty ?? false,
        default_supplier_group_id:  form?.default_supplier_group_id || undefined,
      },
    }),
    onSuccess: () => {
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
      void qc.invalidateQueries({ queryKey: ['buying-settings'] });
    },
  });

  function setF<K extends keyof BuyingSettings>(key: K, v: BuyingSettings[K]) {
    setForm((p) => p ? ({ ...p, [key]: v }) : p);
  }

  if (isLoading || !form) {
    return <Card><Skeleton className="h-40 w-full" /></Card>;
  }

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>
            <ShoppingBag className="size-4 inline mr-1.5 text-accent" />
            Buying Settings
          </CardTitle>
          <CardDescription>
            Tolerances and workflow gates the Procurement module enforces. Per-company; system administrators only.
          </CardDescription>
        </div>
        <Button onClick={() => save.mutate()} loading={save.isPending}>
          <Save className="size-4" /> Save
        </Button>
      </div>

      {error && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">{(error as Error).message}</div>
        </Card>
      )}
      {saved && (
        <Card className="border-l-4 border-l-brand-green-deep">
          <div className="text-body-sm">Saved. Settings take effect for the next PI / GRN submit.</div>
        </Card>
      )}

      <Card>
        <CardTitle>Workflow gates</CardTitle>
        <CardDescription>Block PI submit unless an upstream document is linked.</CardDescription>
        <div className="mt-4 space-y-3">
          <Toggle
            label="Require Purchase Order for Purchase Invoice"
            hint="Block PI submit unless `against_purchase_order_id` is set."
            checked={form.po_required_for_pi}
            onChange={(v) => setF('po_required_for_pi', v)}
          />
          <Toggle
            label="Require Purchase Receipt (GRN) for Purchase Invoice"
            hint="Stricter than PO-required — also demands a GRN."
            checked={form.pr_required_for_pi}
            onChange={(v) => setF('pr_required_for_pi', v)}
          />
        </div>
      </Card>

      <Card>
        <CardTitle>Tolerances</CardTitle>
        <CardDescription>How much beyond the PO qty receipts/bills may go. 0 = strict match.</CardDescription>
        <div className="mt-4 grid sm:grid-cols-2 gap-4">
          <Field label="Over-billing tolerance (%)" hint="PI cannot bill more than PO qty × (1 + tolerance).">
            <Input className="num text-right"
              value={form.over_billing_tolerance_pct}
              onChange={(e) => setF('over_billing_tolerance_pct', e.target.value)} />
          </Field>
          <Field label="Over-receipt tolerance (%)" hint="GRN can accept this much beyond the PO qty.">
            <Input className="num text-right"
              value={form.over_receipt_tolerance_pct}
              onChange={(e) => setF('over_receipt_tolerance_pct', e.target.value)} />
          </Field>
        </div>
      </Card>

      <Card>
        <CardTitle>Pricing</CardTitle>
        <CardDescription>Behaviour when PO/GRN/PI rates differ.</CardDescription>
        <div className="mt-4 space-y-3">
          <Toggle
            label="Maintain same rate throughout the purchase cycle"
            hint="Disallow rate changes on PI/GRN if the PO already set one."
            checked={form.maintain_same_rate}
            onChange={(v) => setF('maintain_same_rate', v)}
          />
          <Toggle
            label="Allow the same item to appear on multiple lines"
            hint="Off = each item may appear only once per document."
            checked={form.allow_item_multiple_times}
            onChange={(v) => setF('allow_item_multiple_times', v)}
          />
          <Toggle
            label="Disable last-purchase-rate suggestion"
            hint="The PO form falls back to standard rate instead."
            checked={form.disable_last_purchase_rate}
            onChange={(v) => setF('disable_last_purchase_rate', v)}
          />
          <Toggle
            label="Bill for rejected qty on Purchase Invoice"
            hint="On = rejected qty still appears in the PI; off = only accepted qty is billable."
            checked={form.bill_for_rejected_qty}
            onChange={(v) => setF('bill_for_rejected_qty', v)}
          />
        </div>
      </Card>

      <div className="rounded-lg border border-hairline bg-surface-soft p-3 text-caption text-stone flex items-start gap-2">
        <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
        <div>
          <div className="text-ink text-body-sm font-medium mb-0.5">When changes take effect</div>
          Settings are cached for 60 seconds on the server. New submits will pick up your change on the next submit after that.
        </div>
      </div>
    </div>
  );
}

function Toggle({
  label, hint, checked, onChange,
}: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-start gap-3 cursor-pointer">
      <input
        type="checkbox" className="mt-1 accent-brand-green-deep"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
      />
      <div>
        <div className="text-body-sm text-charcoal">{label}</div>
        {hint && <div className="text-caption text-stone">{hint}</div>}
      </div>
    </label>
  );
}
