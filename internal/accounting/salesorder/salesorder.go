// Package salesorder implements the Sales Order document — the
// commitment a seller makes to a customer. Unlike Sales Invoice, submit
// does NOT post to GL; SO is fulfilment-only state. GL impact happens
// when the linked Delivery Note (stock) and Sales Invoice (AR) submit.
//
// Status machine (mirrors purchaseorder, inverted for the sell side):
//
//	Draft  ──submit──▶  To Deliver and Bill  ──(delivered_qty==qty)──▶  To Bill
//	                             │
//	                             └─(billed_qty==qty)──▶  To Deliver
//	                             │
//	                             └─(both==qty)─────▶    Completed
//	                             │
//	                             └─cancel▶          Cancelled (docstatus=2)
//
// Auto-status-from-fulfilment is recomputed via RecomputeStatus when the
// downstream Delivery Note / Sales Invoice services update line counters.
// For v1 there's no DN service so submit just lands the SO at
// "To Deliver and Bill" and stays there until manually cancelled.
package salesorder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const Doctype = "sales_order"

// Status enum — stored in sales_order.status.
const (
	StatusDraft            = "Draft"
	StatusToDeliverAndBill = "To Deliver and Bill"
	StatusToBill           = "To Bill"
	StatusToDeliver        = "To Deliver"
	StatusCompleted        = "Completed"
	StatusCancelled        = "Cancelled"
)

// ---- domain types ----

