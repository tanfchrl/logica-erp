import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { Plus, RefreshCw, Building2, ShoppingBag, Users } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Card } from '@/components/Card';
import { Button } from '@/components/Button';
import { DataTable } from '@/components/DataTable';
import { StatusPill } from '@/components/StatusPill';
import { EmptyState, Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

/**
 * Companies — unified view over Customer + Supplier.
 *
 * Backend stays two tables; this page just fetches both and renders one
 * list with a relation pill + a tab filter. Twenty-style "Companies"
 * doesn't really care which side of the ledger the party sits on; users
 * who want the split can use /accounting/customers or
 * /accounting/suppliers directly.
 */

interface Party {
  id: string;
  name: string;
  display_name: string;
  npwp?: string;
  email?: string;
  phone?: string;
  is_individual: boolean;
  // Synthetic — derived from which fetch the row came from.
  relation: 'customer' | 'supplier' | 'both';
}

type RelationFilter = 'all' | 'customer' | 'supplier' | 'both';

export function Companies() {
  const navigate = useNavigate();

  const { data: customers, isLoading: lc, refetch: rc, isFetching: fc } = useQuery({
    queryKey: ['accounting-customers'],
    queryFn:  () => api<{ items: Omit<Party, 'relation'>[] }>('/accounting/customers'),
  });
  const { data: suppliers, isLoading: ls, refetch: rs, isFetching: fs } = useQuery({
    queryKey: ['accounting-suppliers'],
    queryFn:  () => api<{ items: Omit<Party, 'relation'>[] }>('/accounting/suppliers'),
  });

  const isLoading = lc || ls;
  const isFetching = fc || fs;
  const refetch = () => { void rc(); void rs(); };

  // Merge — companies that exist on BOTH sides get marked "both" so the
  // user knows we sell to AND buy from them. Match on display_name +
  // NPWP because the codes (name) are independent on the two tables.
  const merged: Party[] = useMemo(() => {
    const supplierByKey = new Map<string, Omit<Party, 'relation'>>();
    for (const s of suppliers?.items ?? []) {
      supplierByKey.set(matchKey(s), s);
    }
    const out: Party[] = [];
    const seenSupplier = new Set<string>();
    for (const c of customers?.items ?? []) {
      const k = matchKey(c);
      const matchedSupplier = supplierByKey.get(k);
      out.push({ ...c, relation: matchedSupplier ? 'both' : 'customer' });
      if (matchedSupplier) seenSupplier.add(matchedSupplier.id);
    }
    for (const s of suppliers?.items ?? []) {
      if (seenSupplier.has(s.id)) continue;
      out.push({ ...s, relation: 'supplier' });
    }
    return out;
  }, [customers, suppliers]);

  const [filter, setFilter] = useState<RelationFilter>('all');
  const filtered = useMemo(() => {
    if (filter === 'all') return merged;
    return merged.filter((p) => p.relation === filter || (filter !== 'both' && p.relation === 'both'));
  }, [merged, filter]);

  const counts = useMemo(() => ({
    all:      merged.length,
    customer: merged.filter((p) => p.relation === 'customer' || p.relation === 'both').length,
    supplier: merged.filter((p) => p.relation === 'supplier' || p.relation === 'both').length,
    both:     merged.filter((p) => p.relation === 'both').length,
  }), [merged]);

  return (
    <>
      <PageHeader
        crumbs={[{ label: 'CRM', to: '/crm' }, { label: 'Companies' }]}
        title="Companies"
        actions={
          <>
            <Button variant="ghost" size="icon" onClick={refetch} aria-label="Refresh">
              <RefreshCw className={`size-4 ${isFetching ? 'animate-spin' : ''}`} />
            </Button>
            <Button asChild>
              <Link to={'/accounting/customers/new' as never}><Plus className="size-4" /> New customer</Link>
            </Button>
            <Button variant="secondary" asChild>
              <Link to={'/accounting/suppliers/new' as never}><Plus className="size-4" /> New supplier</Link>
            </Button>
          </>
        }
      />

      <div className="flex-1 px-6 lg:px-8 pt-6 pb-8">
        <div className="mb-4 inline-flex items-center p-1 rounded-full bg-surface border border-hairline">
          {(['all','customer','supplier','both'] as const).map((k) => (
            <button
              key={k}
              type="button"
              onClick={() => setFilter(k)}
              className={cn(
                'inline-flex items-center gap-1.5 h-8 px-3 rounded-full text-body-sm transition-colors',
                filter === k ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink',
              )}
            >
              {k === 'all'      ? 'All' :
               k === 'customer' ? <><Users className="size-3.5" /> Customers</> :
               k === 'supplier' ? <><ShoppingBag className="size-3.5" /> Suppliers</> :
                                  'Both'}
              <span className="text-caption text-stone font-mono">{counts[k]}</span>
            </button>
          ))}
        </div>

        {isLoading ? (
          <Card><Skeleton className="h-64 w-full" /></Card>
        ) : (
          <DataTable
            columns={[
              { accessorKey: 'name', header: 'Code', cell: (i) => <span className="font-mono text-dense text-text-primary">{i.getValue<string>()}</span> },
              { accessorKey: 'display_name', header: 'Company',
                cell: (i) => (
                  <div className="flex items-center gap-2.5">
                    <div className="size-7 rounded-md bg-accent-soft text-accent inline-flex items-center justify-center shrink-0">
                      <Building2 className="size-3.5" />
                    </div>
                    <span className="text-text-primary truncate">{i.getValue<string>()}</span>
                  </div>
                ) },
              { accessorKey: 'relation', header: 'Relation', cell: (i) => <RelationPill value={i.getValue<Party['relation']>()} /> },
              { accessorKey: 'npwp', header: 'NPWP', cell: (i) => <span className="font-mono text-dense text-text-secondary">{i.getValue<string>() || '—'}</span> },
              { accessorKey: 'email', header: 'Email', cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
              { accessorKey: 'phone', header: 'Phone', cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
            ]}
            data={filtered}
            loading={isLoading}
            searchPlaceholder="Search companies…"
            onRowClick={(row: Party) => {
              // Click rule: customer takes you to the customer page; supplier
              // takes you there. "Both" defaults to the customer page (the
              // user can switch via the relation pill on the detail page).
              const path = row.relation === 'supplier'
                ? `/accounting/suppliers/${row.id}`
                : `/accounting/customers/${row.id}`;
              void navigate({ to: path as never });
            }}
            emptyState={
              <EmptyState
                icon={Building2}
                title="No companies yet"
                description="Customers and suppliers show up here together. Add one to get started."
              />
            }
          />
        )}
      </div>
    </>
  );
}

function RelationPill({ value }: { value: Party['relation'] }) {
  switch (value) {
    case 'customer': return <StatusPill tone="success" withDot={false}><Users className="size-3" /> Customer</StatusPill>;
    case 'supplier': return <StatusPill tone="info"    withDot={false}><ShoppingBag className="size-3" /> Supplier</StatusPill>;
    case 'both':     return <StatusPill tone="accent"  withDot={false}>Both</StatusPill>;
  }
}

// Match-key: prefer NPWP (unique by tax ID), fall back to lowercased
// display_name. Same NPWP across customer + supplier means same company.
function matchKey(p: { display_name: string; npwp?: string }): string {
  if (p.npwp && p.npwp.length > 0) return `npwp:${p.npwp}`;
  return `name:${p.display_name.trim().toLowerCase()}`;
}
