package asset

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/ledger"
)

// Disposal kind constants.
const (
	DisposalSale  = "sale"
	DisposalScrap = "scrap"
)

// AssetDraftFromPI carries the minimum fields needed to materialise an asset
// draft from a Purchase Invoice line. Mirrors the PI package's anonymous
// shape so both compile against the same struct via duck typing in
// cmd/api/main.go (adapter pattern).
type AssetDraftFromPI struct {
	CompanyID       string
	AssetName       string
	AssetCategoryID string
	PurchaseDate    string
	GrossAmount     string
	SourcePIID      string
	SourcePIItemRow int
}

// CreateDraftForPILine materialises one draft Asset from a PI fixed-asset
// line. Defaults flow from the supplied AssetCategory; the user can fine-
// tune later before submitting. Skips silently if no category is set —
// without a category, we don't have the three required GL accounts to
// populate, and a draft with NULL FK columns would fail the NOT NULL check.
//
// (CreateDraftForPILine is the implementation of the PI service's
//  AssetCreator interface — see internal/accounting/purchaseinvoice.)
func (s *Service) CreateDraftForPILine(ctx context.Context, in AssetDraftFromPI) error {
	if in.AssetCategoryID == "" {
		return fmt.Errorf("asset: cannot auto-create from PI %s line %d — item.asset_category_id is required",
			in.SourcePIID, in.SourcePIItemRow)
	}
	// Pull the category's defaults for the four config fields.
	var (
		method   string
		months   int
		assetAcc string
		accDepAcc string
		expAcc   string
	)
	err := s.db.QueryRow(ctx, `
		SELECT default_depreciation_method, total_useful_life_months,
		       coalesce(asset_account_id, ''),
		       coalesce(accumulated_depreciation_account_id, ''),
		       coalesce(depreciation_expense_account_id, '')
		FROM asset_category WHERE id = $1 AND is_deleted = false`,
		in.AssetCategoryID).Scan(&method, &months, &assetAcc, &accDepAcc, &expAcc)
	if err != nil {
		return fmt.Errorf("asset_category %s: %w", in.AssetCategoryID, err)
	}
	if assetAcc == "" || accDepAcc == "" || expAcc == "" {
		return fmt.Errorf("asset_category %s: all three GL accounts must be set before auto-create from PI", in.AssetCategoryID)
	}

	_, err = s.Create(ctx, AssetCreateInput{
		CompanyID:                        in.CompanyID,
		AssetName:                        in.AssetName,
		AssetCategoryID:                  in.AssetCategoryID,
		PurchaseDate:                     in.PurchaseDate,
		GrossPurchaseAmount:              in.GrossAmount,
		UsefulLifeMonths:                 months,
		DepreciationMethod:               method,
		AssetAccountID:                   assetAcc,
		AccumulatedDepreciationAccountID: accDepAcc,
		DepreciationExpenseAccountID:     expAcc,
	})
	return err
}


// DisposeInput is what /assets/assets/{id}/dispose accepts.
type DisposeInput struct {
	Kind                   string `json:"kind"  doc:"sale | scrap"`
	DisposalDate           string `json:"disposal_date" doc:"YYYY-MM-DD; defaults to today UTC"`
	Proceeds               string `json:"proceeds,omitempty" doc:"required for kind=sale; ignored for scrap"`
	DisposalCashAccountID  string `json:"disposal_cash_account_id,omitempty" doc:"required for kind=sale"`
	GainAccountID          string `json:"gain_account_id,omitempty" doc:"used when proceeds > NBV; defaults to company income.gain_on_disposal"`
	LossAccountID          string `json:"loss_account_id,omitempty" doc:"used when proceeds < NBV (and for scrap); defaults to company expense.loss_on_disposal"`
	Remarks                string `json:"remarks,omitempty"`
}

