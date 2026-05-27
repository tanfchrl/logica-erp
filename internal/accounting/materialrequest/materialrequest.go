// Package materialrequest implements the Material Request document — the
// purchase requisition / internal transfer / issue / manufacture request
// that drives downstream PO, Stock Entry, Purchase Receipt creation.
//
// MR submit DOES NOT post to GL. It records intent only. Fulfilment counters
// (ordered_qty, received_qty, issued_qty, transferred_qty) are written by the
// downstream doctype's service when that doc submits.
//
// Status machine:
//
//   Draft → submit → Pending
//   Pending --(any ordered/issued/transferred)→ Partially Ordered
//   Partially Ordered → (all qty consumed) → Ordered | Issued | Transferred | Received
//   Pending|Partial → Stopped (recoverable via Reopen)
//   Any → Cancelled (terminal, docstatus=2)
//
// The "Ordered" label is reused for purpose='manufacture' — meaning a Work
// Order has been raised. Keep the naming generic so the FE doesn't fork.
package materialrequest

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
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const Doctype = "material_request"

// Purpose enum. The string values exactly match the CHECK constraint.
const (
	PurposePurchase         = "purchase"
	PurposeMaterialTransfer = "material_transfer"
	PurposeMaterialIssue    = "material_issue"
	PurposeManufacture      = "manufacture"
)

// Status constants — the string values stored in material_request.status.
const (
	StatusDraft             = "Draft"
	StatusPending           = "Pending"
	StatusPartiallyOrdered  = "Partially Ordered"
	StatusOrdered           = "Ordered"      // purchase / manufacture
	StatusIssued            = "Issued"       // material_issue
	StatusTransferred       = "Transferred"  // material_transfer
	StatusReceived          = "Received"     // purchase, after GRN
	StatusStopped           = "Stopped"
	StatusCancelled         = "Cancelled"
)

// ---- domain types ----

