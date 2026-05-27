import { useState } from 'react';
import * as Popover from '@radix-ui/react-popover';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Bell, Check, CheckCheck, Inbox } from 'lucide-react';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';
import { Skeleton } from '@/components/EmptyState';

interface Notification {
  id: string;
  user_id: string;
  subject: string;
  body?: string;
  link_doctype?: string;
  link_document_id?: string;
  is_read: boolean;
  read_at?: string | null;
  created_at: string;
}

interface ListResp { items: Notification[] }
interface CountResp { count: number }

/**
 * NotificationsPopover — the bell in the top-right chrome. Polls
 * /notifications/unread-count every 30s for the badge; opens a popover with
 * the last 100 notifications on click. Clicking an item marks it read and
 * (if linked) navigates to the source document.
 *
 * The doctype→URL mapping mirrors the audit middleware's slug→doctype map.
 * Unmapped doctypes still mark-as-read but don't navigate.
 */

// Doctype → URL slug. Inverse of internal/platform/audit/middleware.go.
const DOCTYPE_TO_URL: Record<string, { module: string; slug: string }> = {
  customer:         { module: '/accounting', slug: 'customers' },
  supplier:         { module: '/accounting', slug: 'suppliers' },
  item:             { module: '/accounting', slug: 'items' },
  account:          { module: '/accounting', slug: 'accounts' },
  tax_template:     { module: '/accounting', slug: 'tax-templates' },
  sales_invoice:    { module: '/accounting', slug: 'sales-invoices' },
  purchase_invoice: { module: '/accounting', slug: 'purchase-invoices' },
  journal_entry:    { module: '/accounting', slug: 'journal-entries' },
  payment_entry:    { module: '/accounting', slug: 'payment-entries' },
  employee:         { module: '/hr',         slug: 'employees' },
  warehouse:        { module: '/stock',      slug: 'warehouses' },
  lead:             { module: '/crm',        slug: 'leads' },
  project:          { module: '/projects',   slug: 'projects' },
  issue:            { module: '/support',    slug: 'issues' },
  bom:              { module: '/manufacturing', slug: 'boms' },
  work_order:       { module: '/manufacturing', slug: 'work-orders' },
  asset:            { module: '/assets',     slug: 'assets' },
};

function detailUrl(doctype?: string, docId?: string): string | null {
  if (!doctype || !docId) return null;
  const m = DOCTYPE_TO_URL[doctype];
  if (!m) return null;
  return `${m.module}/${m.slug}/${docId}`;
}

