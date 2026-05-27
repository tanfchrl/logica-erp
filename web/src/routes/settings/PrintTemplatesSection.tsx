import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Plus, FileText, Image as ImageIcon, RefreshCw, Trash2, Upload,
  AlertCircle, Save, FileType, Star, Sparkles,
} from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field, Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { Dialog, DialogContent, DialogTitle } from '@/components/Dialog';
import { api, apiBlob } from '@/lib/api';
import { cn } from '@/lib/cn';

interface Letterhead {
  id: string;
  name: string;
  company_id?: string;
  is_default: boolean;
  logo_url?: string;
  header_html?: string;
  footer_html?: string;
  paper_size: string;
  margin_top: string;
  margin_bottom: string;
  margin_left: string;
  margin_right: string;
  updated_at: string;
}
interface LetterheadList { items: Letterhead[] }

interface PrintTemplate {
  id: string;
  doctype: string;
  name: string;
  company_id?: string;
  is_default: boolean;
  letterhead_id?: string;
  body_html: string;
  is_enabled: boolean;
  updated_at: string;
}
interface TemplateList { items: PrintTemplate[] }

interface DoctypeDef { key: string; label: string; has_bundled: boolean }
interface DoctypeList { items: DoctypeDef[] }

type Tab = 'letterheads' | 'templates';

export function PrintTemplatesSection() {
  const [tab, setTab] = useState<Tab>('templates');

  return (
    <div className="space-y-5">
      <div className="inline-flex items-center p-1 rounded-full bg-surface border border-hairline">
        <TabChip active={tab === 'templates'}   icon={FileType}   label="Templates"   onClick={() => setTab('templates')} />
        <TabChip active={tab === 'letterheads'} icon={ImageIcon}  label="Letterheads" onClick={() => setTab('letterheads')} />
      </div>

      {tab === 'templates'   && <TemplatesTab />}
      {tab === 'letterheads' && <LetterheadsTab />}
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

/* ======================== TEMPLATES TAB ======================== */

function TemplatesTab() {
  const qc = useQueryClient();
  const { data: doctypes }    = useQuery({ queryKey: ['print-doctypes'],  queryFn: () => api<DoctypeList>('/admin/print-templates/doctypes') });
  const { data: tpls, isLoading } = useQuery({ queryKey: ['print-templates'], queryFn: () => api<TemplateList>('/admin/print-templates') });
  const { data: lhs }         = useQuery({ queryKey: ['letterheads'],     queryFn: () => api<LetterheadList>('/admin/letterheads') });

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const items = tpls?.items ?? [];
  const selected = selectedId ? items.find((t) => t.id === selectedId) ?? null : items[0] ?? null;

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Print templates</CardTitle>
          <CardDescription>
            HTML templates rendered to PDF when someone clicks Print on a document.
            Go template syntax — variables like <span className="font-mono text-ink">{'{{.Invoice.Name}}'}</span> resolve at print time.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New template
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-40 w-full" /></Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <FileType className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No custom templates yet — print uses the bundled defaults.</div>
            <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> Create from bundled
            </Button>
          </div>
        </Card>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-[280px_1fr] gap-4">
          <Card padded={false}>
            <ul className="divide-y divide-hairline">
              {items.map((t) => (
                <li key={t.id}>
                  <button type="button" onClick={() => setSelectedId(t.id)}
                    className={cn('w-full text-left px-3 py-2.5 transition-colors',
                      t.id === selected?.id ? 'bg-surface' : 'hover:bg-surface-soft')}>
                    <div className="flex items-center gap-2">
                      <span className="text-body-sm font-medium text-ink truncate">{t.name}</span>
                      {t.is_default && <StatusPill tone="accent" withDot={false}><Star className="size-3" /> Default</StatusPill>}
                      {!t.is_enabled && <StatusPill tone="warning" withDot={false}>Disabled</StatusPill>}
                    </div>
                    <div className="text-caption text-stone font-mono">{t.doctype}</div>
                  </button>
                </li>
              ))}
            </ul>
          </Card>

          {selected && (
            <TemplateEditor
              template={selected}
              letterheads={lhs?.items ?? []}
              onSaved={() => qc.invalidateQueries({ queryKey: ['print-templates'] })}
              onDeleted={() => { setSelectedId(null); qc.invalidateQueries({ queryKey: ['print-templates'] }); }}
            />
          )}
        </div>
      )}

      {createOpen && (
        <CreateTemplateDialog
          doctypes={doctypes?.items ?? []}
          letterheads={lhs?.items ?? []}
          onClose={() => setCreateOpen(false)}
          onCreated={(t) => {
            void qc.invalidateQueries({ queryKey: ['print-templates'] });
            setSelectedId(t.id);
            setCreateOpen(false);
          }}
        />
      )}
    </div>
  );
}

