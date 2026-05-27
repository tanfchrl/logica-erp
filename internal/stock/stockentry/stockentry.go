// Package stockentry implements the Stock Entry document.
//
// Purposes:
//   material_receipt   — qty in (target_warehouse only). GL: Dr Stock-In-Hand / Cr ... (the issuing account, e.g. supplier or capital)
//   material_issue     — qty out (source_warehouse only). GL: Dr <expense_account> / Cr Stock-In-Hand
//   material_transfer  — both source and target. GL: Dr <target stock acct> / Cr <source stock acct>
//   manufacture        — issue raw materials AND receive finished item in one entry.
//
// Phase 2 simplifies "receipt without a source" to require the caller to supply
// an expense_account_id on each line that names the contra account (typically
// the buying-side "stock received but not billed" account, or a JE-style equity
// account for opening balances). A Purchase Receipt doctype (Phase 2 next iteration)
// will wrap stock_entry to derive the contra from the linked Purchase Order/Invoice.
package stockentry

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
	"github.com/tandigital/logica-erp/internal/platform/customfield"
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
	Doctype     = "stock_entry"
	VoucherType = "Stock Entry"

	PurposeReceipt   = "material_receipt"
	PurposeIssue     = "material_issue"
	PurposeTransfer  = "material_transfer"
	PurposeManufacture = "manufacture"
)

