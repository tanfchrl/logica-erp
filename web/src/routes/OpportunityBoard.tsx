import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Plus, BarChart3, Table as TableIcon, Calendar, User, AlertCircle } from 'lucide-react';
import { motion } from 'framer-motion';
import { PageHeader } from '@/components/PageHeader';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogTitle, DialogDescription } from '@/components/Dialog';
import { Field, Input } from '@/components/Input';
import { Kanban, type KanbanColumn } from '@/components/Kanban';
import { api } from '@/lib/api';
import { money, date } from '@/lib/format';
import { toast } from '@/components/Toaster';

interface Opportunity {
  id: string;
  name: string;
  subject: string;
  party_id: string;
  party_name?: string;
  stage: string;
  amount: string;
  currency: string;
  expected_close_date?: string;
  owner_user_id?: string;
  opportunity_from: 'lead' | 'customer';
}
interface OpportunityListResp {
  items: Opportunity[];
  stage_order: string[];
}

// Mirror the backend's defaultProbability — used for the column tone.
const STAGE_LABELS: Record<string, string> = {
  prospecting:   'Prospecting',
  qualification: 'Qualification',
  proposal:      'Proposal',
  negotiation:   'Negotiation',
  closed_won:    'Closed Won',
  closed_lost:   'Closed Lost',
};

const STAGE_TONES: Record<string, KanbanColumn<Opportunity>['tone']> = {
  prospecting:   'neutral',
  qualification: 'info',
  proposal:      'info',
  negotiation:   'warning',
  closed_won:    'success',
  closed_lost:   'danger',
};

export function OpportunityBoard() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['opportunities-board'],
    queryFn:  () => api<OpportunityListResp>('/crm/opportunities'),
  });

  // Drop-into-Lost flow: we need a reason before the server accepts the
  // move. Stash the dropped card and open the modal.
  const [pendingLost, setPendingLost] = useState<{ id: string; subject: string } | null>(null);

  // Group opportunities by stage in the order the backend tells us about,
  // so we never get out of sync if a stage is added.
  const columns: KanbanColumn<Opportunity>[] = useMemo(() => {
    const items = data?.items ?? [];
    const stageOrder = data?.stage_order ?? Object.keys(STAGE_LABELS);
    return stageOrder.map((stageID) => {
      const stageItems = items.filter((o) => o.stage === stageID);
      const totalAmt = stageItems.reduce((s, o) => s + Number(o.amount || 0), 0);
      return {
        id: stageID,
        label: STAGE_LABELS[stageID] ?? stageID,
        tone: STAGE_TONES[stageID],
        items: stageItems,
        // Money roll-up per column — the column header reads
        // "12 · Rp 450.000.000" for a quick pipeline-value glance.
        totalLabel: totalAmt > 0 ? money(String(totalAmt)) : undefined,
      };
    });
  }, [data]);

  // Optimistic + retry-friendly move. PATCH-style endpoint returns the
  // updated opportunity; on success we invalidate the list so any
  // amount/probability mutations the server applied (e.g. closed_won →
  // probability 100) show up.
  const moveMutation = useMutation({
    mutationFn: ({ id, stage }: { id: string; stage: string }) =>
      api<Opportunity>(`/crm/opportunities/${id}/set-stage`, {
        method: 'POST',
        body: { stage },
      }),
    onMutate: async ({ id, stage }) => {
      await qc.cancelQueries({ queryKey: ['opportunities-board'] });
      const prev = qc.getQueryData<OpportunityListResp>(['opportunities-board']);
      qc.setQueryData<OpportunityListResp>(['opportunities-board'], (old) => {
        if (!old) return old;
        return {
          ...old,
          items: old.items.map((o) => o.id === id ? { ...o, stage } : o),
        };
      });
      return { prev };
    },
    onError: (e: Error, _vars, ctx) => {
      // Roll back to pre-mutation snapshot on failure.
      if (ctx?.prev) qc.setQueryData(['opportunities-board'], ctx.prev);
      toast.error('Could not move card', e.message);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ['opportunities-board'] }),
  });

  function onMoveCard(cardId: string, _from: string, to: string) {
    if (to === 'closed_lost') {
      // Pop the dialog instead of POSTing — server needs a lost_reason.
      const card = (data?.items ?? []).find((o) => o.id === cardId);
      setPendingLost({ id: cardId, subject: card?.subject ?? cardId });
      return;
    }
    moveMutation.mutate({ id: cardId, stage: to });
  }

  // All columns accept drops now; closed_lost is handled by the dialog.
  function canDropInto() { return true; }

  const lostMutation = useMutation({
    mutationFn: ({ id, lostReason }: { id: string; lostReason: string }) =>
      api<Opportunity>(`/crm/opportunities/${id}/mark-lost`, {
        method: 'POST',
        body: { lost_reason: lostReason },
      }),
    onSuccess: () => {
      setPendingLost(null);
      void qc.invalidateQueries({ queryKey: ['opportunities-board'] });
    },
    onError: (e: Error) => toast.error('Could not mark lost', e.message),
  });

  return (
    <>
      <PageHeader
        crumbs={[
          { label: 'CRM', to: '/crm' },
          { label: 'Opportunities', to: '/crm/opportunities' },
          { label: 'Board' },
        ]}
        title={<span className="flex items-center gap-2"><BarChart3 className="size-5 text-accent" /> Pipeline</span>}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={'/crm/opportunities' as never}><TableIcon className="size-4" /> Table</Link>
            </Button>
            <Button asChild>
              <Link to={'/crm/opportunities/new' as never}><Plus className="size-4" /> New deal</Link>
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
            renderCard={(o) => <OpportunityCard o={o} />}
            onMoveCard={onMoveCard}
            canDropInto={canDropInto}
          />
        )}
      </motion.div>

      {pendingLost && (
        <LostReasonDialog
          subject={pendingLost.subject}
          onCancel={() => setPendingLost(null)}
          onConfirm={(reason) => lostMutation.mutate({ id: pendingLost.id, lostReason: reason })}
          loading={lostMutation.isPending}
        />
      )}
    </>
  );
}

