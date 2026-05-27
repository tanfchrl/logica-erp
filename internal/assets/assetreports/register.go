// Package assetreports serves read-only aggregations over the asset tables.
// v1: Fixed Asset Register — one row per asset with gross / acc dep / NBV
// as of a chosen date, with optional grouping by category / status /
// location.
package assetreports

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// RegisterRow is one row in the Fixed Asset Register.
type RegisterRow struct {
	AssetID                 string          `json:"asset_id"`
	Name                    string          `json:"name"`
	AssetName               string          `json:"asset_name"`
	CategoryID              string          `json:"category_id,omitempty"`
	CategoryName            string          `json:"category_name,omitempty"`
	PurchaseDate            time.Time       `json:"purchase_date"`
	Gross                   decimal.Decimal `json:"gross"`
	AccumulatedDepreciation decimal.Decimal `json:"accumulated_depreciation"`
	NBV                     decimal.Decimal `json:"nbv"`
	Status                  string          `json:"status"`
	Custodian               string          `json:"custodian,omitempty"`
	Location                string          `json:"location,omitempty"`
}

// RegisterResult is the response body — flat rows + roll-up totals so the
// FE doesn't need to re-sum.
type RegisterResult struct {
	AsOf                    time.Time       `json:"as_of"`
	CompanyID               string          `json:"company_id"`
	Rows                    []RegisterRow   `json:"rows"`
	TotalGross              decimal.Decimal `json:"total_gross"`
	TotalAccumulatedDep     decimal.Decimal `json:"total_accumulated_depreciation"`
	TotalNBV                decimal.Decimal `json:"total_nbv"`
	GroupBy                 string          `json:"group_by"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// Register returns the Fixed Asset Register snapshot as of `asOf`.
//
// accumulated_depreciation is computed dynamically from depreciation_schedule
// rows where schedule_date <= asOf AND is_posted = true. That way the
// "as of" date doesn't lie — even rows posted today are reflected.
//
// `groupBy` is informational (sent back in the response) so the FE can
// render section headers; the rows themselves come back unsorted by group.
func (s *Service) Register(ctx context.Context, companyID string, asOf time.Time, groupBy string,
	includeZeroNBV bool) (*RegisterResult, error) {
	if companyID == "" {
		return nil, errors.New("register: company_id required")
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC().Truncate(24 * time.Hour)
	}
	if groupBy == "" {
		groupBy = "category"
	}
	// The depreciation_schedule join must SUM the posted rows up to asOf
	// rather than trusting asset.accumulated_depreciation (which is a
	// running total that may include posts past the chosen as-of).
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.name, a.asset_name,
		       coalesce(a.asset_category_id, ''), coalesce(ac.name, ''),
		       a.purchase_date, a.gross_purchase_amount,
		       coalesce((SELECT sum(depreciation_amount)
		                 FROM depreciation_schedule
		                 WHERE asset_id = a.id
		                   AND is_posted = true
		                   AND schedule_date <= $2), 0)
		         AS acc_dep_as_of,
		       a.status,
		       coalesce(a.current_custodian, ''), coalesce(a.current_location, '')
		FROM asset a
		LEFT JOIN asset_category ac ON ac.id = a.asset_category_id
		WHERE a.company_id = $1
		  AND a.docstatus <> 2
		ORDER BY a.purchase_date, a.name`, companyID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &RegisterResult{AsOf: asOf, CompanyID: companyID, GroupBy: groupBy, Rows: []RegisterRow{}}
	for rows.Next() {
		var r RegisterRow
		if err := rows.Scan(&r.AssetID, &r.Name, &r.AssetName,
			&r.CategoryID, &r.CategoryName,
			&r.PurchaseDate, &r.Gross, &r.AccumulatedDepreciation,
			&r.Status, &r.Custodian, &r.Location); err != nil {
			return nil, err
		}
		r.NBV = r.Gross.Sub(r.AccumulatedDepreciation)
		if !includeZeroNBV && r.NBV.IsZero() && r.Status != "Sold" && r.Status != "Scrapped" {
			// Zero-NBV but still active (fully depreciated, not disposed)
			// is exactly what the user probably wants to suppress.
			continue
		}
		out.Rows = append(out.Rows, r)
		out.TotalGross = out.TotalGross.Add(r.Gross)
		out.TotalAccumulatedDep = out.TotalAccumulatedDep.Add(r.AccumulatedDepreciation)
		out.TotalNBV = out.TotalNBV.Add(r.NBV)
	}
	return out, rows.Err()
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "asset-register-report", Method: http.MethodGet,
		Path: "/assets/reports/register", Summary: "Fixed Asset Register as of a chosen date",
		Tags: []string{"Assets / Reports"},
	}, func(ctx context.Context, in *regIn) (*regOut, error) {
		// The user can read this report if they can read assets at all —
		// no separate permission for the report.
		if err := h.Perm.Check(ctx, "asset", permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		var asOf time.Time
		if strings.TrimSpace(in.AsOf) != "" {
			t, err := time.Parse("2006-01-02", in.AsOf)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "as_of: "+err.Error())
			}
			asOf = t
		}
		r, err := h.Service.Register(ctx, co, asOf, in.GroupBy, in.IncludeZeroNBV)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &regOut{Body: *r}, nil
	})
}

type (
	regIn struct {
		AsOf           string `query:"as_of"            doc:"YYYY-MM-DD; defaults to today UTC"`
		GroupBy        string `query:"group_by"         doc:"category | status | location | none (informational)"`
		IncludeZeroNBV bool   `query:"include_zero_nbv" doc:"set true to keep fully-depreciated assets in the rows"`
	}
	regOut struct{ Body RegisterResult }
)
