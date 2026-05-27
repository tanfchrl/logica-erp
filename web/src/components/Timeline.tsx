import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Plus, Pencil, CheckCircle2, XCircle, Eye, MessageSquare, RefreshCw,
  Send, Ban, ShieldCheck, ShieldAlert, Clock3, Info,
} from 'lucide-react';
import { Card, CardTitle } from '@/components/Card';
import { Skeleton } from '@/components/EmptyState';
import { StatusPill } from '@/components/StatusPill';
import { Dialog, DialogContent, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

/**
 * Document timeline — clean, human-readable feed: who did what, when.
 *
 * Surface kinds:
 *   view     — "viewed this"
 *   event    — "created", "modified fields", "submitted", "cancelled"
 *   comment  — body shown inline
 *   approval — "requested approval (Rule X)", "approved", "rejected"
 *
 * Diff payloads / raw JSON are NOT shown inline. System users see a small
 * "details" icon next to each event that opens a modal with the raw audit
 * payload. Non-system users never see the JSON (server strips it too).
 */

interface TimelineProps {
  doctype: string;
  documentId: string;
  className?: string;
  /** Hide views to keep the list tighter (events + comments + approvals only). */
  hideViews?: boolean;
}

interface TimelineEntry {
  kind: 'event' | 'view' | 'comment' | 'approval';
  id: string;
  occurred_at: string;
  user_id: string;
  user_email?: string;
  user_name?: string;
  action?: string;
  diff?: unknown;
  body?: string;
  rule_name?: string;
}

interface TimelineResponse {
  items: TimelineEntry[];
  can_view_details: boolean;
}

export function Timeline({ doctype, documentId, className, hideViews }: TimelineProps) {
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['timeline', doctype, documentId],
    queryFn: () => api<TimelineResponse>(
      `/platform/timeline?doctype=${encodeURIComponent(doctype)}&document_id=${encodeURIComponent(documentId)}&limit=80`,
    ),
    enabled: !!doctype && !!documentId,
  });

  const [detailsFor, setDetailsFor] = useState<TimelineEntry | null>(null);
  const items = (data?.items ?? []).filter((it) => !(hideViews && it.kind === 'view'));
  const canViewDetails = !!data?.can_view_details;

  return (
    <Card className={cn('relative', className)}>
      <div className="flex items-center justify-between">
        <CardTitle>Activity</CardTitle>
        <button
          type="button"
          onClick={() => refetch()}
          className="text-stone hover:text-ink"
          aria-label="Refresh timeline"
        >
          <RefreshCw className={cn('size-3.5', isFetching && 'animate-spin')} />
        </button>
      </div>

      {isLoading && (
        <div className="mt-3 space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-3/4" />
        </div>
      )}

      {error && (
        <div className="mt-3 text-caption text-brand-error">{(error as Error).message}</div>
      )}

      {!isLoading && !error && items.length === 0 && (
        <div className="mt-3 text-caption text-stone">No activity yet.</div>
      )}

      {!isLoading && items.length > 0 && (
        <ol className="mt-4 relative">
          <div className="absolute left-[15px] top-1 bottom-1 w-px bg-hairline" aria-hidden />
          {items.map((it, i) => (
            <li key={it.id} className={cn('relative flex gap-3', i > 0 && 'mt-3')}>
              <IconBadge entry={it} />
              <div className="min-w-0 flex-1 pt-0.5">
                <div className="flex items-baseline gap-2 flex-wrap">
                  <span className="text-body-sm text-ink font-medium truncate">
                    {it.user_name || it.user_email || 'Someone'}
                  </span>
                  <span className="text-caption text-stone truncate">{describe(it)}</span>
                  {it.kind === 'approval' && it.action && it.action !== 'requested' && (
                    <ApprovalPill action={it.action} />
                  )}
                  <span className="text-caption text-stone shrink-0 ml-auto inline-flex items-center gap-2">
                    {relativeTime(it.occurred_at)}
                    {canViewDetails && (it.kind === 'event' || it.kind === 'approval') && (
                      <button
                        type="button"
                        onClick={() => setDetailsFor(it)}
                        className="text-stone hover:text-ink"
                        aria-label="View raw details"
                        title="View raw details"
                      >
                        <Info className="size-3.5" />
                      </button>
                    )}
                  </span>
                </div>
                {it.kind === 'comment' && it.body && (
                  <p className="mt-1 text-body-sm text-charcoal whitespace-pre-wrap break-words">{it.body}</p>
                )}
                {it.kind === 'approval' && it.body && (
                  <p className="mt-1 text-caption text-charcoal italic whitespace-pre-wrap break-words">"{it.body}"</p>
                )}
              </div>
            </li>
          ))}
        </ol>
      )}

      <DetailsModal entry={detailsFor} doctype={doctype} documentId={documentId} onClose={() => setDetailsFor(null)} />
    </Card>
  );
}

/* ---------- presentation ---------- */

