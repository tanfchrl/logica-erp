import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus, Mail, Phone, MessageSquare, Users, MessagesSquare,
  ArrowDownLeft, ArrowUpRight, Trash2, AlertCircle, Activity,
} from 'lucide-react';
import { Card, CardTitle, CardDescription } from './Card';
import { Button } from './Button';
import { Field, Input } from './Input';
import { Skeleton } from './EmptyState';
import { api } from '@/lib/api';

/**
 * ActivityPanel — manually logged calls / meetings / emails / WA / SMS.
 * Reads /crm/communications?parent_doctype=X&parent_id=Y. v1 is
 * user-entered only; SMTP / WA Business API auto-population lands later.
 */

type Kind = 'email' | 'sms' | 'phone' | 'meeting' | 'whatsapp';
type Direction = 'in' | 'out';

interface Communication {
  id: string;
  kind: Kind;
  direction: Direction;
  subject: string;
  body?: string;
  with_contact?: string;
  sent_at: string;
  source: string;
}

interface ActivityPanelProps {
  parentDoctype: string;
  parentID: string;
}

const KIND_ICON: Record<Kind, React.ComponentType<{ className?: string }>> = {
  email:    Mail,
  sms:      MessageSquare,
  phone:    Phone,
  meeting:  Users,
  whatsapp: MessagesSquare,
};

const KIND_LABEL: Record<Kind, string> = {
  email:    'Email',
  sms:      'SMS',
  phone:    'Phone',
  meeting:  'Meeting',
  whatsapp: 'WhatsApp',
};

export function ActivityPanel({ parentDoctype, parentID }: ActivityPanelProps) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['communications', parentDoctype, parentID],
    queryFn:  () => api<{ items: Communication[] }>(
      `/crm/communications?parent_doctype=${parentDoctype}&parent_id=${parentID}`,
    ),
    enabled: !!parentID,
  });
  const items = data?.items ?? [];

  const [adding, setAdding] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const del = useMutation({
    mutationFn: (id: string) => api(`/crm/communications/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['communications', parentDoctype, parentID] }),
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <Card padded={false}>
      <div className="px-5 py-3 border-b border-hairline flex items-baseline justify-between">
        <div>
          <CardTitle>
            <Activity className="size-4 inline mr-1.5 text-accent" /> Activity
          </CardTitle>
          <CardDescription>Calls, meetings, emails — logged manually for now.</CardDescription>
        </div>
        {!adding && (
          <Button size="sm" variant="secondary" onClick={() => setAdding(true)}>
            <Plus className="size-3.5" /> Log
          </Button>
        )}
      </div>

      {err && (
        <div className="px-5 py-2 text-caption text-brand-error inline-flex items-start gap-1.5">
          <AlertCircle className="size-3.5 mt-0.5" /> {err}
        </div>
      )}

      {adding && (
        <AddActivity
          parentDoctype={parentDoctype}
          parentID={parentID}
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            void qc.invalidateQueries({ queryKey: ['communications', parentDoctype, parentID] });
          }}
        />
      )}

      {isLoading ? (
        <div className="p-5"><Skeleton className="h-20 w-full" /></div>
      ) : items.length === 0 && !adding ? (
        <div className="px-5 py-6 text-center text-caption text-stone">No activity logged yet.</div>
      ) : (
        <ul className="divide-y divide-hairline">
          {items.map((c) => {
            const Icon = KIND_ICON[c.kind];
            const Dir  = c.direction === 'in' ? ArrowDownLeft : ArrowUpRight;
            return (
              <li key={c.id} className="px-5 py-3 flex items-start gap-3">
                <div className="size-7 rounded-full bg-accent-soft text-accent inline-flex items-center justify-center shrink-0 mt-0.5">
                  <Icon className="size-3.5" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline gap-2 flex-wrap">
                    <Dir className="size-3 text-stone" />
                    <span className="text-caption text-stone">{KIND_LABEL[c.kind]}</span>
                    {c.with_contact && <span className="text-caption text-stone">· {c.with_contact}</span>}
                    <span className="text-caption text-text-tertiary ml-auto num">
                      {new Date(c.sent_at).toLocaleString('id-ID')}
                    </span>
                  </div>
                  <div className="text-body-sm text-ink mt-0.5">{c.subject}</div>
                  {c.body && (
                    <div className="text-caption text-charcoal whitespace-pre-wrap mt-1">{c.body}</div>
                  )}
                </div>
                <button type="button"
                  onClick={() => { if (confirm('Delete this activity?')) del.mutate(c.id); }}
                  className="text-stone hover:text-brand-error" aria-label="Delete">
                  <Trash2 className="size-3.5" />
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </Card>
  );
}

function AddActivity({
  parentDoctype, parentID, onClose, onAdded,
}: { parentDoctype: string; parentID: string; onClose: () => void; onAdded: () => void }) {
  const [kind, setKind]           = useState<Kind>('phone');
  const [direction, setDirection] = useState<Direction>('out');
  const [subject, setSubject]     = useState('');
  const [body, setBody]           = useState('');
  const [withContact, setWith]    = useState('');
  const [err, setErr] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api('/crm/communications', {
      method: 'POST',
      body: {
        parent_doctype: parentDoctype,
        parent_id:      parentID,
        kind, direction, subject,
        body:         body || undefined,
        with_contact: withContact || undefined,
      },
    }),
    onSuccess: () => onAdded(),
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <div className="px-5 py-4 border-t border-hairline bg-surface-soft space-y-3">
      <div className="grid grid-cols-2 gap-2">
        <Field label="Kind">
          <select className="input-base" value={kind} onChange={(e) => setKind(e.target.value as Kind)}>
            <option value="phone">Phone call</option>
            <option value="meeting">Meeting</option>
            <option value="email">Email</option>
            <option value="whatsapp">WhatsApp</option>
            <option value="sms">SMS</option>
          </select>
        </Field>
        <Field label="Direction">
          <select className="input-base" value={direction} onChange={(e) => setDirection(e.target.value as Direction)}>
            <option value="out">Outbound (we reached out)</option>
            <option value="in">Inbound (they reached us)</option>
          </select>
        </Field>
      </div>
      <Field label="Subject">
        <Input value={subject} onChange={(e) => setSubject(e.target.value)} autoFocus
          placeholder="What was it about?" />
      </Field>
      <Field label="With" hint="Person on the other side. Name / phone / email — free text.">
        <Input value={withContact} onChange={(e) => setWith(e.target.value)} />
      </Field>
      <Field label="Notes">
        <textarea className="input-base min-h-[80px] py-2" value={body}
          onChange={(e) => setBody(e.target.value)} placeholder="What was discussed / agreed?" />
      </Field>
      {err && (
        <div className="text-caption text-brand-error inline-flex items-start gap-1.5">
          <AlertCircle className="size-3.5 mt-0.5" /> {err}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onClose}>Cancel</Button>
        <Button size="sm" onClick={() => mut.mutate()} loading={mut.isPending} disabled={!subject.trim()}>
          Log
        </Button>
      </div>
    </div>
  );
}
