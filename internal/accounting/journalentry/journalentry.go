// Package journalentry implements the Journal Entry document — the Phase 0 exit slice.
// Submitting a Journal Entry posts balanced GL entries inside a single transaction;
// cancelling it posts inverse entries.
package journalentry

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
	Doctype     = "journal_entry"
	VoucherType = "Journal Entry"
)

type JournalEntry struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	CompanyID     string           `json:"company_id"`
	PostingDate   time.Time        `json:"posting_date"`
	FiscalYearID  string           `json:"fiscal_year_id"`
	Currency      string           `json:"currency"`
	ExchangeRate  decimal.Decimal  `json:"exchange_rate"`
	TotalDebit    decimal.Decimal  `json:"total_debit"`
	TotalCredit   decimal.Decimal  `json:"total_credit"`
	UserRemark    string           `json:"user_remark,omitempty"`
	Docstatus     submittable.Status `json:"docstatus"`
	SubmittedAt   *time.Time       `json:"submitted_at,omitempty"`
	CancelledAt   *time.Time       `json:"cancelled_at,omitempty"`
	AmendedFrom   string           `json:"amended_from,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Accounts      []Line           `json:"accounts"`
}

type Line struct {
	ID                       string          `json:"id"`
	RowIndex                 int             `json:"row_index"`
	AccountID                string          `json:"account_id"`
	PartyType                string          `json:"party_type,omitempty"`
	PartyID                  string          `json:"party_id,omitempty"`
	CostCenterID             string          `json:"cost_center_id,omitempty"`
	ProjectID                string          `json:"project_id,omitempty"`
	Debit                    decimal.Decimal `json:"debit"`
	Credit                   decimal.Decimal `json:"credit"`
	DebitInAccountCurrency   decimal.Decimal `json:"debit_in_account_currency"`
	CreditInAccountCurrency  decimal.Decimal `json:"credit_in_account_currency"`
	Reference                string          `json:"reference,omitempty"`
}

type JournalEntryCreateInput struct {
	CompanyID    string         `json:"company_id,omitempty"`
	PostingDate  string         `json:"posting_date"` // YYYY-MM-DD
	Currency     string         `json:"currency,omitempty"`
	ExchangeRate string         `json:"exchange_rate,omitempty"`
	UserRemark   string         `json:"user_remark,omitempty"`
	Accounts     []JournalEntryLineInput    `json:"accounts"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

