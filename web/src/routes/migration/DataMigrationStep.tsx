import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Upload, ChevronRight, AlertCircle, CheckCircle2, FileSpreadsheet, X } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field } from '@/components/Input';
import { Combobox } from '@/components/Combobox';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';

/**
 * DataMigrationStep — Step 3 of the Migration Wizard.
 *
 * Three sub-screens:
 *   1. Pick doctype + upload CSV (max 50 MB, .csv only for v1)
 *   2. Map CSV columns → doctype fields (auto-matched on load)
 *   3. Validate (POST /admin/imports/preview), surface Data Quality Report,
 *      then commit (POST /admin/imports/commit)
 *
 * Staged data lives in the agent_session.state["data_migration"] blob so a
 * user can reload mid-flow. Commit is one server-side transaction per row;
 * any errors land in the report and don't block other rows.
 */

interface FieldDef {
  key: string;
  label: string;
  type: string;             // text | number | bool | date | link | enum
  required: boolean;
  description?: string;
  lookup_hint?: string;
  options?: string[];
}
interface Recipe {
  doctype: string;
  label: string;
  description?: string;
  company_scoped: boolean;
  fields: FieldDef[];
}
interface RecipesResp { items: Recipe[] }

interface RowResult {
  row_no: number;
  status: 'ok' | 'error';
  name?: string;
  message?: string;
  fields?: Record<string, string>;
}
interface ImportJob {
  id: string;
  doctype: string;
  total_rows: number;
  success_rows: number;
  error_rows: number;
  status: string;
}
interface ImportResp { job: ImportJob; results: RowResult[] }

interface Props {
  onContinue: () => void;
}

type Phase = 'upload' | 'map' | 'commit';

const MAX_UPLOAD_BYTES = 50 * 1024 * 1024;

export function DataMigrationStep({ onContinue }: Props) {
  const { data: recipesResp } = useQuery({
    queryKey: ['import-recipes'],
    queryFn:  () => api<RecipesResp>('/admin/imports/recipes'),
  });

  const [phase, setPhase] = useState<Phase>('upload');
  const [doctype, setDoctype] = useState<string | null>(null);
  const [fileName, setFileName] = useState('');
  // Raw rows from the CSV. Keys = CSV headers verbatim.
  const [rows, setRows] = useState<Record<string, string>[]>([]);
  const [headers, setHeaders] = useState<string[]>([]);
  // Mapping: CSV header → field key. Empty value means "skip this column".
  const [mapping, setMapping] = useState<Record<string, string>>({});
  // Preview / commit result.
  const [report, setReport] = useState<ImportResp | null>(null);

  const recipe = useMemo(
    () => recipesResp?.items.find((r) => r.doctype === doctype) ?? null,
    [recipesResp, doctype],
  );

  function reset() {
    setPhase('upload');
    setDoctype(null);
    setFileName('');
    setRows([]);
    setHeaders([]);
    setMapping({});
    setReport(null);
  }

  return (
    <div>
      <h1 className="text-heading-3 text-ink mb-1">Step 3 · Data Migration</h1>
      <p className="text-body text-stone mb-6">
        Upload CSV data from your legacy system, map columns to fields, lihat
        Data Quality Report, lalu commit. Setiap baris dijalankan dalam
        transaksinya sendiri — error pada satu baris tidak menghalangi yang lain.
      </p>

      <PhaseTracker phase={phase} />

      {phase === 'upload' && (
        <UploadPhase
          recipes={recipesResp?.items ?? []}
          doctype={doctype}
          onPickDoctype={setDoctype}
          onLoaded={({ headers, rows, fileName, autoMapping }) => {
            setHeaders(headers);
            setRows(rows);
            setFileName(fileName);
            setMapping(autoMapping);
            setPhase('map');
          }}
        />
      )}

      {phase === 'map' && recipe && (
        <MapPhase
          recipe={recipe}
          fileName={fileName}
          rows={rows}
          headers={headers}
          mapping={mapping}
          onChangeMapping={setMapping}
          onBack={() => setPhase('upload')}
          onPreviewed={(r) => {
            setReport(r);
            setPhase('commit');
          }}
        />
      )}

      {phase === 'commit' && recipe && report && (
        <CommitPhase
          recipe={recipe}
          fileName={fileName}
          headers={headers}
          rows={rows}
          mapping={mapping}
          report={report}
          onBack={() => setPhase('map')}
          onReset={reset}
          onContinue={onContinue}
        />
      )}
    </div>
  );
}

/* ─── Phase tracker chip row ─── */

