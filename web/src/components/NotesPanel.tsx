import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2, AlertCircle, StickyNote } from 'lucide-react';
import { Card, CardTitle, CardDescription } from './Card';
import { Button } from './Button';
import { Skeleton } from './EmptyState';
import { api } from '@/lib/api';
import { date } from '@/lib/format';

/**
 * NotesPanel — drops into any record detail page. Reads
 * /crm/notes?parent_doctype=X&parent_id=Y; backend's parentAllowlist
 * (internal/crm/note) gates which doctypes accept notes.
 *
 * User-emitted free text. Author-only delete (server enforces).
 */

interface Note {
  id: string;
  body: string;
  created_at: string;
  created_by: string;
}

interface NotesPanelProps {
  parentDoctype: string;
  parentID: string;
}

export function NotesPanel({ parentDoctype, parentID }: NotesPanelProps) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['notes', parentDoctype, parentID],
    queryFn:  () => api<{ items: Note[] }>(
      `/crm/notes?parent_doctype=${parentDoctype}&parent_id=${parentID}`,
    ),
    enabled: !!parentID,
  });
  const items = data?.items ?? [];

  const [body, setBody] = useState('');
  const [err, setErr] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => api('/crm/notes', {
      method: 'POST',
      body: { parent_doctype: parentDoctype, parent_id: parentID, body },
    }),
    onSuccess: () => {
      setBody('');
      setErr(null);
      void qc.invalidateQueries({ queryKey: ['notes', parentDoctype, parentID] });
    },
    onError: (e: Error) => setErr(e.message),
  });

  const del = useMutation({
    mutationFn: (id: string) => api(`/crm/notes/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notes', parentDoctype, parentID] }),
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <Card padded={false}>
      <div className="px-5 py-3 border-b border-hairline">
        <CardTitle>
          <StickyNote className="size-4 inline mr-1.5 text-accent" /> Notes
        </CardTitle>
        <CardDescription>Free-form annotations on this record.</CardDescription>
      </div>

      <div className="px-5 py-4 border-b border-hairline space-y-2">
        <textarea
          className="input-base min-h-[80px] py-2"
          placeholder="Tulis catatan singkat — meeting summary, follow-up plan, dll."
          value={body}
          onChange={(e) => setBody(e.target.value)}
        />
        {err && (
          <div className="text-caption text-brand-error inline-flex items-start gap-1.5">
            <AlertCircle className="size-3.5 mt-0.5" /> {err}
          </div>
        )}
        <div className="flex justify-end">
          <Button size="sm" onClick={() => create.mutate()}
            loading={create.isPending} disabled={!body.trim()}>
            <Plus className="size-3.5" /> Add note
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div className="p-5"><Skeleton className="h-20 w-full" /></div>
      ) : items.length === 0 ? (
        <div className="px-5 py-6 text-center text-caption text-stone">No notes yet.</div>
      ) : (
        <ul className="divide-y divide-hairline">
          {items.map((n) => (
            <li key={n.id} className="px-5 py-3">
              <div className="flex items-baseline justify-between gap-2">
                <span className="text-caption text-stone">{date(n.created_at)}</span>
                <button
                  type="button"
                  className="text-stone hover:text-brand-error"
                  onClick={() => {
                    if (confirm('Delete this note?')) del.mutate(n.id);
                  }}
                  aria-label="Delete note"
                >
                  <Trash2 className="size-3.5" />
                </button>
              </div>
              <div className="mt-1 text-body-sm text-charcoal whitespace-pre-wrap">{n.body}</div>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
