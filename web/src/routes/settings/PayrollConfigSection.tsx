import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Save, Plus, AlertCircle, Calculator } from 'lucide-react';
// (Save icon used in the modal save button below)
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface PayrollSetting {
  id: string; company_id: string; effective_from: string;
  bpjs_kesehatan_employer: string; bpjs_kesehatan_employee: string; bpjs_kesehatan_cap: string;
  bpjs_jht_employer: string; bpjs_jht_employee: string;
  bpjs_jp_employer: string; bpjs_jp_employee: string; bpjs_jp_cap: string;
  bpjs_jkk_employer: string; bpjs_jkm_employer: string;
  pph21_ter: { category: string; brackets: { max?: number; rate: number }[] }[] | null;
  updated_at: string;
}
interface SettingList { items: PayrollSetting[] }

const SAMPLE_TER: PayrollSetting['pph21_ter'] = [
  { category: 'A', brackets: [
    { max: 5400000, rate: 0 },
    { max: 5650000, rate: 0.0025 },
    { max: 5950000, rate: 0.005 },
    { max: 6300000, rate: 0.0075 },
    { max: 6750000, rate: 0.01 },
    { rate: 0.34 },
  ]},
];

export function PayrollConfigSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['payroll-settings'], queryFn: () => api<SettingList>('/admin/payroll-settings') });
  const items = data?.items ?? [];
  const latest = items[0] ?? null;
  const [editing, setEditing] = useState<PayrollSetting | 'new' | null>(null);

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Payroll configuration</CardTitle>
          <CardDescription>
            BPJS rates and PPh21 TER tables. Indonesian DJP updates the TER brackets annually — versioned by effective date so historical payroll runs use the right rates.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setEditing('new')}>
          <Plus className="size-3.5" /> New effective date
        </Button>
      </div>

      {isLoading ? <Card><Skeleton className="h-32 w-full" /></Card> :
       items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Calculator className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No payroll setting for this company yet.</div>
            <Button size="sm" className="mt-4" onClick={() => setEditing('new')}>
              <Plus className="size-3.5" /> Create
            </Button>
          </div>
        </Card>
       ) : (
        <>
          {latest && <SettingSummary setting={latest} onEdit={() => setEditing(latest)} />}
          {items.length > 1 && (
            <div>
              <div className="text-micro-uppercase text-stone mb-2">History</div>
              <Card padded={false}>
                <ul className="divide-y divide-hairline">
                  {items.slice(1).map((s) => (
                    <li key={s.id} className="px-4 py-2.5 flex items-center justify-between">
                      <span className="text-body-sm text-ink">Effective {s.effective_from.slice(0, 10)}</span>
                      <Button size="sm" variant="ghost" onClick={() => setEditing(s)}>Edit</Button>
                    </li>
                  ))}
                </ul>
              </Card>
            </div>
          )}
        </>
       )}

      {editing && (
        <EditDialog
          mode={editing === 'new' ? 'new' : 'edit'}
          setting={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => { qc.invalidateQueries({ queryKey: ['payroll-settings'] }); setEditing(null); }}
        />
      )}
    </div>
  );
}