function TemplateEditor({
  template, letterheads, onSaved, onDeleted,
}: {
  template: PrintTemplate;
  letterheads: Letterhead[];
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const [name, setName]               = useState(template.name);
  const [body, setBody]               = useState(template.body_html);
  const [letterheadId, setLh]         = useState(template.letterhead_id ?? '');
  const [isDefault, setIsDefault]     = useState(template.is_default);
  const [isEnabled, setIsEnabled]     = useState(template.is_enabled);
  const [error, setError]             = useState<string | null>(null);

  // Re-seed local state when the user switches selection.
  useEffect(() => {
    setName(template.name);
    setBody(template.body_html);
    setLh(template.letterhead_id ?? '');
    setIsDefault(template.is_default);
    setIsEnabled(template.is_enabled);
    setError(null);
  }, [template.id]);

  const selectedLh = letterheads.find((l) => l.id === letterheadId) ?? null;

  const save = useMutation({
    mutationFn: () => api<PrintTemplate>(`/admin/print-templates/${template.id}`, {
      method: 'PUT',
      body: {
        doctype: template.doctype, name,
        ...(letterheadId ? { letterhead_id: letterheadId } : {}),
        is_default: isDefault, body_html: body, is_enabled: isEnabled,
      },
    }),
    onSuccess: () => { onSaved(); setError(null); },
    onError:   (e: Error) => setError(e.message),
  });

  const del = useMutation({
    mutationFn: () => api<void>(`/admin/print-templates/${template.id}`, { method: 'DELETE' }),
    onSuccess: () => onDeleted(),
  });

  return (
    <Card padded={false}>
      <div className="px-5 py-4 border-b border-hairline flex items-end justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <CardTitle>{template.name}</CardTitle>
            <span className="text-caption font-mono text-stone">{template.doctype}</span>
          </div>
          <CardDescription>
            Last updated {new Date(template.updated_at).toLocaleString('id-ID')}
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="ghost"
            onClick={() => { if (confirm(`Delete template "${template.name}"?`)) del.mutate(); }}>
            <Trash2 className="size-3.5" />
          </Button>
          <Button size="sm" onClick={() => save.mutate()} loading={save.isPending}>
            <Save className="size-3.5" /> Save
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 divide-y lg:divide-y-0 lg:divide-x divide-hairline">
        {/* ---- Editor ---- */}
        <div className="p-5 space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label="Letterhead">
              <NativeSelect value={letterheadId} onChange={setLh}
                options={[{ value: '', label: '— None —' }, ...letterheads.map((l) => ({ value: l.id, label: l.name }))]} />
            </Field>
          </div>

          <div className="flex items-center gap-5 flex-wrap text-body-sm text-charcoal">
            <label className="inline-flex items-center gap-2 cursor-pointer">
              <input type="checkbox" className="accent-brand-green-deep" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} />
              Default for {template.doctype}
            </label>
            <label className="inline-flex items-center gap-2 cursor-pointer">
              <input type="checkbox" className="accent-brand-green-deep" checked={isEnabled} onChange={(e) => setIsEnabled(e.target.checked)} />
              Enabled
            </label>
          </div>

          <Field label="HTML body" hint="Go text/template. Variables resolve from the document context at print time.">
            <textarea
              className="input-base !h-auto !py-2 font-mono text-[12px] leading-snug"
              rows={20}
              value={body}
              onChange={(e) => setBody(e.target.value)}
              spellCheck={false}
            />
          </Field>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
              <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
            </div>
          )}
        </div>

        {/* ---- Preview ---- */}
        <PreviewPane
          doctype={template.doctype}
          body={body}
          letterhead={selectedLh}
        />
      </div>
    </Card>
  );
}

