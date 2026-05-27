// Package purchasereceipt implements the Purchase Receipt (GRN) doctype —
// the goods-receipt note that records what physically arrived from a
// supplier. Replaces the generic stock_entry material_receipt path for
// purchases; stock_entry stays for transfers/issues/manufacture.
//
// Submit writes stock_ledger_entry rows directly (one per line per warehouse
// where qty > 0) and bumps the linked PO's received_qty. v1 intentionally
// does NOT post to GL — Dr Stock / Cr SRBNB lands when Buying Settings +
// per-warehouse SRBNB account come online, and PI continues to post the
// expense-side leg today.
//
// Status machine:
//
//   Draft → submit → Completed (if no remaining qty on the linked PO)
//                  → To Bill   (still needs invoicing — used as a future-proof
//                               hookpoint when PI submit flips it to Completed)
//   Submitted → Cancelled (terminal; reverses SLE + PO received_qty)
package purchasereceipt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/accounting/purchaseorder"
	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/ledger/valuation"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const (
	Doctype     = "purchase_receipt"
	VoucherType = "Purchase Receipt"
)

// Status string constants.
const (
	StatusDraft        = "Draft"
	StatusToBill       = "To Bill"
	StatusCompleted    = "Completed"
	StatusReturnIssued = "Return Issued"
	StatusCancelled    = "Cancelled"
)

// ---- domain types ----

type PurchaseReceipt struct {
	ID                      string                  `json:"id"`
	Name                    string                  `json:"name"`
	CompanyID               string                  `json:"company_id"`
	SupplierID              string                  `json:"supplier_id"`
	PostingDate             time.Time               `json:"posting_date"`
	PostingDateTime         time.Time               `json:"posting_datetime"`
	AgainstPurchaseOrderID  string                  `json:"against_purchase_order_id,omitempty"`
	SupplierDeliveryNote    string                  `json:"supplier_delivery_note,omitempty"`
	Status                  string                  `json:"status"`
	Remarks                 string                  `json:"remarks,omitempty"`
	Docstatus               submittable.Status      `json:"docstatus"`
	SubmittedAt             *time.Time              `json:"submitted_at,omitempty"`
	CancelledAt             *time.Time              `json:"cancelled_at,omitempty"`
	FiscalYearID            string                  `json:"fiscal_year_id,omitempty"`
	TotalValue              decimal.Decimal         `json:"total_value"`
	CreatedAt               time.Time               `json:"created_at"`
	UpdatedAt               time.Time               `json:"updated_at"`
	Items                   []PurchaseReceiptLine   `json:"items"`
}

type PurchaseReceiptLine struct {
	ID                    string          `json:"id"`
	RowIndex              int             `json:"row_index"`
	ItemID                string          `json:"item_id"`
	ItemCode              string          `json:"item_code"`
	ItemName              string          `json:"item_name"`
	Description           string          `json:"description,omitempty"`
	UOM                   string          `json:"uom"`
	Rate                  decimal.Decimal `json:"rate"`
	AcceptedQty           decimal.Decimal `json:"accepted_qty"`
	RejectedQty           decimal.Decimal `json:"rejected_qty"`
	AcceptedWarehouseID   string          `json:"accepted_warehouse_id"`
	RejectedWarehouseID   string          `json:"rejected_warehouse_id,omitempty"`
	AgainstPOID           string          `json:"against_po_id,omitempty"`
	AgainstPORowIndex     int             `json:"against_po_row_index,omitempty"`
	ValuationRate         decimal.Decimal `json:"valuation_rate"`
	Amount                decimal.Decimal `json:"amount"`
	CostCenterID          string          `json:"cost_center_id,omitempty"`
}

// ---- input ----

