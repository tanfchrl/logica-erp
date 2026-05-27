// Package buyingsettings implements the per-company Buying Settings
// singleton. PI + GRN services read these values at submit time to enforce
// tolerances and workflow gates ("PO required for PI", etc).
//
// Settings are loaded through a 60-second TTL cache so the hot submit path
// doesn't pay a DB roundtrip every call. Saving via the admin endpoint
// invalidates the cache for the affected company.
package buyingsettings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "buying_settings"

type Settings struct {
	ID                       string          `json:"id"`
	CompanyID                string          `json:"company_id"`
	PORequiredForPI          bool            `json:"po_required_for_pi"`
	PRRequiredForPI          bool            `json:"pr_required_for_pi"`
	OverBillingTolerancePct  decimal.Decimal `json:"over_billing_tolerance_pct"`
	OverReceiptTolerancePct  decimal.Decimal `json:"over_receipt_tolerance_pct"`
	MaintainSameRate         bool            `json:"maintain_same_rate"`
	AllowItemMultipleTimes   bool            `json:"allow_item_multiple_times"`
	DisableLastPurchaseRate  bool            `json:"disable_last_purchase_rate"`
	BillForRejectedQty       bool            `json:"bill_for_rejected_qty"`
	DefaultSupplierGroupID   string          `json:"default_supplier_group_id,omitempty"`
	UpdatedAt                time.Time       `json:"updated_at"`
}

// SaveInput is the body shape for the admin save endpoint. Numeric tolerance
// fields are accepted as decimal strings to avoid float drift.
type SaveInput struct {
	PORequiredForPI         bool   `json:"po_required_for_pi"`
	PRRequiredForPI         bool   `json:"pr_required_for_pi"`
	OverBillingTolerancePct string `json:"over_billing_tolerance_pct,omitempty"`
	OverReceiptTolerancePct string `json:"over_receipt_tolerance_pct,omitempty"`
	MaintainSameRate        bool   `json:"maintain_same_rate"`
	AllowItemMultipleTimes  bool   `json:"allow_item_multiple_times"`
	DisableLastPurchaseRate bool   `json:"disable_last_purchase_rate"`
	BillForRejectedQty      bool   `json:"bill_for_rejected_qty"`
	DefaultSupplierGroupID  string `json:"default_supplier_group_id,omitempty"`
}

// Defaults returns the "no config" baseline — used when a company has never
// saved Buying Settings. Matches the migration defaults.
func Defaults(companyID string) Settings {
	return Settings{
		CompanyID:              companyID,
		AllowItemMultipleTimes: true,
		// Everything else is zero/false/0.
	}
}

// ---- Service ----

type Service struct {
	db    *dbx.DB
	mu    sync.RWMutex
	cache map[string]cached // key = companyID
}

type cached struct {
	v        Settings
	loadedAt time.Time
}

const cacheTTL = 60 * time.Second

func NewService(db *dbx.DB) *Service {
	return &Service{db: db, cache: map[string]cached{}}
}

// ForCompany returns the effective settings for a company. If no row exists,
// returns Defaults — callers don't need to special-case missing config.
//
// Hot-path safe: served from the 60s TTL cache.
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
		v     Settings
		group *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id,
		       po_required_for_pi, pr_required_for_pi,
		       over_billing_tolerance_pct, over_receipt_tolerance_pct,
		       maintain_same_rate, allow_item_multiple_times,
		       disable_last_purchase_rate, bill_for_rejected_qty,
		       default_supplier_group_id, updated_at
		FROM buying_settings WHERE company_id = $1`, companyID).
		Scan(&v.ID, &v.CompanyID,
			&v.PORequiredForPI, &v.PRRequiredForPI,
			&v.OverBillingTolerancePct, &v.OverReceiptTolerancePct,
			&v.MaintainSameRate, &v.AllowItemMultipleTimes,
			&v.DisableLastPurchaseRate, &v.BillForRejectedQty,
			&group, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(companyID), nil
	}
	if err != nil {
		return Defaults(companyID), err
	}
	if group != nil {
		v.DefaultSupplierGroupID = *group
	}
	return v, nil
}

// Save upserts the buying_settings row for the calling user's active
// company and invalidates the cache. System-admin only.
func (s *Service) Save(ctx context.Context, companyID string, in SaveInput) (Settings, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return Settings{}, errors.New("buying_settings: unauthenticated")
	}
	if companyID == "" {
		return Settings{}, errors.New("buying_settings: X-Company-Id required")
	}

	overBilling, err := parsePct(in.OverBillingTolerancePct, "over_billing_tolerance_pct")
	if err != nil {
		return Settings{}, err
	}
	overReceipt, err := parsePct(in.OverReceiptTolerancePct, "over_receipt_tolerance_pct")
	if err != nil {
		return Settings{}, err
	}

	id := dbx.NewIDWithPrefix("bset")
	if _, err := s.db.Exec(ctx, `
		INSERT INTO buying_settings (
			id, company_id,
			po_required_for_pi, pr_required_for_pi,
			over_billing_tolerance_pct, over_receipt_tolerance_pct,
			maintain_same_rate, allow_item_multiple_times,
			disable_last_purchase_rate, bill_for_rejected_qty,
			default_supplier_group_id, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (company_id) DO UPDATE SET
			po_required_for_pi        = EXCLUDED.po_required_for_pi,
			pr_required_for_pi        = EXCLUDED.pr_required_for_pi,
			over_billing_tolerance_pct = EXCLUDED.over_billing_tolerance_pct,
			over_receipt_tolerance_pct = EXCLUDED.over_receipt_tolerance_pct,
			maintain_same_rate        = EXCLUDED.maintain_same_rate,
			allow_item_multiple_times = EXCLUDED.allow_item_multiple_times,
			disable_last_purchase_rate = EXCLUDED.disable_last_purchase_rate,
			bill_for_rejected_qty     = EXCLUDED.bill_for_rejected_qty,
			default_supplier_group_id = EXCLUDED.default_supplier_group_id,
			updated_at = now(), updated_by = EXCLUDED.updated_by`,
		id, companyID,
		in.PORequiredForPI, in.PRRequiredForPI,
		overBilling, overReceipt,
		in.MaintainSameRate, in.AllowItemMultipleTimes,
		in.DisableLastPurchaseRate, in.BillForRejectedQty,
		nullable(in.DefaultSupplierGroupID), p.UserID); err != nil {
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
		OperationID: "get-buying-settings", Method: http.MethodGet,
		Path: "/admin/buying-settings", Summary: "Get effective Buying Settings for the active company",
		Tags: []string{"Admin / Buying"},
	}, func(ctx context.Context, _ *struct{}) (*bsOut, error) {
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
		return &bsOut{Body: v}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-buying-settings", Method: http.MethodPost,
		Path: "/admin/buying-settings", Summary: "Save Buying Settings for the active company",
		Tags: []string{"Admin / Buying"},
	}, func(ctx context.Context, in *bsSaveIn) (*bsOut, error) {
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
		return &bsOut{Body: v}, nil
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

func parsePct(s, field string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s: %w", field, err)
	}
	if d.IsNegative() {
		return decimal.Zero, fmt.Errorf("%s: must be >= 0", field)
	}
	return d, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type (
	bsOut    struct{ Body Settings }
	bsSaveIn struct {
		Body SaveInput
	}
)
