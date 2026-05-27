import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from '@tanstack/react-router';
import { Check, ChevronRight, Sparkles, AlertCircle, ArrowRight } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { cn } from '@/lib/cn';
import { getAccessToken, getActiveCompany } from '@/lib/api';
import { DataMigrationStep } from './migration/DataMigrationStep';
import { OpeningBalancesStep } from './migration/OpeningBalancesStep';

/**
 * MigrationWizard — five-step onboarding flow described in
 * docs/agent-build-prompt.md §4. Full-screen, no sidebar, conversational +
 * structured proposal cards. Resumable: a setup session persists in
 * agent_session so users can come back to it.
 *
 * Phase-D MVP scope:
 *  - Step 1 (Discovery): structured form, not free-form chat. The spec
 *    favours conversation, but a form lets us ship the round-trip cheaply.
 *    Future iteration can swap in a Copilot-style chat-driven intake.
 *  - Step 2 (COA): proposal table + accept. Account creation happens via
 *    the standard /accounting/accounts endpoint (operator-triggered).
 *  - Step 3-4: stubbed with explicit "next phase" placeholders. The
 *    underlying APIs (/admin/imports/* and /accounting/journal-entries)
 *    already exist; wiring them into the wizard is incremental work.
 *  - Step 5 (Readiness): live checks. Calls /migration/{id}/readiness
 *    which evaluates the actual ERP state.
 */

const STEPS = [
  { id: 'discovery',        label: 'Discovery',          short: '1. Profile' },
  { id: 'coa',              label: 'Chart of Accounts',  short: '2. COA' },
  { id: 'data_migration',   label: 'Data Migration',     short: '3. Data' },
  { id: 'opening_balances', label: 'Opening Balances',   short: '4. Balances' },
  { id: 'readiness',        label: 'Go-Live Readiness',  short: '5. Ready' },
] as const;
type StepID = typeof STEPS[number]['id'];

interface SetupProfile {
  business_type: string;
  industry: string;
  employees: number;
  modules: string[];
  multicompany: boolean;
  fiscal_year_start: string;
  base_currency: string;
  legacy_system: string;
}

interface State {
  current_step: StepID;
  completed: StepID[];
  profile?: SetupProfile;
  step_data?: Record<string, unknown>;
}

interface COAAccount {
  account_number: string;
  name: string;
  root_type: string;
  account_type?: string;
  is_group: boolean;
  parent_account_number?: string;
}

interface ReadinessCheck {
  id: string;
  label: string;
  pass: boolean;
  detail?: string;
  fix_url?: string;
}

async function agentFetch<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const co = getActiveCompany();
  if (co) headers['X-Company-Id'] = co;
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  const r = await fetch(path, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return t ? (JSON.parse(t) as T) : ({} as T);
}

export function MigrationWizardEntry() {
  const navigate = useNavigate();
  const start = useMutation({
    mutationFn: () => agentFetch<{ session_id: string }>('/api/agent/v1/migration/start', {
      method: 'POST', body: { title: 'New Company Setup' },
    }),
    onSuccess: (r) => { void navigate({ to: `/setup/${r.session_id}` as never }); },
  });

  return (
    <div className="min-h-screen bg-canvas flex items-center justify-center px-6">
      <Card className="max-w-md">
        <CardTitle><Sparkles className="size-4 text-accent inline mr-1.5" />Implementation Wizard</CardTitle>
        <CardDescription>
          Setup yang dipandu untuk go-live: profil bisnis, chart of accounts, migrasi data, opening balances, dan readiness check.
        </CardDescription>
        <div className="mt-4">
          <Button onClick={() => start.mutate()} loading={start.isPending}>
            Start a new setup <ArrowRight className="size-3.5" />
          </Button>
          {start.error && (
            <div className="mt-2 text-caption text-brand-error">{(start.error as Error).message}</div>
          )}
        </div>
      </Card>
    </div>
  );
}