type MaterialRequest struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	CompanyID       string                `json:"company_id"`
	Purpose         string                `json:"purpose"`
	TransactionDate time.Time             `json:"transaction_date"`
	RequiredByDate  *time.Time            `json:"required_by_date,omitempty"`
	SetWarehouseID  string                `json:"set_warehouse_id,omitempty"`
	FromWarehouseID string                `json:"from_warehouse_id,omitempty"`
	Status          string                `json:"status"`
	Remarks         string                `json:"remarks,omitempty"`
	Docstatus       submittable.Status    `json:"docstatus"`
	SubmittedAt     *time.Time            `json:"submitted_at,omitempty"`
	CancelledAt     *time.Time            `json:"cancelled_at,omitempty"`
	StoppedAt       *time.Time            `json:"stopped_at,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
	Items           []MaterialRequestLine `json:"items"`
}

type MaterialRequestLine struct {
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
	RequiredByDate  *time.Time      `json:"required_by_date,omitempty"`
	OrderedQty      decimal.Decimal `json:"ordered_qty"`
	ReceivedQty     decimal.Decimal `json:"received_qty"`
	IssuedQty       decimal.Decimal `json:"issued_qty"`
	TransferredQty  decimal.Decimal `json:"transferred_qty"`
}

// ---- input shapes ----

type CreateInput struct {
	CompanyID       string         `json:"company_id,omitempty"`
	Purpose         string         `json:"purpose"`
	TransactionDate string         `json:"transaction_date"`
	RequiredByDate  string         `json:"required_by_date,omitempty"`
	SetWarehouseID  string         `json:"set_warehouse_id,omitempty"`
	FromWarehouseID string         `json:"from_warehouse_id,omitempty"`
	Remarks         string         `json:"remarks,omitempty"`
	Items           []LineInput    `json:"items"`
	CustomFields    map[string]any `json:"custom_fields,omitempty"`
}

type LineInput struct {
	ItemID         string `json:"item_id,omitempty"`
	ItemCode       string `json:"item_code,omitempty"`
	ItemName       string `json:"item_name,omitempty"`
	Description    string `json:"description,omitempty"`
	Qty            string `json:"qty"`
	UOM            string `json:"uom,omitempty"`
	Rate           string `json:"rate,omitempty"`
	WarehouseID    string `json:"warehouse_id,omitempty"`
	RequiredByDate string `json:"required_by_date,omitempty"`
}

// CreatePOFromMRInput supports the "Create PO from MR" action. The user
// optionally narrows down which lines + qtys to include; an empty Lines slice
// means "carry over every line at its full remaining qty".
type CreatePOFromMRInput struct {
	MaterialRequestID string                       `json:"material_request_id"`
	SupplierID        string                       `json:"supplier_id"`
	Lines             []CreatePOFromMRLineSelector `json:"lines,omitempty"`
}

type CreatePOFromMRLineSelector struct {
	RowIndex int    `json:"row_index"`
	Qty      string `json:"qty,omitempty"` // empty = "remaining qty"
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

func (s *Service) CreateDraft(ctx context.Context, in CreateInput) (*MaterialRequest, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("material_request: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("material_request.company_id: required")
	}
	if !validPurpose(in.Purpose) {
		return nil, fmt.Errorf("material_request.purpose: must be one of purchase/material_transfer/material_issue/manufacture (got %q)", in.Purpose)
	}
	td, err := time.Parse("2006-01-02", in.TransactionDate)
	if err != nil {
		return nil, fmt.Errorf("material_request.transaction_date: %w", err)
	}
	var requiredBy *time.Time
	if in.RequiredByDate != "" {
		t, err := time.Parse("2006-01-02", in.RequiredByDate)
		if err != nil {
			return nil, fmt.Errorf("material_request.required_by_date: %w", err)
		}
		requiredBy = &t
	}
	if len(in.Items) == 0 {
		return nil, errors.New("material_request.items: at least one required")
	}
	// material_transfer must have a source warehouse — otherwise the
	// downstream Stock Entry has nothing to issue from.
	if in.Purpose == PurposeMaterialTransfer && in.FromWarehouseID == "" {
		return nil, errors.New("material_request.from_warehouse_id: required for material_transfer purpose")
	}

	id := dbx.NewIDWithPrefix("mr")
	var out MaterialRequest
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
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

		draftLines := make([]MaterialRequestLine, len(in.Items))
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
			warehouse := ln.WarehouseID
			if warehouse == "" {
				warehouse = in.SetWarehouseID // header default
			}
			var lineRequiredBy *time.Time
			if ln.RequiredByDate != "" {
				t, err := time.Parse("2006-01-02", ln.RequiredByDate)
				if err != nil {
					return fmt.Errorf("items[%d].required_by_date: %w", i, err)
				}
				lineRequiredBy = &t
			}
			rowID := dbx.NewIDWithPrefix("mri")
			draftLines[i] = MaterialRequestLine{
				ID: rowID, RowIndex: i + 1,
				ItemID: ln.ItemID, ItemCode: itemCode, ItemName: itemName, Description: ln.Description,
				Qty: qty, UOM: uom, Rate: rt, Amount: amount,
				WarehouseID: warehouse, RequiredByDate: lineRequiredBy,
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO material_request (
				id, name, company_id, purpose, transaction_date, required_by_date,
				set_warehouse_id, from_warehouse_id, status, remarks,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`,
			id, name, in.CompanyID, in.Purpose, td, nullableTime(requiredBy),
			nullable(in.SetWarehouseID), nullable(in.FromWarehouseID), StatusDraft, nullable(in.Remarks),
			cf, p.UserID); err != nil {
			return err
		}
		for _, l := range draftLines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO material_request_item (
					id, material_request_id, row_index, item_id, item_code, item_name, description,
					qty, uom, rate, amount, warehouse_id, required_by_date
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				l.ID, id, l.RowIndex, nullable(l.ItemID), l.ItemCode, l.ItemName, nullable(l.Description),
				l.Qty, l.UOM, l.Rate, l.Amount, nullable(l.WarehouseID), nullableTime(l.RequiredByDate)); err != nil {
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

func (s *Service) Submit(ctx context.Context, id string) (*MaterialRequest, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("material_request: unauthenticated")
	}
	var out MaterialRequest
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if mr.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, Doctype); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE material_request
			SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			    status = $2, updated_by = $1
			WHERE id = $3`, p.UserID, StatusPending, id); err != nil {
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

func (s *Service) Cancel(ctx context.Context, id string) (*MaterialRequest, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("material_request: unauthenticated")
	}
	var out MaterialRequest
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if mr.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if hasDownstreamFulfilment(mr) {
			return errors.New("material_request: cannot cancel — downstream POs/Stock Entries reference this MR; cancel those first")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE material_request
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

// ---- Stop / Reopen ----

func (s *Service) Stop(ctx context.Context, id string) (*MaterialRequest, error) {
	return s.transition(ctx, id, StatusStopped, "stopped", func(mr *MaterialRequest) error {
		if mr.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if mr.Status == StatusStopped {
			return errors.New("material_request: already stopped")
		}
		return nil
	})
}

func (s *Service) Reopen(ctx context.Context, id string) (*MaterialRequest, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("material_request: unauthenticated")
	}
	var out MaterialRequest
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if mr.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if mr.Status != StatusStopped {
			return fmt.Errorf("material_request: can only reopen Stopped requests (got %s)", mr.Status)
		}
		next := recomputeStatus(mr)
		if _, err := tx.Exec(ctx, `
			UPDATE material_request
			SET status = $1, stopped_at = NULL, stopped_by = NULL, updated_by = $2
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

