import { useQuery } from '@tanstack/react-query';
import { Plus, Pencil, CheckCircle2, XCircle, Eye, MessageSquare, RefreshCw } from 'lucide-react';
import { Card, CardTitle } from '@/components/Card';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

/**
 * Document timeline — unified feed of write events (create/update/submit/cancel),
 * read views (deduped 24h server-side), and comments. Drops into any detail
 * page's right rail.
 *
 * Powered by /platform/timeline?doctype=X&document_id=Y. Server merges from
 * doc_event (partitioned monthly) + doc_view (partitioned daily) + document_comment.
 */

interface TimelineProps {
  doctype: string;
  documentId: string;
  className?: string;
  /** Hide views to keep the list tighter (events + comments only). */
  hideViews?: boolean;
}

interface TimelineEntry {
  kind: 'event' | 'view' | 'comment';
  id: string;
  occurred_at: string;
  user_id: string;
  user_email?: string;
  user_name?: string;
  action?: string;
  diff?: unknown;
  body?: string;
}

export function Timeline({ doctype, documentId, className, hideViews }: TimelineProps) {
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['timeline', doctype, documentId],
    queryFn: () => api<{ items: TimelineEntry[] }>(
      `/platform/timeline?doctype=${encodeURIComponent(doctype)}&document_id=${encodeURIComponent(documentId)}&limit=80`,
    ),
    enabled: !!doctype && !!documentId,
  });

  const items = (data?.items ?? []).filter((it) => !(hideViews && it.kind === 'view'));

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
          {/* vertical rail */}
          <div className="absolute left-[15px] top-1 bottom-1 w-px bg-hairline" aria-hidden />
          {items.map((it, i) => (
            <li key={it.id} className={cn('relative flex gap-3', i > 0 && 'mt-3')}>
              <IconBadge kind={it.kind} action={it.action} />
              <div className="min-w-0 flex-1 pt-0.5">
                <div className="flex items-baseline gap-2 flex-wrap">
                  <span className="text-body-sm text-ink font-medium truncate">
                    {it.user_name || it.user_email || 'Someone'}
                  </span>
                  <span className="text-caption text-stone truncate">{describe(it)}</span>
                  <span className="text-caption text-stone shrink-0 ml-auto">
                    {relativeTime(it.occurred_at)}
                  </span>
                </div>
                {it.kind === 'comment' && it.body && (
                  <p className="mt-1 text-body-sm text-charcoal whitespace-pre-wrap break-words">{it.body}</p>
                )}
                {it.kind === 'event' && it.diff !== undefined && it.diff !== null && (
                  <DiffSummary diff={it.diff} />
                )}
              </div>
            </li>
          ))}
        </ol>
      )}
    </Card>
  );
}

function IconBadge({ kind, action }: { kind: TimelineEntry['kind']; action?: string }) {
  let Icon = MessageSquare;
  let cls = 'bg-stone/10 text-stone';
  if (kind === 'view') {
    Icon = Eye;
    cls = 'bg-surface text-stone';
  } else if (kind === 'comment') {
    Icon = MessageSquare;
    cls = 'bg-accent/10 text-accent';
  } else if (kind === 'event') {
    switch (action) {
      case 'create': Icon = Plus;           cls = 'bg-brand-success/10 text-brand-success'; break;
      case 'update': Icon = Pencil;          cls = 'bg-accent/10 text-accent'; break;
      case 'submit': Icon = CheckCircle2;    cls = 'bg-brand-success/10 text-brand-success'; break;
      case 'cancel': Icon = XCircle;         cls = 'bg-brand-error/10 text-brand-error'; break;
      default:       Icon = Pencil;          cls = 'bg-stone/10 text-stone'; break;
    }
  }
  return (
    <span
      className={cn('relative z-[1] size-[30px] shrink-0 rounded-full border border-canvas inline-flex items-center justify-center', cls)}
      aria-hidden
    >
      <Icon className="size-3.5" />
    </span>
  );
}

function describe(it: TimelineEntry): string {
  if (it.kind === 'view') return 'viewed this';
  if (it.kind === 'comment') return 'commented';
  switch (it.action) {
    case 'create': return 'created this';
    case 'update': return 'edited fields';
    case 'submit': return 'submitted';
    case 'cancel': return 'cancelled';
    case 'amend':  return 'amended';
    case 'delete': return 'deleted';
    default:       return it.action ?? '';
  }
}

interface DiffSummaryProps { diff: unknown }

/**
 * DiffSummary collapses an audit diff payload to a compact "n fields changed"
 * line, expandable on click. The recorder writes {before, after} but on
 * create/submit only `after` is populated.
 */
function DiffSummary({ diff }: DiffSummaryProps) {
  if (!diff || typeof diff !== 'object') return null;
  const d = diff as { before?: Record<string, unknown>; after?: Record<string, unknown> };
  const after = d.after as Record<string, unknown> | undefined;
  if (!after) return null;
  const keys = Object.keys(after).filter((k) => !k.startsWith('$') && k !== 'custom_fields');
  if (keys.length === 0) return null;
  return (
    <details className="mt-1">
      <summary className="text-caption text-stone cursor-pointer hover:text-charcoal select-none">
        {keys.length} field{keys.length === 1 ? '' : 's'}
      </summary>
      <pre className="mt-1 text-caption text-charcoal bg-surface-soft rounded-md px-2 py-1.5 overflow-auto max-h-[180px]">
        {JSON.stringify(d, null, 2)}
      </pre>
    </details>
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
