import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus, Wand2, Trash2, ChevronRight, Star, Power, PowerOff,
  AlertCircle, ShieldCheck, X, Sparkles, Edit,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';

interface AdminState {
  id: string;
  workflow_id: string;
  name: string;
  doc_status: number;
  is_initial: boolean;
  is_terminal: boolean;
}
interface AdminTransition {
  id: string;
  workflow_id: string;
  from_state: string;
  to_state: string;
  action: string;
  allowed_role_id?: string;
}
interface AdminWorkflow {
  id: string;
  name: string;
  doctype: string;
  state_field: string;
  is_active: boolean;
  states: AdminState[];
  transitions: AdminTransition[];
  created_at: string;
}
interface WorkflowList { items: AdminWorkflow[] }
interface DoctypeList  { items: string[] }
interface Role         { id: string; name: string; is_system: boolean }
interface RoleList     { items: Role[] }

interface ApprovalRule {
  id: string;
  name: string;
  doctype: string;
  company_id?: string;
  condition_field?: string;
  condition_op?: string;
  condition_value?: string;
  required_role_id: string;
  sequence: number;
  is_active: boolean;
  description?: string;
  updated_at: string;
}
interface ApprovalRuleList { items: ApprovalRule[] }

type Tab = 'workflows' | 'rules';

export function WorkflowsSection() {
  const [tab, setTab] = useState<Tab>('workflows');

  return (
    <div className="space-y-5">
      <div className="rounded-lg border border-hairline bg-surface-soft p-3 flex items-start gap-3">
        <ShieldCheck className="size-4 text-stone shrink-0 mt-0.5" />
        <div className="text-caption text-stone">
          <strong className="text-charcoal">Configure here; engine wiring is next.</strong>
          {' '}You can define workflows and approval rules now. Hooking them into the document submit
          pipeline (so submitting an SI actually waits for approval) is a follow-up that touches every submittable doctype.
        </div>
      </div>

      <div className="inline-flex items-center p-1 rounded-full bg-surface border border-hairline">
        <TabChip active={tab === 'workflows'} icon={Wand2}        label="Workflows"      onClick={() => setTab('workflows')} />
        <TabChip active={tab === 'rules'}     icon={ShieldCheck}  label="Approval rules" onClick={() => setTab('rules')} />
      </div>

      {tab === 'workflows' && <WorkflowsTab />}
      {tab === 'rules'     && <ApprovalRulesTab />}
    </div>
  );
}

function TabChip({
  active, icon: Icon, label, onClick,
}: { active: boolean; icon: React.ComponentType<{ className?: string }>; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 h-8 px-3 rounded-full text-body-sm transition-colors',
        active ? 'bg-canvas text-ink shadow-soft' : 'text-steel hover:text-ink',
      )}>
      <Icon className="size-3.5" />
      {label}
    </button>
  );
}

/* ======================= WORKFLOWS TAB ======================= */

function WorkflowsTab() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['workflows'], queryFn: () => api<WorkflowList>('/admin/workflows') });
  const { data: doctypes }  = useQuery({ queryKey: ['workflow-doctypes'], queryFn: () => api<DoctypeList>('/admin/workflows/doctypes') });
  const { data: roles }     = useQuery({ queryKey: ['roles'], queryFn: () => api<RoleList>('/admin/roles') });

  const items = data?.items ?? [];
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const selected = selectedId ? items.find((w) => w.id === selectedId) ?? null : items[0] ?? null;

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Workflows</CardTitle>
          <CardDescription>
            One workflow per submittable doctype. States map to <span className="font-mono text-ink">docstatus</span> (0 draft, 1 submitted, 2 cancelled);
            transitions are gated by role.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New workflow
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-40 w-full" /></Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Wand2 className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No workflows yet.</div>
            <div className="text-caption text-stone mt-1">Define one to route documents through review / approval.</div>
            <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> Create workflow
            </Button>
          </div>
        </Card>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-[280px_1fr] gap-4">
          <Card padded={false}>
            <ul className="divide-y divide-hairline">
              {items.map((w) => (
                <li key={w.id}>
                  <button type="button" onClick={() => setSelectedId(w.id)}
                    className={cn('w-full text-left px-3 py-2.5 transition-colors',
                      w.id === selected?.id ? 'bg-surface' : 'hover:bg-surface-soft')}>
                    <div className="flex items-center gap-2">
                      <span className="text-body-sm font-medium text-ink truncate">{w.name}</span>
                      {!w.is_active && <StatusPill tone="warning" withDot={false}>Inactive</StatusPill>}
                    </div>
                    <div className="text-caption text-stone font-mono">{w.doctype}</div>
                    <div className="text-caption text-stone">{w.states.length} states · {w.transitions.length} transitions</div>
                  </button>
                </li>
              ))}
            </ul>
          </Card>

          {selected && (
            <WorkflowEditor
              workflow={selected}
              roles={roles?.items ?? []}
              onChanged={() => qc.invalidateQueries({ queryKey: ['workflows'] })}
              onDeleted={() => { setSelectedId(null); qc.invalidateQueries({ queryKey: ['workflows'] }); }}
            />
          )}
        </div>
      )}

      {createOpen && (
        <CreateWorkflowDialog
          doctypes={doctypes?.items ?? []}
          onClose={() => setCreateOpen(false)}
          onCreated={(w) => { void qc.invalidateQueries({ queryKey: ['workflows'] }); setSelectedId(w.id); setCreateOpen(false); }}
        />
      )}
    </div>
  );
}

