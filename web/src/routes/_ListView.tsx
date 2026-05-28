import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { Plus, Download, Filter, RefreshCw } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Button } from '@/components/Button';
import { DataTable } from '@/components/DataTable';
import { EmptyState } from '@/components/EmptyState';
import { Kbd } from '@/components/Kbd';
import { SavedViewsBar, type SavedView } from '@/components/SavedViews';
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

  // Default row click: open the doctype detail page at /{module}/{slug}/{id}.
  // Callers can pass their own onRowClick to override (e.g. open a side panel).
  const handleRowClick = onRowClick ?? ((row: any) => {
    const id = row?.id;
    if (id) void navigate({ to: `${config.modulePath}/${config.slug}/${id}` as never });
  });

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
            <Button variant="secondary"><Filter className="size-4" /> Filter</Button>
            <Button variant="secondary"><Download className="size-4" /> Export</Button>
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