type StockEntry struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	CompanyID           string             `json:"company_id"`
	PostingDate         time.Time          `json:"posting_date"`
	Purpose             string             `json:"purpose"`
	FiscalYearID        string             `json:"fiscal_year_id"`
	WorkOrderID         string             `json:"work_order_id,omitempty"`
	TotalOutgoingValue  decimal.Decimal    `json:"total_outgoing_value"`
	TotalIncomingValue  decimal.Decimal    `json:"total_incoming_value"`
	Remarks             string             `json:"remarks,omitempty"`
	Docstatus           submittable.Status `json:"docstatus"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	Items               []StockEntryLine   `json:"items"`
}

type StockEntryLine struct {
	ID                 string          `json:"id"`
	RowIndex           int             `json:"row_index"`
	ItemID             string          `json:"item_id"`
	Qty                decimal.Decimal `json:"qty"`
	UOM                string          `json:"uom"`
	SourceWarehouseID  string          `json:"source_warehouse_id,omitempty"`
	TargetWarehouseID  string          `json:"target_warehouse_id,omitempty"`
	BasicRate          decimal.Decimal `json:"basic_rate"`
	BasicAmount        decimal.Decimal `json:"basic_amount"`
	ValuationRate      decimal.Decimal `json:"valuation_rate"`
	Amount             decimal.Decimal `json:"amount"`
	CostCenterID       string          `json:"cost_center_id,omitempty"`
	ExpenseAccountID   string          `json:"expense_account_id,omitempty"`
}

type StockEntryCreateInput struct {
	CompanyID    string                  `json:"company_id,omitempty"`
	PostingDate  string                  `json:"posting_date"`
	Purpose      string                  `json:"purpose"`
	WorkOrderID  string                  `json:"work_order_id,omitempty"`
	Remarks      string                  `json:"remarks,omitempty"`
	Items        []StockEntryLineInput   `json:"items"`
	CustomFields map[string]any          `json:"custom_fields,omitempty"`
}

type StockEntryLineInput struct {
	ItemID            string `json:"item_id"`
	Qty               string `json:"qty"`
	UOM               string `json:"uom,omitempty"`
	SourceWarehouseID string `json:"source_warehouse_id,omitempty"`
	TargetWarehouseID string `json:"target_warehouse_id,omitempty"`
	BasicRate         string `json:"basic_rate,omitempty"`           // required for incoming when item has no prior balance
	CostCenterID      string `json:"cost_center_id,omitempty"`
	ExpenseAccountID  string `json:"expense_account_id,omitempty"`   // for material_issue, contra for material_receipt
}

type Service struct {
	db        *dbx.DB
	Approvals approvalChecker
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) CreateDraft(ctx context.Context, in StockEntryCreateInput) (*StockEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("stock_entry: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("stock_entry.company_id: required")
	}
	switch in.Purpose {
	case PurposeReceipt, PurposeIssue, PurposeTransfer, PurposeManufacture:
	default:
		return nil, fmt.Errorf("stock_entry.purpose: invalid %q", in.Purpose)
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("posting_date: %w", err)
	}
	if len(in.Items) == 0 {
		return nil, errors.New("stock_entry.items: at least one required")
	}

	// Per-purpose warehouse direction validation.
	for i, l := range in.Items {
		switch in.Purpose {
		case PurposeReceipt:
			if l.TargetWarehouseID == "" {
				return nil, fmt.Errorf("items[%d].target_warehouse_id: required for material_receipt", i)
			}
			if l.SourceWarehouseID != "" {
				return nil, fmt.Errorf("items[%d].source_warehouse_id: must be empty for material_receipt", i)
			}
		case PurposeIssue:
			if l.SourceWarehouseID == "" {
				return nil, fmt.Errorf("items[%d].source_warehouse_id: required for material_issue", i)
			}
			if l.TargetWarehouseID != "" {
				return nil, fmt.Errorf("items[%d].target_warehouse_id: must be empty for material_issue", i)
			}
		case PurposeTransfer:
			if l.SourceWarehouseID == "" || l.TargetWarehouseID == "" {
				return nil, fmt.Errorf("items[%d]: both source and target warehouses required for transfer", i)
			}
		case PurposeManufacture:
			// Per-line: either source (raw consumed) or target (finished produced) — caller arranges.
			if (l.SourceWarehouseID == "") == (l.TargetWarehouseID == "") {
				return nil, fmt.Errorf("items[%d]: exactly one of source/target_warehouse_id required for manufacture", i)
			}
		}
	}

	id := dbx.NewIDWithPrefix("ste")
	var out StockEntry
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
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

		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_entry (id, name, company_id, posting_date, purpose, fiscal_year_id, work_order_id, remarks, custom_fields, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`,
			id, name, in.CompanyID, pd, in.Purpose, fyID, nullable(in.WorkOrderID), nullable(in.Remarks), cf, p.UserID); err != nil {
			return err
		}

		for i, l := range in.Items {
			qty, err := decimal.NewFromString(strings.TrimSpace(l.Qty))
			if err != nil || !qty.IsPositive() {
				return fmt.Errorf("items[%d].qty: must be > 0", i)
			}
			var basicRate decimal.Decimal
			if l.BasicRate != "" {
				basicRate, err = decimal.NewFromString(strings.TrimSpace(l.BasicRate))
				if err != nil {
					return fmt.Errorf("items[%d].basic_rate: %w", i, err)
				}
			}
			uom := l.UOM
			if uom == "" {
				if err := tx.QueryRow(ctx, `SELECT stock_uom FROM item WHERE id = $1`, l.ItemID).Scan(&uom); err != nil {
					return fmt.Errorf("items[%d]: item lookup: %w", i, err)
				}
			}
			basicAmount := qty.Mul(basicRate).Round(money.Precision)
			rowID := dbx.NewIDWithPrefix("sei")
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_entry_item (
					id, stock_entry_id, row_index, item_id, qty, uom,
					source_warehouse_id, target_warehouse_id, basic_rate, basic_amount,
					cost_center_id, expense_account_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
				rowID, id, i+1, l.ItemID, qty, uom,
				nullable(l.SourceWarehouseID), nullable(l.TargetWarehouseID), basicRate, basicAmount,
				nullable(l.CostCenterID), nullable(l.ExpenseAccountID)); err != nil {
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

func (s *Service) Submit(ctx context.Context, id string) (*StockEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("stock_entry: unauthenticated")
	}
	var out StockEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		se, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if se.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Approvals != nil {
			outV, _ := se.TotalOutgoingValue.Float64()
			inV, _  := se.TotalIncomingValue.Float64()
			amount := outV
			if inV > amount {
				amount = inV
			}
			if err := s.Approvals.CheckSubmit(ctx, tx, "stock_entry", se.ID, se.Name, se.CompanyID,
				map[string]any{
					"total_outgoing_value": outV,
					"total_incoming_value": inV,
					"amount":               amount,
				}); err != nil {
				return err
			}
		}

		postingDT := se.PostingDate

		// For each line, post SLE and contribute to GL entries.
		entries := []ledger.Entry{}
		totalIncoming := decimal.Zero
		totalOutgoing := decimal.Zero

		for i := range se.Items {
			l := &se.Items[i]

			// OUTGOING (source side): always at FIFO-derived rate.
			if l.SourceWarehouseID != "" {
				rate, balQty, balVal, err := valuation.OutgoingRate(ctx, tx, l.ItemID, l.SourceWarehouseID, l.Qty)
				if err != nil {
					return fmt.Errorf("items[%d]: %w", i, err)
				}
				stockValue := l.Qty.Mul(rate).Round(money.Precision)
				if err := insertSLE(ctx, tx, p.UserID, se,
					l.ItemID, l.SourceWarehouseID, l.Qty.Neg(), balQty, rate, balVal, stockValue.Neg(), nil, postingDT); err != nil {
					return err
				}
				l.ValuationRate = rate
				l.Amount = stockValue
				totalOutgoing = totalOutgoing.Add(stockValue)

				// GL Cr stock account of source warehouse
				srcAcct, err := warehouseStockAccount(ctx, tx, l.SourceWarehouseID)
				if err != nil {
					return err
				}
				if srcAcct != "" {
					var cur string
					if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, srcAcct).Scan(&cur); err != nil {
						return err
					}
					entries = append(entries, ledger.Entry{
						AccountID:               srcAcct,
						CostCenterID:            l.CostCenterID,
						Credit:                  stockValue,
						AccountCurrency:         cur,
						CreditInAccountCurrency: stockValue,
						Remarks:                 fmt.Sprintf("%s — %s issue", se.Name, l.ItemID),
					})
				}
			}

			// INCOMING (target side): receipt at basic_rate (manual) or transferred-in at outgoing rate.
			if l.TargetWarehouseID != "" {
				incomingRate := l.BasicRate
				if !l.ValuationRate.IsZero() && l.SourceWarehouseID != "" {
					// Transfer: keep the source's outgoing rate for the receipt
					incomingRate = l.ValuationRate
				}
				if incomingRate.IsZero() {
					return fmt.Errorf("items[%d]: basic_rate required for incoming receipt with no prior balance", i)
				}
				balQty, balVal, err := valuation.IncomingBalance(ctx, tx, l.ItemID, l.TargetWarehouseID, l.Qty, incomingRate)
				if err != nil {
					return err
				}
				stockValue := l.Qty.Mul(incomingRate).Round(money.Precision)
				if err := insertSLE(ctx, tx, p.UserID, se,
					l.ItemID, l.TargetWarehouseID, l.Qty, balQty, incomingRate, balVal, stockValue, &incomingRate, postingDT); err != nil {
					return err
				}
				if l.ValuationRate.IsZero() {
					l.ValuationRate = incomingRate
				}
				l.Amount = stockValue
				totalIncoming = totalIncoming.Add(stockValue)

				// GL Dr stock account of target warehouse
				tgtAcct, err := warehouseStockAccount(ctx, tx, l.TargetWarehouseID)
				if err != nil {
					return err
				}
				if tgtAcct != "" {
					var cur string
					if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, tgtAcct).Scan(&cur); err != nil {
						return err
					}
					entries = append(entries, ledger.Entry{
						AccountID:              tgtAcct,
						CostCenterID:           l.CostCenterID,
						Debit:                  stockValue,
						AccountCurrency:        cur,
						DebitInAccountCurrency: stockValue,
						Remarks:                fmt.Sprintf("%s — %s receipt", se.Name, l.ItemID),
					})
				}
			}

			// Contra leg for material_issue: Dr the user-provided expense account.
			if se.Purpose == PurposeIssue {
				if l.ExpenseAccountID == "" {
					// Fall back to item_default
					_ = tx.QueryRow(ctx, `SELECT default_expense_account_id FROM item_default WHERE item_id = $1 AND company_id = $2`,
						l.ItemID, se.CompanyID).Scan(&l.ExpenseAccountID)
				}
				if l.ExpenseAccountID == "" {
					return fmt.Errorf("items[%d].expense_account_id: required for material_issue (no item default)", i)
				}
				var cur string
				if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, l.ExpenseAccountID).Scan(&cur); err != nil {
					return err
				}
				entries = append(entries, ledger.Entry{
					AccountID:              l.ExpenseAccountID,
					CostCenterID:           l.CostCenterID,
					Debit:                  l.Amount,
					AccountCurrency:        cur,
					DebitInAccountCurrency: l.Amount,
					Remarks:                fmt.Sprintf("%s — %s consumed", se.Name, l.ItemID),
				})
			}

			// Contra for material_receipt: Cr the user-provided contra/expense account
			// (e.g. "Stock Received But Not Billed" or capital).
			if se.Purpose == PurposeReceipt {
				if l.ExpenseAccountID == "" {
					return fmt.Errorf("items[%d].expense_account_id: required for material_receipt (the contra account, e.g. stock-received-but-not-billed or capital)", i)
				}
				var cur string
				if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, l.ExpenseAccountID).Scan(&cur); err != nil {
					return err
				}
				entries = append(entries, ledger.Entry{
					AccountID:               l.ExpenseAccountID,
					CostCenterID:            l.CostCenterID,
					Credit:                  l.Amount,
					AccountCurrency:         cur,
					CreditInAccountCurrency: l.Amount,
					Remarks:                 fmt.Sprintf("%s — %s receipt contra", se.Name, l.ItemID),
				})
			}
		}

		// For transfer + manufacture: both legs are on stock accounts already balanced by SLE pairing.
		// PostGL only if we accumulated at least one entry.
		if len(entries) > 0 {
			v := ledger.Voucher{
				Type: VoucherType, ID: se.ID, Name: se.Name,
				CompanyID: se.CompanyID, PostingDate: se.PostingDate, FiscalYearID: se.FiscalYearID, CreatedBy: p.UserID,
			}
			if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE stock_entry SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			       total_outgoing_value = $2, total_incoming_value = $3, updated_by = $1
			WHERE id = $4`, p.UserID, totalOutgoing, totalIncoming, id); err != nil {
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

func (s *Service) Cancel(ctx context.Context, id string) (*StockEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("stock_entry: unauthenticated")
	}
	var out StockEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		se, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if se.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		// Cancel GL entries (standard reversal).
		if _, err := ledger.CancelGL(ctx, tx, VoucherType, id, p.UserID); err != nil {
			// PostGL may not have been called (pure transfers without GL); ignore.
			if !strings.Contains(err.Error(), "nothing to cancel") {
				return err
			}
		}
		// Mark SLE rows as cancelled (we don't add reversing SLE rows since
		// downstream FIFO reads would otherwise see inconsistent balances).
		if _, err := tx.Exec(ctx, `UPDATE stock_ledger_entry SET is_cancelled = true WHERE voucher_type = $1 AND voucher_id = $2`,
			VoucherType, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE stock_entry SET docstatus = 2, cancelled_at = now(), cancelled_by = $1, updated_by = $1 WHERE id = $2`,
			p.UserID, id); err != nil {
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

func (s *Service) Get(ctx context.Context, id string) (*StockEntry, error) {
	var out *StockEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		se, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = se
		return nil
	})
	return out, err
}

