import { Command } from 'cmdk';
import * as RD from '@radix-ui/react-dialog';
import { useEffect, useMemo } from 'react';
import { useNavigate, useRouterState } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { motion, AnimatePresence } from 'framer-motion';
import {
  Home, Receipt, Wallet, Package, Warehouse, Factory, Briefcase, Users, UserSquare,
  ClipboardList, Headphones, Settings, Sparkles, Moon, Sun, Plus, FileText, ArrowRight, BarChart3,
  ShoppingBag, LogOut, Search, History,
} from 'lucide-react';
import { useUI } from '@/store/ui';
import { Kbd } from '@/components/Kbd';
import { logout } from '@/lib/auth';
import { api, getAccessToken, getActiveCompany } from '@/lib/api';

interface PaletteItem {
  id: string;
  label: string;
  hint?: string;
  shortcut?: string;
  icon: React.ComponentType<{ className?: string }>;
  onSelect: () => void;
  keywords?: string;
  /** Violet-tinted "AI action" styling — distinguishes LLM ops from deterministic ones. */
  aiAccent?: boolean;
}

interface ContractItem {
  module: string;
  display_name?: string;
  suggested_prompts?: string[];
}
interface ContractsResp { items: ContractItem[] }
interface SessionSummary { id: string; title: string; kind: string; updated_at: string }
interface SessionListResp { items: SessionSummary[] }

// Active module from the URL: the first path segment maps 1:1 to a contract slug
// (accounting, stock, crm, hr, manufacturing, projects, assets, support, pos).
function activeModuleFromPath(pathname: string): string | null {
  const seg = pathname.split('/').filter(Boolean)[0];
  if (!seg) return null;
  const known = new Set(['accounting', 'stock', 'crm', 'hr', 'manufacturing', 'projects', 'assets', 'support', 'pos']);
  return known.has(seg) ? seg : null;
}

async function fetchAgent<T>(path: string): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const co = getActiveCompany();
  if (co) headers['X-Company-Id'] = co;
  const r = await fetch(path, { headers });
  const t = await r.text();
  if (!r.ok) throw new Error(t || r.statusText);
  return (t ? JSON.parse(t) : {}) as T;
}

