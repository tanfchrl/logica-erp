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
	CompanyID     string `json:"company_id,omitempty"`
	Name          string `json:"name"`
	Code          string `json:"code,omitempty"`
	ParentID      string `json:"parent_id,omitempty"`
	IsGroup       bool   `json:"is_group,omitempty"`
	WarehouseType string `json:"warehouse_type,omitempty"`
	AccountID     string `json:"account_id,omitempty"`
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
		err := tx.QueryRow(ctx, `
			INSERT INTO warehouse (id, company_id, name, code, parent_id, is_group, warehouse_type, account_id, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
			RETURNING id, company_id, name, coalesce(code,''), coalesce(parent_id,''),
			          is_group, coalesce(warehouse_type,''), coalesce(account_id,''), created_at, updated_at`,
			id, in.CompanyID, in.Name, nullable(in.Code), nullable(in.ParentID), in.IsGroup,
			nullable(in.WarehouseType), nullable(in.AccountID), p.UserID).
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
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