// ---- helpers ----

func insertSLE(ctx context.Context, tx pgx.Tx, userID string, se *StockEntry,
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
		id, se.CompanyID, postingDT, itemID, warehouseID,
		actualQty, qtyAfter, valRate, stockValAfter, stockValueDiff,
		incoming, VoucherType, se.ID, se.Name, userID)
	return err
}

func warehouseStockAccount(ctx context.Context, tx pgx.Tx, warehouseID string) (string, error) {
	var acct *string
	if err := tx.QueryRow(ctx, `SELECT account_id FROM warehouse WHERE id = $1`, warehouseID).Scan(&acct); err != nil {
		return "", err
	}
	if acct == nil {
		return "", nil
	}
	return *acct, nil
}

func load(ctx context.Context, tx pgx.Tx, id string) (*StockEntry, error) {
	var (
		se          StockEntry
		workOrder   *string
		remarks     *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, posting_date, purpose, fiscal_year_id, work_order_id,
		       total_outgoing_value, total_incoming_value, remarks, docstatus, created_at, updated_at
		FROM stock_entry WHERE id = $1`, id).
		Scan(&se.ID, &se.Name, &se.CompanyID, &se.PostingDate, &se.Purpose, &se.FiscalYearID, &workOrder,
			&se.TotalOutgoingValue, &se.TotalIncomingValue, &remarks, &se.Docstatus, &se.CreatedAt, &se.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("stock_entry %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if workOrder != nil {
		se.WorkOrderID = *workOrder
	}
	if remarks != nil {
		se.Remarks = *remarks
	}
	rows, err := tx.Query(ctx, `
		SELECT id, row_index, item_id, qty, uom,
		       coalesce(source_warehouse_id,''), coalesce(target_warehouse_id,''),
		       coalesce(basic_rate,0), coalesce(basic_amount,0),
		       coalesce(valuation_rate,0), coalesce(amount,0),
		       coalesce(cost_center_id,''), coalesce(expense_account_id,'')
		FROM stock_entry_item WHERE stock_entry_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l StockEntryLine
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.Qty, &l.UOM,
			&l.SourceWarehouseID, &l.TargetWarehouseID,
			&l.BasicRate, &l.BasicAmount, &l.ValuationRate, &l.Amount,
			&l.CostCenterID, &l.ExpenseAccountID); err != nil {
			return nil, err
		}
		se.Items = append(se.Items, l)
	}
	return &se, rows.Err()
}

