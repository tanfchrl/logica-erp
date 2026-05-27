import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Sparkles, X, SendHorizonal, RotateCw, Wrench, Check, FileText } from 'lucide-react';
import { api, getAccessToken, getActiveCompany } from '@/lib/api';
import { cn } from '@/lib/cn';
import { useUI } from '@/store/ui';

/**
 * Floating Copilot panel — slides in from the right edge over the page.
 *
 * Lives outside the per-page right rail so detail pages (which already use
 * 320px for Timeline / Approval / Meta) don't fight it for space.
 *
 * Triggered by the violet sparkle button in TopChrome. Closes on outside-
 * click, Esc, or the X button.
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

interface CopilotPanelProps {
  open: boolean;
  onClose: () => void;
}

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

export function CopilotPanel({ open, onClose }: CopilotPanelProps) {
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();
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

  // Close on Esc, focus the input on open, auto-scroll on new turns.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    setTimeout(() => inputRef.current?.focus(), 50);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

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

  if (!open) return null;
  return createPortal(
    <>
      {/* Scrim */}
      <button
        type="button"
        aria-label="Close Copilot"
        onClick={onClose}
        className="fixed inset-0 z-40 bg-ink/20 backdrop-blur-[2px] animate-in fade-in"
      />
      {/* Panel */}
      <aside
        role="dialog"
        aria-label="Logica AI Copilot"
        className="fixed top-0 right-0 z-50 h-full w-[420px] max-w-[90vw] bg-canvas border-l border-hairline shadow-xl flex flex-col"
      >
        <header className="px-4 py-3 border-b border-hairline flex items-center gap-2">
          <span className="inline-flex items-center justify-center size-7 rounded-full bg-accent/15 text-accent">
            <Sparkles className="size-4" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="text-body-sm font-semibold text-ink">Logica AI Copilot</div>
            <div className="text-caption text-stone truncate">
              {sessionId ? `Session ${sessionId.slice(0, 14)}…` : 'New conversation'}
            </div>
          </div>
          <button
            type="button"
            onClick={newConversation}
            title="Start a new conversation"
            className="text-stone hover:text-ink p-1"
            aria-label="New conversation"
          >
            <RotateCw className="size-4" />
          </button>
          <button
            type="button"
            onClick={onClose}
            className="text-stone hover:text-ink p-1"
            aria-label="Close"
          >
            <X className="size-4" />
          </button>
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
          {turn.content}
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
