// Package asset implements the Asset doctype with straight-line monthly depreciation.
//
// Submit:
//   1. Validates accounts (asset, accumulated dep, dep expense).
//   2. Generates the depreciation schedule: (gross - salvage) / useful_life_months evenly.
//   3. Marks docstatus=1, status='Submitted'.
//
// PostDepreciation:
//   For each schedule row with schedule_date <= as_of AND NOT is_posted:
//     Dr Depreciation Expense  amount
//     Cr Accumulated Depreciation amount
//   Marks the row is_posted, updates asset.accumulated_depreciation + next_depreciation_date + status.
//
// Cancel: reverses any posted GL entries via ledger.CancelGL and marks docstatus=2.
package asset

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/ledger"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const (
	Doctype     = "asset"
	VoucherType = "Depreciation"
)

type Asset struct {
	ID                                 string             `json:"id"`
	Name                               string             `json:"name"`
	CompanyID                          string             `json:"company_id"`
	AssetName                          string             `json:"asset_name"`
	AssetCategoryID                    string             `json:"asset_category_id,omitempty"`
	PurchaseDate                       time.Time          `json:"purchase_date"`
	GrossPurchaseAmount                decimal.Decimal    `json:"gross_purchase_amount"`
	ExpectedValueAfterUsefulLife       decimal.Decimal    `json:"expected_value_after_useful_life"`
	UsefulLifeMonths                   int                `json:"useful_life_months"`
	DepreciationMethod                 string             `json:"depreciation_method"`
	DepreciationRatePct                decimal.Decimal    `json:"depreciation_rate_pct"`
	ProRataBasis                       bool               `json:"pro_rata_basis"`
	AssetAccountID                     string             `json:"asset_account_id"`
	AccumulatedDepreciationAccountID   string             `json:"accumulated_depreciation_account_id"`
	DepreciationExpenseAccountID       string             `json:"depreciation_expense_account_id"`
	CostCenterID                       string             `json:"cost_center_id,omitempty"`
	AccumulatedDepreciation            decimal.Decimal    `json:"accumulated_depreciation"`
	Status                             string             `json:"status"`
	NextDepreciationDate               *time.Time         `json:"next_depreciation_date,omitempty"`
	CurrentCustodian                   string             `json:"current_custodian,omitempty"`
	CurrentLocation                    string             `json:"current_location,omitempty"`
	CurrentLocationID                  string             `json:"current_location_id,omitempty"`
	Docstatus                          submittable.Status `json:"docstatus"`
	CreatedAt                          time.Time          `json:"created_at"`
	UpdatedAt                          time.Time          `json:"updated_at"`
	Schedule                           []DepreciationRow  `json:"schedule,omitempty"`
}

type DepreciationRow struct {
	ID                  string          `json:"id"`
	RowIndex            int             `json:"row_index"`
	ScheduleDate        time.Time       `json:"schedule_date"`
	DepreciationAmount  decimal.Decimal `json:"depreciation_amount"`
	AccumulatedAfter    decimal.Decimal `json:"accumulated_after"`
	IsPosted            bool            `json:"is_posted"`
	PostedVoucherID     string          `json:"posted_voucher_id,omitempty"`
	PostedAt            *time.Time      `json:"posted_at,omitempty"`
}

type AssetCreateInput struct {
	CompanyID                          string `json:"company_id,omitempty"`
	AssetName                          string `json:"asset_name"`
	AssetCategoryID                    string `json:"asset_category_id,omitempty"`
	PurchaseDate                       string `json:"purchase_date"`
	GrossPurchaseAmount                string `json:"gross_purchase_amount"`
	ExpectedValueAfterUsefulLife       string `json:"expected_value_after_useful_life,omitempty"`
	UsefulLifeMonths                   int    `json:"useful_life_months"`
	DepreciationMethod                 string `json:"depreciation_method,omitempty"`
	DepreciationRatePct                string `json:"depreciation_rate_pct,omitempty"`
	ProRataBasis                       *bool  `json:"pro_rata_basis,omitempty" doc:"defaults to true (first row pro-rated)"`
	AssetAccountID                     string `json:"asset_account_id"`
	AccumulatedDepreciationAccountID   string `json:"accumulated_depreciation_account_id"`
	DepreciationExpenseAccountID       string `json:"depreciation_expense_account_id"`
	CostCenterID                       string `json:"cost_center_id,omitempty"`
}

