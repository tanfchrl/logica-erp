import { useMemo, useState } from 'react';
import {
  flexRender, getCoreRowModel, getFilteredRowModel, getSortedRowModel, useReactTable,
  type ColumnDef, type SortingState,
} from '@tanstack/react-table';
import { ArrowDown, ArrowUp, ArrowUpDown, Search } from 'lucide-react';
import { cn } from '@/lib/cn';
import { Input } from './Input';
import { Skeleton } from './EmptyState';

interface DataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[];
  data: TData[];
  loading?: boolean;
  searchPlaceholder?: string;
  searchableKeys?: (keyof TData)[];
  toolbar?: React.ReactNode;
  emptyState?: React.ReactNode;
  onRowClick?: (row: TData) => void;
}

export function DataTable<TData, TValue>({
  columns,
  data,
  loading,
  searchPlaceholder = 'Search…',
  toolbar,
  emptyState,
  onRowClick,
}: DataTableProps<TData, TValue>) {
  const [sorting, setSorting] = useState<SortingState>([]);
  const [globalFilter, setGlobalFilter] = useState('');

  const table = useReactTable({
    data,
    columns,
    state: { sorting, globalFilter },
    onSortingChange: setSorting,
    onGlobalFilterChange: setGlobalFilter,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    globalFilterFn: 'includesString',
  });

  const rows = table.getRowModel().rows;
  const empty = !loading && rows.length === 0;
  const placeholderRows = useMemo(() => Array.from({ length: 8 }, (_, i) => i), []);

  return (
    <div className="surface-card !p-0 overflow-hidden">
      {/* toolbar */}
      <div className="flex items-center gap-3 px-4 py-3 border-b border-border">
        <div className="relative w-72">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-text-tertiary pointer-events-none" />
          <Input
            value={globalFilter}
            onChange={(e) => setGlobalFilter(e.target.value)}
            placeholder={searchPlaceholder}
            className="pl-9"
          />
        </div>
        <div className="ml-auto flex items-center gap-2">{toolbar}</div>
      </div>

      {/* table */}
      <div className="overflow-x-auto">
        <table className="w-full text-dense">
          <thead className="sticky top-0 z-10 bg-bg-surface">
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id} className="border-b border-border">
                {hg.headers.map((header) => {
                  const sortable = header.column.getCanSort();
                  const sortDir = header.column.getIsSorted();
                  return (
                    <th
                      key={header.id}
                      className={cn(
                        'text-left font-medium text-text-secondary',
                        'px-4 py-2.5 align-middle whitespace-nowrap',
                        sortable && 'cursor-pointer select-none hover:text-text-primary',
                        (header.column.columnDef.meta as { align?: string } | undefined)?.align === 'right' && 'text-right',
                      )}
                      onClick={sortable ? header.column.getToggleSortingHandler() : undefined}
                    >
                      <span className="inline-flex items-center gap-1.5">
                        {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                        {sortable && (
                          sortDir === 'asc' ? <ArrowUp className="size-3" /> :
                          sortDir === 'desc' ? <ArrowDown className="size-3" /> :
                          <ArrowUpDown className="size-3 opacity-30" />
                        )}
                      </span>
                    </th>
                  );
                })}
              </tr>
            ))}
          </thead>
          <tbody>
            {loading && placeholderRows.map((i) => (
              <tr key={i} className="border-b border-border last:border-0">
                {table.getAllColumns().map((c) => (
                  <td key={c.id} className="px-4 py-2.5"><Skeleton className="h-3.5 w-3/5" /></td>
                ))}
              </tr>
            ))}

            {!loading && rows.map((row) => (
              <tr
                key={row.id}
                onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                className={cn(
                  'border-b border-border last:border-0 transition-colors',
                  'hover:bg-bg-subtle',
                  onRowClick && 'cursor-pointer',
                )}
              >
                {row.getVisibleCells().map((cell) => (
                  <td
                    key={cell.id}
                    className={cn(
                      'px-4 py-2.5 align-middle text-text-primary',
                      (cell.column.columnDef.meta as { align?: string } | undefined)?.align === 'right' && 'text-right num',
                    )}
                  >
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                ))}
              </tr>
            ))}

            {empty && (
              <tr>
                <td colSpan={table.getAllColumns().length} className="p-0">
                  {emptyState}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* footer */}
      <div className="flex items-center justify-between px-4 py-2.5 border-t border-border text-caption text-text-tertiary">
        <span>{loading ? 'Loading…' : `${rows.length} of ${data.length} rows`}</span>
        <span className="kbd">↑↓ navigate</span>
      </div>
    </div>
  );
}
