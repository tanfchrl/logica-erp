import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Upload, ChevronRight, AlertCircle, CheckCircle2, Scale, Package, ArrowDown, X } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { cn } from '@/lib/cn';
import { getAccessToken, getActiveCompany } from '@/lib/api';

/**
 * OpeningBalancesStep — Step 4 of the Migration Wizard.
 *
 * Two-phase flow:
 *   1. Upload trial balance CSV (columns: account_number, debit, credit,
 *      optional legacy_label). Optional posting_date.
 *   2. Backend computes the reconciliation proof:
 *        - total debits == total credits (the *only* gate-blocking check)
 *        - opening stock value matches the Inventory account balance
 *          (downgraded to a soft warning when the stock_ledger summary
 *           endpoint isn't reachable on this install)
 *      Each row is mapped to a current-COA account by account_number;
 *      unmapped rows surface as errors and block submit.
 *   3. On approve, the agent creates *and submits* a single Journal Entry.
 *      This is the only Tier-2 auto-submit flow in v1 (per spec §4 Step 4):
 *      opening-balance JEs are by construction a one-time setup action,
 *      and the user just visually approved the proof one card up.
 */

interface OBLine {
  account_number: string;
  legacy_label?: string;
  debit: string;
  credit: string;
  resolved_account_id?: string;
  resolved_account_name?: string;
  account_type?: string;
}

interface StockCheck {
  inventory_account_id: string;
  inventory_account_no: string;
  trial_balance_debit: string;
  stock_ledger_total: string;
  matches: boolean;
  difference?: string;
}

interface Proposal {
  lines: OBLine[];
  total_debit: string;
  total_credit: string;
  balanced: boolean;
  imbalance?: string;
  unmapped_accounts?: string[];
  stock_check?: StockCheck;
  posting_date: string;
}

interface Props { onContinue: () => void; sessionId: string }