type Service struct {
	db        *dbx.DB
	Approvals approvalChecker
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in AssetCreateInput) (*Asset, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("asset.company_id: required")
	}
	in.AssetName = strings.TrimSpace(in.AssetName)
	if in.AssetName == "" {
		return nil, errors.New("asset.asset_name: required")
	}
	pd, err := time.Parse("2006-01-02", in.PurchaseDate)
	if err != nil {
		return nil, fmt.Errorf("purchase_date: %w", err)
	}
	gross, err := decimal.NewFromString(strings.TrimSpace(in.GrossPurchaseAmount))
	if err != nil || !gross.IsPositive() {
		return nil, errors.New("gross_purchase_amount: must be > 0")
	}
	salvage := decimal.Zero
	if in.ExpectedValueAfterUsefulLife != "" {
		s, err := decimal.NewFromString(strings.TrimSpace(in.ExpectedValueAfterUsefulLife))
		if err != nil || s.IsNegative() {
			return nil, errors.New("expected_value_after_useful_life: must be >= 0")
		}
		salvage = s
	}
	if salvage.GreaterThanOrEqual(gross) {
		return nil, errors.New("expected_value_after_useful_life: must be < gross_purchase_amount")
	}
	if in.UsefulLifeMonths <= 0 {
		return nil, errors.New("useful_life_months: must be > 0")
	}
	method := in.DepreciationMethod
	if method == "" {
		method = MethodStraightLine
	}
	switch method {
	case MethodStraightLine, MethodWrittenDownValue, MethodManual:
	default:
		return nil, fmt.Errorf("depreciation_method: %q not supported", method)
	}
	rate := decimal.Zero
	if strings.TrimSpace(in.DepreciationRatePct) != "" {
		r, err := decimal.NewFromString(strings.TrimSpace(in.DepreciationRatePct))
		if err != nil || r.IsNegative() {
			return nil, errors.New("depreciation_rate_pct: must be a non-negative decimal")
		}
		rate = r
	}
	proRata := true
	if in.ProRataBasis != nil {
		proRata = *in.ProRataBasis
	}
	if in.AssetAccountID == "" || in.AccumulatedDepreciationAccountID == "" || in.DepreciationExpenseAccountID == "" {
		return nil, errors.New("asset accounts: all three (asset, accumulated_dep, dep_expense) are required")
	}

	id := dbx.NewIDWithPrefix("asset")
	var out Asset
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, pd, nil)
		if err != nil {
			return err
		}
		var rateArg any
		if rate.IsPositive() {
			rateArg = rate
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset (
				id, name, company_id, asset_name, asset_category_id, purchase_date,
				gross_purchase_amount, expected_value_after_useful_life, useful_life_months, depreciation_method,
				depreciation_rate_pct, pro_rata_basis,
				asset_account_id, accumulated_depreciation_account_id, depreciation_expense_account_id, cost_center_id,
				created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)`,
			id, name, in.CompanyID, in.AssetName, nullable(in.AssetCategoryID), pd,
			gross, salvage, in.UsefulLifeMonths, method,
			rateArg, proRata,
			in.AssetAccountID, in.AccumulatedDepreciationAccountID, in.DepreciationExpenseAccountID,
			nullable(in.CostCenterID), p.UserID); err != nil {
			return err
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

// AssetUpdateInput holds editable Draft fields. company_id and name are
// immutable. The depreciation schedule is generated on Submit, so updating
// the financial inputs here just changes what gets baked in then.
type AssetUpdateInput struct {
	AssetName                        string `json:"asset_name"`
	AssetCategoryID                  string `json:"asset_category_id,omitempty"`
	PurchaseDate                     string `json:"purchase_date"`
	GrossPurchaseAmount              string `json:"gross_purchase_amount"`
	ExpectedValueAfterUsefulLife     string `json:"expected_value_after_useful_life,omitempty"`
	UsefulLifeMonths                 int    `json:"useful_life_months"`
	DepreciationMethod               string `json:"depreciation_method,omitempty"`
	DepreciationRatePct              string `json:"depreciation_rate_pct,omitempty"`
	ProRataBasis                     *bool  `json:"pro_rata_basis,omitempty"`
	AssetAccountID                   string `json:"asset_account_id"`
	AccumulatedDepreciationAccountID string `json:"accumulated_depreciation_account_id"`
	DepreciationExpenseAccountID     string `json:"depreciation_expense_account_id"`
	CostCenterID                     string `json:"cost_center_id,omitempty"`
}

func (s *Service) Update(ctx context.Context, id string, in AssetUpdateInput) (*Asset, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset: unauthenticated")
	}
	in.AssetName = strings.TrimSpace(in.AssetName)
	if in.AssetName == "" {
		return nil, errors.New("asset.asset_name: required")
	}
	pd, err := time.Parse("2006-01-02", in.PurchaseDate)
	if err != nil {
		return nil, fmt.Errorf("purchase_date: %w", err)
	}
	gross, err := decimal.NewFromString(strings.TrimSpace(in.GrossPurchaseAmount))
	if err != nil || !gross.IsPositive() {
		return nil, errors.New("gross_purchase_amount: must be > 0")
	}
	salvage := decimal.Zero
	if in.ExpectedValueAfterUsefulLife != "" {
		s, err := decimal.NewFromString(strings.TrimSpace(in.ExpectedValueAfterUsefulLife))
		if err != nil || s.IsNegative() {
			return nil, errors.New("expected_value_after_useful_life: must be >= 0")
		}
		salvage = s
	}
	if salvage.GreaterThanOrEqual(gross) {
		return nil, errors.New("expected_value_after_useful_life: must be < gross_purchase_amount")
	}
	if in.UsefulLifeMonths <= 0 {
		return nil, errors.New("useful_life_months: must be > 0")
	}
	method := in.DepreciationMethod
	if method == "" {
		method = MethodStraightLine
	}
	switch method {
	case MethodStraightLine, MethodWrittenDownValue, MethodManual:
	default:
		return nil, fmt.Errorf("depreciation_method: %q not supported", method)
	}
	rate := decimal.Zero
	if strings.TrimSpace(in.DepreciationRatePct) != "" {
		r, err := decimal.NewFromString(strings.TrimSpace(in.DepreciationRatePct))
		if err != nil || r.IsNegative() {
			return nil, errors.New("depreciation_rate_pct: must be a non-negative decimal")
		}
		rate = r
	}
	proRata := true
	if in.ProRataBasis != nil {
		proRata = *in.ProRataBasis
	}
	if in.AssetAccountID == "" || in.AccumulatedDepreciationAccountID == "" || in.DepreciationExpenseAccountID == "" {
		return nil, errors.New("asset accounts: all three (asset, accumulated_dep, dep_expense) are required")
	}

	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if existing.Docstatus != submittable.Draft {
			return fmt.Errorf("asset: cannot edit (docstatus=%d)", existing.Docstatus)
		}
		var rateArg any
		if rate.IsPositive() {
			rateArg = rate
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset SET
			  asset_name                          = $2,
			  asset_category_id                   = $3,
			  purchase_date                       = $4,
			  gross_purchase_amount               = $5,
			  expected_value_after_useful_life    = $6,
			  useful_life_months                  = $7,
			  depreciation_method                 = $8,
			  depreciation_rate_pct               = $9,
			  pro_rata_basis                      = $10,
			  asset_account_id                    = $11,
			  accumulated_depreciation_account_id = $12,
			  depreciation_expense_account_id     = $13,
			  cost_center_id                      = $14,
			  updated_by                          = $15
			WHERE id = $1 AND docstatus = 0`,
			id, in.AssetName, nullable(in.AssetCategoryID), pd,
			gross, salvage, in.UsefulLifeMonths, method,
			rateArg, proRata,
			in.AssetAccountID, in.AccumulatedDepreciationAccountID, in.DepreciationExpenseAccountID,
			nullable(in.CostCenterID), p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Submit(ctx context.Context, id string) (*Asset, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset: unauthenticated")
	}
	var out Asset
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		a, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Approvals != nil {
			cost, _ := a.GrossPurchaseAmount.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, "asset", a.ID, a.Name, a.CompanyID,
				map[string]any{"gross_purchase_amount": cost, "amount": cost}); err != nil {
				return err
			}
		}

		// Generate the schedule through the pure helper so the same math is
		// testable without a DB and so future methods (WDV / manual) flow
		// through one code path.
		rows, err := BuildSchedule(ScheduleParams{
			Gross:               a.GrossPurchaseAmount,
			Salvage:             a.ExpectedValueAfterUsefulLife,
			UsefulLifeMonths:    a.UsefulLifeMonths,
			Method:              a.DepreciationMethod,
			PurchaseDate:        a.PurchaseDate,
			ProRataBasis:        a.ProRataBasis,
			DepreciationRatePct: a.DepreciationRatePct,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			rid := dbx.NewIDWithPrefix("depsch")
			if _, err := tx.Exec(ctx, `
				INSERT INTO depreciation_schedule (id, asset_id, row_index, schedule_date, depreciation_amount, accumulated_after)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				rid, a.ID, r.RowIndex, r.ScheduleDate, r.DepreciationAmount, r.AccumulatedAfter); err != nil {
				return err
			}
		}

		var nextDate time.Time
		if len(rows) > 0 {
			nextDate = rows[0].ScheduleDate
		} else {
			nextDate = a.PurchaseDate.AddDate(0, 1, 0)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset SET docstatus = 1, status = 'Submitted', submitted_at = now(), submitted_by = $1,
			       next_depreciation_date = $2, updated_by = $1
			WHERE id = $3`, p.UserID, nextDate, id); err != nil {
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

// PostDepreciation posts any not-yet-posted schedule rows up to (and including) asOf.
// Each row becomes a small GL voucher of its own so cancellation can be granular.
func (s *Service) PostDepreciation(ctx context.Context, assetID string, asOf time.Time) (*Asset, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset: unauthenticated")
	}
	var out Asset
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		a, err := load(ctx, tx, assetID)
		if err != nil {
			return err
		}
		if a.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}

		rows, err := tx.Query(ctx, `
			SELECT id, row_index, schedule_date, depreciation_amount
			FROM depreciation_schedule
			WHERE asset_id = $1 AND is_posted = false AND schedule_date <= $2
			ORDER BY row_index`, assetID, asOf)
		if err != nil {
			return err
		}
		type pending struct {
			id    string
			idx   int
			date  time.Time
			amt   decimal.Decimal
		}
		var batch []pending
		for rows.Next() {
			var pp pending
			if err := rows.Scan(&pp.id, &pp.idx, &pp.date, &pp.amt); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, pp)
		}
		rows.Close()
		if len(batch) == 0 {
			return errors.New("asset: no depreciation due")
		}

		// Find fiscal year for the latest posted date in the batch.
		var fyID, accCur, expCur string
		if err := tx.QueryRow(ctx, `
			SELECT fy.id FROM fiscal_year fy
			JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
			WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
			ORDER BY fy.start_date DESC LIMIT 1`, a.CompanyID, batch[len(batch)-1].date).Scan(&fyID); err != nil {
			return fmt.Errorf("fiscal year: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, a.AccumulatedDepreciationAccountID).Scan(&accCur); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, a.DepreciationExpenseAccountID).Scan(&expCur); err != nil {
			return err
		}

		// Post one combined voucher containing all pending rows.
		voucherID := dbx.NewIDWithPrefix("depvch")
		entries := []ledger.Entry{}
		totalAmt := decimal.Zero
		for _, b := range batch {
			entries = append(entries, ledger.Entry{
				AccountID:              a.DepreciationExpenseAccountID,
				CostCenterID:           a.CostCenterID,
				Debit:                  b.amt,
				AccountCurrency:        expCur,
				DebitInAccountCurrency: b.amt,
				Against:                a.AccumulatedDepreciationAccountID,
				Remarks:                fmt.Sprintf("Depreciation %s — %s", a.Name, b.date.Format("2006-01-02")),
			})
			entries = append(entries, ledger.Entry{
				AccountID:               a.AccumulatedDepreciationAccountID,
				Credit:                  b.amt,
				AccountCurrency:         accCur,
				CreditInAccountCurrency: b.amt,
				Against:                 a.DepreciationExpenseAccountID,
				Remarks:                 fmt.Sprintf("Depreciation %s — %s", a.Name, b.date.Format("2006-01-02")),
			})
			totalAmt = totalAmt.Add(b.amt)
		}
		v := ledger.Voucher{
			Type: VoucherType, ID: voucherID, Name: a.Name + " / Depreciation",
			CompanyID: a.CompanyID, PostingDate: batch[len(batch)-1].date, FiscalYearID: fyID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}

		// Flag posted rows.
		for _, b := range batch {
			if _, err := tx.Exec(ctx, `
				UPDATE depreciation_schedule SET is_posted = true, posted_voucher_id = $1, posted_at = now() WHERE id = $2`,
				voucherID, b.id); err != nil {
				return err
			}
		}

		// Update asset.
		newAcc := a.AccumulatedDepreciation.Add(totalAmt)
		var nextDate *time.Time
		var status string
		err = tx.QueryRow(ctx, `
			SELECT min(schedule_date) FROM depreciation_schedule
			WHERE asset_id = $1 AND is_posted = false`, assetID).Scan(&nextDate)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if nextDate == nil {
			status = "Fully Depreciated"
		} else {
			status = "Partially Depreciated"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset SET accumulated_depreciation = $1, next_depreciation_date = $2, status = $3, updated_by = $4
			WHERE id = $5`, newAcc, nextDate, status, p.UserID, assetID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, assetID, p.UserID, audit.ActionUpdate, audit.Diff{After: map[string]any{"posted_amount": totalAmt.String()}}); err != nil {
			return err
		}
		loaded, err := load(ctx, tx, assetID)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

// RunResult captures the outcome of a batch depreciation run. Each asset's
// per-transaction failure is captured rather than aborting the run — one
// asset with a missing fiscal year shouldn't block the other 49.
type RunResult struct {
	AsOf          time.Time      `json:"as_of"`
	CompanyID     string         `json:"company_id"`
	Processed     int            `json:"processed"`
	TotalAmount   string         `json:"total_amount"`
	AssetsPosted  []RunPostedRow `json:"assets_posted"`
	AssetsFailed  []RunFailedRow `json:"assets_failed"`
}
type RunPostedRow struct {
	AssetID   string `json:"asset_id"`
	AssetName string `json:"asset_name"`
	Amount    string `json:"amount"`
	Rows      int    `json:"rows"`
}
type RunFailedRow struct {
	AssetID   string `json:"asset_id"`
	AssetName string `json:"asset_name"`
	Error     string `json:"error"`
}

// RunDepreciation finds every submitted asset in the company whose
// next_depreciation_date is on or before `asOf` and posts each one's due
// rows via PostDepreciation. Per-asset errors are collected rather than
// failing the whole batch.
//
// This is the manual month-end button. Future: a scheduled worker wrapping
// the same method on the 1st of each month.
func (s *Service) RunDepreciation(ctx context.Context, companyID string, asOf time.Time) (*RunResult, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset: unauthenticated")
	}
	if companyID == "" {
		return nil, errors.New("asset: company_id required")
	}

	// Pick asset IDs in a single query, then drive PostDepreciation per
	// asset so each gets its own transaction (one failure ≠ whole abort).
	rows, err := s.db.Query(ctx, `
		SELECT id, asset_name FROM asset
		WHERE company_id = $1
		  AND docstatus = 1
		  AND next_depreciation_date IS NOT NULL
		  AND next_depreciation_date <= $2
		ORDER BY next_depreciation_date, name`, companyID, asOf)
	if err != nil {
		return nil, err
	}
	type candidate struct{ id, name string }
	var todo []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.name); err != nil {
			rows.Close()
			return nil, err
		}
		todo = append(todo, c)
	}
	rows.Close()

	out := &RunResult{
		AsOf: asOf, CompanyID: companyID,
		AssetsPosted: []RunPostedRow{}, AssetsFailed: []RunFailedRow{},
	}
	totalAmt := decimal.Zero

	for _, c := range todo {
		// Snapshot acc dep + remaining-row count before posting so we can
		// report the delta cleanly.
		var beforeAcc decimal.Decimal
		var beforeRows int
		if err := s.db.QueryRow(ctx,
			`SELECT accumulated_depreciation FROM asset WHERE id = $1`, c.id).Scan(&beforeAcc); err != nil {
			out.AssetsFailed = append(out.AssetsFailed, RunFailedRow{AssetID: c.id, AssetName: c.name, Error: err.Error()})
			continue
		}
		if err := s.db.QueryRow(ctx, `
			SELECT count(*) FROM depreciation_schedule
			WHERE asset_id = $1 AND is_posted = false AND schedule_date <= $2`,
			c.id, asOf).Scan(&beforeRows); err != nil {
			out.AssetsFailed = append(out.AssetsFailed, RunFailedRow{AssetID: c.id, AssetName: c.name, Error: err.Error()})
			continue
		}
		if beforeRows == 0 {
			// next_depreciation_date may have been racy; skip silently.
			continue
		}

		a, err := s.PostDepreciation(ctx, c.id, asOf)
		if err != nil {
			out.AssetsFailed = append(out.AssetsFailed, RunFailedRow{AssetID: c.id, AssetName: c.name, Error: err.Error()})
			continue
		}
		amount := a.AccumulatedDepreciation.Sub(beforeAcc)
		totalAmt = totalAmt.Add(amount)
		out.AssetsPosted = append(out.AssetsPosted, RunPostedRow{
			AssetID: a.ID, AssetName: a.AssetName,
			Amount: amount.String(), Rows: beforeRows,
		})
		out.Processed++
	}
	out.TotalAmount = totalAmt.String()
	return out, nil
}

func (s *Service) Get(ctx context.Context, id string) (*Asset, error) {
	var out *Asset
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		a, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]Asset, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM asset WHERE company_id = $1 ORDER BY created_at DESC LIMIT 200`, companyID)
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
	out := make([]Asset, 0, len(ids))
	for _, id := range ids {
		a, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, nil
}

func load(ctx context.Context, tx pgx.Tx, id string) (*Asset, error) {
	var (
		a               Asset
		assetCategoryID *string
		costCenterID    *string
		nextDate        *time.Time
		rateOpt         *decimal.Decimal
	)
	var (
		curCustodian, curLocation, curLocationID *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, asset_name, asset_category_id, purchase_date,
		       gross_purchase_amount, expected_value_after_useful_life, useful_life_months, depreciation_method,
		       depreciation_rate_pct, pro_rata_basis,
		       asset_account_id, accumulated_depreciation_account_id, depreciation_expense_account_id, cost_center_id,
		       accumulated_depreciation, status, next_depreciation_date,
		       current_custodian, current_location, current_location_id,
		       docstatus, created_at, updated_at
		FROM asset WHERE id = $1`, id).
		Scan(&a.ID, &a.Name, &a.CompanyID, &a.AssetName, &assetCategoryID, &a.PurchaseDate,
			&a.GrossPurchaseAmount, &a.ExpectedValueAfterUsefulLife, &a.UsefulLifeMonths, &a.DepreciationMethod,
			&rateOpt, &a.ProRataBasis,
			&a.AssetAccountID, &a.AccumulatedDepreciationAccountID, &a.DepreciationExpenseAccountID, &costCenterID,
			&a.AccumulatedDepreciation, &a.Status, &nextDate,
			&curCustodian, &curLocation, &curLocationID,
			&a.Docstatus, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("asset %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if assetCategoryID != nil {
		a.AssetCategoryID = *assetCategoryID
	}
	if costCenterID != nil {
		a.CostCenterID = *costCenterID
	}
	if rateOpt != nil {
		a.DepreciationRatePct = *rateOpt
	}
	if curCustodian != nil {
		a.CurrentCustodian = *curCustodian
	}
	if curLocation != nil {
		a.CurrentLocation = *curLocation
	}
	if curLocationID != nil {
		a.CurrentLocationID = *curLocationID
	}
	a.NextDepreciationDate = nextDate

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, schedule_date, depreciation_amount, accumulated_after, is_posted, coalesce(posted_voucher_id,''), posted_at
		FROM depreciation_schedule WHERE asset_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r DepreciationRow
		if err := rows.Scan(&r.ID, &r.RowIndex, &r.ScheduleDate, &r.DepreciationAmount, &r.AccumulatedAfter,
			&r.IsPosted, &r.PostedVoucherID, &r.PostedAt); err != nil {
			return nil, err
		}
		a.Schedule = append(a.Schedule, r)
	}
	return &a, rows.Err()
}

func pickSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `
		SELECT id, pattern FROM naming_series
		WHERE doctype = $1 AND is_default = true AND (company_id = $2 OR company_id IS NULL)
		ORDER BY company_id NULLS LAST LIMIT 1`, doctype, companyID).Scan(&id, &pat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no series for %s", doctype)
	}
	return id, pat, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-assets", Method: http.MethodGet,
		Path: "/assets/assets", Summary: "List assets",
		Tags: []string{"Assets / Asset"},
	}, func(ctx context.Context, _ *struct{}) (*assetListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		as, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &assetListOut{Body: assetListBody{Items: as}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-asset", Method: http.MethodPost,
		Path: "/assets/assets", Summary: "Create an Asset",
		Tags: []string{"Assets / Asset"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *assetCreateIn) (*assetOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		a, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &assetOut{Body: *a}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-asset", Method: http.MethodPut,
		Path: "/assets/assets/{id}", Summary: "Update an Asset draft",
		Tags: []string{"Assets / Asset"},
	}, func(ctx context.Context, in *assetUpdateIn) (*assetOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		a, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &assetOut{Body: *a}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-asset", Method: http.MethodPost,
		Path: "/assets/assets/{id}/submit", Summary: "Submit an Asset (generates depreciation schedule)",
		Tags: []string{"Assets / Asset"},
	}, func(ctx context.Context, in *assetGetIn) (*assetOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		a, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &assetOut{Body: *a}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "post-asset-depreciation", Method: http.MethodPost,
		Path: "/assets/assets/{id}/post-depreciation", Summary: "Post depreciation up to as_of (today by default)",
		Tags: []string{"Assets / Asset"},
	}, func(ctx context.Context, in *depPostIn) (*assetOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
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
		a, err := h.Service.PostDepreciation(ctx, in.ID, asOf)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &assetOut{Body: *a}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-asset", Method: http.MethodGet,
		Path: "/assets/assets/{id}", Summary: "Get an Asset", Tags: []string{"Assets / Asset"},
	}, func(ctx context.Context, in *assetGetIn) (*assetOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		a, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &assetOut{Body: *a}, nil
	})

	// Dispose: sale or scrap. Posts a single GL voucher and flips the
	// asset to a terminal status (Sold / Scrapped). Idempotent at the
	// service layer via the status guard.
	huma.Register(api, huma.Operation{
		OperationID: "dispose-asset", Method: http.MethodPost,
		Path: "/assets/assets/{id}/dispose", Summary: "Sell or scrap an asset (posts disposal GL voucher)",
		Tags: []string{"Assets / Asset"},
	}, func(ctx context.Context, in *assetDisposeIn) (*assetOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		a, err := h.Service.Dispose(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &assetOut{Body: *a}, nil
	})

	// Batch depreciation run — wraps PostDepreciation across every eligible
	// asset in the active company. Per-asset failures are collected, not
	// raised, so the month-end button stays useful even if one asset is
	// misconfigured.
	huma.Register(api, huma.Operation{
		OperationID: "run-asset-depreciation", Method: http.MethodPost,
		Path: "/assets/depreciation-run", Summary: "Batch-post depreciation for every eligible asset",
		Tags: []string{"Assets / Asset"},
	}, func(ctx context.Context, in *depRunIn) (*depRunOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
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
		r, err := h.Service.RunDepreciation(ctx, co, asOf)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &depRunOut{Body: *r}, nil
	})
}

type (
	assetCreateIn struct{ Body AssetCreateInput }
	assetOut      struct{ Body Asset }
	assetGetIn    struct {
		ID string `path:"id"`
	}
	depPostIn struct {
		ID   string `path:"id"`
		AsOf string `query:"as_of"`
	}
	depRunIn struct {
		AsOf string `query:"as_of"  doc:"YYYY-MM-DD; defaults to today UTC"`
	}
	depRunOut       struct{ Body RunResult }
	assetDisposeIn struct {
		ID   string `path:"id"`
		Body DisposeInput
	}
	assetUpdateIn struct {
		ID   string `path:"id"`
		Body AssetUpdateInput
	}
	assetListOut  struct{ Body assetListBody }
	assetListBody struct {
		Items []Asset `json:"items"`
	}
)
