// Package purchaseinvoice implements the Purchase Invoice document — the mirror of Sales Invoice.
//
// Submit posts:
//   Dr <expense_account>    base_amount (per item)
//   Dr <tax.account_id>     base_tax_amount (per tax row)         (typically Pajak Masukan — an asset)
//   Cr Payable              base_grand_total      party=supplier
//
// Withholding rows are stored at create time but NOT posted to GL until the
// Payment Entry that references this invoice is submitted. PE.pay handles the
// withholding-payable posting on the buyer side.
package purchaseinvoice

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
	"github.com/tandigital/logica-erp/internal/platform/ledger"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
	"github.com/tandigital/logica-erp/internal/platform/tax"
)

const (
	Doctype     = "purchase_invoice"
	VoucherType = "Purchase Invoice"
)

// ---- domain types ----

type PurchaseInvoice struct {
	ID                       string                            `json:"id"`
	Name                     string                            `json:"name"`
	CompanyID                string                            `json:"company_id"`
	SupplierID               string                            `json:"supplier_id"`
	PostingDate              time.Time                         `json:"posting_date"`
	DueDate                  time.Time                         `json:"due_date"`
	FiscalYearID             string                            `json:"fiscal_year_id"`
	Currency                 string                            `json:"currency"`
	ExchangeRate             decimal.Decimal                   `json:"exchange_rate"`
	TaxTemplateID            string                            `json:"tax_template_id,omitempty"`
	SupplierInvoiceNo        string                            `json:"supplier_invoice_no,omitempty"`
	SupplierInvoiceDate      *time.Time                        `json:"supplier_invoice_date,omitempty"`
	BillNo                   string                            `json:"bill_no,omitempty"`
	NetTotal                 decimal.Decimal                   `json:"net_total"`
	TotalTaxesAndCharges     decimal.Decimal                   `json:"total_taxes_and_charges"`
	GrandTotal               decimal.Decimal                   `json:"grand_total"`
	PaidAmount               decimal.Decimal                   `json:"paid_amount"`
	OutstandingAmount        decimal.Decimal                   `json:"outstanding_amount"`
	BaseGrandTotal           decimal.Decimal                   `json:"base_grand_total"`
	BaseOutstandingAmount    decimal.Decimal                   `json:"base_outstanding_amount"`
	Remarks                  string                            `json:"remarks,omitempty"`
	PayableAccountID         string                            `json:"payable_account_id"`
	IsReturn                 bool                              `json:"is_return"`
	ReturnAgainst            string                            `json:"return_against,omitempty"`
	Docstatus                submittable.Status                `json:"docstatus"`
	SubmittedAt              *time.Time                        `json:"submitted_at,omitempty"`
	CancelledAt              *time.Time                        `json:"cancelled_at,omitempty"`
	CreatedAt                time.Time                         `json:"created_at"`
	UpdatedAt                time.Time                         `json:"updated_at"`
	Items                    []PurchaseInvoiceLine             `json:"items"`
	Taxes                    []PurchaseInvoiceTaxRow           `json:"taxes"`
	Withholding              []PurchaseInvoiceWithholdingRow   `json:"withholding,omitempty"`
}

type PurchaseInvoiceLine struct {
	ID               string          `json:"id"`
	RowIndex         int             `json:"row_index"`
	ItemID           string          `json:"item_id,omitempty"`
	ItemCode         string          `json:"item_code"`
	ItemName         string          `json:"item_name"`
	Description      string          `json:"description,omitempty"`
	Qty              decimal.Decimal `json:"qty"`
	UOM              string          `json:"uom"`
	Rate             decimal.Decimal `json:"rate"`
	Amount           decimal.Decimal `json:"amount"`
	ExpenseAccountID string          `json:"expense_account_id"`
	CostCenterID     string          `json:"cost_center_id,omitempty"`
	TaxAmount        decimal.Decimal `json:"tax_amount"`
	Total            decimal.Decimal `json:"total"`
	BaseAmount       decimal.Decimal `json:"base_amount"`
	BaseTaxAmount    decimal.Decimal `json:"base_tax_amount"`
	BaseTotal        decimal.Decimal `json:"base_total"`
}

