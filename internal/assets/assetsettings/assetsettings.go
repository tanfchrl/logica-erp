// Package assetsettings implements the per-company Asset Settings singleton.
// Same pattern as buyingsettings — short TTL cache, system-only admin API.
package assetsettings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "asset_settings"

type Settings struct {
	ID                       string    `json:"id"`
	CompanyID                string    `json:"company_id"`
	AutoCreateAssetsFromPI   bool      `json:"auto_create_assets_from_pi"`
	DefaultFinanceBookID     string    `json:"default_finance_book_id,omitempty"`
	RegisterShowZeroNBV      bool      `json:"register_show_zero_nbv"`
	RegisterGroupBy          string    `json:"register_group_by"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type SaveInput struct {
	AutoCreateAssetsFromPI bool   `json:"auto_create_assets_from_pi"`
	DefaultFinanceBookID   string `json:"default_finance_book_id,omitempty"`
	RegisterShowZeroNBV    bool   `json:"register_show_zero_nbv"`
	RegisterGroupBy        string `json:"register_group_by,omitempty"`
}

func Defaults(companyID string) Settings {
	return Settings{
		CompanyID: companyID,
		// Mirrors the migration's column defaults; AutoCreateAssetsFromPI
		// is the deliberate-but-quiet hot path so users who set
		// is_fixed_asset+asset_category get the magic without touching
		// the settings page.
		AutoCreateAssetsFromPI: true,
		RegisterGroupBy:        "category",
	}
}

type Service struct {
	db    *dbx.DB
	mu    sync.RWMutex
	cache map[string]cached
}
type cached struct {
	v        Settings
	loadedAt time.Time
}

const cacheTTL = 60 * time.Second

func NewService(db *dbx.DB) *Service {
	return &Service{db: db, cache: map[string]cached{}}
}

func (s *Service) ForCompany(ctx context.Context, companyID string) (Settings, error) {
	if companyID == "" {
		return Defaults(""), nil
	}
	s.mu.RLock()
	c, ok := s.cache[companyID]
	s.mu.RUnlock()
	if ok && time.Since(c.loadedAt) < cacheTTL {
		return c.v, nil
	}
	v, err := s.loadFromDB(ctx, companyID)
	if err != nil {
		return Defaults(companyID), err
	}
	s.mu.Lock()
	s.cache[companyID] = cached{v: v, loadedAt: time.Now()}
	s.mu.Unlock()
	return v, nil
}

func (s *Service) loadFromDB(ctx context.Context, companyID string) (Settings, error) {
	var (
		v       Settings
		bookOpt *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, auto_create_assets_from_pi, default_finance_book_id,
		       register_show_zero_nbv, register_group_by, updated_at
		FROM asset_settings WHERE company_id = $1`, companyID).
		Scan(&v.ID, &v.CompanyID, &v.AutoCreateAssetsFromPI, &bookOpt,
			&v.RegisterShowZeroNBV, &v.RegisterGroupBy, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(companyID), nil
	}
	if err != nil {
		return Defaults(companyID), err
	}
	if bookOpt != nil {
		v.DefaultFinanceBookID = *bookOpt
	}
	return v, nil
}

func (s *Service) Save(ctx context.Context, companyID string, in SaveInput) (Settings, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return Settings{}, errors.New("asset_settings: unauthenticated")
	}
	if companyID == "" {
		return Settings{}, errors.New("asset_settings: X-Company-Id required")
	}
	groupBy := in.RegisterGroupBy
	if groupBy == "" {
		groupBy = "category"
	}
	switch groupBy {
	case "category", "status", "location", "none":
	default:
		return Settings{}, fmt.Errorf("register_group_by: invalid %q", groupBy)
	}
	id := dbx.NewIDWithPrefix("aset")
	if _, err := s.db.Exec(ctx, `
		INSERT INTO asset_settings (
			id, company_id, auto_create_assets_from_pi, default_finance_book_id,
			register_show_zero_nbv, register_group_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (company_id) DO UPDATE SET
			auto_create_assets_from_pi = EXCLUDED.auto_create_assets_from_pi,
			default_finance_book_id    = EXCLUDED.default_finance_book_id,
			register_show_zero_nbv     = EXCLUDED.register_show_zero_nbv,
			register_group_by          = EXCLUDED.register_group_by,
			updated_at = now(), updated_by = EXCLUDED.updated_by`,
		id, companyID, in.AutoCreateAssetsFromPI, nullable(in.DefaultFinanceBookID),
		in.RegisterShowZeroNBV, groupBy, p.UserID); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	delete(s.cache, companyID)
	s.mu.Unlock()
	return s.loadFromDB(ctx, companyID)
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-asset-settings", Method: http.MethodGet,
		Path: "/admin/asset-settings", Summary: "Get Asset Settings for the active company",
		Tags: []string{"Admin / Assets"},
	}, func(ctx context.Context, _ *struct{}) (*asOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		v, err := h.Service.ForCompany(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &asOut{Body: v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "save-asset-settings", Method: http.MethodPost,
		Path: "/admin/asset-settings", Summary: "Save Asset Settings for the active company",
		Tags: []string{"Admin / Assets"},
	}, func(ctx context.Context, in *asSaveIn) (*asOut, error) {
		if err := requireSystem(ctx); err != nil {
			return nil, err
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		v, err := h.Service.Save(ctx, co, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &asOut{Body: v}, nil
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

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type (
	asOut    struct{ Body Settings }
	asSaveIn struct {
		Body SaveInput
	}
)
