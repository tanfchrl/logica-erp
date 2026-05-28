// Package warehouse implements the Warehouse master (per-company, tree).
package warehouse

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
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "warehouse"

type Warehouse struct {
	ID            string    `json:"id"`
	CompanyID     string    `json:"company_id"`
	Name          string    `json:"name"`
	Code          string    `json:"code,omitempty"`
	ParentID      string    `json:"parent_id,omitempty"`
	IsGroup       bool      `json:"is_group"`
	WarehouseType string    `json:"warehouse_type,omitempty"`
	AccountID     string    `json:"account_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WarehouseCreateInput struct {
	CompanyID     string         `json:"company_id,omitempty"`
	Name          string         `json:"name"`
	Code          string         `json:"code,omitempty"`
	ParentID      string         `json:"parent_id,omitempty"`
	IsGroup       bool           `json:"is_group,omitempty"`
	WarehouseType string         `json:"warehouse_type,omitempty"`
	AccountID     string         `json:"account_id,omitempty"`
	CustomFields  map[string]any `json:"custom_fields,omitempty"`
}

// WarehouseUpdateInput allows editing a warehouse's mutable fields including
// tree position. `code` and `company_id` remain immutable (code is a stable
// external lookup key; warehouses are company-scoped for life).
//
// Tree position: parent_id may be re-parented; the new parent must belong to
// the same company, must be is_group=true, and must not be the warehouse
// itself or any descendant (cycle guard). is_group can be toggled freely
// except group→leaf is rejected when children exist.
//
// The lft/rgt columns on the schema are currently unused (no traversal logic
// reads them), so parent_id changes do not require a nested-set rebuild.
type WarehouseUpdateInput struct {
	Name          string         `json:"name"`
	ParentID      *string        `json:"parent_id,omitempty"`
	IsGroup       *bool          `json:"is_group,omitempty"`
	WarehouseType string         `json:"warehouse_type,omitempty"`
	AccountID     string         `json:"account_id,omitempty"`
	CustomFields  map[string]any `json:"custom_fields,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in WarehouseCreateInput) (*Warehouse, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("warehouse: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("warehouse.company_id: required")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("warehouse.name: required")
	}

	id := dbx.NewIDWithPrefix("wh")
	var w Warehouse
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO warehouse (id, company_id, name, code, parent_id, is_group, warehouse_type, account_id, custom_fields, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
			RETURNING id, company_id, name, coalesce(code,''), coalesce(parent_id,''),
			          is_group, coalesce(warehouse_type,''), coalesce(account_id,''), created_at, updated_at`,
			id, in.CompanyID, in.Name, nullable(in.Code), nullable(in.ParentID), in.IsGroup,
			nullable(in.WarehouseType), nullable(in.AccountID), cf, p.UserID).
			Scan(&w.ID, &w.CompanyID, &w.Name, &w.Code, &w.ParentID, &w.IsGroup, &w.WarehouseType, &w.AccountID, &w.CreatedAt, &w.UpdatedAt)
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("warehouse: duplicate name in company")
			}
			return err
		}
		return audit.Record(ctx, tx, Doctype, w.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &w, err
}

// Update edits a warehouse's mutable fields. See WarehouseUpdateInput for
// the tree-move guards.
func (s *Service) Update(ctx context.Context, id string, in WarehouseUpdateInput) (*Warehouse, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("warehouse: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("warehouse.name: required")
	}

	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Load current row (company, is_group, parent_id) under the tx so the
		// tree-move guards see a consistent snapshot.
		var (
			companyID    string
			curParent    *string
			curIsGroup   bool
		)
		if err := tx.QueryRow(ctx, `
			SELECT company_id, parent_id, is_group FROM warehouse
			WHERE id = $1 AND is_deleted = false`, id).Scan(&companyID, &curParent, &curIsGroup); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("warehouse %s not found", id)
			}
			return err
		}

		newIsGroup := curIsGroup
		if in.IsGroup != nil {
			newIsGroup = *in.IsGroup
		}

		// Determine target parent. nil → keep; pointer to "" → detach (root).
		newParent := curParent
		if in.ParentID != nil {
			if *in.ParentID == "" {
				newParent = nil
			} else {
				np := *in.ParentID
				newParent = &np
			}
		}

		if newParent != nil {
			if *newParent == id {
				return errors.New("warehouse.parent_id: cannot parent to self")
			}
			var (
				parentCompany string
				parentIsGroup bool
			)
			if err := tx.QueryRow(ctx, `
				SELECT company_id, is_group FROM warehouse
				WHERE id = $1 AND is_deleted = false`, *newParent).Scan(&parentCompany, &parentIsGroup); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("warehouse.parent_id: %s not found", *newParent)
				}
				return err
			}
			if parentCompany != companyID {
				return errors.New("warehouse.parent_id: must be in same company")
			}
			if !parentIsGroup {
				return errors.New("warehouse.parent_id: target must be a group")
			}
			// Cycle guard: walk ancestors of the target and ensure we don't
			// hit `id`. Tree depth is small; recursive CTE is overkill.
			cursor := *newParent
			for hop := 0; hop < 64; hop++ {
				if cursor == id {
					return errors.New("warehouse.parent_id: move would create a cycle")
				}
				var next *string
				if err := tx.QueryRow(ctx, `SELECT parent_id FROM warehouse WHERE id = $1`, cursor).Scan(&next); err != nil {
					return err
				}
				if next == nil {
					break
				}
				cursor = *next
			}
		}

		// Group → leaf flip with existing children is rejected.
		if curIsGroup && !newIsGroup {
			var n int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM warehouse
				WHERE parent_id = $1 AND is_deleted = false`, id).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return fmt.Errorf("warehouse.is_group: cannot convert group to leaf while %d child warehouse(s) exist", n)
			}
		}

		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE warehouse SET
			  name = $2,
			  parent_id = $3,
			  is_group = $4,
			  warehouse_type = $5,
			  account_id = $6,
			  custom_fields = $7,
			  updated_by = $8,
			  updated_at = now()
			WHERE id = $1 AND is_deleted = false`,
			id, in.Name, newParent, newIsGroup,
			nullable(in.WarehouseType), nullable(in.AccountID), cf, p.UserID); err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("warehouse: duplicate name in company")
			}
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, companyID string) ([]Warehouse, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, name, coalesce(code,''), coalesce(parent_id,''),
		       is_group, coalesce(warehouse_type,''), coalesce(account_id,''), created_at, updated_at
		FROM warehouse WHERE company_id = $1 AND is_deleted = false ORDER BY name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Warehouse
	for rows.Next() {
		var w Warehouse
		if err := rows.Scan(&w.ID, &w.CompanyID, &w.Name, &w.Code, &w.ParentID, &w.IsGroup,
			&w.WarehouseType, &w.AccountID, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (*Warehouse, error) {
	var w Warehouse
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, name, coalesce(code,''), coalesce(parent_id,''),
		       is_group, coalesce(warehouse_type,''), coalesce(account_id,''), created_at, updated_at
		FROM warehouse WHERE id = $1`, id).
		Scan(&w.ID, &w.CompanyID, &w.Name, &w.Code, &w.ParentID, &w.IsGroup,
			&w.WarehouseType, &w.AccountID, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("warehouse %s not found", id)
	}
	return &w, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-warehouses", Method: http.MethodGet,
		Path: "/stock/warehouses", Summary: "List warehouses",
		Tags: []string{"Stock / Warehouse"},
	}, func(ctx context.Context, _ *struct{}) (*whListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ws, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whListOut{Body: whListBody{Items: ws}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-warehouse", Method: http.MethodPost,
		Path: "/stock/warehouses", Summary: "Create a warehouse",
		Tags: []string{"Stock / Warehouse"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *whCreateIn) (*whOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-warehouse", Method: http.MethodGet,
		Path: "/stock/warehouses/{id}", Summary: "Get a warehouse",
		Tags: []string{"Stock / Warehouse"},
	}, func(ctx context.Context, in *whGetIn) (*whOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-warehouse", Method: http.MethodPut,
		Path: "/stock/warehouses/{id}", Summary: "Update a warehouse",
		Tags: []string{"Stock / Warehouse"},
	}, func(ctx context.Context, in *whUpdateIn) (*whOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &whOut{Body: *w}, nil
	})
}

type (
	whCreateIn struct{ Body WarehouseCreateInput }
	whOut      struct{ Body Warehouse }
	whListOut  struct{ Body whListBody }
	whListBody struct {
		Items []Warehouse `json:"items"`
	}
	whGetIn struct {
		ID string `path:"id"`
	}
	whUpdateIn struct {
		ID   string `path:"id"`
		Body WarehouseUpdateInput
	}
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
