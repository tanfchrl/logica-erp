// Package paymententry implements Payment Entry.
//
// Model:
//   paid_amount     = cash actually moving (source side, in PaidFromCurrency)
//   received_amount = cash actually moving (target side, in PaidToCurrency)
//   total_deductions       = withholding/other deductions that *also* settle the AR/AP
//   total_allocated_amount = sum of references' allocated_amount
//   unallocated_amount     = paid_amount + total_deductions - total_allocated_amount  (>= 0; advances)
//
// Invariant: total_allocated + unallocated = paid + deductions
//
// This is ERPNext's model and the only one that composes with full-clearance + withholding:
// invoice 5,550,000 cleared by cash 5,000,000 + PPh23 550,000 ⇒
// paid_amount=5,000,000, total_deductions=550,000, total_allocated=5,550,000, unallocated=0.
//
// Submit (receive from customer):
//   Dr paid_to_account (cash/bank)        base_paid_amount
//   Dr <deduction.account_id>             deduction.amount       (e.g. PPh23 receivable)
//   Cr paid_from_account (receivable)     base_paid + total_deductions   party=customer
//
// Submit (pay to supplier):
//   Dr paid_to_account (payable)          base_paid + total_deductions   party=supplier
//   Cr paid_from_account (cash/bank)      base_paid_amount
//   Cr <deduction.account_id>             deduction.amount       (e.g. PPh23 payable to DJP)
//
// On submit, each referenced SI/PI's paid_amount + outstanding_amount are
// updated atomically. On cancel, those updates are reversed and the GL is
// inverted.
package paymententry

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
	"github.com/tandigital/logica-erp/internal/platform/ledger"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const (
	Doctype     = "payment_entry"
	VoucherType = "Payment Entry"

	PaymentReceive          = "receive"
	PaymentPay              = "pay"
	PaymentInternalTransfer = "internal_transfer"
)

type PaymentEntry struct {
	ID                       string                  `json:"id"`
	Name                     string                  `json:"name"`
	CompanyID                string                  `json:"company_id"`
	PaymentType              string                  `json:"payment_type"`
	PartyType                string                  `json:"party_type,omitempty"`
	PartyID                  string                  `json:"party_id,omitempty"`
	PostingDate              time.Time               `json:"posting_date"`
	FiscalYearID             string                  `json:"fiscal_year_id"`
	PaidFromAccountID        string                  `json:"paid_from_account_id"`
	PaidToAccountID          string                  `json:"paid_to_account_id"`
	PaidFromCurrency         string                  `json:"paid_from_currency"`
	PaidToCurrency           string                  `json:"paid_to_currency"`
	PaidAmount               decimal.Decimal         `json:"paid_amount"`
	ReceivedAmount           decimal.Decimal         `json:"received_amount"`
	SourceExchangeRate       decimal.Decimal         `json:"source_exchange_rate"`
	TargetExchangeRate       decimal.Decimal         `json:"target_exchange_rate"`
	BasePaidAmount           decimal.Decimal         `json:"base_paid_amount"`
	BaseReceivedAmount       decimal.Decimal         `json:"base_received_amount"`
	TotalAllocatedAmount     decimal.Decimal         `json:"total_allocated_amount"`
	BaseTotalAllocatedAmount decimal.Decimal         `json:"base_total_allocated_amount"`
	UnallocatedAmount        decimal.Decimal         `json:"unallocated_amount"`
	TotalDeductions          decimal.Decimal         `json:"total_deductions"`
	ReferenceNo              string                  `json:"reference_no,omitempty"`
	ReferenceDate            *time.Time              `json:"reference_date,omitempty"`
	Remarks                  string                  `json:"remarks,omitempty"`
	Docstatus                submittable.Status      `json:"docstatus"`
	SubmittedAt              *time.Time              `json:"submitted_at,omitempty"`
	CancelledAt              *time.Time              `json:"cancelled_at,omitempty"`
	CreatedAt                time.Time               `json:"created_at"`
	UpdatedAt                time.Time               `json:"updated_at"`
	References               []PaymentEntryReference `json:"references,omitempty"`
	Deductions               []PaymentEntryDeduction `json:"deductions,omitempty"`
}

type PaymentEntryReference struct {
	ID                  string          `json:"id"`
	RowIndex            int             `json:"row_index"`
	ReferenceDoctype    string          `json:"reference_doctype"`
	ReferenceID         string          `json:"reference_id"`
	ReferenceName       string          `json:"reference_name"`
	TotalAmount         decimal.Decimal `json:"total_amount"`
	AllocatedAmount     decimal.Decimal `json:"allocated_amount"`
	BaseAllocatedAmount decimal.Decimal `json:"base_allocated_amount"`
}

type PaymentEntryDeduction struct {
	ID                   string          `json:"id"`
	RowIndex             int             `json:"row_index"`
	AccountID            string          `json:"account_id"`
	Description          string          `json:"description"`
	Amount               decimal.Decimal `json:"amount"`
	CostCenterID         string          `json:"cost_center_id,omitempty"`
	WithholdingTaxTypeID string          `json:"withholding_tax_type_id,omitempty"`
}

