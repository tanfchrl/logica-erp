import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Sparkles, Save, Check, X, RefreshCw, KeyRound, AlertCircle } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';

// ---- Types mirror internal/agent/agentllmconfig.Config / SaveInput ----
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
  provider?: string;
  base_url: string;
  model: string;
  api_key?: string;        // omit = keep existing
  clear_api_key?: boolean; // true = wipe
  is_active?: boolean;
}
interface TestResp { ok: boolean; error?: string }

// Provider presets — picking one prefills base_url + a model hint. Free-text
// always wins; presets are just convenience.
interface Preset {
  key: string;
  label: string;
  baseURL: string;
  defaultModel: string;
  modelHint: string;
}
const PRESETS: Preset[] = [
  { key: 'openai',    label: 'OpenAI',                baseURL: 'https://api.openai.com/v1',                    defaultModel: 'gpt-4o-mini',                 modelHint: 'gpt-4o, gpt-4o-mini, gpt-4-turbo' },
  { key: 'anthropic', label: 'Anthropic (via proxy)', baseURL: '',                                             defaultModel: 'claude-opus-4-7',             modelHint: 'Anthropic SDK is not OpenAI-compatible — use a proxy (LiteLLM, OpenRouter) and paste its URL.' },
  { key: 'azure',     label: 'Azure OpenAI',          baseURL: 'https://YOUR-RESOURCE.openai.azure.com/openai',defaultModel: 'gpt-4o-mini',                 modelHint: 'Deployment name (not model name). URL ends in /openai.' },
  { key: 'litellm',   label: 'LiteLLM proxy',         baseURL: 'http://localhost:4000/v1',                     defaultModel: 'openai/gpt-4o-mini',          modelHint: 'Any model the proxy is configured for.' },
  { key: 'local',     label: 'Local (Ollama / vLLM)', baseURL: 'http://localhost:11434/v1',                    defaultModel: 'llama3.1',                    modelHint: 'Ollama: pull the model first. vLLM: depends on --model.' },
  { key: 'custom',    label: 'Custom',                baseURL: '',                                             defaultModel: '',                            modelHint: 'Any OpenAI /chat/completions-compatible endpoint.' },
];

