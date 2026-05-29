// Package salesinvoice implements the Sales Invoice document.
//
// Submit posts:
//   Dr Receivable          base_grand_total      party=customer
//   Cr <income_account>    base_amount (per item)
//   Cr <tax.account_id>    base_tax_amount (per tax row)
//
// Withholding rows are stored at create time but NOT posted to GL until the
// Payment Entry that references this invoice is submitted.
package salesinvoice

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
	Doctype     = "sales_invoice"
	VoucherType = "Sales Invoice"
)

// ---- domain types ----

type SalesInvoice struct {
	ID                       string                          `json:"id"`
	Name                     string                          `json:"name"`
	CompanyID                string                          `json:"company_id"`
	CustomerID               string                          `json:"customer_id"`
	PostingDate              time.Time                       `json:"posting_date"`
	DueDate                  time.Time                       `json:"due_date"`
	FiscalYearID             string                          `json:"fiscal_year_id"`
	Currency                 string                          `json:"currency"`
	ExchangeRate             decimal.Decimal                 `json:"exchange_rate"`
	TaxTemplateID            string                          `json:"tax_template_id,omitempty"`
	TaxInvoiceNumber         string                          `json:"tax_invoice_number,omitempty"`
	NetTotal                 decimal.Decimal                 `json:"net_total"`
	TotalTaxesAndCharges     decimal.Decimal                 `json:"total_taxes_and_charges"`
	GrandTotal               decimal.Decimal                 `json:"grand_total"`
	PaidAmount               decimal.Decimal                 `json:"paid_amount"`
	OutstandingAmount        decimal.Decimal                 `json:"outstanding_amount"`
	BaseGrandTotal           decimal.Decimal                 `json:"base_grand_total"`
	BaseOutstandingAmount    decimal.Decimal                 `json:"base_outstanding_amount"`
	Remarks                  string                          `json:"remarks,omitempty"`
	ReceivableAccountID      string                          `json:"receivable_account_id"`
	IsReturn                 bool                            `json:"is_return"`
	ReturnAgainst            string                          `json:"return_against,omitempty"`
	Docstatus                submittable.Status              `json:"docstatus"`
	SubmittedAt              *time.Time                      `json:"submitted_at,omitempty"`
	CancelledAt              *time.Time                      `json:"cancelled_at,omitempty"`
	CreatedAt                time.Time                       `json:"created_at"`
	UpdatedAt                time.Time                       `json:"updated_at"`
	Items                    []SalesInvoiceLine              `json:"items"`
	Taxes                    []SalesInvoiceTaxRow            `json:"taxes"`
	Withholding              []SalesInvoiceWithholdingRow    `json:"withholding,omitempty"`
}

type SalesInvoiceLine struct {
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
	IncomeAccountID string          `json:"income_account_id"`
	CostCenterID    string          `json:"cost_center_id,omitempty"`
	TaxAmount       decimal.Decimal `json:"tax_amount"`
	Total           decimal.Decimal `json:"total"`
	BaseAmount      decimal.Decimal `json:"base_amount"`
	BaseTaxAmount   decimal.Decimal `json:"base_tax_amount"`
	BaseTotal       decimal.Decimal `json:"base_total"`
}

