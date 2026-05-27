import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus, ShieldCheck, Trash2, Pencil, X, Save, Check, AlertCircle, Filter,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface Role { id: string; name: string; description?: string; is_system: boolean; user_count: number; created_at: string }
interface RoleList { items: Role[] }

interface PermissionRow {
  doctype:    string;
  can_read:   boolean;
  can_write:  boolean;
  can_create: boolean;
  can_delete: boolean;
  can_submit: boolean;
  can_cancel: boolean;
  can_amend:  boolean;
  can_print:  boolean;
  can_export: boolean;
}
interface PermList { items: PermissionRow[] }
interface DTList { items: string[] }

const ACTIONS: { key: keyof Omit<PermissionRow, 'doctype'>; label: string; abbr: string }[] = [
  { key: 'can_read',   label: 'Read',   abbr: 'R' },
  { key: 'can_write',  label: 'Write',  abbr: 'W' },
  { key: 'can_create', label: 'Create', abbr: 'C' },
  { key: 'can_delete', label: 'Delete', abbr: 'D' },
  { key: 'can_submit', label: 'Submit', abbr: 'S' },
  { key: 'can_cancel', label: 'Cancel', abbr: 'X' },
  { key: 'can_amend',  label: 'Amend',  abbr: 'A' },
  { key: 'can_print',  label: 'Print',  abbr: 'P' },
  { key: 'can_export', label: 'Export', abbr: 'E' },
];

export function RolesSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['roles'], queryFn: () => api<RoleList>('/admin/roles') });
  const roles = data?.items ?? [];

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editing,    setEditing]    = useState<Role | null>(null);

  const selected = selectedId
    ? roles.find((r) => r.id === selectedId) ?? null
    : roles[0] ?? null;

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Roles & permissions</CardTitle>
          <CardDescription>
            A role bundles per-doctype permissions. Stack roles on a user; the user's effective access is the union.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New role
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-40 w-full" /></Card>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-[260px_1fr] gap-4">
          <RolesList roles={roles}
            selectedId={selected?.id ?? null}
            onSelect={setSelectedId}
            onEdit={setEditing}
            onChanged={() => qc.invalidateQueries({ queryKey: ['roles'] })} />
          {selected && <PermissionMatrix role={selected} />}
        </div>
      )}

      {createOpen && (
        <RoleDialog
          mode="create"
          onClose={() => setCreateOpen(false)}
          onSaved={(r) => { void qc.invalidateQueries({ queryKey: ['roles'] }); setSelectedId(r.id); setCreateOpen(false); }}
        />
      )}
      {editing && (
        <RoleDialog
          mode="edit" role={editing}
          onClose={() => setEditing(null)}
          onSaved={() => { void qc.invalidateQueries({ queryKey: ['roles'] }); setEditing(null); }}
        />
      )}
    </div>
  );
}

/* ----------------- list ----------------- */