export function NotificationsPopover() {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  // Count polls in the background. List only fetches on popover open to avoid
  // pulling 100 rows every 30s.
  const { data: countData } = useQuery({
    queryKey: ['notifications', 'unread-count'],
    queryFn:  () => api<CountResp>('/notifications/unread-count'),
    refetchInterval: 30_000,
  });
  const { data: listData, isLoading } = useQuery({
    queryKey: ['notifications', 'list'],
    queryFn:  () => api<ListResp>('/notifications'),
    enabled:  open,
  });

  const markRead = useMutation({
    mutationFn: (id: string) => api(`/notifications/${id}/read`, { method: 'POST' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['notifications'] });
    },
  });
  const markAllRead = useMutation({
    mutationFn: () => api('/notifications/mark-all-read', { method: 'POST' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['notifications'] });
    },
  });

  const unread = countData?.count ?? 0;
  const items = listData?.items ?? [];

  function onItemClick(n: Notification) {
    if (!n.is_read) markRead.mutate(n.id);
    const url = detailUrl(n.link_doctype, n.link_document_id);
    if (url) {
      setOpen(false);
      void navigate({ to: url as never });
    }
  }

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button
          aria-label={`Notifications${unread > 0 ? ` (${unread} unread)` : ''}`}
          className="relative inline-flex items-center justify-center size-8 rounded-full text-steel hover:bg-surface hover:text-ink transition-colors"
        >
          <Bell className="size-4" />
          {unread > 0 && (
            <span className="absolute -top-0.5 -right-0.5 inline-flex items-center justify-center min-w-[16px] h-[16px] px-1 rounded-full bg-brand-error text-canvas text-[10px] font-semibold leading-none">
              {unread > 99 ? '99+' : unread}
            </span>
          )}
        </button>
      </Popover.Trigger>

      <Popover.Portal>
        <Popover.Content
          align="end"
          sideOffset={8}
          className="w-[380px] max-h-[70vh] rounded-lg border border-hairline bg-canvas shadow-lg flex flex-col overflow-hidden z-50"
        >
          <div className="flex items-center justify-between px-4 py-3 border-b border-hairline">
            <div className="text-body-sm font-semibold text-ink">
              Notifications {unread > 0 && <span className="text-stone font-normal">· {unread} unread</span>}
            </div>
            {unread > 0 && (
              <button
                type="button"
                onClick={() => markAllRead.mutate()}
                disabled={markAllRead.isPending}
                className="inline-flex items-center gap-1 text-caption text-stone hover:text-ink disabled:opacity-50"
              >
                <CheckCheck className="size-3.5" /> Mark all read
              </button>
            )}
          </div>

          <div className="flex-1 overflow-y-auto">
            {isLoading && (
              <div className="p-4 space-y-2">
                <Skeleton className="h-12 w-full" />
                <Skeleton className="h-12 w-full" />
                <Skeleton className="h-12 w-full" />
              </div>
            )}

            {!isLoading && items.length === 0 && (
              <div className="px-6 py-10 flex flex-col items-center text-stone">
                <Inbox className="size-6 mb-2" />
                <span className="text-body-sm">You're all caught up.</span>
              </div>
            )}

            {!isLoading && items.length > 0 && (
              <ul className="divide-y divide-hairline">
                {items.map((n) => {
                  const url = detailUrl(n.link_doctype, n.link_document_id);
                  return (
                    <li key={n.id}>
                      <button
                        type="button"
                        onClick={() => onItemClick(n)}
                        className={cn(
                          'w-full text-left px-4 py-3 hover:bg-surface transition-colors flex gap-3 items-start',
                          !n.is_read && 'bg-accent/[0.03]',
                        )}
                      >
                        <span
                          className={cn(
                            'mt-1.5 size-2 rounded-full shrink-0',
                            n.is_read ? 'bg-stone/30' : 'bg-accent',
                          )}
                          aria-label={n.is_read ? 'Read' : 'Unread'}
                        />
                        <div className="min-w-0 flex-1">
                          <div className="flex items-baseline justify-between gap-2">
                            <span className={cn(
                              'text-body-sm truncate',
                              n.is_read ? 'text-charcoal' : 'text-ink font-medium',
                            )}>
                              {n.subject}
                            </span>
                            <span className="text-caption text-stone shrink-0">{relativeTime(n.created_at)}</span>
                          </div>
                          {n.body && (
                            <p className="text-caption text-stone mt-0.5 line-clamp-2 whitespace-pre-wrap">{n.body}</p>
                          )}
                          {url && (
                            <span className="text-caption text-accent mt-0.5 inline-block">Open →</span>
                          )}
                        </div>
                        {!n.is_read && (
                          <span
                            role="button"
                            tabIndex={0}
                            onClick={(e) => { e.stopPropagation(); markRead.mutate(n.id); }}
                            onKeyDown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); markRead.mutate(n.id); } }}
                            className="shrink-0 text-stone hover:text-ink p-1 -m-1 rounded cursor-pointer"
                            aria-label="Mark read"
                            title="Mark read"
                          >
                            <Check className="size-3.5" />
                          </span>
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return '';
  const sec = Math.round((Date.now() - t) / 1000);
  if (sec < 60) return 'now';
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h`;
  const d = Math.round(hr / 24);
  if (d < 30) return `${d}d`;
  return new Date(iso).toISOString().slice(0, 10);
}
