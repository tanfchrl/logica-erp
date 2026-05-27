package policy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
)

// LimitsStore is the DB-backed LimitProvider. Caches the active set with a
// short TTL so the hot tool-dispatch path doesn't pay a DB hit per call.
type LimitsStore struct {
	db    *dbx.DB
	mu    sync.RWMutex
	cache map[string]ValueLimit // key = doctype + "|" + companyID
	loaded time.Time
}

func NewLimitsStore(db *dbx.DB) *LimitsStore {
	return &LimitsStore{db: db, cache: map[string]ValueLimit{}}
}

// LimitFor satisfies the LimitProvider interface. Returns the most specific
// active limit: a (doctype, companyID) override beats a (doctype, nil)
// global default. Cache is refreshed every 60s.
func (s *LimitsStore) LimitFor(doctype, companyID string) (ValueLimit, bool) {
	s.refreshIfStale()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if companyID != "" {
		if v, ok := s.cache[doctype+"|"+companyID]; ok {
			return v, true
		}
	}
	v, ok := s.cache[doctype+"|"] // global default
	return v, ok
}

const cacheTTL = 60 * time.Second

func (s *LimitsStore) refreshIfStale() {
	s.mu.RLock()
	fresh := time.Since(s.loaded) < cacheTTL
	s.mu.RUnlock()
	if fresh {
		return
	}
	_ = s.refresh(context.Background())
}

// refresh re-reads the active rows. Called every cacheTTL and explicitly
// after admin writes (Save / Delete).
func (s *LimitsStore) refresh(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `
		SELECT doctype, coalesce(company_id, ''), field, max_idr, label
		FROM agent_policy_value_limit
		WHERE is_active = true`)
	if err != nil {
		return err
	}
	defer rows.Close()
	next := map[string]ValueLimit{}
	for rows.Next() {
		var dt, co, field, label string
		var max decimal.Decimal
		if err := rows.Scan(&dt, &co, &field, &max, &label); err != nil {
			return err
		}
		next[dt+"|"+co] = ValueLimit{Doctype: dt, Field: field, MaxIDR: max, Label: label}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.cache = next
	s.loaded = time.Now()
	s.mu.Unlock()
	return nil
}

// ---- Admin types ----

// LimitRow is the API-facing shape — string-encoded amount so JS preserves
// IDR precision (no float).
type LimitRow struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id,omitempty"` // empty = global
	Doctype   string `json:"doctype"`
	Field     string `json:"field"`
	MaxIDR    string `json:"max_idr"`
	Label     string `json:"label,omitempty"`
	IsActive  bool   `json:"is_active"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SaveLimitInput is the request body for upsert.
type SaveLimitInput struct {
	CompanyID string `json:"company_id,omitempty"`
	Doctype   string `json:"doctype"   doc:"e.g. sales_invoice"`
	Field     string `json:"field"     doc:"e.g. grand_total"`
	MaxIDR    string `json:"max_idr"   doc:"decimal string in IDR"`
	Label     string `json:"label,omitempty"`
	IsActive  bool   `json:"is_active"`
}

// List returns every configured limit. Used by the Settings UI.
func (s *LimitsStore) List(ctx context.Context) ([]LimitRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, coalesce(company_id, ''), doctype, field, max_idr, label, is_active, updated_at
		FROM agent_policy_value_limit
		ORDER BY doctype, company_id NULLS FIRST, field`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LimitRow, 0)
	for rows.Next() {
		var r LimitRow
		var maxDec decimal.Decimal
		if err := rows.Scan(&r.ID, &r.CompanyID, &r.Doctype, &r.Field, &maxDec, &r.Label, &r.IsActive, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.MaxIDR = maxDec.StringFixed(2)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Save upserts a (company_id, doctype, field) row. Returns the persisted
// state. Refreshes the cache so the next dispatch sees the new limit.
func (s *LimitsStore) Save(ctx context.Context, in SaveLimitInput) (*LimitRow, error) {
	in.Doctype = strings.TrimSpace(in.Doctype)
	in.Field = strings.TrimSpace(in.Field)
	if in.Doctype == "" || in.Field == "" {
		return nil, errors.New("doctype and field are required")
	}
	max, err := decimal.NewFromString(strings.TrimSpace(in.MaxIDR))
	if err != nil {
		return nil, errors.New("max_idr must be a decimal string")
	}
	if max.Sign() < 0 {
		return nil, errors.New("max_idr must be >= 0")
	}
	id := dbx.NewIDWithPrefix("aplim")
	var row LimitRow
	var maxOut decimal.Decimal
	err = s.db.QueryRow(ctx, `
		INSERT INTO agent_policy_value_limit
		  (id, company_id, doctype, field, max_idr, label, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT ((coalesce(company_id, '')), doctype, field) DO UPDATE SET
		  max_idr    = EXCLUDED.max_idr,
		  label      = EXCLUDED.label,
		  is_active  = EXCLUDED.is_active,
		  updated_at = now()
		RETURNING id, coalesce(company_id, ''), doctype, field, max_idr, label, is_active, updated_at`,
		id, nullStr(in.CompanyID), in.Doctype, in.Field, max, in.Label, in.IsActive,
	).Scan(&row.ID, &row.CompanyID, &row.Doctype, &row.Field, &maxOut, &row.Label, &row.IsActive, &row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	row.MaxIDR = maxOut.StringFixed(2)
	_ = s.refresh(ctx)
	return &row, nil
}

// Delete drops a (company_id, doctype, field) row.
func (s *LimitsStore) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM agent_policy_value_limit WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("limit not found")
	}
	_ = s.refresh(ctx)
	return nil
}

// ---- HTTP ----

// RegisterAdmin wires the limit-admin endpoints on the agent service's huma
// adapter. All three are system-only.
func RegisterAdmin(api huma.API, s *LimitsStore) {
	huma.Register(api, huma.Operation{
		OperationID: "list-agent-policy-limits",
		Method:      http.MethodGet,
		Path:        "/admin/policy/limits",
		Summary:     "Configured Tier-1 value caps (system administrators only)",
		Tags:        []string{"Agent / Admin"},
	}, func(ctx context.Context, _ *struct{}) (*limitsOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		rs, err := s.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &limitsOut{Body: limitsBody{Items: rs}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-agent-policy-limit",
		Method:      http.MethodPost,
		Path:        "/admin/policy/limits",
		Summary:     "Upsert a value cap (company_id, doctype, field)",
		Tags:        []string{"Agent / Admin"},
	}, func(ctx context.Context, in *saveLimitIn) (*saveLimitOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		r, err := s.Save(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &saveLimitOut{Body: *r}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-agent-policy-limit",
		Method:      http.MethodDelete,
		Path:        "/admin/policy/limits/{id}",
		Summary:     "Delete a value cap by id",
		Tags:        []string{"Agent / Admin"},
	}, func(ctx context.Context, in *deleteLimitIn) (*struct{ Body map[string]string }, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		if err := s.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
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

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Silence the unused import linter on pgx — its sentinel is referenced
// inside the conflict block via the upsert clause but tooling doesn't
// always see that.
var _ = pgx.ErrNoRows

type (
	limitsOut struct{ Body limitsBody }
	limitsBody struct {
		Items []LimitRow `json:"items"`
	}
	saveLimitIn struct {
		Body SaveLimitInput
	}
	saveLimitOut struct{ Body LimitRow }
	deleteLimitIn struct {
		ID string `path:"id"`
	}
)
