package reports

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// ---- Profit & Loss ----

type PLAccount struct {
	AccountID     string          `json:"account_id"`
	AccountName   string          `json:"account_name"`
	AccountNumber string          `json:"account_number,omitempty"`
	Amount        decimal.Decimal `json:"amount"`
}

type ProfitAndLossReport struct {
	CompanyID    string          `json:"company_id"`
	FromDate     string          `json:"from_date"`
	ToDate       string          `json:"to_date"`
	Income       []PLAccount     `json:"income"`
	Expense      []PLAccount     `json:"expense"`
	TotalIncome  decimal.Decimal `json:"total_income"`
	TotalExpense decimal.Decimal `json:"total_expense"`
	NetProfit    decimal.Decimal `json:"net_profit"`
}

func (s *Service) ProfitAndLoss(ctx context.Context, companyID string, fromDate, toDate time.Time) (*ProfitAndLossReport, error) {
	report := &ProfitAndLossReport{
		CompanyID: companyID,
		FromDate:  fromDate.Format("2006-01-02"),
		ToDate:    toDate.Format("2006-01-02"),
	}

	// Income: credit - debit; positive = net revenue
	if err := fillPLSide(ctx, s, companyID, fromDate, toDate, "income", true, &report.Income); err != nil {
		return nil, err
	}
	for _, a := range report.Income {
		report.TotalIncome = report.TotalIncome.Add(a.Amount)
	}
	// Expense: debit - credit; positive = net expense
	if err := fillPLSide(ctx, s, companyID, fromDate, toDate, "expense", false, &report.Expense); err != nil {
		return nil, err
	}
	for _, a := range report.Expense {
		report.TotalExpense = report.TotalExpense.Add(a.Amount)
	}
	report.NetProfit = report.TotalIncome.Sub(report.TotalExpense)
	return report, nil
}

func fillPLSide(ctx context.Context, s *Service, companyID string, from, to time.Time, rootType string, creditMinusDebit bool, into *[]PLAccount) error {
	var sumExpr string
	if creditMinusDebit {
		sumExpr = "coalesce(sum(credit - debit), 0)"
	} else {
		sumExpr = "coalesce(sum(debit - credit), 0)"
	}
	q := fmt.Sprintf(`
		SELECT a.id, a.name, coalesce(a.account_number,''), %s
		FROM account a
		LEFT JOIN gl_entry g
		  ON g.account_id = a.id
		 AND g.company_id = $1
		 AND g.posting_date BETWEEN $2 AND $3
		WHERE a.company_id = $1 AND a.is_group = false AND a.is_deleted = false AND a.root_type = $4
		GROUP BY a.id, a.name, a.account_number
		HAVING %s <> 0
		ORDER BY a.account_number NULLS LAST, a.name`, sumExpr, sumExpr)
	rows, err := s.db.Query(ctx, q, companyID, from, to, rootType)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a PLAccount
		if err := rows.Scan(&a.AccountID, &a.AccountName, &a.AccountNumber, &a.Amount); err != nil {
			return err
		}
		*into = append(*into, a)
	}
	return rows.Err()
}

// ---- Balance Sheet ----

type BSAccount struct {
	AccountID     string          `json:"account_id"`
	AccountName   string          `json:"account_name"`
	AccountNumber string          `json:"account_number,omitempty"`
	Amount        decimal.Decimal `json:"amount"`
}

type BalanceSheetReport struct {
	CompanyID         string          `json:"company_id"`
	AsOf              string          `json:"as_of"`
	Assets            []BSAccount     `json:"assets"`
	Liabilities       []BSAccount     `json:"liabilities"`
	Equity            []BSAccount     `json:"equity"`
	PeriodNetProfit   decimal.Decimal `json:"period_net_profit"`
	TotalAssets       decimal.Decimal `json:"total_assets"`
	TotalLiabilities  decimal.Decimal `json:"total_liabilities"`
	TotalEquity       decimal.Decimal `json:"total_equity"`
	Balanced          bool            `json:"balanced"`
}