func (s *Service) transition(ctx context.Context, id, toStatus, auditAction string, guard func(*MaterialRequest) error) (*MaterialRequest, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("material_request: unauthenticated")
	}
	var out MaterialRequest
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := guard(mr); err != nil {
			return err
		}
		stampCol, stampUserCol := "stopped_at", "stopped_by"
		query := fmt.Sprintf(`
			UPDATE material_request
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

// ---- CreatePOFromMR ----

// CreatePOFromMR creates a draft Purchase Order from the remaining-to-order
// qty of a submitted, purchase-purpose MR. Returns the created PO.
//
// Behavior:
//   - MR must be docstatus=1 (Pending or Partially Ordered).
//   - Purpose must be 'purchase'.
//   - Each line's remaining qty = qty - ordered_qty. Lines with zero remaining
//     are skipped.
//   - The caller can narrow to specific (row_index, qty) tuples via in.Lines;
//     omit to copy every remaining line in full.
//   - Atomically updates each MR line's ordered_qty (and the MR's status via
//     recompute) so re-running CreatePOFromMR doesn't double-allocate.
//   - Writes a doc_link row so the resulting PO traces back to this MR.
func (s *Service) CreatePOFromMR(ctx context.Context, in CreatePOFromMRInput) (*purchaseorder.PurchaseOrder, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("material_request: unauthenticated")
	}
	if in.SupplierID == "" {
		return nil, errors.New("supplier_id: required")
	}

	mr, err := s.Get(ctx, in.MaterialRequestID)
	if err != nil {
		return nil, err
	}
	if mr.Docstatus != submittable.Submitted {
		return nil, errors.New("material_request: must be submitted before raising a PO")
	}
	if mr.Status == StatusStopped || mr.Status == StatusCancelled || mr.Status == StatusOrdered {
		return nil, fmt.Errorf("material_request: cannot create PO from status %s", mr.Status)
	}
	if mr.Purpose != PurposePurchase {
		return nil, fmt.Errorf("material_request: purpose must be 'purchase' to raise a PO (got %s)", mr.Purpose)
	}

	// Build the selector — default to "everything remaining".
	type want struct {
		rowIndex int
		qty      decimal.Decimal
	}
	wants := []want{}
	if len(in.Lines) == 0 {
		for _, l := range mr.Items {
			remaining := l.Qty.Sub(l.OrderedQty)
			if remaining.IsPositive() {
				wants = append(wants, want{rowIndex: l.RowIndex, qty: remaining})
			}
		}
	} else {
		byRow := map[int]MaterialRequestLine{}
		for _, l := range mr.Items {
			byRow[l.RowIndex] = l
		}
		for _, sel := range in.Lines {
			l, ok := byRow[sel.RowIndex]
			if !ok {
				return nil, fmt.Errorf("lines: row_index %d not found in MR", sel.RowIndex)
			}
			remaining := l.Qty.Sub(l.OrderedQty)
			var q decimal.Decimal
			if sel.Qty == "" {
				q = remaining
			} else {
				parsed, err := parseDec(sel.Qty)
				if err != nil {
					return nil, fmt.Errorf("lines[%d].qty: %w", sel.RowIndex, err)
				}
				if parsed.GreaterThan(remaining) {
					return nil, fmt.Errorf("lines[%d].qty: %s exceeds remaining %s", sel.RowIndex, parsed, remaining)
				}
				q = parsed
			}
			if q.IsPositive() {
				wants = append(wants, want{rowIndex: sel.RowIndex, qty: q})
			}
		}
	}
	if len(wants) == 0 {
		return nil, errors.New("material_request: nothing left to order from this MR")
	}

	// Build PO input mirroring the MR header + selected lines.
	byRow := map[int]MaterialRequestLine{}
	for _, l := range mr.Items {
		byRow[l.RowIndex] = l
	}
	poLines := make([]purchaseorder.LineInput, 0, len(wants))
	for _, w := range wants {
		ml := byRow[w.rowIndex]
		var requiredBy string
		if ml.RequiredByDate != nil {
			requiredBy = ml.RequiredByDate.Format("2006-01-02")
		}
		poLines = append(poLines, purchaseorder.LineInput{
			ItemID:         ml.ItemID,
			ItemCode:       ml.ItemCode,
			ItemName:       ml.ItemName,
			Description:    ml.Description,
			Qty:            w.qty.String(),
			UOM:            ml.UOM,
			Rate:           ml.Rate.String(),
			WarehouseID:    ml.WarehouseID,
			RequiredByDate: requiredBy,
		})
	}

	transactionDate := time.Now().UTC().Format("2006-01-02")
	var requiredByHdr string
	if mr.RequiredByDate != nil {
		requiredByHdr = mr.RequiredByDate.Format("2006-01-02")
	}

	po, err := s.po.CreateDraft(ctx, purchaseorder.CreateInput{
		CompanyID:       mr.CompanyID,
		SupplierID:      in.SupplierID,
		TransactionDate: transactionDate,
		RequiredByDate:  requiredByHdr,
		Items:           poLines,
		Remarks:         fmt.Sprintf("Raised from %s", mr.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("create_po: %w", err)
	}

	// Bump the MR's ordered_qty + recompute status + write a doc_link
	// row so traceability is preserved.
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		for _, w := range wants {
			if _, err := tx.Exec(ctx, `
				UPDATE material_request_item
				SET ordered_qty = ordered_qty + $1
				WHERE material_request_id = $2 AND row_index = $3`,
				w.qty, mr.ID, w.rowIndex); err != nil {
				return err
			}
		}
		// Reload to recompute against the just-updated counters.
		updated, err := load(ctx, tx, mr.ID)
		if err != nil {
			return err
		}
		nextStatus := recomputeStatus(updated)
		if _, err := tx.Exec(ctx,
			`UPDATE material_request SET status = $1, updated_by = $2 WHERE id = $3`,
			nextStatus, p.UserID, mr.ID); err != nil {
			return err
		}
		// doc_link: (material_request → purchase_order)
		totalQty := decimal.Zero
		totalAmt := decimal.Zero
		for _, w := range wants {
			ml := byRow[w.rowIndex]
			totalQty = totalQty.Add(w.qty)
			totalAmt = totalAmt.Add(w.qty.Mul(ml.Rate))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO doc_link (parent_doctype, parent_id, child_doctype, child_id, qty_linked, amount_linked)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (parent_doctype, parent_id, child_doctype, child_id)
			DO UPDATE SET qty_linked = EXCLUDED.qty_linked, amount_linked = EXCLUDED.amount_linked`,
			Doctype, mr.ID, purchaseorder.Doctype, po.ID, totalQty, totalAmt); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, mr.ID, p.UserID, "create_po",
			audit.Diff{After: map[string]any{"purchase_order_id": po.ID, "purchase_order_name": po.Name}})
	})
	if err != nil {
		return nil, err
	}
	return po, nil
}

