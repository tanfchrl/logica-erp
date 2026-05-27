// Package bom implements the Bill of Materials master.
// Phase 4 MVP: single-level only — a multi-level structure is just BOMs chained
// where a child item itself has a BOM.
package bom

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const Doctype = "bom"

type BOM struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	CompanyID  string             `json:"company_id"`
	ItemID     string             `json:"item_id"`
	Quantity   decimal.Decimal    `json:"quantity"`
	UOM        string             `json:"uom"`
	IsActive   bool               `json:"is_active"`
	IsDefault  bool               `json:"is_default"`
	TotalCost  decimal.Decimal    `json:"total_cost"`
	Docstatus  submittable.Status `json:"docstatus"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Items      []BOMLine          `json:"items"`
}

type BOMLine struct {
	ID       string          `json:"id"`
	RowIndex int             `json:"row_index"`
	ItemID   string          `json:"item_id"`
	Qty      decimal.Decimal `json:"qty"`
	UOM      string          `json:"uom"`
	Rate     decimal.Decimal `json:"rate"`
	Amount   decimal.Decimal `json:"amount"`
}

type BOMCreateInput struct {
	CompanyID string         `json:"company_id,omitempty"`
	ItemID    string         `json:"item_id"`
	Quantity  string         `json:"quantity,omitempty"`
	UOM       string         `json:"uom,omitempty"`
	IsDefault bool           `json:"is_default,omitempty"`
	Items     []BOMLineInput `json:"items"`
}

type BOMLineInput struct {
	ItemID string `json:"item_id"`
	Qty    string `json:"qty"`
	UOM    string `json:"uom,omitempty"`
	Rate   string `json:"rate,omitempty"`
}

type Service struct {
	db        *dbx.DB
	Approvals approvalChecker
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) CreateDraft(ctx context.Context, in BOMCreateInput) (*BOM, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("bom: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" || in.ItemID == "" {
		return nil, errors.New("bom: company_id and item_id required")
	}
	if len(in.Items) == 0 {
		return nil, errors.New("bom.items: at least one required")
	}
	qty := decimal.NewFromInt(1)
	if in.Quantity != "" {
		q, err := decimal.NewFromString(strings.TrimSpace(in.Quantity))
		if err != nil || !q.IsPositive() {
			return nil, errors.New("bom.quantity: must be > 0")
		}
		qty = q
	}

	id := dbx.NewIDWithPrefix("bom")
	var out BOM
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, time.Now().UTC(), nil)
		if err != nil {
			return err
		}
		uom := in.UOM
		if uom == "" {
			if err := tx.QueryRow(ctx, `SELECT stock_uom FROM item WHERE id = $1`, in.ItemID).Scan(&uom); err != nil {
				return err
			}
		}
		total := decimal.Zero
		linesToInsert := make([]BOMLine, len(in.Items))
		for i, l := range in.Items {
			q, err := decimal.NewFromString(strings.TrimSpace(l.Qty))
			if err != nil || !q.IsPositive() {
				return fmt.Errorf("items[%d].qty: must be > 0", i)
			}
			rate := decimal.Zero
			if l.Rate != "" {
				rate, err = decimal.NewFromString(strings.TrimSpace(l.Rate))
				if err != nil {
					return fmt.Errorf("items[%d].rate: %w", i, err)
				}
			}
			lu := l.UOM
			if lu == "" {
				if err := tx.QueryRow(ctx, `SELECT stock_uom FROM item WHERE id = $1`, l.ItemID).Scan(&lu); err != nil {
					return err
				}
			}
			amount := q.Mul(rate).Round(money.Precision)
			total = total.Add(amount)
			linesToInsert[i] = BOMLine{
				ID: dbx.NewIDWithPrefix("bomi"), RowIndex: i + 1,
				ItemID: l.ItemID, Qty: q, UOM: lu, Rate: rate, Amount: amount,
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO bom (id, name, company_id, item_id, quantity, uom, is_default, total_cost, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
			id, name, in.CompanyID, in.ItemID, qty, uom, in.IsDefault, total, p.UserID); err != nil {
			return err
		}
		for _, l := range linesToInsert {
			if _, err := tx.Exec(ctx, `
				INSERT INTO bom_item (id, bom_id, row_index, item_id, qty, uom, rate, amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				l.ID, id, l.RowIndex, l.ItemID, l.Qty, l.UOM, l.Rate, l.Amount); err != nil {
				return err
			}
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

// BOMUpdateInput is the set of fields editable on a Draft BOM. company_id and
// item_id are immutable (they pin the doc to a finished good); the item rows
// are replaced wholesale on every update.
type BOMUpdateInput struct {
	Quantity  string         `json:"quantity,omitempty"`
	UOM       string         `json:"uom,omitempty"`
	IsDefault bool           `json:"is_default,omitempty"`
	Items     []BOMLineInput `json:"items"`
}