async function agentFetch<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const co = getActiveCompany();
  if (co) headers['X-Company-Id'] = co;
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  const r = await fetch(path, {
    method: opts.method ?? 'GET', headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return t ? (JSON.parse(t) as T) : ({} as T);
}

const MAX_UPLOAD_BYTES = 50 * 1024 * 1024;

export function OpeningBalancesStep({ onContinue, sessionId }: Props) {
  const [lines, setLines] = useState<OBLine[]>([]);
  const [postingDate, setPostingDate] = useState('');
  const [fileName, setFileName] = useState('');
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [proposal, setProposal] = useState<Proposal | null>(null);
  const [submitted, setSubmitted] = useState<string | null>(null);

  const propose = useMutation({
    mutationFn: () => agentFetch<Proposal>(
      `/api/agent/v1/migration/${sessionId}/opening-balances/propose`,
      { method: 'POST', body: { lines, posting_date: postingDate } },
    ),
    onSuccess: (p) => setProposal(p),
  });

  const submit = useMutation({
    mutationFn: () => agentFetch<{ journal_entry_id: string }>(
      `/api/agent/v1/migration/${sessionId}/opening-balances/submit`,
      { method: 'POST' },
    ),
    onSuccess: (r) => setSubmitted(r.journal_entry_id),
  });

  function onFile(file: File) {
    setUploadError(null);
    if (file.size > MAX_UPLOAD_BYTES) {
      setUploadError(`File terlalu besar (${Math.round(file.size / 1024 / 1024)} MB). Maks 50 MB.`);
      return;
    }
    if (!/\.csv$/i.test(file.name)) {
      setUploadError('Hanya .csv yang didukung.');
      return;
    }
    const reader = new FileReader();
    reader.onload = (ev) => {
      const text = String(ev.target?.result ?? '');
      const parsed = parseTrialBalance(text);
      if (parsed.length === 0) {
        setUploadError('CSV kosong atau header tidak dikenali. Required headers: account_number, debit, credit.');
        return;
      }
      setLines(parsed);
      setFileName(file.name);
      setProposal(null); // re-propose on next click
    };
    reader.onerror = () => setUploadError('Gagal membaca file.');
    reader.readAsText(file);
  }

  // Compute totals locally for the upload-preview before sending to the
  // server. The server's totals are authoritative; this is just a hint.
  const localDebit  = lines.reduce((s, l) => s + Number(l.debit || 0), 0);
  const localCredit = lines.reduce((s, l) => s + Number(l.credit || 0), 0);
  const localBalanced = Math.abs(localDebit - localCredit) < 0.01;

  if (submitted) {
    return (
      <div>
        <h1 className="text-heading-3 text-ink mb-1">Step 4 · Opening Balances</h1>
        <Card className="border-l-4 border-l-brand-success">
          <div className="flex items-start gap-3">
            <CheckCircle2 className="size-5 text-brand-success shrink-0 mt-0.5" />
            <div>
              <CardTitle>Opening JE submitted</CardTitle>
              <CardDescription>
                Journal Entry <span className="font-mono">{submitted}</span> diposting ke GL.
                Saldo awal sekarang tercermin di laporan keuangan.
              </CardDescription>
            </div>
          </div>
        </Card>
        <div className="mt-4 flex justify-end">
          <Button onClick={onContinue}>
            Continue to Step 5 (Readiness) <ChevronRight className="size-3.5" />
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div>
      <h1 className="text-heading-3 text-ink mb-1">Step 4 · Opening Balances</h1>
      <p className="text-body text-stone mb-6">
        Upload trial balance dari sistem lama (CSV: <span className="font-mono">account_number,debit,credit</span>).
        Setelah lulus proof rekonsiliasi (debit = credit + stok ↔ Inventory), agent akan
        membuat dan submit satu Journal Entry — satu-satunya auto-submit yang diizinkan
        di v1, karena saldo awal sifatnya satu kali setup dan baru saja Anda review.
      </p>

      {/* Phase 1: Upload */}
      <Card>
        <CardTitle>Step 4.1 · Upload trial balance</CardTitle>
        <CardDescription>
          CSV dengan kolom <span className="font-mono">account_number</span>,
          <span className="font-mono"> debit</span>,
          <span className="font-mono"> credit</span>.
          Optional <span className="font-mono">legacy_label</span>.
        </CardDescription>

        <div className="mt-4 grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Field label="Posting date (defaults to today)">
            <Input
              type="date"
              value={postingDate}
              onChange={(e) => setPostingDate(e.target.value)}
            />
          </Field>
        </div>

        <label className="mt-4 block border border-dashed border-hairline rounded-lg px-6 py-8 text-center cursor-pointer hover:border-accent/60 hover:bg-accent/[0.03] transition-colors">
          <input type="file" accept=".csv" className="sr-only" onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) onFile(f);
          }} />
          <Upload className="size-6 mx-auto text-stone mb-2" />
          <div className="text-body-sm text-ink">
            {fileName ? <>Loaded: <span className="font-mono">{fileName}</span> · {lines.length} lines</> : 'Klik untuk pilih CSV'}
          </div>
          <div className="text-caption text-stone mt-1">Maks 50 MB</div>
        </label>

        {uploadError && (
          <div className="mt-3 text-caption text-brand-error flex items-start gap-1.5">
            <AlertCircle className="size-3.5 shrink-0 mt-0.5" /><span>{uploadError}</span>
          </div>
        )}

        {lines.length > 0 && (
          <div className="mt-4 flex items-center justify-between rounded-md border border-hairline bg-surface-soft px-3 py-2">
            <div className="text-caption text-stone">
              Local check (pre-server): debit <span className="font-mono">{localDebit.toLocaleString('id-ID')}</span> · credit{' '}
              <span className="font-mono">{localCredit.toLocaleString('id-ID')}</span>
            </div>
            <span className={cn('text-caption font-medium', localBalanced ? 'text-brand-success' : 'text-warning')}>
              {localBalanced ? 'balanced ✓' : 'imbalanced'}
            </span>
          </div>
        )}

        <div className="mt-4 flex justify-end">
          <Button onClick={() => propose.mutate()} disabled={lines.length === 0 || propose.isPending} loading={propose.isPending}>
            Run reconciliation proof <ChevronRight className="size-3.5" />
          </Button>
        </div>
        {propose.error && (
          <div className="mt-2 text-caption text-brand-error">{(propose.error as Error).message}</div>
        )}
      </Card>

      {/* Reconciliation proof report */}
      {proposal && (
        <div className="mt-4 space-y-4">
          <ProofCard p={proposal} />
          <UnmappedCard p={proposal} />
          {proposal.stock_check && <StockCheckCard c={proposal.stock_check} />}
          <LinesTable p={proposal} />

          <div className="flex items-center justify-between">
            <div className="text-caption text-stone">
              {proposal.balanced
                ? (proposal.unmapped_accounts && proposal.unmapped_accounts.length > 0)
                  ? `${proposal.unmapped_accounts.length} unmapped account(s) block submit — fix the CSV and re-upload.`
                  : 'Reconciliation passed. Submit creates a JE and posts to GL.'
                : 'Reconciliation failed. Fix the source data and re-upload.'}
            </div>
            <Button
              disabled={
                !proposal.balanced
                || (proposal.unmapped_accounts?.length ?? 0) > 0
                || (proposal.stock_check ? !proposal.stock_check.matches : false)
                || submit.isPending
              }
              loading={submit.isPending}
              onClick={() => submit.mutate()}
            >
              Submit opening JE <ChevronRight className="size-3.5" />
            </Button>
          </div>
          {submit.error && (
            <div className="text-caption text-brand-error">{(submit.error as Error).message}</div>
          )}
        </div>
      )}
    </div>
  );
}