/* ----- Preview pane (used by both template & letterhead editor) ----- */

function PreviewPane({
  doctype, body, letterhead,
}: { doctype: string; body: string; letterhead: Letterhead | null }) {
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const lastUrl = useRef<string | null>(null);

  const render = useMutation({
    mutationFn: async () => {
      const blob = await apiBlob('/admin/print-templates/preview', {
        method: 'POST',
        body: {
          doctype, body_html: body,
          header_html:  letterhead?.header_html ?? '',
          footer_html:  letterhead?.footer_html ?? '',
          logo_url:     letterhead?.logo_url    ?? '',
          paper_size:   letterhead?.paper_size  ?? 'A4',
          margin_top:    letterhead?.margin_top    ?? '',
          margin_bottom: letterhead?.margin_bottom ?? '',
          margin_left:   letterhead?.margin_left   ?? '',
          margin_right:  letterhead?.margin_right  ?? '',
        },
      });
      return URL.createObjectURL(blob);
    },
    onSuccess: (u) => {
      if (lastUrl.current) URL.revokeObjectURL(lastUrl.current);
      lastUrl.current = u;
      setUrl(u);
      setError(null);
    },
    onError: (e: Error) => setError(e.message),
  });

  useEffect(() => {
    return () => { if (lastUrl.current) URL.revokeObjectURL(lastUrl.current); };
  }, []);

  return (
    <div className="p-5 space-y-3 min-h-[420px] flex flex-col">
      <div className="flex items-end justify-between gap-3">
        <div>
          <div className="label-base">Preview</div>
          <div className="text-caption text-stone">Renders with built-in sample data via Gotenberg.</div>
        </div>
        <Button size="sm" variant="secondary" onClick={() => render.mutate()} loading={render.isPending}>
          <RefreshCw className="size-3.5" /> Render
        </Button>
      </div>

      {error && (
        <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2 inline-flex items-start gap-2">
          <AlertCircle className="size-4 mt-0.5 shrink-0" /> {error}
        </div>
      )}

      <div className="flex-1 min-h-[400px] rounded-md border border-hairline bg-surface-soft overflow-hidden">
        {url ? (
          <iframe src={url} title="Preview" className="size-full" />
        ) : (
          <div className="size-full flex items-center justify-center text-stone text-caption">
            <div className="text-center">
              <Sparkles className="mx-auto size-5 mb-1.5 text-stone" />
              Click <span className="font-medium text-ink">Render</span> to generate a preview PDF.
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/* ----- Create template dialog ----- */

function CreateTemplateDialog({
  doctypes, letterheads, onClose, onCreated,
}: {
  doctypes: DoctypeDef[];
  letterheads: Letterhead[];
  onClose: () => void;
  onCreated: (t: PrintTemplate) => void;
}) {
  const [doctype, setDoctype]   = useState(doctypes[0]?.key ?? 'sales_invoice');
  const [name, setName]         = useState('Default — custom');
  const [letterheadId, setLh]   = useState(letterheads.find((l) => l.is_default)?.id ?? '');
  const [body, setBody]         = useState('');
  const [error, setError]       = useState<string | null>(null);

  // Auto-fetch the bundled source when the user picks a doctype.
  const { data: bundled, isFetching } = useQuery({
    queryKey: ['print-bundled', doctype],
    queryFn: () => api<{ has_bundled: boolean; body_html?: string }>(`/admin/print-templates/bundled/${doctype}`),
  });
  useEffect(() => {
    if (bundled?.has_bundled && bundled.body_html && body === '') {
      setBody(bundled.body_html);
    } else if (bundled && !bundled.has_bundled && body === '') {
      setBody(`<h1>${doctype}</h1>\n<p>Your custom template goes here.</p>`);
    }
  }, [bundled]);

  // When doctype switches, reset body so we re-fetch from bundled.
  function onDoctypeChange(v: string) {
    setDoctype(v);
    setBody('');
  }

  const mut = useMutation({
    mutationFn: () => api<PrintTemplate>('/admin/print-templates', {
      method: 'POST',
      body: {
        doctype, name,
        ...(letterheadId ? { letterhead_id: letterheadId } : {}),
        is_default: false, body_html: body, is_enabled: true,
      },
    }),
    onSuccess: (t) => onCreated(t),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>New print template</DialogTitle>
        <form onSubmit={(e) => { e.preventDefault(); setError(null); mut.mutate(); }}
          className="mt-4 space-y-3">
          <div className="grid sm:grid-cols-2 gap-3">
            <Field label="Doctype">
              <NativeSelect value={doctype} onChange={onDoctypeChange}
                options={doctypes.map((d) => ({ value: d.key, label: d.label + (d.has_bundled ? '' : ' (no bundled)') }))} />
            </Field>
            <Field label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
          </div>
          <Field label="Letterhead">
            <NativeSelect value={letterheadId} onChange={setLh}
              options={[{ value: '', label: '— None —' }, ...letterheads.map((l) => ({ value: l.id, label: l.name }))]} />
          </Field>
          <Field label="Body HTML" hint={isFetching ? 'Loading bundled source…' : 'Pre-filled from the bundled template; edit before saving.'}>
            <textarea className="input-base !h-auto !py-2 font-mono text-[12px] leading-snug"
              rows={12} value={body} onChange={(e) => setBody(e.target.value)} spellCheck={false} />
          </Field>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create template</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ======================== LETTERHEADS TAB ======================== */

function LetterheadsTab() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['letterheads'], queryFn: () => api<LetterheadList>('/admin/letterheads') });
  const items = data?.items ?? [];

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const selected = selectedId ? items.find((l) => l.id === selectedId) ?? null : items[0] ?? null;

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <CardTitle>Letterheads</CardTitle>
          <CardDescription>
            Brand-stamped header + footer that wraps every printed document. Pick paper size and margins here.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" /> New letterhead
        </Button>
      </div>

      {isLoading ? (
        <Card><Skeleton className="h-32 w-full" /></Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <ImageIcon className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No letterheads yet.</div>
            <div className="text-caption text-stone mt-1">Create one to apply your brand to every PDF.</div>
            <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" /> Create letterhead
            </Button>
          </div>
        </Card>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-[280px_1fr] gap-4">
          <Card padded={false}>
            <ul className="divide-y divide-hairline">
              {items.map((l) => (
                <li key={l.id}>
                  <button type="button" onClick={() => setSelectedId(l.id)}
                    className={cn('w-full text-left px-3 py-2.5 flex items-center gap-3 transition-colors',
                      l.id === selected?.id ? 'bg-surface' : 'hover:bg-surface-soft')}>
                    <div className="size-9 rounded-md bg-surface text-ink inline-flex items-center justify-center overflow-hidden shrink-0">
                      {l.logo_url
                        ? <img src={l.logo_url} alt="" className="size-full object-contain" />
                        : <ImageIcon className="size-4 text-stone" />}
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="text-body-sm font-medium text-ink truncate">{l.name}</span>
                        {l.is_default && <StatusPill tone="accent" withDot={false}><Star className="size-3" /> Default</StatusPill>}
                      </div>
                      <div className="text-caption text-stone">{l.paper_size}</div>
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          </Card>

          {selected && (
            <LetterheadEditor letterhead={selected}
              onSaved={() => qc.invalidateQueries({ queryKey: ['letterheads'] })}
              onDeleted={() => { setSelectedId(null); qc.invalidateQueries({ queryKey: ['letterheads'] }); }}
            />
          )}
        </div>
      )}

      {createOpen && (
        <CreateLetterheadDialog
          onClose={() => setCreateOpen(false)}
          onCreated={(l) => { void qc.invalidateQueries({ queryKey: ['letterheads'] }); setSelectedId(l.id); setCreateOpen(false); }}
        />
      )}
    </div>
  );
}

function LetterheadEditor({
  letterhead, onSaved, onDeleted,
}: { letterhead: Letterhead; onSaved: () => void; onDeleted: () => void }) {
  const [name, setName]             = useState(letterhead.name);
  const [logoUrl, setLogoUrl]       = useState(letterhead.logo_url ?? '');
  const [header, setHeader]         = useState(letterhead.header_html ?? '');
  const [footer, setFooter]         = useState(letterhead.footer_html ?? '');
  const [paperSize, setPaperSize]   = useState(letterhead.paper_size);
  const [marginTop, setMT]          = useState(String(letterhead.margin_top));
  const [marginBottom, setMB]       = useState(String(letterhead.margin_bottom));
  const [marginLeft, setML]         = useState(String(letterhead.margin_left));
  const [marginRight, setMR]        = useState(String(letterhead.margin_right));
  const [isDefault, setIsDefault]   = useState(letterhead.is_default);
  const [error, setError]           = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  // Re-seed on selection change.
  useEffect(() => {
    setName(letterhead.name);
    setLogoUrl(letterhead.logo_url ?? '');
    setHeader(letterhead.header_html ?? '');
    setFooter(letterhead.footer_html ?? '');
    setPaperSize(letterhead.paper_size);
    setMT(String(letterhead.margin_top)); setMB(String(letterhead.margin_bottom));
    setML(String(letterhead.margin_left)); setMR(String(letterhead.margin_right));
    setIsDefault(letterhead.is_default);
    setError(null);
  }, [letterhead.id]);

  async function pickLogo(file: File) {
    if (file.size > 512 * 1024) { alert('Image must be under 512 KB.'); return; }
    const r = new FileReader();
    r.onload = () => setLogoUrl(r.result as string);
    r.readAsDataURL(file);
  }

  const save = useMutation({
    mutationFn: () => api<Letterhead>(`/admin/letterheads/${letterhead.id}`, {
      method: 'PUT',
      body: {
        name, is_default: isDefault, logo_url: logoUrl,
        header_html: header, footer_html: footer,
        paper_size: paperSize, margin_top: marginTop, margin_bottom: marginBottom,
        margin_left: marginLeft, margin_right: marginRight,
      },
    }),
    onSuccess: () => { onSaved(); setError(null); },
    onError:   (e: Error) => setError(e.message),
  });
  const del = useMutation({
    mutationFn: () => api<void>(`/admin/letterheads/${letterhead.id}`, { method: 'DELETE' }),
    onSuccess: () => onDeleted(),
  });

  // Build a synthetic letterhead for the preview pane that reflects current form state.
  const livePreview: Letterhead = useMemo(() => ({
    ...letterhead, name, logo_url: logoUrl,
    header_html: header, footer_html: footer,
    paper_size: paperSize, margin_top: marginTop,
    margin_bottom: marginBottom, margin_left: marginLeft, margin_right: marginRight,
  }), [letterhead, name, logoUrl, header, footer, paperSize, marginTop, marginBottom, marginLeft, marginRight]);

  return (
    <Card padded={false}>
      <div className="px-5 py-4 border-b border-hairline flex items-end justify-between gap-3">
        <div>
          <CardTitle>{letterhead.name}</CardTitle>
          <CardDescription>{paperSize} · margins {marginTop}/{marginRight}/{marginBottom}/{marginLeft} in</CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="ghost"
            onClick={() => { if (confirm(`Delete letterhead "${letterhead.name}"?`)) del.mutate(); }}>
            <Trash2 className="size-3.5" />
          </Button>
          <Button size="sm" onClick={() => save.mutate()} loading={save.isPending}>
            <Save className="size-3.5" /> Save
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 divide-y lg:divide-y-0 lg:divide-x divide-hairline">
        <div className="p-5 space-y-3">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>

          <div className="space-y-1.5">
            <div className="label-base">Logo</div>
            <div className="flex items-center gap-3">
              <div className="size-16 rounded-md bg-surface border border-hairline overflow-hidden inline-flex items-center justify-center">
                {logoUrl
                  ? <img src={logoUrl} alt="" className="size-full object-contain" />
                  : <ImageIcon className="size-5 text-stone" />}
              </div>
              <div className="flex items-center gap-2">
                <Button type="button" variant="secondary" size="sm" onClick={() => fileRef.current?.click()}>
                  <Upload className="size-3.5" /> {logoUrl ? 'Replace' : 'Upload'}
                </Button>
                {logoUrl && <Button type="button" variant="ghost" size="sm" onClick={() => setLogoUrl('')}>
                  <Trash2 className="size-3.5" />
                </Button>}
                <input ref={fileRef} type="file" accept="image/*" className="hidden"
                  onChange={(e) => { const f = e.target.files?.[0]; if (f) void pickLogo(f); e.target.value = ''; }} />
              </div>
            </div>
            <p className="text-caption text-stone">PNG / SVG / JPG. Max 512 KB. Encoded as data URL.</p>
          </div>

          <Field label="Header HTML" hint="Rendered above the document body.">
            <textarea className="input-base !h-auto !py-2 font-mono text-[12px] leading-snug" rows={5}
              value={header} onChange={(e) => setHeader(e.target.value)} spellCheck={false} />
          </Field>

          <Field label="Footer HTML">
            <textarea className="input-base !h-auto !py-2 font-mono text-[12px] leading-snug" rows={4}
              value={footer} onChange={(e) => setFooter(e.target.value)} spellCheck={false} />
          </Field>

          <div className="grid grid-cols-5 gap-2">
            <Field label="Paper">
              <NativeSelect value={paperSize} onChange={setPaperSize}
                options={[{ value: 'A4', label: 'A4' }, { value: 'Letter', label: 'Letter' }, { value: 'Legal', label: 'Legal' }]} />
            </Field>
            <Field label="Top (in)"><Input className="text-right num" value={marginTop} onChange={(e) => setMT(e.target.value)} /></Field>
            <Field label="Right">  <Input className="text-right num" value={marginRight} onChange={(e) => setMR(e.target.value)} /></Field>
            <Field label="Bottom"> <Input className="text-right num" value={marginBottom} onChange={(e) => setMB(e.target.value)} /></Field>
            <Field label="Left">   <Input className="text-right num" value={marginLeft} onChange={(e) => setML(e.target.value)} /></Field>
          </div>

          <label className="inline-flex items-center gap-2 text-body-sm text-charcoal cursor-pointer">
            <input type="checkbox" className="accent-brand-green-deep" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} />
            Default letterhead for this scope
          </label>

          {error && (
            <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>
          )}
        </div>

        <PreviewPane doctype="sales_invoice" body="" letterhead={livePreview} />
      </div>
    </Card>
  );
}

function CreateLetterheadDialog({
  onClose, onCreated,
}: { onClose: () => void; onCreated: (l: Letterhead) => void }) {
  const [name, setName] = useState('Default letterhead');
  const [error, setError] = useState<string | null>(null);
  const mut = useMutation({
    mutationFn: () => api<Letterhead>('/admin/letterheads', {
      method: 'POST',
      body: {
        name, is_default: true, paper_size: 'A4',
        margin_top: '0.5', margin_bottom: '0.5', margin_left: '0.5', margin_right: '0.5',
        header_html: `<div><strong>{{.Company.LegalName}}</strong><br>
{{if .Company.NPWP}}NPWP: {{.Company.NPWP}}<br>{{end}}
{{if .Company.AddressLine}}{{.Company.AddressLine}}{{end}}</div>`,
        footer_html: `<div style="text-align:center;color:#999">Generated by Logica ERP · {{.Company.Website}}</div>`,
      },
    }),
    onSuccess: (l) => onCreated(l),
    onError:   (e: Error) => setError(e.message),
  });

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent>
        <DialogTitle>New letterhead</DialogTitle>
        <form onSubmit={(e) => { e.preventDefault(); if (!name.trim()) return setError('Name required'); mut.mutate(); }}
          className="mt-4 space-y-3">
          <Field label="Name" hint="A starter letterhead is created with sample header/footer; edit after creation.">
            <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
          </Field>
          {error && <div className="rounded-md bg-brand-error/10 text-brand-error text-caption px-3 py-2">{error}</div>}
          <div className="flex justify-end gap-2 pt-2 border-t border-hairline">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" loading={mut.isPending}>Create letterhead</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* ----- shared ----- */

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