type JournalEntryLineInput struct {
	AccountID    string `json:"account_id"`
	PartyType    string `json:"party_type,omitempty"`
	PartyID      string `json:"party_id,omitempty"`
	CostCenterID string `json:"cost_center_id,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	Debit        string `json:"debit,omitempty"`
	Credit       string `json:"credit,omitempty"`
	Reference    string `json:"reference,omitempty"`
}

type Service struct {
	db *dbx.DB
	// Approvals is optional. When set, Submit() consults active approval_rules
	// for this doctype + company; missing approvals block submit.
	Approvals approvalChecker
	// Workflow is optional. Gates submit by role.
	Workflow workflowGate
	// Notifier is optional. Submit() fires journal_entry.submitted after commit.
	Notifier notifier
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

type notifier interface {
	Fire(eventKey string, payload map[string]any)
}

type workflowGate interface {
	CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// CreateDraft inserts a Journal Entry in draft state. No GL impact.
func (s *Service) CreateDraft(ctx context.Context, in JournalEntryCreateInput) (*JournalEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("journal_entry: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("journal_entry.company_id: required")
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("journal_entry.posting_date: %w", err)
	}
	if len(in.Accounts) < 2 {
		return nil, errors.New("journal_entry.accounts: at least 2 lines required")
	}

	lines, totDr, totCr, err := parseLines(in.Accounts)
	if err != nil {
		return nil, err
	}

	rate := decimal.NewFromInt(1)
	if in.ExchangeRate != "" {
		r, err := decimal.NewFromString(in.ExchangeRate)
		if err != nil {
			return nil, fmt.Errorf("journal_entry.exchange_rate: %w", err)
		}
		if !r.IsPositive() {
			return nil, errors.New("journal_entry.exchange_rate: must be > 0")
		}
		rate = r
	}

	id := dbx.NewIDWithPrefix("je")
	var out JournalEntry
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		cur := in.Currency
		if cur == "" {
			if err := tx.QueryRow(ctx, `SELECT default_currency FROM company WHERE id = $1`, in.CompanyID).Scan(&cur); err != nil {
				return fmt.Errorf("company currency: %w", err)
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

		if _, err := tx.Exec(ctx, `
			INSERT INTO journal_entry (
				id, name, company_id, posting_date, fiscal_year_id, currency, exchange_rate,
				total_debit, total_credit, user_remark, custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`,
			id, name, in.CompanyID, pd, fyID, cur, rate, totDr, totCr, nullable(in.UserRemark), cf, p.UserID); err != nil {
			return err
		}

		for i, l := range lines {
			lid := dbx.NewIDWithPrefix("jea")
			if _, err := tx.Exec(ctx, `
				INSERT INTO journal_entry_account (
					id, journal_entry_id, row_index, account_id, party_type, party_id,
					cost_center_id, project_id, debit, credit,
					debit_in_account_currency, credit_in_account_currency, reference
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				lid, id, i+1, l.AccountID, nullable(l.PartyType), nullable(l.PartyID),
				nullable(l.CostCenterID), nullable(l.ProjectID),
				l.Debit, l.Credit, l.DebitInAccountCurrency, l.CreditInAccountCurrency, nullable(l.Reference)); err != nil {
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

// Submit transitions a draft to submitted and posts balanced GL entries.
func (s *Service) Submit(ctx context.Context, id string) (*JournalEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("journal_entry: unauthenticated")
	}
	var out JournalEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		je, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if je.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}

		// Workflow role gate.
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, "journal_entry"); err != nil {
				return err
			}
		}
		// Approval gate. Both total_debit and total_credit are exposed; rules
		// typically key off total_debit since the two are equal at this point.
		if s.Approvals != nil {
			td, _ := je.TotalDebit.Float64()
			tc, _ := je.TotalCredit.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, "journal_entry", je.ID, je.Name, je.CompanyID,
				map[string]any{
					"total_debit":  td,
					"total_credit": tc,
					"amount":       td,
				}); err != nil {
				return err
			}
		}

		entries := make([]ledger.Entry, 0, len(je.Accounts))
		for _, l := range je.Accounts {
			e := ledger.Entry{
				AccountID:                l.AccountID,
				CostCenterID:             l.CostCenterID,
				ProjectID:                l.ProjectID,
				Debit:                    l.Debit,
				Credit:                   l.Credit,
				DebitInAccountCurrency:   l.DebitInAccountCurrency,
				CreditInAccountCurrency:  l.CreditInAccountCurrency,
				Remarks:                  l.Reference,
			}
			if l.PartyType != "" {
				e.PartyType = ledger.PartyType(l.PartyType)
				e.PartyID = l.PartyID
			}
			if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, l.AccountID).Scan(&e.AccountCurrency); err != nil {
				return fmt.Errorf("account %s: %w", l.AccountID, err)
			}
			entries = append(entries, e)
		}
		v := ledger.Voucher{
			Type:         VoucherType,
			ID:           je.ID,
			Name:         je.Name,
			CompanyID:    je.CompanyID,
			PostingDate:  je.PostingDate,
			FiscalYearID: je.FiscalYearID,
			CreatedBy:    p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE journal_entry SET docstatus = 1, submitted_at = now(), submitted_by = $1, updated_by = $1 WHERE id = $2`,
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
		dr, _ := out.TotalDebit.Float64()
		s.Notifier.Fire("journal_entry.submitted", map[string]any{
			"company_id":    out.CompanyID,
			"doctype":       Doctype,
			"document_id":   out.ID,
			"document_name": out.Name,
			"total_debit":   dr,
			"summary": fmt.Sprintf("Journal entry %s submitted, total %s %s",
				out.Name, out.Currency, out.TotalDebit.String()),
			"JournalEntry": out,
		})
	}
	return &out, err
}

// Cancel reverses a submitted Journal Entry's GL entries.
func (s *Service) Cancel(ctx context.Context, id string) (*JournalEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("journal_entry: unauthenticated")
	}
	var out JournalEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		je, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if je.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if _, err := ledger.CancelGL(ctx, tx, VoucherType, id, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE journal_entry SET docstatus = 2, cancelled_at = now(), cancelled_by = $1, updated_by = $1 WHERE id = $2`,
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

func (s *Service) Get(ctx context.Context, id string) (*JournalEntry, error) {
	var out *JournalEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		je, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = je
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]JournalEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM journal_entry WHERE company_id = $1 ORDER BY posting_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]JournalEntry, 0, len(ids))
	for _, id := range ids {
		je, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *je)
	}
	return out, nil
}

