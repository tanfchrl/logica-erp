import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Sparkles, X, SendHorizonal, RotateCw, Wrench, Check, FileText, Minus } from 'lucide-react';
import { api, getAccessToken, getActiveCompany } from '@/lib/api';
import { cn } from '@/lib/cn';
import { useUI } from '@/store/ui';

/**
 * Floating Copilot panel — Intercom-style bottom-right card.
 *
 * Non-modal: the page behind it stays interactive, so the user can keep
 * navigating while a long-running chat turn streams. State (turns,
 * sessionId, draft) lives in the component, which stays mounted in the
 * shell — close/reopen and route changes don't lose history. Explicit
 * reset only via the "New conversation" icon.
 *
 * Three visual states driven by useUI store flags:
 *   - hidden    (!open && !minimized): nothing rendered
 *   - minimized (!open && minimized):  small chip in bottom-right
 *   - open      (open):                floating card
 *
 * The panel talks to the standalone Agent service at /api/agent/v1 (port
 * 8090 in dev, behind a proxy in prod) — see cmd/agent. It forwards the
 * user's existing JWT; no separate login.
 */

interface ContractsResp {
  items: Array<{
    module: string;
    display_name: string;
    suggested_prompts?: string[];
  }>;
}
// ChatTurn is now a discriminated union — assistant turns are mutable
// during streaming, tool turns and proposals are inline markers that
// help the user follow what the agent is doing.
type ChatTurn =
  | { kind: 'user';      content: string; at: number }
  | { kind: 'assistant'; content: string; at: number; streaming?: boolean }
  | { kind: 'tool';      name: string;    ok?: boolean; at: number }
  | { kind: 'proposal';  doctype: string; document_name: string; document_id: string; at: number };

// CopilotPanel has no props — it's a singleton mounted in TopChrome and
// drives its visibility from the useUI store. Keeping it propless ensures
// the chat state hidden in component-local hooks persists across the open/
// minimize/close lifecycle.

// streamChat opens POST /chat/stream as a Server-Sent Events stream. Each
// SSE event is wrapped to the handler as a tagged object so React state
// updates stay narrow. Returns when the stream ends or onEvent throws.
//
// SSE wire format (from the Go handler):
//   event: <kind>\ndata: <json>\n\n
async function streamChat(
  body: { session_id?: string; message: string },
  onEvent: (ev: AgentSSE) => void,
  signal?: AbortSignal,
): Promise<void> {
  const headers: Record<string, string> = {
    Accept: 'text/event-stream',
    'Content-Type': 'application/json',
  };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const co = getActiveCompany();
  if (co) headers['X-Company-Id'] = co;

  const resp = await fetch('/api/agent/v1/chat/stream', {
    method: 'POST', headers, body: JSON.stringify(body), signal,
  });
  if (!resp.ok || !resp.body) {
    const text = await resp.text();
    throw new Error(text || resp.statusText);
  }

  const reader = resp.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    // SSE events are separated by a blank line.
    let idx: number;
    while ((idx = buf.indexOf('\n\n')) !== -1) {
      const rawEvent = buf.slice(0, idx);
      buf = buf.slice(idx + 2);
      const ev = parseSSE(rawEvent);
      if (ev) onEvent(ev);
    }
  }
}

// agentSessions: small fetch helper for the non-streaming session endpoints.
// Same shape as the SSE wrapper but returns JSON.
async function agentSessions<T = unknown>(path: string): Promise<T> {
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

interface SessionSummary {
  id: string;
  title: string;
  kind: string;
  updated_at: string;
}
interface SessionList { items: SessionSummary[] }
interface SessionMessage {
  id: string;
  role: 'user' | 'assistant' | 'tool' | 'system';
  content: string;
  tool_name?: string;
  created_at: string;
}
interface SessionMessages { items: SessionMessage[] }

type AgentSSE =
  | { kind: 'session';     data: { session_id: string } }
  | { kind: 'tool_call';   data: { name: string; arguments: string } }
  | { kind: 'tool_result'; data: { name: string; ok: boolean } }
  | { kind: 'proposal';    data: { doctype: string; document_id: string; document_name: string } }
  | { kind: 'delta';       data: { content: string } }
  | { kind: 'done';        data: { session_id: string; turn: number } }
  | { kind: 'error';       data: { message: string } };

function parseSSE(raw: string): AgentSSE | null {
  let event = 'message';
  let data = '';
  for (const line of raw.split('\n')) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) data += line.slice(5).trim();
  }
  if (!event || !data) return null;
  try {
    return { kind: event, data: JSON.parse(data) } as AgentSSE;
  } catch {
    return null;
  }
}

