import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Bell, Trash2, AlertCircle, Edit } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface EventDef { key: string; label: string }
interface EventList { items: EventDef[] }
interface Rule {
  id: string; name: string; event_key: string; company_id?: string;
  is_active: boolean; recipients: string[]; channels: string[];
  condition_field?: string; condition_op?: string; condition_value?: string;
  description?: string; updated_at: string;
}
interface RuleList { items: Rule[] }
interface Role { id: string; name: string }
interface RoleList { items: Role[] }
interface User { id: string; email: string; full_name?: string }
interface UserList { items: User[] }

const CHANNELS = ['in_app', 'email', 'whatsapp'] as const;
const OPS = ['', '=', '<>', '>', '>=', '<', '<='] as const;

export function NotificationRulesSection() {
  const qc = useQueryClient();
  const { data: events } = useQuery({ queryKey: ['notif-events'], queryFn: () => api<EventList>('/admin/notification-rules/events') });
  const { data: rules, isLoading } = useQuery({ queryKey: ['notif-rules'], queryFn: () => api<RuleList>('/admin/notification-rules') });
  const { data: roles } = useQuery({ queryKey: ['roles'], queryFn: () => api<RoleList>('/admin/roles') });
  const { data: users } = useQuery({ queryKey: ['users'], queryFn: () => api<UserList>('/admin/users') });

  const [editing, setEditing] = useState<Rule | 'new' | null>(null);

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Notification rules</CardTitle>
          <CardDescription>
            "When event X fires, notify recipients Y via channels Z." Engine wiring (sending the actual emails / WhatsApp) is a downstream piece — rules stored today drive nothing yet.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setEditing('new')}>
          <Plus className="size-3.5" /> New rule
        </Button>
      </div>

      {isLoading ? <Card><Skeleton className="h-32 w-full" /></Card> :
       (rules?.items ?? []).length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Bell className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No rules yet.</div>
          </div>
        </Card>
       ) : (
        <div className="space-y-2">
          {rules!.items.map((r) => (
            <RuleRow key={r.id} rule={r}
              roleName={(id: string) => roles?.items.find((x) => x.id === id)?.name ?? id}
              userEmail={(id: string) => users?.items.find((x) => x.id === id)?.email ?? id}
              onEdit={() => setEditing(r)}
              onChanged={() => qc.invalidateQueries({ queryKey: ['notif-rules'] })}
            />
          ))}
        </div>
       )}

      {editing && (
        <RuleDialog
          mode={editing === 'new' ? 'new' : 'edit'}
          rule={editing === 'new' ? null : editing}
          events={events?.items ?? []}
          roles={roles?.items ?? []}
          users={users?.items ?? []}
          onClose={() => setEditing(null)}
          onSaved={() => { qc.invalidateQueries({ queryKey: ['notif-rules'] }); setEditing(null); }}
        />
      )}
    </div>
  );
}

function RuleRow({
  rule, roleName, userEmail, onEdit, onChanged,
}: { rule: Rule; roleName: (id: string) => string; userEmail: (id: string) => string; onEdit: () => void; onChanged: () => void }) {
  const del = useMutation({
    mutationFn: () => api(`/admin/notification-rules/${rule.id}`, { method: 'DELETE' }),
    onSuccess: () => onChanged(),
  });
  return (
    <div className="bg-canvas border border-hairline rounded-lg p-3.5 flex items-center gap-3">
      <span className="size-9 rounded-md bg-surface text-ink inline-flex items-center justify-center shrink-0">
        <Bell className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-body-sm font-medium text-ink">{rule.name}</span>
          <span className="text-caption font-mono text-stone">{rule.event_key}</span>
          {!rule.is_active && <StatusPill tone="warning" withDot={false}>Inactive</StatusPill>}
        </div>
        <div className="text-caption text-stone">
          {rule.condition_field
            ? <>When <span className="font-mono text-ink">{rule.condition_field} {rule.condition_op} {rule.condition_value}</span> → </>
            : 'Always → '}
          {rule.recipients.map((r) => {
            const [t, id] = r.split(':');
            return t === 'role' ? `role:${roleName(id ?? '')}` : `user:${userEmail(id ?? '')}`;
          }).join(', ')} via {rule.channels.join(', ')}
        </div>
      </div>
      <Button size="sm" variant="ghost" onClick={onEdit}><Edit className="size-3.5" /></Button>
      <Button size="sm" variant="ghost"
        onClick={() => { if (confirm(`Delete "${rule.name}"?`)) del.mutate(); }}>
        <Trash2 className="size-3.5" />
      </Button>
    </div>
  );
}