type SalesOrder struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	CompanyID       string             `json:"company_id"`
	CustomerID      string             `json:"customer_id"`
	TransactionDate time.Time          `json:"transaction_date"`
	DeliveryDate    *time.Time         `json:"delivery_date,omitempty"`
	Currency        string             `json:"currency"`
	ExchangeRate    decimal.Decimal    `json:"exchange_rate"`
	Total           decimal.Decimal    `json:"total"`
	BaseTotal       decimal.Decimal    `json:"base_total"`
	Status          string             `json:"status"`
	Remarks         string             `json:"remarks,omitempty"`
	Docstatus       submittable.Status `json:"docstatus"`
	SubmittedAt     *time.Time         `json:"submitted_at,omitempty"`
	CancelledAt     *time.Time         `json:"cancelled_at,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Items           []SalesOrderLine   `json:"items"`
}

type SalesOrderLine struct {
	ID           string          `json:"id"`
	RowIndex     int             `json:"row_index"`
	ItemID       string          `json:"item_id,omitempty"`
	ItemCode     string          `json:"item_code"`
	ItemName     string          `json:"item_name"`
	Description  string          `json:"description,omitempty"`
	Qty          decimal.Decimal `json:"qty"`
	UOM          string          `json:"uom"`
	Rate         decimal.Decimal `json:"rate"`
	Amount       decimal.Decimal `json:"amount"`
	WarehouseID  string          `json:"warehouse_id,omitempty"`
	DeliveredQty decimal.Decimal `json:"delivered_qty"`
	BilledQty    decimal.Decimal `json:"billed_qty"`
}

// ---- input shapes ----

type SOCreateInput struct {
	CompanyID       string         `json:"company_id,omitempty"`
	CustomerID      string         `json:"customer_id"`
	TransactionDate string         `json:"transaction_date"`
	DeliveryDate    string         `json:"delivery_date,omitempty"`
	Currency        string         `json:"currency,omitempty"`
	ExchangeRate    string         `json:"exchange_rate,omitempty"`
	Remarks         string         `json:"remarks,omitempty"`
	Items           []SOLineInput  `json:"items"`
	CustomFields    map[string]any `json:"custom_fields,omitempty"`
}

type SOLineInput struct {
	ItemID      string `json:"item_id,omitempty"`
	ItemCode    string `json:"item_code,omitempty"`
	ItemName    string `json:"item_name,omitempty"`
	Description string `json:"description,omitempty"`
	Qty         string `json:"qty"`
	UOM         string `json:"uom,omitempty"`
	Rate        string `json:"rate"`
	WarehouseID string `json:"warehouse_id,omitempty"`
}

// ---- Service ----

type Service struct {
	db        *dbx.DB
	Approvals approvalChecker
	Workflow  workflowGate
	// Notifier is optional. When set, Submit() fires `so.submitted` after the
	// document transitions to docstatus 1.
	Notifier notifier
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

type workflowGate interface {
	CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error
}

type notifier interface {
	Fire(eventKey string, payload map[string]any)
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in SOCreateInput) (*SalesOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("sales_order: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("sales_order.company_id: required")
	}
	if in.CustomerID == "" {
		return nil, errors.New("sales_order.customer_id: required")
	}
	td, err := time.Parse("2006-01-02", in.TransactionDate)
	if err != nil {
		return nil, fmt.Errorf("sales_order.transaction_date: %w", err)
	}
	var deliveryDate *time.Time
	if in.DeliveryDate != "" {
		t, err := time.Parse("2006-01-02", in.DeliveryDate)
		if err != nil {
			return nil, fmt.Errorf("sales_order.delivery_date: %w", err)
		}
		deliveryDate = &t
	}
	if len(in.Items) == 0 {
		return nil, errors.New("sales_order.items: at least one required")
	}

	// Currency + FX defaults — match customer's currency on the company
	// when unspecified.
	currency := in.Currency
	exchangeRate := decimal.NewFromInt(1)
	if in.ExchangeRate != "" {
		ex, err := decimal.NewFromString(in.ExchangeRate)
		if err != nil {
			return nil, fmt.Errorf("sales_order.exchange_rate: %w", err)
		}
		if !ex.IsPositive() {
			return nil, errors.New("sales_order.exchange_rate: must be > 0")
		}
		exchangeRate = ex
	}

	id := dbx.NewIDWithPrefix("so")
	var out SalesOrder
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Resolve customer's currency on this company so we don't ship
		// SOs in the wrong currency by accident.
		if currency == "" {
			var custCur *string
			if err := tx.QueryRow(ctx, `
				SELECT default_currency FROM customer_default
				WHERE customer_id = $1 AND company_id = $2`, in.CustomerID, in.CompanyID).Scan(&custCur); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if custCur != nil && *custCur != "" {
				currency = *custCur
			}
		}
		if currency == "" {
			// fall back to company default
			if err := tx.QueryRow(ctx,
				`SELECT default_currency FROM company WHERE id = $1`, in.CompanyID).Scan(&currency); err != nil {
				return err
			}
		}

		seriesID, pattern, err := pickNamingSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, td, nil)
		if err != nil {
			return err
		}
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}

		draftLines := make([]SalesOrderLine, len(in.Items))
		total := decimal.Zero
		for i, ln := range in.Items {
			qty, err := parseDec(ln.Qty)
			if err != nil {
				return fmt.Errorf("items[%d].qty: %w", i, err)
			}
			if !qty.IsPositive() {
				return fmt.Errorf("items[%d].qty: must be > 0", i)
			}
			rt, err := parseDec(ln.Rate)
			if err != nil {
				return fmt.Errorf("items[%d].rate: %w", i, err)
			}
			if rt.IsNegative() {
				return fmt.Errorf("items[%d].rate: must be >= 0", i)
			}
			amount := qty.Mul(rt).Round(money.Precision)

			itemCode, itemName, uom := ln.ItemCode, ln.ItemName, ln.UOM
			if ln.ItemID != "" {
				var code, nm, stockUOM string
				if err := tx.QueryRow(ctx,
					`SELECT code, name, stock_uom FROM item WHERE id = $1 AND is_deleted = false`, ln.ItemID).
					Scan(&code, &nm, &stockUOM); err != nil {
					return fmt.Errorf("items[%d]: item %s not found: %w", i, ln.ItemID, err)
				}
				if itemCode == "" {
					itemCode = code
				}
				if itemName == "" {
					itemName = nm
				}
				if uom == "" {
					uom = stockUOM
				}
			}
			if itemCode == "" {
				return fmt.Errorf("items[%d].item_code: required for free-text lines", i)
			}
			if itemName == "" {
				itemName = itemCode
			}
			if uom == "" {
				uom = "Unit"
			}
			rowID := dbx.NewIDWithPrefix("soi")
			draftLines[i] = SalesOrderLine{
				ID: rowID, RowIndex: i + 1,
				ItemID: ln.ItemID, ItemCode: itemCode, ItemName: itemName, Description: ln.Description,
				Qty: qty, UOM: uom, Rate: rt, Amount: amount,
				WarehouseID: ln.WarehouseID,
			}
			total = total.Add(amount)
		}
		baseTotal := total.Mul(exchangeRate).Round(money.Precision)

		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_order (
				id, name, company_id, customer_id, transaction_date, delivery_date,
				currency, exchange_rate, total, base_total, status, remarks,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)`,
			id, name, in.CompanyID, in.CustomerID, td, nullableTime(deliveryDate),
			currency, exchangeRate, total, baseTotal, StatusDraft, nullable(in.Remarks),
			cf, p.UserID); err != nil {
			return err
		}
		for _, l := range draftLines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_order_item (
					id, sales_order_id, row_index, item_id, item_code, item_name, description,
					qty, uom, rate, amount, warehouse_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
				l.ID, id, l.RowIndex, nullable(l.ItemID), l.ItemCode, l.ItemName, nullable(l.Description),
				l.Qty, l.UOM, l.Rate, l.Amount, nullable(l.WarehouseID)); err != nil {
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

// ---- Submit ----

func (s *Service) Submit(ctx context.Context, id string) (*SalesOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("sales_order: unauthenticated")
	}
	var out SalesOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		so, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if so.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if so.Total.IsZero() {
			return errors.New("sales_order: total is zero")
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, Doctype); err != nil {
				return err
			}
		}
		if s.Approvals != nil {
			gt, _ := so.Total.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, Doctype, so.ID, so.Name, so.CompanyID,
				map[string]any{"total": gt}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sales_order
			SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			    status = $2, updated_by = $1
			WHERE id = $3`, p.UserID, StatusToDeliverAndBill, id); err != nil {
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
	if err == nil && s.Notifier != nil {
		t, _ := out.Total.Float64()
		s.Notifier.Fire("so.submitted", map[string]any{
			"company_id":    out.CompanyID,
			"doctype":       Doctype,
			"document_id":   out.ID,
			"document_name": out.Name,
			"total":         t,
			"summary":       fmt.Sprintf("Sales order %s submitted", out.Name),
			"SO":            out,
		})
	}
	return &out, err
}

// ---- Cancel ----

func (s *Service) Cancel(ctx context.Context, id string) (*SalesOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("sales_order: unauthenticated")
	}
	var out SalesOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		so, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if so.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		// Downstream-fulfilment guard: refuse if any line already has a
		// linked Delivery Note or Sales Invoice consumed it.
		if hasDownstreamFulfilment(so) {
			return errors.New("sales_order: cannot cancel — deliveries or invoices reference this SO; cancel those first")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sales_order
			SET docstatus = 2, cancelled_at = now(), cancelled_by = $1,
			    status = $2, updated_by = $1
			WHERE id = $3`, p.UserID, StatusCancelled, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCancel, audit.Diff{}); err != nil {
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

// ---- Read ----

func (s *Service) Get(ctx context.Context, id string) (*SalesOrder, error) {
	var out *SalesOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		so, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = so
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]SalesOrder, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM sales_order
		WHERE company_id = $1
		ORDER BY transaction_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]SalesOrder, 0, len(ids))
	for _, id := range ids {
		so, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *so)
	}
	return out, nil
}