function PhaseTracker({ phase }: { phase: Phase }) {
  const order: Phase[] = ['upload', 'map', 'commit'];
  const labels: Record<Phase, string> = {
    upload: '1. Upload',
    map:    '2. Map columns',
    commit: '3. Review & commit',
  };
  const idx = order.indexOf(phase);
  return (
    <div className="flex items-center gap-2 mb-4">
      {order.map((p, i) => (
        <span
          key={p}
          className={cn(
            'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-caption border',
            i < idx && 'bg-brand-success/10 border-brand-success/30 text-brand-success',
            i === idx && 'bg-accent/10 border-accent/30 text-accent font-medium',
            i > idx && 'bg-surface border-hairline text-stone',
          )}
        >
          {labels[p]}
        </span>
      ))}
    </div>
  );
}

/* ─── Phase 1: Upload ─── */

function UploadPhase({
  recipes, doctype, onPickDoctype, onLoaded,
}: {
  recipes: Recipe[];
  doctype: string | null;
  onPickDoctype: (d: string | null) => void;
  onLoaded: (r: { headers: string[]; rows: Record<string, string>[]; fileName: string; autoMapping: Record<string, string> }) => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const recipe = recipes.find((r) => r.doctype === doctype) ?? null;

  function onFile(file: File) {
    setError(null);
    if (file.size > MAX_UPLOAD_BYTES) {
      setError(`File terlalu besar (${Math.round(file.size / 1024 / 1024)} MB). Maks 50 MB.`);
      return;
    }
    if (!/\.csv$/i.test(file.name)) {
      setError('Hanya .csv yang didukung untuk saat ini. (XLSX support menyusul.)');
      return;
    }
    const reader = new FileReader();
    reader.onload = (ev) => {
      const text = String(ev.target?.result ?? '');
      const parsed = parseCSV(text);
      if (parsed.headers.length === 0) {
        setError('CSV kosong atau header tidak terbaca.');
        return;
      }
      if (!recipe) {
        setError('Pilih doctype dulu sebelum upload.');
        return;
      }
      const autoMapping = autoMatchMapping(parsed.headers, recipe.fields);
      onLoaded({
        headers: parsed.headers,
        rows: parsed.rows,
        fileName: file.name,
        autoMapping,
      });
    };
    reader.onerror = () => setError('Gagal membaca file.');
    reader.readAsText(file);
  }

  const opts = useMemo(
    () => recipes.map((r) => ({ value: r.doctype, label: r.label, description: r.description })),
    [recipes],
  );

  return (
    <Card>
      <CardTitle>Step 3.1 · Pick doctype + upload CSV</CardTitle>
      <CardDescription>Tipe data yang akan diimpor, lalu file CSV-nya.</CardDescription>

      <div className="mt-4 space-y-4">
        <Field label="Doctype">
          <Combobox
            options={opts}
            value={doctype}
            onChange={(v) => onPickDoctype(v)}
            placeholder="Pilih doctype…"
          />
        </Field>

        {recipe && (
          <div className="text-caption text-stone">
            <span className="font-mono">{recipe.fields.filter((f) => f.required).length}</span> required fields:
            {' '}
            <span className="font-mono">
              {recipe.fields.filter((f) => f.required).map((f) => f.key).join(', ')}
            </span>
          </div>
        )}

        <label
          className={cn(
            'block border border-dashed rounded-lg px-6 py-10 text-center cursor-pointer transition-colors',
            doctype ? 'border-hairline hover:border-accent/60 hover:bg-accent/[0.03]' : 'border-hairline opacity-50 cursor-not-allowed',
          )}
        >
          <input
            type="file"
            accept=".csv"
            disabled={!doctype}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) onFile(f);
            }}
            className="sr-only"
          />
          <Upload className="size-6 mx-auto text-stone mb-2" />
          <div className="text-body-sm text-ink">Klik atau drop a CSV file here</div>
          <div className="text-caption text-stone mt-1">Maks 50 MB · UTF-8 · header di baris pertama</div>
        </label>

        {error && (
          <div className="text-caption text-brand-error flex items-start gap-1.5">
            <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        )}
      </div>
    </Card>
  );
}

/* ─── Map columns ─── */