export function MigrationWizard() {
  const { sessionId } = useParams({ strict: false }) as { sessionId?: string };
  const qc = useQueryClient();
  const { data: state, isLoading, error } = useQuery({
    queryKey: ['migration', sessionId],
    queryFn:  () => agentFetch<State>(`/api/agent/v1/migration/${sessionId}/state`),
    enabled:  !!sessionId,
  });
  const [activeStep, setActiveStep] = useState<StepID | null>(null);
  useEffect(() => {
    if (state && !activeStep) setActiveStep(state.current_step);
  }, [state, activeStep]);

  if (!sessionId) return <MigrationWizardEntry />;
  if (isLoading) return <FullPageMessage>Loading wizard…</FullPageMessage>;
  if (error)     return <FullPageMessage tone="error">{(error as Error).message}</FullPageMessage>;
  if (!state)    return <FullPageMessage>No setup state found.</FullPageMessage>;

  const completed = new Set<StepID>(state.completed ?? []);
  const step: StepID = activeStep ?? state.current_step;

  return (
    <div className="min-h-screen bg-canvas grid grid-cols-1 lg:grid-cols-[280px,1fr]">
      <aside className="bg-surface-soft border-r border-hairline p-6">
        <div className="text-caption text-stone mb-1">Implementation</div>
        <div className="text-body font-semibold text-ink mb-6">{sessionId.slice(0, 16)}…</div>
        <ol className="space-y-1">
          {STEPS.map((s, i) => {
            const done = completed.has(s.id);
            const isActive = s.id === step;
            return (
              <li key={s.id}>
                <button
                  type="button"
                  onClick={() => setActiveStep(s.id)}
                  className={cn(
                    'w-full text-left flex items-center gap-3 px-3 py-2 rounded-md transition-colors',
                    isActive && 'bg-canvas border border-hairline',
                    !isActive && 'hover:bg-canvas',
                  )}
                >
                  <span className={cn(
                    'size-6 rounded-full inline-flex items-center justify-center text-caption font-semibold shrink-0',
                    done ? 'bg-brand-success/15 text-brand-success'
                         : isActive ? 'bg-accent/15 text-accent'
                                    : 'bg-surface text-stone',
                  )}>
                    {done ? <Check className="size-3.5" /> : i + 1}
                  </span>
                  <div className="min-w-0">
                    <div className={cn('text-body-sm truncate', isActive ? 'text-ink font-medium' : 'text-charcoal')}>
                      {s.label}
                    </div>
                  </div>
                </button>
              </li>
            );
          })}
        </ol>
      </aside>

      <main className="px-8 lg:px-12 py-10 max-w-3xl">
        {step === 'discovery' && (
          <DiscoveryStep
            initial={state.profile}
            sessionId={sessionId}
            onDone={() => { void qc.invalidateQueries({ queryKey: ['migration', sessionId] }); setActiveStep('coa'); }}
          />
        )}
        {step === 'coa' && (
          <COAStep
            sessionId={sessionId}
            onAccepted={() => { void qc.invalidateQueries({ queryKey: ['migration', sessionId] }); setActiveStep('data_migration'); }}
          />
        )}
        {step === 'data_migration' && (
          <DataMigrationStep onContinue={() => setActiveStep('opening_balances')} />
        )}
        {step === 'opening_balances' && (
          <OpeningBalancesStep sessionId={sessionId} onContinue={() => setActiveStep('readiness')} />
        )}
        {step === 'readiness' && <ReadinessStep sessionId={sessionId} />}
      </main>
    </div>
  );
}

/* ---- Step 1: Discovery ---- */

