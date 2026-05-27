import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Calculator, Calendar, Lock, Unlock, AlertCircle, Play, CheckCircle2 } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface FiscalYear {
  id: string;
  name: string;
  start_date: string;
  end_date: string;
  is_closed: boolean;
  companies: string[];
}
interface FYList { items: FiscalYear[] }

interface Account     { id: string; account_name: string; account_number?: string; root_type?: string }
interface AccountList { items: Account[] }

interface PCV {
  id: string;
  name: string;
  fiscal_year_id: string;
  posting_date: string;
  closing_account_id: string;
  remarks?: string;
  docstatus: number;
  created_at: string;
}
interface PCVList { items: PCV[] }

export function FiscalYearsSection() {
  const qc = useQueryClient();
  const { data: fy, isLoading } = useQuery({
    queryKey: ['fiscal-years'],
    queryFn: () => api<FYList>('/admin/fiscal-years'),
  });
  const { data: pcvs } = useQuery({
    queryKey: ['period-closing-vouchers'],
    queryFn: () => api<PCVList>('/accounting/period-closing-vouchers'),
  });

  const [createOpen, setCreateOpen] = useState(false);
  const [closingFY, setClosingFY]   = useState<FiscalYear | null>(null);

  const years = fy?.items ?? [];

  // For each FY, find the submitted PCV if any.
  const closedByFY = useMemo(() => {
    const m = new Map<string, PCV>();
    for (const p of (pcvs?.items ?? [])) {
      if (p.docstatus === 1) m.set(p.fiscal_year_id, p);
    }
    return m;
  }, [pcvs]);

  return (
    <div className="space-y-6">
      {/* ---- Fiscal years ---- */}
      <section>
        <div className="mb-3 flex items-end justify-between gap-3 flex-wrap">
          <div>
            <CardTitle>Fiscal years</CardTitle>
            <CardDescription>
              Indonesian SMEs typically follow calendar year (Jan–Dec). Add one per accounting period.
            </CardDescription>
          </div>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="size-3.5" /> New fiscal year
          </Button>
        </div>

        {isLoading ? (
          <Card><Skeleton className="h-24 w-full" /></Card>
        ) : years.length === 0 ? (
          <Card>
            <div className="text-center py-8">
              <Calendar className="mx-auto size-6 text-stone mb-2" />
              <div className="text-body-sm text-charcoal">No fiscal years yet.</div>
              <div className="text-caption text-stone mt-1">Add one to start posting documents.</div>
              <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
                <Plus className="size-3.5" /> Create fiscal year
              </Button>
            </div>
          </Card>
        ) : (
          <div className="space-y-2">
            {years.map((y) => (
              <FYRow
                key={y.id}
                fy={y}
                closedBy={closedByFY.get(y.id) ?? null}
                onClose={() => setClosingFY(y)}
                onChanged={() => qc.invalidateQueries({ queryKey: ['fiscal-years'] })}
              />
            ))}
          </div>
        )}
      </section>

      {/* ---- Period close history ---- */}
      <section>
        <div className="mb-3">
          <CardTitle>Period close history</CardTitle>
          <CardDescription>
            Submitting a PCV posts an offsetting JE that zeroes income + expense accounts into retained earnings.
          </CardDescription>
        </div>
        {(pcvs?.items ?? []).length === 0 ? (
          <Card>
            <div className="text-center py-6 text-body-sm text-stone">
              <Calculator className="mx-auto size-5 mb-2" /> No period closes yet.
            </div>
          </Card>
        ) : (
          <Card padded={false}>
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft border-b border-hairline">
                <tr className="text-micro-uppercase text-stone">
                  <th className="text-left  font-medium px-4 py-2.5">Voucher</th>
                  <th className="text-left  font-medium px-4 py-2.5">Posting date</th>
                  <th className="text-left  font-medium px-4 py-2.5">Fiscal year</th>
                  <th className="text-right font-medium px-4 py-2.5">Status</th>
                </tr>
              </thead>
              <tbody>
                {(pcvs?.items ?? []).map((p) => {
                  const fyName = years.find((y) => y.id === p.fiscal_year_id)?.name ?? '—';
                  return (
                    <tr key={p.id} className="border-b border-hairline last:border-0">
                      <td className="px-4 py-2 font-mono text-ink">{p.name}</td>
                      <td className="px-4 py-2 text-stone num">{p.posting_date.slice(0, 10)}</td>
                      <td className="px-4 py-2 text-charcoal">{fyName}</td>
                      <td className="px-4 py-2 text-right">
                        {p.docstatus === 0 && <StatusPill tone="neutral">Draft</StatusPill>}
                        {p.docstatus === 1 && <StatusPill tone="success">Submitted</StatusPill>}
                        {p.docstatus === 2 && <StatusPill tone="danger">Cancelled</StatusPill>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </Card>
        )}
      </section>

      {createOpen && (
        <CreateFYDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => { void qc.invalidateQueries({ queryKey: ['fiscal-years'] }); setCreateOpen(false); }}
        />
      )}

      {closingFY && (
        <RunCloseDialog
          fy={closingFY}
          onClose={() => setClosingFY(null)}
          onDone={() => { void qc.invalidateQueries({ queryKey: ['period-closing-vouchers'] }); setClosingFY(null); }}
        />
      )}
    </div>
  );
}

/* ---------------------------- FY row ---------------------------- */

function FYRow({
  fy, closedBy, onClose, onChanged,
}: { fy: FiscalYear; closedBy: PCV | null; onClose: () => void; onChanged: () => void }) {
  const qc = useQueryClient();
  const toggle = useMutation({
    mutationFn: (close: boolean) =>
      api<FiscalYear>(`/admin/fiscal-years/${fy.id}`, { method: 'PUT', body: { is_closed: close } }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['fiscal-years'] }); onChanged(); },
  });

  const yearDays = Math.round((new Date(fy.end_date).getTime() - new Date(fy.start_date).getTime()) / 86400000);

  return (
    <div className={cn('bg-canvas border rounded-lg p-3',
      fy.is_closed ? 'border-hairline' : 'border-hairline')}>
      <div className="flex items-center gap-3 flex-wrap">
        <span className="inline-flex items-center justify-center size-9 rounded-md bg-surface text-ink">
          <Calendar className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-body-sm font-medium text-ink">{fy.name}</span>
            {fy.is_closed && <StatusPill tone="neutral" withDot={false}><Lock className="size-3" /> Closed</StatusPill>}
            {closedBy && <StatusPill tone="success" withDot={false}><CheckCircle2 className="size-3" /> Period closed via {closedBy.name}</StatusPill>}
          </div>
          <div className="text-caption text-stone mt-0.5">
            {fy.start_date.slice(0, 10)} → {fy.end_date.slice(0, 10)} · {yearDays} days ·{' '}
            {fy.companies.length === 0 ? 'All companies' : `${fy.companies.length} companies`}
          </div>
        </div>

        <div className="flex items-center gap-1">
          {!fy.is_closed && !closedBy && (
            <Button size="sm" onClick={onClose}>
              <Play className="size-3.5" /> Run period close
            </Button>
          )}
          <Button size="sm" variant="ghost"
            onClick={() => toggle.mutate(!fy.is_closed)}
            loading={toggle.isPending}>
            {fy.is_closed
              ? <><Unlock className="size-3.5" /> Re-open</>
              : <><Lock className="size-3.5" /> Lock</>}
          </Button>
        </div>
      </div>
    </div>
  );
}

/* ---------------------- Create FY dialog ---------------------- */

function CreateFYDialog({
  onClose, onCreated,
}: { onClose: () => void; onCreated: () => void }) {
  const nextYear = new Date().getFullYear();
  const [name, setName]       = useState(`FY ${nextYear}`);
  const [start, setStart]     = useState(`${nextYear}-01-01`);
  const [end, setEnd]         = useState(`${nextYear}-12-31`);
  const [error, setError]     = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<FiscalYear>('/admin/fiscal-years', {
      method: 'POST',
      body: { name, start_date: start, end_date: end },
    }),
    onSuccess: () => onCreated(),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New fiscal year</DialogTitle>
        <DialogDescription>
          Fiscal years are workspace-wide. Each can be locked to prevent back-dated entries.
        </DialogDescription>

        <form onSubmit={(e) => { e.preventDefault(); setError(null); if (!name) return setError('Name required'); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} required />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Start date">
              <Input type="date" value={start} onChange={(e) => setStart(e.target.value)} required />
            </Field>
            <Field label="End date">
              <Input type="date" value={end} onChange={(e) => setEnd(e.target.value)} required />
            </Field>
          </div>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ---------------------- Run period close ---------------------- */

function RunCloseDialog({
  fy, onClose, onDone,
}: { fy: FiscalYear; onClose: () => void; onDone: () => void }) {
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

  const [closingAccount, setClosingAccount] = useState('');
  const [postingDate, setPostingDate]       = useState(fy.end_date.slice(0, 10));
  const [remarks, setRemarks]               = useState(`Period close — ${fy.name}`);
  const [error, setError]                   = useState<string | null>(null);
  const [createdId, setCreatedId]           = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => api<PCV>('/accounting/period-closing-vouchers', {
      method: 'POST',
      body: {
        fiscal_year_id: fy.id,
        posting_date: postingDate,
        closing_account_id: closingAccount,
        remarks,
      },
    }),
    onSuccess: (pcv) => setCreatedId(pcv.id),
    onError:   (e: Error) => setError(e.message),
  });
  const submit = useMutation({
    mutationFn: () => api<PCV>(`/accounting/period-closing-vouchers/${createdId}/submit`, { method: 'POST' }),
    onSuccess: () => onDone(),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>Run period close — {fy.name}</DialogTitle>
        <DialogDescription>
          Creates a Period Closing Voucher that zeroes income + expense accounts into the chosen retained-earnings account.
          You can review the draft before submitting.
        </DialogDescription>

        <form onSubmit={(e) => {
            e.preventDefault();
            setError(null);
            if (!closingAccount) return setError('Pick a retained-earnings (equity) account.');
            create.mutate();
          }}
          className="mt-4 space-y-3"
        >
          <Field label="Posting date" hint="Should fall within the fiscal year being closed.">
            <Input type="date" value={postingDate} onChange={(e) => setPostingDate(e.target.value)} required />
          </Field>
          <Field label="Closing account" hint="Equity account, typically Retained Earnings.">
            <NativeSelect value={closingAccount} options={accountOpts} onChange={setClosingAccount} placeholder="Select account…" />
          </Field>
          <Field label="Remarks">
            <Input value={remarks} onChange={(e) => setRemarks(e.target.value)} />
          </Field>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>
              {createdId ? 'Close (draft kept)' : 'Cancel'}
            </Button>
            {!createdId ? (
              <Button type="submit" loading={create.isPending}>
                <Play className="size-3.5" /> Create draft
              </Button>
            ) : (
              <Button type="button" loading={submit.isPending} onClick={() => submit.mutate()}>
                <CheckCircle2 className="size-3.5" /> Submit period close
              </Button>
            )}
          </div>

          {createdId && !submit.isSuccess && (
            <div className="rounded-md bg-info/10 text-info text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" />
              Draft <span className="font-mono">{createdId}</span> created. Submitting posts irreversible GL entries.
            </div>
          )}
        </form>
      </DialogContent>
    </Dialog>
  );
}

function NativeSelect({
  value, options, onChange, placeholder,
}: { value: string; options: { value: string; label: string }[]; onChange: (v: string) => void; placeholder?: string }) {
  return (
    <select
      className={cn('input-base appearance-none pr-8 bg-no-repeat bg-[right_0.75rem_center] bg-[length:1.25rem] cursor-pointer')}
      style={{ backgroundImage: "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>\")" }}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {placeholder && <option value="">{placeholder}</option>}
      {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  );
}
