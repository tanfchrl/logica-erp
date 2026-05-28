import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Plus, ClipboardList, Table as TableIcon, Calendar } from 'lucide-react';
import { motion } from 'framer-motion';
import { PageHeader } from '@/components/PageHeader';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Skeleton } from '@/components/EmptyState';
import { Kanban, type KanbanColumn } from '@/components/Kanban';
import { api } from '@/lib/api';
import { money, date } from '@/lib/format';

interface PurchaseOrder {
  id: string;
  name: string;
  supplier_id: string;
  transaction_date: string;
  required_by_date?: string;
  grand_total: string;
  status: string;
}

// PO status enum from internal/accounting/purchaseorder. Kanban is
// view-only (no onMoveCard) because PO transitions (hold/close/stop)
// are per-action endpoints, not a generic SetStatus — drag-to-transition
// semantics don't map cleanly. Click a card to open the form and use
// the transition buttons there.
const STATUS_ORDER = [
  'Draft', 'To Receive and Bill', 'To Receive', 'To Bill',
  'Completed', 'On Hold', 'Stopped', 'Closed',
];

const STATUS_TONE: Record<string, KanbanColumn<PurchaseOrder>['tone']> = {
  Draft:                 'neutral',
  'To Receive and Bill': 'warning',
  'To Receive':          'info',
  'To Bill':             'info',
  Completed:             'success',
  'On Hold':             'warning',
  Stopped:               'danger',
  Closed:                'neutral',
};

export function PurchaseOrderBoard() {
  const { data, isLoading } = useQuery({
    queryKey: ['purchase-orders-board'],
    queryFn:  () => api<{ items: PurchaseOrder[] }>('/accounting/purchase-orders'),
  });

  const columns: KanbanColumn<PurchaseOrder>[] = useMemo(() => {
    const items = (data?.items ?? []).filter((o) => o.status !== 'Cancelled');
    return STATUS_ORDER.map((s) => {
      const inStatus = items.filter((o) => o.status === s);
      const total = inStatus.reduce((acc, o) => acc + Number(o.grand_total || 0), 0);
      return {
        id: s, label: s, tone: STATUS_TONE[s], items: inStatus,
        totalLabel: total > 0 ? money(String(total)) : undefined,
      };
    });
  }, [data]);

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Procurement', to: '/buying' },
          { label: 'Purchase Orders', to: '/accounting/purchase-orders' },
          { label: 'Board' },
        ]}
        title="Purchase Orders — Board"
        subtitle="Read-only pipeline view. Click a card to open the PO and use its transition buttons."
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/accounting/purchase-orders' as never}><TableIcon className="size-4" /> Table</Link>
            </Button>
            <Button asChild>
              <Link to={'/accounting/purchase-orders/new' as never}><Plus className="size-4" /> New PO</Link>
            </Button>
          </>
        }
      />

      <motion.div
        initial={{ opacity: 0, y: 4 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.15 }}
        className="flex-1 px-6 lg:px-8 pt-4 pb-8 min-w-0"
      >
        {isLoading ? (
          <Card><Skeleton className="h-[60vh] w-full" /></Card>
        ) : (
          <Kanban
            columns={columns}
            getCardId={(o) => o.id}
            renderCard={(o) => <POCard o={o} />}
            // Intentionally no onMoveCard — see the file header for why.
          />
        )}
      </motion.div>
    </>
  );
}

function POCard({ o }: { o: PurchaseOrder }) {
  return (
    <Link to={`/accounting/purchase-orders/${o.id}` as never} className="block p-3 hover:bg-surface-soft transition-colors">
      <div className="font-mono text-caption text-stone">{o.name}</div>
      <div className="num text-body-sm text-ink mt-1">{money(o.grand_total)}</div>
      {o.required_by_date && (
        <div className="mt-1 text-caption text-stone inline-flex items-center gap-1">
          <Calendar className="size-3" /> by {date(o.required_by_date)}
        </div>
      )}
    </Link>
  );
}

export function MaterialRequestBoard() {
  const { data, isLoading } = useQuery({
    queryKey: ['material-requests-board'],
    queryFn:  () => api<{ items: MaterialRequest[] }>('/accounting/material-requests'),
  });

  const columns: KanbanColumn<MaterialRequest>[] = useMemo(() => {
    const items = (data?.items ?? []).filter((m) => m.status !== 'Cancelled');
    return MR_STATUS_ORDER.map((s) => {
      const inStatus = items.filter((m) => m.status === s);
      return {
        id: s, label: s, tone: MR_STATUS_TONE[s], items: inStatus,
      };
    });
  }, [data]);

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'Procurement', to: '/buying' },
          { label: 'Material Requests', to: '/accounting/material-requests' },
          { label: 'Board' },
        ]}
        title="Material Requests — Board"
        subtitle="Read-only kanban view. Click a card to open the MR."
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/accounting/material-requests' as never}><TableIcon className="size-4" /> Table</Link>
            </Button>
            <Button asChild>
              <Link to={'/accounting/material-requests/new' as never}><Plus className="size-4" /> New MR</Link>
            </Button>
          </>
        }
      />
      <motion.div
        initial={{ opacity: 0, y: 4 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.15 }}
        className="flex-1 px-6 lg:px-8 pt-4 pb-8 min-w-0"
      >
        {isLoading ? (
          <Card><Skeleton className="h-[60vh] w-full" /></Card>
        ) : (
          <Kanban
            columns={columns}
            getCardId={(m) => m.id}
            renderCard={(m) => <MRCard m={m} />}
          />
        )}
      </motion.div>
    </>
  );
}

interface MaterialRequest {
  id: string;
  name: string;
  purpose: string;
  status: string;
  transaction_date: string;
  required_by_date?: string;
}

const MR_STATUS_ORDER = [
  'Draft', 'Pending', 'Partially Ordered', 'Ordered',
  'Issued', 'Transferred', 'Received', 'Stopped',
];

const MR_STATUS_TONE: Record<string, KanbanColumn<MaterialRequest>['tone']> = {
  Draft:               'neutral',
  Pending:             'warning',
  'Partially Ordered': 'info',
  Ordered:             'info',
  Issued:              'success',
  Transferred:         'success',
  Received:            'success',
  Stopped:             'danger',
};

function MRCard({ m }: { m: MaterialRequest }) {
  // Purpose chip — distinguishes purchase MRs from internal transfers at a
  // glance in the board view.
  const purposeLabel = m.purpose === 'purchase' ? 'Buy'
    : m.purpose === 'material_transfer' ? 'Move'
    : m.purpose === 'material_issue' ? 'Issue'
    : m.purpose === 'manufacture' ? 'Make' : m.purpose;
  return (
    <Link to={`/accounting/material-requests/${m.id}` as never} className="block p-3 hover:bg-surface-soft transition-colors">
      <div className="font-mono text-caption text-stone">{m.name}</div>
      <div className="text-body-sm text-ink mt-1">
        <ClipboardList className="size-3 inline mr-1 text-accent" />
        {purposeLabel}
      </div>
      {m.required_by_date && (
        <div className="mt-1 text-caption text-stone inline-flex items-center gap-1">
          <Calendar className="size-3" /> by {date(m.required_by_date)}
        </div>
      )}
    </Link>
  );
}
