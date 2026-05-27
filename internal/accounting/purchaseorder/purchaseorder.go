// Package purchaseorder implements the Purchase Order document — the
// commitment a buyer makes to a supplier. Unlike Purchase Invoice, submit
// does NOT post to GL; PO is fulfilment-only state. GL impact happens when
// the linked Purchase Receipt (stock) and Purchase Invoice (AP) are
// submitted.
//
// Status machine (Buying §4 in the gap audit):
//
//	Draft  ──submit──▶  To Receive and Bill  ──(received_qty==qty)──▶  To Bill
//	                            │
//	                            └─(billed_qty==qty)──▶  To Receive
//	                            │
//	                            └─(both==qty)──────▶    Completed
//	                            │
//	                            ├─hold──▶  On Hold   ──unhold──▶  (prior status)
//	                            ├─close─▶  Closed    ──reopen──▶  (recomputed)
//	                            ├─stop──▶  Stopped   ──reopen──▶  (recomputed)
//	                            └─cancel▶  Cancelled (terminal — docstatus=2)
//
// Hold/Close/Stop don't mutate fulfilment counters; reopen recomputes status
// from received_qty / billed_qty.
package purchaseorder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/accounting/taxtemplate"
	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
	"github.com/tandigital/logica-erp/internal/platform/tax"
)

const (
	Doctype = "purchase_order"
)

// Status values — string enum stored in purchase_order.status. Kept as
// constants here so callers and tests don't carry stringly-typed knowledge.
const (
	StatusDraft             = "Draft"
	StatusToReceiveAndBill  = "To Receive and Bill"
	StatusToBill            = "To Bill"
	StatusToReceive         = "To Receive"
	StatusCompleted         = "Completed"
	StatusOnHold            = "On Hold"
	StatusClosed            = "Closed"
	StatusStopped           = "Stopped"
	StatusCancelled         = "Cancelled"
)

// ---- domain types ----

type PurchaseOrder struct {
	ID                       string             `json:"id"`
	Name                     string             `json:"name"`
	CompanyID                string             `json:"company_id"`
	SupplierID               string             `json:"supplier_id"`
	TransactionDate          time.Time          `json:"transaction_date"`
	RequiredByDate           *time.Time         `json:"required_by_date,omitempty"`
	FiscalYearID             string             `json:"fiscal_year_id,omitempty"`
	Currency                 string             `json:"currency"`
	ExchangeRate             decimal.Decimal    `json:"exchange_rate"`
	TaxTemplateID            string             `json:"tax_template_id,omitempty"`
	NetTotal                 decimal.Decimal    `json:"net_total"`
	TotalTaxesAndCharges     decimal.Decimal    `json:"total_taxes_and_charges"`
	GrandTotal               decimal.Decimal    `json:"grand_total"`
	BaseNetTotal             decimal.Decimal    `json:"base_net_total"`
	BaseTotalTaxesAndCharges decimal.Decimal    `json:"base_total_taxes_and_charges"`
	BaseGrandTotal           decimal.Decimal    `json:"base_grand_total"`
	Status                   string             `json:"status"`
	Remarks                  string             `json:"remarks,omitempty"`
	TermsAndConditions       string             `json:"terms_and_conditions,omitempty"`
	PaymentTerms             string             `json:"payment_terms,omitempty"`
	LetterheadID             string             `json:"letterhead_id,omitempty"`
	Docstatus                submittable.Status `json:"docstatus"`
	SubmittedAt              *time.Time         `json:"submitted_at,omitempty"`
	CancelledAt              *time.Time         `json:"cancelled_at,omitempty"`
	HeldAt                   *time.Time         `json:"held_at,omitempty"`
	ClosedAt                 *time.Time         `json:"closed_at,omitempty"`
	StoppedAt                *time.Time         `json:"stopped_at,omitempty"`
	CreatedAt                time.Time          `json:"created_at"`
	UpdatedAt                time.Time          `json:"updated_at"`
	Items                    []PurchaseOrderLine `json:"items"`
	Taxes                    []PurchaseOrderTaxRow `json:"taxes"`
}