function ProofCard({ p }: { p: Proposal }) {
  return (
    <Card className={cn('border-l-4', p.balanced ? 'border-l-brand-success' : 'border-l-brand-error')}>
      <div className="flex items-start justify-between">
        <div>
          <CardTitle>
            <span className="inline-flex items-center gap-1.5">
              <Scale className="size-4" /> Debit = Credit
            </span>
          </CardTitle>
          <CardDescription>
            Total debit <span className="font-mono">{Number(p.total_debit).toLocaleString('id-ID')}</span> ·
            total credit <span className="font-mono">{Number(p.total_credit).toLocaleString('id-ID')}</span>
            {!p.balanced && p.imbalance && <> · imbalance <span className="font-mono text-brand-error">{Number(p.imbalance).toLocaleString('id-ID')}</span></>}
          </CardDescription>
        </div>
        {p.balanced
          ? <CheckCircle2 className="size-5 text-brand-success" />
          : <X className="size-5 text-brand-error" />}
      </div>
    </Card>
  );
}

function UnmappedCard({ p }: { p: Proposal }) {
  if (!p.unmapped_accounts || p.unmapped_accounts.length === 0) return null;
  return (
    <Card className="border-l-4 border-l-warning">
      <div className="flex items-start gap-3">
        <AlertCircle className="size-5 text-warning shrink-0 mt-0.5" />
        <div>
          <CardTitle>{p.unmapped_accounts.length} account number(s) not in your COA</CardTitle>
          <CardDescription>
            These rows reference numbers Logica doesn't know about:
            <span className="ml-2 font-mono">{p.unmapped_accounts.join(', ')}</span>.
            Either add them to the Chart of Accounts (Step 2) or fix the CSV, then re-upload.
          </CardDescription>
        </div>
      </div>
    </Card>
  );
}