type PurchaseInvoiceTaxRow struct {
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

type PurchaseInvoiceWithholdingRow struct {
	ID                   string          `json:"id"`
	WithholdingTaxTypeID string          `json:"withholding_tax_type_id"`
	Rate                 decimal.Decimal `json:"rate"`
	Amount               decimal.Decimal `json:"amount"`
	BaseAmount           decimal.Decimal `json:"base_amount"`
	AccountID            string          `json:"account_id"`
}

// ---- input ----

type PurchaseInvoiceCreateInput struct {
	CompanyID           string                              `json:"company_id,omitempty"`
	SupplierID          string                              `json:"supplier_id"`
	PostingDate         string                              `json:"posting_date"`
	DueDate             string                              `json:"due_date,omitempty"`
	Currency            string                              `json:"currency,omitempty"`
	ExchangeRate        string                              `json:"exchange_rate,omitempty"`
	TaxTemplateID       string                              `json:"tax_template_id,omitempty"`
	SupplierInvoiceNo   string                              `json:"supplier_invoice_no,omitempty"`
	SupplierInvoiceDate string                              `json:"supplier_invoice_date,omitempty"`
	BillNo              string                              `json:"bill_no,omitempty"`
	Remarks             string                              `json:"remarks,omitempty"`
	PayableAccountID    string                              `json:"payable_account_id,omitempty"`
	IsReturn            bool                                `json:"is_return,omitempty"`
	ReturnAgainst       string                              `json:"return_against,omitempty"`
	Items               []PurchaseInvoiceLineInput          `json:"items"`
	Withholding         []PurchaseInvoiceWithholdingInput   `json:"withholding,omitempty"`
	CustomFields        map[string]any                      `json:"custom_fields,omitempty"`
}

type PurchaseInvoiceLineInput struct {
	ItemID           string `json:"item_id,omitempty"`
	ItemCode         string `json:"item_code,omitempty"`
	ItemName         string `json:"item_name,omitempty"`
	Description      string `json:"description,omitempty"`
	Qty              string `json:"qty"`
	UOM              string `json:"uom,omitempty"`
	Rate             string `json:"rate"`
	ExpenseAccountID string `json:"expense_account_id,omitempty"`
	CostCenterID     string `json:"cost_center_id,omitempty"`
}

type PurchaseInvoiceWithholdingInput struct {
	WithholdingTaxTypeID string `json:"withholding_tax_type_id"`
	Amount               string `json:"amount,omitempty"`
}

type Service struct {
	db *dbx.DB
	// Approvals is optional. When set, Submit() consults active approval_rules
	// for this doctype + company; missing approvals block submit.
	Approvals approvalChecker
}

// approvalChecker is the narrow contract we need from workflow.ApprovalEngine.
// Defined locally so this package doesn't depend on the workflow package.
type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in PurchaseInvoiceCreateInput) (*PurchaseInvoice, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_invoice: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("purchase_invoice.company_id: required")
	}
	if in.SupplierID == "" {
		return nil, errors.New("purchase_invoice.supplier_id: required")
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("purchase_invoice.posting_date: %w", err)
	}
	var dueDate time.Time
	if in.DueDate == "" {
		dueDate = pd.AddDate(0, 0, 30)
	} else {
		dueDate, err = time.Parse("2006-01-02", in.DueDate)
		if err != nil {
			return nil, fmt.Errorf("purchase_invoice.due_date: %w", err)
		}
	}
	if len(in.Items) == 0 {
		return nil, errors.New("purchase_invoice.items: at least one required")
	}

	rate := decimal.NewFromInt(1)
	if in.ExchangeRate != "" {
		r, err := decimal.NewFromString(in.ExchangeRate)
		if err != nil {
			return nil, fmt.Errorf("purchase_invoice.exchange_rate: %w", err)
		}
		if !r.IsPositive() {
			return nil, errors.New("purchase_invoice.exchange_rate: must be > 0")
		}
		rate = r
	}

	id := dbx.NewIDWithPrefix("pi")
	var out PurchaseInvoice
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

		payable := in.PayableAccountID
		if payable == "" {
			err := tx.QueryRow(ctx, `
				SELECT coalesce(sd.default_payable_account_id, co.default_payable_account_id)
				FROM company co
				LEFT JOIN supplier_default sd ON sd.supplier_id = $2 AND sd.company_id = co.id
				WHERE co.id = $1`, in.CompanyID, in.SupplierID).Scan(&payable)
			if err != nil || payable == "" {
				return errors.New("purchase_invoice.payable_account_id: not provided and no default configured")
			}
		}

		taxTemplateID := in.TaxTemplateID
		if taxTemplateID == "" {
			_ = tx.QueryRow(ctx,
				`SELECT default_tax_template_id FROM supplier_default WHERE supplier_id = $1 AND company_id = $2`,
				in.SupplierID, in.CompanyID).Scan(&taxTemplateID)
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

		draftLines := make([]PurchaseInvoiceLine, len(in.Items))
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

			itemCode, itemName, uom, expAcc := ln.ItemCode, ln.ItemName, ln.UOM, ln.ExpenseAccountID
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
				if expAcc == "" {
					_ = tx.QueryRow(ctx,
						`SELECT default_expense_account_id FROM item_default WHERE item_id = $1 AND company_id = $2`,
						ln.ItemID, in.CompanyID).Scan(&expAcc)
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
			if expAcc == "" {
				if err := tx.QueryRow(ctx,
					`SELECT default_expense_account_id FROM company WHERE id = $1`, in.CompanyID).Scan(&expAcc); err != nil || expAcc == "" {
					return fmt.Errorf("items[%d].expense_account_id: not provided and no default configured", i)
				}
			}
			rowID := dbx.NewIDWithPrefix("pii")
			draftLines[i] = PurchaseInvoiceLine{
				ID: rowID, RowIndex: i + 1,
				ItemID: ln.ItemID, ItemCode: itemCode, ItemName: itemName, Description: ln.Description,
				Qty: qty, UOM: uom, Rate: rt, Amount: amount,
				ExpenseAccountID: expAcc, CostCenterID: ln.CostCenterID,
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
				return errors.New("purchase_invoice.tax_template: must be a purchase template")
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

		taxRows := make([]PurchaseInvoiceTaxRow, len(taxResult.TaxRows))
		for i, tr := range taxResult.TaxRows {
			taxRows[i] = PurchaseInvoiceTaxRow{
				ID: dbx.NewIDWithPrefix("pit"), RowIndex: i + 1,
				AccountID: tr.AccountID, Description: tr.Description,
				Rate: tr.Rate, ChargeType: string(tr.ChargeType),
				IncludedInBasicRate: tr.IncludedInBasicRate,
				TaxAmount:           tr.TaxAmount,
				BaseTaxAmount:       tr.TaxAmount.Mul(rate).Round(money.Precision),
				CostCenterID:        tr.CostCenterID,
			}
		}

		whRows, err := buildWithholding(ctx, tx, in.Withholding, taxResult.NetTotal, rate)
		if err != nil {
			return err
		}

		netTotal := taxResult.NetTotal
		taxTotal := taxResult.TaxTotal
		grand := taxResult.GrandTotal
		baseGrand := grand.Mul(rate).Round(money.Precision)

		if in.IsReturn && in.ReturnAgainst != "" {
			var origSupplier string
			var origDocstatus int16
			if err := tx.QueryRow(ctx, `SELECT supplier_id, docstatus FROM purchase_invoice WHERE id = $1`,
				in.ReturnAgainst).Scan(&origSupplier, &origDocstatus); err != nil {
				return fmt.Errorf("return_against: %w", err)
			}
			if origDocstatus != 1 {
				return errors.New("return_against: original purchase invoice must be submitted")
			}
			if origSupplier != in.SupplierID {
				return errors.New("return_against: supplier must match the original invoice")
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_invoice (
				id, name, company_id, supplier_id, posting_date, due_date, fiscal_year_id,
				currency, exchange_rate, tax_template_id, supplier_invoice_no, supplier_invoice_date, bill_no,
				net_total, total_taxes_and_charges, grand_total,
				paid_amount, outstanding_amount,
				base_net_total, base_total_taxes_and_charges, base_grand_total,
				base_paid_amount, base_outstanding_amount,
				remarks, payable_account_id, is_return, return_against,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,
			          $14,$15,$16,
			          0, $16,
			          $17,$18,$19,
			          0, $19,
			          $20,$21,$22,$23,
			          $24,$25,$25)`,
			id, name, in.CompanyID, in.SupplierID, pd, dueDate, fyID,
			currency, rate, nullable(taxTemplateID), nullable(in.SupplierInvoiceNo), nullableDate(in.SupplierInvoiceDate), nullable(in.BillNo),
			netTotal, taxTotal, grand,
			netTotal.Mul(rate).Round(money.Precision),
			taxTotal.Mul(rate).Round(money.Precision),
			baseGrand,
			nullable(in.Remarks), payable, in.IsReturn, nullable(in.ReturnAgainst),
			cf, p.UserID); err != nil {
			return err
		}

		for _, l := range draftLines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_invoice_item (
					id, purchase_invoice_id, row_index, item_id, item_code, item_name, description,
					qty, uom, rate, amount, expense_account_id, cost_center_id,
					tax_amount, total, base_amount, base_tax_amount, base_total
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
				l.ID, id, l.RowIndex, nullable(l.ItemID), l.ItemCode, l.ItemName, nullable(l.Description),
				l.Qty, l.UOM, l.Rate, l.Amount, l.ExpenseAccountID, nullable(l.CostCenterID),
				l.TaxAmount, l.Total, l.BaseAmount, l.BaseTaxAmount, l.BaseTotal); err != nil {
				return err
			}
		}
		for _, tr := range taxRows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_invoice_tax (
					id, purchase_invoice_id, row_index, account_id, description, rate,
					charge_type, included_in_basic_rate, tax_amount, base_tax_amount, cost_center_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				tr.ID, id, tr.RowIndex, tr.AccountID, tr.Description, tr.Rate,
				tr.ChargeType, tr.IncludedInBasicRate, tr.TaxAmount, tr.BaseTaxAmount, nullable(tr.CostCenterID)); err != nil {
				return err
			}
		}
		for _, w := range whRows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_invoice_withholding (
					id, purchase_invoice_id, withholding_tax_type_id, rate, amount, base_amount, account_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				w.ID, id, w.WithholdingTaxTypeID, w.Rate, w.Amount, w.BaseAmount, w.AccountID); err != nil {
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

func (s *Service) Submit(ctx context.Context, id string) (*PurchaseInvoice, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_invoice: unauthenticated")
	}
	var out PurchaseInvoice
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pi, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pi.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if pi.GrandTotal.IsZero() {
			return errors.New("purchase_invoice: grand_total is zero, nothing to post")
		}

		// Approval gate. If the workspace has active approval_rules for PI,
		// every matching rule must have an approved request before submit
		// can complete. Pending requests are created on the first attempt.
		if s.Approvals != nil {
			gt, _ := pi.GrandTotal.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, "purchase_invoice", pi.ID, pi.Name, pi.CompanyID,
				map[string]any{"grand_total": gt}); err != nil {
				return err
			}
		}

		// For a Debit Note (is_return=true), swap Dr/Cr polarity on every leg.
		dr := func(d decimal.Decimal) decimal.Decimal { if pi.IsReturn { return decimal.Zero }; return d }
		cr := func(d decimal.Decimal) decimal.Decimal { if pi.IsReturn { return decimal.Zero }; return d }
		drR := func(d decimal.Decimal) decimal.Decimal { if pi.IsReturn { return d }; return decimal.Zero }
		crR := func(d decimal.Decimal) decimal.Decimal { if pi.IsReturn { return d }; return decimal.Zero }

		entries := make([]ledger.Entry, 0, len(pi.Items)+len(pi.Taxes)+1)
		entries = append(entries, ledger.Entry{
			AccountID:               pi.PayableAccountID,
			PartyType:               ledger.PartySupplier,
			PartyID:                 pi.SupplierID,
			Debit:                   drR(pi.BaseGrandTotal),
			Credit:                  cr(pi.BaseGrandTotal),
			AccountCurrency:         pi.Currency,
			DebitInAccountCurrency:  drR(pi.GrandTotal),
			CreditInAccountCurrency: cr(pi.GrandTotal),
			Against:                 "Expense",
			Remarks:                 pi.Name,
		})
		for _, l := range pi.Items {
			var acctCurrency string
			if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, l.ExpenseAccountID).Scan(&acctCurrency); err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID:               l.ExpenseAccountID,
				CostCenterID:            l.CostCenterID,
				Debit:                   dr(l.BaseAmount),
				Credit:                  crR(l.BaseAmount),
				AccountCurrency:         acctCurrency,
				DebitInAccountCurrency:  dr(l.Amount),
				CreditInAccountCurrency: crR(l.Amount),
				Against:                 pi.PayableAccountID,
				Remarks:                 fmt.Sprintf("%s — %s", pi.Name, l.ItemCode),
			})
		}
		for _, tr := range pi.Taxes {
			if tr.BaseTaxAmount.IsZero() {
				continue
			}
			var acctCurrency string
			if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, tr.AccountID).Scan(&acctCurrency); err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID:               tr.AccountID,
				CostCenterID:            tr.CostCenterID,
				Debit:                   dr(tr.BaseTaxAmount),
				Credit:                  crR(tr.BaseTaxAmount),
				AccountCurrency:         acctCurrency,
				DebitInAccountCurrency:  dr(tr.TaxAmount),
				CreditInAccountCurrency: crR(tr.TaxAmount),
				Against:                 pi.PayableAccountID,
				Remarks:                 fmt.Sprintf("%s — %s", pi.Name, tr.Description),
			})
		}

		v := ledger.Voucher{
			Type: VoucherType, ID: pi.ID, Name: pi.Name,
			CompanyID: pi.CompanyID, PostingDate: pi.PostingDate, FiscalYearID: pi.FiscalYearID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE purchase_invoice SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			       outstanding_amount = grand_total,
			       base_outstanding_amount = base_grand_total,
			       updated_by = $1
			WHERE id = $2`, p.UserID, id); err != nil {
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

func (s *Service) Cancel(ctx context.Context, id string) (*PurchaseInvoice, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("purchase_invoice: unauthenticated")
	}
	var out PurchaseInvoice
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pi, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pi.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if !pi.PaidAmount.IsZero() {
			return errors.New("purchase_invoice: cannot cancel an invoice with payments; cancel the Payment Entry first")
		}
		if _, err := ledger.CancelGL(ctx, tx, VoucherType, id, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE purchase_invoice SET docstatus = 2, cancelled_at = now(), cancelled_by = $1,
			       outstanding_amount = 0, base_outstanding_amount = 0, updated_by = $1
			WHERE id = $2`, p.UserID, id); err != nil {
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

func (s *Service) Get(ctx context.Context, id string) (*PurchaseInvoice, error) {
	var out *PurchaseInvoice
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pi, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = pi
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]PurchaseInvoice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM purchase_invoice WHERE company_id = $1 ORDER BY posting_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]PurchaseInvoice, 0, len(ids))
	for _, id := range ids {
		pi, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *pi)
	}
	return out, nil
}

// ---- internal helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*PurchaseInvoice, error) {
	var (
		pi                              PurchaseInvoice
		submittedAt, cancelledAt        *time.Time
		supplierInvoiceDate             *time.Time
		taxTemplateID, supplierInvoiceNo *string
		billNo, remarks, returnAgainst   *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, supplier_id, posting_date, due_date, fiscal_year_id,
		       currency, exchange_rate, tax_template_id, supplier_invoice_no, supplier_invoice_date, bill_no,
		       net_total, total_taxes_and_charges, grand_total, paid_amount, outstanding_amount,
		       base_grand_total, base_outstanding_amount, remarks, payable_account_id,
		       is_return, return_against, docstatus, submitted_at, cancelled_at, created_at, updated_at
		FROM purchase_invoice WHERE id = $1`, id).
		Scan(&pi.ID, &pi.Name, &pi.CompanyID, &pi.SupplierID, &pi.PostingDate, &pi.DueDate, &pi.FiscalYearID,
			&pi.Currency, &pi.ExchangeRate, &taxTemplateID, &supplierInvoiceNo, &supplierInvoiceDate, &billNo,
			&pi.NetTotal, &pi.TotalTaxesAndCharges, &pi.GrandTotal, &pi.PaidAmount, &pi.OutstandingAmount,
			&pi.BaseGrandTotal, &pi.BaseOutstandingAmount, &remarks, &pi.PayableAccountID,
			&pi.IsReturn, &returnAgainst, &pi.Docstatus, &submittedAt, &cancelledAt, &pi.CreatedAt, &pi.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("purchase_invoice %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if taxTemplateID != nil {
		pi.TaxTemplateID = *taxTemplateID
	}
	if supplierInvoiceNo != nil {
		pi.SupplierInvoiceNo = *supplierInvoiceNo
	}
	pi.SupplierInvoiceDate = supplierInvoiceDate
	if billNo != nil {
		pi.BillNo = *billNo
	}
	if remarks != nil {
		pi.Remarks = *remarks
	}
	if returnAgainst != nil {
		pi.ReturnAgainst = *returnAgainst
	}
	pi.SubmittedAt = submittedAt
	pi.CancelledAt = cancelledAt

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, coalesce(item_id,''), item_code, item_name, coalesce(description,''),
		       qty, uom, rate, amount, expense_account_id, coalesce(cost_center_id,''),
		       tax_amount, total, base_amount, base_tax_amount, base_total
		FROM purchase_invoice_item WHERE purchase_invoice_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l PurchaseInvoiceLine
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.ItemCode, &l.ItemName, &l.Description,
			&l.Qty, &l.UOM, &l.Rate, &l.Amount, &l.ExpenseAccountID, &l.CostCenterID,
			&l.TaxAmount, &l.Total, &l.BaseAmount, &l.BaseTaxAmount, &l.BaseTotal); err != nil {
			rows.Close()
			return nil, err
		}
		pi.Items = append(pi.Items, l)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT id, row_index, account_id, description, rate, charge_type, included_in_basic_rate,
		       tax_amount, base_tax_amount, coalesce(cost_center_id,'')
		FROM purchase_invoice_tax WHERE purchase_invoice_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t PurchaseInvoiceTaxRow
		if err := rows.Scan(&t.ID, &t.RowIndex, &t.AccountID, &t.Description, &t.Rate,
			&t.ChargeType, &t.IncludedInBasicRate, &t.TaxAmount, &t.BaseTaxAmount, &t.CostCenterID); err != nil {
			rows.Close()
			return nil, err
		}
		pi.Taxes = append(pi.Taxes, t)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT id, withholding_tax_type_id, rate, amount, base_amount, account_id
		FROM purchase_invoice_withholding WHERE purchase_invoice_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w PurchaseInvoiceWithholdingRow
		if err := rows.Scan(&w.ID, &w.WithholdingTaxTypeID, &w.Rate, &w.Amount, &w.BaseAmount, &w.AccountID); err != nil {
			return nil, err
		}
		pi.Withholding = append(pi.Withholding, w)
	}
	return &pi, nil
}

func buildWithholding(ctx context.Context, tx pgx.Tx, in []PurchaseInvoiceWithholdingInput, netTotal, exchangeRate decimal.Decimal) ([]PurchaseInvoiceWithholdingRow, error) {
	out := make([]PurchaseInvoiceWithholdingRow, 0, len(in))
	for i, w := range in {
		if w.WithholdingTaxTypeID == "" {
			return nil, fmt.Errorf("withholding[%d].withholding_tax_type_id: required", i)
		}
		var (
			rate    decimal.Decimal
			account string
		)
		if err := tx.QueryRow(ctx, `SELECT rate, account_id FROM withholding_tax_type WHERE id = $1 AND is_deleted = false`,
			w.WithholdingTaxTypeID).Scan(&rate, &account); err != nil {
			return nil, fmt.Errorf("withholding[%d]: lookup type: %w", i, err)
		}
		var amount decimal.Decimal
		if w.Amount != "" {
			a, err := parseDec(w.Amount)
			if err != nil {
				return nil, fmt.Errorf("withholding[%d].amount: %w", i, err)
			}
			amount = a
		} else {
			amount = netTotal.Mul(rate).Div(decimal.NewFromInt(100)).Round(money.Precision)
		}
		out = append(out, PurchaseInvoiceWithholdingRow{
			ID:                   dbx.NewIDWithPrefix("piw"),
			WithholdingTaxTypeID: w.WithholdingTaxTypeID,
			Rate:                 rate,
			Amount:               amount,
			BaseAmount:           amount.Mul(exchangeRate).Round(money.Precision),
			AccountID:            account,
		})
	}
	return out, nil
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

func nullableDate(s string) any {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return t
}