function MapPhase({
  recipe, fileName, rows, headers, mapping, onChangeMapping, onBack, onPreviewed,
}: {
  recipe: Recipe;
  fileName: string;
  rows: Record<string, string>[];
  headers: string[];
  mapping: Record<string, string>;
  onChangeMapping: (m: Record<string, string>) => void;
  onBack: () => void;
  onPreviewed: (r: ImportResp) => void;
}) {
  const preview = useMutation({
    mutationFn: () => api<ImportResp>('/admin/imports/preview', {
      method: 'POST',
      // Backend expects rows as string[][] with row[0]=headers — see
      // internal/platform/dataimport/dataimport.go impApplyInput.
      body: { doctype: recipe.doctype, mapping, rows: rowsToWireFormat(headers, rows) },
    }),
    onSuccess: (r) => onPreviewed(r),
  });

  const fieldOpts = useMemo(() => {
    return [
      { value: '', label: '— Skip this column —' },
      ...recipe.fields.map((f) => ({
        value: f.key,
        label: f.label + (f.required ? ' *' : ''),
        description: f.description,
      })),
    ];
  }, [recipe]);

  // Detect missing required mappings to surface inline.
  const mappedFields = new Set(Object.values(mapping).filter(Boolean));
  const missingRequired = recipe.fields.filter((f) => f.required && !mappedFields.has(f.key));

  return (
    <Card>
      <CardTitle>Step 3.2 · Map columns → fields</CardTitle>
      <CardDescription>
        <FileSpreadsheet className="size-3 inline mr-1" />
        {fileName} · {rows.length} rows · {headers.length} columns
      </CardDescription>

      <div className="mt-4 grid grid-cols-1 lg:grid-cols-[1fr,auto,1fr] gap-y-1 gap-x-3 items-center max-w-2xl">
        <div className="text-caption text-stone font-semibold">CSV column</div>
        <div />
        <div className="text-caption text-stone font-semibold">→ Field</div>
        {headers.map((h) => (
          <div key={h} className="contents">
            <span className="text-body-sm text-charcoal font-mono truncate">{h}</span>
            <ChevronRight className="size-3.5 text-stone" />
            <Combobox
              options={fieldOpts}
              value={mapping[h] ?? ''}
              onChange={(v) => onChangeMapping({ ...mapping, [h]: v ?? '' })}
              placeholder="Skip"
            />
          </div>
        ))}
      </div>

      {missingRequired.length > 0 && (
        <div className="mt-4 text-caption text-warning bg-warning/5 border border-warning/30 rounded-md px-3 py-2 flex items-start gap-2">
          <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
          <div>
            Required fields not yet mapped:{' '}
            <span className="font-mono">{missingRequired.map((f) => f.key).join(', ')}</span>
          </div>
        </div>
      )}

      <div className="mt-5 flex items-center justify-between">
        <Button variant="ghost" onClick={onBack}>← Back</Button>
        <Button
          onClick={() => preview.mutate()}
          loading={preview.isPending}
          disabled={missingRequired.length > 0}
        >
          Validate &amp; preview <ChevronRight className="size-3.5" />
        </Button>
      </div>
      {preview.error && (
        <div className="mt-3 text-caption text-brand-error">{(preview.error as Error).message}</div>
      )}
    </Card>
  );
}

/* ─── Phase 3: Report + commit ─── */