type PRCreateInput struct {
	CompanyID              string                  `json:"company_id,omitempty"`
	SupplierID             string                  `json:"supplier_id"`
	PostingDate            string                  `json:"posting_date"`
	AgainstPurchaseOrderID string                  `json:"against_purchase_order_id,omitempty"`
	SupplierDeliveryNote   string                  `json:"supplier_delivery_note,omitempty"`
	Remarks                string                  `json:"remarks,omitempty"`
	Items                  []PRLineInput             `json:"items"`
	CustomFields           map[string]any          `json:"custom_fields,omitempty"`
}

type PRLineInput struct {
	ItemID                string `json:"item_id"`
	ItemCode              string `json:"item_code,omitempty"`
	ItemName              string `json:"item_name,omitempty"`
	Description           string `json:"description,omitempty"`
	UOM                   string `json:"uom,omitempty"`
	Rate                  string `json:"rate"`
	AcceptedQty           string `json:"accepted_qty"`
	RejectedQty           string `json:"rejected_qty,omitempty"`
	AcceptedWarehouseID   string `json:"accepted_warehouse_id"`
	RejectedWarehouseID   string `json:"rejected_warehouse_id,omitempty"`
	AgainstPORowIndex     int    `json:"against_po_row_index,omitempty"`
	CostCenterID          string `json:"cost_center_id,omitempty"`
}

// ---- Service ----

type Service struct {
	db        *dbx.DB
	po        *purchaseorder.Service
	Workflow  workflowGate
}

type workflowGate interface {
	CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error
}

