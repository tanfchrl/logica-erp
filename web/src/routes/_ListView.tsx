import { useMemo, useState } from 'react';
import * as Popover from '@radix-ui/react-popover';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { Plus, Download, Filter, RefreshCw, X } from 'lucide-react';
import type { ColumnDef, ColumnFiltersState } from '@tanstack/react-table';
import { PageHeader } from '@/components/PageHeader';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { DataTable } from '@/components/DataTable';
import { EmptyState } from '@/components/EmptyState';
import { Kbd } from '@/components/Kbd';
import { SavedViewsBar, type SavedView } from '@/components/SavedViews';
import { CustomizeFieldsButton } from '@/components/CustomizeFieldsButton';
import { StarMenuButton } from '@/components/StarMenuButton';
import { api } from '@/lib/api';
import type { DoctypeConfig } from '@/lib/doctypes';

// Shape we persist as saved_view.body for table-style lists. Kept small
// for v1 — searchText is by far the highest-value piece.
interface ListViewBody {
  searchText?: string;
}

interface ListResponseShape {
  items: unknown[];
}

interface ListViewProps {
  config: DoctypeConfig;
  /** Override toolbar (e.g. custom export buttons). Replaces the default toolbar. */
  extraActions?: React.ReactNode;
  /** Optional callback when a row is clicked. */
  onRowClick?: (row: any) => void;
}

/**
 * Generic list page driven by a DoctypeConfig.
 * Handles loading, empty, error, list rendering, and standard toolbar.
 */
export function ListView({ config, extraActions, onRowClick }: ListViewProps) {
  const navigate = useNavigate();
  const { data, isLoading, isError, error, refetch, isFetching } = useQuery({
    queryKey: ['doctype', config.endpoint],
    queryFn: () => api<ListResponseShape>(config.endpoint),
    // Don't retry 403/401 — they're "no access", not a transient failure.
    retry: (count, err) => {
      const s = (err as { status?: number })?.status ?? 0;
      if (s === 401 || s === 403 || s === 404) return false;
      return count < 2;
    },
  });
  const rows = (data?.items as any[] | undefined) ?? [];
  const forbidden = (error as { status?: number })?.status === 403;
  const newHref = config.newPath ?? `${config.modulePath}/${config.slug}/new`;

  // Saved-views state. The active view's body drives searchText; otherwise
  // the user owns it.
  const [searchText, setSearchText] = useState('');
  const [activeViewId, setActiveViewId] = useState<string | null>(null);
  const onSelectView = (v: SavedView<ListViewBody> | null) => {
    setActiveViewId(v?.id ?? null);
    setSearchText(v?.body?.searchText ?? '');
  };

  // Column-level "contains" filters surfaced by the Filter popover.
  // tanstack-react-table applies these on top of the global search.
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);
  const filterableColumns = useMemo(
    () => listFilterableColumns(config.columns),
    [config.columns],
  );
  const activeFilterCount = columnFilters.filter((f) => String(f.value ?? '').trim() !== '').length;

  // Default row click: open the doctype detail page at /{module}/{slug}/{id}.
  // Callers can pass their own onRowClick to override (e.g. open a side panel).
  const handleRowClick = onRowClick ?? ((row: any) => {
    const id = row?.id;
    if (id) void navigate({ to: `${config.modulePath}/${config.slug}/${id}` as never });
  });

  function handleExport() {
    if (rows.length === 0) return;
    const csv = rowsToCSV(rows, config.columns);
    downloadCSV(`${config.slug}-${todayStamp()}.csv`, csv);
  }

  return (
    <>
      <PageHeader
        crumbs={[{ label: config.module, to: config.modulePath }, { label: config.title }]}
        title={config.title}
        subtitle={isError ? (
          <span className="text-danger">
            {forbidden ? "You don't have access to this menu." : 'Failed to load.'}
          </span>
        ) : undefined}
        actions={
          <>
            <Button variant="ghost" size="icon" onClick={() => refetch()} aria-label="Refresh">
              <RefreshCw className={`size-4 ${isFetching ? 'animate-spin' : ''}`} />
            </Button>
            <FilterPopover
              columns={filterableColumns}
              value={columnFilters}
              onChange={setColumnFilters}
              activeCount={activeFilterCount}
            />
            <Button
              variant="secondary"
              onClick={handleExport}
              disabled={rows.length === 0}
              title={rows.length === 0 ? 'Nothing to export' : `Export ${rows.length} rows as CSV`}
            >
              <Download className="size-4" /> Export
            </Button>
            <StarMenuButton
              path={`${config.modulePath}/${config.slug}`}
              label={config.title}
            />
            <CustomizeFieldsButton doctype={config.doctype} />
            {extraActions}
            {config.hasNew !== false && (
              <Button asChild>
                <Link to={newHref as never}>
                  <Plus className="size-4" /> New {config.singular}
                </Link>
              </Button>
            )}
          </>
        }
      />
      <div className="flex-1 px-6 lg:px-8 pt-3 pb-8 space-y-3">
        <div className="flex items-center gap-2 flex-wrap">
          <SavedViewsBar<ListViewBody>
            doctype={config.doctype}
            currentBody={{ searchText: searchText || undefined }}
            activeViewId={activeViewId}
            onSelectView={onSelectView}
          />
        </div>
        <DataTable
          columns={config.columns}
          data={rows}
          loading={isLoading}
          searchPlaceholder={`Search ${config.title.toLowerCase()}…`}
          onRowClick={handleRowClick}
          globalFilter={searchText}
          onGlobalFilterChange={setSearchText}
          columnFilters={columnFilters}
          onColumnFiltersChange={setColumnFilters}
          emptyState={
            isError ? (
              <EmptyState
                icon={config.icon}
                title={forbidden ? "You don't have access to this menu" : "Couldn't load this list"}
                description={forbidden
                  ? `Your role doesn't include read permission for ${config.title.toLowerCase()}. Ask an administrator to grant access in Settings → Roles.`
                  : 'The server returned an error. Check the API logs or try again.'}
                action={forbidden ? undefined : (
                  <Button onClick={() => refetch()}><RefreshCw className="size-4" /> Retry</Button>
                )}
              />
            ) : (
              <EmptyState
                icon={config.icon}
                title={`No ${config.title.toLowerCase()} yet`}
                description={`Create your first ${config.singular.toLowerCase()} to populate this list.`}
                action={
                  config.hasNew !== false ? (
                    <Button asChild>
                      <Link to={newHref as never}><Plus className="size-4" /> New {config.singular}</Link>
                    </Button>
                  ) : (
                    <span className="text-caption text-text-tertiary inline-flex items-center gap-1">
                      Create via API <Kbd>⌘K</Kbd>
                    </span>
                  )
                }
              />
            )
          }
        />
      </div>
    </>
  );
}