function DiscoveryStep({
  initial, sessionId, onDone,
}: {
  initial: SetupProfile | undefined;
  sessionId: string;
  onDone: () => void;
}) {
  const [profile, setProfile] = useState<SetupProfile>(initial ?? {
    business_type: '', industry: '', employees: 0, modules: [],
    multicompany: false, fiscal_year_start: '01-01', base_currency: 'IDR',
    legacy_system: '',
  });
  const save = useMutation({
    mutationFn: (p: SetupProfile) =>
      agentFetch(`/api/agent/v1/migration/${sessionId}/profile`, { method: 'POST', body: p }),
    onSuccess: () => onDone(),
  });

  function setField<K extends keyof SetupProfile>(key: K, value: SetupProfile[K]) {
    setProfile((p) => ({ ...p, [key]: value }));
  }

  return (
    <div>
      <h1 className="text-heading-3 text-ink mb-1">Step 1 · Discovery Interview</h1>
      <p className="text-body text-stone mb-6">
        Beberapa pertanyaan tentang perusahaan dan operasinya. Jawaban ini mendorong setiap rekomendasi berikutnya — chart of accounts, modul yang diaktifkan, dan readiness check.
      </p>
      <Card>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Field label="Business type" hint="e.g. Trading, Manufacturing, Service">
            <Input value={profile.business_type} onChange={(e) => setField('business_type', e.target.value)} />
          </Field>
          <Field label="Industry">
            <Input value={profile.industry} onChange={(e) => setField('industry', e.target.value)} />
          </Field>
          <Field label="Number of employees">
            <Input
              type="number"
              value={profile.employees || ''}
              onChange={(e) => setField('employees', parseInt(e.target.value, 10) || 0)}
            />
          </Field>
          <Field label="Legacy system" hint="What are you migrating from?">
            <Input value={profile.legacy_system} onChange={(e) => setField('legacy_system', e.target.value)} />
          </Field>
          <Field label="Base currency">
            <Input value={profile.base_currency} onChange={(e) => setField('base_currency', e.target.value)} />
          </Field>
          <Field label="Fiscal year start (MM-DD)">
            <Input value={profile.fiscal_year_start} onChange={(e) => setField('fiscal_year_start', e.target.value)} />
          </Field>
          <div className="sm:col-span-2">
            <Field label="Multi-company">
              <label className="inline-flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={profile.multicompany}
                  onChange={(e) => setField('multicompany', e.target.checked)}
                  className="size-4 rounded border-border accent-accent"
                />
                <span className="text-body text-text-secondary">{profile.multicompany ? 'Yes' : 'No'}</span>
              </label>
            </Field>
          </div>
        </div>
      </Card>
      <div className="mt-4 flex items-center justify-end">
        <Button onClick={() => save.mutate(profile)} loading={save.isPending}>
          Save &amp; continue <ChevronRight className="size-3.5" />
        </Button>
      </div>
      {save.error && (
        <div className="mt-2 text-caption text-brand-error">{(save.error as Error).message}</div>
      )}
    </div>
  );
}

/* ---- Step 2: COA ---- */

