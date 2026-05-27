import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Sparkles, Check, X } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { getAccessToken, getActiveCompany } from '@/lib/api';

interface DraftEntry {
  id: string;
  doctype: string;
  document_id: string;
  document_name: string;
  prompt: string;
  created_at: string;
}
interface ListResp { items: DraftEntry[] }

// Inverse of the audit middleware's slug→doctype map. Keep in sync with
// internal/platform/audit/middleware.go.
const DOCTYPE_TO_URL: Record<string, { module: string; slug: string }> = {
  customer:         { module: '/accounting', slug: 'customers' },
  supplier:         { module: '/accounting', slug: 'suppliers' },
  item:             { module: '/accounting', slug: 'items' },
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

async function agentFetch<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const co = getActiveCompany();
  if (co) headers['X-Company-Id'] = co;
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  const r = await fetch(path, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return t ? (JSON.parse(t) as T) : ({} as T);
}

/**
 * AgentDraftsCard — drops onto the Dashboard. Lists Tier-1 drafts the
 * Copilot has produced and that need a human to open + submit. Approve
 * navigates to the detail view (where the user submits); Reject just
 * dismisses the queue entry.
 */
export function AgentDraftsCard() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['agent-approvals-pending'],
    queryFn:  () => agentFetch<ListResp>('/api/agent/v1/approvals/pending'),
    refetchInterval: 60_000,
  });
  const resolve = useMutation({
    mutationFn: ({ id, status }: { id: string; status: 'approved' | 'rejected' }) =>
      agentFetch(`/api/agent/v1/approvals/${id}/resolve`, { method: 'POST', body: { status } }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['agent-approvals-pending'] }); },
  });

  const items = data?.items ?? [];
  if (isLoading || items.length === 0) {
    // Hide the card entirely when empty — keeps the dashboard tidy.
    return null;
  }

  function openAndApprove(e: DraftEntry) {
    resolve.mutate({ id: e.id, status: 'approved' });
    const map = DOCTYPE_TO_URL[e.doctype];
    if (map) void navigate({ to: `${map.module}/${map.slug}/${e.document_id}` as never });
  }

  return (
    <Card>
      <div className="flex items-center justify-between">
        <div>
          <CardTitle>
            <span className="inline-flex items-center gap-1.5">
              <Sparkles className="size-4 text-accent" /> AI Drafts menunggu review
            </span>
          </CardTitle>
          <CardDescription>
            Draft yang dibuat oleh Copilot. Klik untuk buka, periksa, lalu submit manual.
          </CardDescription>
        </div>
        <span className="text-caption font-semibold text-accent">{items.length}</span>
      </div>
      <ul className="mt-3 divide-y divide-hairline">
        {items.map((e) => (
          <li key={e.id} className="py-2.5 flex items-start gap-3">
            <div className="min-w-0 flex-1">
              <div className="text-body-sm text-ink font-medium truncate">
                {e.document_name} · <span className="text-stone font-normal">{e.doctype}</span>
              </div>
              {e.prompt && (
                <div className="text-caption text-stone line-clamp-1 mt-0.5">
                  "{e.prompt}"
                </div>
              )}
            </div>
            <div className="flex items-center gap-1.5 shrink-0">
              <Button
                size="sm"
                variant="secondary"
                onClick={() => resolve.mutate({ id: e.id, status: 'rejected' })}
                disabled={resolve.isPending}
                aria-label="Dismiss"
                title="Dismiss"
              >
                <X className="size-3.5" />
              </Button>
              <Button
                size="sm"
                onClick={() => openAndApprove(e)}
                disabled={resolve.isPending}
              >
                <Check className="size-3.5" /> Review &amp; submit
              </Button>
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}