func NewService(db *dbx.DB, po *purchaseorder.Service) *Service {
	return &Service{db: db, po: po}
}

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in PRCreateInput) (*PurchaseReceipt, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_receipt: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("purchase_receipt.company_id: required")
	}
	if in.SupplierID == "" {
		return nil, errors.New("purchase_receipt.supplier_id: required")
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("purchase_receipt.posting_date: %w", err)
	}
	if len(in.Items) == 0 {
		return nil, errors.New("purchase_receipt.items: at least one required")
	}

	id := dbx.NewIDWithPrefix("pr")
	var out PurchaseReceipt
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		// If the user said "this is against PO X", make sure supplier
		// matches — receipts have to belong to the right supplier.
		if in.AgainstPurchaseOrderID != "" {
			var poSupplier string
			var poDocstatus int16
			if err := tx.QueryRow(ctx,
				`SELECT supplier_id, docstatus FROM purchase_order WHERE id = $1 AND company_id = $2`,
				in.AgainstPurchaseOrderID, in.CompanyID).Scan(&poSupplier, &poDocstatus); err != nil {
				return fmt.Errorf("against_purchase_order_id: %w", err)
			}
			if poDocstatus != 1 {
				return errors.New("against_purchase_order_id: PO must be submitted")
			}
			if poSupplier != in.SupplierID {
				return errors.New("against_purchase_order_id: supplier must match the PO's supplier")
			}
		}

		fyID, err := pickFiscalYear(ctx, tx, in.CompanyID, pd)
		if err != nil {
			return err
		}
		seriesID, pattern, err := pickNamingSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, pd, nil)
		if err != nil {
			return err
		}
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}

		// Validate + materialise lines. Rate is fed forward from the PO
		// line when the user omits it, so the FE doesn't have to.
		draftLines := make([]PurchaseReceiptLine, len(in.Items))
		var total decimal.Decimal
		for i, ln := range in.Items {
			if ln.ItemID == "" {
				return fmt.Errorf("items[%d].item_id: required (purchase_receipt has no free-text fallback)", i)
			}
			acc, err := parseDec(ln.AcceptedQty)
			if err != nil {
				return fmt.Errorf("items[%d].accepted_qty: %w", i, err)
			}
			rej, err := parseDec(ln.RejectedQty)
			if err != nil {
				return fmt.Errorf("items[%d].rejected_qty: %w", i, err)
			}
			if acc.IsNegative() || rej.IsNegative() {
				return fmt.Errorf("items[%d]: qty cannot be negative", i)
			}
			if acc.Add(rej).IsZero() {
				return fmt.Errorf("items[%d]: must have accepted_qty or rejected_qty > 0", i)
			}
			if ln.AcceptedWarehouseID == "" {
				return fmt.Errorf("items[%d].accepted_warehouse_id: required", i)
			}
			if rej.IsPositive() && ln.RejectedWarehouseID == "" {
				return fmt.Errorf("items[%d].rejected_warehouse_id: required when rejected_qty > 0", i)
			}
			rate, err := parseDec(ln.Rate)
			if err != nil {
				return fmt.Errorf("items[%d].rate: %w", i, err)
			}

			// Pull item meta from master + (optionally) snapshot the PO line.
			var code, nm, stockUOM string
			if err := tx.QueryRow(ctx,
				`SELECT code, name, stock_uom FROM item WHERE id = $1 AND is_deleted = false`,
				ln.ItemID).Scan(&code, &nm, &stockUOM); err != nil {
				return fmt.Errorf("items[%d].item_id: %w", i, err)
			}

			itemCode, itemName, uom := ln.ItemCode, ln.ItemName, ln.UOM
			if itemCode == "" {
				itemCode = code
			}
			if itemName == "" {
				itemName = nm
			}
			if uom == "" {
				uom = stockUOM
			}

			// PO-line validation: qty within remaining-to-receive bounds.
			var againstPOID *string
			if in.AgainstPurchaseOrderID != "" && ln.AgainstPORowIndex > 0 {
				var poQty, poReceived decimal.Decimal
				var poItemID string
				err := tx.QueryRow(ctx, `
					SELECT item_id, qty, received_qty FROM purchase_order_item
					WHERE purchase_order_id = $1 AND row_index = $2`,
					in.AgainstPurchaseOrderID, ln.AgainstPORowIndex).
					Scan(&poItemID, &poQty, &poReceived)
				if err != nil {
					return fmt.Errorf("items[%d]: PO row %d not found: %w", i, ln.AgainstPORowIndex, err)
				}
				if poItemID != "" && poItemID != ln.ItemID {
					return fmt.Errorf("items[%d]: item mismatch against PO row %d", i, ln.AgainstPORowIndex)
				}
				remaining := poQty.Sub(poReceived)
				totalThisLine := acc.Add(rej)
				if totalThisLine.GreaterThan(remaining) {
					return fmt.Errorf("items[%d]: total qty %s exceeds remaining %s on PO row %d",
						i, totalThisLine, remaining, ln.AgainstPORowIndex)
				}
				poID := in.AgainstPurchaseOrderID
				againstPOID = &poID

				if rate.IsZero() {
					// Carry forward the PO rate so the FE doesn't have to
					// repeat itself.
					var poRate decimal.Decimal
					if err := tx.QueryRow(ctx,
						`SELECT rate FROM purchase_order_item WHERE purchase_order_id = $1 AND row_index = $2`,
						in.AgainstPurchaseOrderID, ln.AgainstPORowIndex).Scan(&poRate); err == nil {
						rate = poRate
					}
				}
			}

			amount := acc.Add(rej).Mul(rate).Round(money.Precision)
			total = total.Add(amount)

			rowID := dbx.NewIDWithPrefix("pri")
			draftLines[i] = PurchaseReceiptLine{
				ID: rowID, RowIndex: i + 1,
				ItemID: ln.ItemID, ItemCode: itemCode, ItemName: itemName, Description: ln.Description,
				UOM: uom, Rate: rate, Amount: amount,
				AcceptedQty: acc, RejectedQty: rej,
				AcceptedWarehouseID: ln.AcceptedWarehouseID,
				RejectedWarehouseID: ln.RejectedWarehouseID,
				CostCenterID:        ln.CostCenterID,
				AgainstPORowIndex:   ln.AgainstPORowIndex,
			}
			if againstPOID != nil {
				draftLines[i].AgainstPOID = *againstPOID
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_receipt (
				id, name, company_id, supplier_id, posting_date,
				against_purchase_order_id, supplier_delivery_note,
				status, remarks, fiscal_year_id, total_value,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`,
			id, name, in.CompanyID, in.SupplierID, pd,
			nullable(in.AgainstPurchaseOrderID), nullable(in.SupplierDeliveryNote),
			StatusDraft, nullable(in.Remarks), fyID, total,
			cf, p.UserID); err != nil {
			return err
		}
		for _, l := range draftLines {
			var againstPOID any
			if l.AgainstPOID != "" {
				againstPOID = l.AgainstPOID
			}
			var againstRow any
			if l.AgainstPORowIndex > 0 {
				againstRow = l.AgainstPORowIndex
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_receipt_item (
					id, purchase_receipt_id, row_index, item_id, item_code, item_name, description, uom,
					rate, accepted_qty, rejected_qty, accepted_warehouse_id, rejected_warehouse_id,
					against_po_id, against_po_row_index, amount, cost_center_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
				l.ID, id, l.RowIndex, l.ItemID, l.ItemCode, l.ItemName, nullable(l.Description), l.UOM,
				l.Rate, l.AcceptedQty, l.RejectedQty, l.AcceptedWarehouseID, nullable(l.RejectedWarehouseID),
				againstPOID, againstRow, l.Amount, nullable(l.CostCenterID)); err != nil {
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

// Submit writes a stock_ledger_entry per (line, warehouse) where qty > 0,
// bumps PO received_qty, and recomputes the PO status.
func (s *Service) Submit(ctx context.Context, id string) (*PurchaseReceipt, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_receipt: unauthenticated")
	}
	var out PurchaseReceipt
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pr.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, Doctype); err != nil {
				return err
			}
		}

		postingDT := pr.PostingDate

		// Walk lines, write SLE for accepted + rejected qty, capture
		// valuation_rate back onto the line for later display.
		for i := range pr.Items {
			l := &pr.Items[i]

			// Receipts use the supplied rate as the incoming valuation rate.
			// If the rate is zero (no PO carryforward), reject — we'd corrupt
			// the FIFO ledger with a zero-valued receipt.
			if l.Rate.IsZero() {
				return fmt.Errorf("items[%d]: rate required to value the receipt", i)
			}

			if l.AcceptedQty.IsPositive() {
				balQty, balVal, err := valuation.IncomingBalance(ctx, tx, l.ItemID, l.AcceptedWarehouseID, l.AcceptedQty, l.Rate)
				if err != nil {
					return err
				}
				stockValue := l.AcceptedQty.Mul(l.Rate).Round(money.Precision)
				if err := insertSLE(ctx, tx, p.UserID, pr,
					l.ItemID, l.AcceptedWarehouseID, l.AcceptedQty, balQty, l.Rate, balVal, stockValue, &l.Rate, postingDT); err != nil {
					return err
				}
			}
			if l.RejectedQty.IsPositive() {
				balQty, balVal, err := valuation.IncomingBalance(ctx, tx, l.ItemID, l.RejectedWarehouseID, l.RejectedQty, l.Rate)
				if err != nil {
					return err
				}
				stockValue := l.RejectedQty.Mul(l.Rate).Round(money.Precision)
				if err := insertSLE(ctx, tx, p.UserID, pr,
					l.ItemID, l.RejectedWarehouseID, l.RejectedQty, balQty, l.Rate, balVal, stockValue, &l.Rate, postingDT); err != nil {
					return err
				}
			}
			l.ValuationRate = l.Rate
		}

		// Bump PO received_qty + recompute status for every line referencing the PO.
		// Group line bumps by PO so we recompute the status once per PO.
		bumpedPOs := map[string]bool{}
		for _, l := range pr.Items {
			if l.AgainstPOID == "" || l.AgainstPORowIndex == 0 {
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE purchase_order_item
				SET received_qty = received_qty + $1
				WHERE purchase_order_id = $2 AND row_index = $3`,
				l.AcceptedQty.Add(l.RejectedQty), l.AgainstPOID, l.AgainstPORowIndex); err != nil {
				return err
			}
			bumpedPOs[l.AgainstPOID] = true
		}
		for poID := range bumpedPOs {
			if err := recomputePOStatus(ctx, tx, poID); err != nil {
				return err
			}
			// doc_link row for traceability.
			if _, err := tx.Exec(ctx, `
				INSERT INTO doc_link (parent_doctype, parent_id, child_doctype, child_id)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (parent_doctype, parent_id, child_doctype, child_id) DO NOTHING`,
				purchaseorder.Doctype, poID, Doctype, pr.ID); err != nil {
				return err
			}
		}

		// Final status — Completed if the linked PO has no remaining qty;
		// otherwise To Bill. Without a PO link, default to Completed.
		nextStatus := StatusCompleted
		if pr.AgainstPurchaseOrderID != "" {
			done, err := poFullyReceived(ctx, tx, pr.AgainstPurchaseOrderID)
			if err != nil {
				return err
			}
			if !done {
				nextStatus = StatusToBill
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE purchase_receipt
			SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			    status = $2, updated_by = $1
			WHERE id = $3`, p.UserID, nextStatus, id); err != nil {
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

// Cancel marks SLE rows cancelled, decrements PO received_qty, recomputes PO
// status, and sets docstatus=2.
func (s *Service) Cancel(ctx context.Context, id string) (*PurchaseReceipt, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_receipt: unauthenticated")
	}
	var out PurchaseReceipt
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pr.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}

		// Mark SLE rows cancelled. We don't add reversing SLE rows because
		// downstream FIFO reads filter on is_cancelled = false; adding
		// reversal rows would double-count.
		if _, err := tx.Exec(ctx,
			`UPDATE stock_ledger_entry SET is_cancelled = true WHERE voucher_type = $1 AND voucher_id = $2`,
			VoucherType, id); err != nil {
			return err
		}

		// Decrement PO received_qty + recompute.
		bumpedPOs := map[string]bool{}
		for _, l := range pr.Items {
			if l.AgainstPOID == "" || l.AgainstPORowIndex == 0 {
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE purchase_order_item
				SET received_qty = received_qty - $1
				WHERE purchase_order_id = $2 AND row_index = $3`,
				l.AcceptedQty.Add(l.RejectedQty), l.AgainstPOID, l.AgainstPORowIndex); err != nil {
				return err
			}
			bumpedPOs[l.AgainstPOID] = true
		}
		for poID := range bumpedPOs {
			if err := recomputePOStatus(ctx, tx, poID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE purchase_receipt SET docstatus = 2, cancelled_at = now(), cancelled_by = $1,
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

// ---- Get / List ----

func (s *Service) Get(ctx context.Context, id string) (*PurchaseReceipt, error) {
	var out *PurchaseReceipt
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = pr
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]PurchaseReceipt, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM purchase_receipt WHERE company_id = $1
		ORDER BY posting_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]PurchaseReceipt, 0, len(ids))
	for _, id := range ids {
		pr, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *pr)
	}
	return out, nil
}