// ---- input ----

type PaymentEntryCreateInput struct {
	CompanyID          string                       `json:"company_id,omitempty"`
	PaymentType        string                       `json:"payment_type"`
	PartyType          string                       `json:"party_type,omitempty"`
	PartyID            string                       `json:"party_id,omitempty"`
	PostingDate        string                       `json:"posting_date"`
	PaidFromAccountID  string                       `json:"paid_from_account_id"`
	PaidToAccountID    string                       `json:"paid_to_account_id"`
	PaidAmount         string                       `json:"paid_amount"`
	SourceExchangeRate string                       `json:"source_exchange_rate,omitempty"`
	TargetExchangeRate string                       `json:"target_exchange_rate,omitempty"`
	ReferenceNo        string                       `json:"reference_no,omitempty"`
	ReferenceDate      string                       `json:"reference_date,omitempty"`
	Remarks            string                       `json:"remarks,omitempty"`
	References         []PaymentReferenceInput      `json:"references,omitempty"`
	Deductions         []PaymentDeductionInput      `json:"deductions,omitempty"`
	CustomFields       map[string]any               `json:"custom_fields,omitempty"`
}

type PaymentReferenceInput struct {
	ReferenceDoctype string `json:"reference_doctype"`
	ReferenceID      string `json:"reference_id"`
	AllocatedAmount  string `json:"allocated_amount"`
}

type PaymentDeductionInput struct {
	AccountID            string `json:"account_id,omitempty"`
	Description          string `json:"description,omitempty"`
	Amount               string `json:"amount"`
	CostCenterID         string `json:"cost_center_id,omitempty"`
	WithholdingTaxTypeID string `json:"withholding_tax_type_id,omitempty"`
}

