import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { motion } from 'framer-motion';
import { ArrowLeft, Save, AlertTriangle, ExternalLink, Sparkles } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { NumericInput } from '@/components/NumericInput';
import { Combobox } from '@/components/Combobox';
import { Kbd } from '@/components/Kbd';
import { api } from '@/lib/api';
import { toast } from '@/components/Toaster';
import { cn } from '@/lib/cn';
import type { DoctypeConfig } from '@/lib/doctypes';
import type { CreateSchema, FieldDef } from '@/lib/createSchema';

interface CreateFormPageProps {
  config: DoctypeConfig;
  schema: CreateSchema;
}

export function CreateFormPage({ config, schema }: CreateFormPageProps) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const listPath = `${config.modulePath}/${config.slug}`;

  // Seed default values
  const initial = useMemo(() => {
    const obj: Record<string, any> = {};
    for (const f of schema.fields) if (f.default !== undefined) obj[f.name] = f.default;
    return obj;
  }, [schema]);
  const [values, setValues] = useState<Record<string, any>>(initial);
  const set = (name: string, v: any) => setValues((s) => ({ ...s, [name]: v }));

  // ---- needsChildTable: friendly stub ----
  if (schema.needsChildTable) {
    return (
      <>
        <PageHeader
          crumbs={[{ label: config.module, to: config.modulePath }, { label: config.title, to: listPath }, { label: 'New' }]}
          title={`New ${config.singular}`}
          actions={
            <Button variant="ghost" asChild>
              <Link to={listPath as never}><ArrowLeft className="size-4" /> Back to list</Link>
            </Button>
          }
        />
        <div className="flex-1 px-6 lg:px-8 pt-6 pb-8 max-w-2xl">
          <Card className="!p-8">
            <div className="flex items-start gap-4">
              <div className="size-10 rounded-full bg-warning/10 text-warning inline-flex items-center justify-center shrink-0">
                <AlertTriangle className="size-5" />
              </div>
              <div className="flex-1">
                <CardTitle>Bespoke form coming next iteration</CardTitle>
                <CardDescription>
                  {config.singular} requires <strong>{schema.needsChildTable.label}</strong> — a child-table editor that
                  isn't in this slice yet. The backend endpoint is fully functional;
                  create one via API docs in the meantime.
                </CardDescription>
                <div className="mt-5 flex flex-wrap gap-2">
                  <Button asChild>
                    <a href="/api/v1/docs" target="_blank" rel="noreferrer">
                      <ExternalLink className="size-4" /> Open API docs <Kbd>↗</Kbd>
                    </a>
                  </Button>
                  <Button variant="secondary" asChild>
                    <Link to={listPath as never}>Back to {config.title}</Link>
                  </Button>
                </div>
              </div>
            </div>
          </Card>

          <Card className="mt-4 !p-5 bg-accent-soft/40 border-accent/10">
            <div className="flex gap-3">
              <Sparkles className="size-4 text-accent shrink-0 mt-0.5" />
              <div>
                <div className="text-body text-text-primary font-medium">Working examples already shipped</div>
                <p className="mt-1 text-caption text-text-secondary">
                  The <Link to={'/accounting/sales-invoices/new' as never} className="text-accent hover:underline">Sales Invoice</Link> and{' '}
                  <Link to={'/accounting/journal-entries/new' as never} className="text-accent hover:underline">Journal Entry</Link> forms
                  show the child-table pattern that other docs will follow.
                </p>
              </div>
            </div>
          </Card>
        </div>
      </>
    );
  }

  // ---- regular create form ----
  const createMutation = useMutation({
    mutationFn: () => {
      // Strip empty strings and undefineds so the server gets clean JSON.
      const body: Record<string, any> = {};
      for (const f of schema.fields) {
        const v = values[f.name];
        if (v === '' || v === undefined || v === null) continue;
        if (f.kind === 'number' || f.kind === 'money') {
          body[f.name] = typeof v === 'number' ? v : String(v);
        } else {
          body[f.name] = v;
        }
      }
      return api<any>(config.endpoint, { method: 'POST', body });
    },
    onSuccess: (resp) => {
      toast.success(`${config.singular} created`, resp?.name ?? resp?.id ?? '');
      qc.invalidateQueries({ queryKey: ['doctype', config.endpoint] });
      if (schema.redirectTo && resp?.id) {
        navigate({ to: schema.redirectTo(resp.id) as never });
      } else {
        navigate({ to: listPath as never });
      }
    },
    onError: (e: any) => toast.error('Create failed', e?.message ?? 'Check inputs / API logs'),
  });

  const missing = schema.fields.some((f) => f.required && !values[f.name]);

  return (
    <>
      <PageHeader
        crumbs={[{ label: config.module, to: config.modulePath }, { label: config.title, to: listPath }, { label: 'New' }]}
        title={`New ${config.singular}`}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={listPath as never}><ArrowLeft className="size-4" /> Back</Link>
            </Button>
            <Button onClick={() => createMutation.mutate()} disabled={missing} loading={createMutation.isPending}>
              <Save className="size-4" /> Create {config.singular}
            </Button>
          </>
        }
      />

      <motion.div
        initial={{ opacity: 0, y: 4 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.15 }}
        className="flex-1 px-6 lg:px-8 pt-6 pb-8 max-w-3xl"
      >
        {schema.notice && (
          <Card className="!p-3 mb-4 bg-warning/5 border-warning/30 flex items-start gap-3">
            <AlertTriangle className="size-4 text-warning shrink-0 mt-0.5" />
            <p className="text-body text-text-secondary">{schema.notice}</p>
          </Card>
        )}

        <Card>
          <CardTitle>Details</CardTitle>
          <CardDescription>
            Fill in the essentials. Optional fields can be edited via the API or in a future bespoke form.
          </CardDescription>
          <div className="mt-5 grid grid-cols-1 sm:grid-cols-2 gap-4">
            {schema.fields.map((f) => (
              <div key={f.name} className={cn(f.span === 2 ? 'sm:col-span-2' : '')}>
                <FieldControl
                  field={f}
                  value={values[f.name]}
                  onChange={(v) => set(f.name, v)}
                />
              </div>
            ))}
          </div>
        </Card>
      </motion.div>
    </>
  );
}

