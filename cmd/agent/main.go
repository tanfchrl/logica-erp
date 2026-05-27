// Command agent is the Logica ERP agentic-layer service.
//
// It is an API client of the ERP core: it never reads or writes the ERP
// database directly, only its own tables (agent_session,
// agent_conversation, agent_audit_log, agent_approval_queue, agent_nudge).
//
// All requests authenticate the user via the same JWT secret as the ERP
// core, then proxy the user's token forward when making ERP API calls.
// The agent never holds a privileged service account.
//
// See docs/agent-build-prompt.md for the full architecture spec.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/tandigital/logica-erp/internal/agent/approvals"
	"github.com/tandigital/logica-erp/internal/agent/audit"
	"github.com/tandigital/logica-erp/internal/agent/migration"
	"github.com/tandigital/logica-erp/internal/agent/erpclient"
	"github.com/tandigital/logica-erp/internal/agent/llm"
	"github.com/tandigital/logica-erp/internal/agent/policy"
	"github.com/tandigital/logica-erp/internal/agent/session"
	"github.com/tandigital/logica-erp/internal/agent/tools"
	"github.com/tandigital/logica-erp/internal/agentcontract"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	dbURL := mustEnv("DATABASE_URL")
	jwtSecret := mustEnv("JWT_SECRET")
	erpBase := envDefault("ERP_API_BASE", "http://localhost:8080/api/v1")
	port := envDefault("AGENT_PORT", "8090")
	llmBase := os.Getenv("AGENT_LLM_BASE_URL")
	llmKey := os.Getenv("AGENT_LLM_API_KEY")
	llmModel := envDefault("AGENT_LLM_MODEL", "gpt-4o-mini")
	contractsDir := envDefault("AGENT_CONTRACTS_DIR", "/src")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := dbx.Open(ctx, dbURL)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	signer := auth.NewSigner(jwtSecret, time.Hour)

	registry, err := agentcontract.LoadFS(os.DirFS(contractsDir), ".")
	if err != nil {
		logger.Error("load contracts", "err", err)
		os.Exit(1)
	}
	logger.Info("agent: contracts loaded", "summary", registry.Summary())

	gate := policy.NewGate(policy.DefaultConfig(), registry)
	rec := audit.New(db)
	sessStore := session.New(db)
	llmClient := llm.New(llm.Config{BaseURL: llmBase, APIKey: llmKey, Model: llmModel})
	erp := erpclient.New(erpBase)
	toolReg := tools.New(erp, registry)
	apvStore := approvals.New(db)
	migrationSvc := migration.New(sessStore, erp)

	r := chi.NewRouter()
	r.Use(httpx.RequestID)
	r.Use(httpx.AccessLog(logger))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Company-Id"},
		AllowCredentials: false,
		MaxAge:           600,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/api/agent/v1", func(api chi.Router) {
		// Use the same JWT auth as the ERP core. publicPrefixes is empty —
		// the agent has no anonymous surface area.
		api.Use(httpx.Auth(db, signer, nil))

		humaCfg := huma.DefaultConfig("Logica ERP Agent", "0.1.0")
		humaCfg.OpenAPIPath = "/openapi"
		humaCfg.DocsPath = "/docs"
		humaCfg.Servers = []*huma.Server{{URL: "/api/agent/v1"}}

		hapi := humachi.New(api, humaCfg)

		registerChat(hapi, sessStore, rec, llmClient, registry, toolReg, gate, apvStore)
		registerSessions(hapi, sessStore)
		registerApprovals(hapi, apvStore, rec)
		registerMigration(hapi, migrationSvc)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.Info("agent: listening", "addr", srv.Addr, "llm_configured", llmClient.Configured(), "model", llmModel)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("agent: serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	logger.Info("agent: stopped")
}

// ---- handlers ----

func registerSessions(api huma.API, store *session.Store) {
	huma.Register(api, huma.Operation{
		OperationID: "list-agent-sessions",
		Method:      http.MethodGet,
		Path:        "/sessions",
		Summary:     "List the caller's open agent sessions",
		Tags:        []string{"Agent / Sessions"},
	}, func(ctx context.Context, in *listSessionsIn) (*listSessionsOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		ss, err := store.ListOpenForUser(ctx, p.UserID, session.Kind(in.Kind))
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &listSessionsOut{Body: listSessionsBody{Items: ss}}, nil
	})
}

// maxToolIterations caps the number of LLM<->tool round-trips per turn so a
// malformed model can't spin forever. 6 is plenty for queries like
// "find the customer and show me their AR aging" which usually take 2-3 hops.
const maxToolIterations = 6