// BalanceSheet returns balances cumulative through `asOf` (inclusive).
// Period Net Profit is computed against the fiscal year covering `asOf`.
func (s *Service) BalanceSheet(ctx context.Context, companyID string, asOf time.Time) (*BalanceSheetReport, error) {
	report := &BalanceSheetReport{
		CompanyID: companyID,
		AsOf:      asOf.Format("2006-01-02"),
	}

	// Asset accounts: balance = sum(debit - credit) cumulative
	if err := fillBSSide(ctx, s, companyID, asOf, "asset", false, &report.Assets); err != nil {
		return nil, err
	}
	for _, a := range report.Assets {
		report.TotalAssets = report.TotalAssets.Add(a.Amount)
	}
	// Liability accounts: balance = sum(credit - debit) cumulative
	if err := fillBSSide(ctx, s, companyID, asOf, "liability", true, &report.Liabilities); err != nil {
		return nil, err
	}
	for _, l := range report.Liabilities {
		report.TotalLiabilities = report.TotalLiabilities.Add(l.Amount)
	}
	// Equity (excluding period NP)
	if err := fillBSSide(ctx, s, companyID, asOf, "equity", true, &report.Equity); err != nil {
		return nil, err
	}
	for _, e := range report.Equity {
		report.TotalEquity = report.TotalEquity.Add(e.Amount)
	}

	// Period Net Profit: income - expense over the FY containing asOf (or since asOf year start if no FY found).
	from, err := fiscalYearStart(ctx, s, companyID, asOf)
	if err != nil {
		return nil, err
	}
	pl, err := s.ProfitAndLoss(ctx, companyID, from, asOf)
	if err != nil {
		return nil, err
	}
	report.PeriodNetProfit = pl.NetProfit
	report.TotalEquity = report.TotalEquity.Add(report.PeriodNetProfit)

	report.Balanced = report.TotalAssets.Equal(report.TotalLiabilities.Add(report.TotalEquity))
	return report, nil
}

func fillBSSide(ctx context.Context, s *Service, companyID string, asOf time.Time, rootType string, creditMinusDebit bool, into *[]BSAccount) error {
	var sumExpr string
	if creditMinusDebit {
		sumExpr = "coalesce(sum(credit - debit), 0)"
	} else {
		sumExpr = "coalesce(sum(debit - credit), 0)"
	}
	q := fmt.Sprintf(`
		SELECT a.id, a.name, coalesce(a.account_number,''), %s
		FROM account a
		LEFT JOIN gl_entry g
		  ON g.account_id = a.id
		 AND g.company_id = $1
		 AND g.posting_date <= $2
		WHERE a.company_id = $1 AND a.is_group = false AND a.is_deleted = false AND a.root_type = $3
		GROUP BY a.id, a.name, a.account_number
		HAVING %s <> 0
		ORDER BY a.account_number NULLS LAST, a.name`, sumExpr, sumExpr)
	rows, err := s.db.Query(ctx, q, companyID, asOf, rootType)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a BSAccount
		if err := rows.Scan(&a.AccountID, &a.AccountName, &a.AccountNumber, &a.Amount); err != nil {
			return err
		}
		*into = append(*into, a)
	}
	return rows.Err()
}

func fiscalYearStart(ctx context.Context, s *Service, companyID string, asOf time.Time) (time.Time, error) {
	var start time.Time
	err := s.db.QueryRow(ctx, `
		SELECT fy.start_date FROM fiscal_year fy
		JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
		WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
		ORDER BY fy.start_date DESC LIMIT 1`, companyID, asOf).Scan(&start)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Date(asOf.Year(), 1, 1, 0, 0, 0, 0, time.UTC), nil
	}
	return start, err
}

// ---- HTTP ----

func RegisterFinancialStatements(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "profit-and-loss-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/profit-and-loss",
		Summary:     "Profit & Loss for a date range",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *plIn) (*plOut, error) {
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
		r, err := h.Service.ProfitAndLoss(ctx, co, from, to)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &plOut{Body: *r}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "balance-sheet-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/balance-sheet",
		Summary:     "Balance Sheet as of a date",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *bsIn) (*bsOut, error) {
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
		var asOf time.Time
		if in.AsOf == "" {
			asOf = time.Now().UTC().Truncate(24 * time.Hour)
		} else {
			t, err := time.Parse("2006-01-02", in.AsOf)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "as_of: "+err.Error())
			}
			asOf = t
		}
		r, err := h.Service.BalanceSheet(ctx, co, asOf)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		if !r.Balanced {
			return nil, fmt.Errorf("balance sheet imbalanced: assets=%s vs liab+equity=%s",
				r.TotalAssets, r.TotalLiabilities.Add(r.TotalEquity))
		}
		return &bsOut{Body: *r}, nil
	})
}

type plIn struct {
	CompanyID string `query:"company_id"`
	FromDate  string `query:"from_date"`
	ToDate    string `query:"to_date"`
}
type plOut struct{ Body ProfitAndLossReport }

type bsIn struct {
	CompanyID string `query:"company_id"`
	AsOf      string `query:"as_of"`
}
type bsOut struct{ Body BalanceSheetReport }