function WorkflowEditor({
  workflow, roles, onChanged, onDeleted,
}: {
  workflow: AdminWorkflow;
  roles: Role[];
  onChanged: () => void;
  onDeleted: () => void;
}) {
  const qc = useQueryClient();
  const [addStateOpen, setAddStateOpen] = useState(false);
  const [addTransOpen, setAddTransOpen] = useState(false);

  const toggleActive = useMutation({
    mutationFn: () => api<AdminWorkflow>(`/admin/workflows/${workflow.id}`, {
      method: 'PUT', body: { name: workflow.name, doctype: workflow.doctype, is_active: !workflow.is_active },
    }),
    onSuccess: () => onChanged(),
  });
  const del = useMutation({
    mutationFn: () => api<void>(`/admin/workflows/${workflow.id}`, { method: 'DELETE' }),
    onSuccess: () => onDeleted(),
  });
  const delState = useMutation({
    mutationFn: (id: string) => api<void>(`/admin/workflow-states/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workflows'] }),
  });
  const delTrans = useMutation({
    mutationFn: (id: string) => api<void>(`/admin/workflow-transitions/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['workflows'] }),
  });

  const roleName = (id?: string) => id ? roles.find((r) => r.id === id)?.name ?? id : '— Anyone with write —';

  return (
    <Card padded={false}>
      <div className="px-5 py-4 border-b border-hairline flex items-end justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <CardTitle>{workflow.name}</CardTitle>
            <span className="text-caption font-mono text-stone">{workflow.doctype}</span>
            {!workflow.is_active && <StatusPill tone="warning" withDot={false}>Inactive</StatusPill>}
          </div>
          <CardDescription>
            State field: <span className="font-mono text-ink">{workflow.state_field}</span>
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="secondary" onClick={() => toggleActive.mutate()} loading={toggleActive.isPending}>
            {workflow.is_active
              ? <><PowerOff className="size-3.5" /> Deactivate</>
              : <><Power    className="size-3.5" /> Activate</>}
          </Button>
          <Button size="sm" variant="ghost"
            onClick={() => { if (confirm(`Delete workflow "${workflow.name}"? States and transitions go with it.`)) del.mutate(); }}>
            <Trash2 className="size-3.5" />
          </Button>
        </div>
      </div>

      {/* States */}
      <div className="p-5 border-b border-hairline">
        <div className="flex items-end justify-between mb-3">
          <div>
            <div className="label-base">States</div>
            <div className="text-caption text-stone">Each state maps to a docstatus (0 draft, 1 submitted, 2 cancelled). One state must be the initial.</div>
          </div>
          <Button size="sm" variant="secondary" onClick={() => setAddStateOpen(true)}>
            <Plus className="size-3.5" /> Add state
          </Button>
        </div>
        {workflow.states.length === 0 ? (
          <div className="text-body-sm text-stone py-3 text-center">No states yet.</div>
        ) : (
          <div className="flex flex-wrap gap-2">
            {workflow.states.map((s) => (
              <div key={s.id}
                className={cn('inline-flex items-center gap-2 pl-3 pr-1 h-8 rounded-full border',
                  s.is_initial ? 'border-brand-green-deep bg-brand-green-soft/30' :
                  s.is_terminal ? 'border-hairline bg-surface' : 'border-hairline')}>
                {s.is_initial && <Sparkles className="size-3 text-brand-green-deep" />}
                <span className="text-body-sm text-ink">{s.name}</span>
                <span className={cn('text-caption px-1.5 rounded-full',
                  s.doc_status === 0 ? 'bg-surface text-stone' :
                  s.doc_status === 1 ? 'bg-success/10 text-success' : 'bg-brand-error/10 text-brand-error')}>
                  ds:{s.doc_status}
                </span>
                <button type="button" onClick={() => { if (confirm(`Delete state "${s.name}"?`)) delState.mutate(s.id); }}
                  className="inline-flex size-6 items-center justify-center rounded-full text-stone hover:bg-canvas hover:text-brand-error">
                  <X className="size-3" />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Transitions */}
      <div className="p-5">
        <div className="flex items-end justify-between mb-3">
          <div>
            <div className="label-base">Transitions</div>
            <div className="text-caption text-stone">Each transition is keyed by (from-state, action). The optional role gates who can fire it.</div>
          </div>
          <Button size="sm" variant="secondary" onClick={() => setAddTransOpen(true)} disabled={workflow.states.length < 2}>
            <Plus className="size-3.5" /> Add transition
          </Button>
        </div>
        {workflow.transitions.length === 0 ? (
          <div className="text-body-sm text-stone py-3 text-center">No transitions yet.</div>
        ) : (
          <table className="w-full text-body-sm">
            <thead>
              <tr className="text-micro-uppercase text-stone">
                <th className="text-left font-medium py-1.5">From</th>
                <th className="text-left font-medium py-1.5">Action</th>
                <th className="text-left font-medium py-1.5">To</th>
                <th className="text-left font-medium py-1.5">Allowed role</th>
                <th className="w-[40px]"></th>
              </tr>
            </thead>
            <tbody>
              {workflow.transitions.map((t) => (
                <tr key={t.id} className="border-t border-hairline">
                  <td className="py-2 text-ink">{t.from_state}</td>
                  <td className="py-2 text-charcoal font-mono">{t.action}</td>
                  <td className="py-2 text-ink inline-flex items-center gap-1.5">
                    <ChevronRight className="size-3 text-stone" />{t.to_state}
                  </td>
                  <td className="py-2 text-steel">{roleName(t.allowed_role_id)}</td>
                  <td className="py-2 text-right">
                    <button type="button" onClick={() => { if (confirm('Delete this transition?')) delTrans.mutate(t.id); }}
                      className="inline-flex size-6 items-center justify-center rounded text-stone hover:bg-surface hover:text-brand-error">
                      <Trash2 className="size-3" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {addStateOpen && (
        <AddStateDialog workflow={workflow}
          onClose={() => setAddStateOpen(false)}
          onAdded={() => { qc.invalidateQueries({ queryKey: ['workflows'] }); setAddStateOpen(false); }}
        />
      )}
      {addTransOpen && (
        <AddTransitionDialog workflow={workflow} roles={roles}
          onClose={() => setAddTransOpen(false)}
          onAdded={() => { qc.invalidateQueries({ queryKey: ['workflows'] }); setAddTransOpen(false); }}
        />
      )}
    </Card>
  );
}

/* ----- Create workflow dialog ----- */

function CreateWorkflowDialog({
  doctypes, onClose, onCreated,
}: { doctypes: string[]; onClose: () => void; onCreated: (w: AdminWorkflow) => void }) {
  const [name, setName]       = useState('Approval workflow');
  const [doctype, setDoctype] = useState(doctypes[0] ?? 'purchase_invoice');
  const [error, setError]     = useState<string | null>(null);
  const mut = useMutation({
    mutationFn: () => api<AdminWorkflow>('/admin/workflows', {
      method: 'POST',
      body: { name, doctype, state_field: 'status', is_active: true },
    }),
    onSuccess: (w) => onCreated(w),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New workflow</DialogTitle>
        <DialogDescription>
          One workflow per doctype. Add states and transitions after creating.
        </DialogDescription>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
          </Field>
          <Field label="Doctype">
            <NativeSelect value={doctype} onChange={setDoctype}
              options={doctypes.map((d) => ({ value: d, label: d }))} />
          </Field>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create workflow</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function AddStateDialog({
  workflow, onClose, onAdded,
}: { workflow: AdminWorkflow; onClose: () => void; onAdded: () => void }) {
  const [name, setName]             = useState('');
  const [docStatus, setDocStatus]   = useState(0);
  const [isInitial, setIsInitial]   = useState(workflow.states.length === 0);
  const [isTerminal, setIsTerminal] = useState(false);
  const [error, setError]           = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<AdminState>(`/admin/workflows/${workflow.id}/states`, {
      method: 'POST',
      body: { name, doc_status: docStatus, is_initial: isInitial, is_terminal: isTerminal },
    }),
    onSuccess: () => onAdded(),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>Add state</DialogTitle>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); if (!name.trim()) return setError('Name required'); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="State name" hint="Free text. Reference it from transitions exactly as written.">
            <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
          </Field>
          <Field label="Maps to docstatus">
            <NativeSelect value={String(docStatus)} onChange={(v) => setDocStatus(Number(v))}
              options={[
                { value: '0', label: '0 — Draft' },
                { value: '1', label: '1 — Submitted' },
                { value: '2', label: '2 — Cancelled' },
              ]} />
          </Field>
          <div className="flex gap-5 flex-wrap text-body-sm text-charcoal">
            <label className="inline-flex items-center gap-2 cursor-pointer">
              <input type="checkbox" className="accent-brand-green-deep" checked={isInitial} onChange={(e) => setIsInitial(e.target.checked)} />
              Initial state
            </label>
            <label className="inline-flex items-center gap-2 cursor-pointer">
              <input type="checkbox" className="accent-brand-green-deep" checked={isTerminal} onChange={(e) => setIsTerminal(e.target.checked)} />
              Terminal state
            </label>
          </div>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Add state</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function AddTransitionDialog({
  workflow, roles, onClose, onAdded,
}: { workflow: AdminWorkflow; roles: Role[]; onClose: () => void; onAdded: () => void }) {
  const stateNames = workflow.states.map((s) => s.name);
  const [from, setFrom]       = useState(stateNames[0] ?? '');
  const [to, setTo]           = useState(stateNames[1] ?? stateNames[0] ?? '');
  const [action, setAction]   = useState('approve');
  const [roleID, setRoleID]   = useState('');
  const [error, setError]     = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api<AdminTransition>(`/admin/workflows/${workflow.id}/transitions`, {
      method: 'POST',
      body: { from_state: from, to_state: to, action, ...(roleID ? { allowed_role_id: roleID } : {}) },
    }),
    onSuccess: () => onAdded(),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>Add transition</DialogTitle>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); mut.mutate(); }}
          className="mt-4 space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <Field label="From state">
              <NativeSelect value={from} onChange={setFrom}
                options={stateNames.map((s) => ({ value: s, label: s }))} />
            </Field>
            <Field label="To state">
              <NativeSelect value={to} onChange={setTo}
                options={stateNames.map((s) => ({ value: s, label: s }))} />
            </Field>
          </div>
          <Field label="Action verb" hint="The button label users will click. E.g. submit, approve, reject.">
            <Input value={action} onChange={(e) => setAction(e.target.value)} className="font-mono" />
          </Field>
          <Field label="Allowed role" hint="Omit to allow anyone with write access on the doctype.">
            <NativeSelect value={roleID} onChange={setRoleID}
              options={[{ value: '', label: '— Anyone with write —' },
                ...roles.map((r) => ({ value: r.id, label: r.name }))]} />
          </Field>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Add transition</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ======================= APPROVAL RULES TAB ======================= */

const CONDITION_OPS = [
  { value: '',    label: '— Always fires —' },
  { value: '>',   label: '>' },
  { value: '>=',  label: '>=' },
  { value: '<',   label: '<' },
  { value: '<=',  label: '<=' },
  { value: '=',   label: '=' },
  { value: '<>',  label: '≠' },
];

const COMMON_FIELDS = [
  { value: '',                   label: '— No condition —' },
  { value: 'grand_total',        label: 'grand_total (amount)' },
  { value: 'amount',             label: 'amount' },
  { value: 'total_debit',        label: 'total_debit (JE)' },
  { value: 'total_outstanding',  label: 'total_outstanding' },
];

function ApprovalRulesTab() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['approval-rules'], queryFn: () => api<ApprovalRuleList>('/admin/approval-rules') });
  const { data: doctypes }  = useQuery({ queryKey: ['workflow-doctypes'], queryFn: () => api<DoctypeList>('/admin/workflows/doctypes') });
  const { data: roles }     = useQuery({ queryKey: ['roles'], queryFn: () => api<RoleList>('/admin/roles') });

  const rules = data?.items ?? [];
  const grouped = useMemo(() => {
    const m = new Map<string, ApprovalRule[]>();
    for (const r of rules) {
      const arr = m.get(r.doctype) ?? [];
      arr.push(r);
      m.set(r.doctype, arr);
    }
    return Array.from(m.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [rules]);

  const [editing, setEditing] = useState<ApprovalRule | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const del = useMutation({
    mutationFn: (id: string) => api<void>(`/admin/approval-rules/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['approval-rules'] }),
  });

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Approval rules</CardTitle>
          <CardDescription>
            Declarative triggers. Example: "Purchase Invoice with grand_total &gt; 50,000,000 requires Finance Manager approval before submit."
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New rule
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-40 w-full" /></Card>
      ) : rules.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <ShieldCheck className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No approval rules yet.</div>
            <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> Create rule
            </Button>
          </div>
        </Card>
      ) : (
        <div className="space-y-3">
          {grouped.map(([doctype, rs]) => (
            <Card key={doctype} padded={false}>
              <div className="px-4 py-2.5 border-b border-hairline flex items-baseline justify-between">
                <span className="text-body-sm font-medium text-ink font-mono">{doctype}</span>
                <span className="text-caption text-stone">{rs.length} rules</span>
              </div>
              <ul className="divide-y divide-hairline">
                {rs.sort((a, b) => a.sequence - b.sequence).map((r) => {
                  const role = roles?.items.find((x) => x.id === r.required_role_id);
                  return (
                    <li key={r.id} className="px-4 py-3 flex items-center gap-3">
                      <span className="inline-flex items-center justify-center size-7 rounded-full bg-surface text-stone text-caption font-mono">
                        #{r.sequence}
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="text-body-sm font-medium text-ink truncate">{r.name}</span>
                          {!r.is_active && <StatusPill tone="warning" withDot={false}>Inactive</StatusPill>}
                        </div>
                        <div className="text-caption text-stone">
                          {r.condition_field
                            ? <>When <span className="font-mono text-ink">{r.condition_field} {r.condition_op} {r.condition_value}</span></>
                            : 'Always'} → requires <span className="text-ink">{role?.name ?? r.required_role_id}</span> approval
                          {r.description && <span> · {r.description}</span>}
                        </div>
                      </div>
                      <Button size="sm" variant="ghost" onClick={() => setEditing(r)}>
                        <Edit className="size-3.5" />
                      </Button>
                      <Button size="sm" variant="ghost"
                        onClick={() => { if (confirm(`Delete rule "${r.name}"?`)) del.mutate(r.id); }}>
                        <Trash2 className="size-3.5" />
                      </Button>
                    </li>
                  );
                })}
              </ul>
            </Card>
          ))}
        </div>
      )}

      {(createOpen || editing) && (
        <ApprovalRuleDialog
          doctypes={doctypes?.items ?? []}
          roles={roles?.items ?? []}
          rule={editing}
          onClose={() => { setCreateOpen(false); setEditing(null); }}
          onSaved={() => { void qc.invalidateQueries({ queryKey: ['approval-rules'] }); setCreateOpen(false); setEditing(null); }}
        />
      )}
    </div>
  );
}

function ApprovalRuleDialog({
  doctypes, roles, rule, onClose, onSaved,
}: {
  doctypes: string[];
  roles: Role[];
  rule?: ApprovalRule | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const editing = !!rule;
  const [name, setName]               = useState(rule?.name ?? '');
  const [doctype, setDoctype]         = useState(rule?.doctype ?? doctypes[0] ?? 'purchase_invoice');
  const [conditionField, setCF]       = useState(rule?.condition_field ?? '');
  const [conditionOp, setCO]          = useState(rule?.condition_op ?? '>');
  const [conditionValue, setCV]       = useState(rule?.condition_value ?? '');
  const [requiredRole, setRR]         = useState(rule?.required_role_id ?? (roles[0]?.id ?? ''));
  const [sequence, setSequence]       = useState(rule?.sequence ?? 100);
  const [isActive, setIsActive]       = useState(rule?.is_active ?? true);
  const [description, setDescription] = useState(rule?.description ?? '');
  const [error, setError]             = useState<string | null>(null);

  useEffect(() => {
    if (!conditionField) setCO(''); // clear op if no field
  }, [conditionField]);

  const mut = useMutation({
    mutationFn: () => editing
      ? api<ApprovalRule>(`/admin/approval-rules/${rule!.id}`, {
          method: 'PUT', body: payload(),
        })
      : api<ApprovalRule>('/admin/approval-rules', {
          method: 'POST', body: payload(),
        }),
    onSuccess: () => onSaved(),
    onError:   (e: Error) => setError(e.message),
  });

  function payload() {
    return {
      name, doctype,
      ...(conditionField ? { condition_field: conditionField, condition_op: conditionOp, condition_value: conditionValue } : {}),
      required_role_id: requiredRole, sequence, is_active: isActive, description,
    };
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-xl">
        <DialogTitle>{editing ? `Edit ${rule!.name}` : 'New approval rule'}</DialogTitle>
        <form onSubmit={(e) => {
            e.preventDefault();
            setError(null);
            if (!name.trim()) return setError('Name required');
            if (!requiredRole) return setError('Pick a required role');
            if (conditionField && !conditionValue) return setError('Condition value required when field is set');
            mut.mutate();
          }}
          className="mt-4 space-y-3">
          <div className="grid sm:grid-cols-2 gap-3">
            <Field label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
            </Field>
            <Field label="Doctype">
              <NativeSelect value={doctype} onChange={setDoctype}
                options={doctypes.map((d) => ({ value: d, label: d }))} />
            </Field>
          </div>

          <div className="grid grid-cols-[1fr_100px_1fr] gap-2 items-end">
            <Field label="Condition field" hint="Field name on the document; e.g. grand_total.">
              <NativeSelect value={conditionField} onChange={setCF} options={COMMON_FIELDS} />
            </Field>
            <Field label="Operator">
              <NativeSelect value={conditionOp} onChange={setCO} options={CONDITION_OPS}
                />
            </Field>
            <Field label="Value">
              <Input value={conditionValue} onChange={(e) => setCV(e.target.value)}
                className="num text-right" disabled={!conditionField} placeholder="e.g. 50000000" />
            </Field>
          </div>
          {conditionField === '' && (
            <div className="text-caption text-stone -mt-1">
              No condition: the rule fires on every document of this type.
            </div>
          )}

          <div className="grid sm:grid-cols-2 gap-3">
            <Field label="Required role">
              <NativeSelect value={requiredRole} onChange={setRR}
                options={roles.map((r) => ({ value: r.id, label: r.name + (r.is_system ? ' (system)' : '') }))} />
            </Field>
            <Field label="Sequence" hint="Lower = earlier when multiple rules match.">
              <Input type="number" value={sequence} onChange={(e) => setSequence(Number(e.target.value))} className="num text-right" />
            </Field>
          </div>

          <Field label="Description" hint="Shown alongside the rule.">
            <Input value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>

          <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
            <input type="checkbox" className="accent-brand-green-deep" checked={isActive} onChange={(e) => setIsActive(e.target.checked)} />
            Active
          </label>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>{editing ? 'Save changes' : 'Create rule'}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ------------------ shared ------------------ */

function NativeSelect({
  value, options, onChange,
}: { value: string; options: { value: string; label: string }[]; onChange: (v: string) => void }) {
  return (
    <select
      className="input-base appearance-none pr-8 bg-no-repeat bg-[right_0.75rem_center] bg-[length:1.25rem] cursor-pointer"
      style={{ backgroundImage: "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>\")" }}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  );
}