function CommitPhase({
  recipe, fileName, headers, rows, mapping, report, onBack, onReset, onContinue,
}: {
  recipe: Recipe;
  fileName: string;
  headers: string[];
  rows: Record<string, string>[];
  mapping: Record<string, string>;
  report: ImportResp;
  onBack: () => void;
  onReset: () => void;
  onContinue: () => void;
}) {
  const [filter, setFilter] = useState<'all' | 'error'>('all');
  const [committed, setCommitted] = useState<ImportResp | null>(null);

  const commit = useMutation({
    mutationFn: () => api<ImportResp>('/admin/imports/commit', {
      method: 'POST',
      body: { doctype: recipe.doctype, mapping, rows: rowsToWireFormat(headers, rows) },
    }),
    onSuccess: (r) => setCommitted(r),
  });

  const view = committed ?? report;
  const visible = filter === 'error' ? view.results.filter((r) => r.status === 'error') : view.results;
  const errCount = view.results.filter((r) => r.status === 'error').length;
  const okCount = view.results.filter((r) => r.status === 'ok').length;
  const isCommitted = !!committed;

  return (
    <div className="space-y-4">
      <Card className={cn('border-l-4', errCount === 0 ? 'border-l-brand-success' : 'border-l-warning')}>
        <div className="flex items-start justify-between">
          <div>
            <CardTitle>
              {isCommitted ? 'Committed' : 'Preview'} · {recipe.label}
            </CardTitle>
            <CardDescription>
              <FileSpreadsheet className="size-3 inline mr-1" />
              {fileName} · {view.job.total_rows} rows · {okCount} ok · {errCount} errors
            </CardDescription>
          </div>
          {isCommitted ? (
            <CheckCircle2 className="size-5 text-brand-success" />
          ) : (
            <span className="text-caption text-stone">dry run</span>
          )}
        </div>
      </Card>

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => setFilter('all')}
          className={cn('px-2.5 py-1 rounded-full border text-caption',
            filter === 'all' ? 'bg-canvas border-accent text-accent font-medium' : 'bg-surface border-hairline text-stone')}
        >
          All {view.results.length}
        </button>
        <button
          type="button"
          onClick={() => setFilter('error')}
          className={cn('px-2.5 py-1 rounded-full border text-caption',
            filter === 'error' ? 'bg-canvas border-brand-error text-brand-error font-medium' : 'bg-surface border-hairline text-stone')}
        >
          Errors {errCount}
        </button>
      </div>

      <Card padded={false}>
        <div className="max-h-[420px] overflow-y-auto">
          <table className="w-full text-body-sm">
            <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone sticky top-0">
              <tr>
                <th className="text-left font-medium px-3 py-2">Row</th>
                <th className="text-left font-medium px-3 py-2">Status</th>
                <th className="text-left font-medium px-3 py-2">Name</th>
                <th className="text-left font-medium px-3 py-2">Message</th>
              </tr>
            </thead>
            <tbody>
              {visible.length === 0 && (
                <tr>
                  <td colSpan={4} className="text-center px-3 py-6 text-stone">No rows match this filter.</td>
                </tr>
              )}
              {visible.map((r) => (
                <tr key={r.row_no} className="border-t border-hairline">
                  <td className="px-3 py-2 font-mono text-caption text-stone">{r.row_no}</td>
                  <td className="px-3 py-2">
                    {r.status === 'ok' ? (
                      <span className="inline-flex items-center gap-1 text-brand-success">
                        <CheckCircle2 className="size-3" /> ok
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-brand-error">
                        <X className="size-3" /> error
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-charcoal">{r.name ?? '—'}</td>
                  <td className="px-3 py-2 text-stone">{r.message ?? ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      <div className="flex items-center justify-between gap-3">
        <Button variant="ghost" onClick={onBack} disabled={isCommitted}>← Back to mapping</Button>
        {!isCommitted ? (
          <Button onClick={() => commit.mutate()} loading={commit.isPending}>
            Commit {okCount} rows <ChevronRight className="size-3.5" />
          </Button>
        ) : (
          <div className="flex items-center gap-2">
            <Button variant="secondary" onClick={onReset}>Import another file</Button>
            <Button onClick={onContinue}>
              Continue to Step 4 <ChevronRight className="size-3.5" />
            </Button>
          </div>
        )}
      </div>
      {commit.error && (
        <div className="text-caption text-brand-error">{(commit.error as Error).message}</div>
      )}
    </div>
  );
}

/* ─── CSV parsing ─── */

// rowsToWireFormat converts the local object-row representation back to the
// header + rows[][]string shape the /admin/imports endpoints expect. The
// header row is row 0 — same convention as the original CSV.
function rowsToWireFormat(headers: string[], rows: Record<string, string>[]): string[][] {
  return [headers, ...rows.map((r) => headers.map((h) => r[h] ?? ''))];
}

// parseCSV handles the common case: header row, comma-separated, optional
// double-quoted fields, "" → " escape. Not a full RFC 4180 — that's overkill
// for the wizard. The /admin/imports/preview endpoint is the source of truth
// for validation; any rows we mis-parse will surface as errors there.
function parseCSV(text: string): { headers: string[]; rows: Record<string, string>[] } {
  const lines: string[][] = [];
  let cur: string[] = [];
  let field = '';
  let inQ = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inQ) {
      if (c === '"' && text[i + 1] === '"') { field += '"'; i++; }
      else if (c === '"') { inQ = false; }
      else { field += c; }
    } else if (c === '"') {
      inQ = true;
    } else if (c === ',') {
      cur.push(field); field = '';
    } else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++;
      cur.push(field); field = '';
      // Only push non-empty lines (skip trailing blank line).
      if (cur.some((v) => v !== '')) lines.push(cur);
      cur = [];
    } else {
      field += c;
    }
  }
  // Tail
  if (field !== '' || cur.length > 0) {
    cur.push(field);
    if (cur.some((v) => v !== '')) lines.push(cur);
  }
  if (lines.length === 0) return { headers: [], rows: [] };
  const headers = lines[0]!.map((h) => h.trim());
  const rows = lines.slice(1).map((line) => {
    const obj: Record<string, string> = {};
    for (let i = 0; i < headers.length; i++) {
      obj[headers[i]!] = (line[i] ?? '').trim();
    }
    return obj;
  });
  return { headers, rows };
}

// autoMatchMapping pairs CSV headers to field keys by case-insensitive
// match against (key, label, snake_case(label)). Anything that doesn't
// match cleanly gets an empty string — user picks via the dropdown.
function autoMatchMapping(headers: string[], fields: FieldDef[]): Record<string, string> {
  const out: Record<string, string> = {};
  const norm = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '');
  const byNorm = new Map<string, string>();
  for (const f of fields) {
    byNorm.set(norm(f.key), f.key);
    byNorm.set(norm(f.label), f.key);
  }
  for (const h of headers) {
    out[h] = byNorm.get(norm(h)) ?? '';
  }
  return out;
}
