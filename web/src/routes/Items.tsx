import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { type ColumnDef } from '@tanstack/react-table';
import { Package, Plus, Download, Filter } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Button } from '@/components/Button';
import { DataTable } from '@/components/DataTable';
import { StatusPill } from '@/components/StatusPill';
import { EmptyState } from '@/components/EmptyState';
import { Kbd } from '@/components/Kbd';
import { api } from '@/lib/api';
import { money } from '@/lib/format';

interface Item {
  id: string;
  code: string;
  name: string;
  description?: string;
  stock_uom: string;
  is_stock_item: boolean;
  is_sales_item: boolean;
  is_purchase_item: boolean;
  standard_rate: string;
  created_at: string;
}

interface ItemListResponse {
  items: Item[];
}

export function ItemsPage() {
  const navigate = useNavigate();
  const { data, isLoading } = useQuery({
    queryKey: ['items'],
    queryFn: () => api<ItemListResponse>('/accounting/items'),
  });

  const items = data?.items ?? [];

  const columns = useMemo<ColumnDef<Item>[]>(() => [
    {
      accessorKey: 'code',
      header: 'Code',
      cell: (info) => (
        <span className="font-medium text-text-primary font-mono text-dense">
          {info.getValue<string>()}
        </span>
      ),
    },
    {
      accessorKey: 'name',
      header: 'Name',
      cell: (info) => (
        <div className="flex items-center gap-2.5 min-w-0">
          <div className="size-7 rounded-md bg-accent-soft text-accent inline-flex items-center justify-center shrink-0">
            <Package className="size-3.5" />
          </div>
          <div className="min-w-0">
            <div className="text-text-primary truncate">{info.getValue<string>()}</div>
            {info.row.original.description && (
              <div className="text-caption text-text-tertiary truncate">
                {info.row.original.description}
              </div>
            )}
          </div>
        </div>
      ),
    },
    {
      accessorKey: 'stock_uom',
      header: 'UOM',
      cell: (info) => <span className="text-text-secondary">{info.getValue<string>()}</span>,
    },
    {
      id: 'flags',
      header: 'Flags',
      cell: (info) => (
        <div className="flex items-center gap-1">
          {info.row.original.is_stock_item    && <StatusPill tone="info" withDot={false}>Stock</StatusPill>}
          {info.row.original.is_sales_item    && <StatusPill tone="success" withDot={false}>Sales</StatusPill>}
          {info.row.original.is_purchase_item && <StatusPill tone="accent" withDot={false}>Purchase</StatusPill>}
        </div>
      ),
    },
    {
      accessorKey: 'standard_rate',
      header: 'Standard rate',
      meta: { align: 'right' },
      cell: (info) => <span className="font-medium">{money(info.getValue<string>())}</span>,
    },
  ], []);

  return (
    <>
      <PageHeader
        crumbs={[{ label: 'Finance', to: '/accounting' }, { label: 'Items' }]}
        title="Items"
        subtitle="Master catalogue of products and services."
        actions={
          <>
            <Button variant="secondary"><Filter className="size-4" /> Filter</Button>
            <Button variant="secondary"><Download className="size-4" /> Export</Button>
            <Button asChild>
              <Link to={'/accounting/items/new' as never}>
                <Plus className="size-4" /> New item
                <Kbd className="ml-1">N I</Kbd>
              </Link>
            </Button>
          </>
        }
      />

      <div className="flex-1 px-6 lg:px-8 pt-6 pb-8">
        <DataTable
          columns={columns}
          data={items}
          loading={isLoading}
          searchPlaceholder="Search by code, name, description…"
          onRowClick={(row: Item) => void navigate({ to: `/accounting/items/${row.id}` as never })}
          emptyState={
            <EmptyState
              icon={Package}
              title="No items yet"
              description="Add your first item to start selling, buying, or tracking stock."
              action={
                <Button asChild>
                  <Link to={'/accounting/items/new' as never}>
                    <Plus className="size-4" /> New item
                  </Link>
                </Button>
              }
            />
          }
        />
      </div>
    </>
  );
}