type PurchaseOrderLine struct {
	ID              string          `json:"id"`
	RowIndex        int             `json:"row_index"`
	ItemID          string          `json:"item_id,omitempty"`
	ItemCode        string          `json:"item_code"`
	ItemName        string          `json:"item_name"`
	Description     string          `json:"description,omitempty"`
	Qty             decimal.Decimal `json:"qty"`
	UOM             string          `json:"uom"`
	Rate            decimal.Decimal `json:"rate"`
	Amount          decimal.Decimal `json:"amount"`
	WarehouseID     string          `json:"warehouse_id,omitempty"`
	CostCenterID    string          `json:"cost_center_id,omitempty"`
	RequiredByDate  *time.Time      `json:"required_by_date,omitempty"`
	TaxAmount       decimal.Decimal `json:"tax_amount"`
	Total           decimal.Decimal `json:"total"`
	BaseAmount      decimal.Decimal `json:"base_amount"`
	BaseTaxAmount   decimal.Decimal `json:"base_tax_amount"`
	BaseTotal       decimal.Decimal `json:"base_total"`
	ReceivedQty     decimal.Decimal `json:"received_qty"`
	BilledQty       decimal.Decimal `json:"billed_qty"`
}

type PurchaseOrderTaxRow struct {
	ID                  string          `json:"id"`
	RowIndex            int             `json:"row_index"`
	AccountID           string          `json:"account_id"`
	Description         string          `json:"description"`
	Rate                decimal.Decimal `json:"rate"`
	ChargeType          string          `json:"charge_type"`
	IncludedInBasicRate bool            `json:"included_in_basic_rate"`
	TaxAmount           decimal.Decimal `json:"tax_amount"`
	BaseTaxAmount       decimal.Decimal `json:"base_tax_amount"`
	CostCenterID        string          `json:"cost_center_id,omitempty"`
}

// ---- input shapes ----

type CreateInput struct {
	CompanyID          string                  `json:"company_id,omitempty"`
	SupplierID         string                  `json:"supplier_id"`
	TransactionDate    string                  `json:"transaction_date"`
	RequiredByDate     string                  `json:"required_by_date,omitempty"`
	Currency           string                  `json:"currency,omitempty"`
	ExchangeRate       string                  `json:"exchange_rate,omitempty"`
	TaxTemplateID      string                  `json:"tax_template_id,omitempty"`
	Remarks            string                  `json:"remarks,omitempty"`
	TermsAndConditions string                  `json:"terms_and_conditions,omitempty"`
	PaymentTerms       string                  `json:"payment_terms,omitempty"`
	LetterheadID       string                  `json:"letterhead_id,omitempty"`
	Items              []LineInput             `json:"items"`
	CustomFields       map[string]any          `json:"custom_fields,omitempty"`
}