// ---- PO status helpers ----

// recomputePOStatus loads the PO, runs the pure purchaseorder.RecomputeStatus,
// then writes back if changed. The status column is the source of truth for
// "what should the user do next".
func recomputePOStatus(ctx context.Context, tx pgx.Tx, poID string) error {
	po, err := loadPOForStatus(ctx, tx, poID)
	if err != nil {
		return err
	}
	// Don't override manual states.
	switch po.Status {
	case purchaseorder.StatusOnHold, purchaseorder.StatusClosed, purchaseorder.StatusStopped, purchaseorder.StatusCancelled:
		return nil
	}
	next := purchaseorder.RecomputeStatus(po)
	if next == po.Status {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE purchase_order SET status = $1 WHERE id = $2`, next, poID)
	return err
}

// loadPOForStatus pulls just the fields RecomputeStatus needs (status + items
// with qty/received_qty/billed_qty) without dragging the full PO load path.
func loadPOForStatus(ctx context.Context, tx pgx.Tx, poID string) (*purchaseorder.PurchaseOrder, error) {
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM purchase_order WHERE id = $1`, poID).Scan(&status); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx,
		`SELECT qty, received_qty, billed_qty FROM purchase_order_item WHERE purchase_order_id = $1 ORDER BY row_index`, poID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	po := &purchaseorder.PurchaseOrder{Status: status}
	for rows.Next() {
		var l purchaseorder.PurchaseOrderLine
		if err := rows.Scan(&l.Qty, &l.ReceivedQty, &l.BilledQty); err != nil {
			return nil, err
		}
		po.Items = append(po.Items, l)
	}
	return po, nil
}

