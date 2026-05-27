import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Database, Upload, ArrowRight, ArrowLeft, CheckCircle2, AlertCircle,
  Sparkles, Download, RotateCcw, FileText, FileSpreadsheet,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

/* ---- types ---- */

interface FieldDef {
  key: string;
  label: string;
  required: boolean;
  type: string;
  description?: string;
  options?: string[];
  lookup_hint?: string;
}
interface Recipe {
  doctype: string;
  label: string;
  description?: string;
  company_scoped: boolean;
  fields: FieldDef[];
}
interface RecipeList { items: Recipe[] }

interface RowResult {
  row_no: number;
  status: 'ok' | 'error';
  name?: string;
  message?: string;
  fields?: Record<string, string>;
}
interface ApplyResponse {
  job: { id: string; total_rows: number; success_rows: number; error_rows: number; status: string };
  results: RowResult[];
}

interface ImportJob {
  id: string;
  doctype: string;
  company_id?: string;
  total_rows: number;
  success_rows: number;
  error_rows: number;
  status: string;
  created_at: string;
}
interface JobList { items: ImportJob[] }

type Step = 1 | 2 | 3 | 4 | 5;

export function ImportExportSection() {
  const { data: recipes, isLoading } = useQuery({
    queryKey: ['import-recipes'],
    queryFn:  () => api<RecipeList>('/admin/imports/recipes'),
  });
  const { data: jobs, refetch: refetchJobs } = useQuery({
    queryKey: ['import-jobs'],
    queryFn:  () => api<JobList>('/admin/imports/jobs?limit=10'),
  });

  // Wizard state.
  const [step, setStep]         = useState<Step>(1);
  const [recipe, setRecipe]     = useState<Recipe | null>(null);
  const [csvName, setCsvName]   = useState<string>('');
  const [rows, setRows]         = useState<string[][]>([]);   // row 0 = header
  const [mapping, setMapping]   = useState<Record<string, string>>({}); // csv col → field key
  const [preview, setPreview]   = useState<ApplyResponse | null>(null);
  const [commit, setCommit]     = useState<ApplyResponse | null>(null);

  function resetWizard() {
    setStep(1); setRecipe(null); setCsvName(''); setRows([]); setMapping({});
    setPreview(null); setCommit(null);
  }

  return (
    <div className="space-y-5">
      <div>
        <CardTitle>Import &amp; export</CardTitle>
        <CardDescription>
          Bulk-load masters from CSV. Each row is its own micro-transaction, so partial success is captured —
          good rows commit even when some fail.
        </CardDescription>
      </div>

      {/* Stepper */}
      <Stepper current={step} />

      {step === 1 && (
        <StepChooseDoctype
          recipes={recipes?.items ?? []}
          isLoading={isLoading}
          onPick={(r) => { setRecipe(r); setStep(2); }}
        />
      )}
      {step === 2 && recipe && (
        <StepUpload
          recipe={recipe}
          onParsed={(name, parsed) => { setCsvName(name); setRows(parsed); setMapping(autoMap(parsed[0] ?? [], recipe)); setStep(3); }}
          onBack={() => setStep(1)}
        />
      )}
      {step === 3 && recipe && (
        <StepMap
          recipe={recipe}
          rows={rows}
          mapping={mapping}
          setMapping={setMapping}
          onBack={() => setStep(2)}
          onNext={() => setStep(4)}
        />
      )}
      {step === 4 && recipe && (
        <StepPreview
          recipe={recipe} rows={rows} mapping={mapping}
          preview={preview} setPreview={setPreview}
          onBack={() => setStep(3)}
          onNext={() => setStep(5)}
        />
      )}
      {step === 5 && recipe && (
        <StepCommit
          recipe={recipe} rows={rows} mapping={mapping}
          commit={commit} setCommit={setCommit}
          onBack={() => setStep(4)}
          onDone={() => { void refetchJobs(); resetWizard(); }}
        />
      )}

      {/* Recent jobs */}
      <section>
        <div className="mb-2 flex items-baseline justify-between">
          <CardTitle>Recent imports</CardTitle>
          {csvName && <span className="text-caption text-stone">Wizard file: {csvName}</span>}
        </div>
        {(jobs?.items ?? []).length === 0 ? (
          <Card>
            <div className="text-center py-6 text-body-sm text-stone">
              <Database className="mx-auto size-5 mb-2" /> No imports yet.
            </div>
          </Card>
        ) : (
          <Card padded={false}>
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft border-b border-hairline">
                <tr className="text-micro-uppercase text-stone">
                  <th className="text-left  font-medium px-4 py-2.5">When</th>
                  <th className="text-left  font-medium px-4 py-2.5">Doctype</th>
                  <th className="text-right font-medium px-4 py-2.5">Total</th>
                  <th className="text-right font-medium px-4 py-2.5">OK</th>
                  <th className="text-right font-medium px-4 py-2.5">Errors</th>
                </tr>
              </thead>
              <tbody>
                {jobs!.items.map((j) => (
                  <tr key={j.id} className="border-b border-hairline last:border-0">
                    <td className="px-4 py-2 text-stone num whitespace-nowrap">{new Date(j.created_at).toLocaleString('id-ID')}</td>
                    <td className="px-4 py-2 text-charcoal font-mono text-caption">{j.doctype}</td>
                    <td className="px-4 py-2 text-right num text-ink">{j.total_rows}</td>
                    <td className="px-4 py-2 text-right num text-success">{j.success_rows}</td>
                    <td className={cn('px-4 py-2 text-right num', j.error_rows > 0 ? 'text-brand-error' : 'text-stone')}>{j.error_rows}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        )}
      </section>
    </div>
  );
}

/* ----------------------- step indicator ----------------------- */

const STEPS: { n: Step; label: string }[] = [
  { n: 1, label: 'Choose doctype' },
  { n: 2, label: 'Upload CSV' },
  { n: 3, label: 'Map columns' },
  { n: 4, label: 'Validate' },
  { n: 5, label: 'Commit' },
];

function Stepper({ current }: { current: Step }) {
  return (
    <div className="flex items-center gap-2 flex-wrap">
      {STEPS.map((s, i) => {
        const done   = s.n < current;
        const active = s.n === current;
        return (
          <div key={s.n} className="flex items-center gap-2">
            <div className={cn(
              'inline-flex items-center justify-center size-7 rounded-full text-caption font-medium border',
              done   ? 'bg-success text-white border-success' :
              active ? 'bg-primary text-primary-fg border-primary' :
                       'bg-canvas text-stone border-hairline',
            )}>
              {done ? <CheckCircle2 className="size-3.5" /> : s.n}
            </div>
            <span className={cn('text-body-sm', active ? 'text-ink font-medium' : 'text-stone')}>{s.label}</span>
            {i < STEPS.length - 1 && <ArrowRight className="size-3 text-stone mx-1" />}
          </div>
        );
      })}
    </div>
  );
}

/* ----------------------- step 1: choose doctype ----------------------- */

function StepChooseDoctype({
  recipes, isLoading, onPick,
}: { recipes: Recipe[]; isLoading: boolean; onPick: (r: Recipe) => void }) {
  if (isLoading) return <Card><Skeleton className="h-32 w-full" /></Card>;
  return (
    <Card>
      <CardTitle>What are you importing?</CardTitle>
      <CardDescription className="mb-3">Pick the doctype the CSV's rows describe.</CardDescription>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        {recipes.map((r) => (
          <button key={r.doctype} type="button" onClick={() => onPick(r)}
            className="text-left border border-hairline rounded-lg p-3.5 hover:border-ink transition-colors bg-canvas">
            <div className="flex items-start gap-3">
              <span className="size-9 rounded-md bg-surface text-ink inline-flex items-center justify-center shrink-0">
                <FileSpreadsheet className="size-4" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-body-sm font-medium text-ink">{r.label}</span>
                  {r.company_scoped && <StatusPill tone="info" withDot={false}>Per company</StatusPill>}
                </div>
                <div className="text-caption text-stone mt-0.5">{r.description}</div>
                <div className="text-caption text-stone mt-1.5 font-mono">{r.fields.length} fields · {r.doctype}</div>
              </div>
            </div>
          </button>
        ))}
      </div>
    </Card>
  );
}

