package reports

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// ---- Cash Flow (simplified direct-method) ----
//
// We treat any account with account_type IN ('cash','bank') as a cash account.
// Every GL entry on a cash account in the period is grouped by the counterpart
// account's root_type into 3 buckets:
//   operating  = counterpart root_type IN ('income','expense')
//   investing  = counterpart root_type = 'asset' (other than cash itself)
//   financing  = counterpart root_type IN ('liability','equity')
//
// The counterpart per gl_entry is derived from sibling rows of the same voucher.

type CashFlowLine struct {
	VoucherType   string          `json:"voucher_type"`
	VoucherName   string          `json:"voucher_name"`
	PostingDate   time.Time       `json:"posting_date"`
	Category      string          `json:"category"` // operating | investing | financing
	Inflow        decimal.Decimal `json:"inflow"`
	Outflow       decimal.Decimal `json:"outflow"`
	Net           decimal.Decimal `json:"net"`
}

type CashFlowSection struct {
	Inflow  decimal.Decimal `json:"inflow"`
	Outflow decimal.Decimal `json:"outflow"`
	Net     decimal.Decimal `json:"net"`
}

type CashFlowReport struct {
	CompanyID      string             `json:"company_id"`
	FromDate       string             `json:"from_date"`
	ToDate         string             `json:"to_date"`
	OpeningCash    decimal.Decimal    `json:"opening_cash"`
	ClosingCash    decimal.Decimal    `json:"closing_cash"`
	Operating      CashFlowSection    `json:"operating"`
	Investing      CashFlowSection    `json:"investing"`
	Financing      CashFlowSection    `json:"financing"`
	NetChange      decimal.Decimal    `json:"net_change"`
	Movements      []CashFlowLine     `json:"movements"`
}

