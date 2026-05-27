import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import Decimal from 'decimal.js';
import {
  Plus, Trash2, Send, Save, Ban, ArrowLeft, ArrowRight, CheckCircle2, AlertCircle,
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
import { api } from '@/lib/api';
import { money } from '@/lib/format';
import { toast } from '@/components/Toaster';

interface Account { id: string; name: string; account_number?: string; root_type: string; }
interface JELine { id: string; row_index: number; account_id: string; debit: string; credit: string; reference?: string; }
interface JournalEntry {
  id: string; name: string; company_id: string;
  posting_date: string; currency: string; exchange_rate: string;
  total_debit: string; total_credit: string; user_remark?: string;
  docstatus: 0 | 1 | 2;
  accounts: JELine[];
}
interface DraftLine { rowId: string; account_id: string | null; debit: string; credit: string; reference?: string; }

const rid = () => Math.random().toString(36).slice(2);
const todayISO = () => new Date().toISOString().slice(0, 10);

export function JournalEntryForm() {
  const params = useParams({ strict: false }) as { id?: string };
  const id = params.id;
  const isNew = !id || id === 'new';
  const navigate = useNavigate();
  const qc = useQueryClient();

  const { data: existing } = useQuery({
    queryKey: ['journal-entry', id],
    enabled: !isNew,
    queryFn: () => api<JournalEntry>(`/accounting/journal-entries/${id}`),
  });

  const { data: accounts } = useQuery({
    queryKey: ['accounts'],
    queryFn: () => api<{ items: Account[] }>('/accounting/accounts'),
  });

  const [postingDate, setPostingDate] = useState(todayISO());
  const [remark, setRemark] = useState('');
  const [lines, setLines] = useState<DraftLine[]>([
    { rowId: rid(), account_id: null, debit: '', credit: '' },
    { rowId: rid(), account_id: null, debit: '', credit: '' },
  ]);

  useEffect(() => {
    if (!existing) return;
    setPostingDate(existing.posting_date.slice(0, 10));
    setRemark(existing.user_remark ?? '');
    setLines(existing.accounts.map((l) => ({
      rowId: l.id,
      account_id: l.account_id,
      debit: l.debit !== '0' ? l.debit : '',
      credit: l.credit !== '0' ? l.credit : '',
      reference: l.reference,
    })));
  }, [existing]);

  const accountOptions = useMemo(
    () => (accounts?.items ?? []).map((a) => ({
      value: a.id,
      label: a.name,
      description: [a.account_number, a.root_type].filter(Boolean).join(' · '),
    })),
    [accounts],
  );

  const totals = useMemo(() => {
    const dr = lines.reduce((a, l) => a.plus(l.debit  || '0'), new Decimal(0));
    const cr = lines.reduce((a, l) => a.plus(l.credit || '0'), new Decimal(0));
    return { dr, cr, diff: dr.minus(cr), balanced: dr.equals(cr) && !dr.isZero() };
  }, [lines]);

  const editable = isNew || existing?.docstatus === 0;
  const submitted = existing?.docstatus === 1;
  const cancelled = existing?.docstatus === 2;

  const createMutation = useMutation({
    mutationFn: async () => {
      if (!totals.balanced) throw { message: `Debits ${totals.dr} ≠ credits ${totals.cr}. Difference: ${totals.diff}.` };
      const body = {
        posting_date: postingDate,
        user_remark: remark || undefined,
        accounts: lines
          .filter((l) => l.account_id && (l.debit || l.credit))
          .map((l) => ({
            account_id: l.account_id,
            debit: l.debit || '0',
            credit: l.credit || '0',
            reference: l.reference,
          })),
      };
      return api<JournalEntry>('/accounting/journal-entries', { method: 'POST', body });
    },
    onSuccess: (je) => {
      toast.success('Draft saved', je.name);
      qc.invalidateQueries({ queryKey: ['doctype', '/accounting/journal-entries'] });
      navigate({ to: `/accounting/journal-entries/${je.id}` as never });
    },
    onError: (e: any) => toast.error('Save failed', e?.message ?? 'Check inputs'),
  });

  const submitMutation = useMutation({
    mutationFn: () => api<JournalEntry>(`/accounting/journal-entries/${id}/submit`, { method: 'POST' }),
    onSuccess: () => {
      toast.success('Submitted', 'Posted to the General Ledger.');
      qc.invalidateQueries({ queryKey: ['journal-entry', id] });
      qc.invalidateQueries({ queryKey: ['doctype', '/accounting/journal-entries'] });
    },
    onError: (e: any) => toast.error('Submit failed', e?.message),
  });

  const cancelMutation = useMutation({
    mutationFn: () => api<JournalEntry>(`/accounting/journal-entries/${id}/cancel`, { method: 'POST' }),
    onSuccess: () => {
      toast.success('Cancelled', 'Posted reversing GL entries.');
      qc.invalidateQueries({ queryKey: ['journal-entry', id] });
      qc.invalidateQueries({ queryKey: ['doctype', '/accounting/journal-entries'] });
    },
    onError: (e: any) => toast.error('Cancel failed', e?.message),
  });

  const addLine = () => setLines((ls) => [...ls, { rowId: rid(), account_id: null, debit: '', credit: '' }]);
  const removeLine = (rowId: string) => setLines((ls) => ls.length > 2 ? ls.filter((l) => l.rowId !== rowId) : ls);
  const update = (rowId: string, patch: Partial<DraftLine>) =>
    setLines((ls) => ls.map((l) => l.rowId === rowId ? { ...l, ...patch } : l));

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Finance', to: '/accounting' },
          { label: 'Journal Entries', to: '/accounting/journal-entries' },
          { label: isNew ? 'New' : (existing?.name ?? '…') },
        ]}
        title={isNew ? 'New Journal Entry' : (existing?.name ?? '…')}
        status={existing && <DocstatusPill docstatus={existing.docstatus} />}
        actions={
          <>
            <Button variant="ghost" asChild><Link to={'/accounting/journal-entries' as never}><ArrowLeft className="size-4" /> Back</Link></Button>
            {editable && isNew && (
              <Button onClick={() => createMutation.mutate()} loading={createMutation.isPending} disabled={!totals.balanced}>
                <Save className="size-4" /> Save draft
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
        className="flex-1 px-6 lg:px-8 pb-8 grid grid-cols-1 lg:grid-cols-[1fr,320px] gap-4 max-w-[1400px]"
      >
        <div className="space-y-4 min-w-0">
          {cancelled && (
            <Card className="!p-3 flex items-center gap-3 bg-danger/5 border-danger/30">
              <AlertCircle className="size-4 text-danger" />
              <span className="text-body text-danger">Cancelled — reversing entries posted.</span>
            </Card>
          )}

          {!isNew && existing && (
            <ApprovalWidget doctype="journal_entry" documentId={existing.id} />
          )}

          <Card>
            <CardTitle>Header</CardTitle>
            <div className="mt-4 grid sm:grid-cols-2 gap-4">
              <Field label="Posting date">
                <Input type="date" value={postingDate} onChange={(e) => setPostingDate(e.target.value)} disabled={!editable} />
              </Field>
              <Field label="Remark">
                <Input value={remark} onChange={(e) => setRemark(e.target.value)} disabled={!editable} placeholder="e.g. Setoran modal pendiri" />
              </Field>
            </div>
          </Card>

          <Card padded={false}>
            <div className="flex items-center justify-between p-5 pb-3">
              <CardTitle>Lines</CardTitle>
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
                    <th className="text-left font-medium text-text-secondary px-4 py-2 min-w-[260px]">Account</th>
                    <th className="text-right font-medium text-text-secondary px-4 py-2 w-44">Debit</th>
                    <th className="text-right font-medium text-text-secondary px-4 py-2 w-44">Credit</th>
                    <th className="text-left font-medium text-text-secondary px-4 py-2">Reference</th>
                    {editable && <th className="w-10" />}
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l, idx) => (
                    <tr key={l.rowId} className="border-b border-border last:border-0">
                      <td className="px-4 py-1.5 text-text-tertiary">{idx + 1}</td>
                      <td className="px-2 py-1.5">
                        {editable
                          ? <Combobox
                              options={accountOptions}
                              value={l.account_id}
                              onChange={(v) => update(l.rowId, { account_id: v })}
                              placeholder="Pick account…"
                            />
                          : <span className="px-3 text-text-primary">{accountOptions.find((o) => o.value === l.account_id)?.label || l.account_id}</span>}
                      </td>
                      <td className="px-2 py-1.5">
                        {editable
                          ? <NumericInput value={l.debit} onChange={(v) => update(l.rowId, { debit: v, credit: v ? '' : l.credit })} />
                          : <span className="num text-right block px-3">{l.debit !== '0' ? money(l.debit) : '—'}</span>}
                      </td>
                      <td className="px-2 py-1.5">
                        {editable
                          ? <NumericInput value={l.credit} onChange={(v) => update(l.rowId, { credit: v, debit: v ? '' : l.debit })} />
                          : <span className="num text-right block px-3">{l.credit !== '0' ? money(l.credit) : '—'}</span>}
                      </td>
                      <td className="px-2 py-1.5">
                        {editable ? <Input value={l.reference ?? ''} onChange={(e) => update(l.rowId, { reference: e.target.value })} />
                                  : <span className="text-text-secondary px-3">{l.reference || '—'}</span>}
                      </td>
                      {editable && (
                        <td className="px-2 py-1.5">
                          <Button variant="ghost" size="icon" aria-label="Remove row" onClick={() => removeLine(l.rowId)} disabled={lines.length <= 2}>
                            <Trash2 className="size-3.5" />
                          </Button>
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="px-5 py-4 border-t border-border flex items-center justify-end gap-6 num">
              <div className="text-caption text-text-tertiary">Debit</div>
              <div className="font-medium">{money(totals.dr.toString())}</div>
              <div className="text-caption text-text-tertiary"><ArrowRight className="size-3 inline mx-1 opacity-40" />Credit</div>
              <div className="font-medium">{money(totals.cr.toString())}</div>
              {totals.balanced
                ? <StatusPill tone="success"><CheckCircle2 className="size-3" /> Balanced</StatusPill>
                : <StatusPill tone={totals.diff.isZero() ? 'neutral' : 'warning'}>
                    {totals.diff.isZero() ? 'No amount' : `Diff: ${money(totals.diff.abs().toString())}`}
                  </StatusPill>}
            </div>
          </Card>
        </div>

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
                  <div className="flex items-center justify-between"><span className="text-text-secondary">Posting</span><span>{existing.posting_date.slice(0,10)}</span></div>
                  <div className="flex items-center justify-between"><span className="text-text-secondary">Total Dr</span><span className="num">{money(existing.total_debit)}</span></div>
                  <div className="flex items-center justify-between"><span className="text-text-secondary">Total Cr</span><span className="num">{money(existing.total_credit)}</span></div>
                </>
              )}
            </div>
          </Card>
          <Card>
            <CardTitle>Tips</CardTitle>
            <ul className="mt-2 space-y-2 text-caption text-text-secondary">
              <li>The ledger invariant (Dr = Cr) is enforced server-side inside the submit transaction.</li>
              <li>Cancel posts inverse entries — both visible in reports, net to zero.</li>
            </ul>
          </Card>
        </aside>
      </motion.div>
    </>
  );
}