function COAStep({ sessionId, onAccepted }: { sessionId: string; onAccepted: () => void }) {
  const propose = useMutation({
    mutationFn: () => agentFetch<{ proposal: COAAccount[] }>(`/api/agent/v1/migration/${sessionId}/coa/propose`, { method: 'POST' }),
  });
  const accept = useMutation({
    mutationFn: () => agentFetch(`/api/agent/v1/migration/${sessionId}/coa/accept`, { method: 'POST' }),
    onSuccess: () => onAccepted(),
  });
  const rows = useMemo(() => propose.data?.proposal ?? [], [propose.data]);

  return (
    <div>
      <h1 className="text-heading-3 text-ink mb-1">Step 2 · Chart of Accounts</h1>
      <p className="text-body text-stone mb-6">
        Struktur PSAK-aligned dengan akun pajak (PPN, PPh 21/23/25/26) sudah disiapkan. Tinjau dan terima, lalu Copilot bisa membantu membuat akun via API.
      </p>
      {rows.length === 0 ? (
        <div className="flex items-center gap-3">
          <Button onClick={() => propose.mutate()} loading={propose.isPending}>
            Generate proposal
          </Button>
        </div>
      ) : (
        <Card padded={false}>
          <div className="px-5 py-3 border-b border-hairline">
            <CardTitle>{rows.length} accounts proposed</CardTitle>
            <CardDescription>Akun bertipe group ditampilkan tebal — tidak dapat diposting.</CardDescription>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft border-b border-hairline text-micro-uppercase text-stone">
                <tr>
                  <th className="text-left font-medium px-3 py-2">No.</th>
                  <th className="text-left font-medium px-3 py-2">Name</th>
                  <th className="text-left font-medium px-3 py-2">Root</th>
                  <th className="text-left font-medium px-3 py-2">Type</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((a) => (
                  <tr key={a.account_number} className="border-t border-hairline">
                    <td className="px-3 py-2 font-mono text-caption text-charcoal">{a.account_number}</td>
                    <td className={cn('px-3 py-2', a.is_group && 'font-semibold text-ink')}>{a.name}</td>
                    <td className="px-3 py-2 text-charcoal">{a.root_type}</td>
                    <td className="px-3 py-2 text-stone">{a.account_type ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      {rows.length > 0 && (
        <div className="mt-4 flex items-center justify-between gap-3">
          <Button variant="ghost" onClick={() => propose.mutate()} disabled={propose.isPending}>
            Regenerate
          </Button>
          <Button onClick={() => accept.mutate()} loading={accept.isPending}>
            Accept &amp; continue <ChevronRight className="size-3.5" />
          </Button>
        </div>
      )}
    </div>
  );
}

/* ---- Steps 3-4 placeholder ---- */

function PlaceholderStep({
  title, description, onSkip,
}: {
  title: string; description: string; onSkip: () => void;
}) {
  return (
    <div>
      <h1 className="text-heading-3 text-ink mb-1">{title}</h1>
      <Card>
        <div className="flex items-start gap-3">
          <AlertCircle className="size-5 text-warning shrink-0 mt-0.5" />
          <div>
            <p className="text-body text-charcoal">{description}</p>
            <p className="text-caption text-stone mt-2">Step ini akan ditambahkan setelah Phase D wizard shell di-review. Untuk sekarang, gunakan UI Settings yang ada untuk import data dan membuat opening-balance Journal Entry secara manual.</p>
          </div>
        </div>
      </Card>
      <div className="mt-4 flex items-center justify-end">
        <Button variant="ghost" onClick={onSkip}>Skip for now <ChevronRight className="size-3.5" /></Button>
      </div>
    </div>
  );
}

/* ---- Step 5: Readiness ---- */

function ReadinessStep({ sessionId }: { sessionId: string }) {
  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['migration', sessionId, 'readiness'],
    queryFn:  () => agentFetch<{ items: ReadinessCheck[] }>(`/api/agent/v1/migration/${sessionId}/readiness`),
  });
  const items = data?.items ?? [];
  const failing = items.filter((i) => !i.pass);
  const ready = items.length > 0 && failing.length === 0;

  return (
    <div>
      <h1 className="text-heading-3 text-ink mb-1">Step 5 · Go-Live Readiness</h1>
      <p className="text-body text-stone mb-6">
        Cek otomatis terhadap state ERP saat ini. Tidak ada keputusan LLM — semua observable.
      </p>
      {isLoading && <Card>Loading checks…</Card>}
      {!isLoading && (
        <>
          <Card className={cn('border-l-4', ready ? 'border-l-brand-success' : 'border-l-warning')}>
            <div className="flex items-center justify-between">
              <div>
                <CardTitle>{ready ? '✅ Ready to go live' : `${failing.length} items need attention`}</CardTitle>
                <CardDescription>
                  {items.length - failing.length} of {items.length} checks passing.
                </CardDescription>
              </div>
              <Button variant="ghost" size="sm" onClick={() => refetch()} disabled={isFetching}>
                Re-check
              </Button>
            </div>
          </Card>
          <ul className="mt-3 space-y-1.5">
            {items.map((c) => (
              <li key={c.id}>
                <div className="flex items-center gap-3 px-3 py-2 rounded-md bg-canvas border border-hairline">
                  <span className={cn(
                    'size-5 rounded-full inline-flex items-center justify-center',
                    c.pass ? 'bg-brand-success/15 text-brand-success' : 'bg-warning/15 text-warning',
                  )}>
                    {c.pass ? <Check className="size-3" /> : <AlertCircle className="size-3" />}
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="text-body-sm text-ink">{c.label}</div>
                    {c.detail && <div className="text-caption text-stone">{c.detail}</div>}
                  </div>
                  {!c.pass && c.fix_url && (
                    <a href={c.fix_url} className="text-caption text-accent hover:underline">Fix →</a>
                  )}
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function FullPageMessage({ children, tone }: { children: React.ReactNode; tone?: 'error' }) {
  return (
    <div className="min-h-screen flex items-center justify-center bg-canvas">
      <p className={cn('text-body', tone === 'error' ? 'text-brand-error' : 'text-stone')}>{children}</p>
    </div>
  );
}