// ----- Individual field renderer -----
function FieldControl({ field, value, onChange }: { field: FieldDef; value: any; onChange: (v: any) => void }) {
  const fieldId = `f-${field.name}`;

  switch (field.kind) {
    case 'text':
      return (
        <Field label={field.label + (field.required ? ' *' : '')} htmlFor={fieldId} hint={field.hint}>
          <Input id={fieldId} value={value ?? ''} onChange={(e) => onChange(e.target.value)} placeholder={field.placeholder} />
        </Field>
      );
    case 'textarea':
      return (
        <Field label={field.label + (field.required ? ' *' : '')} htmlFor={fieldId} hint={field.hint}>
          <textarea
            id={fieldId}
            className="input-base min-h-[80px] py-2"
            value={value ?? ''}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.placeholder}
          />
        </Field>
      );
    case 'number':
      return (
        <Field label={field.label + (field.required ? ' *' : '')} htmlFor={fieldId} hint={field.hint}>
          <NumericInput id={fieldId} value={value ?? ''} onChange={(v) => onChange(v)} decimalPlaces={0} placeholder={field.placeholder ?? '0'} />
        </Field>
      );
    case 'money':
      return (
        <Field label={field.label + (field.required ? ' *' : '')} htmlFor={fieldId} hint={field.hint}>
          <NumericInput id={fieldId} value={value ?? ''} onChange={(v) => onChange(v)} currencyPrefix="Rp" decimalPlaces={0} placeholder="0" />
        </Field>
      );
    case 'date':
      return (
        <Field label={field.label + (field.required ? ' *' : '')} htmlFor={fieldId} hint={field.hint}>
          <Input id={fieldId} type="date" value={value ?? ''} onChange={(e) => onChange(e.target.value)} />
        </Field>
      );
    case 'bool':
      return (
        <Field label={field.label} htmlFor={fieldId} hint={field.hint}>
          <label htmlFor={fieldId} className="inline-flex items-center gap-2 cursor-pointer">
            <input id={fieldId} type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)}
              className="size-4 rounded border-border accent-accent" />
            <span className="text-body text-text-secondary">{value ? 'Yes' : 'No'}</span>
          </label>
        </Field>
      );
    case 'select':
      return (
        <Field label={field.label + (field.required ? ' *' : '')} htmlFor={fieldId} hint={field.hint}>
          <select
            id={fieldId}
            className="input-base"
            value={value ?? ''}
            onChange={(e) => onChange(e.target.value)}
          >
            {field.options?.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </Field>
      );
    case 'link':
      return (
        <Field label={field.label + (field.required ? ' *' : '')} htmlFor={fieldId} hint={field.hint}>
          <LinkPicker field={field} value={value ?? null} onChange={onChange} />
        </Field>
      );
  }
}

function LinkPicker({ field, value, onChange }: { field: FieldDef; value: string | null; onChange: (v: string | null) => void }) {
  const { data } = useQuery({
    queryKey: ['link', field.linkEndpoint],
    queryFn: () => api<{ items: any[] }>(field.linkEndpoint!),
    enabled: !!field.linkEndpoint,
  });
  const options = useMemo(() => {
    return (data?.items ?? []).map((it) => {
      const labelKey = field.linkLabel ?? (it.display_name ? 'display_name' : 'name');
      const descKey  = field.linkDescription;
      return {
        value: it.id,
        label: String(it[labelKey] ?? it.name ?? it.id),
        description: descKey ? String(it[descKey] ?? '') : (it.name && labelKey !== 'name' ? String(it.name) : undefined),
      };
    });
  }, [data, field.linkLabel, field.linkDescription]);
  return (
    <Combobox
      options={options}
      value={value}
      onChange={onChange}
      placeholder="Select…"
      emptyText="No matches"
    />
  );
}
