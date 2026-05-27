import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, KeyRound, Trash2, Copy, Check, AlertCircle, Eye } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface APIToken {
  id: string; name: string; prefix: string; user_id: string; user_email?: string;
  scopes: string[]; expires_at?: string; last_used_at?: string; revoked_at?: string;
  created_at: string;
}
interface TokenList { items: APIToken[] }
interface CreateResult { token: APIToken; plaintext: string }

export function APITokensSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['api-tokens'], queryFn: () => api<TokenList>('/admin/api-tokens') });
  const items = data?.items ?? [];
  const [createOpen, setCreateOpen] = useState(false);
  const [reveal, setReveal] = useState<CreateResult | null>(null);

  const revoke = useMutation({
    mutationFn: (id: string) => api(`/admin/api-tokens/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-tokens'] }),
  });

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>API tokens</CardTitle>
          <CardDescription>
            Personal access tokens for scripts and integrations. The token plaintext is shown once;
            store it now — it's only the hash that lives in the DB.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> Issue token
        </Button>
      </div>

      <div className="rounded-lg border border-hairline bg-surface-soft p-3 text-caption text-stone">
        <strong className="text-charcoal">Heads up:</strong> tokens are recorded and revocable here, but the bearer-token
        middleware doesn't yet accept them — JWT still required. Wiring is a one-line follow-up in <span className="font-mono">httpx.Auth</span>.
      </div>

      {isLoading ? <Card><Skeleton className="h-32 w-full" /></Card> :
       items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <KeyRound className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No tokens yet.</div>
          </div>
        </Card>
       ) : (
        <Card padded={false}>
          <table className="w-full text-body-sm">
            <thead className="bg-surface-soft border-b border-hairline">
              <tr className="text-micro-uppercase text-stone">
                <th className="text-left  font-medium px-4 py-2.5">Name</th>
                <th className="text-left  font-medium px-4 py-2.5">Prefix</th>
                <th className="text-left  font-medium px-4 py-2.5">User</th>
                <th className="text-left  font-medium px-4 py-2.5">Created</th>
                <th className="text-left  font-medium px-4 py-2.5">Last used</th>
                <th className="text-left  font-medium px-4 py-2.5">Status</th>
                <th className="text-right font-medium px-4 py-2.5"></th>
              </tr>
            </thead>
            <tbody>
              {items.map((t) => (
                <tr key={t.id} className="border-b border-hairline last:border-0">
                  <td className="px-4 py-2 text-ink font-medium">{t.name}</td>
                  <td className="px-4 py-2 text-charcoal font-mono text-caption">{t.prefix}…</td>
                  <td className="px-4 py-2 text-stone truncate max-w-[200px]">{t.user_email || t.user_id}</td>
                  <td className="px-4 py-2 text-stone num">{new Date(t.created_at).toLocaleDateString('id-ID')}</td>
                  <td className="px-4 py-2 text-stone num">{t.last_used_at ? new Date(t.last_used_at).toLocaleString('id-ID') : '—'}</td>
                  <td className="px-4 py-2">
                    {t.revoked_at ? <StatusPill tone="danger">Revoked</StatusPill> : <StatusPill tone="success">Active</StatusPill>}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {!t.revoked_at && (
                      <Button size="sm" variant="ghost"
                        onClick={() => { if (confirm(`Revoke "${t.name}"?`)) revoke.mutate(t.id); }}>
                        <Trash2 className="size-3.5" />
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
       )}

      {createOpen && (
        <CreateDialog
          onClose={() => setCreateOpen(false)}
          onCreated={(r) => { qc.invalidateQueries({ queryKey: ['api-tokens'] }); setCreateOpen(false); setReveal(r); }}
        />
      )}
      {reveal && <RevealDialog result={reveal} onClose={() => setReveal(null)} />}
    </div>
  );
}

function CreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (r: CreateResult) => void }) {
  const [name, setName] = useState('');
  const [expiry, setExpiry] = useState('');
  const [error, setError] = useState<string | null>(null);
  const mut = useMutation({
    mutationFn: () => api<CreateResult>('/admin/api-tokens', {
      method: 'POST',
      body: { name, ...(expiry ? { expires_at: new Date(expiry).toISOString() } : {}) },
    }),
    onSuccess: (r) => onCreated(r),
    onError: (e: Error) => setError(e.message),
  });
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>Issue API token</DialogTitle>
        <DialogDescription>The plaintext is shown next; copy it now.</DialogDescription>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); if (!name.trim()) return setError('Name required'); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="Name" hint="e.g. ‘Tokopedia sync’"><Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus /></Field>
          <Field label="Expires" hint="Optional"><Input type="date" value={expiry} onChange={(e) => setExpiry(e.target.value)} /></Field>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Generate token</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RevealDialog({ result, onClose }: { result: CreateResult; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>Token issued</DialogTitle>
        <DialogDescription>
          Copy this now. Once you close this dialog the plaintext is unrecoverable — only the hash is stored.
        </DialogDescription>
        <div className="mt-4 space-y-3">
          <div className="rounded-md bg-surface-code text-on-dark font-mono text-caption px-3 py-3 break-all">
            {result.plaintext}
          </div>
          <Button onClick={() => { navigator.clipboard.writeText(result.plaintext); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>
            {copied ? <><Check className="size-3.5" /> Copied</> : <><Copy className="size-3.5" /> Copy</>}
          </Button>
          <div className="text-caption text-stone">Prefix shown in lists: <span className="font-mono text-ink">{result.token.prefix}…</span></div>
          <div className="pt-2 border-t border-hairline flex justify-end">
            <Button variant="secondary" onClick={onClose}>Done</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
