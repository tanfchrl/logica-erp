package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tandigital/logica-erp/internal/agent/approvals"
	"github.com/tandigital/logica-erp/internal/agent/audit"
	"github.com/tandigital/logica-erp/internal/agent/erpclient"
	"github.com/tandigital/logica-erp/internal/agent/llm"
	"github.com/tandigital/logica-erp/internal/agent/policy"
	"github.com/tandigital/logica-erp/internal/agent/session"
	"github.com/tandigital/logica-erp/internal/agent/tools"
	"github.com/tandigital/logica-erp/internal/agentcontract"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
)

// chatStreamHandler is the SSE counterpart to registerChat. Same ReAct loop,
// but emits events progressively so the FE can render incremental text +
// tool-call breadcrumbs instead of staring at "Thinking…" for 30s.
//
// SSE event shapes (each event is `event: <kind>` + `data: <json>` + blank line):
//
//	event: session     {session_id, turn}            first event, before any thinking
//	event: tool_call   {name, arguments}             before dispatching a tool
//	event: tool_result {name, ok: true|false, error?} after the tool ran
//	event: proposal    {approval_id, document_name}  when a Tier-1 draft was queued
//	event: delta       {content}                     incremental text from final turn
//	event: done        {session_id, turn}            terminal
//	event: error       {message}                     terminal on failure
func chatStreamHandler(
	store *session.Store, rec *audit.Recorder, provider LLMProvider,
	registry *agentcontract.Registry, toolReg *tools.Registry, gate *policy.Gate,
	apvStore *approvals.Store,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		p := auth.FromContext(ctx)
		if p == nil {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		co := auth.CompanyFromContext(ctx)
		erpcc := erpclient.CallContext{Token: httpx.BearerFromContext(ctx), CompanyID: co}
		// Per-company BYOM resolution — re-read on every request so a
		// Settings change takes effect on the next turn (modulo TTL).
		ll := provider.ForCompany(ctx, co)

		var in chatInBody
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if in.Message == "" {
			http.Error(w, "message required", http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // nginx hint
		w.WriteHeader(http.StatusOK)

		emit := func(event string, data any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
			flusher.Flush()
		}

		// Resume or create the session.
		var sess *session.Session
		if in.SessionID != "" {
			s, err := store.Get(ctx, p.UserID, in.SessionID)
			if err != nil {
				emit("error", map[string]string{"message": err.Error()})
				return
			}
			sess = s
		} else {
			s, err := store.Create(ctx, p.UserID, co, truncate(in.Message, 60), session.KindCopilot)
			if err != nil {
				emit("error", map[string]string{"message": err.Error()})
				return
			}
			sess = s
		}
		emit("session", map[string]any{"session_id": sess.ID})

		history, err := store.History(ctx, sess.ID)
		if err != nil {
			emit("error", map[string]string{"message": err.Error()})
			return
		}
		turn := len(history) + 1
		_ = store.AppendMessage(ctx, session.Message{
			SessionID: sess.ID, Turn: turn, Role: "user", Content: in.Message,
		})
		rec.Record(ctx, audit.Event{
			SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
			Turn: turn, Type: audit.EventPrompt,
			Payload: map[string]any{"content": in.Message},
			Model:   ll.Model(),
		})

		if !ll.Configured() {
			reply := fmt.Sprintf("Agent service is up. No LLM configured — set AGENT_LLM_BASE_URL/_API_KEY/_MODEL. Loaded %s.",
				registry.Summary())
			emit("delta", map[string]string{"content": reply})
			turn++
			_ = store.AppendMessage(ctx, session.Message{
				SessionID: sess.ID, Turn: turn, Role: "assistant", Content: reply,
			})
			emit("done", map[string]any{"session_id": sess.ID, "turn": turn})
			return
		}

		msgs := buildSystemAndHistory(registry, history)
		msgs = append(msgs, llm.Message{Role: "user", Content: in.Message})

		for iter := 0; iter < maxToolIterations; iter++ {
			// Streaming call; collect deltas + accumulated tool_calls.
			ch, err := ll.ChatStream(ctx, llm.Request{
				Messages: msgs, Tools: toolReg.LLMTools(), Temperature: 0.2,
			})
			if err != nil {
				emit("error", map[string]string{"message": err.Error()})
				return
			}
			var (
				assistantText string
				toolCalls     []llm.ToolCall
				streamErr     error
			)
			for ev := range ch {
				switch ev.Kind {
				case "delta":
					// Relay token-by-token. If a tool_calls event arrives
					// after some text, the FE sees both — that mirrors the
					// model's actual output (e.g. "Let me look that up…"
					// before a tool call).
					assistantText += ev.Content
					emit("delta", map[string]string{"content": ev.Content})
				case "tool_calls":
					toolCalls = ev.ToolCalls
				case "error":
					streamErr = ev.Err
				}
			}
			if streamErr != nil {
				emit("error", map[string]string{"message": streamErr.Error()})
				return
			}

			if len(toolCalls) > 0 {
				// Persist the assistant tool-call turn so history replay is faithful.
				turn++
				tcBlob, _ := json.Marshal(toolCalls)
				_ = store.AppendMessage(ctx, session.Message{
					SessionID: sess.ID, Turn: turn, Role: "assistant",
					Content: assistantText, ToolCalls: tcBlob,
				})
				// Mirror into msgs for next iter.
				msgs = append(msgs, llm.Message{Role: "assistant", Content: assistantText, ToolCalls: toolCalls})

				for _, tc := range toolCalls {
					emit("tool_call", map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					})
					rec.Record(ctx, audit.Event{
						SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
						Turn: turn, Type: audit.EventToolCall,
						Payload: map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
						Model:   ll.Model(),
					})
					result := runOneToolCall(ctx, toolReg, gate, erpcc, tc, rec,
						audit.Event{SessionID: sess.ID, UserID: p.UserID, CompanyID: co, Turn: turn})

					// Surface the tool result + queue any draft into approvals.
					okFlag := true
					if m, ok := result.(map[string]any); ok {
						if _, bad := m["error"]; bad {
							okFlag = false
						}
					}
					emit("tool_result", map[string]any{
						"name": tc.Function.Name,
						"ok":   okFlag,
					})
					if isDraftCreator(tc.Function.Name) {
						maybeEnqueueDraft(ctx, apvStore, rec, p.UserID, co, sess.ID, turn, in.Message, result)
						if m, ok := result.(map[string]any); ok {
							if name, _ := m["name"].(string); name != "" {
								emit("proposal", map[string]any{
									"doctype":       m["doctype"],
									"document_id":   m["id"],
									"document_name": name,
								})
							}
						}
					}

					resultBytes, _ := json.Marshal(result)
					turn++
					_ = store.AppendMessage(ctx, session.Message{
						SessionID: sess.ID, Turn: turn, Role: "tool",
						Content: string(resultBytes), ToolCallID: tc.ID, ToolName: tc.Function.Name,
					})
					msgs = append(msgs, llm.Message{
						Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
						Content: string(resultBytes),
					})
				}
				continue
			}

			// No tool calls → this was the final assistant turn. The deltas
			// were already emitted above; just persist + finalize.
			turn++
			_ = store.AppendMessage(ctx, session.Message{
				SessionID: sess.ID, Turn: turn, Role: "assistant", Content: assistantText,
			})
			rec.Record(ctx, audit.Event{
				SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
				Turn: turn, Type: audit.EventToolResult,
				Payload: map[string]any{"content": assistantText},
				Model:   ll.Model(),
				LatencyMS: int(time.Since(time.Now()).Abs() / time.Millisecond), // 0; precise latency measured by full session timer (#33 will surface)
			})
			emit("done", map[string]any{"session_id": sess.ID, "turn": turn})
			return
		}

		emit("error", map[string]string{"message": "max tool iterations exceeded"})
	}
}