function IconBadge({ entry }: { entry: TimelineEntry }) {
  const { Icon, cls } = badgeFor(entry);
  return (
    <span
      className={cn('relative z-[1] size-[30px] shrink-0 rounded-full border border-canvas inline-flex items-center justify-center', cls)}
      aria-hidden
    >
      <Icon className="size-3.5" />
    </span>
  );
}

function badgeFor(it: TimelineEntry): { Icon: React.ComponentType<{ className?: string }>; cls: string } {
  if (it.kind === 'view')    return { Icon: Eye,           cls: 'bg-surface text-stone' };
  if (it.kind === 'comment') return { Icon: MessageSquare, cls: 'bg-accent/10 text-accent' };
  if (it.kind === 'approval') {
    switch (it.action) {
      case 'approved':  return { Icon: ShieldCheck, cls: 'bg-brand-success/10 text-brand-success' };
      case 'rejected':  return { Icon: ShieldAlert, cls: 'bg-brand-error/10 text-brand-error' };
      default:          return { Icon: Clock3,      cls: 'bg-warning/10 text-warning' };
    }
  }
  // event kind
  switch (it.action) {
    case 'create': return { Icon: Plus,        cls: 'bg-brand-success/10 text-brand-success' };
    case 'update': return { Icon: Pencil,      cls: 'bg-accent/10 text-accent' };
    case 'submit': return { Icon: Send,        cls: 'bg-brand-success/10 text-brand-success' };
    case 'cancel': return { Icon: Ban,         cls: 'bg-brand-error/10 text-brand-error' };
    case 'amend':  return { Icon: Pencil,      cls: 'bg-accent/10 text-accent' };
    case 'delete': return { Icon: XCircle,     cls: 'bg-brand-error/10 text-brand-error' };
    default:       return { Icon: CheckCircle2, cls: 'bg-stone/10 text-stone' };
  }
}

function describe(it: TimelineEntry): string {
  if (it.kind === 'view') return 'viewed this';
  if (it.kind === 'comment') return 'commented';
  if (it.kind === 'approval') {
    const rule = it.rule_name ? ` (${it.rule_name})` : '';
    switch (it.action) {
      case 'requested': return `requested approval${rule}`;
      case 'approved':  return `approved${rule}`;
      case 'rejected':  return `rejected${rule}`;
      default:          return it.action ?? '';
    }
  }
  switch (it.action) {
    case 'create': return 'created this';
    case 'update': return 'modified fields';
    case 'submit': return 'submitted';
    case 'cancel': return 'cancelled';
    case 'amend':  return 'amended';
    case 'delete': return 'deleted';
    default:       return it.action ?? '';
  }
}

function ApprovalPill({ action }: { action: string }) {
  if (action === 'approved') return <StatusPill tone="success" withDot={false}>Approved</StatusPill>;
  if (action === 'rejected') return <StatusPill tone="danger" withDot={false}>Rejected</StatusPill>;
  return null;
}

/* ---------- raw-details modal (system users only) ---------- */

function DetailsModal({
  entry, doctype, documentId, onClose,
}: {
  entry: TimelineEntry | null;
  doctype: string;
  documentId: string;
  onClose: () => void;
}) {
  if (!entry) return null;
  return (
    <Dialog open={!!entry} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>Event details</DialogTitle>
        <div className="mt-3 space-y-3 text-body-sm">
          <Row label="Kind" value={entry.kind} mono />
          {entry.action  && <Row label="Action"      value={entry.action} mono />}
          <Row label="Doctype"     value={doctype} mono />
          <Row label="Document ID" value={documentId} mono />
          <Row label="Event ID"    value={entry.id} mono />
          <Row label="Occurred at" value={new Date(entry.occurred_at).toISOString()} mono />
          <Row label="User"        value={`${entry.user_name || entry.user_email || '—'} (${entry.user_id})`} />
          {entry.rule_name && <Row label="Approval rule" value={entry.rule_name} />}
          {entry.body && (
            <div>
              <div className="text-caption text-stone mb-1">{entry.kind === 'approval' ? 'Decision note' : 'Body'}</div>
              <pre className="text-caption text-charcoal bg-surface-soft rounded-md px-2 py-2 whitespace-pre-wrap break-words">{entry.body}</pre>
            </div>
          )}
          {entry.diff !== undefined && entry.diff !== null && (
            <div>
              <div className="text-caption text-stone mb-1">Audit diff (before/after)</div>
              <pre className="text-caption text-charcoal bg-surface-soft rounded-md px-2 py-2 overflow-auto max-h-[400px]">
{JSON.stringify(entry.diff, null, 2)}
              </pre>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline gap-3">
      <span className="text-stone shrink-0 min-w-[110px]">{label}</span>
      <span className={cn('text-ink break-all', mono && 'font-mono text-caption')}>{value}</span>
    </div>
  );
}

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return '';
  const diffMs = Date.now() - t;
  const sec = Math.round(diffMs / 1000);
  if (sec < 60) return 'just now';
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.round(hr / 24);
  if (d < 30) return `${d}d ago`;
  const mo = Math.round(d / 30);
  if (mo < 12) return `${mo}mo ago`;
  return new Date(iso).toISOString().slice(0, 10);
}
