import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  ShieldCheck, Check, X, AlertCircle, Clock, Loader2,
} from 'lucide-react';
import { Button } from '@/components/Button';
import { Field } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Avatar } from '@/components/Avatar';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { me } from '@/lib/auth';
import { cn } from '@/lib/cn';

/**
 * ApprovalWidget — surfaces approval_request rows for a single document.
 * Renders nothing if there are no approval rows. Otherwise shows a card with:
 *   - pending requests (with inline approve/reject for eligible viewers)
 *   - approved / rejected rows for audit
 */

interface ApprovalRow {
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
interface ApprovalList { items: ApprovalRow[] }

export function ApprovalWidget({
  doctype, documentId, className,
}: { doctype: string; documentId: string; className?: string }) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['approvals-by-doc', doctype, documentId],
    queryFn:  () => api<ApprovalList>(`/admin/approvals/by-doc/${doctype}/${documentId}`),
    enabled:  !!documentId,
  });
  const { data: caller } = useQuery({ queryKey: ['me'], queryFn: () => me() });

  const [decide, setDecide] = useState<{ row: ApprovalRow; action: 'approve' | 'reject' } | null>(null);

  if (!documentId) return null;
  if (isLoading) {
    return (
      <div className={cn('bg-canvas border border-hairline rounded-lg p-3 inline-flex items-center gap-2 text-caption text-stone', className)}>
        <Loader2 className="size-3.5 animate-spin" /> Checking approvals…
      </div>
    );
  }

  const rows = data?.items ?? [];
  if (rows.length === 0) return null;

  const pending  = rows.filter((r) => r.status === 'pending');
  const approved = rows.filter((r) => r.status === 'approved');
  const rejected = rows.filter((r) => r.status === 'rejected');

  const canActOn = (row: ApprovalRow) =>
    caller?.is_system || (caller?.roles ?? []).includes(row.required_role_id);

  const tone =
    rejected.length ? 'danger' :
    pending.length  ? 'warning' :
                      'success';
  const toneClass =
    tone === 'danger'  ? 'border-brand-error/30 bg-brand-error/5' :
    tone === 'warning' ? 'border-warning/30 bg-warning/5' :
                         'border-success/30 bg-success/5';
  const headIcon =
    tone === 'danger'  ? <AlertCircle  className="size-4 text-brand-error" /> :
    tone === 'warning' ? <Clock        className="size-4 text-warning" /> :
                         <ShieldCheck  className="size-4 text-success" />;
  const headline =
    tone === 'danger'  ? `${rejected.length} approval ${rejected.length === 1 ? 'request was' : 'requests were'} rejected — submit is blocked` :
    tone === 'warning' ? `${pending.length} approval ${pending.length === 1 ? 'request' : 'requests'} pending — submit will be blocked` :
                         `All approvals granted — ready to submit`;

  return (
    <div className={cn('rounded-lg border', toneClass, className)}>
      <div className="px-4 py-3 flex items-center gap-2">
        {headIcon}
        <span className="text-body-sm text-ink font-medium">{headline}</span>
      </div>

      <ul className="border-t border-hairline divide-y divide-hairline bg-canvas/40">
        {/* Show rejected first (most urgent), then pending, then approved */}
        {[...rejected, ...pending, ...approved].map((row) => (
          <li key={row.id} className="px-4 py-3 flex items-start gap-3">
            <Avatar
              name={row.status === 'pending' ? (row.requested_by_email || row.requested_by) : (row.decided_by_email || row.required_role)}
              size="sm"
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                {row.status === 'pending'  && <StatusPill tone="warning"><Clock        className="size-3" /> Pending</StatusPill>}
                {row.status === 'approved' && <StatusPill tone="success"><Check        className="size-3" /> Approved</StatusPill>}
                {row.status === 'rejected' && <StatusPill tone="danger"> <X            className="size-3" /> Rejected</StatusPill>}
                <span className="text-body-sm text-ink">{row.rule_name}</span>
                <span className="text-caption text-stone">requires</span>
                <span className="text-caption font-medium text-ink">{row.required_role}</span>
              </div>
              <div className="text-caption text-stone mt-0.5">
                {row.status === 'pending'
                  ? <>Requested {timeAgo(row.requested_at)}{row.requested_by_email ? ` by ${row.requested_by_email}` : ''}</>
                  : <>{row.status === 'approved' ? 'Approved' : 'Rejected'} {row.decided_at ? timeAgo(row.decided_at) : ''}{row.decided_by_email ? ` by ${row.decided_by_email}` : ''}</>}
              </div>
              {row.decision_note && (
                <div className="mt-1.5 text-caption text-charcoal bg-surface-soft rounded-md px-2 py-1.5 border border-hairline">
                  “{row.decision_note}”
                </div>
              )}
            </div>

            {row.status === 'pending' && (
              canActOn(row) ? (
                <div className="flex items-center gap-1 shrink-0">
                  <Button size="sm" variant="secondary"
                    onClick={() => setDecide({ row, action: 'reject' })}>
                    <X className="size-3.5" /> Reject
                  </Button>
                  <Button size="sm"
                    onClick={() => setDecide({ row, action: 'approve' })}>
                    <Check className="size-3.5" /> Approve
                  </Button>
                </div>
              ) : (
                <span className="text-caption text-stone shrink-0">Awaiting {row.required_role}</span>
              )
            )}
          </li>
        ))}
      </ul>

      {decide && (
        <DecideDialog
          row={decide.row}
          action={decide.action}
          onClose={() => setDecide(null)}
          onDone={() => {
            void qc.invalidateQueries({ queryKey: ['approvals-by-doc', doctype, documentId] });
            void qc.invalidateQueries({ queryKey: ['approvals-pending'] });
            void qc.invalidateQueries({ queryKey: ['approvals-resolved'] });
            setDecide(null);
          }}
        />
      )}
    </div>
  );
}

function DecideDialog({
  row, action, onClose, onDone,
}: { row: ApprovalRow; action: 'approve' | 'reject'; onClose: () => void; onDone: () => void }) {
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
          Rule: <span className="text-ink">{row.rule_name}</span>. The decision is logged with your user id and the note below.
        </DialogDescription>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="Note" hint={approve ? 'Optional.' : 'Recommended — tells the requester what to fix.'}>
            <textarea className="input-base !h-auto !py-2" rows={3}
              value={note} onChange={(e) => setNote(e.target.value)}
              placeholder={approve ? 'Looks good — approved.' : 'Please clarify…'} />
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

function timeAgo(iso: string): string {
  const sec = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.round(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.round(sec / 3600)}h ago`;
  return `${Math.round(sec / 86400)}d ago`;
}
