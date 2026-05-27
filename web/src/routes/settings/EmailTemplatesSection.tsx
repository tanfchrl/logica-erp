import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { BookOpen, Check, Edit, Save, X, Trash2, AlertCircle } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface EventDef     { key: string; label: string; description: string }
interface EventList    { items: EventDef[] }
interface Template     { id: string; event_key: string; company_id?: string; subject: string; body_html: string; is_enabled: boolean; updated_at: string }
interface TemplateList { items: Template[] }

const SAMPLE_BODIES: Record<string, { subject: string; body: string }> = {
  'invoice.issued': {
    subject: 'Invoice {{.InvoiceNumber}} from {{.CompanyName}}',
    body: `<p>Hi {{.CustomerName}},</p>
<p>Please find invoice <strong>{{.InvoiceNumber}}</strong> dated {{.PostingDate}} for <strong>{{.GrandTotal}}</strong>, due on {{.DueDate}}.</p>
<p>Pay online: <a href="{{.PaymentLink}}">{{.PaymentLink}}</a></p>
<p>Thank you,<br/>{{.CompanyName}}</p>`,
  },
  'invoice.payment_received': {
    subject: 'Payment received — {{.InvoiceNumber}}',
    body: `<p>Hi {{.CustomerName}},</p>
<p>We've received <strong>{{.AmountPaid}}</strong> against invoice {{.InvoiceNumber}}. Thank you!</p>`,
  },
  'invoice.overdue': {
    subject: 'Payment reminder — invoice {{.InvoiceNumber}}',
    body: `<p>Hi {{.CustomerName}},</p>
<p>Invoice <strong>{{.InvoiceNumber}}</strong> for <strong>{{.OutstandingAmount}}</strong> was due on {{.DueDate}} ({{.DaysOverdue}} days overdue).</p>
<p>Could you confirm payment status? Pay online: <a href="{{.PaymentLink}}">{{.PaymentLink}}</a></p>`,
  },
  'po.sent': {
    subject: 'Purchase order {{.PONumber}} from {{.CompanyName}}',
    body: `<p>Hi {{.SupplierName}},</p>
<p>Please find purchase order <strong>{{.PONumber}}</strong> dated {{.PostingDate}}, total <strong>{{.GrandTotal}}</strong>.</p>`,
  },
  'user.password_reset': {
    subject: 'Reset your Logica ERP password',
    body: `<p>Hi,</p>
<p>Use this link to set a new password (valid for 30 minutes): <a href="{{.ResetLink}}">{{.ResetLink}}</a></p>
<p>If you didn't request this, ignore this message.</p>`,
  },
  'user.invited': {
    subject: '{{.InviterName}} invited you to {{.CompanyName}} on Logica ERP',
    body: `<p>Hi,</p>
<p>{{.InviterName}} has invited you to join <strong>{{.CompanyName}}</strong>.</p>
<p>Accept: <a href="{{.InviteLink}}">{{.InviteLink}}</a></p>`,
  },
  'test': {
    subject: 'Logica ERP test email',
    body: '<p>This is a test message from Logica ERP.</p>',
  },
};