// ---- Filter popover ----------------------------------------------------

interface FilterableColumn { id: string; header: string }

function FilterPopover({
  columns, value, onChange, activeCount,
}: {
  columns: FilterableColumn[];
  value: ColumnFiltersState;
  onChange: (next: ColumnFiltersState) => void;
  activeCount: number;
}) {
  const valueFor = (id: string) =>
    String(value.find((f) => f.id === id)?.value ?? '');
  const setFilter = (id: string, raw: string) => {
    const others = value.filter((f) => f.id !== id);
    if (raw.trim() === '') onChange(others);
    else onChange([...others, { id, value: raw }]);
  };
  const clearAll = () => onChange([]);

  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <Button variant="secondary">
          <Filter className="size-4" /> Filter
          {activeCount > 0 && (
            <span className="ml-1 inline-flex items-center justify-center min-w-5 h-5 px-1 rounded-full bg-accent text-canvas text-caption font-medium">
              {activeCount}
            </span>
          )}
        </Button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          align="end"
          sideOffset={6}
          className="z-50 w-[340px] surface-card !p-0 max-h-[60vh] overflow-y-auto"
        >
          <div className="px-3 py-2.5 border-b border-hairline flex items-center justify-between">
            <span className="text-body-sm font-medium text-ink">Filter</span>
            {activeCount > 0 ? (
              <button
                type="button"
                onClick={clearAll}
                className="text-caption text-stone hover:text-ink inline-flex items-center gap-1"
              >
                <X className="size-3" /> Clear all
              </button>
            ) : (
              <span className="text-caption text-stone">Contains, case-insensitive</span>
            )}
          </div>
          {columns.length === 0 ? (
            <div className="px-3 py-4 text-caption text-stone">
              No filterable columns on this list.
            </div>
          ) : (
            <div className="p-3 space-y-2">
              {columns.map((c) => (
                <div key={c.id}>
                  <label className="text-caption text-stone block mb-1">{c.header}</label>
                  <Input
                    value={valueFor(c.id)}
                    onChange={(e) => setFilter(c.id, e.target.value)}
                    placeholder={`Contains…`}
                    className="!h-8"
                  />
                </div>
              ))}
            </div>
          )}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function listFilterableColumns(cols: ColumnDef<any, any>[]): FilterableColumn[] {
  const out: FilterableColumn[] = [];
  for (const c of cols) {
    const accessor = (c as any).accessorKey as string | undefined;
    const id = ((c as any).id as string | undefined) ?? accessor;
    if (!id || !accessor) continue;            // skip JSX-only columns ('flags' etc.)
    const header = (c as any).header;
    if (typeof header !== 'string') continue;  // skip render-fn headers
    out.push({ id, header });
  }
  return out;
}

// ---- CSV export --------------------------------------------------------

function rowsToCSV(rows: any[], cols: ColumnDef<any, any>[]): string {
  const exportable = cols
    .map((c) => {
      const accessor = (c as any).accessorKey as string | undefined;
      const header   = (c as any).header;
      if (!accessor || typeof header !== 'string') return null;
      return { accessor, header };
    })
    .filter((x): x is { accessor: string; header: string } => x !== null);

  const headerLine = exportable.map((c) => csvCell(c.header)).join(',');
  const lines = rows.map((r) =>
    exportable.map((c) => csvCell(extract(r, c.accessor))).join(','),
  );
  return [headerLine, ...lines].join('\r\n');
}

function extract(row: any, accessor: string): unknown {
  // Support dot-paths just in case ('foo.bar') — keeps the helper honest
  // for any future nested accessor.
  if (!accessor.includes('.')) return row?.[accessor];
  return accessor.split('.').reduce<any>((acc, k) => (acc == null ? acc : acc[k]), row);
}

function csvCell(value: unknown): string {
  if (value === null || value === undefined) return '';
  const s = typeof value === 'object' ? JSON.stringify(value) : String(value);
  // Excel quote rule: if the cell contains ", , \r, or \n, wrap in
  // double-quotes and escape embedded quotes by doubling them.
  if (/[",\r\n]/.test(s)) return `"${s.replace(/"/g, '""')}"`;
  return s;
}

function downloadCSV(filename: string, csv: string) {
  // Prepend UTF-8 BOM so Excel opens it without mangling Indonesian
  // characters or any non-ASCII content.
  const blob = new Blob(['﻿', csv], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  // Give the browser a tick to start the download before revoking.
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function todayStamp(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}`;
}