type LineInput struct {
	ItemID         string `json:"item_id,omitempty"`
	ItemCode       string `json:"item_code,omitempty"`
	ItemName       string `json:"item_name,omitempty"`
	Description    string `json:"description,omitempty"`
	Qty            string `json:"qty"`
	UOM            string `json:"uom,omitempty"`
	Rate           string `json:"rate"`
	WarehouseID    string `json:"warehouse_id,omitempty"`
	CostCenterID   string `json:"cost_center_id,omitempty"`
	RequiredByDate string `json:"required_by_date,omitempty"`
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

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in CreateInput) (*PurchaseOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_order: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("purchase_order.company_id: required")
	}
	if in.SupplierID == "" {
		return nil, errors.New("purchase_order.supplier_id: required")
	}
	td, err := time.Parse("2006-01-02", in.TransactionDate)
	if err != nil {
		return nil, fmt.Errorf("purchase_order.transaction_date: %w", err)
	}
	var requiredBy *time.Time
	if in.RequiredByDate != "" {
		t, err := time.Parse("2006-01-02", in.RequiredByDate)
		if err != nil {
			return nil, fmt.Errorf("purchase_order.required_by_date: %w", err)
		}
		requiredBy = &t
	}
	if len(in.Items) == 0 {
		return nil, errors.New("purchase_order.items: at least one required")
	}

	rate := decimal.NewFromInt(1)
	if in.ExchangeRate != "" {
		r, err := decimal.NewFromString(in.ExchangeRate)
		if err != nil {
			return nil, fmt.Errorf("purchase_order.exchange_rate: %w", err)
		}
		if !r.IsPositive() {
			return nil, errors.New("purchase_order.exchange_rate: must be > 0")
		}
		rate = r
	}

	id := dbx.NewIDWithPrefix("po")
	var out PurchaseOrder
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		currency := in.Currency
		if currency == "" {
			if err := tx.QueryRow(ctx, `
				SELECT coalesce(NULLIF(sd.default_currency,''),
				       NULLIF(s.default_currency,''),
				       co.default_currency)
				FROM company co
				LEFT JOIN supplier s            ON s.id = $2
				LEFT JOIN supplier_default sd   ON sd.supplier_id = s.id AND sd.company_id = co.id
				WHERE co.id = $1`, in.CompanyID, in.SupplierID).Scan(&currency); err != nil {
				return fmt.Errorf("resolve currency: %w", err)
			}
		}

		taxTemplateID := in.TaxTemplateID
		if taxTemplateID == "" {
			_ = tx.QueryRow(ctx,
				`SELECT default_tax_template_id FROM supplier_default WHERE supplier_id = $1 AND company_id = $2`,
				in.SupplierID, in.CompanyID).Scan(&taxTemplateID)
		}

		fyID, err := pickFiscalYear(ctx, tx, in.CompanyID, td)
		if err != nil {
			return err
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

		draftLines := make([]PurchaseOrderLine, len(in.Items))
		calcLines := make([]tax.Line, len(in.Items))
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
			var lineRequiredBy *time.Time
			if ln.RequiredByDate != "" {
				t, err := time.Parse("2006-01-02", ln.RequiredByDate)
				if err != nil {
					return fmt.Errorf("items[%d].required_by_date: %w", i, err)
				}
				lineRequiredBy = &t
			}
			rowID := dbx.NewIDWithPrefix("poi")
			draftLines[i] = PurchaseOrderLine{
				ID: rowID, RowIndex: i + 1,
				ItemID: ln.ItemID, ItemCode: itemCode, ItemName: itemName, Description: ln.Description,
				Qty: qty, UOM: uom, Rate: rt, Amount: amount,
				WarehouseID: ln.WarehouseID, CostCenterID: ln.CostCenterID,
				RequiredByDate: lineRequiredBy,
			}
			calcLines[i] = tax.Line{Key: rowID, NetAmount: amount}
		}

		var taxResult tax.Result
		if taxTemplateID != "" {
			tpl, err := taxtemplate.LoadForCalc(ctx, tx, taxTemplateID)
			if err != nil {
				return err
			}
			if tpl.IsSales {
				return errors.New("purchase_order.tax_template: must be a purchase template")
			}
			taxResult, err = tax.Calculate(calcLines, tpl)
			if err != nil {
				return err
			}
		} else {
			taxResult = tax.Result{Lines: make([]tax.LineResult, len(calcLines))}
			netTotal := decimal.Zero
			for i, l := range calcLines {
				taxResult.Lines[i] = tax.LineResult{Key: l.Key, NetAmount: l.NetAmount, Total: l.NetAmount}
				netTotal = netTotal.Add(l.NetAmount)
			}
			taxResult.NetTotal = netTotal
			taxResult.GrandTotal = netTotal
		}

		taxByKey := map[string]decimal.Decimal{}
		for _, lr := range taxResult.Lines {
			taxByKey[lr.Key] = lr.TaxAmount
		}
		for i := range draftLines {
			t := taxByKey[draftLines[i].ID]
			draftLines[i].TaxAmount = t
			draftLines[i].Total = draftLines[i].Amount.Add(t)
			draftLines[i].BaseAmount = draftLines[i].Amount.Mul(rate).Round(money.Precision)
			draftLines[i].BaseTaxAmount = t.Mul(rate).Round(money.Precision)
			draftLines[i].BaseTotal = draftLines[i].BaseAmount.Add(draftLines[i].BaseTaxAmount)
		}

		taxRows := make([]PurchaseOrderTaxRow, len(taxResult.TaxRows))
		for i, tr := range taxResult.TaxRows {
			taxRows[i] = PurchaseOrderTaxRow{
				ID: dbx.NewIDWithPrefix("pot"), RowIndex: i + 1,
				AccountID: tr.AccountID, Description: tr.Description,
				Rate: tr.Rate, ChargeType: string(tr.ChargeType),
				IncludedInBasicRate: tr.IncludedInBasicRate,
				TaxAmount:           tr.TaxAmount,
				BaseTaxAmount:       tr.TaxAmount.Mul(rate).Round(money.Precision),
				CostCenterID:        tr.CostCenterID,
			}
		}

		netTotal := taxResult.NetTotal
		taxTotal := taxResult.TaxTotal
		grand := taxResult.GrandTotal

		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_order (
				id, name, company_id, supplier_id, transaction_date, required_by_date,
				fiscal_year_id, currency, exchange_rate, tax_template_id,
				total, base_total,
				net_total, total_taxes_and_charges, grand_total,
				base_net_total, base_total_taxes_and_charges, base_grand_total,
				status, remarks, terms_and_conditions, payment_terms, letterhead_id,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,
			          $7,$8,$9,$10,
			          $11,$12,
			          $13,$14,$15,
			          $16,$17,$18,
			          $19,$20,$21,$22,$23,
			          $24,$25,$25)`,
			id, name, in.CompanyID, in.SupplierID, td, nullableTime(requiredBy),
			fyID, currency, rate, nullable(taxTemplateID),
			grand, grand.Mul(rate).Round(money.Precision),
			netTotal, taxTotal, grand,
			netTotal.Mul(rate).Round(money.Precision),
			taxTotal.Mul(rate).Round(money.Precision),
			grand.Mul(rate).Round(money.Precision),
			StatusDraft, nullable(in.Remarks), nullable(in.TermsAndConditions),
			nullable(in.PaymentTerms), nullable(in.LetterheadID),
			cf, p.UserID); err != nil {
			return err
		}

		for _, l := range draftLines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_order_item (
					id, purchase_order_id, row_index, item_id, item_code, item_name, description,
					qty, uom, rate, amount, warehouse_id, cost_center_id, required_by_date,
					tax_amount, total, base_amount, base_tax_amount, base_total
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
				l.ID, id, l.RowIndex, nullable(l.ItemID), l.ItemCode, l.ItemName, nullable(l.Description),
				l.Qty, l.UOM, l.Rate, l.Amount, nullable(l.WarehouseID), nullable(l.CostCenterID),
				nullableTime(l.RequiredByDate),
				l.TaxAmount, l.Total, l.BaseAmount, l.BaseTaxAmount, l.BaseTotal); err != nil {
				return err
			}
		}
		for _, tr := range taxRows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_order_tax (
					id, purchase_order_id, row_index, account_id, description, rate,
					charge_type, included_in_basic_rate, tax_amount, base_tax_amount, cost_center_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				tr.ID, id, tr.RowIndex, tr.AccountID, tr.Description, tr.Rate,
				tr.ChargeType, tr.IncludedInBasicRate, tr.TaxAmount, tr.BaseTaxAmount, nullable(tr.CostCenterID)); err != nil {
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

func (s *Service) Submit(ctx context.Context, id string) (*PurchaseOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_order: unauthenticated")
	}
	var out PurchaseOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		po, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if po.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if po.GrandTotal.IsZero() {
			return errors.New("purchase_order: grand_total is zero")
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, Doctype); err != nil {
				return err
			}
		}
		if s.Approvals != nil {
			gt, _ := po.GrandTotal.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, Doctype, po.ID, po.Name, po.CompanyID,
				map[string]any{"grand_total": gt}); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE purchase_order
			SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			    status = $2, updated_by = $1
			WHERE id = $3`, p.UserID, StatusToReceiveAndBill, id); err != nil {
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

// ---- Cancel ----

func (s *Service) Cancel(ctx context.Context, id string) (*PurchaseOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_order: unauthenticated")
	}
	var out PurchaseOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		po, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if po.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		// A PO with any received or billed qty must be reversed downstream
		// first — cancelling here would leave dangling GRN/PI rows.
		if hasDownstreamFulfilment(po) {
			return errors.New("purchase_order: cannot cancel — receipts or invoices reference this PO; cancel those first")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE purchase_order
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

// ---- Hold / Close / Stop / Reopen ----

// Hold pauses a PO without breaking its commitment — receipts and invoices
// against it are blocked while held. Reversible via Reopen.
func (s *Service) Hold(ctx context.Context, id string) (*PurchaseOrder, error) {
	return s.transition(ctx, id, StatusOnHold, "held", func(po *PurchaseOrder) error {
		if po.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if po.Status == StatusClosed || po.Status == StatusStopped {
			return errors.New("purchase_order: reopen the PO before holding")
		}
		return nil
	})
}

// Close marks a PO as deliberately ended even if not fully received/billed.
// (e.g. supplier cancelled remaining items.) Distinguished from Stopped by
// intent — close = "we're done", stop = "halt fulfilment for now".
func (s *Service) Close(ctx context.Context, id string) (*PurchaseOrder, error) {
	return s.transition(ctx, id, StatusClosed, "closed", func(po *PurchaseOrder) error {
		if po.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		return nil
	})
}

// Stop halts further receipts/invoices. Used when there's a quality issue
// with the supplier or pricing dispute, with the intent to resume later.
func (s *Service) Stop(ctx context.Context, id string) (*PurchaseOrder, error) {
	return s.transition(ctx, id, StatusStopped, "stopped", func(po *PurchaseOrder) error {
		if po.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		return nil
	})
}

// Reopen returns a Held/Closed/Stopped PO to the active fulfilment loop,
// recomputing its status from received_qty/billed_qty.
func (s *Service) Reopen(ctx context.Context, id string) (*PurchaseOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_order: unauthenticated")
	}
	var out PurchaseOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		po, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if po.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if po.Status != StatusOnHold && po.Status != StatusClosed && po.Status != StatusStopped {
			return fmt.Errorf("purchase_order: cannot reopen from status %s", po.Status)
		}
		next := recomputeStatus(po)
		if _, err := tx.Exec(ctx, `
			UPDATE purchase_order
			SET status = $1,
			    held_at = NULL, held_by = NULL,
			    closed_at = NULL, closed_by = NULL,
			    stopped_at = NULL, stopped_by = NULL,
			    updated_by = $2
			WHERE id = $3`, next, p.UserID, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, "reopen", audit.Diff{After: map[string]any{"status": next}}); err != nil {
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

func (s *Service) transition(ctx context.Context, id, toStatus, auditAction string, guard func(*PurchaseOrder) error) (*PurchaseOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_order: unauthenticated")
	}
	var out PurchaseOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		po, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := guard(po); err != nil {
			return err
		}
		var stampCol string
		var stampUserCol string
		switch toStatus {
		case StatusOnHold:
			stampCol, stampUserCol = "held_at", "held_by"
		case StatusClosed:
			stampCol, stampUserCol = "closed_at", "closed_by"
		case StatusStopped:
			stampCol, stampUserCol = "stopped_at", "stopped_by"
		}
		query := fmt.Sprintf(`
			UPDATE purchase_order
			SET status = $1, %s = now(), %s = $2, updated_by = $2
			WHERE id = $3`, stampCol, stampUserCol)
		if _, err := tx.Exec(ctx, query, toStatus, p.UserID, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.Action(auditAction), audit.Diff{After: map[string]any{"status": toStatus}}); err != nil {
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

// ---- Get / List ----

func (s *Service) Get(ctx context.Context, id string) (*PurchaseOrder, error) {
	var out *PurchaseOrder
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		po, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = po
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]PurchaseOrder, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM purchase_order
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
	out := make([]PurchaseOrder, 0, len(ids))
	for _, id := range ids {
		po, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *po)
	}
	return out, nil
}

// ---- Status recomputation (exported for tests + future GRN/PI integration) ----

// RecomputeStatus returns the status a PO should hold given its current
// fulfilment counters. Pure — does not touch the DB. Held/Closed/Stopped
// take precedence over the auto-computed status and are returned as-is.
func RecomputeStatus(po *PurchaseOrder) string {
	return recomputeStatus(po)
}

func recomputeStatus(po *PurchaseOrder) string {
	// Manual states win — only Reopen clears them.
	switch po.Status {
	case StatusOnHold, StatusClosed, StatusStopped, StatusCancelled, StatusDraft:
		// fall through only for OnHold/Closed/Stopped via Reopen; this fn
		// is also called from non-reopen contexts, so don't blanket-return
	}

	allReceived := true
	allBilled := true
	for _, l := range po.Items {
		if l.ReceivedQty.LessThan(l.Qty) {
			allReceived = false
		}
		if l.BilledQty.LessThan(l.Qty) {
			allBilled = false
		}
	}
	switch {
	case allReceived && allBilled:
		return StatusCompleted
	case allReceived:
		return StatusToBill
	case allBilled:
		return StatusToReceive
	}
	return StatusToReceiveAndBill
}

// hasDownstreamFulfilment returns true if any line carries received or billed
// qty — guards Cancel so we don't leave orphan stock/AP entries.
func hasDownstreamFulfilment(po *PurchaseOrder) bool {
	for _, l := range po.Items {
		if !l.ReceivedQty.IsZero() || !l.BilledQty.IsZero() {
			return true
		}
	}
	return false
}

// ---- helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*PurchaseOrder, error) {
	var (
		po                                                      PurchaseOrder
		requiredBy, submittedAt, cancelledAt, heldAt, closedAt, stoppedAt *time.Time
		fiscalYearID, taxTemplateID                             *string
		remarks, termsAndConditions, paymentTerms, letterheadID *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, supplier_id, transaction_date, required_by_date,
		       fiscal_year_id, currency, exchange_rate, tax_template_id,
		       net_total, total_taxes_and_charges, grand_total,
		       base_net_total, base_total_taxes_and_charges, base_grand_total,
		       status, remarks, terms_and_conditions, payment_terms, letterhead_id,
		       docstatus, submitted_at, cancelled_at, held_at, closed_at, stopped_at,
		       created_at, updated_at
		FROM purchase_order WHERE id = $1`, id).
		Scan(&po.ID, &po.Name, &po.CompanyID, &po.SupplierID, &po.TransactionDate, &requiredBy,
			&fiscalYearID, &po.Currency, &po.ExchangeRate, &taxTemplateID,
			&po.NetTotal, &po.TotalTaxesAndCharges, &po.GrandTotal,
			&po.BaseNetTotal, &po.BaseTotalTaxesAndCharges, &po.BaseGrandTotal,
			&po.Status, &remarks, &termsAndConditions, &paymentTerms, &letterheadID,
			&po.Docstatus, &submittedAt, &cancelledAt, &heldAt, &closedAt, &stoppedAt,
			&po.CreatedAt, &po.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("purchase_order %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	po.RequiredByDate = requiredBy
	po.SubmittedAt = submittedAt
	po.CancelledAt = cancelledAt
	po.HeldAt = heldAt
	po.ClosedAt = closedAt
	po.StoppedAt = stoppedAt
	if fiscalYearID != nil {
		po.FiscalYearID = *fiscalYearID
	}
	if taxTemplateID != nil {
		po.TaxTemplateID = *taxTemplateID
	}
	if remarks != nil {
		po.Remarks = *remarks
	}
	if termsAndConditions != nil {
		po.TermsAndConditions = *termsAndConditions
	}
	if paymentTerms != nil {
		po.PaymentTerms = *paymentTerms
	}
	if letterheadID != nil {
		po.LetterheadID = *letterheadID
	}

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, coalesce(item_id,''), item_code, item_name, coalesce(description,''),
		       qty, uom, rate, amount, coalesce(warehouse_id,''), coalesce(cost_center_id,''),
		       required_by_date, tax_amount, total, base_amount, base_tax_amount, base_total,
		       received_qty, billed_qty
		FROM purchase_order_item WHERE purchase_order_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l PurchaseOrderLine
		var lineReq *time.Time
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.ItemCode, &l.ItemName, &l.Description,
			&l.Qty, &l.UOM, &l.Rate, &l.Amount, &l.WarehouseID, &l.CostCenterID,
			&lineReq, &l.TaxAmount, &l.Total, &l.BaseAmount, &l.BaseTaxAmount, &l.BaseTotal,
			&l.ReceivedQty, &l.BilledQty); err != nil {
			rows.Close()
			return nil, err
		}
		l.RequiredByDate = lineReq
		po.Items = append(po.Items, l)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT id, row_index, account_id, description, rate, charge_type, included_in_basic_rate,
		       tax_amount, base_tax_amount, coalesce(cost_center_id,'')
		FROM purchase_order_tax WHERE purchase_order_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t PurchaseOrderTaxRow
		if err := rows.Scan(&t.ID, &t.RowIndex, &t.AccountID, &t.Description, &t.Rate,
			&t.ChargeType, &t.IncludedInBasicRate, &t.TaxAmount, &t.BaseTaxAmount, &t.CostCenterID); err != nil {
			return nil, err
		}
		po.Taxes = append(po.Taxes, t)
	}
	return &po, nil
}

func parseDec(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

func pickFiscalYear(ctx context.Context, tx pgx.Tx, companyID string, pd time.Time) (string, error) {
	var fyID string
	err := tx.QueryRow(ctx, `
		SELECT fy.id FROM fiscal_year fy
		JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
		WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
		ORDER BY fy.start_date DESC LIMIT 1`, companyID, pd).Scan(&fyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no fiscal year covers %s for company %s", pd.Format("2006-01-02"), companyID)
	}
	return fyID, err
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
