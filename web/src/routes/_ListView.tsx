import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { Plus, Download, Filter, RefreshCw } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Button } from '@/components/Button';
import { DataTable } from '@/components/DataTable';
import { EmptyState } from '@/components/EmptyState';
import { Kbd } from '@/components/Kbd';
import { api } from '@/lib/api';
import type { DoctypeConfig } from '@/lib/doctypes';

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
  const { data, isLoading, isError, refetch, isFetching } = useQuery({
    queryKey: ['doctype', config.endpoint],
    queryFn: () => api<ListResponseShape>(config.endpoint),
  });
  const rows = (data?.items as any[] | undefined) ?? [];
  const newHref = config.newPath ?? `${config.modulePath}/${config.slug}/new`;

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
        subtitle={isError ? <span className="text-danger">Failed to load.</span> : undefined}
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
      <div className="flex-1 px-6 lg:px-8 pt-6 pb-8">
        <DataTable
          columns={config.columns}
          data={rows}
          loading={isLoading}
          searchPlaceholder={`Search ${config.title.toLowerCase()}…`}
          onRowClick={handleRowClick}
          emptyState={
            isError ? (
              <EmptyState
                icon={config.icon}
                title="Couldn't load this list"
                description="The server returned an error. Check the API logs or try again."
                action={<Button onClick={() => refetch()}><RefreshCw className="size-4" /> Retry</Button>}
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
