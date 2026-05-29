import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from '@tanstack/react-router';
import { Check, ChevronRight, Sparkles, AlertCircle, ArrowRight, Send } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
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
 * Scope:
 *  - Step 1 (Discovery): conversational intake (Claude via the agent). The
 *    model interviews the user and records the SetupProfile through a tool
 *    call; a live profile card shows what's been gathered. Backed by
 *    POST /migration/{id}/discovery/chat.
 *  - Step 2 (COA): proposal table + accept. The proposal is the PSAK base +
 *    industry overlay, plus best-effort LLM-suggested extra accounts. Account
 *    creation happens via the standard /accounting/accounts endpoint.
 *  - Step 3 (Data Migration): upload → map → commit, driven by
 *    /admin/imports/*. See DataMigrationStep.tsx.
 *  - Step 4 (Opening Balances): trial-balance upload, reconciliation
 *    proof, and Tier-2 auto-submit of the opening JE. See
 *    OpeningBalancesStep.tsx.
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

/* ---- Step 1: Discovery (conversational) ---- */

interface ChatMsg { role: 'user' | 'assistant'; content: string }
interface DiscoveryResult { reply: string; profile?: SetupProfile; complete: boolean }

const DISCOVERY_GREETING =
  'Halo! Saya akan membantu menyiapkan Logica ERP untuk bisnis Anda. Untuk mulai — bisnis Anda bergerak di bidang apa, dan kira-kira masuk industri mana (dagang, manufaktur, jasa, atau konstruksi)?';

function DiscoveryStep({
  initial, sessionId, onDone,
}: {
  initial: SetupProfile | undefined;
  sessionId: string;
  onDone: () => void;
}) {
  const [messages, setMessages] = useState<ChatMsg[]>([{ role: 'assistant', content: DISCOVERY_GREETING }]);
  const [draft, setDraft] = useState('');
  const [profile, setProfile] = useState<SetupProfile | undefined>(initial);
  const scrollRef = useRef<HTMLDivElement>(null);

  const send = useMutation({
    mutationFn: (message: string) =>
      agentFetch<DiscoveryResult>(`/api/agent/v1/migration/${sessionId}/discovery/chat`, {
        method: 'POST', body: { message },
      }),
    onSuccess: (r) => {
      setMessages((m) => [...m, { role: 'assistant', content: r.reply }]);
      if (r.profile) setProfile(r.profile);
      if (r.complete) setTimeout(onDone, 900);
    },
  });

  function submit() {
    const msg = draft.trim();
    if (!msg || send.isPending) return;
    setMessages((m) => [...m, { role: 'user', content: msg }]);
    setDraft('');
    send.mutate(msg);
  }

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
  }, [messages, send.isPending]);

  const err = send.error as Error | null;
  const noModel = err?.message?.toLowerCase().includes('no ai model') || err?.message?.toLowerCase().includes('model configured');

  return (
    <div>
      <h1 className="text-heading-3 text-ink mb-1">Step 1 · Discovery Interview</h1>
      <p className="text-body text-stone mb-6">
        Ngobrol sebentar tentang bisnis Anda. Jawaban Anda mendorong rekomendasi berikutnya — chart of accounts, modul, dan readiness check. Asisten akan mengisi profil di kanan otomatis.
      </p>

      <div className="grid grid-cols-1 lg:grid-cols-[1fr,260px] gap-4 items-start">
        <Card padded={false} className="flex flex-col h-[460px]">
          <div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-3">
            {messages.map((m, i) => (
              <div key={i} className={cn('flex', m.role === 'user' ? 'justify-end' : 'justify-start')}>
                <div className={cn(
                  'max-w-[85%] rounded-lg px-3 py-2 text-body-sm whitespace-pre-wrap',
                  m.role === 'user'
                    ? 'bg-accent text-white'
                    : 'bg-surface-soft text-charcoal border border-hairline',
                )}>
                  {m.content}
                </div>
              </div>
            ))}
            {send.isPending && (
              <div className="flex justify-start">
                <div className="rounded-lg px-3 py-2 bg-surface-soft border border-hairline text-stone text-body-sm">
                  <span className="inline-flex gap-1">
                    <span className="size-1.5 rounded-full bg-stone animate-pulse" />
                    <span className="size-1.5 rounded-full bg-stone animate-pulse [animation-delay:150ms]" />
                    <span className="size-1.5 rounded-full bg-stone animate-pulse [animation-delay:300ms]" />
                  </span>
                </div>
              </div>
            )}
          </div>
          <div className="border-t border-hairline p-3 flex items-end gap-2">
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submit(); } }}
              rows={1}
              placeholder="Tulis jawaban Anda…"
              className="flex-1 resize-none px-3 py-2 rounded-md border border-hairline bg-surface text-body-sm focus:outline-none focus:ring-2 focus:ring-accent max-h-32"
            />
            <Button onClick={submit} loading={send.isPending} disabled={!draft.trim()}>
              <Send className="size-4" />
            </Button>
          </div>
        </Card>

        <ProfileCard profile={profile} />
      </div>

      {err && (
        <div className="mt-3">
          {noModel ? (
            <Card className="border-l-4 border-l-warning">
              <div className="text-body-sm text-charcoal">
                Belum ada model AI yang dikonfigurasi. Buka <a href="/settings/ai-model" className="text-accent underline">Settings → AI Model</a>, simpan Anthropic API key, lalu kembali ke sini.
              </div>
            </Card>
          ) : (
            <div className="text-caption text-brand-error">{err.message}</div>
          )}
        </div>
      )}
    </div>
  );
}

function ProfileCard({ profile }: { profile: SetupProfile | undefined }) {
  const rows: Array<[string, string]> = [
    ['Tipe bisnis', profile?.business_type || ''],
    ['Industri', profile?.industry || ''],
    ['Karyawan', profile?.employees ? String(profile.employees) : ''],
    ['Modul', profile?.modules?.length ? profile.modules.join(', ') : ''],
    ['Multi-company', profile ? (profile.multicompany ? 'Ya' : 'Tidak') : ''],
    ['Awal tahun fiskal', profile?.fiscal_year_start || ''],
    ['Mata uang', profile?.base_currency || ''],
    ['Sistem lama', profile?.legacy_system || ''],
  ];
  return (
    <Card className="lg:sticky lg:top-6">
      <CardTitle>Profil terkumpul</CardTitle>
      <CardDescription>Diisi otomatis selama percakapan.</CardDescription>
      <dl className="mt-3 space-y-2">
        {rows.map(([k, v]) => (
          <div key={k} className="flex flex-col">
            <dt className="text-caption text-stone">{k}</dt>
            <dd className={cn('text-body-sm', v ? 'text-ink' : 'text-stone/50 italic')}>{v || 'belum diisi'}</dd>
          </div>
        ))}
      </dl>
    </Card>
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