// --- helpers ---

func parseLines(in []JournalEntryLineInput) ([]Line, decimal.Decimal, decimal.Decimal, error) {
	out := make([]Line, 0, len(in))
	totDr := decimal.Zero
	totCr := decimal.Zero
	debits := make([]decimal.Decimal, 0, len(in))
	credits := make([]decimal.Decimal, 0, len(in))
	for i, l := range in {
		if l.AccountID == "" {
			return nil, decimal.Zero, decimal.Zero, fmt.Errorf("accounts[%d].account_id: required", i)
		}
		dr, err := parseAmt(l.Debit)
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, fmt.Errorf("accounts[%d].debit: %w", i, err)
		}
		cr, err := parseAmt(l.Credit)
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, fmt.Errorf("accounts[%d].credit: %w", i, err)
		}
		if dr.IsZero() == cr.IsZero() {
			return nil, decimal.Zero, decimal.Zero, fmt.Errorf("accounts[%d]: exactly one of debit/credit must be > 0", i)
		}
		out = append(out, Line{
			AccountID:               l.AccountID,
			PartyType:               l.PartyType,
			PartyID:                 l.PartyID,
			CostCenterID:            l.CostCenterID,
			ProjectID:               l.ProjectID,
			Debit:                   dr,
			Credit:                  cr,
			DebitInAccountCurrency:  dr,
			CreditInAccountCurrency: cr,
			Reference:               l.Reference,
		})
		debits = append(debits, dr)
		credits = append(credits, cr)
		totDr = totDr.Add(dr)
		totCr = totCr.Add(cr)
	}
	if err := money.SumBalanced(debits, credits); err != nil {
		return nil, decimal.Zero, decimal.Zero, fmt.Errorf("%w: %s", ledger.ErrImbalanced, err.Error())
	}
	return out, totDr, totCr, nil
}

func parseAmt(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, err
	}
	if err := money.Validate(d); err != nil {
		return decimal.Zero, err
	}
	return d.Round(money.Precision), nil
}

func load(ctx context.Context, tx pgx.Tx, id string) (*JournalEntry, error) {
	var (
		je          JournalEntry
		submittedAt, cancelledAt *time.Time
		amendedFrom              *string
		userRemark               *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, posting_date, fiscal_year_id, currency, exchange_rate,
		       total_debit, total_credit, user_remark, docstatus, submitted_at, cancelled_at, amended_from,
		       created_at, updated_at
		FROM journal_entry WHERE id = $1`, id).
		Scan(&je.ID, &je.Name, &je.CompanyID, &je.PostingDate, &je.FiscalYearID, &je.Currency, &je.ExchangeRate,
			&je.TotalDebit, &je.TotalCredit, &userRemark, &je.Docstatus, &submittedAt, &cancelledAt, &amendedFrom,
			&je.CreatedAt, &je.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("journal_entry %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if userRemark != nil {
		je.UserRemark = *userRemark
	}
	je.SubmittedAt = submittedAt
	je.CancelledAt = cancelledAt
	if amendedFrom != nil {
		je.AmendedFrom = *amendedFrom
	}

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, account_id, coalesce(party_type,''), coalesce(party_id,''),
		       coalesce(cost_center_id,''), coalesce(project_id,''),
		       debit, credit, debit_in_account_currency, credit_in_account_currency, coalesce(reference,'')
		FROM journal_entry_account WHERE journal_entry_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.RowIndex, &l.AccountID, &l.PartyType, &l.PartyID,
			&l.CostCenterID, &l.ProjectID,
			&l.Debit, &l.Credit, &l.DebitInAccountCurrency, &l.CreditInAccountCurrency, &l.Reference); err != nil {
			return nil, err
		}
		je.Accounts = append(je.Accounts, l)
	}
	return &je, rows.Err()
}

// pickFiscalYear finds the fiscal year covering pd for the company. Returns an error if none configured.
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

// pickNamingSeries returns the default series for the doctype, preferring per-company over global.
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
