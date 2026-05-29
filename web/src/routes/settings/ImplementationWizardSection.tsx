import { useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { Wand2, ArrowRight, Sparkles, ListChecks } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { getAccessToken, getActiveCompany } from '@/lib/api';

/**
 * Settings entry point to the implementation/migration wizard (the full-screen
 * /setup flow). Per docs/agent-build-prompt.md §4, the wizard must be
 * reachable from Settings even after go-live so admins can re-run setup or
 * onboard an additional company. The wizard itself lives outside the AppShell;
 * "Start" creates a fresh setup session and navigates into it.
 */
export function ImplementationWizardSection() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);

  async function startNew() {
    setError(null);
    setStarting(true);
    try {
      // The migration API lives on the agent service under /api/agent/v1 —
      // not behind the api() helper's /api/v1 prefix — so call it directly.
      const headers: Record<string, string> = { Accept: 'application/json', 'Content-Type': 'application/json' };
      const token = getAccessToken();
      if (token) headers.Authorization = `Bearer ${token}`;
      const co = getActiveCompany();
      if (co) headers['X-Company-Id'] = co;
      const res = await fetch('/api/agent/v1/migration/start', {
        method: 'POST',
        headers,
        body: JSON.stringify({ title: 'New Company Setup' }),
      });
      const text = await res.text();
      if (!res.ok) throw new Error(text || res.statusText);
      const r = JSON.parse(text) as { session_id: string };
      void navigate({ to: `/setup/${r.session_id}` as never });
    } catch (e) {
      setError((e as { message?: string })?.message ?? 'Failed to start setup');
      setStarting(false);
    }
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardTitle>
          <Wand2 className="size-4 inline mr-1.5 text-accent" />
          Implementation Wizard
        </CardTitle>
        <CardDescription>
          A guided, resumable setup flow for go-live — and for onboarding additional
          companies after go-live. Each run is saved, so you can leave and come back.
        </CardDescription>

        <div className="mt-4 rounded-lg bg-surface-soft border border-hairline p-4">
          <div className="text-micro-uppercase text-stone mb-2">The five steps</div>
          <ul className="space-y-1.5 text-body-sm text-charcoal">
            <li className="flex items-start gap-2"><Sparkles className="size-4 text-accent mt-0.5 shrink-0" /><span><b>Discovery</b> — a short conversation with the AI assistant that builds your business profile.</span></li>
            <li className="flex items-start gap-2"><ListChecks className="size-4 text-stone mt-0.5 shrink-0" /><span><b>Chart of Accounts</b> — a PSAK-aligned proposal tailored to your industry.</span></li>
            <li className="flex items-start gap-2"><ListChecks className="size-4 text-stone mt-0.5 shrink-0" /><span><b>Data Migration</b> — staged CSV/XLSX import of masters.</span></li>
            <li className="flex items-start gap-2"><ListChecks className="size-4 text-stone mt-0.5 shrink-0" /><span><b>Opening Balances</b> — trial-balance upload with a reconciliation proof.</span></li>
            <li className="flex items-start gap-2"><ListChecks className="size-4 text-stone mt-0.5 shrink-0" /><span><b>Go-Live Readiness</b> — automated checks against your live ERP state.</span></li>
          </ul>
        </div>

        <div className="mt-4 flex items-center gap-3">
          <Button onClick={startNew} loading={starting}>
            Start a new setup <ArrowRight className="size-3.5" />
          </Button>
          <a href="/setup" className="text-body-sm text-accent hover:underline">
            Resume an existing setup
          </a>
        </div>
        {error && <div className="mt-2 text-caption text-brand-error">{error}</div>}
      </Card>
    </div>
  );
}
