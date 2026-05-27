// Package assetcategory exposes CRUD for the asset_category master.
// The category captures the four fields a new Asset normally inherits:
// default depreciation method, total useful life (months), and the three
// GL accounts (asset, accumulated dep, dep expense).
//
// Used by the Asset create form to pre-fill those fields after the user
// picks a category — the user can still override per-asset before submit.
package assetcategory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "asset_category"

// AssetCategory is the master record.
type AssetCategory struct {
	ID                                  string    `json:"id"`
	Name                                string    `json:"name"`
	DefaultDepreciationMethod           string    `json:"default_depreciation_method"`
	TotalUsefulLifeMonths               int       `json:"total_useful_life_months"`
	AssetAccountID                      string    `json:"asset_account_id,omitempty"`
	AccumulatedDepreciationAccountID    string    `json:"accumulated_depreciation_account_id,omitempty"`
	DepreciationExpenseAccountID        string    `json:"depreciation_expense_account_id,omitempty"`
	IsDeleted                           bool      `json:"is_deleted"`
	CreatedAt                           time.Time `json:"created_at"`
}

// AssetCategoryInput is the body for create + update. Name is immutable
// once a category exists (it's the foreign-key target on the asset table),
// so update accepts only the four config fields.
type AssetCategoryInput struct {
	Name                             string `json:"name"`
	DefaultDepreciationMethod        string `json:"default_depreciation_method,omitempty"  doc:"straight_line | declining_balance"`
	TotalUsefulLifeMonths            int    `json:"total_useful_life_months"`
	AssetAccountID                   string `json:"asset_account_id,omitempty"`
	AccumulatedDepreciationAccountID string `json:"accumulated_depreciation_account_id,omitempty"`
	DepreciationExpenseAccountID     string `json:"depreciation_expense_account_id,omitempty"`
}

type Service struct {
	db *dbx.DB
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CRUD ----

func (s *Service) Create(ctx context.Context, in AssetCategoryInput) (*AssetCategory, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_category: unauthenticated")
	}
	if err := validate(in); err != nil {
		return nil, err
	}
	id := dbx.NewIDWithPrefix("ac")
	var out AssetCategory
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset_category (
				id, name, default_depreciation_method, total_useful_life_months,
				asset_account_id, accumulated_depreciation_account_id, depreciation_expense_account_id
			) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, strings.TrimSpace(in.Name), defaultMethod(in.DefaultDepreciationMethod), in.TotalUsefulLifeMonths,
			nullable(in.AssetAccountID), nullable(in.AccumulatedDepreciationAccountID), nullable(in.DepreciationExpenseAccountID),
		); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

