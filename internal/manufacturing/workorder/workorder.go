// Package workorder implements Work Order — the production document.
//
// Submit:
//   1. Scale the BOM by (qty / bom.quantity).
//   2. For each BOM line: consume raw material from source_warehouse (FIFO outgoing).
//   3. Receive finished item into target_warehouse, valued at the sum of raw costs / produced qty.
// All SLE + GL postings happen inside one tx with voucher_type='Work Order'.
//
// Phase 4 MVP: no routing, no workstation, no operations costing; pure material flow.
package workorder

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
	"github.com/tandigital/logica-erp/internal/platform/ledger"
	"github.com/tandigital/logica-erp/internal/platform/ledger/valuation"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const (
	Doctype     = "work_order"
	VoucherType = "Work Order"
)

type WorkOrder struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	CompanyID         string             `json:"company_id"`
	BOMID             string             `json:"bom_id"`
	ItemID            string             `json:"item_id"`
	Qty               decimal.Decimal    `json:"qty"`
	SourceWarehouseID string             `json:"source_warehouse_id"`
	TargetWarehouseID string             `json:"target_warehouse_id"`
	Status            string             `json:"status"`
	ProducedQty       decimal.Decimal    `json:"produced_qty"`
	TotalCost         decimal.Decimal    `json:"total_cost"`
	Docstatus         submittable.Status `json:"docstatus"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

type WorkOrderCreateInput struct {
	CompanyID         string `json:"company_id,omitempty"`
	BOMID             string `json:"bom_id"`
	Qty               string `json:"qty"`
	SourceWarehouseID string `json:"source_warehouse_id"`
	TargetWarehouseID string `json:"target_warehouse_id"`
}

type Service struct {
	db        *dbx.DB
	Approvals approvalChecker
	Workflow  workflowGate
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

type workflowGate interface {
	CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) CreateDraft(ctx context.Context, in WorkOrderCreateInput) (*WorkOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("work_order: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("work_order.company_id: required")
	}
	if in.BOMID == "" || in.SourceWarehouseID == "" || in.TargetWarehouseID == "" {
		return nil, errors.New("work_order: bom_id, source_warehouse_id, target_warehouse_id required")
	}
	qty, err := decimal.NewFromString(strings.TrimSpace(in.Qty))
	if err != nil || !qty.IsPositive() {
		return nil, errors.New("work_order.qty: must be > 0")
	}

	id := dbx.NewIDWithPrefix("wo")
	var out WorkOrder
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var (
			bomItem      string
			bomDocstatus int16
		)
		if err := tx.QueryRow(ctx,
			`SELECT item_id, docstatus FROM bom WHERE id = $1`, in.BOMID).
			Scan(&bomItem, &bomDocstatus); err != nil {
			return fmt.Errorf("bom: %w", err)
		}
		if bomDocstatus != 1 {
			return errors.New("work_order: BOM must be submitted")
		}
		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, time.Now().UTC(), nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO work_order (id, name, company_id, bom_id, item_id, qty, source_warehouse_id, target_warehouse_id, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
			id, name, in.CompanyID, in.BOMID, bomItem, qty, in.SourceWarehouseID, in.TargetWarehouseID, p.UserID); err != nil {
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

// WorkOrderUpdateInput holds editable Draft fields. company_id and item_id are
// immutable (item_id is denormalised from BOM at create time). bom_id may
// change as long as the new BOM is submitted and belongs to the same company.
type WorkOrderUpdateInput struct {
	BOMID             string `json:"bom_id,omitempty"`
	Qty               string `json:"qty"`
	SourceWarehouseID string `json:"source_warehouse_id"`
	TargetWarehouseID string `json:"target_warehouse_id"`
	PlannedStartDate  string `json:"planned_start_date,omitempty"`
	PlannedEndDate    string `json:"planned_end_date,omitempty"`
}

func (s *Service) Update(ctx context.Context, id string, in WorkOrderUpdateInput) (*WorkOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("work_order: unauthenticated")
	}
	if in.SourceWarehouseID == "" || in.TargetWarehouseID == "" {
		return nil, errors.New("work_order: source_warehouse_id, target_warehouse_id required")
	}
	qty, err := decimal.NewFromString(strings.TrimSpace(in.Qty))
	if err != nil || !qty.IsPositive() {
		return nil, errors.New("work_order.qty: must be > 0")
	}
	var plannedStart, plannedEnd *time.Time
	if in.PlannedStartDate != "" {
		t, err := time.Parse("2006-01-02", in.PlannedStartDate)
		if err != nil {
			return nil, fmt.Errorf("planned_start_date: %w", err)
		}
		plannedStart = &t
	}
	if in.PlannedEndDate != "" {
		t, err := time.Parse("2006-01-02", in.PlannedEndDate)
		if err != nil {
			return nil, fmt.Errorf("planned_end_date: %w", err)
		}
		plannedEnd = &t
	}

	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Docstatus != submittable.Draft {
			return fmt.Errorf("work_order: cannot edit (docstatus=%d)", existing.Docstatus)
		}
		bomID := in.BOMID
		itemID := existing.ItemID
		if bomID == "" {
			bomID = existing.BOMID
		}
		if bomID != existing.BOMID {
			var (
				bomItem      string
				bomDocstatus int16
			)
			if err := tx.QueryRow(ctx,
				`SELECT item_id, docstatus FROM bom WHERE id = $1`, bomID).
				Scan(&bomItem, &bomDocstatus); err != nil {
				return fmt.Errorf("bom: %w", err)
			}
			if bomDocstatus != 1 {
				return errors.New("work_order: BOM must be submitted")
			}
			itemID = bomItem
		}
		var startParam, endParam any
		if plannedStart != nil {
			startParam = *plannedStart
		}
		if plannedEnd != nil {
			endParam = *plannedEnd
		}
		if _, err := tx.Exec(ctx, `
			UPDATE work_order SET
			  bom_id              = $2,
			  item_id             = $3,
			  qty                 = $4,
			  source_warehouse_id = $5,
			  target_warehouse_id = $6,
			  planned_start_date  = $7,
			  planned_end_date    = $8,
			  updated_by          = $9
			WHERE id = $1 AND docstatus = 0`,
			id, bomID, itemID, qty, in.SourceWarehouseID, in.TargetWarehouseID,
			startParam, endParam, p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Submit(ctx context.Context, id string) (*WorkOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("work_order: unauthenticated")
	}
	var out WorkOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		wo, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if wo.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, "work_order"); err != nil {
				return err
			}
		}
		if s.Approvals != nil {
			cost, _ := wo.TotalCost.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, "work_order", wo.ID, wo.Name, wo.CompanyID,
				map[string]any{"total_cost": cost, "amount": cost}); err != nil {
				return err
			}
		}

		// BOM scale factor.
		var bomQty decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT quantity FROM bom WHERE id = $1`, wo.BOMID).Scan(&bomQty); err != nil {
			return err
		}
		if !bomQty.IsPositive() {
			return errors.New("work_order: BOM has zero quantity")
		}
		scale := wo.Qty.Div(bomQty)

		// Find fiscal year.
		var fyID string
		if err := tx.QueryRow(ctx, `
			SELECT fy.id FROM fiscal_year fy
			JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
			WHERE fyc.company_id = $1 AND now()::date BETWEEN fy.start_date AND fy.end_date
			ORDER BY fy.start_date DESC LIMIT 1`, wo.CompanyID).Scan(&fyID); err != nil {
			return fmt.Errorf("fiscal year: %w", err)
		}

		// Warehouse stock accounts.
		srcAcct, err := warehouseAccount(ctx, tx, wo.SourceWarehouseID)
		if err != nil || srcAcct == "" {
			return errors.New("work_order: source warehouse has no stock account configured")
		}
		tgtAcct, err := warehouseAccount(ctx, tx, wo.TargetWarehouseID)
		if err != nil || tgtAcct == "" {
			return errors.New("work_order: target warehouse has no stock account configured")
		}
		var srcCur, tgtCur string
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, srcAcct).Scan(&srcCur); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, tgtAcct).Scan(&tgtCur); err != nil {
			return err
		}

		// Consume each BOM line from source; sum the raw cost.
		postingDT := time.Now().UTC()
		totalCost := decimal.Zero
		entries := []ledger.Entry{}

		rows, err := tx.Query(ctx, `SELECT item_id, qty, uom FROM bom_item WHERE bom_id = $1 ORDER BY row_index`, wo.BOMID)
		if err != nil {
			return err
		}
		type rawConsume struct {
			itemID string
			qty    decimal.Decimal
			uom    string
		}
		var consumes []rawConsume
		for rows.Next() {
			var r rawConsume
			if err := rows.Scan(&r.itemID, &r.qty, &r.uom); err != nil {
				rows.Close()
				return err
			}
			r.qty = r.qty.Mul(scale)
			consumes = append(consumes, r)
		}
		rows.Close()

		for _, r := range consumes {
			rate, balQty, balVal, err := valuation.OutgoingRate(ctx, tx, r.itemID, wo.SourceWarehouseID, r.qty)
			if err != nil {
				return fmt.Errorf("consume %s: %w", r.itemID, err)
			}
			stockValue := r.qty.Mul(rate).Round(money.Precision)
			if err := insertSLE(ctx, tx, p.UserID, wo,
				r.itemID, wo.SourceWarehouseID, r.qty.Neg(), balQty, rate, balVal, stockValue.Neg(), nil, postingDT); err != nil {
				return err
			}
			totalCost = totalCost.Add(stockValue)
			entries = append(entries, ledger.Entry{
				AccountID:               srcAcct,
				Credit:                  stockValue,
				AccountCurrency:         srcCur,
				CreditInAccountCurrency: stockValue,
				Remarks:                 fmt.Sprintf("%s — consume %s", wo.Name, r.itemID),
			})
		}

		// Produce finished item at totalCost / wo.Qty per unit.
		var unitCost decimal.Decimal
		if wo.Qty.IsPositive() {
			unitCost = totalCost.Div(wo.Qty)
		}
		balQty, balVal, err := valuation.IncomingBalance(ctx, tx, wo.ItemID, wo.TargetWarehouseID, wo.Qty, unitCost)
		if err != nil {
			return err
		}
		if err := insertSLE(ctx, tx, p.UserID, wo,
			wo.ItemID, wo.TargetWarehouseID, wo.Qty, balQty, unitCost, balVal, totalCost, &unitCost, postingDT); err != nil {
			return err
		}
		entries = append(entries, ledger.Entry{
			AccountID:              tgtAcct,
			Debit:                  totalCost,
			AccountCurrency:        tgtCur,
			DebitInAccountCurrency: totalCost,
			Remarks:                fmt.Sprintf("%s — produce %s", wo.Name, wo.ItemID),
		})

		v := ledger.Voucher{
			Type: VoucherType, ID: wo.ID, Name: wo.Name,
			CompanyID: wo.CompanyID, PostingDate: postingDT, FiscalYearID: fyID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE work_order SET docstatus = 1, status = 'Completed', submitted_at = now(), submitted_by = $1,
			       produced_qty = qty, total_cost = $2, updated_by = $1
			WHERE id = $3`, p.UserID, totalCost, id); err != nil {
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

func (s *Service) Get(ctx context.Context, id string) (*WorkOrder, error) {
	var out *WorkOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		w, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = w
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]WorkOrder, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM work_order WHERE company_id = $1 ORDER BY created_at DESC LIMIT 200`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	out := make([]WorkOrder, 0, len(ids))
	for _, id := range ids {
		w, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, nil
}

func load(ctx context.Context, tx pgx.Tx, id string) (*WorkOrder, error) {
	var w WorkOrder
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, bom_id, item_id, qty, source_warehouse_id, target_warehouse_id,
		       status, produced_qty, total_cost, docstatus, created_at, updated_at
		FROM work_order WHERE id = $1`, id).
		Scan(&w.ID, &w.Name, &w.CompanyID, &w.BOMID, &w.ItemID, &w.Qty,
			&w.SourceWarehouseID, &w.TargetWarehouseID, &w.Status, &w.ProducedQty, &w.TotalCost,
			&w.Docstatus, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("work_order %s not found", id)
	}
	return &w, err
}

func insertSLE(ctx context.Context, tx pgx.Tx, userID string, wo *WorkOrder,
	itemID, warehouseID string, actualQty, qtyAfter, valRate, stockValAfter, stockValueDiff decimal.Decimal,
	incomingRate *decimal.Decimal, postingDT time.Time) error {
	id := dbx.NewIDWithPrefix("sle")
	var incoming any
	if incomingRate != nil {
		incoming = *incomingRate
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO stock_ledger_entry (
			id, company_id, posting_datetime, item_id, warehouse_id,
			actual_qty, qty_after_transaction, valuation_rate, stock_value, stock_value_difference,
			incoming_rate, voucher_type, voucher_id, voucher_name, created_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		id, wo.CompanyID, postingDT, itemID, warehouseID,
		actualQty, qtyAfter, valRate, stockValAfter, stockValueDiff,
		incoming, VoucherType, wo.ID, wo.Name, userID)
	return err
}

func warehouseAccount(ctx context.Context, tx pgx.Tx, warehouseID string) (string, error) {
	var acct *string
	if err := tx.QueryRow(ctx, `SELECT account_id FROM warehouse WHERE id = $1`, warehouseID).Scan(&acct); err != nil {
		return "", err
	}
	if acct == nil {
		return "", nil
	}
	return *acct, nil
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
		OperationID: "list-work-orders", Method: http.MethodGet,
		Path: "/manufacturing/work-orders", Summary: "List work orders",
		Tags: []string{"Manufacturing / Work Order"},
	}, func(ctx context.Context, _ *struct{}) (*woListOut, error) {
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
		return &woListOut{Body: woListBody{Items: ws}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-work-order", Method: http.MethodPost,
		Path: "/manufacturing/work-orders", Summary: "Create a Work Order draft",
		Tags: []string{"Manufacturing / Work Order"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *woCreateIn) (*woOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &woOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-work-order", Method: http.MethodPut,
		Path: "/manufacturing/work-orders/{id}", Summary: "Update a Work Order draft",
		Tags: []string{"Manufacturing / Work Order"},
	}, func(ctx context.Context, in *woUpdateIn) (*woOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &woOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-work-order", Method: http.MethodPost,
		Path: "/manufacturing/work-orders/{id}/submit", Summary: "Submit a Work Order (consume raw → produce finished)",
		Tags: []string{"Manufacturing / Work Order"},
	}, func(ctx context.Context, in *woGetIn) (*woOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &woOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-work-order", Method: http.MethodGet,
		Path: "/manufacturing/work-orders/{id}", Summary: "Get a Work Order",
		Tags: []string{"Manufacturing / Work Order"},
	}, func(ctx context.Context, in *woGetIn) (*woOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &woOut{Body: *w}, nil
	})
}

type (
	woCreateIn struct{ Body WorkOrderCreateInput }
	woOut      struct{ Body WorkOrder }
	woGetIn    struct {
		ID string `path:"id"`
	}
	woUpdateIn struct {
		ID   string `path:"id"`
		Body WorkOrderUpdateInput
	}
	woListOut  struct{ Body woListBody }
	woListBody struct {
		Items []WorkOrder `json:"items"`
	}
)
