import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ClipboardList, Save, AlertCircle } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field } from '@/components/Input';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';

interface AssetSettings {
  id: string;
  company_id: string;
  auto_create_assets_from_pi: boolean;
  default_finance_book_id?: string;
  register_show_zero_nbv: boolean;
  register_group_by: 'category' | 'status' | 'location' | 'none';
}
interface FinanceBook { id: string; name: string; is_primary: boolean }

export function AssetSettingsSection() {
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({
    queryKey: ['asset-settings'],
    queryFn:  () => api<AssetSettings>('/admin/asset-settings'),
  });
  const { data: booksResp } = useQuery({
    queryKey: ['finance-books'],
    queryFn:  () => api<{ items: FinanceBook[] }>('/assets/finance-books'),
  });

  const [form, setForm] = useState<AssetSettings | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => { if (data) setForm(data); }, [data]);

  const save = useMutation({
    mutationFn: () => api<AssetSettings>('/admin/asset-settings', {
      method: 'POST',
      body: {
        auto_create_assets_from_pi: form?.auto_create_assets_from_pi ?? true,
        default_finance_book_id:    form?.default_finance_book_id || undefined,
        register_show_zero_nbv:     form?.register_show_zero_nbv ?? false,
        register_group_by:          form?.register_group_by ?? 'category',
      },
    }),
    onSuccess: () => {
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
      void qc.invalidateQueries({ queryKey: ['asset-settings'] });
    },
  });

  function setF<K extends keyof AssetSettings>(key: K, v: AssetSettings[K]) {
    setForm((p) => p ? ({ ...p, [key]: v }) : p);
  }

  if (isLoading || !form) {
    return <Card><Skeleton className="h-40 w-full" /></Card>;
  }

  const books = booksResp?.items ?? [];

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>
            <ClipboardList className="size-4 inline mr-1.5 text-accent" />
            Asset Settings
          </CardTitle>
          <CardDescription>
            Per-company switches for the Assets module. System administrators only.
          </CardDescription>
        </div>
        <Button onClick={() => save.mutate()} loading={save.isPending}>
          <Save className="size-4" /> Save
        </Button>
      </div>

      {error && (
        <Card className="border-l-4 border-l-brand-error">
          <div className="text-body-sm text-brand-error">{(error as Error).message}</div>
        </Card>
      )}
      {saved && (
        <Card className="border-l-4 border-l-brand-green-deep">
          <div className="text-body-sm">Saved. Settings take effect on the next PI submit / report load.</div>
        </Card>
      )}

      <Card>
        <CardTitle>Auto-create from Purchase Invoice</CardTitle>
        <CardDescription>When on, PI submit materialises a draft Asset per unit for items flagged "Fixed asset".</CardDescription>
        <div className="mt-4 space-y-3">
          <label className="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" className="mt-1 accent-brand-green-deep"
              checked={form.auto_create_assets_from_pi}
              onChange={(e) => setF('auto_create_assets_from_pi', e.target.checked)} />
            <div>
              <div className="text-body-sm text-charcoal">Create asset drafts automatically</div>
              <div className="text-caption text-stone">Off = the flag on each item is ignored. Use this to suspend auto-create company-wide.</div>
            </div>
          </label>
        </div>
      </Card>

      <Card>
        <CardTitle>Finance Books</CardTitle>
        <CardDescription>When set, new submitted assets get suggested to attach this book (e.g. a Tax Book).</CardDescription>
        <div className="mt-4">
          <Field label="Default secondary book" hint="Leave blank to skip the suggestion.">
            <select className="input-base"
              value={form.default_finance_book_id ?? ''}
              onChange={(e) => setF('default_finance_book_id', e.target.value)}>
              <option value="">— None —</option>
              {books.filter((b) => !b.is_primary).map((b) => (
                <option key={b.id} value={b.id}>{b.name}</option>
              ))}
            </select>
          </Field>
          {books.filter((b) => !b.is_primary).length === 0 && (
            <div className="mt-2 text-caption text-stone flex items-start gap-1.5">
              <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
              <span>No secondary books exist yet. Create one via the Finance Books admin endpoint, then come back.</span>
            </div>
          )}
        </div>
      </Card>

      <Card>
        <CardTitle>Fixed Asset Register defaults</CardTitle>
        <CardDescription>Initial grouping + zero-NBV visibility on the Fixed Asset Register report.</CardDescription>
        <div className="mt-4 space-y-3">
          <Field label="Group by">
            <select className="input-base"
              value={form.register_group_by}
              onChange={(e) => setF('register_group_by', e.target.value as AssetSettings['register_group_by'])}>
              <option value="category">Category</option>
              <option value="status">Status</option>
              <option value="location">Location</option>
              <option value="none">No grouping</option>
            </select>
          </Field>
          <label className="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" className="mt-1 accent-brand-green-deep"
              checked={form.register_show_zero_nbv}
              onChange={(e) => setF('register_show_zero_nbv', e.target.checked)} />
            <div>
              <div className="text-body-sm text-charcoal">Show fully-depreciated assets (NBV = 0)</div>
              <div className="text-caption text-stone">Off (default) hides assets whose NBV reached zero but aren't sold/scrapped.</div>
            </div>
          </label>
        </div>
      </Card>
    </div>
  );
}