func (s *Service) Update(ctx context.Context, id string, in AssetCategoryInput) (*AssetCategory, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_category: unauthenticated")
	}
	// name is set at create and immutable; only validate the four
	// config fields the user actually changes.
	if in.TotalUsefulLifeMonths <= 0 {
		return nil, errors.New("asset_category.total_useful_life_months: must be > 0")
	}
	var out AssetCategory
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE asset_category SET
				default_depreciation_method = $1,
				total_useful_life_months    = $2,
				asset_account_id            = $3,
				accumulated_depreciation_account_id = $4,
				depreciation_expense_account_id     = $5
			WHERE id = $6 AND is_deleted = false`,
			defaultMethod(in.DefaultDepreciationMethod), in.TotalUsefulLifeMonths,
			nullable(in.AssetAccountID), nullable(in.AccumulatedDepreciationAccountID),
			nullable(in.DepreciationExpenseAccountID), id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("asset_category %s not found", id)
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

func (s *Service) Get(ctx context.Context, id string) (*AssetCategory, error) {
	var out *AssetCategory
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		c, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = c
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context) ([]AssetCategory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, default_depreciation_method, total_useful_life_months,
		       coalesce(asset_account_id, ''),
		       coalesce(accumulated_depreciation_account_id, ''),
		       coalesce(depreciation_expense_account_id, ''),
		       is_deleted, created_at
		FROM asset_category WHERE is_deleted = false
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetCategory
	for rows.Next() {
		var c AssetCategory
		if err := rows.Scan(&c.ID, &c.Name, &c.DefaultDepreciationMethod, &c.TotalUsefulLifeMonths,
			&c.AssetAccountID, &c.AccumulatedDepreciationAccountID, &c.DepreciationExpenseAccountID,
			&c.IsDeleted, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete is a soft-delete — the asset table FK is RESTRICT, so a category
// in use can't be hard-removed without orphaning records. Setting is_deleted
// hides it from the list/dropdown but preserves history.
func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("asset_category: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Block delete if any asset still references it — otherwise reports
		// would silently drop those assets from category groupings.
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM asset WHERE asset_category_id = $1 AND docstatus <> 2`, id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("asset_category: %d active asset(s) still reference this category", count)
		}
		tag, err := tx.Exec(ctx, `UPDATE asset_category SET is_deleted = true WHERE id = $1 AND is_deleted = false`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("asset_category %s not found", id)
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

// ---- helpers ----

func validate(in AssetCategoryInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("asset_category.name: required")
	}
	if in.TotalUsefulLifeMonths <= 0 {
		return errors.New("asset_category.total_useful_life_months: must be > 0")
	}
	method := defaultMethod(in.DefaultDepreciationMethod)
	switch method {
	case "straight_line", "declining_balance", "written_down_value", "manual":
	default:
		return fmt.Errorf("asset_category.default_depreciation_method: invalid %q", method)
	}
	return nil
}

func defaultMethod(m string) string {
	if m == "" {
		return "straight_line"
	}
	return m
}

func load(ctx context.Context, tx pgx.Tx, id string) (*AssetCategory, error) {
	var c AssetCategory
	err := tx.QueryRow(ctx, `
		SELECT id, name, default_depreciation_method, total_useful_life_months,
		       coalesce(asset_account_id, ''),
		       coalesce(accumulated_depreciation_account_id, ''),
		       coalesce(depreciation_expense_account_id, ''),
		       is_deleted, created_at
		FROM asset_category WHERE id = $1`, id).
		Scan(&c.ID, &c.Name, &c.DefaultDepreciationMethod, &c.TotalUsefulLifeMonths,
			&c.AssetAccountID, &c.AccumulatedDepreciationAccountID, &c.DepreciationExpenseAccountID,
			&c.IsDeleted, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("asset_category %s not found", id)
	}
	return &c, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-asset-categories", Method: http.MethodGet,
		Path: "/assets/asset-categories", Summary: "List asset categories",
		Tags: []string{"Assets / Asset Category"},
	}, func(ctx context.Context, _ *struct{}) (*acListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		items, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &acListOut{Body: acListBody{Items: items}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-asset-category", Method: http.MethodPost,
		Path: "/assets/asset-categories", Summary: "Create an asset category",
		Tags: []string{"Assets / Asset Category"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *acCreateIn) (*acOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &acOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-asset-category", Method: http.MethodGet,
		Path: "/assets/asset-categories/{id}", Summary: "Get an asset category",
		Tags: []string{"Assets / Asset Category"},
	}, func(ctx context.Context, in *acGetIn) (*acOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &acOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-asset-category", Method: http.MethodPut,
		Path: "/assets/asset-categories/{id}", Summary: "Update an asset category",
		Tags: []string{"Assets / Asset Category"},
	}, func(ctx context.Context, in *acUpdateIn) (*acOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &acOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-asset-category", Method: http.MethodDelete,
		Path: "/assets/asset-categories/{id}", Summary: "Soft-delete an asset category",
		Tags: []string{"Assets / Asset Category"},
	}, func(ctx context.Context, in *acGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

type (
	acCreateIn struct{ Body AssetCategoryInput }
	acUpdateIn struct {
		ID   string `path:"id"`
		Body AssetCategoryInput
	}
	acGetIn struct {
		ID string `path:"id"`
	}
	acOut     struct{ Body AssetCategory }
	acListOut struct{ Body acListBody }
	acListBody struct {
		Items []AssetCategory `json:"items"`
	}
)