type Service struct {
	db *dbx.DB
	// Approvals is optional. When set, Submit() consults active approval_rules
	// for this doctype + company; missing approvals block submit.
	Approvals approvalChecker
	// Workflow is optional. Gates submit by role.
	Workflow workflowGate
	// Notifier is optional. Submit() fires payment.received / payment.made
	// after successful commit.
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

func (s *Service) CreateDraft(ctx context.Context, in PaymentEntryCreateInput) (*PaymentEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("payment_entry: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("payment_entry.company_id: required")
	}
	switch in.PaymentType {
	case PaymentReceive, PaymentPay, PaymentInternalTransfer:
	default:
		return nil, fmt.Errorf("payment_entry.payment_type: invalid %q", in.PaymentType)
	}
	if in.PaymentType != PaymentInternalTransfer {
		if in.PartyType == "" || in.PartyID == "" {
			return nil, errors.New("payment_entry: party_type/party_id required for receive/pay")
		}
	} else {
		if len(in.References) > 0 {
			return nil, errors.New("payment_entry: internal_transfer cannot reference invoices")
		}
		if len(in.Deductions) > 0 {
			return nil, errors.New("payment_entry: internal_transfer cannot carry deductions")
		}
		if in.PaidFromAccountID == in.PaidToAccountID {
			return nil, errors.New("payment_entry: internal_transfer requires distinct paid_from/paid_to accounts")
		}
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("payment_entry.posting_date: %w", err)
	}
	paidAmount, err := decimal.NewFromString(strings.TrimSpace(in.PaidAmount))
	if err != nil || !paidAmount.IsPositive() {
		return nil, errors.New("payment_entry.paid_amount: must be > 0")
	}
	srcRate := decimal.NewFromInt(1)
	if in.SourceExchangeRate != "" {
		r, err := decimal.NewFromString(in.SourceExchangeRate)
		if err != nil || !r.IsPositive() {
			return nil, errors.New("payment_entry.source_exchange_rate: must be > 0")
		}
		srcRate = r
	}
	tgtRate := decimal.NewFromInt(1)
	if in.TargetExchangeRate != "" {
		r, err := decimal.NewFromString(in.TargetExchangeRate)
		if err != nil || !r.IsPositive() {
			return nil, errors.New("payment_entry.target_exchange_rate: must be > 0")
		}
		tgtRate = r
	}

	id := dbx.NewIDWithPrefix("pe")
	var out PaymentEntry
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var fromCur, toCur string
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, in.PaidFromAccountID).Scan(&fromCur); err != nil {
			return fmt.Errorf("paid_from_account: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, in.PaidToAccountID).Scan(&toCur); err != nil {
			return fmt.Errorf("paid_to_account: %w", err)
		}

		refs := make([]PaymentEntryReference, len(in.References))
		totalAllocated := decimal.Zero
		for i, r := range in.References {
			alloc, err := decimal.NewFromString(strings.TrimSpace(r.AllocatedAmount))
			if err != nil || !alloc.IsPositive() {
				return fmt.Errorf("references[%d].allocated_amount: must be > 0", i)
			}
			var (
				docName     string
				partyOnDoc  string
				outstanding decimal.Decimal
				docstatus   int16
			)
			switch r.ReferenceDoctype {
			case "sales_invoice":
				if in.PaymentType != PaymentReceive {
					return fmt.Errorf("references[%d]: sales_invoice references require payment_type=receive", i)
				}
				if err := tx.QueryRow(ctx, `
					SELECT name, customer_id, outstanding_amount, docstatus
					FROM sales_invoice WHERE id = $1`, r.ReferenceID).
					Scan(&docName, &partyOnDoc, &outstanding, &docstatus); err != nil {
					return fmt.Errorf("references[%d]: SI %s not found: %w", i, r.ReferenceID, err)
				}
			case "purchase_invoice":
				if in.PaymentType != PaymentPay {
					return fmt.Errorf("references[%d]: purchase_invoice references require payment_type=pay", i)
				}
				if err := tx.QueryRow(ctx, `
					SELECT name, supplier_id, outstanding_amount, docstatus
					FROM purchase_invoice WHERE id = $1`, r.ReferenceID).
					Scan(&docName, &partyOnDoc, &outstanding, &docstatus); err != nil {
					return fmt.Errorf("references[%d]: PI %s not found: %w", i, r.ReferenceID, err)
				}
			default:
				return fmt.Errorf("references[%d]: unsupported reference_doctype %q", i, r.ReferenceDoctype)
			}
			if docstatus != 1 {
				return fmt.Errorf("references[%d]: %s %s is not submitted", i, r.ReferenceDoctype, docName)
			}
			if partyOnDoc != in.PartyID {
				return fmt.Errorf("references[%d]: %s %s belongs to a different party", i, r.ReferenceDoctype, docName)
			}
			if alloc.GreaterThan(outstanding) {
				return fmt.Errorf("references[%d]: %s %s outstanding %s < allocated %s", i, r.ReferenceDoctype, docName, outstanding, alloc)
			}
			refs[i] = PaymentEntryReference{
				ID: dbx.NewIDWithPrefix("per"), RowIndex: i + 1,
				ReferenceDoctype: r.ReferenceDoctype, ReferenceID: r.ReferenceID, ReferenceName: docName,
				TotalAmount: outstanding, AllocatedAmount: alloc,
				BaseAllocatedAmount: alloc.Mul(srcRate).Round(money.Precision),
			}
			totalAllocated = totalAllocated.Add(alloc)
		}

		deds := make([]PaymentEntryDeduction, len(in.Deductions))
		totalDeductions := decimal.Zero
		for i, d := range in.Deductions {
			amt, err := decimal.NewFromString(strings.TrimSpace(d.Amount))
			if err != nil || !amt.IsPositive() {
				return fmt.Errorf("deductions[%d].amount: must be > 0", i)
			}
			acct := d.AccountID
			rateForDesc := decimal.Zero
			if d.WithholdingTaxTypeID != "" {
				var whtAcct string
				if err := tx.QueryRow(ctx,
					`SELECT account_id, rate FROM withholding_tax_type WHERE id = $1 AND is_deleted = false`,
					d.WithholdingTaxTypeID).Scan(&whtAcct, &rateForDesc); err != nil {
					return fmt.Errorf("deductions[%d]: withholding type: %w", i, err)
				}
				if acct == "" {
					acct = whtAcct
				}
			}
			if acct == "" {
				return fmt.Errorf("deductions[%d].account_id: required (or specify withholding_tax_type_id)", i)
			}
			desc := d.Description
			if desc == "" && d.WithholdingTaxTypeID != "" {
				desc = fmt.Sprintf("Withholding %s%%", rateForDesc.String())
			}
			if desc == "" {
				desc = "Deduction"
			}
			deds[i] = PaymentEntryDeduction{
				ID: dbx.NewIDWithPrefix("ped"), RowIndex: i + 1,
				AccountID: acct, Description: desc, Amount: amt,
				CostCenterID: d.CostCenterID, WithholdingTaxTypeID: d.WithholdingTaxTypeID,
			}
			totalDeductions = totalDeductions.Add(amt)
		}

		unallocated, err := computeUnallocated(paidAmount, totalAllocated, totalDeductions)
		if err != nil {
			return err
		}
		// FX-correct: same cash flow expressed in target currency.
		// Same-ccy (srcRate=tgtRate=1) collapses to paidAmount.
		receivedAmount := paidAmount.Mul(srcRate).Div(tgtRate).Round(money.Precision)

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

		basePaid := paidAmount.Mul(srcRate).Round(money.Precision)
		baseReceived := receivedAmount.Mul(tgtRate).Round(money.Precision)

		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_entry (
				id, name, company_id, payment_type, party_type, party_id, posting_date, fiscal_year_id,
				paid_from_account_id, paid_to_account_id, paid_from_currency, paid_to_currency,
				paid_amount, received_amount, source_exchange_rate, target_exchange_rate,
				base_paid_amount, base_received_amount,
				total_allocated_amount, base_total_allocated_amount, unallocated_amount, total_deductions,
				reference_no, reference_date, remarks, custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$27)`,
			id, name, in.CompanyID, in.PaymentType, nullable(in.PartyType), nullable(in.PartyID), pd, fyID,
			in.PaidFromAccountID, in.PaidToAccountID, fromCur, toCur,
			paidAmount, receivedAmount, srcRate, tgtRate,
			basePaid, baseReceived,
			totalAllocated, totalAllocated.Mul(srcRate).Round(money.Precision), unallocated, totalDeductions,
			nullable(in.ReferenceNo), nullableDate(in.ReferenceDate), nullable(in.Remarks),
			cf, p.UserID); err != nil {
			return err
		}
		for _, r := range refs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_entry_reference (id, payment_entry_id, row_index, reference_doctype,
				                                    reference_id, reference_name, total_amount,
				                                    allocated_amount, base_allocated_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				r.ID, id, r.RowIndex, r.ReferenceDoctype, r.ReferenceID, r.ReferenceName,
				r.TotalAmount, r.AllocatedAmount, r.BaseAllocatedAmount); err != nil {
				return err
			}
		}
		for _, d := range deds {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_entry_deduction (id, payment_entry_id, row_index, account_id, description,
				                                    amount, cost_center_id, withholding_tax_type_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				d.ID, id, d.RowIndex, d.AccountID, d.Description, d.Amount,
				nullable(d.CostCenterID), nullable(d.WithholdingTaxTypeID)); err != nil {
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

// ---- Update (draft-only) ----

// PaymentEntryUpdateInput mirrors PaymentEntryCreateInput but only fields that
// are safe to mutate on a Draft are editable. Submitted/Cancelled entries are
// immutable. company_id, payment_type, posting_date are immutable to preserve
// the naming-series and fiscal-year context already baked in.
type PaymentEntryUpdateInput struct {
	PartyType          string                  `json:"party_type,omitempty"`
	PartyID            string                  `json:"party_id,omitempty"`
	PaidFromAccountID  string                  `json:"paid_from_account_id"`
	PaidToAccountID    string                  `json:"paid_to_account_id"`
	PaidAmount         string                  `json:"paid_amount"`
	SourceExchangeRate string                  `json:"source_exchange_rate,omitempty"`
	TargetExchangeRate string                  `json:"target_exchange_rate,omitempty"`
	ReferenceNo        string                  `json:"reference_no,omitempty"`
	ReferenceDate      string                  `json:"reference_date,omitempty"`
	Remarks            string                  `json:"remarks,omitempty"`
	References         []PaymentReferenceInput `json:"references,omitempty"`
	Deductions         []PaymentDeductionInput `json:"deductions,omitempty"`
	CustomFields       map[string]any          `json:"custom_fields,omitempty"`
}

func (s *Service) Update(ctx context.Context, id string, in PaymentEntryUpdateInput) (*PaymentEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("payment_entry: unauthenticated")
	}
	paidAmount, err := decimal.NewFromString(strings.TrimSpace(in.PaidAmount))
	if err != nil || !paidAmount.IsPositive() {
		return nil, errors.New("payment_entry.paid_amount: must be > 0")
	}
	srcRate := decimal.NewFromInt(1)
	if in.SourceExchangeRate != "" {
		r, err := decimal.NewFromString(in.SourceExchangeRate)
		if err != nil || !r.IsPositive() {
			return nil, errors.New("payment_entry.source_exchange_rate: must be > 0")
		}
		srcRate = r
	}
	tgtRate := decimal.NewFromInt(1)
	if in.TargetExchangeRate != "" {
		r, err := decimal.NewFromString(in.TargetExchangeRate)
		if err != nil || !r.IsPositive() {
			return nil, errors.New("payment_entry.target_exchange_rate: must be > 0")
		}
		tgtRate = r
	}

	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Docstatus != submittable.Draft {
			return fmt.Errorf("payment_entry: cannot edit (docstatus=%d)", existing.Docstatus)
		}
		partyType := in.PartyType
		partyID := in.PartyID
		if existing.PaymentType != PaymentInternalTransfer {
			if partyType == "" {
				partyType = existing.PartyType
			}
			if partyID == "" {
				partyID = existing.PartyID
			}
			if partyType == "" || partyID == "" {
				return errors.New("payment_entry: party_type/party_id required for receive/pay")
			}
		} else {
			if len(in.References) > 0 {
				return errors.New("payment_entry: internal_transfer cannot reference invoices")
			}
			if len(in.Deductions) > 0 {
				return errors.New("payment_entry: internal_transfer cannot carry deductions")
			}
			if in.PaidFromAccountID == in.PaidToAccountID {
				return errors.New("payment_entry: internal_transfer requires distinct paid_from/paid_to accounts")
			}
			partyType = ""
			partyID = ""
		}

		var fromCur, toCur string
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, in.PaidFromAccountID).Scan(&fromCur); err != nil {
			return fmt.Errorf("paid_from_account: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, in.PaidToAccountID).Scan(&toCur); err != nil {
			return fmt.Errorf("paid_to_account: %w", err)
		}

		// Re-validate + rebuild references.
		refs := make([]PaymentEntryReference, len(in.References))
		totalAllocated := decimal.Zero
		for i, r := range in.References {
			alloc, err := decimal.NewFromString(strings.TrimSpace(r.AllocatedAmount))
			if err != nil || !alloc.IsPositive() {
				return fmt.Errorf("references[%d].allocated_amount: must be > 0", i)
			}
			var (
				docName     string
				partyOnDoc  string
				outstanding decimal.Decimal
				docstatus   int16
			)
			switch r.ReferenceDoctype {
			case "sales_invoice":
				if existing.PaymentType != PaymentReceive {
					return fmt.Errorf("references[%d]: sales_invoice references require payment_type=receive", i)
				}
				if err := tx.QueryRow(ctx, `
					SELECT name, customer_id, outstanding_amount, docstatus
					FROM sales_invoice WHERE id = $1`, r.ReferenceID).
					Scan(&docName, &partyOnDoc, &outstanding, &docstatus); err != nil {
					return fmt.Errorf("references[%d]: SI %s not found: %w", i, r.ReferenceID, err)
				}
			case "purchase_invoice":
				if existing.PaymentType != PaymentPay {
					return fmt.Errorf("references[%d]: purchase_invoice references require payment_type=pay", i)
				}
				if err := tx.QueryRow(ctx, `
					SELECT name, supplier_id, outstanding_amount, docstatus
					FROM purchase_invoice WHERE id = $1`, r.ReferenceID).
					Scan(&docName, &partyOnDoc, &outstanding, &docstatus); err != nil {
					return fmt.Errorf("references[%d]: PI %s not found: %w", i, r.ReferenceID, err)
				}
			default:
				return fmt.Errorf("references[%d]: unsupported reference_doctype %q", i, r.ReferenceDoctype)
			}
			if docstatus != 1 {
				return fmt.Errorf("references[%d]: %s %s is not submitted", i, r.ReferenceDoctype, docName)
			}
			if partyOnDoc != partyID {
				return fmt.Errorf("references[%d]: %s %s belongs to a different party", i, r.ReferenceDoctype, docName)
			}
			if alloc.GreaterThan(outstanding) {
				return fmt.Errorf("references[%d]: %s %s outstanding %s < allocated %s", i, r.ReferenceDoctype, docName, outstanding, alloc)
			}
			refs[i] = PaymentEntryReference{
				ID: dbx.NewIDWithPrefix("per"), RowIndex: i + 1,
				ReferenceDoctype: r.ReferenceDoctype, ReferenceID: r.ReferenceID, ReferenceName: docName,
				TotalAmount: outstanding, AllocatedAmount: alloc,
				BaseAllocatedAmount: alloc.Mul(srcRate).Round(money.Precision),
			}
			totalAllocated = totalAllocated.Add(alloc)
		}

		deds := make([]PaymentEntryDeduction, len(in.Deductions))
		totalDeductions := decimal.Zero
		for i, d := range in.Deductions {
			amt, err := decimal.NewFromString(strings.TrimSpace(d.Amount))
			if err != nil || !amt.IsPositive() {
				return fmt.Errorf("deductions[%d].amount: must be > 0", i)
			}
			acct := d.AccountID
			rateForDesc := decimal.Zero
			if d.WithholdingTaxTypeID != "" {
				var whtAcct string
				if err := tx.QueryRow(ctx,
					`SELECT account_id, rate FROM withholding_tax_type WHERE id = $1 AND is_deleted = false`,
					d.WithholdingTaxTypeID).Scan(&whtAcct, &rateForDesc); err != nil {
					return fmt.Errorf("deductions[%d]: withholding type: %w", i, err)
				}
				if acct == "" {
					acct = whtAcct
				}
			}
			if acct == "" {
				return fmt.Errorf("deductions[%d].account_id: required (or specify withholding_tax_type_id)", i)
			}
			desc := d.Description
			if desc == "" && d.WithholdingTaxTypeID != "" {
				desc = fmt.Sprintf("Withholding %s%%", rateForDesc.String())
			}
			if desc == "" {
				desc = "Deduction"
			}
			deds[i] = PaymentEntryDeduction{
				ID: dbx.NewIDWithPrefix("ped"), RowIndex: i + 1,
				AccountID: acct, Description: desc, Amount: amt,
				CostCenterID: d.CostCenterID, WithholdingTaxTypeID: d.WithholdingTaxTypeID,
			}
			totalDeductions = totalDeductions.Add(amt)
		}

		unallocated, err := computeUnallocated(paidAmount, totalAllocated, totalDeductions)
		if err != nil {
			return err
		}
		// FX-correct: same cash flow expressed in target currency.
		// Same-ccy (srcRate=tgtRate=1) collapses to paidAmount.
		receivedAmount := paidAmount.Mul(srcRate).Div(tgtRate).Round(money.Precision)

		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}

		basePaid := paidAmount.Mul(srcRate).Round(money.Precision)
		baseReceived := receivedAmount.Mul(tgtRate).Round(money.Precision)

		if _, err := tx.Exec(ctx, `
			UPDATE payment_entry SET
			  party_type                  = $2,
			  party_id                    = $3,
			  paid_from_account_id        = $4,
			  paid_to_account_id          = $5,
			  paid_from_currency          = $6,
			  paid_to_currency            = $7,
			  paid_amount                 = $8,
			  received_amount             = $9,
			  source_exchange_rate        = $10,
			  target_exchange_rate        = $11,
			  base_paid_amount            = $12,
			  base_received_amount        = $13,
			  total_allocated_amount      = $14,
			  base_total_allocated_amount = $15,
			  unallocated_amount          = $16,
			  total_deductions            = $17,
			  reference_no                = $18,
			  reference_date              = $19,
			  remarks                     = $20,
			  custom_fields               = $21,
			  updated_by                  = $22
			WHERE id = $1 AND docstatus = 0`,
			id, nullable(partyType), nullable(partyID),
			in.PaidFromAccountID, in.PaidToAccountID, fromCur, toCur,
			paidAmount, receivedAmount, srcRate, tgtRate,
			basePaid, baseReceived,
			totalAllocated, totalAllocated.Mul(srcRate).Round(money.Precision), unallocated, totalDeductions,
			nullable(in.ReferenceNo), nullableDate(in.ReferenceDate), nullable(in.Remarks),
			cf, p.UserID); err != nil {
			return err
		}

		// Replace child tables.
		if _, err := tx.Exec(ctx, `DELETE FROM payment_entry_reference WHERE payment_entry_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM payment_entry_deduction WHERE payment_entry_id = $1`, id); err != nil {
			return err
		}
		for _, r := range refs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_entry_reference (id, payment_entry_id, row_index, reference_doctype,
				                                    reference_id, reference_name, total_amount,
				                                    allocated_amount, base_allocated_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				r.ID, id, r.RowIndex, r.ReferenceDoctype, r.ReferenceID, r.ReferenceName,
				r.TotalAmount, r.AllocatedAmount, r.BaseAllocatedAmount); err != nil {
				return err
			}
		}
		for _, d := range deds {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_entry_deduction (id, payment_entry_id, row_index, account_id, description,
				                                    amount, cost_center_id, withholding_tax_type_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				d.ID, id, d.RowIndex, d.AccountID, d.Description, d.Amount,
				nullable(d.CostCenterID), nullable(d.WithholdingTaxTypeID)); err != nil {
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

// ---- Submit ----

func (s *Service) Submit(ctx context.Context, id string) (*PaymentEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("payment_entry: unauthenticated")
	}
	var out PaymentEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pe, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pe.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}

		// Workflow role gate.
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, "payment_entry"); err != nil {
				return err
			}
		}
		// Approval gate. Expose paid_amount / received_amount plus a generic
		// `amount` (the relevant side of the transfer) so rules can be written
		// against the most natural field name.
		if s.Approvals != nil {
			paid, _ := pe.PaidAmount.Float64()
			received, _ := pe.ReceivedAmount.Float64()
			amount := paid
			if pe.PaymentType == PaymentReceive {
				amount = received
			}
			if err := s.Approvals.CheckSubmit(ctx, tx, "payment_entry", pe.ID, pe.Name, pe.CompanyID,
				map[string]any{
					"paid_amount":     paid,
					"received_amount": received,
					"amount":          amount,
				}); err != nil {
				return err
			}
		}

		entries := []ledger.Entry{}

		// Settlement amount on the party leg = total_allocated + unallocated,
		// which by the invariant equals paid + deductions. Cross-currency with
		// non-zero deductions is undefined for Phase 1 (deduction.amount is
		// taken to be in base currency, so we add it directly to paid_amount
		// for the account-currency view).
		settleBase := pe.BasePaidAmount.Add(pe.TotalDeductions)
		settleAcct := pe.PaidAmount.Add(pe.TotalDeductions)

		switch pe.PaymentType {
		case PaymentReceive:
			// Dr Cash (paid_amount), Dr Deductions (PPh receivable), Cr AR (paid + deductions)
			entries = append(entries, ledger.Entry{
				AccountID:              pe.PaidToAccountID,
				Debit:                  pe.BaseReceivedAmount,
				AccountCurrency:        pe.PaidToCurrency,
				DebitInAccountCurrency: pe.ReceivedAmount,
				Against:                pe.PaidFromAccountID,
				Remarks:                pe.Name + " — payment received",
			})
			for _, d := range pe.Deductions {
				var acctCurrency string
				if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, d.AccountID).Scan(&acctCurrency); err != nil {
					return err
				}
				entries = append(entries, ledger.Entry{
					AccountID:              d.AccountID,
					Debit:                  d.Amount,
					AccountCurrency:        acctCurrency,
					DebitInAccountCurrency: d.Amount,
					Against:                pe.PaidFromAccountID,
					CostCenterID:           d.CostCenterID,
					Remarks:                fmt.Sprintf("%s — %s", pe.Name, d.Description),
				})
			}
			entries = append(entries, ledger.Entry{
				AccountID:               pe.PaidFromAccountID,
				PartyType:               ledger.PartyType(pe.PartyType),
				PartyID:                 pe.PartyID,
				Credit:                  settleBase,
				AccountCurrency:         pe.PaidFromCurrency,
				CreditInAccountCurrency: settleAcct,
				Against:                 pe.PaidToAccountID,
				Remarks:                 pe.Name + " — settle receivable",
			})

		case PaymentInternalTransfer:
			// Dr paid_to (target cash), Cr paid_from (source cash). No party.
			entries = append(entries, ledger.Entry{
				AccountID:              pe.PaidToAccountID,
				Debit:                  pe.BaseReceivedAmount,
				AccountCurrency:        pe.PaidToCurrency,
				DebitInAccountCurrency: pe.ReceivedAmount,
				Against:                pe.PaidFromAccountID,
				Remarks:                pe.Name + " — internal transfer in",
			})
			entries = append(entries, ledger.Entry{
				AccountID:               pe.PaidFromAccountID,
				Credit:                  pe.BasePaidAmount,
				AccountCurrency:         pe.PaidFromCurrency,
				CreditInAccountCurrency: pe.PaidAmount,
				Against:                 pe.PaidToAccountID,
				Remarks:                 pe.Name + " — internal transfer out",
			})

		case PaymentPay:
			// Dr AP (paid + deductions), Cr Cash (paid_amount), Cr Deductions (PPh payable to DJP)
			entries = append(entries, ledger.Entry{
				AccountID:              pe.PaidToAccountID,
				PartyType:              ledger.PartyType(pe.PartyType),
				PartyID:                pe.PartyID,
				Debit:                  settleBase,
				AccountCurrency:        pe.PaidToCurrency,
				DebitInAccountCurrency: settleAcct,
				Against:                pe.PaidFromAccountID,
				Remarks:                pe.Name + " — settle payable",
			})
			entries = append(entries, ledger.Entry{
				AccountID:               pe.PaidFromAccountID,
				Credit:                  pe.BasePaidAmount,
				AccountCurrency:         pe.PaidFromCurrency,
				CreditInAccountCurrency: pe.PaidAmount,
				Against:                 pe.PaidToAccountID,
				Remarks:                 pe.Name + " — cash out",
			})
			for _, d := range pe.Deductions {
				var acctCurrency string
				if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, d.AccountID).Scan(&acctCurrency); err != nil {
					return err
				}
				entries = append(entries, ledger.Entry{
					AccountID:               d.AccountID,
					Credit:                  d.Amount,
					AccountCurrency:         acctCurrency,
					CreditInAccountCurrency: d.Amount,
					Against:                 pe.PaidToAccountID,
					CostCenterID:            d.CostCenterID,
					Remarks:                 fmt.Sprintf("%s — %s", pe.Name, d.Description),
				})
			}
		}

		v := ledger.Voucher{
			Type: VoucherType, ID: pe.ID, Name: pe.Name,
			CompanyID: pe.CompanyID, PostingDate: pe.PostingDate, FiscalYearID: pe.FiscalYearID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}

		if err := applyReferenceUpdates(ctx, tx, pe.References, p.UserID, +1); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE payment_entry SET docstatus = 1, submitted_at = now(), submitted_by = $1, updated_by = $1 WHERE id = $2`,
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
	if err == nil && s.Notifier != nil {
		event := "payment.made"
		amt := out.PaidAmount
		currency := out.PaidFromCurrency
		if out.PaymentType == PaymentReceive {
			event = "invoice.payment_received"
			amt = out.ReceivedAmount
			currency = out.PaidToCurrency
		}
		amtF, _ := amt.Float64()
		s.Notifier.Fire(event, map[string]any{
			"company_id":    out.CompanyID,
			"doctype":       Doctype,
			"document_id":   out.ID,
			"document_name": out.Name,
			"amount":        amtF,
			"summary": fmt.Sprintf("Payment entry %s submitted, %s %s",
				out.Name, currency, amt.String()),
			"Payment": out,
		})
	}
	return &out, err
}

// ---- Cancel ----

func (s *Service) Cancel(ctx context.Context, id string) (*PaymentEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("payment_entry: unauthenticated")
	}
	var out PaymentEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pe, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pe.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if _, err := ledger.CancelGL(ctx, tx, VoucherType, id, p.UserID); err != nil {
			return err
		}
		if err := applyReferenceUpdates(ctx, tx, pe.References, p.UserID, -1); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE payment_entry SET docstatus = 2, cancelled_at = now(), cancelled_by = $1, updated_by = $1 WHERE id = $2`,
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

func (s *Service) Get(ctx context.Context, id string) (*PaymentEntry, error) {
	var out *PaymentEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pe, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = pe
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]PaymentEntry, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM payment_entry WHERE company_id = $1 ORDER BY posting_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]PaymentEntry, 0, len(ids))
	for _, id := range ids {
		pe, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *pe)
	}
	return out, nil
}

func load(ctx context.Context, tx pgx.Tx, id string) (*PaymentEntry, error) {
	var (
		pe                                  PaymentEntry
		submittedAt, cancelledAt            *time.Time
		referenceDate                       *time.Time
		partyType, partyID, refNo, remarks  *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, payment_type, party_type, party_id, posting_date, fiscal_year_id,
		       paid_from_account_id, paid_to_account_id, paid_from_currency, paid_to_currency,
		       paid_amount, received_amount, source_exchange_rate, target_exchange_rate,
		       base_paid_amount, base_received_amount,
		       total_allocated_amount, base_total_allocated_amount, unallocated_amount, total_deductions,
		       reference_no, reference_date, remarks, docstatus, submitted_at, cancelled_at, created_at, updated_at
		FROM payment_entry WHERE id = $1`, id).
		Scan(&pe.ID, &pe.Name, &pe.CompanyID, &pe.PaymentType, &partyType, &partyID, &pe.PostingDate, &pe.FiscalYearID,
			&pe.PaidFromAccountID, &pe.PaidToAccountID, &pe.PaidFromCurrency, &pe.PaidToCurrency,
			&pe.PaidAmount, &pe.ReceivedAmount, &pe.SourceExchangeRate, &pe.TargetExchangeRate,
			&pe.BasePaidAmount, &pe.BaseReceivedAmount,
			&pe.TotalAllocatedAmount, &pe.BaseTotalAllocatedAmount, &pe.UnallocatedAmount, &pe.TotalDeductions,
			&refNo, &referenceDate, &remarks, &pe.Docstatus, &submittedAt, &cancelledAt, &pe.CreatedAt, &pe.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("payment_entry %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if partyType != nil {
		pe.PartyType = *partyType
	}
	if partyID != nil {
		pe.PartyID = *partyID
	}
	if refNo != nil {
		pe.ReferenceNo = *refNo
	}
	if remarks != nil {
		pe.Remarks = *remarks
	}
	pe.SubmittedAt = submittedAt
	pe.CancelledAt = cancelledAt
	pe.ReferenceDate = referenceDate

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, reference_doctype, reference_id, reference_name, total_amount, allocated_amount, base_allocated_amount
		FROM payment_entry_reference WHERE payment_entry_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r PaymentEntryReference
		if err := rows.Scan(&r.ID, &r.RowIndex, &r.ReferenceDoctype, &r.ReferenceID, &r.ReferenceName,
			&r.TotalAmount, &r.AllocatedAmount, &r.BaseAllocatedAmount); err != nil {
			rows.Close()
			return nil, err
		}
		pe.References = append(pe.References, r)
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
		SELECT id, row_index, account_id, description, amount, coalesce(cost_center_id,''), coalesce(withholding_tax_type_id,'')
		FROM payment_entry_deduction WHERE payment_entry_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d PaymentEntryDeduction
		if err := rows.Scan(&d.ID, &d.RowIndex, &d.AccountID, &d.Description, &d.Amount,
			&d.CostCenterID, &d.WithholdingTaxTypeID); err != nil {
			return nil, err
		}
		pe.Deductions = append(pe.Deductions, d)
	}
	return &pe, nil
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

