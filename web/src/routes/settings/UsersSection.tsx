import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus, UserCog, KeyRound, Building2, Monitor, Save, Trash2,
  AlertCircle, Check, Power, PowerOff,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Avatar } from '@/components/Avatar';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface User {
  id: string;
  email: string;
  full_name?: string;
  enabled: boolean;
  is_system: boolean;
  roles: string[];
  companies: string[];
  ip_allowlist: string[];
  created_at: string;
  updated_at: string;
}
interface UserList { items: User[] }

interface Role { id: string; name: string; description?: string; is_system: boolean; user_count: number }
interface RoleList { items: Role[] }

interface Company { id: string; name: string; abbreviation: string }
interface CompanyList { items: Company[] }

interface SessionRow { id: string; issued_at: string; expires_at: string; user_agent?: string; ip?: string; revoked: boolean }
interface SessionList { items: SessionRow[] }

type Tab = 'profile' | 'roles' | 'companies' | 'sessions';

export function UsersSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['users'], queryFn: () => api<UserList>('/admin/users') });
  const users = data?.items ?? [];

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const selected = selectedId
    ? users.find((u) => u.id === selectedId) ?? null
    : users[0] ?? null;

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Users</CardTitle>
          <CardDescription>
            Invite team members, assign roles, manage company access, see active sessions.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New user
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-40 w-full" /></Card>
      ) : users.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <UserCog className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No users yet.</div>
          </div>
        </Card>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-[280px_1fr] gap-4">
          <UserList items={users} selectedId={selected?.id ?? null} onSelect={setSelectedId} />
          {selected && (
            <UserDetail
              user={selected}
              onChanged={() => qc.invalidateQueries({ queryKey: ['users'] })}
            />
          )}
        </div>
      )}

      {createOpen && (
        <CreateUserDialog
          onClose={() => setCreateOpen(false)}
          onCreated={(u) => {
            void qc.invalidateQueries({ queryKey: ['users'] });
            setSelectedId(u.id);
            setCreateOpen(false);
          }}
        />
      )}
    </div>
  );
}

/* ----------------------------- list ----------------------------- */