// ---- Get / List ----

func (s *Service) Get(ctx context.Context, id string) (*MaterialRequest, error) {
	var out *MaterialRequest
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		mr, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = mr
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]MaterialRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM material_request
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
	out := make([]MaterialRequest, 0, len(ids))
	for _, id := range ids {
		mr, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *mr)
	}
	return out, nil
}

// ---- Pure helpers (exported for tests) ----

// RecomputeStatus returns the status an MR should hold based on its
// fulfilment counters + purpose. Manual states (Stopped, Cancelled) and
// pre-submit Draft are returned as-is.
func RecomputeStatus(mr *MaterialRequest) string {
	return recomputeStatus(mr)
}

func recomputeStatus(mr *MaterialRequest) string {
	switch mr.Status {
	case StatusDraft, StatusStopped, StatusCancelled:
		return mr.Status
	}

	consumed := func(l MaterialRequestLine) decimal.Decimal {
		// Which counter "satisfies" a line depends on purpose. We don't
		// mix counters across purposes because a single MR has one purpose.
		switch mr.Purpose {
		case PurposePurchase:
			// For purchase MRs, ordered_qty tracks PO allocation; received_qty
			// tracks GRN. We don't move to "Received" until GRN is in place.
			if !l.ReceivedQty.IsZero() && l.ReceivedQty.GreaterThanOrEqual(l.Qty) {
				return l.ReceivedQty
			}
			return l.OrderedQty
		case PurposeMaterialIssue:
			return l.IssuedQty
		case PurposeMaterialTransfer:
			return l.TransferredQty
		case PurposeManufacture:
			return l.OrderedQty // a WO is "ordered" in our model
		}
		return decimal.Zero
	}
	allConsumed := true
	anyConsumed := false
	for _, l := range mr.Items {
		c := consumed(l)
		if c.GreaterThan(decimal.Zero) {
			anyConsumed = true
		}
		if c.LessThan(l.Qty) {
			allConsumed = false
		}
	}

	if allConsumed {
		switch mr.Purpose {
		case PurposePurchase:
			// Distinguish "all qty has a PO" from "all qty actually received".
			allReceived := true
			for _, l := range mr.Items {
				if l.ReceivedQty.LessThan(l.Qty) {
					allReceived = false
					break
				}
			}
			if allReceived {
				return StatusReceived
			}
			return StatusOrdered
		case PurposeMaterialIssue:
			return StatusIssued
		case PurposeMaterialTransfer:
			return StatusTransferred
		case PurposeManufacture:
			return StatusOrdered
		}
	}
	if anyConsumed {
		return StatusPartiallyOrdered
	}
	return StatusPending
}