// Dispose books the sale/scrap and flips the asset to a terminal state. GL
// posting:
//
//   Dr Accumulated Depreciation Account     acc_dep
//   Cr Asset Account                        gross
//   [Dr Disposal Cash Account               proceeds]      (sale only)
//   [Cr Gain Account                        proceeds - NBV] (if positive)
//   [Dr Loss Account                        NBV - proceeds] (if negative, or full NBV for scrap)
//
// After posting:
//   * accumulated_depreciation column zeroed (it's been cleared from the GL)
//   * status = Sold or Scrapped
//   * disposed_at / disposed_by / disposal_voucher_id stamped
//   * any not-yet-posted depreciation_schedule rows are marked is_posted=true
//     so a future RunDepreciation skips this asset.
func (s *Service) Dispose(ctx context.Context, assetID string, in DisposeInput) (*Asset, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset: unauthenticated")
	}

	if in.Kind != DisposalSale && in.Kind != DisposalScrap {
		return nil, fmt.Errorf("disposal.kind: must be 'sale' or 'scrap' (got %q)", in.Kind)
	}
	disposalDate := time.Now().UTC().Truncate(24 * time.Hour)
	if in.DisposalDate != "" {
		t, err := time.Parse("2006-01-02", in.DisposalDate)
		if err != nil {
			return nil, fmt.Errorf("disposal_date: %w", err)
		}
		disposalDate = t
	}

	var proceeds decimal.Decimal
	if in.Kind == DisposalSale {
		if in.Proceeds == "" {
			return nil, errors.New("disposal.proceeds: required for kind=sale")
		}
		pr, err := decimal.NewFromString(in.Proceeds)
		if err != nil || pr.IsNegative() {
			return nil, errors.New("disposal.proceeds: must be a non-negative decimal")
		}
		proceeds = pr
		if in.DisposalCashAccountID == "" {
			return nil, errors.New("disposal.disposal_cash_account_id: required for kind=sale")
		}
	}

	var out Asset
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		a, err := load(ctx, tx, assetID)
		if err != nil {
			return err
		}
		if a.Docstatus != 1 {
			return errors.New("asset: must be submitted before disposal")
		}
		switch a.Status {
		case "Sold", "Scrapped", "Cancelled":
			return fmt.Errorf("asset: already %s — cannot dispose again", a.Status)
		}

		// NBV = gross - accumulated_depreciation. Negative NBV is impossible
		// in a clean ledger but defensively clamp anyway.
		nbv := a.GrossPurchaseAmount.Sub(a.AccumulatedDepreciation)
		if nbv.IsNegative() {
			nbv = decimal.Zero
		}
		gainLoss := proceeds.Sub(nbv) // > 0 = gain, < 0 = loss

		// Resolve gain/loss accounts. v1: per-asset override → company default.
		// If neither is set and we need one, surface a friendly error.
		gainAcct, lossAcct := in.GainAccountID, in.LossAccountID
		if gainAcct == "" {
			_ = tx.QueryRow(ctx, `SELECT gain_on_disposal_account_id FROM company WHERE id = $1`, a.CompanyID).Scan(&gainAcct)
		}
		if lossAcct == "" {
			_ = tx.QueryRow(ctx, `SELECT loss_on_disposal_account_id FROM company WHERE id = $1`, a.CompanyID).Scan(&lossAcct)
		}
		if gainLoss.IsPositive() && gainAcct == "" {
			return errors.New("disposal: gain_account_id is required (or set company.gain_on_disposal_account_id)")
		}
		if gainLoss.IsNegative() && lossAcct == "" {
			return errors.New("disposal: loss_account_id is required (or set company.loss_on_disposal_account_id)")
		}
		if in.Kind == DisposalScrap && lossAcct == "" {
			return errors.New("disposal: loss_account_id is required for scrap (full NBV is written off as loss)")
		}

		// Pick fiscal year for the disposal date.
		var fyID string
		if err := tx.QueryRow(ctx, `
			SELECT fy.id FROM fiscal_year fy
			JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
			WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
			ORDER BY fy.start_date DESC LIMIT 1`, a.CompanyID, disposalDate).Scan(&fyID); err != nil {
			return fmt.Errorf("fiscal year: %w", err)
		}

		// Build GL entries. Use currency from each leg's account.
		acctCurrency := func(id string) (string, error) {
			var c string
			err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, id).Scan(&c)
			return c, err
		}

		entries := []ledger.Entry{}

		// Clear gross + acc_dep.
		assetCur, err := acctCurrency(a.AssetAccountID)
		if err != nil {
			return err
		}
		entries = append(entries, ledger.Entry{
			AccountID: a.AssetAccountID, Credit: a.GrossPurchaseAmount,
			AccountCurrency: assetCur, CreditInAccountCurrency: a.GrossPurchaseAmount,
			Remarks: fmt.Sprintf("Disposal %s — clear gross", a.Name),
		})
		if a.AccumulatedDepreciation.IsPositive() {
			accCur, err := acctCurrency(a.AccumulatedDepreciationAccountID)
			if err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID: a.AccumulatedDepreciationAccountID, Debit: a.AccumulatedDepreciation,
				AccountCurrency: accCur, DebitInAccountCurrency: a.AccumulatedDepreciation,
				Remarks: fmt.Sprintf("Disposal %s — clear acc dep", a.Name),
			})
		}

		// Cash leg (sale only).
		if in.Kind == DisposalSale && proceeds.IsPositive() {
			cashCur, err := acctCurrency(in.DisposalCashAccountID)
			if err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID: in.DisposalCashAccountID, Debit: proceeds,
				AccountCurrency: cashCur, DebitInAccountCurrency: proceeds,
				Remarks: fmt.Sprintf("Disposal %s — proceeds", a.Name),
			})
		}

		// Gain or loss.
		switch {
		case gainLoss.IsPositive():
			cur, err := acctCurrency(gainAcct)
			if err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID: gainAcct, Credit: gainLoss,
				AccountCurrency: cur, CreditInAccountCurrency: gainLoss,
				Remarks: fmt.Sprintf("Disposal %s — gain", a.Name),
			})
		case gainLoss.IsNegative():
			lossAmt := gainLoss.Neg()
			cur, err := acctCurrency(lossAcct)
			if err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID: lossAcct, Debit: lossAmt,
				AccountCurrency: cur, DebitInAccountCurrency: lossAmt,
				Remarks: fmt.Sprintf("Disposal %s — loss", a.Name),
			})
		}

		// Post the voucher.
		voucherID := dbx.NewIDWithPrefix("disp")
		v := ledger.Voucher{
			Type: VoucherType, ID: voucherID, Name: a.Name + " / Disposal",
			CompanyID: a.CompanyID, PostingDate: disposalDate, FiscalYearID: fyID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}

		// Suppress remaining unposted schedule rows — they'd otherwise be
		// picked up by the next RunDepreciation. Use a sentinel posted_at
		// so reports can distinguish "posted via disposal" from real posts.
		if _, err := tx.Exec(ctx, `
			UPDATE depreciation_schedule
			SET is_posted = true, posted_voucher_id = $1, posted_at = now()
			WHERE asset_id = $2 AND is_posted = false`, voucherID, assetID); err != nil {
			return err
		}

		// Mutate asset.
		newStatus := "Sold"
		if in.Kind == DisposalScrap {
			newStatus = "Scrapped"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset SET
			  status = $1,
			  disposed_at = now(), disposed_by = $2,
			  disposal_kind = $3, disposal_proceeds = $4,
			  disposal_voucher_id = $5,
			  gain_account_id = $6, loss_account_id = $7,
			  disposal_cash_account_id = $8,
			  accumulated_depreciation = 0,
			  next_depreciation_date = NULL,
			  updated_by = $2
			WHERE id = $9`,
			newStatus, p.UserID, in.Kind, proceeds, voucherID,
			nullable(in.GainAccountID), nullable(in.LossAccountID),
			nullable(in.DisposalCashAccountID), assetID); err != nil {
			return err
		}

		if err := audit.Record(ctx, tx, Doctype, assetID, p.UserID, "dispose", audit.Diff{
			After: map[string]any{
				"kind": in.Kind, "proceeds": proceeds.String(),
				"gain_loss": gainLoss.String(), "voucher": voucherID,
				"remarks": in.Remarks,
			},
		}); err != nil {
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