function UserList({ items, selectedId, onSelect }: { items: User[]; selectedId: string | null; onSelect: (id: string) => void }) {
  return (
    <Card padded={false}>
      <ul className="divide-y divide-hairline">
        {items.map((u) => (
          <li key={u.id}>
            <button
              type="button"
              onClick={() => onSelect(u.id)}
              className={cn(
                'w-full text-left px-3 py-2.5 flex items-center gap-3 transition-colors',
                u.id === selectedId ? 'bg-surface' : 'hover:bg-surface-soft',
              )}
            >
              <Avatar name={u.full_name || u.email} size="md" />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-body-sm font-medium text-ink truncate">{u.full_name || u.email}</span>
                  {u.is_system && <StatusPill tone="neutral" withDot={false}>System</StatusPill>}
                </div>
                <div className="text-caption text-stone truncate">{u.email}</div>
              </div>
              {!u.enabled && <StatusPill tone="warning" withDot={false}>Disabled</StatusPill>}
            </button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

/* --------------------------- detail --------------------------- */

function UserDetail({ user, onChanged }: { user: User; onChanged: () => void }) {
  const [tab, setTab] = useState<Tab>('profile');

  return (
    <Card padded={false}>
      <div className="px-5 py-4 border-b border-hairline flex items-center gap-3 flex-wrap">
        <Avatar name={user.full_name || user.email} size="lg" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <div className="text-heading-5 text-ink truncate">{user.full_name || user.email}</div>
            {user.is_system  && <StatusPill tone="neutral" withDot={false}>System</StatusPill>}
            {!user.enabled   && <StatusPill tone="warning" withDot={false}>Disabled</StatusPill>}
          </div>
          <div className="text-caption text-stone">{user.email}</div>
        </div>
        <ToggleEnabledButton user={user} onChanged={onChanged} />
      </div>

      <div className="flex items-center gap-1 px-3 pt-2 border-b border-hairline">
        <TabChip active={tab === 'profile'}   icon={UserCog}   label="Profile"   onClick={() => setTab('profile')} />
        <TabChip active={tab === 'roles'}     icon={KeyRound}  label="Roles"     onClick={() => setTab('roles')} />
        <TabChip active={tab === 'companies'} icon={Building2} label="Companies" onClick={() => setTab('companies')} />
        <TabChip active={tab === 'sessions'}  icon={Monitor}   label="Sessions"  onClick={() => setTab('sessions')} />
      </div>

      <div className="p-5">
        {tab === 'profile'   && <ProfilePanel   user={user} onChanged={onChanged} />}
        {tab === 'roles'     && <RolesPanel     user={user} onChanged={onChanged} />}
        {tab === 'companies' && <CompaniesPanel user={user} onChanged={onChanged} />}
        {tab === 'sessions'  && <SessionsPanel  user={user} />}
      </div>
    </Card>
  );
}

function ToggleEnabledButton({ user, onChanged }: { user: User; onChanged: () => void }) {
  const mut = useMutation({
    mutationFn: () => api<User>(`/admin/users/${user.id}`, { method: 'PUT', body: { enabled: !user.enabled } }),
    onSuccess: () => onChanged(),
  });
  if (user.is_system) return null;
  return (
    <Button size="sm" variant="secondary" onClick={() => mut.mutate()} loading={mut.isPending}>
      {user.enabled
        ? <><PowerOff className="size-3.5" /> Disable</>
        : <><Power    className="size-3.5" /> Enable</>}
    </Button>
  );
}

function TabChip({
  active, icon: Icon, label, onClick,
}: { active: boolean; icon: React.ComponentType<{ className?: string }>; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 h-9 px-3 text-body-sm transition-colors border-b-2 -mb-px',
        active ? 'border-ink text-ink font-medium' : 'border-transparent text-steel hover:text-ink',
      )}
    >
      <Icon className="size-3.5" />
      {label}
    </button>
  );
}

/* ----- Profile ----- */

function ProfilePanel({ user, onChanged }: { user: User; onChanged: () => void }) {
  const [fullName, setFullName] = useState(user.full_name ?? '');
  const [ipAllow, setIPAllow]   = useState((user.ip_allowlist ?? []).join(', '));
  const [pwOpen, setPwOpen]     = useState(false);
  const [error, setError]       = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => api<User>(`/admin/users/${user.id}`, {
      method: 'PUT',
      body: {
        full_name: fullName,
        ip_allowlist: ipAllow.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean),
      },
    }),
    onSuccess: () => { onChanged(); setError(null); },
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <div className="space-y-4 max-w-2xl">
      <div className="grid sm:grid-cols-2 gap-4">
        <Field label="Full name">
          <Input value={fullName} onChange={(e) => setFullName(e.target.value)} />
        </Field>
        <Field label="Email" hint="Email is the login; changing requires support.">
          <Input value={user.email} disabled />
        </Field>
      </div>

      <Field label="IP allowlist" hint="Comma- or space-separated CIDRs (e.g. 203.0.113.0/24, 198.51.100.42/32). Empty = no restriction.">
        <Input value={ipAllow} onChange={(e) => setIPAllow(e.target.value)} className="font-mono" placeholder="(none)" />
      </Field>

      {error && (
        <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
      )}

      <div className="flex items-center gap-3 pt-3 border-t border-hairline">
        <Button size="sm" onClick={() => save.mutate()} loading={save.isPending}>
          <Save className="size-3.5" /> Save profile
        </Button>
        <Button size="sm" variant="secondary" onClick={() => setPwOpen(true)}>
          <KeyRound className="size-3.5" /> Set password
        </Button>
      </div>

      {pwOpen && <PasswordDialog user={user} onClose={() => setPwOpen(false)} onDone={onChanged} />}
    </div>
  );
}

function PasswordDialog({ user, onClose, onDone }: { user: User; onClose: () => void; onDone: () => void }) {
  const [pw, setPw]     = useState('');
  const [error, setErr] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<void>(`/admin/users/${user.id}/password`, { method: 'POST', body: { new_password: pw } }),
    onSuccess: () => { onDone(); onClose(); },
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>Set password for {user.full_name || user.email}</DialogTitle>
        <DialogDescription>
          Setting a password revokes all of this user's active sessions.
        </DialogDescription>

        <form onSubmit={(e) => { e.preventDefault(); setErr(null); if (pw.length < 8) return setErr('At least 8 characters'); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="New password" hint="At least 8 characters.">
            <Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoFocus />
          </Field>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Set password</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ----- Roles ----- */

function RolesPanel({ user, onChanged }: { user: User; onChanged: () => void }) {
  const { data: roles } = useQuery({ queryKey: ['roles'], queryFn: () => api<RoleList>('/admin/roles') });
  const [selected, setSelected] = useState<Set<string>>(new Set(user.roles));
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => api<User>(`/admin/users/${user.id}/roles`, {
      method: 'PUT', body: { roles: Array.from(selected) },
    }),
    onSuccess: () => { onChanged(); setError(null); },
    onError:   (e: Error) => setError(e.message),
  });

  function toggle(id: string) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id); else next.add(id);
    setSelected(next);
  }

  return (
    <div className="space-y-3 max-w-2xl">
      <div className="text-caption text-stone">
        Roles bundle permissions. Stack as many as you need; access is the union of all.
      </div>
      <div className="space-y-1">
        {(roles?.items ?? []).map((r) => {
          const checked = selected.has(r.id);
          return (
            <label key={r.id}
              className={cn('flex items-center gap-3 px-3 py-2.5 rounded-md border transition-colors cursor-pointer',
                checked ? 'border-ink bg-surface' : 'border-hairline hover:bg-surface-soft')}>
              <input type="checkbox" className="accent-brand-green-deep" checked={checked} onChange={() => toggle(r.id)} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-body-sm font-medium text-ink">{r.name}</span>
                  {r.is_system && <StatusPill tone="neutral" withDot={false}>System</StatusPill>}
                </div>
                {r.description && <div className="text-caption text-stone">{r.description}</div>}
              </div>
              <span className="text-caption text-stone">{r.user_count} users</span>
            </label>
          );
        })}
      </div>

      {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}

      <div className="flex justify-end pt-3 border-t border-hairline">
        <Button size="sm" onClick={() => save.mutate()} loading={save.isPending}>
          <Save className="size-3.5" /> Save roles
        </Button>
      </div>
    </div>
  );
}