func poFullyReceived(ctx context.Context, tx pgx.Tx, poID string) (bool, error) {
	po, err := loadPOForStatus(ctx, tx, poID)
	if err != nil {
		return false, err
	}
	for _, l := range po.Items {
		if l.ReceivedQty.LessThan(l.Qty) {
			return false, nil
		}
	}
	return true, nil
}

// ---- low-level helpers ----

func insertSLE(ctx context.Context, tx pgx.Tx, userID string, pr *PurchaseReceipt,
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
		id, pr.CompanyID, postingDT, itemID, warehouseID,
		actualQty, qtyAfter, valRate, stockValAfter, stockValueDiff,
		incoming, VoucherType, pr.ID, pr.Name, userID)
	return err
}

func load(ctx context.Context, tx pgx.Tx, id string) (*PurchaseReceipt, error) {
	var (
		pr                                         PurchaseReceipt
		submittedAt, cancelledAt                   *time.Time
		againstPO, supplierDN, remarks, fiscalYear *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, supplier_id, posting_date, posting_datetime,
		       against_purchase_order_id, supplier_delivery_note, status, remarks,
		       fiscal_year_id, total_value,
		       docstatus, submitted_at, cancelled_at, created_at, updated_at
		FROM purchase_receipt WHERE id = $1`, id).
		Scan(&pr.ID, &pr.Name, &pr.CompanyID, &pr.SupplierID, &pr.PostingDate, &pr.PostingDateTime,
			&againstPO, &supplierDN, &pr.Status, &remarks,
			&fiscalYear, &pr.TotalValue,
			&pr.Docstatus, &submittedAt, &cancelledAt, &pr.CreatedAt, &pr.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("purchase_receipt %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if againstPO != nil {
		pr.AgainstPurchaseOrderID = *againstPO
	}
	if supplierDN != nil {
		pr.SupplierDeliveryNote = *supplierDN
	}
	if remarks != nil {
		pr.Remarks = *remarks
	}
	if fiscalYear != nil {
		pr.FiscalYearID = *fiscalYear
	}
	pr.SubmittedAt = submittedAt
	pr.CancelledAt = cancelledAt

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, item_id, item_code, item_name, coalesce(description,''),
		       uom, rate, accepted_qty, rejected_qty,
		       accepted_warehouse_id, coalesce(rejected_warehouse_id,''),
		       coalesce(against_po_id,''), coalesce(against_po_row_index, 0),
		       valuation_rate, amount, coalesce(cost_center_id,'')
		FROM purchase_receipt_item WHERE purchase_receipt_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l PurchaseReceiptLine
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.ItemCode, &l.ItemName, &l.Description,
			&l.UOM, &l.Rate, &l.AcceptedQty, &l.RejectedQty,
			&l.AcceptedWarehouseID, &l.RejectedWarehouseID,
			&l.AgainstPOID, &l.AgainstPORowIndex,
			&l.ValuationRate, &l.Amount, &l.CostCenterID); err != nil {
			return nil, err
		}
		pr.Items = append(pr.Items, l)
	}
	return &pr, nil
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
