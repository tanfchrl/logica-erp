import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Download, AlertCircle, ScrollText, Calendar, FileDown } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { apiBlob } from '@/lib/api';

export function EFakturSection() {
  const today = new Date();
  const firstOfMonth = new Date(today.getFullYear(), today.getMonth(), 1);
  const lastOfMonth  = new Date(today.getFullYear(), today.getMonth() + 1, 0);
  const fmt = (d: Date) => d.toISOString().slice(0, 10);

  const [from, setFrom] = useState(fmt(firstOfMonth));
  const [to,   setTo]   = useState(fmt(lastOfMonth));
  const [error, setError] = useState<string | null>(null);
  const [lastRun, setLastRun] = useState<{ when: string; range: string; rowCount: number | null } | null>(null);

  const download = useMutation({
    mutationFn: async () => {
      const blob = await apiBlob(`/accounting/exports/efaktur?from_date=${from}&to_date=${to}`);
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `efaktur_${from}_${to}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      return blob;
    },
    onSuccess: (blob) => {
      setError(null);
      setLastRun({ when: new Date().toISOString(), range: `${from} → ${to}`, rowCount: null });
    },
    onError: (e: Error) => setError(e.message),
  });

  return (
    <div className="space-y-5">
      <div>
        <CardTitle>e-Faktur / Coretax</CardTitle>
        <CardDescription>
          Export submitted Sales Invoices for the active company in the e-Faktur CSV format.
          Pick a date range and the file downloads to your browser. Direct Coretax DJP submission is a follow-up.
        </CardDescription>
      </div>

      <div className="rounded-lg border border-hairline bg-surface-soft p-3 text-caption text-stone">
        Backend status: <strong className="text-charcoal">CSV export ready</strong> · Coretax direct API integration pending DJP sandbox access.
      </div>

      <Card>
        <CardTitle>Generate CSV</CardTitle>
        <CardDescription className="mb-4">Only invoices with <span className="font-mono text-ink">docstatus = 1</span> in the range are included.</CardDescription>
        <div className="grid sm:grid-cols-3 gap-3 items-end">
          <Field label="From date">
            <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </Field>
          <Field label="To date">
            <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
          </Field>
          <Button onClick={() => download.mutate()} loading={download.isPending}>
            <Download className="size-3.5" /> Download CSV
          </Button>
        </div>
        {error && (
          <div className="mt-3 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
            <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
          </div>
        )}
        {lastRun && !error && (
          <div className="mt-3 text-caption text-stone">
            Last export: <span className="text-ink">{lastRun.range}</span> at {new Date(lastRun.when).toLocaleString('id-ID')}.
          </div>
        )}
      </Card>

      <Card>
        <CardTitle>What gets exported</CardTitle>
        <CardDescription className="mb-3">The CSV follows the e-Faktur header schema accepted by DJP's bulk upload.</CardDescription>
        <ul className="space-y-1.5 text-body-sm text-charcoal">
          <li className="flex items-start gap-2"><Calendar className="size-4 text-stone mt-0.5 shrink-0" /> Faktur date + faktur number (when set on the SI)</li>
          <li className="flex items-start gap-2"><ScrollText className="size-4 text-stone mt-0.5 shrink-0" /> Customer NPWP and address</li>
          <li className="flex items-start gap-2"><FileDown className="size-4 text-stone mt-0.5 shrink-0" /> Per-line: item, qty, DPP (net), PPN, total</li>
        </ul>
      </Card>
    </div>
  );
}
