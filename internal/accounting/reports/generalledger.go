package reports

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// GeneralLedgerRow is one GL entry as returned to the API.
type GeneralLedgerRow struct {
	PostingDate   time.Time       `json:"posting_date"`
	AccountID     string          `json:"account_id"`
	AccountName   string          `json:"account_name"`
	VoucherType   string          `json:"voucher_type"`
	VoucherID     string          `json:"voucher_id"`
	VoucherName   string          `json:"voucher_name"`
	PartyType     string          `json:"party_type,omitempty"`
	PartyID       string          `json:"party_id,omitempty"`
	PartyName     string          `json:"party_name,omitempty"`
	Debit         decimal.Decimal `json:"debit"`
	Credit        decimal.Decimal `json:"credit"`
	Balance       decimal.Decimal `json:"balance"`
	Remarks       string          `json:"remarks,omitempty"`
}

type GeneralLedgerReport struct {
	CompanyID      string             `json:"company_id"`
	AccountID      string             `json:"account_id,omitempty"`
	PartyType      string             `json:"party_type,omitempty"`
	PartyID        string             `json:"party_id,omitempty"`
	FromDate       string             `json:"from_date"`
	ToDate         string             `json:"to_date"`
	OpeningBalance decimal.Decimal    `json:"opening_balance"`
	ClosingBalance decimal.Decimal    `json:"closing_balance"`
	TotalDebit     decimal.Decimal    `json:"total_debit"`
	TotalCredit    decimal.Decimal    `json:"total_credit"`
	Rows           []GeneralLedgerRow `json:"rows"`
}

// GeneralLedger returns activity for an account (or all accounts) and optional party in a date range.
func (s *Service) GeneralLedger(ctx context.Context, companyID, accountID, partyType, partyID, voucherType string, fromDate, toDate time.Time) (*GeneralLedgerReport, error) {
	report := &GeneralLedgerReport{
		CompanyID: companyID,
		AccountID: accountID,
		PartyType: partyType,
		PartyID:   partyID,
		FromDate:  fromDate.Format("2006-01-02"),
		ToDate:    toDate.Format("2006-01-02"),
	}

	// Opening balance: only meaningful when a single account is filtered.
	if accountID != "" {
		args := []any{companyID, accountID, fromDate}
		q := `SELECT coalesce(sum(debit), 0) - coalesce(sum(credit), 0)
		      FROM gl_entry WHERE company_id = $1 AND account_id = $2 AND posting_date < $3`
		idx := 4
		if partyType != "" {
			q += fmt.Sprintf(" AND party_type = $%d", idx)
			args = append(args, partyType)
			idx++
		}
		if partyID != "" {
			q += fmt.Sprintf(" AND party_id = $%d", idx)
			args = append(args, partyID)
		}
		if err := s.db.QueryRow(ctx, q, args...).Scan(&report.OpeningBalance); err != nil {
			return nil, fmt.Errorf("opening: %w", err)
		}
	}

	// Period rows
	q := `
		SELECT g.posting_date, g.account_id, a.name, g.voucher_type, g.voucher_id, g.voucher_name,
		       coalesce(g.party_type,''), coalesce(g.party_id,''),
		       coalesce(cust.display_name, sup.display_name, ''),
		       g.debit, g.credit, coalesce(g.remarks,'')
		FROM gl_entry g
		JOIN account a ON a.id = g.account_id
		LEFT JOIN customer cust ON g.party_type = 'customer' AND cust.id = g.party_id
		LEFT JOIN supplier sup  ON g.party_type = 'supplier' AND sup.id = g.party_id
		WHERE g.company_id = $1 AND g.posting_date BETWEEN $2 AND $3`
	args := []any{companyID, fromDate, toDate}
	idx := 4
	if accountID != "" {
		q += fmt.Sprintf(" AND g.account_id = $%d", idx)
		args = append(args, accountID)
		idx++
	}
	if partyType != "" {
		q += fmt.Sprintf(" AND g.party_type = $%d", idx)
		args = append(args, partyType)
		idx++
	}
	if partyID != "" {
		q += fmt.Sprintf(" AND g.party_id = $%d", idx)
		args = append(args, partyID)
		idx++
	}
	if voucherType != "" {
		q += fmt.Sprintf(" AND g.voucher_type = $%d", idx)
		args = append(args, voucherType)
	}
	q += ` ORDER BY g.posting_date, g.created_at, g.id`

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	running := report.OpeningBalance
	for rows.Next() {
		var r GeneralLedgerRow
		if err := rows.Scan(&r.PostingDate, &r.AccountID, &r.AccountName, &r.VoucherType, &r.VoucherID, &r.VoucherName,
			&r.PartyType, &r.PartyID, &r.PartyName,
			&r.Debit, &r.Credit, &r.Remarks); err != nil {
			return nil, err
		}
		running = running.Add(r.Debit).Sub(r.Credit)
		r.Balance = running
		report.Rows = append(report.Rows, r)
		report.TotalDebit = report.TotalDebit.Add(r.Debit)
		report.TotalCredit = report.TotalCredit.Add(r.Credit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	report.ClosingBalance = running
	return report, nil
}

// ---- HTTP ----

func RegisterGeneralLedger(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "general-ledger-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/general-ledger",
		Summary:     "General Ledger entries, optionally filtered by account, party, voucher type",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *glIn) (*glOut, error) {
		if err := h.Perm.Check(ctx, "report", permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := in.CompanyID
		if co == "" {
			co = auth.CompanyFromContext(ctx)
		}
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "company_id or X-Company-Id required")
		}
		from, to, err := parseDateRange(in.FromDate, in.ToDate)
		if err != nil {
			return nil, huma.NewError(http.StatusBadRequest, err.Error())
		}
		r, err := h.Service.GeneralLedger(ctx, co, in.AccountID, strings.ToLower(in.PartyType), in.PartyID, in.VoucherType, from, to)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &glOut{Body: *r}, nil
	})
}

type glIn struct {
	CompanyID   string `query:"company_id"`
	AccountID   string `query:"account_id"`
	PartyType   string `query:"party_type"`
	PartyID     string `query:"party_id"`
	VoucherType string `query:"voucher_type"`
	FromDate    string `query:"from_date"`
	ToDate      string `query:"to_date"`
}
type glOut struct{ Body GeneralLedgerReport }

// parseDateRange normalizes optional date strings.
func parseDateRange(fromS, toS string) (time.Time, time.Time, error) {
	var (
		from, to time.Time
		err      error
	)
	if toS == "" {
		to = time.Now().UTC().Truncate(24 * time.Hour)
	} else {
		to, err = time.Parse("2006-01-02", toS)
		if err != nil {
			return from, to, fmt.Errorf("to_date: %w", err)
		}
	}
	if fromS == "" {
		from = time.Date(to.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	} else {
		from, err = time.Parse("2006-01-02", fromS)
		if err != nil {
			return from, to, fmt.Errorf("from_date: %w", err)
		}
	}
	if from.After(to) {
		return from, to, fmt.Errorf("from_date must be <= to_date")
	}
	return from, to, nil
}
