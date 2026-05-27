import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Plug, Trash2, AlertCircle, CheckCircle2 } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

/**
 * Generic UI panel for "connector_config" rows of a given kind.
 * Used by PaymentGatewaysSection / BankFeedsSection / MarketplacesSection.
 */

export interface ConnectorFieldDef { key: string; label: string; secret?: boolean }
export interface ProviderDef { kind: string; provider: string; label: string; fields: ConnectorFieldDef[] }
export interface ProviderList { items: ProviderDef[] }

export interface Connector {
  id: string; kind: string; provider: string; name: string;
  company_id?: string; is_enabled: boolean; test_mode: boolean;
  has_credentials: boolean; config?: Record<string, unknown>;
  updated_at: string;
}
export interface ConnectorList { items: Connector[] }

export function ConnectorsPanel({
  kind, title, description,
}: { kind: string; title: string; description: string }) {
  const qc = useQueryClient();
  const { data: providers } = useQuery({
    queryKey: ['connector-providers'],
    queryFn: () => api<ProviderList>('/admin/connectors/providers'),
  });
  const { data, isLoading } = useQuery({
    queryKey: ['connectors', kind],
    queryFn: () => api<ConnectorList>(`/admin/connectors?kind=${kind}`),
  });
  const items = data?.items ?? [];
  const kindProviders = useMemo(() => (providers?.items ?? []).filter((p) => p.kind === kind), [providers, kind]);

  const [editing, setEditing] = useState<Connector | 'new' | null>(null);

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </div>
        <Button size="sm" onClick={() => setEditing('new')}>
          <Plus className="size-3.5" /> Add connector
        </Button>
      </div>

      <div className="rounded-lg border border-hairline bg-surface-soft p-3 text-caption text-stone">
        Credentials are stored. Live API calls per provider are a downstream lift — wiring each provider's SDK is its own piece of work.
      </div>

      {isLoading ? <Card><Skeleton className="h-32 w-full" /></Card> :
       items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Plug className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No {title.toLowerCase()} configured yet.</div>
            <Button size="sm" className="mt-4" onClick={() => setEditing('new')}>
              <Plus className="size-3.5" /> Add one
            </Button>
          </div>
        </Card>
       ) : (
        <div className="space-y-2">
          {items.map((c) => (
            <ConnectorRow key={c.id} connector={c}
              providerLabel={kindProviders.find((p) => p.provider === c.provider)?.label}
              onEdit={() => setEditing(c)}
              onChanged={() => qc.invalidateQueries({ queryKey: ['connectors', kind] })}
            />
          ))}
        </div>
       )}

      {editing && (
        <ConnectorDialog
          kind={kind} mode={editing === 'new' ? 'new' : 'edit'}
          connector={editing === 'new' ? null : editing}
          providers={kindProviders}
          onClose={() => setEditing(null)}
          onSaved={() => { qc.invalidateQueries({ queryKey: ['connectors', kind] }); setEditing(null); }}
        />
      )}
    </div>
  );
}

function ConnectorRow({
  connector, providerLabel, onEdit, onChanged,
}: { connector: Connector; providerLabel?: string; onEdit: () => void; onChanged: () => void }) {
  const del = useMutation({
    mutationFn: () => api(`/admin/connectors/${connector.id}`, { method: 'DELETE' }),
    onSuccess: () => onChanged(),
  });
  return (
    <div className="bg-canvas border border-hairline rounded-lg p-3.5 flex items-center gap-3">
      <span className="size-9 rounded-md bg-surface text-ink inline-flex items-center justify-center shrink-0">
        <Plug className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-body-sm font-medium text-ink">{connector.name}</span>
          <span className="text-caption font-mono text-stone">{providerLabel ?? connector.provider}</span>
          {connector.is_enabled
            ? <StatusPill tone="success" withDot={false}><CheckCircle2 className="size-3" /> Live</StatusPill>
            : <StatusPill tone="neutral" withDot={false}>Disabled</StatusPill>}
          {connector.test_mode && <StatusPill tone="warning" withDot={false}>Test mode</StatusPill>}
        </div>
        <div className="text-caption text-stone">
          {connector.has_credentials ? 'Credentials set' : 'No credentials yet'} ·
          updated {new Date(connector.updated_at).toLocaleDateString('id-ID')}
        </div>
      </div>
      <Button size="sm" variant="secondary" onClick={onEdit}>Edit</Button>
      <Button size="sm" variant="ghost"
        onClick={() => { if (confirm(`Delete "${connector.name}"?`)) del.mutate(); }}>
        <Trash2 className="size-3.5" />
      </Button>
    </div>
  );
}