/* ----- Companies ----- */

function CompaniesPanel({ user, onChanged }: { user: User; onChanged: () => void }) {
  const { data: companies } = useQuery({
    queryKey: ['companies'], queryFn: () => api<CompanyList>('/accounting/companies'),
  });
  const [selected, setSelected] = useState<Set<string>>(new Set(user.companies));
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => api<User>(`/admin/users/${user.id}/companies`, {
      method: 'PUT', body: { companies: Array.from(selected) },
    }),
    onSuccess: () => { onChanged(); setError(null); },
    onError:   (e: Error) => setError(e.message),
  });

  function toggle(id: string) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id); else next.add(id);
    setSelected(next);
  }

  return (
    <div className="space-y-3 max-w-2xl">
      <div className="text-caption text-stone">
        Grant per-company access. With no companies selected, the user can sign in but won't see any business data.
      </div>
      <div className="space-y-1">
        {(companies?.items ?? []).map((c) => {
          const checked = selected.has(c.id);
          return (
            <label key={c.id}
              className={cn('flex items-center gap-3 px-3 py-2.5 rounded-md border transition-colors cursor-pointer',
                checked ? 'border-ink bg-surface' : 'border-hairline hover:bg-surface-soft')}>
              <input type="checkbox" className="accent-brand-green-deep" checked={checked} onChange={() => toggle(c.id)} />
              <span className="inline-flex items-center justify-center size-7 rounded-md bg-surface text-ink font-semibold text-caption">
                {c.abbreviation}
              </span>
              <span className="text-body-sm text-ink">{c.name}</span>
            </label>
          );
        })}
      </div>

      {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}

      <div className="flex justify-end pt-3 border-t border-hairline">
        <Button size="sm" onClick={() => save.mutate()} loading={save.isPending}>
          <Save className="size-3.5" /> Save companies
        </Button>
      </div>
    </div>
  );
}

/* ----- Sessions ----- */

