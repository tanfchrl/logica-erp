// Package agentllmconfig owns the per-company BYOM model configuration.
// Lets admins switch provider / endpoint / model / API key without touching
// docker-compose. The agent service consults this on every chat dispatch
// (with a short TTL cache).
//
// Hot path:
//   1. agent receives /chat. Extracts company_id from session.
//   2. ConfigProvider.ForCompany(ctx, companyID) returns *llm.Client.
//   3. ForCompany checks the per-company TTL cache.
//   4. Cache miss → DB lookup + Decrypt → build llm.Client → cache.
//   5. Cache hit → return the cached client.
//
// Reload: POST /admin/agent-llm-config/reload (or any Save) invalidates the
// per-company cache entry. Next call picks up the new config.
package agentllmconfig

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/agent/llm"
	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
)

const Doctype = "agent_llm_config"

// defaultBaseURLFor returns the public endpoint for a provider so callers can
// leave base_url blank in the common case. Today only Anthropic is wired.
func defaultBaseURLFor(provider string) string {
	switch provider {
	case "anthropic", "":
		return "https://api.anthropic.com"
	default:
		return ""
	}
}

// LLMConfig is the read-side payload: same shape the FE renders. Note the
// distinction from llm.Config — that's the agent client config; this is
// the persistent DB row (no API key — only the last4 hint).
type LLMConfig struct {
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	Provider      string    `json:"provider"`
	BaseURL       string    `json:"base_url"`
	Model         string    `json:"model"`
	APIKeyLast4   string    `json:"api_key_last4,omitempty"`
	APIKeyPresent bool      `json:"api_key_present"`
	IsActive      bool      `json:"is_active"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// LLMConfigSaveInput is the write-side payload. APIKey is full plaintext; the
// service encrypts before persisting. Empty APIKey means "leave the
// existing key alone"; explicit "" wrapped as ClearAPIKey=true clears it.
type LLMConfigSaveInput struct {
	Provider    string `json:"provider,omitempty" doc:"openai | anthropic | azure | litellm | local | custom"`
	BaseURL     string `json:"base_url"`
	Model       string `json:"model"`
	APIKey      string `json:"api_key,omitempty" doc:"plaintext; omit to keep existing key unchanged"`
	ClearAPIKey bool   `json:"clear_api_key,omitempty" doc:"set true to wipe the stored key"`
	IsActive    bool   `json:"is_active,omitempty"`
}

// ---- Service ----

type Service struct {
	db        *dbx.DB
	encrypter *Encrypter
	envFallback llm.Config // baseline from agent-container env, used when no DB row exists

	mu    sync.RWMutex
	cache map[string]cached // companyID → resolved client
}

type cached struct {
	client   *llm.Client
	loadedAt time.Time
}

const cacheTTL = 60 * time.Second

func NewService(db *dbx.DB, enc *Encrypter, envFallback llm.Config) *Service {
	return &Service{
		db: db, encrypter: enc, envFallback: envFallback,
		cache: map[string]cached{},
	}
}

// ForCompany returns the agent LLM client to use for the given company.
// Cache-first, DB on miss, env-fallback if no DB row. Never errors —
// returns the env-fallback client if anything goes wrong, so chat never
// hard-fails because of config issues.
func (s *Service) ForCompany(ctx context.Context, companyID string) *llm.Client {
	if companyID == "" {
		return llm.New(s.envFallback)
	}
	s.mu.RLock()
	c, ok := s.cache[companyID]
	s.mu.RUnlock()
	if ok && time.Since(c.loadedAt) < cacheTTL {
		return c.client
	}
	client := s.buildForCompany(ctx, companyID)
	s.mu.Lock()
	s.cache[companyID] = cached{client: client, loadedAt: time.Now()}
	s.mu.Unlock()
	return client
}

// Invalidate drops the per-company cache entry so the next ForCompany call
// re-reads from the DB. Called from Save and from the explicit reload
// admin endpoint.
func (s *Service) Invalidate(companyID string) {
	s.mu.Lock()
	delete(s.cache, companyID)
	s.mu.Unlock()
}

// buildForCompany reads the active row, decrypts the API key, returns a
// fresh llm.Client. Never returns nil — falls back to env defaults so
// chat keeps working through transient DB issues.
func (s *Service) buildForCompany(ctx context.Context, companyID string) *llm.Client {
	cfg, err := s.loadFromDB(ctx, companyID)
	if err != nil || cfg == nil || !cfg.IsActive {
		return llm.New(s.envFallback)
	}
	// Pull + decrypt the key.
	var (
		ct, nonce []byte
	)
	row := s.db.QueryRow(ctx,
		`SELECT api_key_ciphertext, api_key_nonce FROM agent_llm_config WHERE id = $1`, cfg.ID)
	if err := row.Scan(&ct, &nonce); err != nil {
		return llm.New(s.envFallback)
	}
	apiKey := s.envFallback.APIKey
	if len(ct) > 0 && s.encrypter != nil {
		pt, derr := s.encrypter.Decrypt(ct, nonce)
		if derr == nil {
			apiKey = string(pt)
		}
	}
	return llm.New(llm.Config{
		Provider: llm.Provider(cfg.Provider),
		BaseURL:  cfg.BaseURL,
		APIKey:   apiKey,
		Model:    cfg.Model,
	})
}

// ---- read-side ----

func (s *Service) GetForCompany(ctx context.Context, companyID string) (*LLMConfig, error) {
	return s.loadFromDB(ctx, companyID)
}

func (s *Service) loadFromDB(ctx context.Context, companyID string) (*LLMConfig, error) {
	if companyID == "" {
		return nil, nil
	}
	var (
		c        LLMConfig
		ct       []byte
		last4    *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, provider, base_url, model,
		       api_key_ciphertext, api_key_last4,
		       is_active, updated_at
		FROM agent_llm_config WHERE company_id = $1`, companyID).
		Scan(&c.ID, &c.CompanyID, &c.Provider, &c.BaseURL, &c.Model,
			&ct, &last4, &c.IsActive, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.APIKeyPresent = len(ct) > 0
	if last4 != nil {
		c.APIKeyLast4 = *last4
	}
	return &c, nil
}

