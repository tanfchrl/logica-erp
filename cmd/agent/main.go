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

	"github.com/tandigital/logica-erp/internal/agent/audit"
	"github.com/tandigital/logica-erp/internal/agent/erpclient"
	"github.com/tandigital/logica-erp/internal/agent/llm"
	"github.com/tandigital/logica-erp/internal/agent/policy"
	"github.com/tandigital/logica-erp/internal/agent/session"
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
	_ = gate
	_ = erp

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

		registerChat(hapi, sessStore, rec, llmClient, registry)
		registerSessions(hapi, sessStore)
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

func registerChat(api huma.API, store *session.Store, rec *audit.Recorder, ll *llm.Client, registry *agentcontract.Registry) {
	huma.Register(api, huma.Operation{
		OperationID: "agent-chat",
		Method:      http.MethodPost,
		Path:        "/chat",
		Summary:     "Send a turn to the agent. Phase-A stub returns a canned reply when no LLM is configured.",
		Tags:        []string{"Agent / Chat"},
	}, func(ctx context.Context, in *chatIn) (*chatOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		co := auth.CompanyFromContext(ctx)

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
		nextTurn := len(history) + 1

		// Record the user turn.
		userMsg := session.Message{
			SessionID: sess.ID, Turn: nextTurn,
			Role: "user", Content: in.Body.Message,
		}
		if err := store.AppendMessage(ctx, userMsg); err != nil {
			return nil, httpx.MapError(err)
		}
		rec.Record(ctx, audit.Event{
			SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
			Turn: nextTurn, Type: audit.EventPrompt,
			Payload: map[string]any{"content": in.Body.Message},
			Model:   ll.Model(),
		})

		// Build the assistant reply. Phase-A behaviour:
		//   - If LLM is configured: call it with no tools (just the prompt) so
		//     ops can verify the network path end-to-end.
		//   - Otherwise: return a canned reply that names the loaded contracts
		//     so the FE has something to render while wiring proceeds.
		var assistantContent string
		started := time.Now()
		var tokensIn, tokensOut int
		if ll.Configured() {
			req := buildBaseRequest(registry, history, in.Body.Message)
			resp, err := ll.Chat(ctx, req)
			if err != nil {
				rec.Record(ctx, audit.Event{
					SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
					Turn: nextTurn + 1, Type: audit.EventError,
					Payload: map[string]any{"err": err.Error()},
					LatencyMS: int(time.Since(started) / time.Millisecond),
				})
				assistantContent = "I couldn't reach the language model: " + err.Error()
			} else {
				if len(resp.Choices) > 0 {
					assistantContent = resp.Choices[0].Message.Content
				}
				tokensIn = resp.Usage.PromptTokens
				tokensOut = resp.Usage.CompletionTokens
			}
		} else {
			assistantContent = fmt.Sprintf(
				"Agent service is up. No LLM is configured — set AGENT_LLM_BASE_URL/AGENT_LLM_API_KEY/AGENT_LLM_MODEL to enable conversation. Loaded %s.",
				registry.Summary(),
			)
		}

		assistantMsg := session.Message{
			SessionID: sess.ID, Turn: nextTurn + 1,
			Role: "assistant", Content: assistantContent,
		}
		if err := store.AppendMessage(ctx, assistantMsg); err != nil {
			return nil, httpx.MapError(err)
		}
		rec.Record(ctx, audit.Event{
			SessionID: sess.ID, UserID: p.UserID, CompanyID: co,
			Turn: nextTurn + 1, Type: audit.EventToolResult,
			Payload: map[string]any{"content": assistantContent},
			Model:   ll.Model(), TokensIn: tokensIn, TokensOut: tokensOut,
			LatencyMS: int(time.Since(started) / time.Millisecond),
		})

		return &chatOut{Body: chatBody{
			SessionID: sess.ID,
			Reply:     assistantContent,
			Turn:      nextTurn + 1,
		}}, nil
	})
}

func buildBaseRequest(reg *agentcontract.Registry, history []session.Message, userMessage string) llm.Request {
	sys := []string{
		"You are Logica AI Copilot — a tool-using agent for an Indonesian ERP.",
		"You assist Bahasa Indonesia or English speakers with their day-to-day ERP tasks.",
		"Be concise and concrete. Refer to documents by their human name (e.g. 'SI-2026-0042') not their internal id.",
		"Never invent data — if you don't have it, say so plainly.",
	}
	for _, c := range reg.All() {
		if c.SystemContext == "" {
			continue
		}
		sys = append(sys, fmt.Sprintf("[%s] %s", c.DisplayName, c.SystemContext))
	}
	messages := []llm.Message{{Role: "system", Content: strings.Join(sys, "\n\n")}}
	for _, m := range history {
		// Don't replay tool messages in Phase A — we have no tools yet, and
		// a stray tool message confuses the model.
		if m.Role == "tool" {
			continue
		}
		messages = append(messages, llm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: userMessage})
	return llm.Request{Messages: messages, Temperature: 0.2}
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

// Quieten the "imported and not used" linter for permission until Phase B
// adds policy-gated tools.
var _ = permission.ActionRead
var _ = json.RawMessage(nil)