function LostReasonDialog({
  subject, onCancel, onConfirm, loading,
}: { subject: string; onCancel: () => void; onConfirm: (reason: string) => void; loading: boolean }) {
  const [reason, setReason] = useState('');
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onCancel(); }}>
      <DialogContent>
        <DialogTitle>Mark as Closed Lost</DialogTitle>
        <DialogDescription>
          Tell us why <span className="text-ink font-medium">{subject}</span> didn't close.
          The reason shows up in reports so the team can spot patterns.
        </DialogDescription>
        <Field label="Lost reason" hint="Examples: price, no decision, picked competitor, no budget.">
          <Input value={reason} onChange={(e) => setReason(e.target.value)} autoFocus
            placeholder="Why was this lost?" />
        </Field>
        {!reason.trim() && (
          <div className="text-caption text-stone inline-flex items-start gap-1.5 mt-2">
            <AlertCircle className="size-3.5 mt-0.5" /> A reason is required.
          </div>
        )}
        <div className="flex justify-end gap-2 pt-3 border-t border-hairline mt-3">
          <Button variant="ghost" onClick={onCancel}>Cancel</Button>
          <Button variant="danger" onClick={() => onConfirm(reason.trim())}
            disabled={!reason.trim()} loading={loading}>
            Mark Lost
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function OpportunityCard({ o }: { o: Opportunity }) {
  return (
    <Link to={`/crm/opportunities/${o.id}` as never} className="block p-3 hover:bg-surface-soft transition-colors">
      <div className="text-body-sm font-medium text-ink truncate">{o.subject}</div>
      {o.party_name && (
        <div className="text-caption text-stone truncate">{o.party_name}</div>
      )}
      <div className="mt-2 flex items-baseline justify-between gap-2">
        <div className="num text-body-sm text-ink">{money(o.amount)}</div>
        {o.expected_close_date && (
          <div className="text-caption text-stone flex items-center gap-1">
            <Calendar className="size-3" /> {date(o.expected_close_date)}
          </div>
        )}
      </div>
      {o.owner_user_id && (
        <div className="mt-1 text-caption text-stone flex items-center gap-1 truncate">
          <User className="size-3" /> {o.owner_user_id.slice(-6)}
        </div>
      )}
    </Link>
  );
}