type SalesInvoiceTaxRow struct {
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

type SalesInvoiceWithholdingRow struct {
	ID                   string          `json:"id"`
	WithholdingTaxTypeID string          `json:"withholding_tax_type_id"`
	Rate                 decimal.Decimal `json:"rate"`
	Amount               decimal.Decimal `json:"amount"`
	BaseAmount           decimal.Decimal `json:"base_amount"`
	AccountID            string          `json:"account_id"`
}

// ---- input ----

type SalesInvoiceCreateInput struct {
	CompanyID           string                         `json:"company_id,omitempty"`
	CustomerID          string                         `json:"customer_id"`
	PostingDate         string                         `json:"posting_date"`
	DueDate             string                         `json:"due_date,omitempty"`
	Currency            string                         `json:"currency,omitempty"`
	ExchangeRate        string                         `json:"exchange_rate,omitempty"`
	TaxTemplateID       string                         `json:"tax_template_id,omitempty"`
	TaxInvoiceNumber    string                         `json:"tax_invoice_number,omitempty"`
	Remarks             string                         `json:"remarks,omitempty"`
	ReceivableAccountID string                         `json:"receivable_account_id,omitempty"`
	IsReturn            bool                           `json:"is_return,omitempty"`
	ReturnAgainst       string                         `json:"return_against,omitempty"`
	Items               []SalesInvoiceLineInput        `json:"items"`
	Withholding         []SalesInvoiceWithholdingInput `json:"withholding,omitempty"`
	CustomFields        map[string]any                 `json:"custom_fields,omitempty"`
}

type SalesInvoiceLineInput struct {
	ItemID          string `json:"item_id,omitempty"`
	ItemCode        string `json:"item_code,omitempty"`
	ItemName        string `json:"item_name,omitempty"`
	Description     string `json:"description,omitempty"`
	Qty             string `json:"qty"`
	UOM             string `json:"uom,omitempty"`
	Rate            string `json:"rate"`
	IncomeAccountID string `json:"income_account_id,omitempty"`
	CostCenterID    string `json:"cost_center_id,omitempty"`
}

type SalesInvoiceWithholdingInput struct {
	WithholdingTaxTypeID string `json:"withholding_tax_type_id"`
	Amount               string `json:"amount,omitempty"`
}

type Service struct {
	db *dbx.DB
	// Approvals is optional. When set, Submit() consults active approval_rules
	// for this doctype + company; missing approvals block submit.
	Approvals approvalChecker
	// Workflow is optional. When set, Submit() consults the workflow definition
	// (if one exists) to gate submit by the caller's role.
	Workflow workflowGate
	// Notifier is optional. When set, Submit() fires `invoice.issued` after
	// successful commit so notifrules can route in-app + email notifications.
	Notifier notifier
	// Indexer is optional. When set, Submit() upserts a global-search row for
	// the invoice inside the submit transaction.
	Indexer searchIndexer
}

type searchIndexer interface {
	IndexDocument(ctx context.Context, tx pgx.Tx, doctype, documentID, name, title, body, companyID string) error
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

func (s *Service) CreateDraft(ctx context.Context, in SalesInvoiceCreateInput) (*SalesInvoice, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("sales_invoice: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("sales_invoice.company_id: required")
	}
	if in.CustomerID == "" {
		return nil, errors.New("sales_invoice.customer_id: required")
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("sales_invoice.posting_date: %w", err)
	}
	var dueDate time.Time
	if in.DueDate == "" {
		dueDate = pd.AddDate(0, 0, 30)
	} else {
		dueDate, err = time.Parse("2006-01-02", in.DueDate)
		if err != nil {
			return nil, fmt.Errorf("sales_invoice.due_date: %w", err)
		}
	}
	if len(in.Items) == 0 {
		return nil, errors.New("sales_invoice.items: at least one required")
	}

	rate := decimal.NewFromInt(1)
	if in.ExchangeRate != "" {
		r, err := decimal.NewFromString(in.ExchangeRate)
		if err != nil {
			return nil, fmt.Errorf("sales_invoice.exchange_rate: %w", err)
		}
		if !r.IsPositive() {
			return nil, errors.New("sales_invoice.exchange_rate: must be > 0")
		}
		rate = r
	}

	id := dbx.NewIDWithPrefix("si")
	var out SalesInvoice
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		currency := in.Currency
		if currency == "" {
			if err := tx.QueryRow(ctx, `
				SELECT coalesce(NULLIF(cd.default_currency,''),
				       NULLIF(c.default_currency,''),
				       co.default_currency)
				FROM company co
				LEFT JOIN customer c            ON c.id = $2
				LEFT JOIN customer_default cd   ON cd.customer_id = c.id AND cd.company_id = co.id
				WHERE co.id = $1`, in.CompanyID, in.CustomerID).Scan(&currency); err != nil {
				return fmt.Errorf("resolve currency: %w", err)
			}
		}

		recv := in.ReceivableAccountID
		if recv == "" {
			err := tx.QueryRow(ctx, `
				SELECT coalesce(cd.default_receivable_account_id, co.default_receivable_account_id)
				FROM company co
				LEFT JOIN customer_default cd ON cd.customer_id = $2 AND cd.company_id = co.id
				WHERE co.id = $1`, in.CompanyID, in.CustomerID).Scan(&recv)
			if err != nil || recv == "" {
				return errors.New("sales_invoice.receivable_account_id: not provided and no default configured")
			}
		}

		taxTemplateID := in.TaxTemplateID
		if taxTemplateID == "" {
			_ = tx.QueryRow(ctx,
				`SELECT default_tax_template_id FROM customer_default WHERE customer_id = $1 AND company_id = $2`,
				in.CustomerID, in.CompanyID).Scan(&taxTemplateID)
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

		draftLines := make([]SalesInvoiceLine, len(in.Items))
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

			itemCode, itemName, uom, incomeAcc := ln.ItemCode, ln.ItemName, ln.UOM, ln.IncomeAccountID
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
				if incomeAcc == "" {
					_ = tx.QueryRow(ctx,
						`SELECT default_income_account_id FROM item_default WHERE item_id = $1 AND company_id = $2`,
						ln.ItemID, in.CompanyID).Scan(&incomeAcc)
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
			if incomeAcc == "" {
				if err := tx.QueryRow(ctx,
					`SELECT default_income_account_id FROM company WHERE id = $1`, in.CompanyID).Scan(&incomeAcc); err != nil || incomeAcc == "" {
					return fmt.Errorf("items[%d].income_account_id: not provided and no default configured", i)
				}
			}
			rowID := dbx.NewIDWithPrefix("sii")
			draftLines[i] = SalesInvoiceLine{
				ID: rowID, RowIndex: i + 1,
				ItemID: ln.ItemID, ItemCode: itemCode, ItemName: itemName, Description: ln.Description,
				Qty: qty, UOM: uom, Rate: rt, Amount: amount,
				IncomeAccountID: incomeAcc, CostCenterID: ln.CostCenterID,
			}
			calcLines[i] = tax.Line{Key: rowID, NetAmount: amount}
		}

		var taxResult tax.Result
		if taxTemplateID != "" {
			tpl, err := taxtemplate.LoadForCalc(ctx, tx, taxTemplateID)
			if err != nil {
				return err
			}
			if !tpl.IsSales {
				return errors.New("sales_invoice.tax_template: must be a sales template")
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

		taxRows := make([]SalesInvoiceTaxRow, len(taxResult.TaxRows))
		for i, tr := range taxResult.TaxRows {
			taxRows[i] = SalesInvoiceTaxRow{
				ID: dbx.NewIDWithPrefix("sit"), RowIndex: i + 1,
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

		// Validate return reference if present.
		if in.IsReturn && in.ReturnAgainst != "" {
			var origCustomer string
			var origDocstatus int16
			if err := tx.QueryRow(ctx, `SELECT customer_id, docstatus FROM sales_invoice WHERE id = $1`,
				in.ReturnAgainst).Scan(&origCustomer, &origDocstatus); err != nil {
				return fmt.Errorf("return_against: %w", err)
			}
			if origDocstatus != 1 {
				return errors.New("return_against: original sales invoice must be submitted")
			}
			if origCustomer != in.CustomerID {
				return errors.New("return_against: customer must match the original invoice")
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_invoice (
				id, name, company_id, customer_id, posting_date, due_date, fiscal_year_id,
				currency, exchange_rate, tax_template_id, tax_invoice_number,
				net_total, total_taxes_and_charges, grand_total,
				paid_amount, outstanding_amount,
				base_net_total, base_total_taxes_and_charges, base_grand_total,
				base_paid_amount, base_outstanding_amount,
				remarks, receivable_account_id, is_return, return_against,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
			          $12,$13,$14,
			          0, $14,
			          $15,$16,$17,
			          0, $17,
			          $18,$19,$20,$21,
			          $22,$23,$23)`,
			id, name, in.CompanyID, in.CustomerID, pd, dueDate, fyID,
			currency, rate, nullable(taxTemplateID), nullable(in.TaxInvoiceNumber),
			netTotal, taxTotal, grand,
			netTotal.Mul(rate).Round(money.Precision),
			taxTotal.Mul(rate).Round(money.Precision),
			baseGrand,
			nullable(in.Remarks), recv, in.IsReturn, nullable(in.ReturnAgainst),
			cf, p.UserID); err != nil {
			return err
		}

		for _, l := range draftLines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_invoice_item (
					id, sales_invoice_id, row_index, item_id, item_code, item_name, description,
					qty, uom, rate, amount, income_account_id, cost_center_id,
					tax_amount, total, base_amount, base_tax_amount, base_total
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
				l.ID, id, l.RowIndex, nullable(l.ItemID), l.ItemCode, l.ItemName, nullable(l.Description),
				l.Qty, l.UOM, l.Rate, l.Amount, l.IncomeAccountID, nullable(l.CostCenterID),
				l.TaxAmount, l.Total, l.BaseAmount, l.BaseTaxAmount, l.BaseTotal); err != nil {
				return err
			}
		}
		for _, tr := range taxRows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_invoice_tax (
					id, sales_invoice_id, row_index, account_id, description, rate,
					charge_type, included_in_basic_rate, tax_amount, base_tax_amount, cost_center_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				tr.ID, id, tr.RowIndex, tr.AccountID, tr.Description, tr.Rate,
				tr.ChargeType, tr.IncludedInBasicRate, tr.TaxAmount, tr.BaseTaxAmount, nullable(tr.CostCenterID)); err != nil {
				return err
			}
		}
		for _, w := range whRows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_invoice_withholding (
					id, sales_invoice_id, withholding_tax_type_id, rate, amount, base_amount, account_id
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

func (s *Service) Submit(ctx context.Context, id string) (*SalesInvoice, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("sales_invoice: unauthenticated")
	}
	var out SalesInvoice
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		si, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if si.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if si.GrandTotal.IsZero() {
			return errors.New("sales_invoice: grand_total is zero, nothing to post")
		}

		// Workflow role gate (when a workflow exists for this doctype).
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, "sales_invoice"); err != nil {
				return err
			}
		}
		// Approval gate.
		if s.Approvals != nil {
			gt, _ := si.GrandTotal.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, "sales_invoice", si.ID, si.Name, si.CompanyID,
				map[string]any{"grand_total": gt}); err != nil {
				return err
			}
		}

		// For a Credit Note (is_return=true), swap Dr/Cr polarity on every leg.
		// Original SI:    Dr AR, Cr Income, Cr Tax  → grows AR
		// Credit Note:    Cr AR, Dr Income, Dr Tax  → shrinks AR
		dr := func(d decimal.Decimal) decimal.Decimal { if si.IsReturn { return decimal.Zero }; return d }
		cr := func(d decimal.Decimal) decimal.Decimal { if si.IsReturn { return decimal.Zero }; return d }
		drR := func(d decimal.Decimal) decimal.Decimal { if si.IsReturn { return d }; return decimal.Zero }
		crR := func(d decimal.Decimal) decimal.Decimal { if si.IsReturn { return d }; return decimal.Zero }

		entries := make([]ledger.Entry, 0, len(si.Items)+len(si.Taxes)+1)
		entries = append(entries, ledger.Entry{
			AccountID:              si.ReceivableAccountID,
			PartyType:              ledger.PartyCustomer,
			PartyID:                si.CustomerID,
			Debit:                  dr(si.BaseGrandTotal),
			Credit:                 crR(si.BaseGrandTotal),
			AccountCurrency:        si.Currency,
			DebitInAccountCurrency: dr(si.GrandTotal),
			CreditInAccountCurrency: crR(si.GrandTotal),
			Against:                "Income",
			Remarks:                si.Name,
		})
		for _, l := range si.Items {
			var acctCurrency string
			if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, l.IncomeAccountID).Scan(&acctCurrency); err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID:               l.IncomeAccountID,
				CostCenterID:            l.CostCenterID,
				Debit:                   drR(l.BaseAmount),
				Credit:                  cr(l.BaseAmount),
				AccountCurrency:         acctCurrency,
				DebitInAccountCurrency:  drR(l.Amount),
				CreditInAccountCurrency: cr(l.Amount),
				Against:                 si.ReceivableAccountID,
				Remarks:                 fmt.Sprintf("%s — %s", si.Name, l.ItemCode),
			})
		}
		for _, tr := range si.Taxes {
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
				Debit:                   drR(tr.BaseTaxAmount),
				Credit:                  cr(tr.BaseTaxAmount),
				AccountCurrency:         acctCurrency,
				DebitInAccountCurrency:  drR(tr.TaxAmount),
				CreditInAccountCurrency: cr(tr.TaxAmount),
				Against:                 si.ReceivableAccountID,
				Remarks:                 fmt.Sprintf("%s — %s", si.Name, tr.Description),
			})
		}

		v := ledger.Voucher{
			Type: VoucherType, ID: si.ID, Name: si.Name,
			CompanyID: si.CompanyID, PostingDate: si.PostingDate, FiscalYearID: si.FiscalYearID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sales_invoice SET docstatus = 1, submitted_at = now(), submitted_by = $1,
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
		if s.Indexer != nil {
			var customerName string
			_ = tx.QueryRow(ctx, `SELECT coalesce(display_name, name) FROM customer WHERE id = $1`, out.CustomerID).Scan(&customerName)
			title := customerName
			if title == "" {
				title = out.Name
			}
			body := strings.TrimSpace(customerName + " " + out.Remarks)
			if err := s.Indexer.IndexDocument(ctx, tx, Doctype, out.ID, out.Name, title, body, out.CompanyID); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && s.Notifier != nil {
		grand, _ := out.GrandTotal.Float64()
		s.Notifier.Fire("invoice.issued", map[string]any{
			"company_id":    out.CompanyID,
			"doctype":       Doctype,
			"document_id":   out.ID,
			"document_name": out.Name,
			"grand_total":   grand,
			"summary": fmt.Sprintf("Sales invoice %s submitted, grand total %s %s",
				out.Name, out.Currency, out.GrandTotal.String()),
			"Invoice": out,
		})
	}
	return &out, err
}

// ---- Cancel ----

func (s *Service) Cancel(ctx context.Context, id string) (*SalesInvoice, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("sales_invoice: unauthenticated")
	}
	var out SalesInvoice
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		si, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if si.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if !si.PaidAmount.IsZero() {
			return errors.New("sales_invoice: cannot cancel an invoice with payments; cancel the Payment Entry first")
		}
		if _, err := ledger.CancelGL(ctx, tx, VoucherType, id, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sales_invoice SET docstatus = 2, cancelled_at = now(), cancelled_by = $1,
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

func (s *Service) Get(ctx context.Context, id string) (*SalesInvoice, error) {
	var out *SalesInvoice
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		si, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = si
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]SalesInvoice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM sales_invoice WHERE company_id = $1 ORDER BY posting_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]SalesInvoice, 0, len(ids))
	for _, id := range ids {
		si, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *si)
	}
	return out, nil
}

// ---- internal helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*SalesInvoice, error) {
	var (
		si                              SalesInvoice
		submittedAt, cancelledAt        *time.Time
		taxTemplateID, taxInvoiceNumber *string
		remarks, returnAgainst          *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, customer_id, posting_date, due_date, fiscal_year_id,
		       currency, exchange_rate, tax_template_id, tax_invoice_number,
		       net_total, total_taxes_and_charges, grand_total, paid_amount, outstanding_amount,
		       base_grand_total, base_outstanding_amount, remarks, receivable_account_id,
		       is_return, return_against, docstatus, submitted_at, cancelled_at, created_at, updated_at
		FROM sales_invoice WHERE id = $1`, id).
		Scan(&si.ID, &si.Name, &si.CompanyID, &si.CustomerID, &si.PostingDate, &si.DueDate, &si.FiscalYearID,
			&si.Currency, &si.ExchangeRate, &taxTemplateID, &taxInvoiceNumber,
			&si.NetTotal, &si.TotalTaxesAndCharges, &si.GrandTotal, &si.PaidAmount, &si.OutstandingAmount,
			&si.BaseGrandTotal, &si.BaseOutstandingAmount, &remarks, &si.ReceivableAccountID,
			&si.IsReturn, &returnAgainst, &si.Docstatus, &submittedAt, &cancelledAt, &si.CreatedAt, &si.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("sales_invoice %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if taxTemplateID != nil {
		si.TaxTemplateID = *taxTemplateID
	}
	if taxInvoiceNumber != nil {
		si.TaxInvoiceNumber = *taxInvoiceNumber
	}
	if remarks != nil {
		si.Remarks = *remarks
	}
	if returnAgainst != nil {
		si.ReturnAgainst = *returnAgainst
	}
	si.SubmittedAt = submittedAt
	si.CancelledAt = cancelledAt

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, coalesce(item_id,''), item_code, item_name, coalesce(description,''),
		       qty, uom, rate, amount, income_account_id, coalesce(cost_center_id,''),
		       tax_amount, total, base_amount, base_tax_amount, base_total
		FROM sales_invoice_item WHERE sales_invoice_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l SalesInvoiceLine
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.ItemID, &l.ItemCode, &l.ItemName, &l.Description,
			&l.Qty, &l.UOM, &l.Rate, &l.Amount, &l.IncomeAccountID, &l.CostCenterID,
			&l.TaxAmount, &l.Total, &l.BaseAmount, &l.BaseTaxAmount, &l.BaseTotal); err != nil {
			rows.Close()
			return nil, err
		}
		si.Items = append(si.Items, l)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT id, row_index, account_id, description, rate, charge_type, included_in_basic_rate,
		       tax_amount, base_tax_amount, coalesce(cost_center_id,'')
		FROM sales_invoice_tax WHERE sales_invoice_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t SalesInvoiceTaxRow
		if err := rows.Scan(&t.ID, &t.RowIndex, &t.AccountID, &t.Description, &t.Rate,
			&t.ChargeType, &t.IncludedInBasicRate, &t.TaxAmount, &t.BaseTaxAmount, &t.CostCenterID); err != nil {
			rows.Close()
			return nil, err
		}
		si.Taxes = append(si.Taxes, t)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT id, withholding_tax_type_id, rate, amount, base_amount, account_id
		FROM sales_invoice_withholding WHERE sales_invoice_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w SalesInvoiceWithholdingRow
		if err := rows.Scan(&w.ID, &w.WithholdingTaxTypeID, &w.Rate, &w.Amount, &w.BaseAmount, &w.AccountID); err != nil {
			return nil, err
		}
		si.Withholding = append(si.Withholding, w)
	}
	return &si, nil
}

func buildWithholding(ctx context.Context, tx pgx.Tx, in []SalesInvoiceWithholdingInput, netTotal, exchangeRate decimal.Decimal) ([]SalesInvoiceWithholdingRow, error) {
	out := make([]SalesInvoiceWithholdingRow, 0, len(in))
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
		out = append(out, SalesInvoiceWithholdingRow{
			ID:                   dbx.NewIDWithPrefix("siw"),
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
