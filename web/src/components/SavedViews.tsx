import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Save, ChevronDown, Plus, Trash2, Share2, User as UserIcon, Filter as FilterIcon } from 'lucide-react';
import { Button } from './Button';
import { Field, Input } from './Input';
import { Dialog, DialogContent, DialogTitle, DialogDescription } from './Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

/**
 * SavedViewsBar — per-doctype dropdown that picks / creates / deletes a
 * saved view. The "view" body is an opaque JSONB object the caller owns
 * the shape of (filters, sort, columns…). v1 just persists; consumers
 * apply the body to their list state on selection.
 */

export interface SavedView<TBody = unknown> {
  id: string;
  name: string;
  is_shared: boolean;
  user_id: string;
  body: TBody;
}

export interface SavedViewsBarProps<TBody> {
  doctype: string;
  currentBody: TBody;
  activeViewId: string | null;
  onSelectView: (view: SavedView<TBody> | null) => void;
  currentUserID?: string;
}

export function SavedViewsBar<TBody>({
  doctype, currentBody, activeViewId, onSelectView, currentUserID,
}: SavedViewsBarProps<TBody>) {
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: ['saved-views', doctype],
    queryFn:  () => api<{ items: SavedView<TBody>[] }>(`/platform/saved-views?doctype=${doctype}`),
  });
  const views = data?.items ?? [];
  const active = views.find((v) => v.id === activeViewId) ?? null;

  const [pickerOpen, setPickerOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);

  const create = useMutation({
    mutationFn: ({ name, isShared, body }: { name: string; isShared: boolean; body: TBody }) =>
      api<SavedView<TBody>>('/platform/saved-views', {
        method: 'POST',
        body: { doctype, name, is_shared: isShared, body },
      }),
    onSuccess: (v) => {
      setSaveOpen(false);
      void qc.invalidateQueries({ queryKey: ['saved-views', doctype] });
      onSelectView(v);
    },
  });

  const del = useMutation({
    mutationFn: (id: string) => api(`/platform/saved-views/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['saved-views', doctype] }),
  });

  return (
    <div className="relative inline-flex items-center gap-2">
      <Button variant="ghost" size="sm" onClick={() => setPickerOpen((o) => !o)}
        aria-haspopup="listbox" aria-expanded={pickerOpen}>
        <FilterIcon className="size-3.5" />
        {active ? active.name : 'Default view'}
        <ChevronDown className="size-3.5" />
      </Button>
      <Button variant="ghost" size="sm" onClick={() => setSaveOpen(true)}>
        <Save className="size-3.5" /> Save view
      </Button>

      {pickerOpen && (
        <div className="absolute top-full left-0 mt-1 w-72 bg-canvas border border-hairline rounded-md shadow-overlay z-30">
          <button type="button"
            className={cn('w-full text-left px-3 py-2 text-body-sm hover:bg-surface-soft border-b border-hairline',
              !active && 'bg-surface')}
            onClick={() => { onSelectView(null); setPickerOpen(false); }}>
            Default view
            <div className="text-caption text-stone">All records, default columns.</div>
          </button>
          {views.length === 0 && (
            <div className="px-3 py-3 text-caption text-stone text-center">
              No saved views yet. Use "Save view" to create one.
            </div>
          )}
          {views.map((v) => (
            <div key={v.id}
              className={cn('px-3 py-2 hover:bg-surface-soft border-b border-hairline last:border-0 flex items-start gap-2',
                activeViewId === v.id && 'bg-surface')}>
              <button type="button"
                className="flex-1 text-left"
                onClick={() => { onSelectView(v); setPickerOpen(false); }}>
                <div className="text-body-sm text-ink flex items-center gap-1.5">
                  {v.is_shared ? <Share2 className="size-3 text-accent" /> : <UserIcon className="size-3 text-stone" />}
                  {v.name}
                </div>
              </button>
              {(!currentUserID || v.user_id === currentUserID) && (
                <button type="button"
                  className="text-stone hover:text-brand-error mt-0.5"
                  onClick={(e) => { e.stopPropagation(); if (confirm(`Delete "${v.name}"?`)) del.mutate(v.id); }}
                  aria-label="Delete view">
                  <Trash2 className="size-3.5" />
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      {saveOpen && (
        <SaveViewDialog
          onCancel={() => setSaveOpen(false)}
          onSave={(name, isShared) => create.mutate({ name, isShared, body: currentBody })}
          loading={create.isPending}
        />
      )}

      {/* Close picker on outside click */}
      {pickerOpen && (
        <div className="fixed inset-0 z-20" onClick={() => setPickerOpen(false)} />
      )}
    </div>
  );
}

function SaveViewDialog({
  onCancel, onSave, loading,
}: { onCancel: () => void; onSave: (name: string, isShared: boolean) => void; loading: boolean }) {
  const [name, setName] = useState('');
  const [shared, setShared] = useState(false);
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onCancel(); }}>
      <DialogContent>
        <DialogTitle>Save current view</DialogTitle>
        <DialogDescription>
          Snapshots your current filters and column layout. Shared views appear for everyone in the company.
        </DialogDescription>
        <Field label="View name">
          <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus
            placeholder="e.g. My open deals, Hot prospects" />
        </Field>
        <label className="inline-flex items-center gap-2 mt-3 cursor-pointer text-body-sm text-charcoal">
          <input type="checkbox" className="accent-brand-green-deep" checked={shared}
            onChange={(e) => setShared(e.target.checked)} />
          <Share2 className="size-3.5" /> Share with my team
        </label>
        <div className="flex justify-end gap-2 pt-3 border-t border-hairline mt-3">
          <Button variant="ghost" onClick={onCancel}>Cancel</Button>
          <Button onClick={() => onSave(name.trim(), shared)} disabled={!name.trim()} loading={loading}>
            <Plus className="size-3.5" /> Save
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// Hook helper — fetches the views and exposes the active one as state.
// Caller decides what shape `TBody` is and how to apply it.
export function useSavedView<TBody>(doctype: string) {
  const [activeViewId, setActiveViewId] = useState<string | null>(null);
  const [activeBody, setActiveBody]     = useState<TBody | null>(null);

  // Reset selection when the doctype changes.
  useEffect(() => {
    setActiveViewId(null);
    setActiveBody(null);
  }, [doctype]);

  return { activeViewId, activeBody, setActiveBody,
    selectView: (v: SavedView<TBody> | null) => {
      setActiveViewId(v?.id ?? null);
      setActiveBody(v?.body ?? null);
    } };
}