function ConnectorDialog({
  kind, mode, connector, providers, onClose, onSaved,
}: {
  kind: string;
  mode: 'new' | 'edit';
  connector: Connector | null;
  providers: ProviderDef[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [provider, setProvider]   = useState(connector?.provider ?? providers[0]?.provider ?? '');
  const [name, setName]           = useState(connector?.name ?? (providers[0]?.label ?? ''));
  const [isEnabled, setIsEnabled] = useState(connector?.is_enabled ?? false);
  const [testMode, setTestMode]   = useState(connector?.test_mode ?? true);
  const [credentials, setCreds]   = useState<Record<string, string>>({});
  const [error, setError]         = useState<string | null>(null);

  const providerDef = providers.find((p) => p.provider === provider);

  const save = useMutation({
    mutationFn: () => {
      const body: Record<string, unknown> = {
        kind, provider, name, is_enabled: isEnabled, test_mode: testMode,
      };
      if (Object.keys(credentials).length > 0) body.credentials = credentials;
      return mode === 'new'
        ? api('/admin/connectors',                { method: 'POST', body })
        : api(`/admin/connectors/${connector!.id}`, { method: 'PUT', body });
    },
    onSuccess: () => onSaved(),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-xl">
        <DialogTitle>{mode === 'new' ? 'New connector' : `Edit ${connector?.name}`}</DialogTitle>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); if (!name.trim() || !provider) return setError('Name and provider required'); save.mutate(); }}
          className="mt-4 space-y-3">
          {mode === 'new' && (
            <Field label="Provider">
              <select className="input-base" value={provider} onChange={(e) => { setProvider(e.target.value); setName(providers.find(p => p.provider === e.target.value)?.label ?? e.target.value); }}>
                {providers.map((p) => <option key={p.provider} value={p.provider}>{p.label}</option>)}
              </select>
            </Field>
          )}
          <Field label="Display name"><Input value={name} onChange={(e) => setName(e.target.value)} required /></Field>
          {providerDef && (
            <div>
              <div className="label-base mb-1.5">Credentials & config</div>
              <div className="grid sm:grid-cols-2 gap-3">
                {providerDef.fields.map((f) => (
                  <Field key={f.key} label={f.label} hint={f.secret ? (connector?.has_credentials ? 'Set — leave blank to keep current' : 'Sensitive — store-only') : ''}>
                    <Input
                      type={f.secret ? 'password' : 'text'}
                      placeholder={f.secret && connector?.has_credentials ? '••••••••' : ''}
                      value={credentials[f.key] ?? ''}
                      onChange={(e) => setCreds({ ...credentials, [f.key]: e.target.value })}
                      autoComplete="off"
                    />
                  </Field>
                ))}
              </div>
            </div>
          )}
          <div className="flex items-center gap-5 flex-wrap">
            <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
              <input type="checkbox" className="accent-brand-green-deep" checked={isEnabled} onChange={(e) => setIsEnabled(e.target.checked)} />
              Enabled
            </label>
            <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
              <input type="checkbox" className="accent-brand-green-deep" checked={testMode} onChange={(e) => setTestMode(e.target.checked)} />
              Test / sandbox mode
            </label>
          </div>
          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={save.isPending}>{mode === 'new' ? 'Create' : 'Save'}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export const PaymentGatewaysSection = () => (
  <ConnectorsPanel
    kind="payment_gateway"
    title="Payment gateways"
    description="Connect Indonesian payment providers (Midtrans, Xendit, DOKU, iPaymu)."
  />
);
export const BankFeedsSection = () => (
  <ConnectorsPanel
    kind="bank_feed"
    title="Bank feeds"
    description="Import bank statements. Manual upload for big-four banks; direct connect via Brick / Finantier."
  />
);
export const MarketplacesSection = () => (
  <ConnectorsPanel
    kind="marketplace"
    title="Marketplaces"
    description="Sync orders + inventory with Tokopedia, Shopee, TikTok Shop, Lazada."
  />
);