// hasDownstreamFulfilment — Cancel guard.
func hasDownstreamFulfilment(mr *MaterialRequest) bool {
	for _, l := range mr.Items {
		if !l.OrderedQty.IsZero() || !l.ReceivedQty.IsZero() ||
			!l.IssuedQty.IsZero() || !l.TransferredQty.IsZero() {
			return true
		}
	}
	return false
}

func validPurpose(p string) bool {
	switch p {
	case PurposePurchase, PurposeMaterialTransfer, PurposeMaterialIssue, PurposeManufacture:
		return true
	}
	return false
}

// ---- load/parse helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*MaterialRequest, error) {
	var (
		mr                                            MaterialRequest
		requiredBy, submittedAt, cancelledAt, stoppedAt *time.Time
		setWarehouse, fromWarehouse, remarks            *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, purpose, transaction_date, required_by_date,
		       set_warehouse_id, from_warehouse_id, status, remarks,
		       docstatus, submitted_at, cancelled_at, stopped_at,
		       created_at, updated_at
		FROM material_request WHERE id = $1`, id).
		Scan(&mr.ID, &mr.Name, &mr.CompanyID, &mr.Purpose, &mr.TransactionDate, &requiredBy,
			&setWarehouse, &fromWarehouse, &mr.Status, &remarks,
			&mr.Docstatus, &submittedAt, &cancelledAt, &stoppedAt,
			&mr.CreatedAt, &mr.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("material_request %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	mr.RequiredByDate = requiredBy
	mr.SubmittedAt = submittedAt
	mr.CancelledAt = cancelledAt
	mr.StoppedAt = stoppedAt
	if setWarehouse != nil {
		mr.SetWarehouseID = *setWarehouse
	}
	if fromWarehouse != nil {
		mr.FromWarehouseID = *fromWarehouse
	}
	if remarks != nil {
		mr.Remarks = *remarks
	}

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, coalesce(item_id,''), item_code, item_name, coalesce(description,''),
		       qty, uom, rate, amount, coalesce(warehouse_id,''), required_by_date,
		       ordered_qty, received_qty, issued_qty, transferred_qty
		FROM material_request_item WHERE material_request_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l MaterialRequestLine
		var lineReq *time.Time
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.ItemCode, &l.ItemName, &l.Description,
			&l.Qty, &l.UOM, &l.Rate, &l.Amount, &l.WarehouseID, &lineReq,
			&l.OrderedQty, &l.ReceivedQty, &l.IssuedQty, &l.TransferredQty); err != nil {
			return nil, err
		}
		l.RequiredByDate = lineReq
		mr.Items = append(mr.Items, l)
	}
	return &mr, nil
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