// applyReferenceUpdates moves paid_amount / outstanding_amount on each referenced invoice.
// sign = +1 on submit, -1 on cancel. Routes to sales_invoice or purchase_invoice.
func applyReferenceUpdates(ctx context.Context, tx pgx.Tx, refs []PaymentEntryReference, userID string, sign int) error {
	for _, r := range refs {
		amt := r.AllocatedAmount
		base := r.BaseAllocatedAmount
		if sign < 0 {
			amt = amt.Neg()
			base = base.Neg()
		}
		var stmt string
		switch r.ReferenceDoctype {
		case "sales_invoice":
			stmt = `UPDATE sales_invoice SET
				paid_amount             = paid_amount + $1,
				outstanding_amount      = outstanding_amount - $1,
				base_paid_amount        = base_paid_amount + $2,
				base_outstanding_amount = base_outstanding_amount - $2,
				updated_by              = $3
			WHERE id = $4`
		case "purchase_invoice":
			stmt = `UPDATE purchase_invoice SET
				paid_amount             = paid_amount + $1,
				outstanding_amount      = outstanding_amount - $1,
				base_paid_amount        = base_paid_amount + $2,
				base_outstanding_amount = base_outstanding_amount - $2,
				updated_by              = $3
			WHERE id = $4`
		default:
			return fmt.Errorf("payment_entry: unsupported reference_doctype %q", r.ReferenceDoctype)
		}
		if _, err := tx.Exec(ctx, stmt, amt, base, userID, r.ReferenceID); err != nil {
			return err
		}
	}
	return nil
}

// computeUnallocated enforces the Payment Entry invariant:
//
//	total_allocated + unallocated = paid + deductions
//
// and returns the unallocated remainder (>= 0). Negative means the user
// allocated more to invoices than cash + deductions can settle.
func computeUnallocated(paid, allocated, deductions decimal.Decimal) (decimal.Decimal, error) {
	diff := paid.Add(deductions).Sub(allocated)
	if diff.IsNegative() {
		return decimal.Zero, fmt.Errorf(
			"payment_entry: over-allocated: allocated %s > paid %s + deductions %s",
			allocated, paid, deductions)
	}
	return diff, nil
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
