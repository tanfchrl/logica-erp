import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Building2, Plus, Check } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface Company {
  id: string;
  name: string;
  legal_name: string;
  abbreviation: string;
  country: string;
  default_currency: string;
  email?: string;
  phone?: string;
  website?: string;
  npwp?: string;
  npwp_address?: string;
  address_line?: string;
  city?: string;
  province?: string;
  postal_code?: string;
}
interface CompanyList { items: Company[] }

export function CompanySection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['companies'],
    queryFn: () => api<CompanyList>('/accounting/companies'),
  });
  const companies = data?.items ?? [];
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen]   = useState(false);

  const selected = selectedId
    ? companies.find((c) => c.id === selectedId) ?? null
    : companies[0] ?? null;

  return (
    <div className="space-y-6">
      <section>
        <div className="mb-3 flex items-end justify-between gap-3">
          <div>
            <CardTitle>Companies</CardTitle>
            <CardDescription>
              Legal entities you transact under. Each ledger, document, and report scopes to one company.
            </CardDescription>
          </div>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="size-3.5" /> New company
          </Button>
        </div>

        {isLoading ? (
          <Card><Skeleton className="h-20 w-full" /></Card>
        ) : companies.length === 0 ? (
          <Card>
            <div className="text-center py-8">
              <Building2 className="mx-auto size-6 text-stone mb-2" />
              <div className="text-body-sm text-charcoal">No companies yet.</div>
              <div className="text-caption text-stone mt-1">Create your first company to start posting documents.</div>
              <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
                <Plus className="size-3.5" /> Create company
              </Button>
            </div>
          </Card>
        ) : (
          <div className="grid gap-2">
            {companies.map((c) => {
              const isSelected = selected?.id === c.id;
              return (
                <button
                  key={c.id}
                  type="button"
                  onClick={() => setSelectedId(c.id)}
                  className={cn(
                    'w-full text-left bg-canvas border rounded-lg p-3 flex items-center gap-3 transition-colors',
                    isSelected ? 'border-ink' : 'border-hairline hover:border-stone/40',
                  )}
                >
                  <span className="inline-flex items-center justify-center size-9 rounded-md bg-surface text-ink font-semibold">
                    {c.abbreviation}
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="text-body-sm font-medium text-ink truncate">{c.name}</div>
                    <div className="text-caption text-stone truncate">
                      {c.legal_name}{c.npwp ? ` · NPWP ${c.npwp}` : ''}
                    </div>
                  </div>
                  <span className="text-caption font-mono text-stone">{c.default_currency}</span>
                  {isSelected && <Check className="size-4 text-brand-green" />}
                </button>
              );
            })}
          </div>
        )}
      </section>

      {selected && (
        <CompanyDetail
          key={selected.id}
          company={selected}
          onSaved={() => void qc.invalidateQueries({ queryKey: ['companies'] })}
        />
      )}

      {createOpen && (
        <CreateCompanyDialog
          onClose={() => setCreateOpen(false)}
          onCreated={(c) => {
            void qc.invalidateQueries({ queryKey: ['companies'] });
            setSelectedId(c.id);
            setCreateOpen(false);
          }}
        />
      )}
    </div>
  );
}

/* ---------------- Company detail (editable) ----------------- */

interface UpdateInput {
  name: string;
  legal_name: string;
  country: string;
  default_currency: string;
  npwp?: string;
  npwp_address?: string;
  address_line?: string;
  city?: string;
  province?: string;
  postal_code?: string;
  email?: string;
  phone?: string;
  website?: string;
}