func pickFiscalYear(ctx context.Context, tx pgx.Tx, companyID string, pd time.Time) (string, error) {
	var fyID string
	err := tx.QueryRow(ctx, `
		SELECT fy.id FROM fiscal_year fy
		JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
		WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
		ORDER BY fy.start_date DESC LIMIT 1`, companyID, pd).Scan(&fyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no fiscal year covers %s", pd.Format("2006-01-02"))
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
		return "", "", fmt.Errorf("no naming series for %s", doctype)
	}
	return id, pat, err
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
		OperationID:   "create-stock-entry",
		Method:        http.MethodPost,
		Path:          "/stock/stock-entries",
		Summary:       "Create a Stock Entry draft",
		Tags:          []string{"Stock / Stock Entry"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *steCreateIn) (*steOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		se, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &steOut{Body: *se}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-stock-entry",
		Method:      http.MethodPost,
		Path:        "/stock/stock-entries/{id}/submit",
		Summary:     "Submit a Stock Entry (posts SLE + GL)",
		Tags:        []string{"Stock / Stock Entry"},
	}, func(ctx context.Context, in *steGetIn) (*steOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		se, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &steOut{Body: *se}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "cancel-stock-entry",
		Method:      http.MethodPost,
		Path:        "/stock/stock-entries/{id}/cancel",
		Summary:     "Cancel a Stock Entry",
		Tags:        []string{"Stock / Stock Entry"},
	}, func(ctx context.Context, in *steGetIn) (*steOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCancel); err != nil {
			return nil, httpx.MapError(err)
		}
		se, err := h.Service.Cancel(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &steOut{Body: *se}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-stock-entry",
		Method:      http.MethodGet,
		Path:        "/stock/stock-entries/{id}",
		Summary:     "Get a Stock Entry",
		Tags:        []string{"Stock / Stock Entry"},
	}, func(ctx context.Context, in *steGetIn) (*steOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		se, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &steOut{Body: *se}, nil
	})
}

type (
	steCreateIn struct{ Body StockEntryCreateInput }
	steOut      struct{ Body StockEntry }
	steGetIn    struct {
		ID string `path:"id"`
	}
)
