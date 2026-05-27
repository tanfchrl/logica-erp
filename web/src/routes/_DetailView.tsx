import { useQuery } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import { ArrowLeft, Pencil, AlertCircle, Calendar } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Timeline } from '@/components/Timeline';
import { api } from '@/lib/api';
import { me } from '@/lib/auth';
import { money, date } from '@/lib/format';
import { cn } from '@/lib/cn';
import type { DoctypeConfig } from '@/lib/doctypes';
import type { CreateSchema, FieldDef } from '@/lib/createSchema';

/**
 * Generic doctype detail view — ERPNext-style read-only page driven by the
 * DoctypeConfig + the existing create schema (we reuse the field list with
 * its labels and kinds so each doctype's detail rendering is correct
 * without a separate schema).
 *
 * Layout: two columns on wide screens.
 *   Left  — field key/value rows grouped into one card per ~8 fields.
 *   Right — meta strip (created/updated dates) + status pill + actions.
 *
 * For bespoke doctypes (sales_invoice, journal_entry, purchase_invoice) the
 * detail page is the form in read-only mode — they get their own routes and
 * aren't routed through here.
 */

export interface DetailViewProps {
  config: DoctypeConfig;
  schema: CreateSchema;
}

export function DetailView({ config, schema }: DetailViewProps) {
  const { id } = useParams({ strict: false }) as { id?: string };

  const { data, isLoading, error } = useQuery({
    queryKey: ['doctype-detail', config.endpoint, id],
    queryFn:  () => api<Record<string, unknown>>(`${config.endpoint}/${id}`),
    enabled:  !!id,
  });

  const listPath = `${config.modulePath}/${config.slug}`;
  const recordTitle = data ? pickTitle(data, config) : '…';

  return (
    <>
      <PageHeader
        crumbs={[
          { label: config.module, to: config.modulePath },
          { label: config.title, to: listPath },
          { label: recordTitle },
        ]}
        title={recordTitle}
        status={renderDocstatus(data)}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={listPath as never}>
                <ArrowLeft className="size-4" /> Back to {config.title}
              </Link>
            </Button>
            <Button variant="secondary" asChild>
              <Link to={`${listPath}/${id}/edit` as never}>
                <Pencil className="size-4" /> Edit
              </Link>
            </Button>
          </>
        }
      />

      <div className="flex-1 px-6 lg:px-8 pt-6 pb-8">
        <div className="grid grid-cols-1 lg:grid-cols-[1fr,320px] gap-4 max-w-[1400px]">
          <div className="space-y-4 min-w-0">
            {error && (
              <Card className="!p-3 bg-brand-error/5 border-brand-error/30 flex items-start gap-2">
                <AlertCircle className="size-4 text-brand-error mt-0.5 shrink-0" />
                <span className="text-body-sm text-brand-error">{(error as Error).message}</span>
              </Card>
            )}

            {isLoading ? (
              <Card><Skeleton className="h-64 w-full" /></Card>
            ) : data ? (
              <FieldsCard fields={schema.fields} record={data} />
            ) : null}

            {/* Child tables: render any array-of-objects field as a flat table. */}
            {data && renderChildTables(data)}
          </div>

          {/* Right rail: meta + raw payload viewer + activity timeline */}
          {data && (
            <div className="space-y-4">
              <MetaRail record={data} />
              {id && <Timeline doctype={config.doctype} documentId={id} />}
            </div>
          )}
        </div>
      </div>
    </>
  );
}

/* ---------- helpers ---------- */

function FieldsCard({ fields, record }: { fields: FieldDef[]; record: Record<string, unknown> }) {
  return (
    <Card>
      <CardTitle>Details</CardTitle>
      <div className="mt-4 grid sm:grid-cols-2 gap-x-6 gap-y-3">
        {fields.map((f) => (
          <DetailRow key={f.name} field={f} value={record[f.name]} className={f.span === 2 ? 'sm:col-span-2' : ''} />
        ))}
      </div>
    </Card>
  );
}

function DetailRow({ field, value, className }: { field: FieldDef; value: unknown; className?: string }) {
  return (
    <div className={cn('space-y-0.5', className)}>
      <div className="text-micro-uppercase text-stone">{field.label}</div>
      <div className="text-body-sm text-ink break-words">{formatValue(field, value)}</div>
    </div>
  );
}

function formatValue(field: FieldDef, v: unknown): React.ReactNode {
  if (v === null || v === undefined || v === '') return <span className="text-stone">—</span>;
  switch (field.kind) {
    case 'money':
      return <span className="num">{money(String(v))}</span>;
    case 'number':
      return <span className="num">{String(v)}</span>;
    case 'date':
      return <span className="num">{date(String(v))}</span>;
    case 'bool':
      return v ? <StatusPill tone="success" withDot={false}>Yes</StatusPill>
               : <StatusPill tone="neutral" withDot={false}>No</StatusPill>;
    case 'textarea':
      return <span className="whitespace-pre-wrap">{String(v)}</span>;
    case 'link':
      // Detail rows don't have access to the linked record; show id with a
      // hint. Bespoke detail pages can override this with their own joins.
      return <span className="font-mono text-caption text-charcoal">{String(v)}</span>;
    default:
      return <span>{String(v)}</span>;
  }
}

