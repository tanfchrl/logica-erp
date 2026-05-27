import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import {
  Inbox, Check, X, ExternalLink, AlertCircle, Sparkles, ShieldCheck,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Avatar } from '@/components/Avatar';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface InboxRow {
  id: string;
  rule_id: string;
  rule_name: string;
  rule_description?: string;
  doctype: string;
  document_id: string;
  document_name: string;
  required_role_id: string;
  required_role?: string;
  requested_by: string;
  requested_by_email?: string;
  requested_at: string;
  status?: 'pending' | 'approved' | 'rejected' | 'cancelled';
  decided_at?: string;
  decision_note?: string;
  decided_by_email?: string;
}
interface InboxList { items: InboxRow[] }

type Tab = 'pending' | 'resolved';

const DOC_ROUTES: Record<string, (id: string) => string> = {
  sales_invoice:          (id) => `/accounting/sales-invoices/${id}`,
  purchase_invoice:       (id) => `/accounting/purchase-invoices/${id}`,
  payment_entry:          (id) => `/accounting/payment-entries/${id}`,
  journal_entry:          (id) => `/accounting/journal-entries/${id}`,
  period_closing_voucher: (id) => `/accounting/period-closing-vouchers/${id}`,
};

export function ApprovalsInboxSection() {
  const [tab, setTab] = useState<Tab>('pending');
  return (
    <div className="space-y-5">
      <div className="inline-flex items-center p-1 rounded-full bg-surface border border-hairline">
        <TabChip active={tab === 'pending'}  icon={Inbox}        label="Pending"  onClick={() => setTab('pending')} />
        <TabChip active={tab === 'resolved'} icon={ShieldCheck}  label="Resolved" onClick={() => setTab('resolved')} />
      </div>

      {tab === 'pending'  ? <PendingTab  /> : <ResolvedTab />}
    </div>
  );
}

function TabChip({
  active, icon: Icon, label, onClick,
}: { active: boolean; icon: React.ComponentType<{ className?: string }>; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 h-8 px-3 rounded-full text-body-sm transition-colors',
        active ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink',
      )}>
      <Icon className="size-3.5" />
      {label}
    </button>
  );
}

/* ----------------------- Pending tab ----------------------- */

function PendingTab() {
  const qc = useQueryClient();
  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['approvals-pending'],
    queryFn:  () => api<InboxList>('/admin/approvals/pending'),
  });
  const rows = data?.items ?? [];
  const [decide, setDecide] = useState<{ row: InboxRow; action: 'approve' | 'reject' } | null>(null);

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Pending for you</CardTitle>
          <CardDescription>
            Approval requests assigned to roles you hold. Acting here unblocks the requester's submit.
          </CardDescription>
        </div>
        <Button size="sm" variant="secondary" onClick={() => refetch()} loading={isFetching}>
          Refresh
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-32 w-full" /></Card>
      ) : rows.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Sparkles className="mx-auto size-6 text-brand-green mb-2" />
            <div className="text-body-sm text-charcoal">All clear — nothing waiting on you.</div>
          </div>
        </Card>
      ) : (
        <div className="space-y-2">
          {rows.map((r) => (
            <InboxRowCard
              key={r.id}
              row={r}
              onApprove={() => setDecide({ row: r, action: 'approve' })}
              onReject={() =>  setDecide({ row: r, action: 'reject'  })}
            />
          ))}
        </div>
      )}

      {decide && (
        <DecideDialog
          row={decide.row}
          action={decide.action}
          onClose={() => setDecide(null)}
          onDone={() => {
            void qc.invalidateQueries({ queryKey: ['approvals-pending'] });
            void qc.invalidateQueries({ queryKey: ['approvals-resolved'] });
            setDecide(null);
          }}
        />
      )}
    </div>
  );
}