/* ----------------------- step 2: upload ----------------------- */

function StepUpload({
  recipe, onParsed, onBack,
}: { recipe: Recipe; onParsed: (name: string, rows: string[][]) => void; onBack: () => void }) {
  const [error, setError] = useState<string | null>(null);

  function downloadTemplate() {
    const header = recipe.fields.map((f) => f.key).join(',');
    const example = recipe.fields.map((f) => sampleFor(f)).join(',');
    const csv = `${header}\n${example}\n`;
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${recipe.doctype}_template.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function pickFile(file: File) {
    if (file.size > 5 * 1024 * 1024) { setError('File too large (>5 MB).'); return; }
    const text = await file.text();
    try {
      const rows = parseCSV(text);
      if (rows.length < 2) { setError('CSV must have a header and at least one data row.'); return; }
      setError(null);
      onParsed(file.name, rows);
    } catch (e) {
      setError(`Could not parse CSV: ${(e as Error).message}`);
    }
  }

  return (
    <Card>
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Upload CSV for {recipe.label}</CardTitle>
          <CardDescription>
            First row must be the header. Comma-delimited. Up to 5 MB.
          </CardDescription>
        </div>
        <Button variant="secondary" size="sm" onClick={downloadTemplate}>
          <Download className="size-3.5" /> Download template
        </Button>
      </div>

      <div className="mt-4 border-2 border-dashed border-hairline rounded-lg p-6 text-center bg-surface-soft hover:border-stone/40 transition-colors">
        <Upload className="mx-auto size-6 text-stone" />
        <div className="mt-2 text-body-sm text-charcoal">Drop a CSV here, or</div>
        <label className="inline-block mt-2">
          <input type="file" accept=".csv,text/csv" className="hidden"
            onChange={(e) => { const f = e.target.files?.[0]; if (f) void pickFile(f); e.target.value = ''; }} />
          <span className="inline-flex items-center gap-1.5 h-9 px-4 rounded-full bg-primary text-primary-fg text-body-sm cursor-pointer hover:bg-primary-pressed transition-colors">
            <FileText className="size-3.5" /> Browse files
          </span>
        </label>
      </div>

      {error && (
        <div className="mt-4 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
          <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
        </div>
      )}

      <div className="mt-4 pt-3 border-t border-hairline">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="size-3.5" /> Back
        </Button>
      </div>
    </Card>
  );
}

/* ----------------------- step 3: map columns ----------------------- */

function StepMap({
  recipe, rows, mapping, setMapping, onBack, onNext,
}: {
  recipe: Recipe;
  rows: string[][];
  mapping: Record<string, string>;
  setMapping: (m: Record<string, string>) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  const header = rows[0] ?? [];
  const sample = rows.slice(1, 4);

  const required = recipe.fields.filter((f) => f.required).map((f) => f.key);
  const mappedFieldSet = new Set(Object.values(mapping));
  const missing = required.filter((k) => !mappedFieldSet.has(k));

  return (
    <Card>
      <CardTitle>Map CSV columns to fields</CardTitle>
      <CardDescription className="mb-3">
        {header.length} columns detected. Required fields must be mapped before validation.
      </CardDescription>

      <div className="overflow-x-auto">
        <table className="w-full text-body-sm">
          <thead className="bg-surface-soft border-b border-hairline">
            <tr className="text-micro-uppercase text-stone">
              <th className="text-left font-medium px-3 py-2">CSV column</th>
              <th className="text-left font-medium px-3 py-2">Sample values</th>
              <th className="text-left font-medium px-3 py-2">Maps to</th>
            </tr>
          </thead>
          <tbody>
            {header.map((col, idx) => {
              const value = mapping[col] ?? '';
              return (
                <tr key={idx} className="border-b border-hairline last:border-0">
                  <td className="px-3 py-2 text-ink font-mono text-caption">{col}</td>
                  <td className="px-3 py-2 text-stone">
                    {sample.map((r, ri) => (
                      <div key={ri} className="truncate max-w-[260px]">{r[idx] || <span className="opacity-50">(empty)</span>}</div>
                    ))}
                  </td>
                  <td className="px-3 py-2">
                    <select
                      className="input-base !h-8 !text-[13px]"
                      value={value}
                      onChange={(e) => setMapping({ ...mapping, [col]: e.target.value })}
                    >
                      <option value="">— Ignore —</option>
                      {recipe.fields.map((f) => (
                        <option key={f.key} value={f.key}>
                          {f.label}{f.required ? ' *' : ''}
                        </option>
                      ))}
                    </select>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {missing.length > 0 && (
        <div className="mt-4 rounded-md bg-warning/10 text-warning text-caption px-3 py-2 inline-flex items-start gap-2">
          <AlertCircle className="size-4 mt-0.5 shrink-0" />
          Required fields still unmapped: <span className="font-mono">{missing.join(', ')}</span>
        </div>
      )}

      <div className="mt-4 pt-3 border-t border-hairline flex items-center justify-between">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="size-3.5" /> Back
        </Button>
        <Button size="sm" onClick={onNext} disabled={missing.length > 0}>
          Validate <ArrowRight className="size-3.5" />
        </Button>
      </div>
    </Card>
  );
}

/* ----------------------- step 4: preview ----------------------- */

function StepPreview({
  recipe, rows, mapping, preview, setPreview, onBack, onNext,
}: {
  recipe: Recipe;
  rows: string[][];
  mapping: Record<string, string>;
  preview: ApplyResponse | null;
  setPreview: (p: ApplyResponse) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  const mut = useMutation({
    mutationFn: () => api<ApplyResponse>('/admin/imports/preview', {
      method: 'POST',
      body: { doctype: recipe.doctype, mapping, rows },
    }),
    onSuccess: (r) => setPreview(r),
  });

  useEffect(() => { if (!preview) mut.mutate(); /* run once on enter */ }, []); // eslint-disable-line

  if (mut.isPending || !preview) {
    return (
      <Card>
        <div className="py-6 text-center text-stone">
          <Sparkles className="mx-auto size-5 mb-2" /> Validating {rows.length - 1} rows…
        </div>
      </Card>
    );
  }

  const { results } = preview;
  const okCount  = results.filter((r) => r.status === 'ok').length;
  const errCount = results.length - okCount;
  const errors   = results.filter((r) => r.status === 'error').slice(0, 50);

  return (
    <Card>
      <div className="flex items-end justify-between gap-3 flex-wrap mb-3">
        <div>
          <CardTitle>Validation preview</CardTitle>
          <CardDescription>
            {okCount} rows pass, {errCount} have errors. No data has been written.
          </CardDescription>
        </div>
        <Button variant="secondary" size="sm" onClick={() => mut.mutate()} loading={mut.isPending}>
          <RotateCcw className="size-3.5" /> Re-validate
        </Button>
      </div>

      <div className="grid grid-cols-3 gap-3 mb-4">
        <SummaryTile tone="neutral" label="Total"   value={results.length} />
        <SummaryTile tone="success" label="Will create" value={okCount} />
        <SummaryTile tone={errCount > 0 ? 'danger' : 'neutral'} label="Errors" value={errCount} />
      </div>

      {errCount > 0 && (
        <div>
          <div className="text-caption text-stone mb-2">First {Math.min(errors.length, 50)} errors:</div>
          <div className="rounded-lg border border-hairline overflow-hidden">
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft border-b border-hairline">
                <tr className="text-micro-uppercase text-stone">
                  <th className="text-left font-medium px-3 py-2 w-[80px]">Row</th>
                  <th className="text-left font-medium px-3 py-2">Identifier</th>
                  <th className="text-left font-medium px-3 py-2">Error</th>
                </tr>
              </thead>
              <tbody>
                {errors.map((r) => (
                  <tr key={r.row_no} className="border-b border-hairline last:border-0">
                    <td className="px-3 py-2 text-stone num">{r.row_no}</td>
                    <td className="px-3 py-2 text-charcoal font-mono text-caption">{r.name || '—'}</td>
                    <td className="px-3 py-2 text-brand-error">{r.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <div className="mt-4 pt-3 border-t border-hairline flex items-center justify-between">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="size-3.5" /> Back to mapping
        </Button>
        <Button size="sm" onClick={onNext} disabled={okCount === 0}>
          {errCount > 0 ? `Commit ${okCount} good rows` : `Commit ${okCount} rows`} <ArrowRight className="size-3.5" />
        </Button>
      </div>
    </Card>
  );
}

/* ----------------------- step 5: commit ----------------------- */

function StepCommit({
  recipe, rows, mapping, commit, setCommit, onBack, onDone,
}: {
  recipe: Recipe;
  rows: string[][];
  mapping: Record<string, string>;
  commit: ApplyResponse | null;
  setCommit: (c: ApplyResponse) => void;
  onBack: () => void;
  onDone: () => void;
}) {
  const mut = useMutation({
    mutationFn: () => api<ApplyResponse>('/admin/imports/commit', {
      method: 'POST',
      body: { doctype: recipe.doctype, mapping, rows },
    }),
    onSuccess: (r) => setCommit(r),
  });

  useEffect(() => { if (!commit && !mut.isPending) mut.mutate(); }, []); // eslint-disable-line

  if (mut.isPending || !commit) {
    return (
      <Card>
        <div className="py-6 text-center text-stone">
          <Sparkles className="mx-auto size-5 mb-2 animate-pulse" /> Committing {rows.length - 1} rows…
        </div>
      </Card>
    );
  }

  const { results, job } = commit;
  const okCount  = results.filter((r) => r.status === 'ok').length;
  const errCount = results.length - okCount;
  const errors   = results.filter((r) => r.status === 'error').slice(0, 50);

  return (
    <Card>
      <div className="flex items-end justify-between gap-3 flex-wrap mb-3">
        <div>
          <CardTitle>Import complete</CardTitle>
          <CardDescription>
            Job <span className="font-mono text-ink">{job.id}</span> · {okCount} succeeded, {errCount} failed.
          </CardDescription>
        </div>
        {errCount === 0
          ? <StatusPill tone="success"><CheckCircle2 className="size-3" /> All clean</StatusPill>
          : <StatusPill tone="warning">{errCount} errors</StatusPill>}
      </div>

      <div className="grid grid-cols-3 gap-3 mb-4">
        <SummaryTile tone="neutral" label="Total"     value={results.length} />
        <SummaryTile tone="success" label="Committed" value={okCount} />
        <SummaryTile tone={errCount > 0 ? 'danger' : 'neutral'} label="Errors" value={errCount} />
      </div>

      {errCount > 0 && (
        <details className="rounded-lg border border-hairline">
          <summary className="px-3 py-2 cursor-pointer text-body-sm text-charcoal bg-surface-soft">
            Errors ({errors.length} shown)
          </summary>
          <table className="w-full text-body-sm">
            <tbody>
              {errors.map((r) => (
                <tr key={r.row_no} className="border-t border-hairline">
                  <td className="px-3 py-2 text-stone num w-[80px]">{r.row_no}</td>
                  <td className="px-3 py-2 text-charcoal font-mono text-caption">{r.name || '—'}</td>
                  <td className="px-3 py-2 text-brand-error">{r.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}

      <div className="mt-4 pt-3 border-t border-hairline flex items-center justify-between">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="size-3.5" /> Back
        </Button>
        <Button size="sm" onClick={onDone}>Start another import</Button>
      </div>
    </Card>
  );
}

/* ----------------------- shared ----------------------- */

function SummaryTile({ label, value, tone }: { label: string; value: number; tone: 'success' | 'danger' | 'neutral' }) {
  const cls = tone === 'success' ? 'text-success' : tone === 'danger' ? 'text-brand-error' : 'text-ink';
  return (
    <div className="rounded-lg border border-hairline bg-canvas p-4">
      <div className="text-micro-uppercase text-stone">{label}</div>
      <div className={cn('mt-1 text-heading-3 num', cls)}>{value.toLocaleString('id-ID')}</div>
    </div>
  );
}

/* ----------------------- CSV parsing ----------------------- */

/** Tiny CSV parser with quoted-field support (RFC 4180-ish). */
function parseCSV(text: string): string[][] {
  const out: string[][] = [];
  let row: string[] = [];
  let cur = '';
  let inQuotes = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inQuotes) {
      if (c === '"' && text[i + 1] === '"') { cur += '"'; i++; }
      else if (c === '"') inQuotes = false;
      else cur += c;
    } else {
      if (c === '"') inQuotes = true;
      else if (c === ',') { row.push(cur); cur = ''; }
      else if (c === '\r') { /* skip */ }
      else if (c === '\n') { row.push(cur); cur = ''; out.push(row); row = []; }
      else cur += c;
    }
  }
  if (cur !== '' || row.length > 0) { row.push(cur); out.push(row); }
  // Trim trailing empty rows.
  while (out.length && out[out.length - 1]!.every((c) => c === '')) out.pop();
  return out;
}

/** Auto-pair CSV columns to fields when names match (case- and underscore-insensitive). */
function autoMap(header: string[], recipe: Recipe): Record<string, string> {
  const m: Record<string, string> = {};
  const norm = (s: string) => s.toLowerCase().replace(/[^a-z0-9]/g, '');
  const byNorm = new Map(recipe.fields.map((f) => [norm(f.key), f.key]));
  // Also accept label match.
  for (const f of recipe.fields) byNorm.set(norm(f.label), f.key);
  for (const col of header) {
    const k = byNorm.get(norm(col));
    if (k) m[col] = k;
  }
  return m;
}

function sampleFor(f: FieldDef): string {
  if (f.options && f.options.length) return f.options[0]!;
  if (f.type === 'bool')   return 'false';
  if (f.type === 'number') return '0';
  if (f.type === 'email')  return 'user@example.com';
  if (f.key === 'npwp')    return '0000000000000000';
  return '';
}