export function AIModelSection() {
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({
    queryKey: ['agent-llm-config'],
    queryFn:  () => api<LLMConfig>('/admin/agent-llm-config'),
  });

  const [form, setForm] = useState<LLMConfig | null>(null);
  // Replace-mode tracks whether the user is editing the key. When false, we
  // show the masked •••• + last4 + Replace button. When true, the password
  // input is editable and the saved key (whatever it is) gets overwritten on
  // Save — unless they cancel.
  const [replaceMode, setReplaceMode] = useState(false);
  const [newKey, setNewKey] = useState('');
  const [saved, setSaved] = useState(false);
  const [testResult, setTestResult] = useState<TestResp | null>(null);

  useEffect(() => {
    if (data) {
      setForm(data);
      setReplaceMode(!data.api_key_present); // first-time = key prompt is open
      setNewKey('');
      setTestResult(null);
    }
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

  if (isLoading || !form) {
    return <Card><Skeleton className="h-40 w-full" /></Card>;
  }

  function setF<K extends keyof LLMConfig>(key: K, v: LLMConfig[K]) {
    setForm((p) => (p ? { ...p, [key]: v } : p));
  }

  function applyPreset(key: string) {
    const p = PRESETS.find((x) => x.key === key);
    if (!p) return;
    setForm((prev) =>
      prev
        ? {
            ...prev,
            provider: p.key,
            base_url: p.baseURL || prev.base_url,
            model:    p.defaultModel || prev.model,
          }
        : prev,
    );
    setTestResult(null);
  }

  function buildSaveInput(opts: { clearKey?: boolean } = {}): SaveInput {
    const input: SaveInput = {
      provider:  form?.provider,
      base_url:  form?.base_url ?? '',
      model:     form?.model ?? '',
      is_active: form?.is_active ?? true,
    };
    if (opts.clearKey) {
      input.clear_api_key = true;
    } else if (replaceMode && newKey) {
      input.api_key = newKey;
    }
    // else: omit api_key → service keeps the existing one
    return input;
  }

  const activePreset = PRESETS.find((p) => p.key === form.provider) ?? PRESETS[PRESETS.length - 1]!;

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>
            <Sparkles className="size-4 inline mr-1.5 text-accent" />
            AI Model (BYOM)
          </CardTitle>
          <CardDescription>
            Point the agent at any OpenAI-compatible chat endpoint. Per company;
            takes effect within ~60s of saving (or click Reload).
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="secondary" onClick={() => reload.mutate()} loading={reload.isPending}>
            <RefreshCw className="size-4" /> Reload
          </Button>
          <Button onClick={() => save.mutate(buildSaveInput())} loading={save.isPending}>
            <Save className="size-4" /> Save
          </Button>
        </div>
      </div>

      {error && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">{(error as Error).message}</div>
        </Card>
      )}
      {saved && (
        <Card className="border-l-4 border-l-brand-green-deep">
          <div className="text-body-sm">Saved. Agent will pick up the new config on the next chat turn.</div>
        </Card>
      )}

      <Card>
        <CardTitle>Provider preset</CardTitle>
        <CardDescription>Pick the closest preset to auto-fill URL + a model name. You can override either.</CardDescription>
        <div className="mt-4 grid grid-cols-2 md:grid-cols-3 gap-2">
          {PRESETS.map((p) => (
            <button
              key={p.key}
              type="button"
              onClick={() => applyPreset(p.key)}
              className={
                'rounded-md border px-3 py-2 text-left text-body-sm transition-colors ' +
                (form.provider === p.key
                  ? 'border-brand-green-deep bg-brand-green/10 text-charcoal'
                  : 'border-stone-200 hover:bg-stone-50 text-charcoal')
              }
            >
              <div className="font-medium">{p.label}</div>
              <div className="text-caption text-stone truncate">{p.baseURL || '—'}</div>
            </button>
          ))}
        </div>
        <div className="mt-3 text-caption text-stone flex items-start gap-1.5">
          <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
          <span>{activePreset.modelHint}</span>
        </div>
      </Card>

      <Card>
        <CardTitle>Endpoint</CardTitle>
        <CardDescription>OpenAI-compatible base URL. Path must end where /chat/completions sits.</CardDescription>
        <div className="mt-4 space-y-3">
          <Field label="Base URL" hint="Example: https://api.openai.com/v1">
            <Input
              value={form.base_url}
              onChange={(e) => setF('base_url', e.target.value)}
              placeholder="https://api.openai.com/v1"
            />
          </Field>
          <Field label="Model" hint="Free text — must match what the provider serves.">
            <Input
              value={form.model}
              onChange={(e) => setF('model', e.target.value)}
              placeholder="gpt-4o-mini"
            />
          </Field>
          <label className="flex items-start gap-3 cursor-pointer">
            <input
              type="checkbox"
              className="mt-1 accent-brand-green-deep"
              checked={form.is_active}
              onChange={(e) => setF('is_active', e.target.checked)}
            />
            <div>
              <div className="text-body-sm text-charcoal">Active</div>
              <div className="text-caption text-stone">When off, the agent falls back to the container env vars (if any).</div>
            </div>
          </label>
        </div>
      </Card>

      <Card>
        <CardTitle><KeyRound className="size-4 inline mr-1.5 text-accent" /> API key</CardTitle>
        <CardDescription>Stored encrypted at rest (AES-256-GCM). Never returned to the browser after save.</CardDescription>
        <div className="mt-4 space-y-3">
          {!replaceMode && form.api_key_present ? (
            <div className="flex items-center gap-3">
              <code className="px-3 py-1.5 rounded bg-stone-50 border border-stone-200 text-body-sm font-mono">
                ••••••••••••{form.api_key_last4 ?? '????'}
              </code>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  setReplaceMode(true);
                  setNewKey('');
                  setTestResult(null);
                }}
              >
                Replace
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  if (confirm('Clear the stored API key? The agent will fall back to container env.')) {
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
                  placeholder="sk-..."
                />
              </Field>
              {form.api_key_present && (
                <button
                  type="button"
                  className="text-caption text-stone hover:text-charcoal underline"
                  onClick={() => {
                    setReplaceMode(false);
                    setNewKey('');
                  }}
                >
                  Cancel — keep existing key
                </button>
              )}
            </div>
          )}
        </div>
      </Card>

      <Card>
        <CardTitle>Test connection</CardTitle>
        <CardDescription>Sends a tiny prompt to the configured endpoint using the in-form values (key from the form if you're replacing it, otherwise the stored key).</CardDescription>
        <div className="mt-4 flex items-center gap-3 flex-wrap">
          <Button
            variant="secondary"
            onClick={() => {
              setTestResult(null);
              test.mutate(buildSaveInput());
            }}
            loading={test.isPending}
          >
            Test now
          </Button>
          {testResult && (
            testResult.ok ? (
              <span className="inline-flex items-center gap-1.5 text-body-sm text-brand-green-deep">
                <Check className="size-4" /> Reached the endpoint successfully.
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