function SettingSummary({ setting, onEdit }: { setting: PayrollSetting; onEdit: () => void }) {
  const pct = (s: string) => `${(Number(s) * 100).toFixed(2)}%`;
  const rp  = (s: string) => `Rp ${Number(s).toLocaleString('id-ID')}`;
  return (
    <Card>
      <div className="flex items-end justify-between mb-3">
        <div>
          <CardTitle>Current setting</CardTitle>
          <CardDescription>Effective from {setting.effective_from.slice(0, 10)}</CardDescription>
        </div>
        <Button size="sm" variant="secondary" onClick={onEdit}>Edit</Button>
      </div>
      <div className="grid sm:grid-cols-2 gap-x-6 gap-y-3">
        <Section title="BPJS Kesehatan">
          <KV k="Employer"  v={pct(setting.bpjs_kesehatan_employer)} />
          <KV k="Employee"  v={pct(setting.bpjs_kesehatan_employee)} />
          <KV k="Salary cap"v={rp(setting.bpjs_kesehatan_cap)} />
        </Section>
        <Section title="BPJS JHT (Old-age savings)">
          <KV k="Employer" v={pct(setting.bpjs_jht_employer)} />
          <KV k="Employee" v={pct(setting.bpjs_jht_employee)} />
        </Section>
        <Section title="BPJS JP (Pension)">
          <KV k="Employer"  v={pct(setting.bpjs_jp_employer)} />
          <KV k="Employee"  v={pct(setting.bpjs_jp_employee)} />
          <KV k="Salary cap"v={rp(setting.bpjs_jp_cap)} />
        </Section>
        <Section title="BPJS JKK + JKM (Work-injury + Death)">
          <KV k="JKK (employer)" v={pct(setting.bpjs_jkk_employer)} />
          <KV k="JKM (employer)" v={pct(setting.bpjs_jkm_employer)} />
        </Section>
      </div>
      <div className="mt-4 pt-3 border-t border-hairline">
        <div className="text-micro-uppercase text-stone mb-2">PPh21 TER brackets</div>
        {!setting.pph21_ter || setting.pph21_ter.length === 0 ? (
          <div className="text-caption text-stone">Not set yet — defaults to legacy gross-up calculation.</div>
        ) : (
          <div className="space-y-2">
            {setting.pph21_ter.map((cat, i) => (
              <div key={i}>
                <div className="text-caption text-charcoal mb-1">Category {cat.category}: {cat.brackets.length} brackets</div>
                <div className="grid grid-cols-2 sm:grid-cols-4 gap-1 text-caption font-mono">
                  {cat.brackets.map((b, j) => (
                    <div key={j} className="px-2 py-1 rounded bg-surface text-charcoal">
                      ≤ {b.max ? Number(b.max).toLocaleString('id-ID') : '∞'} · {(b.rate * 100).toFixed(2)}%
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </Card>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-micro-uppercase text-stone mb-1.5">{title}</div>
      <div className="space-y-1">{children}</div>
    </div>
  );
}
function KV({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-caption text-stone">{k}</span>
      <span className="text-body-sm num text-ink">{v}</span>
    </div>
  );
}

function EditDialog({
  mode, setting, onClose, onSaved,
}: { mode: 'new'|'edit'; setting: PayrollSetting | null; onClose: () => void; onSaved: () => void }) {
  const [effectiveFrom, setEF]    = useState(setting?.effective_from?.slice(0, 10) ?? new Date().getFullYear() + '-01-01');
  const [kesEmp, setKesEmp]       = useState(setting?.bpjs_kesehatan_employer ?? '0.04');
  const [kesEe,  setKesEe]        = useState(setting?.bpjs_kesehatan_employee ?? '0.01');
  const [kesCap, setKesCap]       = useState(setting?.bpjs_kesehatan_cap ?? '12000000');
  const [jhtEmp, setJhtEmp]       = useState(setting?.bpjs_jht_employer ?? '0.037');
  const [jhtEe,  setJhtEe]        = useState(setting?.bpjs_jht_employee ?? '0.02');
  const [jpEmp,  setJpEmp]        = useState(setting?.bpjs_jp_employer ?? '0.02');
  const [jpEe,   setJpEe]         = useState(setting?.bpjs_jp_employee ?? '0.01');
  const [jpCap,  setJpCap]        = useState(setting?.bpjs_jp_cap ?? '10042300');
  const [jkk,    setJkk]          = useState(setting?.bpjs_jkk_employer ?? '0.0024');
  const [jkm,    setJkm]          = useState(setting?.bpjs_jkm_employer ?? '0.0030');
  const [terJSON, setTerJSON]     = useState(JSON.stringify(setting?.pph21_ter ?? SAMPLE_TER, null, 2));
  const [error, setError]         = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => {
      let ter: unknown;
      try { ter = JSON.parse(terJSON); }
      catch (e) { throw new Error('PPh21 TER must be valid JSON'); }
      return api('/admin/payroll-settings', {
        method: 'PUT',
        body: {
          id: setting?.id ?? '',
          effective_from: effectiveFrom,
          bpjs_kesehatan_employer: kesEmp, bpjs_kesehatan_employee: kesEe, bpjs_kesehatan_cap: kesCap,
          bpjs_jht_employer: jhtEmp, bpjs_jht_employee: jhtEe,
          bpjs_jp_employer: jpEmp, bpjs_jp_employee: jpEe, bpjs_jp_cap: jpCap,
          bpjs_jkk_employer: jkk, bpjs_jkm_employer: jkm,
          pph21_ter: ter,
        },
      });
    },
    onSuccess: () => onSaved(),
    onError: (e: Error) => setError(e.message),
  });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div className="bg-canvas border border-hairline rounded-xl shadow-overlay max-w-3xl w-full max-h-[90vh] overflow-y-auto">
        <div className="px-5 py-4 border-b border-hairline flex items-center justify-between">
          <CardTitle>{mode === 'new' ? 'New payroll setting' : `Edit ${setting?.effective_from?.slice(0, 10)}`}</CardTitle>
          <Button variant="ghost" size="sm" onClick={onClose}>Close</Button>
        </div>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); save.mutate(); }} className="p-5 space-y-4">
          <Field label="Effective from"><Input type="date" value={effectiveFrom} onChange={(e) => setEF(e.target.value)} required /></Field>
          <div>
            <div className="text-micro-uppercase text-stone mb-2">BPJS Kesehatan</div>
            <div className="grid grid-cols-3 gap-3">
              <Field label="Employer %"><Input value={kesEmp} onChange={(e) => setKesEmp(e.target.value)} className="num text-right" /></Field>
              <Field label="Employee %"><Input value={kesEe}  onChange={(e) => setKesEe(e.target.value)}  className="num text-right" /></Field>
              <Field label="Salary cap (Rp)"><Input value={kesCap} onChange={(e) => setKesCap(e.target.value)} className="num text-right" /></Field>
            </div>
          </div>
          <div>
            <div className="text-micro-uppercase text-stone mb-2">BPJS JHT</div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Employer %"><Input value={jhtEmp} onChange={(e) => setJhtEmp(e.target.value)} className="num text-right" /></Field>
              <Field label="Employee %"><Input value={jhtEe} onChange={(e) => setJhtEe(e.target.value)} className="num text-right" /></Field>
            </div>
          </div>
          <div>
            <div className="text-micro-uppercase text-stone mb-2">BPJS JP</div>
            <div className="grid grid-cols-3 gap-3">
              <Field label="Employer %"><Input value={jpEmp} onChange={(e) => setJpEmp(e.target.value)} className="num text-right" /></Field>
              <Field label="Employee %"><Input value={jpEe} onChange={(e) => setJpEe(e.target.value)} className="num text-right" /></Field>
              <Field label="Salary cap (Rp)"><Input value={jpCap} onChange={(e) => setJpCap(e.target.value)} className="num text-right" /></Field>
            </div>
          </div>
          <div>
            <div className="text-micro-uppercase text-stone mb-2">BPJS JKK + JKM</div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="JKK employer %"><Input value={jkk} onChange={(e) => setJkk(e.target.value)} className="num text-right" /></Field>
              <Field label="JKM employer %"><Input value={jkm} onChange={(e) => setJkm(e.target.value)} className="num text-right" /></Field>
            </div>
          </div>
          <Field label="PPh21 TER brackets (JSON)" hint="Array of {category, brackets:[{max?, rate}]}. Omit max on the final bracket for the open-ended top.">
            <textarea className="input-base !h-auto !py-2 font-mono text-[12px] leading-snug" rows={10}
              value={terJSON} onChange={(e) => setTerJSON(e.target.value)} spellCheck={false} />
          </Field>
          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}
          <div className="flex justify-end gap-2 pt-3 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={save.isPending}>
              <Save className="size-3.5" /> Save setting
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