// ---- write-side ----

// Save upserts the (company_id) row. If APIKey is non-empty, it's
// encrypted and replaces the prior key. If ClearAPIKey is true, the key
// is wiped. Otherwise the existing key (if any) is preserved.
func (s *Service) Save(ctx context.Context, companyID string, in LLMConfigSaveInput) (*LLMConfig, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("agent_llm_config: unauthenticated")
	}
	if companyID == "" {
		return nil, errors.New("agent_llm_config: X-Company-Id required")
	}
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.Model = strings.TrimSpace(in.Model)
	if in.Model == "" {
		return nil, errors.New("agent_llm_config: model is required")
	}
	provider := strings.TrimSpace(in.Provider)
	if provider == "" {
		provider = "anthropic"
	}
	// base_url is an optional advanced override; default to the provider's
	// public endpoint so the UI doesn't have to send it.
	if in.BaseURL == "" {
		in.BaseURL = defaultBaseURLFor(provider)
	}

	id := dbx.NewIDWithPrefix("llmc")
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Decide what to do with the API key:
		//   APIKey set     → encrypt + replace
		//   ClearAPIKey    → set NULL
		//   neither        → keep whatever's there
		var ct, nonce []byte
		var last4 *string
		keepKey := in.APIKey == "" && !in.ClearAPIKey

		if in.APIKey != "" {
			if s.encrypter == nil {
				return errors.New("agent_llm_config: encryption key not configured on the server (AGENT_CONFIG_ENCRYPTION_KEY)")
			}
			c, n, err := s.encrypter.Encrypt([]byte(in.APIKey))
			if err != nil {
				return fmt.Errorf("encrypt api key: %w", err)
			}
			ct, nonce = c, n
			l := Last4(in.APIKey)
			last4 = &l
		}

		if keepKey {
			// Upsert that preserves the existing key columns.
			_, err := tx.Exec(ctx, `
				INSERT INTO agent_llm_config (id, company_id, provider, base_url, model, is_active, updated_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (company_id) DO UPDATE SET
				  provider = EXCLUDED.provider,
				  base_url = EXCLUDED.base_url,
				  model    = EXCLUDED.model,
				  is_active = EXCLUDED.is_active,
				  updated_at = now(), updated_by = EXCLUDED.updated_by`,
				id, companyID, provider, in.BaseURL, in.Model, in.IsActive, p.UserID)
			if err != nil {
				return err
			}
		} else {
			// Upsert that also writes the key columns (or clears them).
			_, err := tx.Exec(ctx, `
				INSERT INTO agent_llm_config (
					id, company_id, provider, base_url, model,
					api_key_ciphertext, api_key_nonce, api_key_last4,
					is_active, updated_by
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT (company_id) DO UPDATE SET
				  provider = EXCLUDED.provider,
				  base_url = EXCLUDED.base_url,
				  model    = EXCLUDED.model,
				  api_key_ciphertext = EXCLUDED.api_key_ciphertext,
				  api_key_nonce      = EXCLUDED.api_key_nonce,
				  api_key_last4      = EXCLUDED.api_key_last4,
				  is_active = EXCLUDED.is_active,
				  updated_at = now(), updated_by = EXCLUDED.updated_by`,
				id, companyID, provider, in.BaseURL, in.Model,
				ct, nonce, last4, in.IsActive, p.UserID)
			if err != nil {
				return err
			}
		}

		// Audit: never log the key, even encrypted. Provider + URL + model
		// only.
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{
			After: map[string]any{
				"provider": provider, "base_url": in.BaseURL, "model": in.Model,
				"is_active": in.IsActive,
				"key_changed": !keepKey,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	s.Invalidate(companyID)
	return s.loadFromDB(ctx, companyID)
}

// TestConnection dials the supplied base_url with a tiny chat-completions
// request. Each input field is independently optional — missing ones fall
// back to the stored row so the user can "Test" by editing only one field.
func (s *Service) TestConnection(ctx context.Context, companyID string, in LLMConfigSaveInput) error {
	baseURL, model, apiKey := strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.Model), in.APIKey
	provider := strings.TrimSpace(in.Provider)

	if baseURL == "" || model == "" || provider == "" {
		stored, err := s.loadFromDB(ctx, companyID)
		if err != nil {
			return fmt.Errorf("test: %w", err)
		}
		if model == "" {
			if stored == nil {
				return errors.New("test: model required (no stored config to fall back to)")
			}
			model = stored.Model
		}
		if baseURL == "" && stored != nil {
			baseURL = stored.BaseURL
		}
		if provider == "" {
			if stored != nil && stored.Provider != "" {
				provider = stored.Provider
			} else {
				provider = "anthropic"
			}
		}
	}
	if baseURL == "" {
		baseURL = defaultBaseURLFor(provider)
	}
	if apiKey == "" {
		ct, nonce, err := s.fetchKeyCipher(ctx, companyID)
		if err != nil {
			return fmt.Errorf("test: read stored key: %w", err)
		}
		if len(ct) == 0 {
			return errors.New("test: no API key provided and none on file")
		}
		if s.encrypter == nil {
			return errors.New("test: encryption key not configured on the server")
		}
		pt, err := s.encrypter.Decrypt(ct, nonce)
		if err != nil {
			return fmt.Errorf("test: decrypt stored key: %w", err)
		}
		apiKey = string(pt)
	}
	if model == "" {
		return errors.New("test: model is required")
	}

	// Single-message ping. We only care that auth + URL work; quality of
	// the reply is irrelevant.
	client := llm.New(llm.Config{Provider: llm.Provider(provider), BaseURL: baseURL, APIKey: apiKey, Model: model})
	_, err := client.Chat(ctx, llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "ping"}},
	})
	return err
}