func (s *Service) Update(ctx context.Context, id string, in BOMUpdateInput) (*BOM, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("bom: unauthenticated")
	}
	if len(in.Items) == 0 {
		return nil, errors.New("bom.items: at least one required")
	}
	qty := decimal.NewFromInt(1)
	if in.Quantity != "" {
		q, err := decimal.NewFromString(strings.TrimSpace(in.Quantity))
		if err != nil || !q.IsPositive() {
			return nil, errors.New("bom.quantity: must be > 0")
		}
		qty = q
	}

	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Docstatus != submittable.Draft {
			return fmt.Errorf("bom: cannot edit (docstatus=%d)", existing.Docstatus)
		}
		uom := in.UOM
		if uom == "" {
			if err := tx.QueryRow(ctx, `SELECT stock_uom FROM item WHERE id = $1`, existing.ItemID).Scan(&uom); err != nil {
				return err
			}
		}
		total := decimal.Zero
		linesToInsert := make([]BOMLine, len(in.Items))
		for i, l := range in.Items {
			q, err := decimal.NewFromString(strings.TrimSpace(l.Qty))
			if err != nil || !q.IsPositive() {
				return fmt.Errorf("items[%d].qty: must be > 0", i)
			}
			rate := decimal.Zero
			if l.Rate != "" {
				rate, err = decimal.NewFromString(strings.TrimSpace(l.Rate))
				if err != nil {
					return fmt.Errorf("items[%d].rate: %w", i, err)
				}
			}
			lu := l.UOM
			if lu == "" {
				if err := tx.QueryRow(ctx, `SELECT stock_uom FROM item WHERE id = $1`, l.ItemID).Scan(&lu); err != nil {
					return err
				}
			}
			amount := q.Mul(rate).Round(money.Precision)
			total = total.Add(amount)
			linesToInsert[i] = BOMLine{
				ID: dbx.NewIDWithPrefix("bomi"), RowIndex: i + 1,
				ItemID: l.ItemID, Qty: q, UOM: lu, Rate: rate, Amount: amount,
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE bom SET
			  quantity   = $2,
			  uom        = $3,
			  is_default = $4,
			  total_cost = $5,
			  updated_by = $6
			WHERE id = $1 AND docstatus = 0`,
			id, qty, uom, in.IsDefault, total, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bom_item WHERE bom_id = $1`, id); err != nil {
			return err
		}
		for _, l := range linesToInsert {
			if _, err := tx.Exec(ctx, `
				INSERT INTO bom_item (id, bom_id, row_index, item_id, qty, uom, rate, amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				l.ID, id, l.RowIndex, l.ItemID, l.Qty, l.UOM, l.Rate, l.Amount); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Submit(ctx context.Context, id string) (*BOM, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("bom: unauthenticated")
	}
	var out BOM
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		b, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if b.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Approvals != nil {
			cost, _ := b.TotalCost.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, "bom", b.ID, b.Name, b.CompanyID,
				map[string]any{"total_cost": cost, "amount": cost}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bom SET docstatus = 1, submitted_at = now(), submitted_by = $1, updated_by = $1 WHERE id = $2`,
			p.UserID, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionSubmit, audit.Diff{}); err != nil {
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

func (s *Service) Get(ctx context.Context, id string) (*BOM, error) {
	var out *BOM
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		b, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = b
		return nil
	})
	return out, err
}

func load(ctx context.Context, tx pgx.Tx, id string) (*BOM, error) {
	var b BOM
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, item_id, quantity, uom, is_active, is_default, total_cost, docstatus, created_at, updated_at
		FROM bom WHERE id = $1`, id).
		Scan(&b.ID, &b.Name, &b.CompanyID, &b.ItemID, &b.Quantity, &b.UOM, &b.IsActive, &b.IsDefault,
			&b.TotalCost, &b.Docstatus, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("bom %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, row_index, item_id, qty, uom, rate, amount
		FROM bom_item WHERE bom_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l BOMLine
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.Qty, &l.UOM, &l.Rate, &l.Amount); err != nil {
			return nil, err
		}
		b.Items = append(b.Items, l)
	}
	return &b, rows.Err()
}

func pickSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `
		SELECT id, pattern FROM naming_series
		WHERE doctype = $1 AND is_default = true AND (company_id = $2 OR company_id IS NULL)
		ORDER BY company_id NULLS LAST LIMIT 1`, doctype, companyID).Scan(&id, &pat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no series for %s", doctype)
	}
	return id, pat, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "create-bom", Method: http.MethodPost,
		Path: "/manufacturing/boms", Summary: "Create a BOM draft",
		Tags: []string{"Manufacturing / BOM"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *bomCreateIn) (*bomOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		b, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &bomOut{Body: *b}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-bom", Method: http.MethodPut,
		Path: "/manufacturing/boms/{id}", Summary: "Update a BOM draft",
		Tags: []string{"Manufacturing / BOM"},
	}, func(ctx context.Context, in *bomUpdateIn) (*bomOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		b, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &bomOut{Body: *b}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-bom", Method: http.MethodPost,
		Path: "/manufacturing/boms/{id}/submit", Summary: "Submit a BOM",
		Tags: []string{"Manufacturing / BOM"},
	}, func(ctx context.Context, in *bomGetIn) (*bomOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		b, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &bomOut{Body: *b}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-bom", Method: http.MethodGet,
		Path: "/manufacturing/boms/{id}", Summary: "Get a BOM",
		Tags: []string{"Manufacturing / BOM"},
	}, func(ctx context.Context, in *bomGetIn) (*bomOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		b, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &bomOut{Body: *b}, nil
	})
}

type (
	bomCreateIn struct{ Body BOMCreateInput }
	bomOut      struct{ Body BOM }
	bomGetIn    struct {
		ID string `path:"id"`
	}
	bomUpdateIn struct {
		ID   string `path:"id"`
		Body BOMUpdateInput
	}
)