export function CommandPalette() {
  const { paletteOpen, setPaletteOpen, theme, toggleTheme } = useUI();
  const openCopilotWith = useUI((s) => s.openCopilotWith);
  const openCopilotSession = useUI((s) => s.openCopilotSession);
  const navigate = useNavigate();
  const router = useRouterState();
  const activeModule = activeModuleFromPath(router.location.pathname);

  // Contract registry — same source CopilotPanel uses. Cheap to keep around;
  // we re-derive the active-module slice on every open.
  const { data: contracts } = useQuery({
    queryKey: ['agent-contracts'],
    queryFn:  () => api<ContractsResp>('/agent/contracts'),
    staleTime: 5 * 60_000,
  });

  // Recent copilot sessions — fetched lazily, only when the palette opens, so
  // the user always sees fresh titles after they've named a conversation.
  const { data: recentSessions } = useQuery({
    queryKey: ['agent-sessions', 'palette', paletteOpen],
    queryFn:  () => fetchAgent<SessionListResp>('/api/agent/v1/sessions?kind=copilot'),
    enabled:  paletteOpen,
    staleTime: 30_000,
  });

  // ⌘K / Ctrl+K toggle anywhere.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen(!paletteOpen);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [paletteOpen, setPaletteOpen]);

  const close = () => setPaletteOpen(false);
  const go = (to: string) => () => { close(); void navigate({ to }); };
  const ai = (prompt: string) => () => { close(); openCopilotWith(prompt); };
  const resumeSession = (sid: string) => () => { close(); openCopilotSession(sid); };

  // Items sourced from the active module's contract: one chip per
  // `suggested_prompts` entry. Bounded to 6 to keep the section tight.
  const contractItems: PaletteItem[] = useMemo(() => {
    if (!activeModule) return [];
    const c = contracts?.items.find((x) => x.module === activeModule);
    if (!c?.suggested_prompts?.length) return [];
    return c.suggested_prompts.slice(0, 6).map((prompt, i): PaletteItem => ({
      id:        `ai-mod-${activeModule}-${i}`,
      label:     prompt,
      keywords:  `ai copilot ${activeModule} ${c.display_name ?? ''}`,
      icon:      Sparkles,
      aiAccent:  true,
      onSelect:  ai(prompt),
    }));
  }, [contracts, activeModule]);

  // Recent copilot conversations — picked up by the palette when open. Each
  // selection re-opens the panel and asks it to load that session's history.
  const recentSessionItems: PaletteItem[] = useMemo(() => {
    return (recentSessions?.items ?? []).slice(0, 5).map((s): PaletteItem => ({
      id:        `ai-session-${s.id}`,
      label:     s.title || 'Untitled conversation',
      hint:      new Date(s.updated_at).toLocaleString(),
      keywords:  `ai copilot session history resume ${s.title}`,
      icon:      History,
      aiAccent:  true,
      onSelect:  resumeSession(s.id),
    }));
  }, [recentSessions]);

  const primaryActions: PaletteItem[] = [
    { id: 'new-si', label: 'New Sales Invoice', icon: Receipt, shortcut: 'N S', onSelect: go('/accounting/sales-invoices?new=1'), keywords: 'invoice billing' },
    { id: 'new-pi', label: 'New Purchase Invoice', icon: ShoppingBag, onSelect: go('/accounting/purchase-invoices?new=1'), keywords: 'vendor bill' },
    { id: 'new-pe', label: 'New Payment Entry', icon: Wallet, onSelect: go('/accounting/payment-entries?new=1') },
    { id: 'new-je', label: 'New Journal Entry', icon: FileText, onSelect: go('/accounting/journal-entries?new=1') },
    { id: 'new-item', label: 'New Item', icon: Package, onSelect: go('/accounting/items?new=1') },
  ];

  const navigation: PaletteItem[] = [
    { id: 'go-home',         label: 'Go to Home',         icon: Home,          shortcut: 'G H', onSelect: go('/') },
    { id: 'go-accounting',   label: 'Go to Finance',      icon: Wallet,        shortcut: 'G A', onSelect: go('/accounting') },
    { id: 'go-items',        label: 'Go to Items',        icon: Package,       shortcut: 'G I', onSelect: go('/accounting/items') },
    { id: 'go-stock',        label: 'Go to Stock',        icon: Warehouse,     onSelect: go('/stock') },
    { id: 'go-mfg',          label: 'Go to Production',   icon: Factory,       onSelect: go('/manufacturing') },
    { id: 'go-projects',     label: 'Go to Operations',   icon: Briefcase,     onSelect: go('/projects') },
    { id: 'go-crm',          label: 'Go to CRM',          icon: UserSquare,    onSelect: go('/crm') },
    { id: 'go-hr',           label: 'Go to HR & Payroll', icon: Users,         onSelect: go('/hr') },
    { id: 'go-assets',       label: 'Go to Asset & Inventory', icon: ClipboardList, onSelect: go('/assets') },
    { id: 'go-support',      label: 'Go to Helpdesk',     icon: Headphones,    onSelect: go('/support') },
    { id: 'go-reports',      label: 'Go to Reports',      icon: BarChart3,     onSelect: go('/accounting/reports') },
    { id: 'go-settings',     label: 'Open Settings',      icon: Settings,      onSelect: go('/settings') },
    { id: 'go-appearance',   label: 'Appearance settings',icon: Sparkles,      onSelect: go('/settings') },
  ];

  // "Tindakan AI" — items that prompt the Copilot instead of navigating.
  // Visually distinct (violet tint via PaletteItem.aiAccent so the user
  // never confuses an LLM action with a deterministic one).
  const aiActions: PaletteItem[] = [
    { id: 'ai-ar-aging',
      label: 'AR aging bulan ini',
      keywords: 'ai copilot piutang aging receivables',
      icon: Sparkles, aiAccent: true,
      onSelect: ai('Tampilkan AR aging untuk bulan ini.') },
    { id: 'ai-overdue-si',
      label: 'Sales Invoice yang overdue',
      keywords: 'ai copilot overdue invoice',
      icon: Sparkles, aiAccent: true,
      onSelect: ai('Sales Invoice mana yang overdue lebih dari 30 hari?') },
    { id: 'ai-cash-balance',
      label: 'Kas + bank hari ini',
      keywords: 'ai copilot saldo kas bank cash',
      icon: Sparkles, aiAccent: true,
      onSelect: ai('Berapa saldo Kas dan Bank saat ini?') },
    { id: 'ai-drafts',
      label: 'Draft yang belum di-submit',
      keywords: 'ai copilot drafts pending',
      icon: Sparkles, aiAccent: true,
      onSelect: ai('Tampilkan semua draft yang belum di-submit lebih dari 3 hari.') },
    { id: 'ai-open',
      label: 'Buka Copilot…',
      keywords: 'ai copilot assistant chat tanya',
      icon: Sparkles, aiAccent: true,
      onSelect: ai('') },
  ];

  const utilities: PaletteItem[] = [
    {
      id: 'theme',
      label: theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme',
      icon: theme === 'dark' ? Sun : Moon,
      shortcut: 'T',
      onSelect: () => { toggleTheme(); close(); },
    },
    {
      id: 'logout',
      label: 'Log out',
      icon: LogOut,
      onSelect: async () => { close(); await logout(); window.location.href = '/login'; },
    },
  ];

  return (
    <RD.Root open={paletteOpen} onOpenChange={setPaletteOpen}>
      <AnimatePresence>
        {paletteOpen && (
          <RD.Portal forceMount>
            <RD.Overlay asChild>
              <motion.div
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                transition={{ duration: 0.12 }}
                className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px]"
              />
            </RD.Overlay>
            <RD.Content asChild>
              <motion.div
                initial={{ opacity: 0, y: 8, scale: 0.98 }}
                animate={{ opacity: 1, y: 0, scale: 1 }}
                exit={{ opacity: 0, y: 4, scale: 0.98 }}
                transition={{ duration: 0.15, ease: [0.4, 0, 0.2, 1] }}
                className="fixed inset-x-4 top-[14vh] mx-auto z-50 w-auto max-w-[640px] sm:inset-x-0 sm:w-[calc(100vw-2rem)]"
              >
                <RD.Title className="sr-only">Command palette</RD.Title>
                <RD.Description className="sr-only">Type a command or search.</RD.Description>

                <div className="surface-card !p-0 overflow-hidden shadow-overlay">
                  <Command label="Command palette" loop>
                    <div className="flex items-center gap-2 px-4 border-b border-border">
                      <Search className="size-4 text-text-tertiary shrink-0" />
                      <Command.Input
                        autoFocus
                        placeholder="Type a command or search anything…"
                        className="!border-0 !py-3.5 !px-0 flex-1"
                      />
                      <Kbd>ESC</Kbd>
                    </div>

                    <Command.List>
                      <Command.Empty>No results.</Command.Empty>

                      <Command.Group heading="Quick create">
                        {primaryActions.map((it) => (
                          <Item key={it.id} item={it} />
                        ))}
                      </Command.Group>

                      <Command.Group heading="Tindakan AI">
                        {aiActions.map((it) => (
                          <Item key={it.id} item={it} />
                        ))}
                      </Command.Group>

                      {contractItems.length > 0 && (
                        <Command.Group heading={`Tindakan AI · modul aktif`}>
                          {contractItems.map((it) => (
                            <Item key={it.id} item={it} />
                          ))}
                        </Command.Group>
                      )}

                      {recentSessionItems.length > 0 && (
                        <Command.Group heading="Percakapan terakhir">
                          {recentSessionItems.map((it) => (
                            <Item key={it.id} item={it} />
                          ))}
                        </Command.Group>
                      )}

                      <Command.Group heading="Navigation">
                        {navigation.map((it) => (
                          <Item key={it.id} item={it} />
                        ))}
                      </Command.Group>

                      <Command.Group heading="Utilities">
                        {utilities.map((it) => (
                          <Item key={it.id} item={it} />
                        ))}
                      </Command.Group>
                    </Command.List>

                    <div className="flex items-center justify-between px-4 py-2 border-t border-border text-caption text-text-tertiary">
                      <div className="flex items-center gap-3">
                        <span className="flex items-center gap-1"><Kbd>↑</Kbd><Kbd>↓</Kbd> navigate</span>
                        <span className="flex items-center gap-1"><Kbd>⏎</Kbd> select</span>
                      </div>
                      <span>Logica ERP</span>
                    </div>
                  </Command>
                </div>
              </motion.div>
            </RD.Content>
          </RD.Portal>
        )}
      </AnimatePresence>
    </RD.Root>
  );
}

function Item({ item }: { item: PaletteItem }) {
  // AI items get a violet wash on the active row so users can't mistake an
  // LLM-driven action for a deterministic one. The data-[selected] CSS
  // selector matches cmdk's active-row marker.
  const ai = item.aiAccent;
  return (
    <Command.Item
      value={`${item.label} ${item.keywords ?? ''}`}
      onSelect={item.onSelect}
      className={ai ? 'data-[selected=true]:!bg-accent/15 data-[selected=true]:!text-accent' : ''}
    >
      <item.icon className={ai ? 'size-4 text-accent' : 'size-4 text-text-tertiary'} />
      <span className="flex-1">{item.label}</span>
      {item.hint && <span className="text-caption text-text-tertiary">{item.hint}</span>}
      {item.shortcut && (
        <span className="flex items-center gap-1 ml-2">
          {item.shortcut.split(' ').map((k, i) => <Kbd key={i}>{k}</Kbd>)}
        </span>
      )}
      <ArrowRight className="size-3 text-text-tertiary opacity-0 group-data-[selected=true]:opacity-100" />
    </Command.Item>
  );
}