func (s *Service) fetchKeyCipher(ctx context.Context, companyID string) (ct, nonce []byte, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT coalesce(api_key_ciphertext, ''::bytea), coalesce(api_key_nonce, ''::bytea)
		FROM agent_llm_config WHERE company_id = $1`, companyID).Scan(&ct, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	return ct, nonce, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-agent-llm-config", Method: http.MethodGet,
		Path: "/admin/agent-llm-config", Summary: "Get the BYOM LLM config for the active company",
		Tags: []string{"Admin / Agent"},
	}, func(ctx context.Context, _ *struct{}) (*llmCfgOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		cfg, err := h.Service.GetForCompany(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		if cfg == nil {
			return &llmCfgOut{Body: LLMConfig{CompanyID: co}}, nil
		}
		return &llmCfgOut{Body: *cfg}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "save-agent-llm-config", Method: http.MethodPost,
		Path: "/admin/agent-llm-config", Summary: "Save the BYOM LLM config",
		Tags: []string{"Admin / Agent"},
	}, func(ctx context.Context, in *llmCfgSaveIn) (*llmCfgOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		cfg, err := h.Service.Save(ctx, co, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &llmCfgOut{Body: *cfg}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "reload-agent-llm-config", Method: http.MethodPost,
		Path: "/admin/agent-llm-config/reload", Summary: "Drop the agent's cached config and re-read on next call",
		Tags: []string{"Admin / Agent"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body map[string]string }, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		h.Service.Invalidate(co)
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "test-agent-llm-config", Method: http.MethodPost,
		Path: "/admin/agent-llm-config/test", Summary: "Dial the configured endpoint with a tiny prompt to verify auth + URL",
		Tags: []string{"Admin / Agent"},
	}, func(ctx context.Context, in *llmCfgSaveIn) (*llmTestOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		if err := h.Service.TestConnection(ctx, co, in.Body); err != nil {
			return &llmTestOut{Body: llmTestBody{OK: false, Error: err.Error()}}, nil
		}
		return &llmTestOut{Body: llmTestBody{OK: true}}, nil
	})
}

func requireSystem(ctx context.Context) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return huma.NewError(http.StatusUnauthorized, "unauthenticated")
	}
	if !p.IsSystem {
		return huma.NewError(http.StatusForbidden, "system administrators only")
	}
	return nil
}

type (
	llmCfgOut   struct{ Body LLMConfig }
	llmCfgSaveIn struct {
		Body LLMConfigSaveInput
	}
	llmTestOut  struct{ Body llmTestBody }
	llmTestBody struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
)
