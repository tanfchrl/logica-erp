import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
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
  /** When true, the route is /id/edit — load the record and switch to PUT. */
  editMode?: boolean;
}

export function CreateFormPage({ config, schema, editMode = false }: CreateFormPageProps) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const listPath = `${config.modulePath}/${config.slug}`;
  const { id } = useParams({ strict: false }) as { id?: string };
  const editId = editMode ? id : undefined;

  // Load existing record when in edit mode.
  const { data: existing } = useQuery({
    queryKey: ['doctype-detail', config.endpoint, editId],
    queryFn:  () => api<Record<string, any>>(`${config.endpoint}/${editId}`),
    enabled:  !!editId,
  });

  // Seed default values
  const initial = useMemo(() => {
    const obj: Record<string, any> = {};
    for (const f of schema.fields) if (f.default !== undefined) obj[f.name] = f.default;
    return obj;
  }, [schema]);
  const [values, setValues] = useState<Record<string, any>>(initial);
  const set = (name: string, v: any) => {
    setValues((s) => {
      const next = { ...s, [name]: v };
      // linkSwitch: when a trigger field changes, clear every dependent
      // link field — the old id won't exist under the new target.
      for (const f of schema.fields) {
        if (f.kind === 'link' && f.linkSwitch?.triggerField === name) {
          next[f.name] = '';
        }
      }
      return next;
    });
    // schema.prefillFromLink — when the trigger field changes, GET the linked
    // resource and copy mapped properties onto local fields. Empty-string
    // selections clear the dependent fields so the user starts clean if they
    // switch categories.
    if (schema.prefillFromLink && name === schema.prefillFromLink.triggerField) {
      const cfg = schema.prefillFromLink;
      if (!v) {
        const cleared: Record<string, any> = {};
        for (const local of Object.keys(cfg.mapping)) cleared[local] = '';
        setValues((s) => ({ ...s, ...cleared }));
        return;
      }
      const endpoint = cfg.fetchEndpoint.replace('{id}', String(v));
      api<Record<string, any>>(endpoint).then((rec) => {
        const next: Record<string, any> = {};
        for (const [local, remote] of Object.entries(cfg.mapping)) {
          if (rec[remote] !== undefined && rec[remote] !== null && rec[remote] !== '') {
            next[local] = rec[remote];
          }
        }
        if (Object.keys(next).length > 0) setValues((s) => ({ ...s, ...next }));
      }).catch(() => { /* silent — user can still fill manually */ });
    }
  };

  // Hydrate from existing record when it arrives.
  useEffect(() => {
    if (!existing) return;
    const next: Record<string, any> = {};
    for (const f of schema.fields) {
      const v = existing[f.name];
      if (v === undefined || v === null) continue;
      // Date fields come back as ISO timestamps — keep only YYYY-MM-DD for date inputs.
      next[f.name] = f.kind === 'date' && typeof v === 'string' ? v.slice(0, 10) : v;
    }
    setValues((s) => ({ ...s, ...next }));
  }, [existing, schema]);

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

  // ---- regular create / edit form ----
  const saveMutation = useMutation({
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
      const url = editId ? `${config.endpoint}/${editId}` : config.endpoint;
      return api<any>(url, { method: editId ? 'PUT' : 'POST', body });
    },
    onSuccess: (resp) => {
      toast.success(`${config.singular} ${editId ? 'updated' : 'created'}`, resp?.name ?? resp?.id ?? '');
      qc.invalidateQueries({ queryKey: ['doctype', config.endpoint] });
      if (editId) qc.invalidateQueries({ queryKey: ['doctype-detail', config.endpoint, editId] });
      if (editId && resp?.id) {
        navigate({ to: `${listPath}/${resp.id}` as never });
      } else if (schema.redirectTo && resp?.id) {
        navigate({ to: schema.redirectTo(resp.id) as never });
      } else {
        navigate({ to: listPath as never });
      }
    },
    onError: (e: any) => toast.error(editId ? 'Update failed' : 'Create failed', e?.message ?? 'Check inputs / API logs'),
  });

  const missing = schema.fields.some((f) => f.required && !values[f.name]);
  const recordTitle = (existing?.display_name ?? existing?.name ?? existing?.id ?? editId) as string | undefined;

  return (
    <>
      <PageHeader
        crumbs={[
          { label: config.module, to: config.modulePath },
          { label: config.title, to: listPath },
          ...(editId
            ? [{ label: recordTitle ?? '…', to: `${listPath}/${editId}` }, { label: 'Edit' }]
            : [{ label: 'New' }]),
        ]}
        title={editId ? `Edit ${config.singular}` : `New ${config.singular}`}
        actions={
          <>
            <Button variant="ghost" asChild>
              <Link to={(editId ? `${listPath}/${editId}` : listPath) as never}>
                <ArrowLeft className="size-4" /> {editId ? 'Cancel' : 'Back'}
              </Link>
            </Button>
            <Button onClick={() => saveMutation.mutate()} disabled={missing} loading={saveMutation.isPending}>
              <Save className="size-4" /> {editId ? 'Save changes' : `Create ${config.singular}`}
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
                  formValues={values}
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
function FieldControl({
  field, value, onChange, formValues,
}: { field: FieldDef; value: any; onChange: (v: any) => void; formValues: Record<string, any> }) {
  const fieldId = `f-${field.name}`;

  // linkSwitch: resolve the effective endpoint/label from a sibling field's
  // current value. When the trigger isn't set yet, render the picker
  // disabled with a guidance hint.
  let effective = field;
  let linkDisabledHint: string | undefined;
  if (field.kind === 'link' && field.linkSwitch) {
    const triggerVal = formValues[field.linkSwitch.triggerField];
    const cfg = triggerVal ? field.linkSwitch.byValue[triggerVal] : undefined;
    if (cfg) {
      effective = {
        ...field,
        linkEndpoint:   cfg.endpoint,
        linkLabel:      cfg.label ?? field.linkLabel,
        linkDescription: cfg.description ?? field.linkDescription,
      };
    } else {
      effective = { ...field, linkEndpoint: undefined };
      linkDisabledHint = `Choose ${field.linkSwitch.triggerField.replace(/_/g, ' ')} first.`;
    }
  }

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
        <Field label={effective.label + (effective.required ? ' *' : '')} htmlFor={fieldId} hint={linkDisabledHint ?? effective.hint}>
          <LinkPicker field={effective} value={value ?? null} onChange={onChange} />
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
