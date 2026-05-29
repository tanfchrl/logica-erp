import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Sparkles, Save, Check, X, RefreshCw, KeyRound, AlertCircle, ChevronDown } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Skeleton } from '@/components/EmptyState';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';

// ---- Types mirror internal/agent/agentllmconfig.LLMConfig / SaveInput ----
// The backend column shape is unchanged (provider/base_url/model/key), but the
// agent now speaks the native Anthropic Messages API, so this page is
// Anthropic-only: provider is always "anthropic" and base_url is an advanced
// override that defaults to https://api.anthropic.com server-side.
interface LLMConfig {
  id: string;
  company_id: string;
  provider: string;
  base_url: string;
  model: string;
  api_key_last4?: string;
  api_key_present: boolean;
  is_active: boolean;
  updated_at: string;
}
interface SaveInput {
  provider: string;
  base_url: string;
  model: string;
  api_key?: string;        // omit = keep existing
  clear_api_key?: boolean; // true = wipe
  is_active: boolean;
}
interface TestResp { ok: boolean; error?: string }

const PROVIDER = 'anthropic';
const DEFAULT_BASE_URL = 'https://api.anthropic.com';

// Curated Claude catalog. The agent sends the exact `id` to Anthropic. Keep
// these in sync with internal/agent/audit/costs.go so usage reporting prices
// correctly. The "custom" escape hatch lets an admin pin any model id (e.g. a
// dated snapshot) the catalog doesn't list yet.
interface ModelOption { id: string; label: string; hint: string }
const MODELS: ModelOption[] = [
  { id: 'claude-opus-4-8',   label: 'Claude Opus 4.8',   hint: 'Most capable. Best for complex reasoning. Highest cost.' },
  { id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6', hint: 'Balanced capability and cost. Recommended for most workloads.' },
  { id: 'claude-haiku-4-5',  label: 'Claude Haiku 4.5',  hint: 'Fastest and cheapest. Good for high-volume, simple tasks.' },
];
const CUSTOM = '__custom__';

export function AIModelSection() {
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({
    queryKey: ['agent-llm-config'],
    queryFn:  () => api<LLMConfig>('/admin/agent-llm-config'),
  });

  const [model, setModel] = useState('');
  const [customModel, setCustomModel] = useState('');
  const [isActive, setIsActive] = useState(true);
  const [baseURL, setBaseURL] = useState('');
  const [showAdvanced, setShowAdvanced] = useState(false);

  // API key replace-mode. When false we show ••••last4 + Replace. When true
  // the input is editable and Save overwrites the stored key (unless cleared).
  const [apiKeyPresent, setApiKeyPresent] = useState(false);
  const [apiKeyLast4, setApiKeyLast4] = useState<string | undefined>();
  const [replaceMode, setReplaceMode] = useState(false);
  const [newKey, setNewKey] = useState('');

  const [saved, setSaved] = useState(false);
  const [testResult, setTestResult] = useState<TestResp | null>(null);

  // Hydrate local form state from the server row.
  useEffect(() => {
    if (!data) return;
    const known = MODELS.some((m) => m.id === data.model);
    setModel(data.model ? (known ? data.model : CUSTOM) : 'claude-sonnet-4-6');
    setCustomModel(known ? '' : (data.model ?? ''));
    setIsActive(data.id ? data.is_active : true);
    setBaseURL(data.base_url ?? '');
    setApiKeyPresent(data.api_key_present);
    setApiKeyLast4(data.api_key_last4);
    setReplaceMode(!data.api_key_present); // first-time = key prompt open
    setNewKey('');
    setShowAdvanced(Boolean(data.base_url && data.base_url !== DEFAULT_BASE_URL));
    setTestResult(null);
  }, [data]);

  const save = useMutation({
    mutationFn: (input: SaveInput) =>
      api<LLMConfig>('/admin/agent-llm-config', { method: 'POST', body: input }),
    onSuccess: () => {
      setSaved(true);
      setNewKey('');
      setReplaceMode(false);
      setTimeout(() => setSaved(false), 2500);
      void qc.invalidateQueries({ queryKey: ['agent-llm-config'] });
    },
  });

  const reload = useMutation({
    mutationFn: () => api<{ status: string }>('/admin/agent-llm-config/reload', { method: 'POST' }),
  });

  const test = useMutation({
    mutationFn: (input: SaveInput) =>
      api<TestResp>('/admin/agent-llm-config/test', { method: 'POST', body: input }),
    onSuccess: (r) => setTestResult(r),
    onError:   (e) => setTestResult({ ok: false, error: (e as Error).message }),
  });

  if (isLoading) {
    return <Card><Skeleton className="h-40 w-full" /></Card>;
  }

  const effectiveModel = model === CUSTOM ? customModel.trim() : model;
  const modelHint = MODELS.find((m) => m.id === model)?.hint;

  function buildSaveInput(opts: { clearKey?: boolean } = {}): SaveInput {
    const input: SaveInput = {
      provider:  PROVIDER,
      base_url:  showAdvanced ? baseURL.trim() : '', // blank → server defaults to api.anthropic.com
      model:     effectiveModel,
      is_active: isActive,
    };
    if (opts.clearKey) {
      input.clear_api_key = true;
    } else if (replaceMode && newKey) {
      input.api_key = newKey;
    }
    // else: omit api_key → service keeps the existing one
    return input;
  }

  const canSave = effectiveModel.length > 0 && (apiKeyPresent || newKey.length > 0);

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>
            <Sparkles className="size-4 inline mr-1.5 text-accent" />
            AI Model
          </CardTitle>
          <CardDescription>
            The agent runs on Anthropic&rsquo;s Claude models. Add your API key and
            pick a model. Per company; takes effect within ~60s of saving (or click Reload).
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="secondary" onClick={() => reload.mutate()} loading={reload.isPending}>
            <RefreshCw className="size-4" /> Reload
          </Button>
          <Button onClick={() => save.mutate(buildSaveInput())} loading={save.isPending} disabled={!canSave}>
            <Save className="size-4" /> Save
          </Button>
        </div>
      </div>

      {error && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">{(error as Error).message}</div>
        </Card>
      )}
      {save.isError && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">
            Save failed: {(save.error as { message?: string })?.message ?? 'unknown error'}
          </div>
        </Card>
      )}
      {saved && (
        <Card className="border-l-4 border-l-brand-green-deep">
          <div className="text-body-sm">Saved. The agent picks up the new config on the next chat turn.</div>
        </Card>
      )}

      <Card>
        <CardTitle><KeyRound className="size-4 inline mr-1.5 text-accent" /> Anthropic API key</CardTitle>
        <CardDescription>
          Create one at console.anthropic.com → API keys. Stored encrypted at rest
          (AES-256-GCM); never returned to the browser after save.
        </CardDescription>
        <div className="mt-4 space-y-3">
          {!replaceMode && apiKeyPresent ? (
            <div className="flex items-center gap-3 flex-wrap">
              <code className="px-3 py-1.5 rounded bg-stone-50 border border-stone-200 text-body-sm font-mono">
                sk-ant-••••••••{apiKeyLast4 ?? '????'}
              </code>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => { setReplaceMode(true); setNewKey(''); setTestResult(null); }}
              >
                Replace
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  if (confirm('Clear the stored API key? The agent will fall back to the container env key (if any).')) {
                    save.mutate(buildSaveInput({ clearKey: true }));
                  }
                }}
              >
                Clear
              </Button>
            </div>
          ) : (
            <div className="space-y-2">
              <Field label="Paste key" hint="Stored encrypted. You won't be able to read it back.">
                <Input
                  type="password"
                  autoComplete="off"
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                  placeholder="sk-ant-..."
                />
              </Field>
              {apiKeyPresent && (
                <button
                  type="button"
                  className="text-caption text-stone hover:text-charcoal underline"
                  onClick={() => { setReplaceMode(false); setNewKey(''); }}
                >
                  Cancel — keep existing key
                </button>
              )}
            </div>
          )}
        </div>
      </Card>

      <Card>
        <CardTitle>Model</CardTitle>
        <CardDescription>Which Claude model the agent calls.</CardDescription>
        <div className="mt-4 space-y-3">
          <div className="grid gap-2">
            {MODELS.map((m) => (
              <label
                key={m.id}
                className={cn(
                  'flex items-start gap-3 rounded-md border px-3 py-2.5 cursor-pointer transition-colors',
                  model === m.id
                    ? 'border-brand-green-deep bg-brand-green/10'
                    : 'border-stone-200 hover:bg-stone-50',
                )}
              >
                <input
                  type="radio"
                  name="claude-model"
                  className="mt-1 accent-brand-green-deep"
                  checked={model === m.id}
                  onChange={() => { setModel(m.id); setTestResult(null); }}
                />
                <div>
                  <div className="text-body-sm text-charcoal font-medium">{m.label}</div>
                  <div className="text-caption text-stone">{m.hint}</div>
                  <code className="text-caption text-stone font-mono">{m.id}</code>
                </div>
              </label>
            ))}
            <label
              className={cn(
                'flex items-start gap-3 rounded-md border px-3 py-2.5 cursor-pointer transition-colors',
                model === CUSTOM
                  ? 'border-brand-green-deep bg-brand-green/10'
                  : 'border-stone-200 hover:bg-stone-50',
              )}
            >
              <input
                type="radio"
                name="claude-model"
                className="mt-1 accent-brand-green-deep"
                checked={model === CUSTOM}
                onChange={() => { setModel(CUSTOM); setTestResult(null); }}
              />
              <div className="flex-1">
                <div className="text-body-sm text-charcoal font-medium">Custom model ID</div>
                <div className="text-caption text-stone">Pin any Claude model id, e.g. a dated snapshot.</div>
                {model === CUSTOM && (
                  <Input
                    className="mt-2"
                    value={customModel}
                    onChange={(e) => setCustomModel(e.target.value)}
                    placeholder="claude-sonnet-4-6"
                  />
                )}
              </div>
            </label>
          </div>
          {modelHint && (
            <div className="text-caption text-stone flex items-start gap-1.5">
              <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
              <span>{modelHint}</span>
            </div>
          )}
        </div>
      </Card>

      <Card>
        <label className="flex items-start gap-3 cursor-pointer">
          <input
            type="checkbox"
            className="mt-1 accent-brand-green-deep"
            checked={isActive}
            onChange={(e) => setIsActive(e.target.checked)}
          />
          <div>
            <div className="text-body-sm text-charcoal">Active</div>
            <div className="text-caption text-stone">When off, the agent falls back to the container env key/model (if any).</div>
          </div>
        </label>

        <div className="mt-4 border-t border-stone-100 pt-3">
          <button
            type="button"
            className="text-caption text-stone hover:text-charcoal inline-flex items-center gap-1"
            onClick={() => setShowAdvanced((v) => !v)}
          >
            <ChevronDown className={cn('size-3.5 transition-transform', showAdvanced && 'rotate-180')} />
            Advanced
          </button>
          {showAdvanced && (
            <div className="mt-3">
              <Field label="API base URL override" hint={`Default: ${DEFAULT_BASE_URL}. Only change this for an Anthropic-compatible gateway.`}>
                <Input
                  value={baseURL}
                  onChange={(e) => setBaseURL(e.target.value)}
                  placeholder={DEFAULT_BASE_URL}
                />
              </Field>
            </div>
          )}
        </div>
      </Card>

      <Card>
        <CardTitle>Test connection</CardTitle>
        <CardDescription>
          Sends a one-token ping to Anthropic using the in-form values (key from the
          form if you&rsquo;re replacing it, otherwise the stored key).
        </CardDescription>
        <div className="mt-4 flex items-center gap-3 flex-wrap">
          <Button
            variant="secondary"
            onClick={() => { setTestResult(null); test.mutate(buildSaveInput()); }}
            loading={test.isPending}
            disabled={!effectiveModel}
          >
            Test now
          </Button>
          {testResult && (
            testResult.ok ? (
              <span className="inline-flex items-center gap-1.5 text-body-sm text-brand-green-deep">
                <Check className="size-4" /> Reached Anthropic successfully — click Save to apply.
              </span>
            ) : (
              <span className="inline-flex items-center gap-1.5 text-body-sm text-brand-error">
                <X className="size-4" /> {testResult.error ?? 'Failed.'}
              </span>
            )
          )}
        </div>
      </Card>
    </div>
  );
}
