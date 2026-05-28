import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Check, Undo2, Trash2, AlertCircle, CalendarClock, ListTodo } from 'lucide-react';
import { Card, CardTitle, CardDescription } from './Card';
import { Button } from './Button';
import { Field, Input } from './Input';
import { Skeleton } from './EmptyState';
import { StatusPill } from './StatusPill';
import { api } from '@/lib/api';
import { date } from '@/lib/format';
import { cn } from '@/lib/cn';

/**
 * TasksPanel — todo list attached to any record. Reads
 * /projects/tasks?parent_doctype=X&parent_id=Y; backend parentAllowlist
 * gates which doctypes accept tasks.
 *
 * Default sort: due_date ascending (NULL last), then created_at desc.
 * One-click complete / reopen via dedicated endpoints.
 */

interface Task {
  id: string;
  subject: string;
  status: 'Open' | 'Working' | 'Completed' | 'Cancelled';
  priority: 'Low' | 'Medium' | 'High' | 'Urgent';
  due_date?: string;
  assigned_to?: string;
  description?: string;
}

interface TasksPanelProps {
  parentDoctype: string;
  parentID: string;
}

export function TasksPanel({ parentDoctype, parentID }: TasksPanelProps) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['tasks', parentDoctype, parentID],
    queryFn:  () => api<{ items: Task[] }>(
      `/projects/tasks?parent_doctype=${parentDoctype}&parent_id=${parentID}`,
    ),
    enabled: !!parentID,
  });
  const items = data?.items ?? [];

  const [adding, setAdding] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const complete = useMutation({
    mutationFn: (id: string) => api(`/projects/tasks/${id}/complete`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks', parentDoctype, parentID] }),
    onError:   (e: Error) => setErr(e.message),
  });
  const reopen = useMutation({
    mutationFn: (id: string) => api(`/projects/tasks/${id}/reopen`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks', parentDoctype, parentID] }),
    onError:   (e: Error) => setErr(e.message),
  });
  const del = useMutation({
    mutationFn: (id: string) => api(`/projects/tasks/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks', parentDoctype, parentID] }),
    onError:   (e: Error) => setErr(e.message),
  });

  const today = new Date().toISOString().slice(0, 10);
  const overdue = (t: Task) => t.due_date && t.due_date.slice(0, 10) < today && t.status !== 'Completed';

  return (
    <Card padded={false}>
      <div className="px-5 py-3 border-b border-hairline flex items-baseline justify-between">
        <div>
          <CardTitle>
            <ListTodo className="size-4 inline mr-1.5 text-accent" /> Tasks
          </CardTitle>
          <CardDescription>Todos attached to this {parentDoctype}.</CardDescription>
        </div>
        {!adding && (
          <Button size="sm" variant="secondary" onClick={() => setAdding(true)}>
            <Plus className="size-3.5" /> Add
          </Button>
        )}
      </div>

      {err && (
        <div className="px-5 py-2 text-caption text-brand-error inline-flex items-start gap-1.5">
          <AlertCircle className="size-3.5 mt-0.5" /> {err}
        </div>
      )}

      {adding && (
        <AddTask
          parentDoctype={parentDoctype}
          parentID={parentID}
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            void qc.invalidateQueries({ queryKey: ['tasks', parentDoctype, parentID] });
          }}
        />
      )}

      {isLoading ? (
        <div className="p-5"><Skeleton className="h-20 w-full" /></div>
      ) : items.length === 0 && !adding ? (
        <div className="px-5 py-6 text-center text-caption text-stone">No tasks yet.</div>
      ) : (
        <ul className="divide-y divide-hairline">
          {items.map((t) => {
            const done = t.status === 'Completed';
            const isOverdue = overdue(t);
            return (
              <li key={t.id} className="px-5 py-3 flex items-start gap-3">
                <button
                  type="button"
                  className={cn(
                    'size-5 rounded border mt-0.5 inline-flex items-center justify-center shrink-0',
                    done ? 'bg-brand-green-deep border-brand-green-deep text-white' : 'bg-canvas border-hairline hover:border-accent',
                  )}
                  onClick={() => done ? reopen.mutate(t.id) : complete.mutate(t.id)}
                  aria-label={done ? 'Reopen' : 'Complete'}
                >
                  {done && <Check className="size-3" />}
                </button>
                <div className="min-w-0 flex-1">
                  <div className={cn('text-body-sm', done ? 'text-stone line-through' : 'text-ink')}>
                    {t.subject}
                  </div>
                  <div className="mt-0.5 flex items-center gap-2 flex-wrap text-caption text-stone">
                    {t.priority !== 'Medium' && (
                      <StatusPill tone={t.priority === 'Urgent' ? 'danger' : t.priority === 'High' ? 'warning' : 'neutral'} withDot={false}>
                        {t.priority}
                      </StatusPill>
                    )}
                    {t.due_date && (
                      <span className={cn('inline-flex items-center gap-1', isOverdue && 'text-brand-error')}>
                        <CalendarClock className="size-3" /> {date(t.due_date)}
                      </span>
                    )}
                    {t.assigned_to && (
                      <span className="font-mono">{t.assigned_to.slice(-6)}</span>
                    )}
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  {done && (
                    <button type="button" onClick={() => reopen.mutate(t.id)}
                      className="text-stone hover:text-ink" aria-label="Reopen">
                      <Undo2 className="size-3.5" />
                    </button>
                  )}
                  <button type="button"
                    onClick={() => { if (confirm('Delete this task?')) del.mutate(t.id); }}
                    className="text-stone hover:text-brand-error" aria-label="Delete">
                    <Trash2 className="size-3.5" />
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Card>
  );
}

function AddTask({
  parentDoctype, parentID, onClose, onAdded,
}: { parentDoctype: string; parentID: string; onClose: () => void; onAdded: () => void }) {
  const [subject, setSubject]   = useState('');
  const [dueDate, setDueDate]   = useState('');
  const [priority, setPriority] = useState<'Low'|'Medium'|'High'|'Urgent'>('Medium');
  const [err, setErr] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api('/projects/tasks', {
      method: 'POST',
      body: {
        parent_doctype: parentDoctype,
        parent_id:      parentID,
        subject,
        due_date:       dueDate || undefined,
        priority,
      },
    }),
    onSuccess: () => onAdded(),
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <div className="px-5 py-4 border-t border-hairline bg-surface-soft space-y-3">
      <Field label="What needs to happen?">
        <Input value={subject} onChange={(e) => setSubject(e.target.value)} autoFocus
          placeholder="e.g. Telpon balik Bu Ratna besok jam 2" />
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Due date">
          <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
        </Field>
        <Field label="Priority">
          <select className="input-base" value={priority} onChange={(e) => setPriority(e.target.value as typeof priority)}>
            <option>Low</option><option>Medium</option><option>High</option><option>Urgent</option>
          </select>
        </Field>
      </div>
      {err && (
        <div className="text-caption text-brand-error inline-flex items-start gap-1.5">
          <AlertCircle className="size-3.5 mt-0.5" /> {err}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onClose}>Cancel</Button>
        <Button size="sm" onClick={() => mut.mutate()} loading={mut.isPending} disabled={!subject.trim()}>
          Save
        </Button>
      </div>
    </div>
  );
}