// ---- Pure helpers ----

// RecomputeStatus returns the status an SO should hold based on its
// fulfilment counters. Manual (Cancelled) and pre-submit (Draft) states are
// returned unchanged. Wired by future Delivery Note / Sales Invoice services
// that mutate delivered_qty / billed_qty on the lines.
func RecomputeStatus(so *SalesOrder) string {
	if so.Status == StatusDraft || so.Status == StatusCancelled {
		return so.Status
	}
	allDelivered := true
	allBilled := true
	for _, l := range so.Items {
		if l.DeliveredQty.LessThan(l.Qty) {
			allDelivered = false
		}
		if l.BilledQty.LessThan(l.Qty) {
			allBilled = false
		}
	}
	switch {
	case allDelivered && allBilled:
		return StatusCompleted
	case allDelivered:
		return StatusToBill
	case allBilled:
		return StatusToDeliver
	default:
		return StatusToDeliverAndBill
	}
}

func hasDownstreamFulfilment(so *SalesOrder) bool {
	for _, l := range so.Items {
		if !l.DeliveredQty.IsZero() || !l.BilledQty.IsZero() {
			return true
		}
	}
	return false
}

// ---- load / DB helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*SalesOrder, error) {
	var (
		so                                            SalesOrder
		deliveryDate, submittedAt, cancelledAt        *time.Time
		remarks                                       *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, customer_id, transaction_date, delivery_date,
		       currency, exchange_rate, total, base_total, status, remarks,
		       docstatus, submitted_at, cancelled_at,
		       created_at, updated_at
		FROM sales_order WHERE id = $1`, id).
		Scan(&so.ID, &so.Name, &so.CompanyID, &so.CustomerID, &so.TransactionDate, &deliveryDate,
			&so.Currency, &so.ExchangeRate, &so.Total, &so.BaseTotal, &so.Status, &remarks,
			&so.Docstatus, &submittedAt, &cancelledAt,
			&so.CreatedAt, &so.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("sales_order %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	so.DeliveryDate = deliveryDate
	so.SubmittedAt = submittedAt
	so.CancelledAt = cancelledAt
	if remarks != nil {
		so.Remarks = *remarks
	}

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, coalesce(item_id,''), item_code, item_name, coalesce(description,''),
		       qty, uom, rate, amount, coalesce(warehouse_id,''),
		       delivered_qty, billed_qty
		FROM sales_order_item WHERE sales_order_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l SalesOrderLine
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.ItemCode, &l.ItemName, &l.Description,
			&l.Qty, &l.UOM, &l.Rate, &l.Amount, &l.WarehouseID,
			&l.DeliveredQty, &l.BilledQty); err != nil {
			return nil, err
		}
		so.Items = append(so.Items, l)
	}
	return &so, nil
}

func parseDec(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

func pickNamingSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `
		SELECT id, pattern FROM naming_series
		WHERE doctype = $1 AND is_default = true AND (company_id = $2 OR company_id IS NULL)
		ORDER BY company_id NULLS LAST LIMIT 1`, doctype, companyID).Scan(&id, &pat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no default naming series for %s", doctype)
	}
	return id, pat, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
