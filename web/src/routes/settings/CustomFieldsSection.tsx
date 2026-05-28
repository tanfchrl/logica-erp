import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2, Save, Settings, AlertCircle } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { Skeleton } from '@/components/EmptyState';
import { StatusPill } from '@/components/StatusPill';
import { api } from '@/lib/api';

/**
 * CustomFieldsSection — Settings → "Custom fields". Admins define per-
 * doctype extra fields; the backend's existing customfield Validator
 * picks them up automatically on create/update of any doctype that runs
 * through customfield.EnsureTxValidator.
 *
 * Two-pane: doctype list on the left, definitions for the selected
 * doctype on the right. Inline add row at the bottom.
 */

interface FieldDef {
  id: string;
  doctype: string;
  field_name: string;
  label_id: string;
  label_en: string;
  field_type: 'text'|'int'|'decimal'|'date'|'datetime'|'bool'|'select'|'link';
  is_required: boolean;
  default_value?: string;
  options?: unknown;
  position: number;
  is_indexed: boolean;
}

// Same doctype set the backend's customfield validator can target.
// Keep in sync with the parent_allowlist enums in the various services.
const DOCTYPE_OPTIONS = [
  'customer', 'supplier', 'lead', 'contact', 'opportunity', 'item',
  'sales_invoice', 'purchase_invoice', 'purchase_order', 'material_request',
  'purchase_receipt', 'payment_entry', 'journal_entry',
  'asset', 'asset_category', 'asset_movement', 'asset_location',
  'project', 'task',
];

const FIELD_TYPES: FieldDef['field_type'][] = [
  'text', 'int', 'decimal', 'date', 'datetime', 'bool', 'select', 'link',
];

export function CustomFieldsSection() {
  const qc = useQueryClient();
  const [selectedDoctype, setSelectedDoctype] = useState<string>(DOCTYPE_OPTIONS[0]!);

  const { data, isLoading } = useQuery({
    queryKey: ['custom-fields', selectedDoctype],
    queryFn:  () => api<{ items: FieldDef[] }>(`/admin/custom-fields?doctype=${selectedDoctype}`),
  });
  const items = data?.items ?? [];

  return (
    <div className="space-y-5">
      <div>
        <CardTitle>
          <Settings className="size-4 inline mr-1.5 text-accent" /> Custom fields
        </CardTitle>
        <CardDescription>
          Define extra fields per doctype. The API + auto-generated forms read
          these on the fly — no redeploy needed. System administrators only.
        </CardDescription>
      </div>

      <Card padded={false}>
        <div className="px-5 py-3 border-b border-hairline flex items-center gap-3 flex-wrap">
          <span className="text-body-sm text-stone">Doctype:</span>
          <select className="input-base !h-8 !w-auto"
            value={selectedDoctype}
            onChange={(e) => setSelectedDoctype(e.target.value)}>
            {DOCTYPE_OPTIONS.map((dt) => <option key={dt} value={dt}>{dt}</option>)}
          </select>
          <span className="text-caption text-stone ml-auto">{items.length} field(s)</span>
        </div>

        {isLoading ? (
          <div className="p-5"><Skeleton className="h-32 w-full" /></div>
        ) : (
          <div className="divide-y divide-hairline">
            {items.length === 0 && (
              <div className="px-5 py-6 text-center text-caption text-stone">
                No custom fields on this doctype yet.
              </div>
            )}
            {items.map((d) => (
              <FieldRow key={d.id} def={d} onChanged={() => qc.invalidateQueries({ queryKey: ['custom-fields', selectedDoctype] })} />
            ))}
            <AddField doctype={selectedDoctype} onAdded={() => qc.invalidateQueries({ queryKey: ['custom-fields', selectedDoctype] })} />
          </div>
        )}
      </Card>

      <Card className="!p-3 flex items-start gap-2 bg-info/5 border-info/30">
        <AlertCircle className="size-4 text-info shrink-0 mt-0.5" />
        <div className="text-body-sm text-text-secondary">
          Field names use <code className="font-mono text-caption">[a-z0-9_]</code> and become JSON keys on the
          <code className="font-mono text-caption"> custom_fields</code> column. For <code>select</code>, set options to
          <code className="font-mono text-caption">{` {"values":["a","b"]}`}</code>; for <code>link</code>,
          <code className="font-mono text-caption">{` {"doctype":"customer"}`}</code>.
        </div>
      </Card>
    </div>
  );
}