function StockCheckCard({ c }: { c: StockCheck }) {
  return (
    <Card className={cn('border-l-4', c.matches ? 'border-l-brand-success' : 'border-l-warning')}>
      <div className="flex items-start justify-between">
        <div>
          <CardTitle>
            <span className="inline-flex items-center gap-1.5">
              <Package className="size-4" /> Stock ↔ Inventory account
            </span>
          </CardTitle>
          <CardDescription>
            TB Inventory debit <span className="font-mono">{Number(c.trial_balance_debit).toLocaleString('id-ID')}</span>
            <ArrowDown className="inline size-3 mx-1" />
            stock ledger total <span className="font-mono">{Number(c.stock_ledger_total).toLocaleString('id-ID')}</span>
            {!c.matches && c.difference && <> · diff <span className="font-mono text-warning">{Number(c.difference).toLocaleString('id-ID')}</span></>}
          </CardDescription>
        </div>
        {c.matches
          ? <CheckCircle2 className="size-5 text-brand-success" />
          : <AlertCircle className="size-5 text-warning" />}
      </div>
    </Card>
  );
}

function LinesTable({ p }: { p: Proposal }) {
  return (
    <Card padded={false}>
      <div className="px-5 py-3 border-b border-hairline">
        <CardTitle>Lines · {p.lines.length}</CardTitle>
      </div>
      <div className="max-h-[300px] overflow-y-auto">
        <table className="w-full text-body-sm">
          <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone sticky top-0">
            <tr>
              <th className="text-left font-medium px-3 py-2">Account No.</th>
              <th className="text-left font-medium px-3 py-2">Resolved</th>
              <th className="text-right font-medium px-3 py-2">Debit</th>
              <th className="text-right font-medium px-3 py-2">Credit</th>
            </tr>
          </thead>
          <tbody>
            {p.lines.map((l, i) => (
              <tr key={i} className="border-t border-hairline">
                <td className="px-3 py-1.5 font-mono text-caption">{l.account_number}</td>
                <td className="px-3 py-1.5">
                  {l.resolved_account_name
                    ? <span className="text-ink">{l.resolved_account_name}</span>
                    : <span className="text-brand-error">unmapped</span>}
                </td>
                <td className="px-3 py-1.5 text-right font-mono">{l.debit !== '0' ? Number(l.debit).toLocaleString('id-ID') : '—'}</td>
                <td className="px-3 py-1.5 text-right font-mono">{l.credit !== '0' ? Number(l.credit).toLocaleString('id-ID') : '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

// parseTrialBalance reads a CSV with the documented header set. Tolerant of
// header case + whitespace. Returns [] if the required columns aren't found.
function parseTrialBalance(text: string): OBLine[] {
  const rows = splitCSV(text);
  if (rows.length < 2) return [];
  const headers = rows[0]!.map((h) => h.trim().toLowerCase());
  const idx = (k: string) => headers.indexOf(k);
  const i_no    = idx('account_number');
  const i_d     = idx('debit');
  const i_c     = idx('credit');
  const i_label = idx('legacy_label');
  if (i_no < 0 || i_d < 0 || i_c < 0) return [];
  const out: OBLine[] = [];
  for (let r = 1; r < rows.length; r++) {
    const row = rows[r]!;
    const no = (row[i_no] ?? '').trim();
    if (!no) continue;
    out.push({
      account_number: no,
      legacy_label:   i_label >= 0 ? (row[i_label] ?? '').trim() : '',
      debit:          (row[i_d] ?? '0').trim() || '0',
      credit:         (row[i_c] ?? '0').trim() || '0',
    });
  }
  return out;
}

// Minimal CSV splitter — same approach as DataMigrationStep. Quoted fields,
// "" → " escape, CRLF tolerant. Not full RFC 4180.
function splitCSV(text: string): string[][] {
  const out: string[][] = [];
  let cur: string[] = [];
  let field = '';
  let inQ = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inQ) {
      if (c === '"' && text[i + 1] === '"') { field += '"'; i++; }
      else if (c === '"') inQ = false;
      else field += c;
    } else if (c === '"') inQ = true;
    else if (c === ',') { cur.push(field); field = ''; }
    else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++;
      cur.push(field); field = '';
      if (cur.some((v) => v !== '')) out.push(cur);
      cur = [];
    } else {
      field += c;
    }
  }
  if (field !== '' || cur.length > 0) {
    cur.push(field);
    if (cur.some((v) => v !== '')) out.push(cur);
  }
  return out;
}