export function EmailTemplatesSection() {
  const qc = useQueryClient();
  const { data: events } = useQuery({
    queryKey: ['email-events'],
    queryFn: () => api<EventList>('/admin/email/events'),
  });
  const { data: tpls, isLoading } = useQuery({
    queryKey: ['email-templates'],
    queryFn: () => api<TemplateList>('/admin/email-templates'),
  });

  // Map event_key -> template (workspace-wide; first match per event).
  const byEvent = useMemo(() => {
    const map = new Map<string, Template>();
    for (const t of (tpls?.items ?? [])) if (!map.has(t.event_key)) map.set(t.event_key, t);
    return map;
  }, [tpls]);

  const [editing, setEditing] = useState<string | null>(null);

  return (
    <div className="space-y-5">
      <div>
        <CardTitle>Email templates</CardTitle>
        <CardDescription>
          One template per event. Subject and body are Go text/templates;
          variables like <span className="font-mono text-ink">{`{{.CustomerName}}`}</span> resolve at send time.
        </CardDescription>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-40 w-full" /></Card>
      ) : (
        <div className="space-y-2">
          {(events?.items ?? []).map((ev) => {
            const tpl = byEvent.get(ev.key);
            const isEditing = editing === ev.key;
            return (
              <TemplateRow
                key={ev.key}
                event={ev}
                template={tpl}
                editing={isEditing}
                onEdit={() => setEditing(ev.key)}
                onCancel={() => setEditing(null)}
                onSaved={() => {
                  setEditing(null);
                  void qc.invalidateQueries({ queryKey: ['email-templates'] });
                }}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

function TemplateRow({
  event, template, editing, onEdit, onCancel, onSaved,
}: {
  event: EventDef;
  template?: Template;
  editing: boolean;
  onEdit: () => void;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const fallback = SAMPLE_BODIES[event.key] ?? { subject: '', body: '' };
  const [subject, setSubject]   = useState(template?.subject ?? fallback.subject);
  const [body, setBody]         = useState(template?.body_html ?? fallback.body);
  const [enabled, setEnabled]   = useState(template?.is_enabled ?? true);
  const [error, setError]       = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () => api<Template>('/admin/email-templates', {
      method: 'PUT',
      body: { event_key: event.key, subject, body_html: body, is_enabled: enabled },
    }),
    onSuccess: () => onSaved(),
    onError: (e: Error) => setError(e.message),
  });
  const del = useMutation({
    mutationFn: () => api<void>(`/admin/email-templates/${template?.id}`, { method: 'DELETE' }),
    onSuccess: () => onSaved(),
  });

  // Re-seed local state when entering edit mode (so cancel discards inputs).
  function startEdit() {
    setSubject(template?.subject ?? fallback.subject);
    setBody(template?.body_html ?? fallback.body);
    setEnabled(template?.is_enabled ?? true);
    setError(null);
    onEdit();
  }

  return (
    <Card padded={false}>
      <div className="px-4 py-3 flex items-start gap-3">
        <div className="size-9 rounded-md bg-surface text-ink inline-flex items-center justify-center shrink-0">
          <BookOpen className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <div className="text-body-sm font-medium text-ink">{event.label}</div>
            <span className="text-caption font-mono text-stone">{event.key}</span>
            {template
              ? template.is_enabled
                ? <StatusPill tone="success" withDot={false}><Check className="size-3" /> Configured</StatusPill>
                : <StatusPill tone="warning" withDot={false}>Disabled</StatusPill>
              : <StatusPill tone="neutral" withDot={false}>Using default</StatusPill>}
          </div>
          <div className="text-caption text-stone mt-0.5">{event.description}</div>
        </div>

        <div className="flex items-center gap-1 shrink-0">
          {!editing && (
            <Button variant="ghost" size="sm" onClick={startEdit}>
              <Edit className="size-3.5" /> {template ? 'Edit' : 'Customize'}
            </Button>
          )}
          {template && !editing && (
            <Button variant="ghost" size="sm"
              onClick={() => { if (confirm('Remove this template? It will fall back to the default.')) del.mutate(); }}>
              <Trash2 className="size-3.5" />
            </Button>
          )}
        </div>
      </div>

      {editing && (
        <div className="px-4 pb-4 pt-1 border-t border-hairline space-y-3 bg-surface-soft">
          <Field label="Subject">
            <Input value={subject} onChange={(e) => setSubject(e.target.value)} />
          </Field>
          <Field label="HTML body" hint="Go text/template syntax. Saved as-is, rendered at send time.">
            <textarea
              className={cn('input-base !h-auto !py-2 font-mono text-[12.5px] leading-snug')}
              rows={10}
              value={body}
              onChange={(e) => setBody(e.target.value)}
            />
          </Field>
          <label className="flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
            <input type="checkbox" className="accent-brand-green-deep" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            Enabled (uncheck to silence this event)
          </label>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}

          <div className="flex items-center justify-end gap-2 pt-2">
            <Button variant="ghost" size="sm" onClick={onCancel}>
              <X className="size-3.5" /> Cancel
            </Button>
            <Button size="sm" onClick={() => save.mutate()} loading={save.isPending}>
              <Save className="size-3.5" /> Save template
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}