function RolesList({
  roles, selectedId, onSelect, onEdit, onChanged,
}: {
  roles: Role[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onEdit: (role: Role) => void;
  onChanged: () => void;
}) {
  const del = useMutation({
    mutationFn: (id: string) => api<void>(`/admin/roles/${id}`, { method: 'DELETE' }),
    onSuccess: () => onChanged(),
  });

  return (
    <Card padded={false}>
      <ul className="divide-y divide-hairline">
        {roles.map((r) => (
          <li key={r.id}>
            <button
              type="button"
              onClick={() => onSelect(r.id)}
              className={cn(
                'w-full text-left px-3 py-2.5 flex items-center gap-3 transition-colors',
                r.id === selectedId ? 'bg-surface' : 'hover:bg-surface-soft',
              )}
            >
              <ShieldCheck className={cn('size-4 shrink-0', r.is_system ? 'text-stone' : 'text-ink')} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-body-sm font-medium text-ink truncate">{r.name}</span>
                  {r.is_system && <StatusPill tone="neutral" withDot={false}>System</StatusPill>}
                </div>
                <div className="text-caption text-stone truncate">{r.user_count} {r.user_count === 1 ? 'user' : 'users'}</div>
              </div>
              {!r.is_system && (
                <span className="flex items-center gap-0.5 opacity-60 hover:opacity-100">
                  <span role="button" onClick={(e) => { e.stopPropagation(); onEdit(r); }}
                    className="inline-flex size-6 items-center justify-center rounded text-stone hover:bg-canvas hover:text-ink">
                    <Pencil className="size-3" />
                  </span>
                  <span role="button"
                    onClick={(e) => { e.stopPropagation(); if (confirm(`Delete role "${r.name}"?`)) del.mutate(r.id); }}
                    className="inline-flex size-6 items-center justify-center rounded text-stone hover:bg-canvas hover:text-brand-error">
                    <Trash2 className="size-3" />
                  </span>
                </span>
              )}
            </button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

/* ----------------- create/edit dialog ----------------- */

function RoleDialog({
  mode, role, onClose, onSaved,
}: {
  mode: 'create' | 'edit';
  role?: Role;
  onClose: () => void;
  onSaved: (r: Role) => void;
}) {
  const [name, setName]               = useState(role?.name ?? '');
  const [description, setDescription] = useState(role?.description ?? '');
  const [error, setError]             = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => mode === 'create'
      ? api<Role>('/admin/roles', { method: 'POST', body: { name, description } })
      : api<Role>(`/admin/roles/${role!.id}`, { method: 'PUT', body: { name, description } }),
    onSuccess: (r) => onSaved(r),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>{mode === 'create' ? 'New role' : `Edit ${role?.name}`}</DialogTitle>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); if (!name.trim()) return setError('Name required'); save.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
          </Field>
          <Field label="Description" hint="Optional. Shown next to the role wherever it's listed.">
            <Input value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={save.isPending}>{mode === 'create' ? 'Create role' : 'Save'}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ----------------- permission matrix ----------------- */

function PermissionMatrix({ role }: { role: Role }) {
  const qc = useQueryClient();
  const { data: dtData } = useQuery({
    queryKey: ['doctypes-catalog'],
    queryFn: () => api<DTList>('/admin/doctypes'),
  });
  const { data: permData, isLoading: permLoading } = useQuery({
    queryKey: ['role-permissions', role.id],
    queryFn: () => api<PermList>(`/admin/roles/${role.id}/permissions`),
  });

  // Local map keyed by doctype with the editable boolean matrix.
  const [rows, setRows] = useState<Map<string, PermissionRow>>(new Map());
  const [filter, setFilter] = useState('');
  const [showOnly, setShowOnly] = useState<'all' | 'granted'>('all');
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<number>(0);

  // Build the row map whenever the source data changes (or the selected role flips).
  useEffect(() => {
    const m = new Map<string, PermissionRow>();
    for (const dt of (dtData?.items ?? [])) {
      m.set(dt, emptyRow(dt));
    }
    for (const pr of (permData?.items ?? [])) {
      m.set(pr.doctype, { ...emptyRow(pr.doctype), ...pr });
    }
    setRows(m);
    setDirty(false);
  }, [dtData, permData, role.id]);

  const filtered = useMemo(() => {
    const arr = Array.from(rows.values()).sort((a, b) => a.doctype.localeCompare(b.doctype));
    const q = filter.trim().toLowerCase();
    return arr.filter((r) => {
      if (q && !r.doctype.toLowerCase().includes(q)) return false;
      if (showOnly === 'granted' && !ACTIONS.some((a) => r[a.key])) return false;
      return true;
    });
  }, [rows, filter, showOnly]);

  function setCell(doctype: string, action: keyof Omit<PermissionRow, 'doctype'>, value: boolean) {
    const next = new Map(rows);
    const row = { ...(next.get(doctype) ?? emptyRow(doctype)) };
    row[action] = value;
    // Sensible cascade: write/create/delete/submit/cancel/amend/print/export imply read.
    if (value && action !== 'can_read') row.can_read = true;
    next.set(doctype, row);
    setRows(next);
    setDirty(true);
  }

  function setColumn(action: keyof Omit<PermissionRow, 'doctype'>, value: boolean) {
    const next = new Map(rows);
    for (const r of next.values()) {
      r[action] = value;
      if (value && action !== 'can_read') r.can_read = true;
    }
    setRows(new Map(next));
    setDirty(true);
  }

  function setRow(doctype: string, value: boolean) {
    const next = new Map(rows);
    const row = { ...(next.get(doctype) ?? emptyRow(doctype)) };
    for (const a of ACTIONS) row[a.key] = value;
    next.set(doctype, row);
    setRows(next);
    setDirty(true);
  }

  const save = useMutation({
    mutationFn: () => api<PermList>(`/admin/roles/${role.id}/permissions`, {
      method: 'PUT',
      body: { items: Array.from(rows.values()) },
    }),
    onSuccess: () => {
      setDirty(false);
      setError(null);
      setSavedAt(Date.now());
      void qc.invalidateQueries({ queryKey: ['role-permissions', role.id] });
    },
    onError: (e: Error) => setError(e.message),
  });

  if (role.is_system) {
    return (
      <Card>
        <div className="text-center py-8">
          <ShieldCheck className="mx-auto size-6 text-stone mb-2" />
          <div className="text-body-sm text-charcoal">System role</div>
          <div className="text-caption text-stone mt-1">
            System roles always have full access. Create a custom role to manage a permission subset.
          </div>
        </div>
      </Card>
    );
  }

  return (
    <Card padded={false}>
      <div className="px-5 py-4 border-b border-hairline flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Permissions — {role.name}</CardTitle>
          <CardDescription>
            Tick the actions this role allows for each doctype. Writing implies reading.
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          {dirty
            ? <StatusPill tone="warning">Unsaved changes</StatusPill>
            : savedAt > 0 && <span className="text-caption text-brand-green-deep inline-flex items-center gap-1"><Check className="size-3.5" /> Saved</span>}
          <Button size="sm" onClick={() => save.mutate()} disabled={!dirty} loading={save.isPending}>
            <Save className="size-3.5" /> Save matrix
          </Button>
        </div>
      </div>

      <div className="px-3 py-2 border-b border-hairline flex items-center gap-2">
        <div className="relative flex-1 max-w-[280px]">
          <Filter className="size-3.5 text-stone absolute left-2.5 top-1/2 -translate-y-1/2" />
          <Input className="!h-8 !text-[13px] pl-7" value={filter}
            onChange={(e) => setFilter(e.target.value)} placeholder="Filter doctypes…" />
        </div>
        <div className="inline-flex items-center p-0.5 rounded-full bg-surface border border-hairline">
          <SegChip active={showOnly === 'all'}     label="All"     onClick={() => setShowOnly('all')} />
          <SegChip active={showOnly === 'granted'} label="Granted" onClick={() => setShowOnly('granted')} />
        </div>
        {filter && <span className="text-caption text-stone">{filtered.length} matching</span>}
      </div>

      {permLoading ? (
        <div className="p-5"><Skeleton className="h-64 w-full" /></div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-body-sm">
            <thead className="bg-surface-soft border-b border-hairline sticky top-0">
              <tr>
                <th className="text-left font-medium px-4 py-2 text-micro-uppercase text-stone">Doctype</th>
                {ACTIONS.map((a) => (
                  <th key={a.key} className="text-center font-medium px-2 py-2 text-micro-uppercase text-stone"
                    title={a.label}>
                    <div className="flex flex-col items-center gap-1">
                      <span>{a.abbr}</span>
                      <button type="button"
                        className="text-[10px] text-stone hover:text-ink"
                        onClick={() => {
                          const allOn = filtered.every((r) => r[a.key]);
                          setColumn(a.key, !allOn);
                        }}>
                        toggle
                      </button>
                    </div>
                  </th>
                ))}
                <th className="px-2 py-2 w-[60px]"></th>
              </tr>
            </thead>
            <tbody>
              {filtered.length === 0 ? (
                <tr>
                  <td colSpan={ACTIONS.length + 2} className="text-center py-10 text-stone text-body-sm">
                    No doctypes match your filter.
                  </td>
                </tr>
              ) : filtered.map((r) => {
                const allOn  = ACTIONS.every((a) => r[a.key]);
                return (
                  <tr key={r.doctype} className="border-b border-hairline last:border-0 hover:bg-surface-soft">
                    <td className="px-4 py-2 text-ink font-mono text-caption">{r.doctype}</td>
                    {ACTIONS.map((a) => (
                      <td key={a.key} className="px-2 py-2 text-center">
                        <input type="checkbox" className="accent-brand-green-deep"
                          checked={r[a.key]} onChange={(e) => setCell(r.doctype, a.key, e.target.checked)} />
                      </td>
                    ))}
                    <td className="px-2 py-2 text-right">
                      <button type="button"
                        className="text-caption text-stone hover:text-ink"
                        onClick={() => setRow(r.doctype, !allOn)}>
                        {allOn ? 'clear' : 'all'}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {error && (
        <div className="px-5 py-3 border-t border-hairline rounded-b-lg bg-brand-error/10 text-brand-error text-caption inline-flex items-start gap-2">
          <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
        </div>
      )}

      <div className="px-5 py-3 border-t border-hairline flex items-center gap-3 text-caption text-stone">
        <span>R Read · W Write · C Create · D Delete · S Submit · X Cancel · A Amend · P Print · E Export</span>
      </div>
    </Card>
  );
}

function SegChip({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick}
      className={cn('inline-flex items-center h-7 px-3 rounded-full text-caption transition-colors',
        active ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink')}>
      {label}
    </button>
  );
}

function emptyRow(doctype: string): PermissionRow {
  return {
    doctype,
    can_read: false, can_write: false, can_create: false, can_delete: false,
    can_submit: false, can_cancel: false, can_amend: false, can_print: false, can_export: false,
  };
}