function InboxRowCard({
  row, onApprove, onReject,
}: { row: InboxRow; onApprove: () => void; onReject: () => void }) {
  const docPath = DOC_ROUTES[row.doctype]?.(row.document_id);
  return (
    <div className="bg-canvas border border-hairline rounded-lg p-3.5 flex items-start gap-3">
      <Avatar name={row.requested_by_email || row.requested_by} size="md" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-body-sm font-medium text-ink">{row.document_name}</span>
          <span className="text-caption font-mono text-stone">{row.doctype}</span>
          <StatusPill tone="info" withDot={false}>{row.required_role}</StatusPill>
        </div>
        <div className="text-caption text-stone mt-0.5">
          {row.rule_name}{row.rule_description ? ` · ${row.rule_description}` : ''}
        </div>
        <div className="text-caption text-stone mt-0.5">
          Requested by <span className="text-charcoal">{row.requested_by_email || row.requested_by}</span> · {timeAgo(row.requested_at)}
        </div>
      </div>
      <div className="flex items-center gap-2">
        {docPath && (
          <Button asChild size="sm" variant="ghost">
            <Link to={docPath as never}>
              <ExternalLink className="size-3.5" /> Open
            </Link>
          </Button>
        )}
        <Button size="sm" variant="secondary" onClick={onReject}>
          <X className="size-3.5" /> Reject
        </Button>
        <Button size="sm" onClick={onApprove}>
          <Check className="size-3.5" /> Approve
        </Button>
      </div>
    </div>
  );
}

function DecideDialog({
  row, action, onClose, onDone,
}: { row: InboxRow; action: 'approve' | 'reject'; onClose: () => void; onDone: () => void }) {
  const approve = action === 'approve';
  const [note, setNote] = useState('');
  const [error, setError] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<void>(`/admin/approvals/${row.id}/${action}`, {
      method: 'POST',
      body: { note },
    }),
    onSuccess: () => onDone(),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>{approve ? 'Approve' : 'Reject'} {row.document_name}?</DialogTitle>
        <DialogDescription>
          Rule: <span className="text-ink">{row.rule_name}</span>. Your decision is logged with your user id and the note below.
        </DialogDescription>

        <form onSubmit={(e) => { e.preventDefault(); setError(null); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="Note" hint={approve ? 'Optional.' : 'Recommended — tells the requester what to fix.'}>
            <textarea className="input-base !h-auto !py-2" rows={3}
              value={note} onChange={(e) => setNote(e.target.value)}
              placeholder={approve ? 'Looks good — approved.' : 'Please clarify the supplier ref and resubmit.'} />
          </Field>
          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" variant={approve ? 'primary' : 'danger'} loading={mut.isPending}>
              {approve ? <><Check className="size-3.5" /> Approve</> : <><X className="size-3.5" /> Reject</>}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ----------------------- Resolved tab ----------------------- */

function ResolvedTab() {
  const { data, isLoading } = useQuery({
    queryKey: ['approvals-resolved'],
    queryFn:  () => api<InboxList>('/admin/approvals/resolved'),
  });
  const rows = data?.items ?? [];

  if (isLoading) return <Card><Skeleton className="h-32 w-full" /></Card>;
  if (rows.length === 0) {
    return (
      <Card>
        <div className="text-center py-8 text-body-sm text-stone">
          <ShieldCheck className="mx-auto size-5 mb-2" /> No recent decisions.
        </div>
      </Card>
    );
  }

  return (
    <Card padded={false}>
      <table className="w-full text-body-sm">
        <thead className="bg-surface-soft border-b border-hairline">
          <tr className="text-micro-uppercase text-stone">
            <th className="text-left  font-medium px-4 py-2.5">When</th>
            <th className="text-left  font-medium px-4 py-2.5">Document</th>
            <th className="text-left  font-medium px-4 py-2.5">Rule</th>
            <th className="text-left  font-medium px-4 py-2.5">Note</th>
            <th className="text-right font-medium px-4 py-2.5">Decision</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const docPath = DOC_ROUTES[r.doctype]?.(r.document_id);
            return (
              <tr key={r.id} className="border-b border-hairline last:border-0">
                <td className="px-4 py-2 text-stone num whitespace-nowrap">{r.decided_at ? new Date(r.decided_at).toLocaleString('id-ID') : '—'}</td>
                <td className="px-4 py-2">
                  {docPath
                    ? <Link to={docPath as never} className="text-ink hover:underline">{r.document_name}</Link>
                    : <span className="text-ink">{r.document_name}</span>}
                  <div className="text-caption text-stone font-mono">{r.doctype}</div>
                </td>
                <td className="px-4 py-2 text-charcoal">{r.rule_name}</td>
                <td className="px-4 py-2 text-stone truncate max-w-[280px]">{r.decision_note || '—'}</td>
                <td className="px-4 py-2 text-right">
                  {r.status === 'approved'
                    ? <StatusPill tone="success">Approved</StatusPill>
                    : <StatusPill tone="danger">Rejected</StatusPill>}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </Card>
  );
}

/* ----- helpers ----- */

function timeAgo(iso: string): string {
  const sec = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.round(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.round(sec / 3600)}h ago`;
  return `${Math.round(sec / 86400)}d ago`;
}
