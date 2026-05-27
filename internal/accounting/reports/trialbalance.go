// Package reports holds the accounting report queries. Phase 1A ships
// Trial Balance only; GL / P&L / BS / Ageing land in Phase 1B.
package reports

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// TrialBalanceRow is one row of a trial balance.
type TrialBalanceRow struct {
	AccountID      string          `json:"account_id"`
	AccountName    string          `json:"account_name"`
	AccountNumber  string          `json:"account_number,omitempty"`
	RootType       string          `json:"root_type"`
	OpeningDebit   decimal.Decimal `json:"opening_debit"`
	OpeningCredit  decimal.Decimal `json:"opening_credit"`
	PeriodDebit    decimal.Decimal `json:"period_debit"`
	PeriodCredit   decimal.Decimal `json:"period_credit"`
	ClosingDebit   decimal.Decimal `json:"closing_debit"`
	ClosingCredit  decimal.Decimal `json:"closing_credit"`
}

type TrialBalanceTotals struct {
	OpeningDebit  decimal.Decimal `json:"opening_debit"`
	OpeningCredit decimal.Decimal `json:"opening_credit"`
	PeriodDebit   decimal.Decimal `json:"period_debit"`
	PeriodCredit  decimal.Decimal `json:"period_credit"`
	ClosingDebit  decimal.Decimal `json:"closing_debit"`
	ClosingCredit decimal.Decimal `json:"closing_credit"`
}

type TrialBalanceReport struct {
	CompanyID string              `json:"company_id"`
	FromDate  string              `json:"from_date"`
	ToDate    string              `json:"to_date"`
	Rows      []TrialBalanceRow   `json:"rows"`
	Totals    TrialBalanceTotals  `json:"totals"`
	Balanced  bool                `json:"balanced"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) TrialBalance(ctx context.Context, companyID string, fromDate, toDate time.Time, includeZero bool) (*TrialBalanceReport, error) {
	rows, err := s.db.Query(ctx, `
		WITH opening AS (
		  SELECT account_id,
		         coalesce(sum(debit), 0) - coalesce(sum(credit), 0) AS net
		  FROM gl_entry
		  WHERE company_id = $1 AND posting_date < $2		  GROUP BY account_id
		),
		period AS (
		  SELECT account_id,
		         coalesce(sum(debit), 0)  AS dr,
		         coalesce(sum(credit), 0) AS cr
		  FROM gl_entry
		  WHERE company_id = $1 AND posting_date BETWEEN $2 AND $3		  GROUP BY account_id
		)
		SELECT a.id, a.name, coalesce(a.account_number,''), a.root_type,
		       coalesce(o.net, 0) AS opening_net,
		       coalesce(p.dr,  0) AS period_dr,
		       coalesce(p.cr,  0) AS period_cr
		FROM account a
		LEFT JOIN opening o ON o.account_id = a.id
		LEFT JOIN period  p ON p.account_id = a.id
		WHERE a.company_id = $1 AND a.is_group = false AND a.is_deleted = false
		ORDER BY a.root_type, a.account_number NULLS LAST, a.name`, companyID, fromDate, toDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	report := &TrialBalanceReport{
		CompanyID: companyID,
		FromDate:  fromDate.Format("2006-01-02"),
		ToDate:    toDate.Format("2006-01-02"),
	}
	for rows.Next() {
		var (
			r          TrialBalanceRow
			openingNet decimal.Decimal
		)
		if err := rows.Scan(&r.AccountID, &r.AccountName, &r.AccountNumber, &r.RootType,
			&openingNet, &r.PeriodDebit, &r.PeriodCredit); err != nil {
			return nil, err
		}
		// Split openingNet into debit/credit columns.
		if openingNet.IsNegative() {
			r.OpeningCredit = openingNet.Neg()
		} else {
			r.OpeningDebit = openingNet
		}
		// closing = opening + period
		closingNet := openingNet.Add(r.PeriodDebit).Sub(r.PeriodCredit)
		if closingNet.IsNegative() {
			r.ClosingCredit = closingNet.Neg()
		} else {
			r.ClosingDebit = closingNet
		}

		if !includeZero {
			zero := r.OpeningDebit.IsZero() && r.OpeningCredit.IsZero() &&
				r.PeriodDebit.IsZero() && r.PeriodCredit.IsZero() &&
				r.ClosingDebit.IsZero() && r.ClosingCredit.IsZero()
			if zero {
				continue
			}
		}

		report.Totals.OpeningDebit = report.Totals.OpeningDebit.Add(r.OpeningDebit)
		report.Totals.OpeningCredit = report.Totals.OpeningCredit.Add(r.OpeningCredit)
		report.Totals.PeriodDebit = report.Totals.PeriodDebit.Add(r.PeriodDebit)
		report.Totals.PeriodCredit = report.Totals.PeriodCredit.Add(r.PeriodCredit)
		report.Totals.ClosingDebit = report.Totals.ClosingDebit.Add(r.ClosingDebit)
		report.Totals.ClosingCredit = report.Totals.ClosingCredit.Add(r.ClosingCredit)
		report.Rows = append(report.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	report.Balanced = report.Totals.PeriodDebit.Equal(report.Totals.PeriodCredit) &&
		report.Totals.ClosingDebit.Equal(report.Totals.ClosingCredit)
	return report, nil
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	RegisterGeneralLedger(api, h)
	RegisterFinancialStatements(api, h)
	RegisterAgeing(api, h)
	RegisterCashFlowAndTax(api, h)

	huma.Register(api, huma.Operation{
		OperationID: "trial-balance-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/trial-balance",
		Summary:     "Trial Balance for a company over a date range",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *tbIn) (*tbOut, error) {
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
		var (
			from, to time.Time
			err      error
		)
		if in.ToDate == "" {
			to = time.Now().UTC().Truncate(24 * time.Hour)
		} else {
			to, err = time.Parse("2006-01-02", in.ToDate)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "to_date: "+err.Error())
			}
		}
		if in.FromDate == "" {
			// default to fiscal year start covering to_date
			from = time.Date(to.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		} else {
			from, err = time.Parse("2006-01-02", in.FromDate)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "from_date: "+err.Error())
			}
		}
		if !from.Before(to.AddDate(0, 0, 1)) {
			return nil, huma.NewError(http.StatusBadRequest, "from_date must be <= to_date")
		}

		r, err := h.Service.TrialBalance(ctx, co, from, to, in.IncludeZero)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		if !r.Balanced {
			return nil, fmt.Errorf("trial balance imbalanced: dr=%s cr=%s",
				r.Totals.ClosingDebit, r.Totals.ClosingCredit)
		}
		return &tbOut{Body: *r}, nil
	})
}

type tbIn struct {
	CompanyID   string `query:"company_id"`
	FromDate    string `query:"from_date"`
	ToDate      string `query:"to_date"`
	IncludeZero bool   `query:"include_zero"`
}
type tbOut struct{ Body TrialBalanceReport }
