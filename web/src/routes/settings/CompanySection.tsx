import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Building2, Plus, Check, AlertCircle } from 'lucide-react';
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

      {selected && <CompanyDetail company={selected} />}

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

/* ---------------- Company detail (read-only for now) ----------------- */

function CompanyDetail({ company }: { company: Company }) {
  return (
    <section>
      <div className="mb-3 flex items-end justify-between gap-3">
        <div>
          <CardTitle>{company.name}</CardTitle>
          <CardDescription>{company.legal_name}</CardDescription>
        </div>
        <StatusPill tone="neutral">Read-only</StatusPill>
      </div>

      <Card>
        <div className="grid sm:grid-cols-2 gap-x-6 gap-y-4">
          <ReadField label="Name"             value={company.name} />
          <ReadField label="Legal name"       value={company.legal_name} />
          <ReadField label="Abbreviation"     value={company.abbreviation} mono />
          <ReadField label="Default currency" value={company.default_currency} mono />
          <ReadField label="Country"          value={company.country} />
          <ReadField label="NPWP"             value={company.npwp ?? '—'} mono />
          <ReadField label="NPWP address"     value={company.npwp_address ?? '—'} className="sm:col-span-2" />
          <ReadField label="Address"          value={company.address_line ?? '—'} className="sm:col-span-2" />
          <ReadField label="City"             value={company.city ?? '—'} />
          <ReadField label="Province"         value={company.province ?? '—'} />
          <ReadField label="Postal code"      value={company.postal_code ?? '—'} mono />
          <ReadField label="Email"            value={company.email ?? '—'} />
          <ReadField label="Phone"            value={company.phone ?? '—'} mono />
          <ReadField label="Website"          value={company.website ?? '—'} />
        </div>

        <div className="mt-5 pt-4 border-t border-hairline flex items-start gap-2 text-caption text-stone">
          <AlertCircle className="size-3.5 mt-0.5 shrink-0" />
          <span>
            Editing existing companies needs the <span className="font-mono">PUT /accounting/companies/{'{id}'}</span> endpoint
            (not yet on the API). For now, create a fresh company or contact ops.
          </span>
        </div>
      </Card>
    </section>
  );
}

function ReadField({ label, value, mono, className }: { label: string; value: string; mono?: boolean; className?: string }) {
  return (
    <div className={className}>
      <div className="text-micro-uppercase text-stone mb-0.5">{label}</div>
      <div className={cn('text-body-sm text-ink truncate', mono && 'font-mono')}>{value || '—'}</div>
    </div>
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
