package reports

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// AgeingRow is one party-level row in an ageing report.
type AgeingRow struct {
	PartyID          string          `json:"party_id"`
	PartyName        string          `json:"party_name"`
	Current          decimal.Decimal `json:"current"`           // not yet overdue
	D0To30           decimal.Decimal `json:"d_0_30"`            // 1–30 days overdue
	D31To60          decimal.Decimal `json:"d_31_60"`           // 31–60
	D61To90          decimal.Decimal `json:"d_61_90"`           // 61–90
	D90Plus          decimal.Decimal `json:"d_90_plus"`         // 90+
	TotalOutstanding decimal.Decimal `json:"total_outstanding"`
}

type AgeingReport struct {
	CompanyID string      `json:"company_id"`
	AsOf      string      `json:"as_of"`
	Side      string      `json:"side"` // "receivable" | "payable"
	Rows      []AgeingRow `json:"rows"`
	Totals    AgeingRow   `json:"totals"`
}

// AccountsReceivableAgeing buckets all submitted SI with outstanding > 0 by overdue days.
func (s *Service) AccountsReceivableAgeing(ctx context.Context, companyID string, asOf time.Time) (*AgeingReport, error) {
	return s.ageing(ctx, companyID, asOf, "receivable")
}

// AccountsPayableAgeing — same for PI.
func (s *Service) AccountsPayableAgeing(ctx context.Context, companyID string, asOf time.Time) (*AgeingReport, error) {
	return s.ageing(ctx, companyID, asOf, "payable")
}

func (s *Service) ageing(ctx context.Context, companyID string, asOf time.Time, side string) (*AgeingReport, error) {
	report := &AgeingReport{
		CompanyID: companyID,
		AsOf:      asOf.Format("2006-01-02"),
		Side:      side,
	}
	var q string
	switch side {
	case "receivable":
		q = `
			SELECT i.customer_id AS party_id, c.display_name AS party_name,
			       sum(CASE WHEN i.due_date >= $2                                   THEN i.outstanding_amount ELSE 0 END) AS current,
			       sum(CASE WHEN ($2::date - i.due_date) BETWEEN 1  AND 30          THEN i.outstanding_amount ELSE 0 END) AS d_0_30,
			       sum(CASE WHEN ($2::date - i.due_date) BETWEEN 31 AND 60          THEN i.outstanding_amount ELSE 0 END) AS d_31_60,
			       sum(CASE WHEN ($2::date - i.due_date) BETWEEN 61 AND 90          THEN i.outstanding_amount ELSE 0 END) AS d_61_90,
			       sum(CASE WHEN ($2::date - i.due_date) > 90                       THEN i.outstanding_amount ELSE 0 END) AS d_90_plus,
			       sum(i.outstanding_amount) AS total_outstanding
			FROM sales_invoice i
			JOIN customer c ON c.id = i.customer_id
			WHERE i.company_id = $1 AND i.docstatus = 1 AND i.outstanding_amount > 0 AND i.posting_date <= $2
			GROUP BY i.customer_id, c.display_name
			ORDER BY c.display_name`
	case "payable":
		q = `
			SELECT i.supplier_id AS party_id, s.display_name AS party_name,
			       sum(CASE WHEN i.due_date >= $2                                   THEN i.outstanding_amount ELSE 0 END) AS current,
			       sum(CASE WHEN ($2::date - i.due_date) BETWEEN 1  AND 30          THEN i.outstanding_amount ELSE 0 END) AS d_0_30,
			       sum(CASE WHEN ($2::date - i.due_date) BETWEEN 31 AND 60          THEN i.outstanding_amount ELSE 0 END) AS d_31_60,
			       sum(CASE WHEN ($2::date - i.due_date) BETWEEN 61 AND 90          THEN i.outstanding_amount ELSE 0 END) AS d_61_90,
			       sum(CASE WHEN ($2::date - i.due_date) > 90                       THEN i.outstanding_amount ELSE 0 END) AS d_90_plus,
			       sum(i.outstanding_amount) AS total_outstanding
			FROM purchase_invoice i
			JOIN supplier s ON s.id = i.supplier_id
			WHERE i.company_id = $1 AND i.docstatus = 1 AND i.outstanding_amount > 0 AND i.posting_date <= $2
			GROUP BY i.supplier_id, s.display_name
			ORDER BY s.display_name`
	default:
		return nil, fmt.Errorf("ageing: invalid side %q", side)
	}

	rows, err := s.db.Query(ctx, q, companyID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r AgeingRow
		if err := rows.Scan(&r.PartyID, &r.PartyName, &r.Current, &r.D0To30, &r.D31To60, &r.D61To90, &r.D90Plus, &r.TotalOutstanding); err != nil {
			return nil, err
		}
		report.Totals.Current = report.Totals.Current.Add(r.Current)
		report.Totals.D0To30 = report.Totals.D0To30.Add(r.D0To30)
		report.Totals.D31To60 = report.Totals.D31To60.Add(r.D31To60)
		report.Totals.D61To90 = report.Totals.D61To90.Add(r.D61To90)
		report.Totals.D90Plus = report.Totals.D90Plus.Add(r.D90Plus)
		report.Totals.TotalOutstanding = report.Totals.TotalOutstanding.Add(r.TotalOutstanding)
		report.Rows = append(report.Rows, r)
	}
	return report, rows.Err()
}

// ---- HTTP ----

func RegisterAgeing(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "ar-ageing-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/accounts-receivable-ageing",
		Summary:     "Accounts Receivable ageing by party",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *ageingIn) (*ageingOut, error) {
		return runAgeing(ctx, h, in, "receivable")
	})
	huma.Register(api, huma.Operation{
		OperationID: "ap-ageing-report",
		Method:      http.MethodGet,
		Path:        "/accounting/reports/accounts-payable-ageing",
		Summary:     "Accounts Payable ageing by party",
		Tags:        []string{"Accounting / Reports"},
	}, func(ctx context.Context, in *ageingIn) (*ageingOut, error) {
		return runAgeing(ctx, h, in, "payable")
	})
}

func runAgeing(ctx context.Context, h *Handler, in *ageingIn, side string) (*ageingOut, error) {
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
	var (
		r   *AgeingReport
		err error
	)
	switch side {
	case "receivable":
		r, err = h.Service.AccountsReceivableAgeing(ctx, co, asOf)
	case "payable":
		r, err = h.Service.AccountsPayableAgeing(ctx, co, asOf)
	}
	if err != nil {
		return nil, httpx.MapError(err)
	}
	return &ageingOut{Body: *r}, nil
}

type ageingIn struct {
	CompanyID string `query:"company_id"`
	AsOf      string `query:"as_of"`
}
type ageingOut struct{ Body AgeingReport }