func registerChat(api huma.API, store *session.Store, rec *audit.Recorder, ll *llm.Client,
	registry *agentcontract.Registry, toolReg *tools.Registry, gate *policy.Gate, apvStore *approvals.Store) {
	huma.Register(api, huma.Operation{
		OperationID: "agent-chat",
		Method:      http.MethodPost,
		Path:        "/chat",
		Summary:     "Send a turn to the agent. Runs a ReAct loop over the tool registry.",
		Tags:        []string{"Agent / Chat"},
	}, func(ctx context.Context, in *chatIn) (*chatOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		co := auth.CompanyFromContext(ctx)

		// Forward the user's JWT to the ERP API on tool calls. The agent
		// never holds its own credential.
		erpcc := erpclient.CallContext{Token: httpx.BearerFromContext(ctx), CompanyID: co}

		// Resume or create the session.
		var sess *session.Session
		if in.Body.SessionID != "" {
			s, err := store.Get(ctx, p.UserID, in.Body.SessionID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			sess = s
		} else {
			title := truncate(in.Body.Message, 60)
			s, err := store.Create(ctx, p.UserID, co, title, session.KindCopilot)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			sess = s
		}

		history, err := store.History(ctx, sess.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		turn := len(history) + 1

		// Persist + audit the user prompt.
		if err := store.AppendMessage(ctx, session.Message{
			SessionID: sess.ID, Turn: turn, Role: "user", Content: in.Body.Message,
		}); err != nil {
			return nil, httpx.MapError(err)
		}
		rec.Record(ctx, audit.Event{
			SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
			Turn: turn, Type: audit.EventPrompt,
			Payload: map[string]any{"content": in.Body.Message},
			Model:   ll.Model(),
		})

		// No LLM? Return the canned reply so the FE can still render.
		if !ll.Configured() {
			reply := fmt.Sprintf(
				"Agent service is up. No LLM is configured — set AGENT_LLM_BASE_URL/AGENT_LLM_API_KEY/AGENT_LLM_MODEL to enable conversation. Loaded %s.",
				registry.Summary(),
			)
			turn++
			_ = store.AppendMessage(ctx, session.Message{
				SessionID: sess.ID, Turn: turn, Role: "assistant", Content: reply,
			})
			return &chatOut{Body: chatBody{SessionID: sess.ID, Reply: reply, Turn: turn}}, nil
		}

		// ---- ReAct loop ----
		msgs := buildSystemAndHistory(registry, history)
		msgs = append(msgs, llm.Message{Role: "user", Content: in.Body.Message})

		var assistantContent string
		var totalIn, totalOut int
		started := time.Now()
		for iter := 0; iter < maxToolIterations; iter++ {
			resp, err := ll.Chat(ctx, llm.Request{
				Messages:    msgs,
				Tools:       toolReg.LLMTools(),
				Temperature: 0.2,
			})
			if err != nil {
				rec.Record(ctx, audit.Event{
					SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
					Turn: turn + 1, Type: audit.EventError,
					Payload: map[string]any{"err": err.Error()},
					LatencyMS: int(time.Since(started) / time.Millisecond),
				})
				assistantContent = "I couldn't reach the language model: " + err.Error()
				break
			}
			totalIn += resp.Usage.PromptTokens
			totalOut += resp.Usage.CompletionTokens
			if len(resp.Choices) == 0 {
				break
			}
			choice := resp.Choices[0]

			// Tool call? Run each one, persist, feed results back.
			if len(choice.Message.ToolCalls) > 0 {
				turn++
				tcBlob, _ := json.Marshal(choice.Message.ToolCalls)
				_ = store.AppendMessage(ctx, session.Message{
					SessionID: sess.ID, Turn: turn, Role: "assistant",
					Content: choice.Message.Content, ToolCalls: tcBlob,
				})
				msgs = append(msgs, choice.Message)

				for _, tc := range choice.Message.ToolCalls {
					rec.Record(ctx, audit.Event{
						SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
						Turn: turn, Type: audit.EventToolCall,
						Payload: map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
						Model:   ll.Model(),
					})
					result := runOneToolCall(ctx, toolReg, gate, erpcc, tc, rec,
						audit.Event{SessionID: sess.ID, UserID: p.UserID, CompanyID: co, Turn: turn})
					// Tier-1: agent-authored drafts get a row in the approval
					// queue + a `proposal` audit event so the human can find
					// them later from the dashboard or the recent-drafts surface.
					if tc.Function.Name == "create_draft" {
						maybeEnqueueDraft(ctx, apvStore, rec, p.UserID, co, sess.ID, turn, in.Body.Message, result)
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
				continue // next iteration: re-ask the model with tool results in scope
			}

			// No tool call — final assistant turn.
			assistantContent = choice.Message.Content
			break
		}

		turn++
		_ = store.AppendMessage(ctx, session.Message{
			SessionID: sess.ID, Turn: turn, Role: "assistant", Content: assistantContent,
		})
		rec.Record(ctx, audit.Event{
			SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
			Turn: turn, Type: audit.EventToolResult, // re-using as "assistant completion"
			Payload: map[string]any{"content": assistantContent},
			Model:   ll.Model(), TokensIn: totalIn, TokensOut: totalOut,
			LatencyMS: int(time.Since(started) / time.Millisecond),
		})

		return &chatOut{Body: chatBody{
			SessionID: sess.ID,
			Reply:     assistantContent,
			Turn:      turn,
		}}, nil
	})
}

// maybeEnqueueDraft writes a row into agent_approval_queue when a
// create_draft tool call succeeded — gives the human a single place to find
// what the copilot has been building. Errors are logged via the audit
// recorder but never propagated; a missed queue row shouldn't fail the chat
// loop.
func maybeEnqueueDraft(ctx context.Context, apv *approvals.Store, rec *audit.Recorder,
	userID, companyID, sessionID string, turn int, prompt string, result any) {
	m, ok := result.(map[string]any)
	if !ok {
		return
	}
	if _, errSet := m["error"]; errSet {
		return
	}
	docID, _ := m["id"].(string)
	docName, _ := m["name"].(string)
	doctype, _ := m["doctype"].(string)
	if docID == "" || doctype == "" {
		return
	}
	entry, err := apv.Enqueue(ctx, approvals.Entry{
		SessionID: sessionID, UserID: userID, CompanyID: companyID,
		Doctype: doctype, DocumentID: docID, DocumentName: docName,
		Prompt: prompt,
	})
	if err != nil {
		rec.Record(ctx, audit.Event{
			SessionID: sessionID, UserID: userID, CompanyID: companyID,
			Turn: turn, Type: audit.EventError,
			Payload: map[string]any{"context": "approvals.enqueue", "err": err.Error()},
		})
		return
	}
	rec.Record(ctx, audit.Event{
		SessionID: sessionID, UserID: userID, CompanyID: companyID,
		Turn: turn, Type: audit.EventProposal,
		Payload: map[string]any{
			"approval_id":   entry.ID,
			"doctype":       doctype,
			"document_id":   docID,
			"document_name": docName,
		},
	})
}

// runOneToolCall executes a single LLM-emitted tool call through the policy
// gate and the tool registry, recording audit events as it goes. Returns a
// JSON-serialisable result that gets fed back into the next LLM iteration.
func runOneToolCall(ctx context.Context, reg *tools.Registry, gate *policy.Gate,
	erpcc erpclient.CallContext, tc llm.ToolCall, rec *audit.Recorder, base audit.Event) any {
	t, ok := reg.Lookup(tc.Function.Name)
	if !ok {
		return map[string]any{"error": "unknown tool: " + tc.Function.Name}
	}
	// Doctype, if the args carry one, drives policy. Tools that don't
	// involve a doctype (reports, search) skip the gate.
	var argsObj map[string]any
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &argsObj)
	doctype, _ := argsObj["doctype"].(string)
	if doctype != "" {
		// Resolve tool tier via gate. The gate's per-doctype tier list lives
		// in AGENT_CONTRACT.md.
		dec := gate.Check(doctype, mapToolForGate(t.Name))
		if !dec.Allowed {
			ev := base
			ev.Type = audit.EventPolicyBlocked
			ev.Payload = map[string]any{"tool": t.Name, "doctype": doctype, "reason": dec.Reason}
			rec.Record(ctx, ev)
			return map[string]any{"error": "policy_blocked: " + dec.Reason}
		}
	}
	out, err := t.Run(ctx, erpcc, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		ev := base
		ev.Type = audit.EventError
		ev.Payload = map[string]any{"tool": t.Name, "err": err.Error()}
		rec.Record(ctx, ev)
		return map[string]any{"error": err.Error()}
	}
	ev := base
	ev.Type = audit.EventToolResult
	ev.Payload = map[string]any{"tool": t.Name, "ok": true}
	rec.Record(ctx, ev)
	return out
}

// mapToolForGate maps a registered tool name to the tier-level string used
// in AGENT_CONTRACT.md tier{0,1,2}_tools lists.
func mapToolForGate(toolName string) string {
	switch toolName {
	case "list_documents":
		return "list_with_filters"
	case "get_document":
		return "get_by_id"
	}
	return toolName
}

// registerApprovals wires the human-side approval queue endpoints:
//
//	GET  /approvals/pending           — caller's pending drafts
//	POST /approvals/{id}/resolve      — flip to approved or rejected
func registerApprovals(api huma.API, store *approvals.Store, rec *audit.Recorder) {
	huma.Register(api, huma.Operation{
		OperationID: "list-pending-agent-approvals",
		Method:      http.MethodGet,
		Path:        "/approvals/pending",
		Summary:     "Tier-1 drafts the agent has produced and are waiting for the caller to review",
		Tags:        []string{"Agent / Approvals"},
	}, func(ctx context.Context, _ *struct{}) (*approvalsListOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		es, err := store.ListPending(ctx, p.UserID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &approvalsListOut{Body: approvalsListBody{Items: es}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "resolve-agent-approval",
		Method:      http.MethodPost,
		Path:        "/approvals/{id}/resolve",
		Summary:     "Mark an agent draft as approved (after a human reviews) or rejected",
		Tags:        []string{"Agent / Approvals"},
	}, func(ctx context.Context, in *resolveApprovalIn) (*approvalsActionOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		co := auth.CompanyFromContext(ctx)
		if err := store.Resolve(ctx, p.UserID, in.ID, in.Body.Status); err != nil {
			return nil, httpx.MapError(err)
		}
		var evType audit.EventType = audit.EventHumanApproved
		if in.Body.Status == "rejected" {
			evType = audit.EventHumanRejected
		}
		rec.Record(ctx, audit.Event{
			UserID: p.UserID, CompanyID: co,
			SessionID: "—", // queue resolutions aren't tied to a chat session
			Type: evType,
			Payload: map[string]any{"approval_id": in.ID},
		})
		return &approvalsActionOut{Body: map[string]string{"status": "ok"}}, nil
	})
}

// buildSystemAndHistory composes the system prompt from contract context
// plus the persisted conversation history (excluding the just-appended user
// message — the caller adds that back). Used by the chat handler.
func buildSystemAndHistory(reg *agentcontract.Registry, history []session.Message) []llm.Message {
	sys := []string{
		"You are Logica AI Copilot — a tool-using agent for an Indonesian ERP (PSAK aligned).",
		"You answer queries about the user's own ERP data by calling tools. Never invent data.",
		"When the user mentions a record by partial name, call global_search first.",
		"Show currency amounts in IDR with comma thousand separators (Rp 1.234.567).",
		"Be concise. Reply in the same language the user used (Bahasa Indonesia or English).",
	}
	for _, c := range reg.All() {
		if c.SystemContext == "" {
			continue
		}
		sys = append(sys, fmt.Sprintf("[%s] %s", c.DisplayName, c.SystemContext))
	}
	msgs := []llm.Message{{Role: "system", Content: strings.Join(sys, "\n\n")}}
	for _, m := range history {
		llmMsg := llm.Message{Role: m.Role, Content: m.Content,
			ToolCallID: m.ToolCallID, Name: m.ToolName}
		if len(m.ToolCalls) > 0 {
			_ = json.Unmarshal(m.ToolCalls, &llmMsg.ToolCalls)
		}
		msgs = append(msgs, llmMsg)
	}
	return msgs
}

// registerMigration wires the implementation-wizard endpoints. The five-step
// flow lives in internal/agent/migration; this handler is a thin REST surface
// over it. Steps 3 (data migration) and 4 (opening balances) reuse existing
// ERP endpoints (/admin/imports/* and /accounting/journal-entries) so they
// aren't duplicated here.
func registerMigration(api huma.API, svc *migration.Service) {
	huma.Register(api, huma.Operation{
		OperationID: "start-migration",
		Method:      http.MethodPost,
		Path:        "/migration/start",
		Summary:     "Begin a new implementation wizard session for the caller",
		Tags:        []string{"Agent / Migration"},
	}, func(ctx context.Context, in *startMigrationIn) (*migrationSessionOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		co := auth.CompanyFromContext(ctx)
		sess, err := svc.Start(ctx, p.UserID, co, in.Body.Title)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &migrationSessionOut{Body: migrationSessionBody{SessionID: sess.ID, Title: sess.Title}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-migration-state",
		Method:      http.MethodGet,
		Path:        "/migration/{session_id}/state",
		Summary:     "Read the wizard state — used by the FE to render the step tracker",
		Tags:        []string{"Agent / Migration"},
	}, func(ctx context.Context, in *migrationStateIn) (*migrationStateOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		st, err := svc.LoadState(ctx, p.UserID, in.SessionID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &migrationStateOut{Body: *st}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-migration-profile",
		Method:      http.MethodPost,
		Path:        "/migration/{session_id}/profile",
		Summary:     "Complete Step 1 by saving the SetupProfile",
		Tags:        []string{"Agent / Migration"},
	}, func(ctx context.Context, in *saveProfileIn) (*migrationStateOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		st, err := svc.SaveProfile(ctx, p.UserID, in.SessionID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &migrationStateOut{Body: *st}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "propose-coa",
		Method:      http.MethodPost,
		Path:        "/migration/{session_id}/coa/propose",
		Summary:     "Step 2: generate a PSAK-aligned Chart of Accounts proposal",
		Tags:        []string{"Agent / Migration"},
	}, func(ctx context.Context, in *migrationStateIn) (*coaProposeOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		proposal, err := svc.ProposeCOA(ctx, p.UserID, in.SessionID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &coaProposeOut{Body: coaProposeBody{Proposal: proposal}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "accept-coa",
		Method:      http.MethodPost,
		Path:        "/migration/{session_id}/coa/accept",
		Summary:     "Step 2: confirm the COA proposal. Actual account creation is the caller's responsibility (loop through create_draft).",
		Tags:        []string{"Agent / Migration"},
	}, func(ctx context.Context, in *migrationStateIn) (*migrationStateOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		st, err := svc.AcceptCOA(ctx, p.UserID, in.SessionID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &migrationStateOut{Body: *st}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "migration-readiness",
		Method:      http.MethodGet,
		Path:        "/migration/{session_id}/readiness",
		Summary:     "Step 5: evaluate the go-live readiness checklist against current ERP state",
		Tags:        []string{"Agent / Migration"},
	}, func(ctx context.Context, in *migrationStateIn) (*readinessOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		co := auth.CompanyFromContext(ctx)
		cc := erpclient.CallContext{Token: httpx.BearerFromContext(ctx), CompanyID: co}
		checks, err := svc.Readiness(ctx, p.UserID, in.SessionID, cc)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &readinessOut{Body: readinessBody{Items: checks}}, nil
	})
}

// ---- HTTP types ----

type (
	chatIn  struct{ Body chatInBody }
	chatOut struct{ Body chatBody }
	chatInBody struct {
		SessionID string `json:"session_id,omitempty"`
		Message   string `json:"message" required:"true"`
	}
	chatBody struct {
		SessionID string `json:"session_id"`
		Reply     string `json:"reply"`
		Turn      int    `json:"turn"`
	}
	listSessionsIn struct {
		Kind string `query:"kind"` // "" | copilot | migration
	}
	listSessionsOut  struct{ Body listSessionsBody }
	listSessionsBody struct {
		Items []session.Session `json:"items"`
	}
	approvalsListOut  struct{ Body approvalsListBody }
	approvalsListBody struct {
		Items []approvals.Entry `json:"items"`
	}
	resolveApprovalIn struct {
		ID   string `path:"id"`
		Body resolveBody
	}
	resolveBody struct {
		Status string `json:"status" enum:"approved,rejected" required:"true"`
	}
	approvalsActionOut struct {
		Body map[string]string
	}
	startMigrationIn struct {
		Body startMigrationBody
	}
	startMigrationBody struct {
		Title string `json:"title,omitempty"`
	}
	migrationSessionOut  struct{ Body migrationSessionBody }
	migrationSessionBody struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
	}
	migrationStateIn struct {
		SessionID string `path:"session_id"`
	}
	migrationStateOut struct{ Body migration.State }
	saveProfileIn     struct {
		SessionID string `path:"session_id"`
		Body      migration.SetupProfile
	}
	coaProposeOut  struct{ Body coaProposeBody }
	coaProposeBody struct {
		Proposal []migration.COAAccount `json:"proposal"`
	}
	readinessOut  struct{ Body readinessBody }
	readinessBody struct {
		Items []migration.Check `json:"items"`
	}
)

// ---- helpers ----

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fmt.Fprintf(os.Stderr, "agent: env %s required\n", k)
		os.Exit(2)
	}
	return v
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// permission import retained so future Phase-C policy checks can reuse it.
var _ = permission.ActionRead