export function CopilotPanel() {
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();
  const open            = useUI((s) => s.copilotOpen);
  const minimized       = useUI((s) => s.copilotMinimized);
  const openCopilot     = useUI((s) => s.openCopilot);
  const minimizeCopilot = useUI((s) => s.minimizeCopilot);
  const closeCopilot    = useUI((s) => s.closeCopilot);
  // Seed prompt set by openCopilotWith() (e.g. from a nudge CTA). When the
  // panel opens and a seed is pending, we fire it as a chat turn and clear
  // the store so future opens don't re-send.
  const copilotSeedPrompt = useUI((s) => s.copilotSeedPrompt);
  const clearCopilotSeed = useUI((s) => s.clearCopilotSeed);

  // Pull contract suggested prompts via the ERP API (not the agent service)
  // so it works even before any contracts have been registered server-side.
  const { data: contracts } = useQuery({
    queryKey: ['agent-contracts'],
    queryFn:  () => api<ContractsResp>('/agent/contracts'),
    staleTime: 5 * 60_000,
  });

  // Past sessions: shown in a small dropdown next to the header so the user
  // can resume a conversation from this session or a previous one. Lazily
  // refreshed each time the panel opens.
  const { data: sessions } = useQuery({
    queryKey: ['agent-sessions', open],
    queryFn:  () => agentSessions<SessionList>('/api/agent/v1/sessions?kind=copilot'),
    enabled:  open,
    staleTime: 30_000,
  });
  const [showSessions, setShowSessions] = useState(false);

  async function loadSession(sid: string) {
    setShowSessions(false);
    if (sid === sessionId) return;
    try {
      const hist = await agentSessions(`/api/agent/v1/sessions/${sid}/messages`)
        .then((r) => (r as SessionMessages).items)
        .catch(() => null);
      if (!hist) return;
      setSessionId(sid);
      setTurns(hist.map((m): ChatTurn => {
        if (m.role === 'user')      return { kind: 'user',      content: m.content, at: Date.parse(m.created_at) };
        if (m.role === 'assistant') return { kind: 'assistant', content: m.content, at: Date.parse(m.created_at) };
        if (m.role === 'tool')      return { kind: 'tool',      name: m.tool_name ?? 'tool', ok: !m.content.includes('"error"'), at: Date.parse(m.created_at) };
        return { kind: 'assistant', content: m.content, at: Date.parse(m.created_at) };
      }));
    } catch {
      // surface in chat error rather than crashing
    }
  }

  const [pending, setPending] = useState(false);
  async function sendStreaming(msg: string) {
    if (pending) return;
    setPending(true);
    setDraft('');
    // Echo the user turn immediately + create the streaming assistant placeholder.
    setTurns((t) => [
      ...t,
      { kind: 'user',      content: msg, at: Date.now() },
      { kind: 'assistant', content: '',  at: Date.now() + 1, streaming: true },
    ]);
    try {
      await streamChat(
        { session_id: sessionId ?? undefined, message: msg },
        (ev) => {
          switch (ev.kind) {
            case 'session':
              setSessionId(ev.data.session_id);
              break;
            case 'tool_call':
              setTurns((t) => [...t, { kind: 'tool', name: ev.data.name, at: Date.now() }]);
              // The assistant bubble that was streaming pauses; we'll resume
              // when the next delta arrives, or finalize on done.
              setTurns((t) => [...t.map((x) =>
                x.kind === 'assistant' && x.streaming ? { ...x, streaming: false } : x,
              )]);
              break;
            case 'tool_result':
              setTurns((t) => {
                // Update the most recent matching tool turn.
                const i = [...t].reverse().findIndex((x) => x.kind === 'tool' && x.name === ev.data.name && x.ok === undefined);
                if (i === -1) return t;
                const idx = t.length - 1 - i;
                const next = t.slice();
                next[idx] = { ...t[idx]!, ok: ev.data.ok } as ChatTurn;
                return next;
              });
              break;
            case 'proposal':
              setTurns((t) => [...t, {
                kind: 'proposal',
                doctype: ev.data.doctype,
                document_id: ev.data.document_id,
                document_name: ev.data.document_name,
                at: Date.now(),
              }]);
              break;
            case 'delta':
              setTurns((t) => {
                // Append to the last streaming-assistant turn, or start a new one.
                const last = t[t.length - 1];
                if (last && last.kind === 'assistant' && last.streaming) {
                  const next = t.slice();
                  next[t.length - 1] = { ...last, content: last.content + ev.data.content };
                  return next;
                }
                return [...t, { kind: 'assistant', content: ev.data.content, at: Date.now(), streaming: true }];
              });
              break;
            case 'done':
              setTurns((t) => t.map((x) =>
                x.kind === 'assistant' && x.streaming ? { ...x, streaming: false } : x,
              ));
              break;
            case 'error':
              setTurns((t) => [...t, {
                kind: 'assistant', content: `(error) ${ev.data.message}`, at: Date.now(),
              }]);
              break;
          }
        },
      );
    } catch (e) {
      setTurns((t) => [...t, { kind: 'assistant', content: `(error) ${(e as Error).message}`, at: Date.now() }]);
    } finally {
      setPending(false);
      // Streamed actions may have created docs; nudge the list caches.
      void qc.invalidateQueries({ queryKey: ['doctype'] });
      void qc.invalidateQueries({ queryKey: ['agent-approvals-pending'] });
      void qc.invalidateQueries({ queryKey: ['agent-nudges-active'] });
    }
  }

  // Esc minimizes (not closes) so accidental keypresses don't bury the
  // chat — the chip stays visible as a reminder it's still alive. Focus
  // the input when the panel becomes visible.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') minimizeCopilot(); };
    window.addEventListener('keydown', onKey);
    setTimeout(() => inputRef.current?.focus(), 50);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, minimizeCopilot]);

  // When the panel opens with a seed prompt waiting (set by openCopilotWith
  // from somewhere else — typically a nudge CTA), auto-send it.
  useEffect(() => {
    if (!open || !copilotSeedPrompt) return;
    void sendStreaming(copilotSeedPrompt);
    clearCopilotSeed();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, copilotSeedPrompt]);
  useEffect(() => {
    if (!scrollRef.current) return;
    scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [turns, pending]);

  const quickChips = useMemo(() => {
    const all: string[] = [];
    for (const c of contracts?.items ?? []) {
      for (const p of (c.suggested_prompts ?? []).slice(0, 1)) {
        all.push(p);
      }
    }
    return all.slice(0, 6);
  }, [contracts]);

  function onSend() {
    const msg = draft.trim();
    if (!msg || pending) return;
    void sendStreaming(msg);
  }

  function newConversation() {
    setSessionId(null);
    setTurns([]);
    setTimeout(() => inputRef.current?.focus(), 50);
  }

  // Hidden: render nothing but keep the component mounted so chat state
  // (turns, sessionId, draft) survives close → reopen cycles.
  if (!open && !minimized) return null;

  // Minimized: small chip in bottom-right. Click to restore. Preserves
  // state. Surfaces the turn count so the user knows something is
  // waiting for them.
  if (!open && minimized) {
    const lastUserMsg = [...turns].reverse().find((t) => t.kind === 'user');
    const preview = lastUserMsg?.kind === 'user' ? lastUserMsg.content : '';
    return createPortal(
      <button
        type="button"
        onClick={openCopilot}
        aria-label="Restore Logica AI Copilot"
        className="fixed bottom-4 right-4 z-40 inline-flex items-center gap-2 rounded-full bg-accent text-canvas pl-3 pr-4 py-2 shadow-lg hover:bg-accent/90 transition-colors max-w-[280px]"
      >
        <Sparkles className="size-4 shrink-0" />
        <span className="text-body-sm font-medium shrink-0">Copilot</span>
        {turns.length > 0 && (
          <>
            <span className="text-canvas/70 shrink-0">·</span>
            <span className="text-caption text-canvas/85 truncate">
              {preview || `${turns.length} turn${turns.length === 1 ? '' : 's'}`}
            </span>
          </>
        )}
      </button>,
      document.body,
    );
  }

  return createPortal(
    <>
      {/* No scrim — the panel is non-modal so the user can keep clicking
          the page behind it. */}
      <aside
        role="dialog"
        aria-label="Logica AI Copilot"
        className="fixed bottom-4 right-4 z-40 w-[400px] max-w-[calc(100vw-2rem)] h-[620px] max-h-[calc(100vh-2rem)] bg-canvas border border-hairline rounded-xl shadow-2xl flex flex-col overflow-hidden"
      >
        <header className="px-4 py-3 border-b border-hairline flex items-center gap-2 relative">
          <span className="inline-flex items-center justify-center size-7 rounded-full bg-accent/15 text-accent">
            <Sparkles className="size-4" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="text-body-sm font-semibold text-ink">Logica AI Copilot</div>
            <button
              type="button"
              onClick={() => sessions && sessions.items.length > 0 && setShowSessions((v) => !v)}
              className="text-caption text-stone hover:text-ink truncate inline-flex items-center gap-0.5"
              title={sessions && sessions.items.length > 0 ? 'Past sessions' : ''}
            >
              {sessionId ? `Session ${sessionId.slice(0, 14)}…` : 'New conversation'}
              {sessions && sessions.items.length > 0 && (
                <span className={cn('inline-block transition-transform', showSessions && 'rotate-180')}>▾</span>
              )}
            </button>
          </div>
          <button
            type="button"
            onClick={() => { newConversation(); setShowSessions(false); }}
            title="Start a new conversation (resets the current one)"
            className="text-stone hover:text-ink p-1"
            aria-label="New conversation"
          >
            <RotateCw className="size-4" />
          </button>
          <button
            type="button"
            onClick={minimizeCopilot}
            title="Minimize to chip — conversation kept"
            className="text-stone hover:text-ink p-1"
            aria-label="Minimize"
          >
            <Minus className="size-4" />
          </button>
          <button
            type="button"
            onClick={closeCopilot}
            title="Hide — conversation kept; reopen via the sparkle icon in the top bar"
            className="text-stone hover:text-ink p-1"
            aria-label="Hide"
          >
            <X className="size-4" />
          </button>

          {showSessions && sessions && (
            <div className="absolute left-3 right-3 top-full mt-1 z-10 max-h-[300px] overflow-y-auto rounded-md border border-hairline bg-canvas shadow-lg">
              <div className="px-3 py-2 border-b border-hairline text-caption text-stone">
                Recent sessions
              </div>
              <ul>
                {sessions.items.map((s) => (
                  <li key={s.id}>
                    <button
                      type="button"
                      onClick={() => void loadSession(s.id)}
                      className={cn(
                        'w-full text-left px-3 py-2 text-body-sm hover:bg-surface-soft transition-colors flex items-center gap-2',
                        s.id === sessionId && 'bg-accent/5',
                      )}
                    >
                      <span className="truncate flex-1">{s.title || s.id}</span>
                      <span className="text-caption text-stone shrink-0">{relativeTime(s.updated_at)}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </header>

        <div ref={scrollRef} className="flex-1 overflow-y-auto px-4 py-3 space-y-3">
          {turns.length === 0 && (
            <div className="space-y-3">
              <p className="text-body-sm text-stone">
                Tanya apa saja tentang data ERP Anda. Saya akan membaca, menjelaskan,
                atau membuat draft dokumen — tapi tidak pernah submit tanpa Anda.
              </p>
              {quickChips.length > 0 && (
                <div className="flex flex-wrap gap-1.5 pt-1">
                  {quickChips.map((q) => (
                    <button
                      key={q}
                      type="button"
                      onClick={() => void sendStreaming(q)}
                      className="text-caption text-charcoal bg-surface hover:bg-surface-soft border border-hairline rounded-full px-2.5 py-1 transition-colors text-left"
                    >
                      {q}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
          {turns.map((t, i) => (
            <ChatBubble key={i} turn={t} />
          ))}
          {pending && (
            <div className="flex gap-2 items-center text-caption text-stone pl-1">
              <span className="size-2 rounded-full bg-accent animate-pulse" />
              Thinking…
            </div>
          )}
        </div>

        <div className="border-t border-hairline p-3">
          <div className="rounded-lg border border-hairline focus-within:border-accent/60 bg-canvas">
            <textarea
              ref={inputRef}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); onSend(); }
              }}
              placeholder="Tanya tentang data Anda…"
              rows={2}
              className="w-full bg-transparent px-3 py-2 text-body-sm text-ink resize-none focus:outline-none"
            />
            <div className="flex items-center justify-between px-2 pb-2">
              <span className="text-caption text-stone">
                Enter to send · Shift+Enter for newline
              </span>
              <button
                type="button"
                onClick={onSend}
                disabled={!draft.trim() || pending}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent text-canvas px-2.5 py-1 text-caption font-medium hover:bg-accent/90 disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <SendHorizonal className="size-3.5" />
                Send
              </button>
            </div>
          </div>
        </div>
      </aside>
    </>,
    document.body,
  );
}

// Doctype → URL slug mirror — same map the audit middleware / Approval
// card uses. Keep in sync.
const DOCTYPE_TO_URL: Record<string, { module: string; slug: string }> = {
  customer:         { module: '/accounting', slug: 'customers' },
  supplier:         { module: '/accounting', slug: 'suppliers' },
  item:             { module: '/accounting', slug: 'items' },
  sales_invoice:    { module: '/accounting', slug: 'sales-invoices' },
  purchase_invoice: { module: '/accounting', slug: 'purchase-invoices' },
  journal_entry:    { module: '/accounting', slug: 'journal-entries' },
  payment_entry:    { module: '/accounting', slug: 'payment-entries' },
  employee:         { module: '/hr',         slug: 'employees' },
  lead:             { module: '/crm',        slug: 'leads' },
  project:          { module: '/projects',   slug: 'projects' },
  issue:            { module: '/support',    slug: 'issues' },
  bom:              { module: '/manufacturing', slug: 'boms' },
  work_order:       { module: '/manufacturing', slug: 'work-orders' },
  asset:            { module: '/assets',     slug: 'assets' },
};

// DocLinkedText renders chat content with inline `<doctype>-YYYY-NNNN` (or
// similar) document references rewritten as clickable links to the detail
// page. The pattern is intentionally narrow — it has to match how the ERP
// names documents (SI-2026-0042, PI-2026-0001, JE-..., PE-..., etc.) without
// false-positives on dates or financial figures.
const DOC_REF_RE = /\b([A-Z]{2,5})-(\d{4})-(\d{4,6})\b/g;
const DOC_PREFIX_TO_PATH: Record<string, { module: string; slug: string }> = {
  SI:     { module: '/accounting', slug: 'sales-invoices' },
  PI:     { module: '/accounting', slug: 'purchase-invoices' },
  JE:     { module: '/accounting', slug: 'journal-entries' },
  PE:     { module: '/accounting', slug: 'payment-entries' },
  CUST:   { module: '/accounting', slug: 'customers' },
  SUPP:   { module: '/accounting', slug: 'suppliers' },
  ITEM:   { module: '/accounting', slug: 'items' },
  EMP:    { module: '/hr',         slug: 'employees' },
  LEAD:   { module: '/crm',        slug: 'leads' },
  PROJ:   { module: '/projects',   slug: 'projects' },
  ISS:    { module: '/support',    slug: 'issues' },
  BOM:    { module: '/manufacturing', slug: 'boms' },
  WO:     { module: '/manufacturing', slug: 'work-orders' },
  ASSET:  { module: '/assets',     slug: 'assets' },
  POS:    { module: '/pos',        slug: 'invoices' },
};
// Map from a document name → list endpoint so the link points at the list
// pre-filtered by `?name=...` when we don't have the id. (Detail pages need
// the id; without it we navigate to the list, which is more useful than not
// linking at all.)
function DocLinkedText({ text, onNavigate }: { text: string; onNavigate: (to: string) => void }) {
  const parts: Array<string | { name: string; to: string }> = [];
  let last = 0;
  let m: RegExpExecArray | null;
  DOC_REF_RE.lastIndex = 0;
  while ((m = DOC_REF_RE.exec(text)) !== null) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    const prefix = m[1]!;
    const map = DOC_PREFIX_TO_PATH[prefix];
    if (map) {
      parts.push({ name: m[0], to: `${map.module}/${map.slug}?name=${encodeURIComponent(m[0])}` });
    } else {
      parts.push(m[0]);
    }
    last = m.index + m[0].length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return (
    <>
      {parts.map((p, i) =>
        typeof p === 'string'
          ? <span key={i}>{p}</span>
          : <button
              key={i}
              type="button"
              onClick={(e) => { e.preventDefault(); onNavigate(p.to); }}
              className="text-accent hover:underline font-mono"
            >
              {p.name}
            </button>,
      )}
    </>
  );
}

function relativeTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const diff = Math.max(0, Date.now() - t);
  const min = Math.round(diff / 60_000);
  if (min < 1)    return 'now';
  if (min < 60)   return `${min}m`;
  const hr = Math.round(min / 60);
  if (hr < 24)    return `${hr}h`;
  const d  = Math.round(hr / 24);
  if (d < 30)     return `${d}d`;
  return iso.slice(0, 10);
}

function ChatBubble({ turn }: { turn: ChatTurn }) {
  const navigate = useNavigate();
  if (turn.kind === 'user') {
    return (
      <div className="flex justify-end">
        <div className="max-w-[85%] rounded-lg px-3 py-2 text-body-sm whitespace-pre-wrap break-words bg-accent text-canvas">
          {turn.content}
        </div>
      </div>
    );
  }
  if (turn.kind === 'assistant') {
    return (
      <div className="flex">
        <div className="max-w-[85%] rounded-lg px-3 py-2 text-body-sm whitespace-pre-wrap break-words bg-surface text-charcoal">
          <DocLinkedText text={turn.content} onNavigate={(to) => void navigate({ to: to as never })} />
          {turn.streaming && <span className="inline-block size-2 rounded-full bg-accent animate-pulse ml-1 align-middle" />}
        </div>
      </div>
    );
  }
  if (turn.kind === 'tool') {
    return (
      <div className="flex items-center gap-2 text-caption text-stone pl-1">
        {turn.ok === undefined
          ? <span className="size-2 rounded-full bg-accent/60 animate-pulse" />
          : turn.ok
            ? <Check className="size-3 text-brand-success" />
            : <X className="size-3 text-brand-error" />}
        <Wrench className="size-3" />
        <span className="font-mono">{turn.name}</span>
        {turn.ok === undefined && <span>running…</span>}
      </div>
    );
  }
  if (turn.kind === 'proposal') {
    const map = DOCTYPE_TO_URL[turn.doctype];
    const url = map ? `${map.module}/${map.slug}/${turn.document_id}` : null;
    return (
      <button
        type="button"
        onClick={() => url && void navigate({ to: url as never })}
        className={cn(
          'flex items-center gap-2 w-full text-left px-3 py-2 rounded-md bg-accent/[0.06] border border-accent/30 hover:bg-accent/10 transition-colors',
          !url && 'cursor-default',
        )}
      >
        <FileText className="size-4 text-accent" />
        <div className="min-w-0 flex-1">
          <div className="text-body-sm text-ink truncate">{turn.document_name}</div>
          <div className="text-caption text-stone">{turn.doctype} · review &amp; submit</div>
        </div>
        {url && <span className="text-caption text-accent">Open →</span>}
      </button>
    );
  }
  return null;
}