function CompanyDetail({ company, onSaved }: { company: Company; onSaved: () => void }) {
  const initial: UpdateInput = {
    name: company.name,
    legal_name: company.legal_name,
    country: company.country,
    default_currency: company.default_currency,
    npwp: company.npwp ?? '',
    npwp_address: company.npwp_address ?? '',
    address_line: company.address_line ?? '',
    city: company.city ?? '',
    province: company.province ?? '',
    postal_code: company.postal_code ?? '',
    email: company.email ?? '',
    phone: company.phone ?? '',
    website: company.website ?? '',
  };
  const [form, setForm] = useState<UpdateInput>(initial);
  const [error, setError] = useState<string | null>(null);

  const dirty = JSON.stringify(form) !== JSON.stringify(initial);

  const mut = useMutation({
    mutationFn: (input: UpdateInput) =>
      api<Company>(`/accounting/companies/${company.id}`, { method: 'PUT', body: input }),
    onSuccess: () => { setError(null); onSaved(); },
    onError:   (e: Error) => setError(e.message),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!form.name.trim() || !form.legal_name.trim()) {
      setError('Name and legal name are required.');
      return;
    }
    if (form.npwp && !/^\d{16}$/.test(form.npwp)) {
      setError('NPWP must be 16 digits.');
      return;
    }
    mut.mutate(form);
  }

  function set<K extends keyof UpdateInput>(key: K, value: UpdateInput[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  return (
    <section>
      <div className="mb-3 flex items-end justify-between gap-3">
        <div>
          <CardTitle>{company.name}</CardTitle>
          <CardDescription>{company.legal_name}</CardDescription>
        </div>
        <StatusPill tone={dirty ? 'accent' : 'neutral'}>{dirty ? 'Unsaved changes' : 'Editing'}</StatusPill>
      </div>

      <Card>
        <form onSubmit={submit} className="space-y-5">
          <div className="grid sm:grid-cols-2 gap-x-6 gap-y-4">
            <Field label="Name (display)">
              <Input value={form.name} onChange={(e) => set('name', e.target.value)} required />
            </Field>
            <Field label="Legal name">
              <Input value={form.legal_name} onChange={(e) => set('legal_name', e.target.value)} required />
            </Field>
            <div>
              <div className="text-micro-uppercase text-stone mb-0.5">Abbreviation</div>
              <div className="text-body-sm text-ink font-mono">{company.abbreviation}</div>
              <div className="text-caption text-stone mt-0.5">Immutable — embedded in account names.</div>
            </div>
            <Field label="Default currency">
              <Input value={form.default_currency} maxLength={3} className="uppercase font-mono"
                onChange={(e) => set('default_currency', e.target.value.toUpperCase())} />
            </Field>
            <Field label="Country">
              <Input value={form.country} onChange={(e) => set('country', e.target.value)} />
            </Field>
            <Field label="NPWP">
              <Input value={form.npwp ?? ''} className="font-mono" onChange={(e) => set('npwp', e.target.value)} />
            </Field>
            <div className="sm:col-span-2">
              <Field label="NPWP address">
                <Input value={form.npwp_address ?? ''} onChange={(e) => set('npwp_address', e.target.value)} />
              </Field>
            </div>
            <div className="sm:col-span-2">
              <Field label="Address">
                <Input value={form.address_line ?? ''} onChange={(e) => set('address_line', e.target.value)} />
              </Field>
            </div>
            <Field label="City">
              <Input value={form.city ?? ''} onChange={(e) => set('city', e.target.value)} />
            </Field>
            <Field label="Province">
              <Input value={form.province ?? ''} onChange={(e) => set('province', e.target.value)} />
            </Field>
            <Field label="Postal code">
              <Input value={form.postal_code ?? ''} className="font-mono" onChange={(e) => set('postal_code', e.target.value)} />
            </Field>
            <Field label="Email">
              <Input type="email" value={form.email ?? ''} onChange={(e) => set('email', e.target.value)} />
            </Field>
            <Field label="Phone">
              <Input value={form.phone ?? ''} className="font-mono" onChange={(e) => set('phone', e.target.value)} />
            </Field>
            <Field label="Website">
              <Input value={form.website ?? ''} onChange={(e) => set('website', e.target.value)} />
            </Field>
          </div>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="pt-4 border-t border-hairline flex items-center justify-end gap-2">
            <Button type="button" variant="ghost" disabled={!dirty || mut.isPending}
              onClick={() => { setForm(initial); setError(null); }}>
              Reset
            </Button>
            <Button type="submit" loading={mut.isPending} disabled={!dirty}>Save changes</Button>
          </div>
        </form>
      </Card>
    </section>
  );
}

/* ---------------- Create dialog ----------------- */

interface CreateInput {
  name: string;
  legal_name: string;
  abbreviation: string;
  country: string;
  default_currency: string;
  npwp?: string;
  address_line?: string;
  city?: string;
  email?: string;
  phone?: string;
  grant_to_current_user: boolean;
}

function CreateCompanyDialog({
  onClose, onCreated,
}: { onClose: () => void; onCreated: (c: Company) => void }) {
  const [form, setForm] = useState<CreateInput>({
    name: '', legal_name: '', abbreviation: '',
    country: 'Indonesia', default_currency: 'IDR',
    grant_to_current_user: true,
  });
  const [error, setError] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: (input: CreateInput) =>
      api<Company>('/accounting/companies', { method: 'POST', body: input }),
    onSuccess: (c) => onCreated(c),
    onError:   (e: Error) => setError(e.message),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!form.name || !form.legal_name || !form.abbreviation) {
      setError('Name, legal name, and abbreviation are required.');
      return;
    }
    mut.mutate(form);
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New company</DialogTitle>
        <DialogDescription>
          Create a new legal entity. You can switch between companies from the workspace dropdown.
        </DialogDescription>

        <form onSubmit={submit} className="mt-4 grid sm:grid-cols-2 gap-3">
          <Field label="Name (display)">
            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          </Field>
          <Field label="Abbreviation">
            <Input
              value={form.abbreviation}
              maxLength={5}
              className="uppercase font-mono"
              onChange={(e) => setForm({ ...form, abbreviation: e.target.value.toUpperCase() })}
              required
            />
          </Field>
          <Field label="Legal name" htmlFor="legal_name">
            <Input id="legal_name" value={form.legal_name} onChange={(e) => setForm({ ...form, legal_name: e.target.value })} required />
          </Field>
          <Field label="Country">
            <Input value={form.country} onChange={(e) => setForm({ ...form, country: e.target.value })} />
          </Field>
          <Field label="Default currency">
            <Input value={form.default_currency} maxLength={3} className="uppercase font-mono"
              onChange={(e) => setForm({ ...form, default_currency: e.target.value.toUpperCase() })} />
          </Field>
          <Field label="NPWP">
            <Input value={form.npwp ?? ''} className="font-mono"
              onChange={(e) => setForm({ ...form, npwp: e.target.value })} />
          </Field>
          <Field label="Address" htmlFor="addr">
            <Input id="addr" value={form.address_line ?? ''} onChange={(e) => setForm({ ...form, address_line: e.target.value })} />
          </Field>
          <Field label="City">
            <Input value={form.city ?? ''} onChange={(e) => setForm({ ...form, city: e.target.value })} />
          </Field>
          <Field label="Email">
            <Input type="email" value={form.email ?? ''} onChange={(e) => setForm({ ...form, email: e.target.value })} />
          </Field>
          <Field label="Phone">
            <Input value={form.phone ?? ''} className="font-mono" onChange={(e) => setForm({ ...form, phone: e.target.value })} />
          </Field>

          {error && (
            <div className="sm:col-span-2 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">
              {error}
            </div>
          )}

          <div className="sm:col-span-2 flex justify-end gap-2 mt-2">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create company</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