function pickTitle(record: Record<string, unknown>, config: DoctypeConfig): string {
  // Prefer the most "human" field for the page title.
  for (const k of ['name', 'display_name', 'item_code', 'code', 'account_name', 'asset_name', 'subject']) {
    if (typeof record[k] === 'string' && record[k]) return record[k] as string;
  }
  return (record.id as string) ?? config.singular;
}

function renderDocstatus(record: Record<string, unknown> | undefined): React.ReactNode {
  const ds = record?.docstatus;
  if (ds === undefined || ds === null) return null;
  if (ds === 1) return <StatusPill tone="success">Submitted</StatusPill>;
  if (ds === 2) return <StatusPill tone="danger">Cancelled</StatusPill>;
  return <StatusPill tone="neutral">Draft</StatusPill>;
}

function renderChildTables(record: Record<string, unknown>): React.ReactNode {
  // Any field whose value is an array of objects is treated as a child table.
  const tables = Object.entries(record).filter(([, v]) =>
    Array.isArray(v) && v.length > 0 && typeof v[0] === 'object' && v[0] !== null,
  ) as Array<[string, Record<string, unknown>[]]>;
  if (tables.length === 0) return null;
  return tables.map(([key, rows]) => {
    const columns = Object.keys(rows[0]!).filter(
      (k) => !['id', '$schema'].includes(k) && rows[0]![k] !== null && typeof rows[0]![k] !== 'object',
    );
    return (
      <Card key={key} padded={false}>
        <div className="px-5 py-3 border-b border-hairline">
          <CardTitle>{prettifyKey(key)}</CardTitle>
          <CardDescription>{rows.length} {rows.length === 1 ? 'row' : 'rows'}</CardDescription>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-body-sm">
            <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone">
              <tr>
                {columns.map((c) => (
                  <th key={c} className="text-left font-medium px-3 py-2">{prettifyKey(c)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i} className="border-t border-hairline">
                  {columns.map((c) => (
                    <td key={c} className="px-3 py-2 text-charcoal align-top">
                      {formatCell(c, r[c])}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    );
  });
}

function formatCell(key: string, v: unknown): React.ReactNode {
  if (v === null || v === undefined || v === '') return <span className="text-stone">—</span>;
  if (typeof v === 'boolean') return v ? 'Yes' : 'No';
  if (typeof v === 'number') return <span className="num">{v}</span>;
  // Heuristics for common keys
  if (/_amount$|_total$|amount$|rate$|^debit$|^credit$/i.test(key)) {
    return <span className="num">{money(String(v))}</span>;
  }
  if (/_at$|_date$/.test(key)) {
    return <span className="num">{date(String(v))}</span>;
  }
  if (typeof v === 'string' && v.startsWith('acc_')) return <span className="font-mono text-caption">{v}</span>;
  return <span>{String(v)}</span>;
}

function prettifyKey(k: string): string {
  return k.replace(/_/g, ' ').replace(/\b\w/g, (m) => m.toUpperCase());
}

function MetaRail({ record }: { record: Record<string, unknown> }) {
  // Raw-JSON panel is only useful to operators — gate it behind is_system so
  // regular users see a clean Meta card and nothing more.
  const { data: caller } = useQuery({ queryKey: ['me'], queryFn: () => me() });
  const created = record.created_at as string | undefined;
  const updated = record.updated_at as string | undefined;
  return (
    <div className="space-y-4">
      <Card>
        <CardTitle>Meta</CardTitle>
        <div className="mt-3 space-y-2 text-body-sm">
          <Row label="ID" value={String(record.id ?? '—')} mono />
          {created && <Row label="Created" value={date(created)} icon={Calendar} />}
          {updated && <Row label="Updated" value={date(updated)} icon={Calendar} />}
        </div>
      </Card>
      {caller?.is_system && (
        <details className="rounded-lg border border-hairline bg-canvas">
          <summary className="cursor-pointer px-4 py-3 text-body-sm text-charcoal">Raw JSON</summary>
          <pre className="text-caption font-mono text-charcoal px-4 pb-4 overflow-auto max-h-[400px]">
            {JSON.stringify(record, null, 2)}
          </pre>
        </details>
      )}
    </div>
  );
}

function Row({ label, value, mono, icon: Icon }: { label: string; value: string; mono?: boolean; icon?: React.ComponentType<{ className?: string }> }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-stone inline-flex items-center gap-1.5">{Icon ? <Icon className="size-3" /> : null}{label}</span>
      <span className={'text-right ' + (mono ? 'font-mono text-caption text-charcoal' : 'text-ink')}>{value}</span>
    </div>
  );
}
