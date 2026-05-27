import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Mail, Check, AlertCircle, Send } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface SMTPConfig {
  host: string;
  port: number;
  username?: string;
  has_password: boolean;
  use_tls: boolean;
  from_email: string;
  from_name?: string;
  reply_to_email?: string;
  is_enabled: boolean;
  updated_at: string;
}

interface LogEntry {
  id: string;
  to_addr: string;
  subject: string;
  event_key?: string;
  status: 'sent' | 'failed';
  error_message?: string;
  sent_at: string;
}
interface LogList { items: LogEntry[] }

export function SMTPSection() {
  const qc = useQueryClient();
  const { data: cfg, isLoading } = useQuery({
    queryKey: ['smtp-config'],
    queryFn: () => api<SMTPConfig>('/admin/smtp'),
  });
  const { data: log } = useQuery({
    queryKey: ['email-log', 25],
    queryFn: () => api<LogList>('/admin/email-log?limit=25'),
  });

  const [form, setForm] = useState<SMTPConfig | null>(null);
  // `password` is a controlled field that's only sent when changed.
  const [password, setPassword] = useState<string | null>(null);
  const [testTo, setTestTo]     = useState('');
  const [error, setError]       = useState<string | null>(null);
  const [savedAt, setSavedAt]   = useState<string | null>(null);

  useEffect(() => {
    if (cfg && !form) setForm(cfg);
  }, [cfg, form]);

  const save = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = {
        host: form?.host ?? '',
        port: form?.port ?? 587,
        username: form?.username ?? '',
        use_tls: form?.use_tls ?? true,
        from_email: form?.from_email ?? '',
        from_name: form?.from_name ?? '',
        reply_to_email: form?.reply_to_email ?? '',
        is_enabled: form?.is_enabled ?? false,
      };
      if (password !== null) body.password = password;
      return api<SMTPConfig>('/admin/smtp', { method: 'PUT', body });
    },
    onSuccess: (next) => {
      setForm(next);
      setPassword(null);
      setError(null);
      setSavedAt(new Date().toISOString());
      void qc.invalidateQueries({ queryKey: ['smtp-config'] });
    },
    onError: (e: Error) => setError(e.message),
  });

  const test = useMutation({
    mutationFn: () => api<{ ok: boolean; error?: string }>('/admin/smtp/test', {
      method: 'POST',
      body: { to: testTo },
    }),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['email-log'] }); },
  });

  if (isLoading || !form) {
    return <Card><Skeleton className="h-64 w-full" /></Card>;
  }

  return (
    <div className="space-y-6">
      <section>
        <div className="mb-3 flex items-end justify-between gap-3 flex-wrap">
          <div>
            <CardTitle>SMTP server</CardTitle>
            <CardDescription>
              Outbound mail server used for all notifications. One workspace-wide profile.
            </CardDescription>
          </div>
          <StatusPill tone={form.is_enabled ? 'success' : 'neutral'}>
            {form.is_enabled ? 'Enabled' : 'Disabled'}
          </StatusPill>
        </div>

        <Card>
          <div className="grid sm:grid-cols-2 gap-4">
            <Field label="Host">
              <Input value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} placeholder="smtp.gmail.com" />
            </Field>
            <Field label="Port" hint="465 = implicit TLS · 587 = STARTTLS · 25 = plaintext (avoid)">
              <Input type="number" value={form.port} onChange={(e) => setForm({ ...form, port: Number(e.target.value) })} className="num text-right" />
            </Field>
            <Field label="Username">
              <Input value={form.username ?? ''} onChange={(e) => setForm({ ...form, username: e.target.value })} autoComplete="off" />
            </Field>
            <Field label="Password" hint={form.has_password && password === null ? 'Set — leave blank to keep current' : ''}>
              <Input
                type="password"
                value={password ?? ''}
                placeholder={form.has_password ? '••••••••' : 'app password'}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
              />
            </Field>
            <Field label="From email">
              <Input type="email" value={form.from_email} onChange={(e) => setForm({ ...form, from_email: e.target.value })} placeholder="noreply@yourdomain.com" />
            </Field>
            <Field label="From name">
              <Input value={form.from_name ?? ''} onChange={(e) => setForm({ ...form, from_name: e.target.value })} placeholder="Logica ERP" />
            </Field>
            <Field label="Reply-to" hint="Optional. Used when recipients hit Reply.">
              <Input type="email" value={form.reply_to_email ?? ''} onChange={(e) => setForm({ ...form, reply_to_email: e.target.value })} />
            </Field>
            <div className="space-y-1.5">
              <div className="label-base">Transport security</div>
              <label className="flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
                <input
                  type="checkbox"
                  className="accent-brand-green-deep"
                  checked={form.use_tls}
                  onChange={(e) => setForm({ ...form, use_tls: e.target.checked })}
                />
                Use TLS (recommended)
              </label>
              <label className="flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
                <input
                  type="checkbox"
                  className="accent-brand-green-deep"
                  checked={form.is_enabled}
                  onChange={(e) => setForm({ ...form, is_enabled: e.target.checked })}
                />
                Send live email (uncheck to put outbound on hold)
              </label>
            </div>
          </div>

          {error && (
            <div className="mt-4 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}

          <div className="mt-5 pt-4 border-t border-hairline flex items-center gap-3 flex-wrap">
            <Button onClick={() => save.mutate()} loading={save.isPending}>Save changes</Button>
            {savedAt && !save.isPending && (
              <span className="text-caption text-brand-green-deep inline-flex items-center gap-1">
                <Check className="size-3.5" /> Saved
              </span>
            )}
            <span className="ml-auto text-caption text-stone">
              Last updated {new Date(form.updated_at).toLocaleString('id-ID')}
            </span>
          </div>
        </Card>
      </section>

      <section>
        <div className="mb-3">
          <CardTitle>Send a test email</CardTitle>
          <CardDescription>Confirms the saved configuration works end-to-end.</CardDescription>
        </div>
        <Card>
          <div className="flex items-end gap-3 flex-wrap">
            <Field label="Recipient" htmlFor="test-to">
              <Input
                id="test-to"
                type="email"
                value={testTo}
                onChange={(e) => setTestTo(e.target.value)}
                placeholder="you@example.com"
                className="w-[320px]"
              />
            </Field>
            <Button onClick={() => test.mutate()} loading={test.isPending} disabled={!testTo}>
              <Send className="size-3.5" /> Send test
            </Button>
          </div>

          {test.data && (
            test.data.ok ? (
              <div className="mt-3 rounded-md bg-success/10 text-success text-caption px-3 py-2 inline-flex items-center gap-2">
                <Check className="size-4" /> Test sent. Check the recipient's inbox.
              </div>
            ) : (
              <div className="mt-3 rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
                <AlertCircle className="size-4 mt-0.5 shrink-0" />
                <span>{test.data.error}</span>
              </div>
            )
          )}
        </Card>
      </section>

      <section>
        <div className="mb-3">
          <CardTitle>Recent activity</CardTitle>
          <CardDescription>Last 25 send attempts. Tests aren't logged.</CardDescription>
        </div>
        {(log?.items ?? []).length === 0 ? (
          <Card>
            <div className="text-center py-6 text-body-sm text-stone">
              <Mail className="mx-auto size-5 mb-2" /> No send attempts yet.
            </div>
          </Card>
        ) : (
          <Card padded={false}>
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft border-b border-hairline">
                <tr className="text-micro-uppercase text-stone">
                  <th className="text-left  font-medium px-4 py-2.5">When</th>
                  <th className="text-left  font-medium px-4 py-2.5">To</th>
                  <th className="text-left  font-medium px-4 py-2.5">Subject</th>
                  <th className="text-left  font-medium px-4 py-2.5">Event</th>
                  <th className="text-right font-medium px-4 py-2.5">Status</th>
                </tr>
              </thead>
              <tbody>
                {(log?.items ?? []).map((e) => (
                  <tr key={e.id} className="border-b border-hairline last:border-0">
                    <td className="px-4 py-2 text-stone num">{new Date(e.sent_at).toLocaleString('id-ID')}</td>
                    <td className="px-4 py-2 text-ink truncate">{e.to_addr}</td>
                    <td className="px-4 py-2 text-charcoal truncate">{e.subject}</td>
                    <td className="px-4 py-2 text-stone font-mono text-caption">{e.event_key ?? '—'}</td>
                    <td className={cn('px-4 py-2 text-right text-caption font-medium',
                      e.status === 'sent' ? 'text-success' : 'text-brand-error')}>
                      {e.status}
                    </td>
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