function FieldRow({ def, onChanged }: { def: FieldDef; onChanged: () => void }) {
  const [editing, setEditing] = useState(false);
  const [labelID, setLabelID] = useState(def.label_id);
  const [labelEN, setLabelEN] = useState(def.label_en);
  const [fieldType, setFieldType] = useState(def.field_type);
  const [isRequired, setIsRequired] = useState(def.is_required);
  const [defaultValue, setDefaultValue] = useState(def.default_value ?? '');
  const [position, setPosition] = useState(def.position);
  const [optionsRaw, setOptionsRaw] = useState(def.options ? JSON.stringify(def.options) : '');
  const [err, setErr] = useState<string | null>(null);

  const optionsJSON = useMemo(() => {
    if (!optionsRaw.trim()) return undefined;
    try { return JSON.parse(optionsRaw); } catch { return null; }
  }, [optionsRaw]);

  const update = useMutation({
    mutationFn: () => api(`/admin/custom-fields/${def.id}`, {
      method: 'PUT',
      body: {
        doctype: def.doctype, field_name: def.field_name,
        label_id: labelID, label_en: labelEN, field_type: fieldType,
        is_required: isRequired, default_value: defaultValue || undefined,
        options: optionsJSON ?? undefined, position, is_indexed: def.is_indexed,
      },
    }),
    onSuccess: () => { setEditing(false); setErr(null); onChanged(); },
    onError:   (e: Error) => setErr(e.message),
  });

  const del = useMutation({
    mutationFn: () => api(`/admin/custom-fields/${def.id}`, { method: 'DELETE' }),
    onSuccess: onChanged,
    onError:   (e: Error) => setErr(e.message),
  });

  if (!editing) {
    return (
      <div className="px-5 py-3 flex items-baseline gap-3 flex-wrap">
        <div className="font-mono text-body-sm text-ink">{def.field_name}</div>
        <span className="text-caption text-stone">{def.label_en}</span>
        <StatusPill tone="neutral" withDot={false}>{def.field_type}</StatusPill>
        {def.is_required && <StatusPill tone="warning" withDot={false}>required</StatusPill>}
        {def.is_indexed && <StatusPill tone="info" withDot={false}>indexed</StatusPill>}
        <div className="ml-auto flex items-center gap-1">
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>Edit</Button>
          <Button size="sm" variant="ghost"
            onClick={() => { if (confirm(`Delete custom field "${def.field_name}"?`)) del.mutate(); }}>
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="px-5 py-3 space-y-2 bg-surface-soft">
      <div className="grid grid-cols-2 gap-2">
        <Field label="Label (ID)">
          <Input value={labelID} onChange={(e) => setLabelID(e.target.value)} />
        </Field>
        <Field label="Label (EN)">
          <Input value={labelEN} onChange={(e) => setLabelEN(e.target.value)} />
        </Field>
        <Field label="Type">
          <select className="input-base" value={fieldType}
            onChange={(e) => setFieldType(e.target.value as FieldDef['field_type'])}>
            {FIELD_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        </Field>
        <Field label="Position">
          <Input type="number" value={String(position)} onChange={(e) => setPosition(Number(e.target.value) || 0)} />
        </Field>
        <Field label="Default value">
          <Input value={defaultValue} onChange={(e) => setDefaultValue(e.target.value)} />
        </Field>
        <Field label='Options (JSON)' hint="e.g. {&quot;values&quot;:[&quot;a&quot;,&quot;b&quot;]} or {&quot;doctype&quot;:&quot;customer&quot;}">
          <Input value={optionsRaw} onChange={(e) => setOptionsRaw(e.target.value)}
            className={optionsJSON === null ? 'border-brand-error' : ''} />
        </Field>
      </div>
      <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
        <input type="checkbox" className="accent-brand-green-deep"
          checked={isRequired} onChange={(e) => setIsRequired(e.target.checked)} />
        Required
      </label>
      {err && (
        <div className="text-caption text-brand-error inline-flex items-start gap-1.5">
          <AlertCircle className="size-3.5 mt-0.5" /> {err}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>Cancel</Button>
        <Button size="sm" onClick={() => update.mutate()} loading={update.isPending} disabled={optionsJSON === null}>
          <Save className="size-3.5" /> Save
        </Button>
      </div>
    </div>
  );
}

function AddField({ doctype, onAdded }: { doctype: string; onAdded: () => void }) {
  const [fieldName, setFieldName] = useState('');
  const [labelID, setLabelID]     = useState('');
  const [labelEN, setLabelEN]     = useState('');
  const [fieldType, setFieldType] = useState<FieldDef['field_type']>('text');
  const [optionsRaw, setOptionsRaw] = useState('');
  const [err, setErr] = useState<string | null>(null);

  const optionsJSON = useMemo(() => {
    if (!optionsRaw.trim()) return undefined;
    try { return JSON.parse(optionsRaw); } catch { return null; }
  }, [optionsRaw]);

  const create = useMutation({
    mutationFn: () => api('/admin/custom-fields', {
      method: 'POST',
      body: {
        doctype, field_name: fieldName,
        label_id: labelID || labelEN,
        label_en: labelEN || labelID,
        field_type: fieldType,
        options: optionsJSON ?? undefined,
      },
    }),
    onSuccess: () => {
      setFieldName(''); setLabelID(''); setLabelEN(''); setFieldType('text'); setOptionsRaw('');
      setErr(null);
      onAdded();
    },
    onError: (e: Error) => setErr(e.message),
  });

  const needsOptions = fieldType === 'select' || fieldType === 'link';

  return (
    <div className="px-5 py-3 space-y-2 bg-surface-soft border-t border-hairline">
      <div className="text-caption text-stone uppercase">Add new field</div>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Field name (snake_case)">
          <Input value={fieldName} onChange={(e) => setFieldName(e.target.value)}
            placeholder="e.g. nps_score" className="font-mono" />
        </Field>
        <Field label="Type">
          <select className="input-base" value={fieldType}
            onChange={(e) => setFieldType(e.target.value as FieldDef['field_type'])}>
            {FIELD_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        </Field>
        <Field label="Label (ID)">
          <Input value={labelID} onChange={(e) => setLabelID(e.target.value)} placeholder="Skor NPS" />
        </Field>
        <Field label="Label (EN)">
          <Input value={labelEN} onChange={(e) => setLabelEN(e.target.value)} placeholder="NPS Score" />
        </Field>
        {needsOptions && (
          <Field label="Options (JSON)" hint={fieldType === 'select' ? '{"values":["a","b"]}' : '{"doctype":"customer"}'}>
            <Input value={optionsRaw} onChange={(e) => setOptionsRaw(e.target.value)}
              className={optionsJSON === null ? 'border-brand-error' : ''} />
          </Field>
        )}
      </div>
      {err && (
        <div className="text-caption text-brand-error inline-flex items-start gap-1.5">
          <AlertCircle className="size-3.5 mt-0.5" /> {err}
        </div>
      )}
      <div className="flex justify-end">
        <Button size="sm" onClick={() => create.mutate()} loading={create.isPending}
          disabled={!fieldName.trim() || (!labelID.trim() && !labelEN.trim()) || optionsJSON === null}>
          <Plus className="size-3.5" /> Add field
        </Button>
      </div>
    </div>
  );
}