function RuleDialog({
  mode, rule, events, roles, users, onClose, onSaved,
}: {
  mode: 'new'|'edit'; rule: Rule | null;
  events: EventDef[]; roles: Role[]; users: User[];
  onClose: () => void; onSaved: () => void;
}) {
  const [name, setName]         = useState(rule?.name ?? '');
  const [eventKey, setEventKey] = useState(rule?.event_key ?? events[0]?.key ?? '');
  const [isActive, setIsActive] = useState(rule?.is_active ?? true);
  const [recipients, setRec]    = useState<Set<string>>(new Set(rule?.recipients ?? []));
  const [channels, setCh]       = useState<Set<string>>(new Set(rule?.channels ?? ['in_app']));
  const [cf, setCF]             = useState(rule?.condition_field ?? '');
  const [co, setCO]             = useState(rule?.condition_op ?? '');
  const [cv, setCV]             = useState(rule?.condition_value ?? '');
  const [error, setError]       = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = {
        name, event_key: eventKey, is_active: isActive,
        recipients: Array.from(recipients), channels: Array.from(channels),
      };
      if (cf) { body.condition_field = cf; body.condition_op = co; body.condition_value = cv; }
      return mode === 'new'
        ? api('/admin/notification-rules',                { method: 'POST', body })
        : api(`/admin/notification-rules/${rule!.id}`,    { method: 'PUT',  body });
    },
    onSuccess: () => onSaved(),
    onError:   (e: Error) => setError(e.message),
  });

  function toggleSet<T>(s: Set<T>, x: T, set: (n: Set<T>) => void) {
    const n = new Set(s);
    n.has(x) ? n.delete(x) : n.add(x);
    set(n);
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>{mode === 'new' ? 'New notification rule' : `Edit ${rule?.name}`}</DialogTitle>
        <form onSubmit={(e) => {
            e.preventDefault(); setError(null);
            if (!name.trim() || !eventKey) return setError('Name + event required');
            if (recipients.size === 0) return setError('Pick at least one recipient');
            save.mutate();
          }}
          className="mt-4 space-y-3">
          <div className="grid sm:grid-cols-2 gap-3">
            <Field label="Name"><Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus /></Field>
            <Field label="Event">
              <select className="input-base" value={eventKey} onChange={(e) => setEventKey(e.target.value)}>
                {events.map((ev) => <option key={ev.key} value={ev.key}>{ev.label}</option>)}
              </select>
            </Field>
          </div>
          <div>
            <div className="label-base mb-1.5">Channels</div>
            <div className="flex gap-3 flex-wrap">
              {CHANNELS.map((c) => (
                <label key={c} className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
                  <input type="checkbox" className="accent-brand-green-deep" checked={channels.has(c)}
                    onChange={() => toggleSet(channels, c, setCh)} />
                  {c}
                </label>
              ))}
            </div>
          </div>
          <div>
            <div className="label-base mb-1.5">Recipients (roles)</div>
            <div className="grid grid-cols-2 gap-1.5">
              {roles.map((r) => (
                <label key={r.id} className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
                  <input type="checkbox" className="accent-brand-green-deep"
                    checked={recipients.has(`role:${r.id}`)}
                    onChange={() => toggleSet(recipients, `role:${r.id}`, setRec)} />
                  {r.name}
                </label>
              ))}
            </div>
          </div>
          <div>
            <div className="label-base mb-1.5">Recipients (specific users)</div>
            <div className="grid grid-cols-2 gap-1.5 max-h-32 overflow-y-auto">
              {users.map((u) => (
                <label key={u.id} className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
                  <input type="checkbox" className="accent-brand-green-deep"
                    checked={recipients.has(`user:${u.id}`)}
                    onChange={() => toggleSet(recipients, `user:${u.id}`, setRec)} />
                  {u.email}
                </label>
              ))}
            </div>
          </div>
          <div>
            <div className="label-base mb-1.5">Condition (optional)</div>
            <div className="grid grid-cols-[1fr_80px_1fr] gap-2">
              <Input value={cf} onChange={(e) => setCF(e.target.value)} placeholder="grand_total" className="font-mono" />
              <select className="input-base" value={co} onChange={(e) => setCO(e.target.value)}>
                {OPS.map((o) => <option key={o} value={o}>{o || '—'}</option>)}
              </select>
              <Input value={cv} onChange={(e) => setCV(e.target.value)} placeholder="50000000" className="num text-right" disabled={!cf} />
            </div>
          </div>
          <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
            <input type="checkbox" className="accent-brand-green-deep" checked={isActive} onChange={(e) => setIsActive(e.target.checked)} />
            Active
          </label>
          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={save.isPending}>{mode === 'new' ? 'Create' : 'Save'}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