func (s *Service) CashFlow(ctx context.Context, companyID string, fromDate, toDate time.Time) (*CashFlowReport, error) {
	report := &CashFlowReport{
		CompanyID: companyID,
		FromDate:  fromDate.Format("2006-01-02"),
		ToDate:    toDate.Format("2006-01-02"),
	}

	// Opening cash: cumulative net Dr on cash/bank accounts < fromDate
	if err := s.db.QueryRow(ctx, `
		SELECT coalesce(sum(g.debit - g.credit), 0)
		FROM gl_entry g
		JOIN account a ON a.id = g.account_id
		WHERE g.company_id = $1 AND a.account_type IN ('cash','bank') AND g.posting_date < $2`,
		companyID, fromDate).Scan(&report.OpeningCash); err != nil {
		return nil, err
	}

	// Movements grouped by counterpart bucket. For each cash-account GL row in the period,
	// determine the counterpart category by looking at the other rows of the same voucher.
	rows, err := s.db.Query(ctx, `
		WITH cash_rows AS (
		  SELECT g.id, g.voucher_type, g.voucher_id, g.voucher_name, g.posting_date,
		         g.debit AS inflow, g.credit AS outflow
		  FROM gl_entry g
		  JOIN account a ON a.id = g.account_id
		  WHERE g.company_id = $1 AND a.account_type IN ('cash','bank')
		    AND g.posting_date BETWEEN $2 AND $3
		),
		counterparts AS (
		  SELECT cr.id AS cash_id,
		         CASE
		           WHEN bool_or(a2.root_type IN ('income','expense'))   THEN 'operating'
		           WHEN bool_or(a2.root_type IN ('liability','equity')) THEN 'financing'
		           WHEN bool_or(a2.root_type = 'asset')                 THEN 'investing'
		           ELSE 'operating'
		         END AS category
		  FROM cash_rows cr
		  JOIN gl_entry g2 ON g2.voucher_type = cr.voucher_type AND g2.voucher_id = cr.voucher_id AND g2.id <> cr.id
		  JOIN account a2 ON a2.id = g2.account_id
		  WHERE a2.account_type IS NULL OR a2.account_type NOT IN ('cash','bank')
		  GROUP BY cr.id
		)
		SELECT cr.voucher_type, cr.voucher_name, cr.posting_date,
		       coalesce(cp.category, 'operating') AS category,
		       cr.inflow, cr.outflow
		FROM cash_rows cr
		LEFT JOIN counterparts cp ON cp.cash_id = cr.id
		ORDER BY cr.posting_date, cr.voucher_name`,
		companyID, fromDate, toDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var l CashFlowLine
		if err := rows.Scan(&l.VoucherType, &l.VoucherName, &l.PostingDate, &l.Category, &l.Inflow, &l.Outflow); err != nil {
			return nil, err
		}
		l.Net = l.Inflow.Sub(l.Outflow)

		var sec *CashFlowSection
		switch l.Category {
		case "operating":
			sec = &report.Operating
		case "investing":
			sec = &report.Investing
		case "financing":
			sec = &report.Financing
		default:
			sec = &report.Operating
		}
		sec.Inflow = sec.Inflow.Add(l.Inflow)
		sec.Outflow = sec.Outflow.Add(l.Outflow)
		sec.Net = sec.Net.Add(l.Net)

		report.Movements = append(report.Movements, l)
		report.NetChange = report.NetChange.Add(l.Net)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	report.ClosingCash = report.OpeningCash.Add(report.NetChange)
	return report, nil
}

// ---- PPN summary ----
//
// Output VAT (PPN Keluaran): credits to tax-type accounts on sales-side vouchers.
// Input VAT (PPN Masukan):   debits to tax-type accounts on purchase-side vouchers.
// Net PPN payable = Output − Input.

type PPNRow struct {
	AccountID     string          `json:"account_id"`
	AccountName   string          `json:"account_name"`
	Debit         decimal.Decimal `json:"debit"`
	Credit        decimal.Decimal `json:"credit"`
	Net           decimal.Decimal `json:"net"`
}

type PPNSummaryReport struct {
	CompanyID    string          `json:"company_id"`
	FromDate     string          `json:"from_date"`
	ToDate       string          `json:"to_date"`
	OutputVAT    []PPNRow        `json:"output_vat"`
	InputVAT     []PPNRow        `json:"input_vat"`
	TotalOutput  decimal.Decimal `json:"total_output_vat"`
	TotalInput   decimal.Decimal `json:"total_input_vat"`
	NetPayable   decimal.Decimal `json:"net_payable"`
}

func (s *Service) PPNSummary(ctx context.Context, companyID string, fromDate, toDate time.Time) (*PPNSummaryReport, error) {
	report := &PPNSummaryReport{
		CompanyID: companyID,
		FromDate:  fromDate.Format("2006-01-02"),
		ToDate:    toDate.Format("2006-01-02"),
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.name,
		       coalesce(sum(g.debit), 0)  AS dr,
		       coalesce(sum(g.credit), 0) AS cr
		FROM account a
		JOIN gl_entry g ON g.account_id = a.id
		WHERE a.company_id = $1 AND a.account_type = 'tax'
		  AND g.posting_date BETWEEN $2 AND $3
		GROUP BY a.id, a.name
		ORDER BY a.name`,
		companyID, fromDate, toDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r PPNRow
		if err := rows.Scan(&r.AccountID, &r.AccountName, &r.Debit, &r.Credit); err != nil {
			return nil, err
		}
		r.Net = r.Credit.Sub(r.Debit)
		// Heuristic: predominantly-credit accounts are output VAT (liabilities); predominantly-debit are input (assets).
		if r.Credit.GreaterThan(r.Debit) {
			report.OutputVAT = append(report.OutputVAT, r)
			report.TotalOutput = report.TotalOutput.Add(r.Net)
		} else {
			r.Net = r.Debit.Sub(r.Credit)
			report.InputVAT = append(report.InputVAT, r)
			report.TotalInput = report.TotalInput.Add(r.Net)
		}
	}
	report.NetPayable = report.TotalOutput.Sub(report.TotalInput)
	return report, rows.Err()
}

// ---- HTTP ----

func RegisterCashFlowAndTax(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "cash-flow-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/cash-flow",
		Summary:     "Cash Flow (simplified direct method by counterpart category)",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *cfIn) (*cfOut, error) {
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
		r, err := h.Service.CashFlow(ctx, co, from, to)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &cfOut{Body: *r}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ppn-summary-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/ppn-summary",
		Summary:     "PPN summary: output (Keluaran) vs input (Masukan), net payable",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *ppnIn) (*ppnOut, error) {
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
		r, err := h.Service.PPNSummary(ctx, co, from, to)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &ppnOut{Body: *r}, nil
	})
}

type cfIn struct {
	CompanyID string `query:"company_id"`
	FromDate  string `query:"from_date"`
	ToDate    string `query:"to_date"`
}
type cfOut struct{ Body CashFlowReport }

type ppnIn struct {
	CompanyID string `query:"company_id"`
	FromDate  string `query:"from_date"`
	ToDate    string `query:"to_date"`
}
type ppnOut struct{ Body PPNSummaryReport }
