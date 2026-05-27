import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Sparkles, X, SendHorizonal, RotateCw } from 'lucide-react';
import { api, getAccessToken, getActiveCompany } from '@/lib/api';
import { cn } from '@/lib/cn';

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

interface ChatResponse {
  session_id: string;
  reply: string;
  turn: number;
}
interface ContractsResp {
  items: Array<{
    module: string;
    display_name: string;
    suggested_prompts?: string[];
  }>;
}
interface ChatTurn {
  role: 'user' | 'assistant';
  content: string;
  at: number; // ms epoch
}

interface CopilotPanelProps {
  open: boolean;
  onClose: () => void;
}

// Agent traffic goes to /api/agent/v1 which the Vite dev server proxies to
// the standalone agent process on :8090 (see web/vite.config.ts). In prod,
// the reverse proxy maps the same prefix to the agent container — so the
// browser only ever sees one origin.
function agentFetch<T>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const co = getActiveCompany();
  if (co) headers['X-Company-Id'] = co;
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  return fetch(path, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  }).then(async (r) => {
    const text = await r.text();
    const json = text ? JSON.parse(text) : ({} as unknown);
    if (!r.ok) throw new Error((json as { detail?: string; message?: string }).detail ?? r.statusText);
    return json as T;
  });
}

export function CopilotPanel({ open, onClose }: CopilotPanelProps) {
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();

  // Pull contract suggested prompts via the ERP API (not the agent service)
  // so it works even before any contracts have been registered server-side.
  const { data: contracts } = useQuery({
    queryKey: ['agent-contracts'],
    queryFn:  () => api<ContractsResp>('/agent/contracts'),
    staleTime: 5 * 60_000,
  });

  const send = useMutation({
    mutationFn: (msg: string) =>
      agentFetch<ChatResponse>('/api/agent/v1/chat', {
        method: 'POST',
        body: { session_id: sessionId ?? undefined, message: msg },
      }),
    onSuccess: (resp, msg) => {
      setSessionId(resp.session_id);
      setTurns((t) => [
        ...t,
        { role: 'user',      content: msg,        at: Date.now() },
        { role: 'assistant', content: resp.reply, at: Date.now() + 1 },
      ]);
      setDraft('');
      // The reply may have created/modified documents; nudge the list caches.
      void qc.invalidateQueries({ queryKey: ['doctype'] });
    },
    onError: (e: Error, msg) => {
      setTurns((t) => [
        ...t,
        { role: 'user',      content: msg, at: Date.now() },
        { role: 'assistant', content: `(error) ${e.message}`, at: Date.now() + 1 },
      ]);
    },
  });

  // Close on Esc, focus the input on open, auto-scroll on new turns.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    setTimeout(() => inputRef.current?.focus(), 50);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);
  useEffect(() => {
    if (!scrollRef.current) return;
    scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [turns, send.isPending]);

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
    if (!msg || send.isPending) return;
    send.mutate(msg);
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
                      onClick={() => send.mutate(q)}
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
          {send.isPending && (
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
                disabled={!draft.trim() || send.isPending}
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

function ChatBubble({ turn }: { turn: ChatTurn }) {
  const isUser = turn.role === 'user';
  return (
    <div className={cn('flex', isUser && 'justify-end')}>
      <div
        className={cn(
          'max-w-[85%] rounded-lg px-3 py-2 text-body-sm whitespace-pre-wrap break-words',
          isUser ? 'bg-accent text-canvas' : 'bg-surface text-charcoal',
        )}
      >
        {turn.content}
      </div>
    </div>
  );
}