function SessionsPanel({ user }: { user: User }) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['user-sessions', user.id],
    queryFn: () => api<SessionList>(`/admin/users/${user.id}/sessions`),
  });
  const sessions = data?.items ?? [];

  const revoke = useMutation({
    mutationFn: (sid: string) => api<void>(`/admin/users/${user.id}/sessions/${sid}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['user-sessions', user.id] }),
  });

  if (isLoading) return <Skeleton className="h-32 w-full" />;
  if (sessions.length === 0) {
    return <div className="text-body-sm text-stone py-4 text-center">No sessions on record.</div>;
  }

  const active = sessions.filter((s) => !s.revoked && new Date(s.expires_at) > new Date());

  return (
    <div className="space-y-3 max-w-3xl">
      <div className="text-caption text-stone">{active.length} active · {sessions.length - active.length} expired or revoked</div>
      <Card padded={false}>
        <table className="w-full text-body-sm">
          <thead className="bg-surface-soft border-b border-hairline">
            <tr className="text-micro-uppercase text-stone">
              <th className="text-left  font-medium px-4 py-2.5">Issued</th>
              <th className="text-left  font-medium px-4 py-2.5">Expires</th>
              <th className="text-left  font-medium px-4 py-2.5">IP</th>
              <th className="text-left  font-medium px-4 py-2.5">User agent</th>
              <th className="text-right font-medium px-4 py-2.5"></th>
            </tr>
          </thead>
          <tbody>
            {sessions.map((s) => {
              const isActive = !s.revoked && new Date(s.expires_at) > new Date();
              return (
                <tr key={s.id} className="border-b border-hairline last:border-0">
                  <td className="px-4 py-2 text-stone num">{new Date(s.issued_at).toLocaleString('id-ID')}</td>
                  <td className="px-4 py-2 text-stone num">{new Date(s.expires_at).toLocaleString('id-ID')}</td>
                  <td className="px-4 py-2 text-charcoal font-mono text-caption">{s.ip ?? '—'}</td>
                  <td className="px-4 py-2 text-stone truncate max-w-[280px]">{s.user_agent ?? '—'}</td>
                  <td className="px-4 py-2 text-right">
                    {isActive ? (
                      <Button size="sm" variant="ghost" onClick={() => revoke.mutate(s.id)}>
                        <Trash2 className="size-3.5" /> Revoke
                      </Button>
                    ) : (
                      <StatusPill tone="neutral" withDot={false}>
                        {s.revoked ? 'Revoked' : 'Expired'}
                      </StatusPill>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Card>
    </div>
  );
}

/* ----- Create dialog ----- */

function CreateUserDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (u: User) => void }) {
  const { data: roles }     = useQuery({ queryKey: ['roles'],     queryFn: () => api<RoleList>('/admin/roles') });
  const { data: companies } = useQuery({ queryKey: ['companies'], queryFn: () => api<CompanyList>('/accounting/companies') });

  const [email, setEmail]       = useState('');
  const [fullName, setFullName] = useState('');
  const [password, setPassword] = useState('');
  const [pickedRoles, setPR]    = useState<Set<string>>(new Set());
  const [pickedCos, setPC]      = useState<Set<string>>(new Set());
  const [error, setError]       = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<User>('/admin/users', {
      method: 'POST',
      body: { email, full_name: fullName, password, roles: Array.from(pickedRoles), companies: Array.from(pickedCos) },
    }),
    onSuccess: (u) => onCreated(u),
    onError:   (e: Error) => setError(e.message),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!email || !password) return setError('Email and password are required.');
    if (password.length < 8)  return setError('Password must be at least 8 characters.');
    mut.mutate();
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>New user</DialogTitle>
        <DialogDescription>
          The user signs in with email + password. Share the password out-of-band; they can change it after first login.
        </DialogDescription>

        <form onSubmit={submit} className="mt-4 space-y-4">
          <div className="grid sm:grid-cols-2 gap-3">
            <Field label="Email">
              <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
            </Field>
            <Field label="Full name">
              <Input value={fullName} onChange={(e) => setFullName(e.target.value)} />
            </Field>
            <div className="sm:col-span-2">
              <Field label="Initial password" hint="At least 8 characters.">
                <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
              </Field>
            </div>
          </div>

          <PickerBlock title="Assign roles" empty="No roles defined yet.">
            <ChipPicker items={(roles?.items ?? []).map((r) => ({ id: r.id, label: r.name }))}
              selected={pickedRoles} onChange={setPR} />
          </PickerBlock>

          <PickerBlock title="Grant companies" empty="No companies created yet.">
            <ChipPicker items={(companies?.items ?? []).map((c) => ({ id: c.id, label: c.name, hint: c.abbreviation }))}
              selected={pickedCos} onChange={setPC} />
          </PickerBlock>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create user</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function PickerBlock({ title, empty, children }: { title: string; empty: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="label-base mb-1.5">{title}</div>
      <div className="text-caption text-stone mb-2">{empty}</div>
      {children}
    </div>
  );
}

function ChipPicker({
  items, selected, onChange,
}: {
  items: { id: string; label: string; hint?: string }[];
  selected: Set<string>;
  onChange: (next: Set<string>) => void;
}) {
  function toggle(id: string) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id); else next.add(id);
    onChange(next);
  }
  if (items.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-1.5">
      {items.map((it) => {
        const on = selected.has(it.id);
        return (
          <button key={it.id} type="button" onClick={() => toggle(it.id)}
            className={cn(
              'inline-flex items-center gap-1.5 px-3 h-8 rounded-full text-caption transition-colors',
              on ? 'bg-primary text-primary-fg' : 'bg-surface text-steel hover:text-ink border border-hairline',
            )}>
            {on && <Check className="size-3" />}
            <span>{it.label}</span>
            {it.hint && <span className="text-stone font-mono">{it.hint}</span>}
          </button>
        );
      })}
    </div>
  );
}
