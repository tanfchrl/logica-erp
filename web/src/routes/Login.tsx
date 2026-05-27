import { useState } from 'react';
import { motion } from 'framer-motion';
import { LogIn, Sparkles } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { login } from '@/lib/auth';
import type { ApiError } from '@/lib/api';
import { useUI } from '@/store/ui';

export function LoginPage() {
  const { t } = useTranslation();
  const [email, setEmail]       = useState('admin@example.com');
  const [password, setPassword] = useState('ChangeMe!Now123');
  const [busy, setBusy]         = useState(false);
  const [err, setErr]           = useState<string | null>(null);
  const brand                   = useUI((s) => s.brand);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      await login(email, password);
      window.location.href = '/';
    } catch (e) {
      const ae = e as ApiError;
      setErr(t(`error.${ae.code}`, { defaultValue: ae.message }));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-surface-soft">
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.25, ease: [0.4, 0, 0.2, 1] }}
        className="w-full max-w-[420px]"
      >
        <div className="text-center mb-8">
          <div className="inline-flex size-12 items-center justify-center rounded-xl bg-primary text-primary-fg overflow-hidden">
            {brand.logoDataUrl
              ? <img src={brand.logoDataUrl} alt="" className="size-full object-contain" />
              : <span className="text-lg font-bold">{brand.mark}</span>}
          </div>
          <h1 className="mt-5 text-heading-3 text-ink tracking-tight">{t('app.name', 'Logica ERP')}</h1>
          <p className="mt-1 text-body-sm text-slate">
            {t('login.subtitle', 'Sign in to continue')}
          </p>
        </div>

        <div className="bg-canvas border border-hairline rounded-xl p-6">
          <form onSubmit={submit} className="space-y-4">
            <Field label={t('login.email', 'Email')} htmlFor="email">
              <Input
                id="email" type="email" autoComplete="email" required
                value={email} onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
            <Field label={t('login.password', 'Password')} htmlFor="password">
              <Input
                id="password" type="password" autoComplete="current-password" required minLength={8}
                value={password} onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
            {err && (
              <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">
                {err}
              </div>
            )}
            <Button type="submit" size="lg" loading={busy} className="w-full">
              {!busy && <LogIn className="size-4" />}
              {busy ? t('login.signing_in', 'Signing in…') : t('login.sign_in', 'Sign in')}
            </Button>
          </form>
        </div>

        <p className="mt-6 text-center text-caption text-stone inline-flex items-center justify-center gap-1.5 w-full">
          <Sparkles className="size-3 text-brand-green" />
          Phase 0 preview — design system + shell
        </p>
      </motion.div>
    </div>
  );
}
