// Package assetvalueadjustment implements PSAK 16 revaluation + PSAK 36
// impairment via one doctype with three kinds:
//
//   revaluation       — value goes up; Dr Asset / Cr Revaluation Surplus
//   revaluation_down  — reverse a prior revaluation; Dr Surplus / Cr Asset
//   impairment        — value goes down with no surplus offset;
//                       Dr Impairment Loss / Cr Accumulated Depreciation
//
// Submit posts a single GL voucher and mutates asset.gross_purchase_amount
// (revaluation/_down) or asset.accumulated_depreciation (impairment). Cancel
// reverses both the GL and the asset balance.
package assetvalueadjustment

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
	Doctype     = "asset_value_adjustment"
	VoucherType = "Asset Value Adjustment"
)

const (
	KindRevaluation     = "revaluation"
	KindImpairment      = "impairment"
	KindRevaluationDown = "revaluation_down"
)

type ValueAdjustment struct {
	ID                          string             `json:"id"`
	Name                        string             `json:"name"`
	CompanyID                   string             `json:"company_id"`
	AssetID                     string             `json:"asset_id"`
	AdjustmentDate              time.Time          `json:"adjustment_date"`
	Kind                        string             `json:"kind"`
	Amount                      decimal.Decimal    `json:"amount"`
	Reason                      string             `json:"reason,omitempty"`
	RevaluationSurplusAccountID string             `json:"revaluation_surplus_account_id,omitempty"`
	ImpairmentLossAccountID     string             `json:"impairment_loss_account_id,omitempty"`
	PostedVoucherID             string             `json:"posted_voucher_id,omitempty"`
	Docstatus                   submittable.Status `json:"docstatus"`
	SubmittedAt                 *time.Time         `json:"submitted_at,omitempty"`
	CancelledAt                 *time.Time         `json:"cancelled_at,omitempty"`
	CreatedAt                   time.Time          `json:"created_at"`
	UpdatedAt                   time.Time          `json:"updated_at"`
}

type AssetValueAdjustmentInput struct {
	CompanyID                   string `json:"company_id,omitempty"`
	AssetID                     string `json:"asset_id"`
	AdjustmentDate              string `json:"adjustment_date"`
	Kind                        string `json:"kind" doc:"revaluation | impairment | revaluation_down"`
	Amount                      string `json:"amount" doc:"positive decimal"`
	Reason                      string `json:"reason,omitempty"`
	RevaluationSurplusAccountID string `json:"revaluation_surplus_account_id,omitempty"`
	ImpairmentLossAccountID     string `json:"impairment_loss_account_id,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in AssetValueAdjustmentInput) (*ValueAdjustment, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_value_adjustment: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("asset_value_adjustment.company_id: required")
	}
	if in.AssetID == "" {
		return nil, errors.New("asset_value_adjustment.asset_id: required")
	}
	switch in.Kind {
	case KindRevaluation, KindImpairment, KindRevaluationDown:
	default:
		return nil, fmt.Errorf("asset_value_adjustment.kind: must be revaluation | impairment | revaluation_down (got %q)", in.Kind)
	}
	amt, err := decimal.NewFromString(strings.TrimSpace(in.Amount))
	if err != nil || !amt.IsPositive() {
		return nil, errors.New("asset_value_adjustment.amount: must be a positive decimal")
	}
	ad, err := time.Parse("2006-01-02", in.AdjustmentDate)
	if err != nil {
		return nil, fmt.Errorf("asset_value_adjustment.adjustment_date: %w", err)
	}

	id := dbx.NewIDWithPrefix("ava")
	var out ValueAdjustment
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var assetCo, assetStatus string
		var assetDocstatus int16
		if err := tx.QueryRow(ctx,
			`SELECT company_id, status, docstatus FROM asset WHERE id = $1`, in.AssetID).
			Scan(&assetCo, &assetStatus, &assetDocstatus); err != nil {
			return fmt.Errorf("asset %s: %w", in.AssetID, err)
		}
		if assetCo != in.CompanyID {
			return errors.New("asset_value_adjustment: asset's company must match")
		}
		if assetDocstatus != 1 {
			return errors.New("asset_value_adjustment: asset must be submitted")
		}
		switch assetStatus {
		case "Sold", "Scrapped", "Cancelled":
			return fmt.Errorf("asset_value_adjustment: cannot adjust a %s asset", assetStatus)
		}

		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, ad, nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset_value_adjustment (
				id, name, company_id, asset_id, adjustment_date, kind, amount, reason,
				revaluation_surplus_account_id, impairment_loss_account_id,
				created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
			id, name, in.CompanyID, in.AssetID, ad, in.Kind, amt, in.Reason,
			nullable(in.RevaluationSurplusAccountID), nullable(in.ImpairmentLossAccountID),
			p.UserID); err != nil {
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

// ---- Submit ----

func (s *Service) Submit(ctx context.Context, id string) (*ValueAdjustment, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_value_adjustment: unauthenticated")
	}
	var out ValueAdjustment
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		ava, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if ava.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}

		// Load the asset's accounts + balances.
		var assetAcc, accDepAcc string
		var gross, accDep decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT asset_account_id, accumulated_depreciation_account_id,
			       gross_purchase_amount, accumulated_depreciation
			FROM asset WHERE id = $1`, ava.AssetID).
			Scan(&assetAcc, &accDepAcc, &gross, &accDep); err != nil {
			return err
		}

		// Resolve gain/loss account from per-adjustment override → company default.
		surplus := ava.RevaluationSurplusAccountID
		loss := ava.ImpairmentLossAccountID
		if surplus == "" {
			_ = tx.QueryRow(ctx,
				`SELECT revaluation_surplus_account_id FROM company WHERE id = $1`, ava.CompanyID).Scan(&surplus)
		}
		if loss == "" {
			_ = tx.QueryRow(ctx,
				`SELECT impairment_loss_account_id FROM company WHERE id = $1`, ava.CompanyID).Scan(&loss)
		}

		// Pre-check + build entries by kind.
		entries := []ledger.Entry{}
		acctCurrency := func(id string) (string, error) {
			var c string
			err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, id).Scan(&c)
			return c, err
		}

		switch ava.Kind {
		case KindRevaluation:
			if surplus == "" {
				return errors.New("revaluation: revaluation_surplus_account_id is required (set per-adjustment or on the company)")
			}
			assetCur, err := acctCurrency(assetAcc)
			if err != nil {
				return err
			}
			surplusCur, err := acctCurrency(surplus)
			if err != nil {
				return err
			}
			entries = append(entries,
				ledger.Entry{AccountID: assetAcc, Debit: ava.Amount,
					AccountCurrency: assetCur, DebitInAccountCurrency: ava.Amount,
					Remarks: fmt.Sprintf("Revaluation %s", ava.Name)},
				ledger.Entry{AccountID: surplus, Credit: ava.Amount,
					AccountCurrency: surplusCur, CreditInAccountCurrency: ava.Amount,
					Remarks: fmt.Sprintf("Revaluation %s", ava.Name)},
			)

		case KindRevaluationDown:
			if surplus == "" {
				return errors.New("revaluation_down: revaluation_surplus_account_id is required")
			}
			if ava.Amount.GreaterThan(gross) {
				return errors.New("revaluation_down: amount cannot exceed asset gross")
			}
			assetCur, err := acctCurrency(assetAcc)
			if err != nil {
				return err
			}
			surplusCur, err := acctCurrency(surplus)
			if err != nil {
				return err
			}
			entries = append(entries,
				ledger.Entry{AccountID: surplus, Debit: ava.Amount,
					AccountCurrency: surplusCur, DebitInAccountCurrency: ava.Amount,
					Remarks: fmt.Sprintf("Revaluation down %s", ava.Name)},
				ledger.Entry{AccountID: assetAcc, Credit: ava.Amount,
					AccountCurrency: assetCur, CreditInAccountCurrency: ava.Amount,
					Remarks: fmt.Sprintf("Revaluation down %s", ava.Name)},
			)

		case KindImpairment:
			if loss == "" {
				return errors.New("impairment: impairment_loss_account_id is required (set per-adjustment or on the company)")
			}
			// Impairment can't push NBV below salvage; cap defensively.
			nbv := gross.Sub(accDep)
			if ava.Amount.GreaterThan(nbv) {
				return fmt.Errorf("impairment: amount %s cannot exceed NBV %s", ava.Amount, nbv)
			}
			lossCur, err := acctCurrency(loss)
			if err != nil {
				return err
			}
			accDepCur, err := acctCurrency(accDepAcc)
			if err != nil {
				return err
			}
			entries = append(entries,
				ledger.Entry{AccountID: loss, Debit: ava.Amount,
					AccountCurrency: lossCur, DebitInAccountCurrency: ava.Amount,
					Remarks: fmt.Sprintf("Impairment %s", ava.Name)},
				ledger.Entry{AccountID: accDepAcc, Credit: ava.Amount,
					AccountCurrency: accDepCur, CreditInAccountCurrency: ava.Amount,
					Remarks: fmt.Sprintf("Impairment %s", ava.Name)},
			)
		}

		// Fiscal year for the GL voucher.
		var fyID string
		if err := tx.QueryRow(ctx, `
			SELECT fy.id FROM fiscal_year fy
			JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
			WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
			ORDER BY fy.start_date DESC LIMIT 1`, ava.CompanyID, ava.AdjustmentDate).Scan(&fyID); err != nil {
			return fmt.Errorf("fiscal year: %w", err)
		}

		voucherID := dbx.NewIDWithPrefix("avav")
		v := ledger.Voucher{
			Type: VoucherType, ID: voucherID, Name: ava.Name,
			CompanyID: ava.CompanyID, PostingDate: ava.AdjustmentDate, FiscalYearID: fyID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}

		// Mutate asset.
		switch ava.Kind {
		case KindRevaluation:
			if _, err := tx.Exec(ctx,
				`UPDATE asset SET gross_purchase_amount = gross_purchase_amount + $1, updated_by = $2 WHERE id = $3`,
				ava.Amount, p.UserID, ava.AssetID); err != nil {
				return err
			}
		case KindRevaluationDown:
			if _, err := tx.Exec(ctx,
				`UPDATE asset SET gross_purchase_amount = gross_purchase_amount - $1, updated_by = $2 WHERE id = $3`,
				ava.Amount, p.UserID, ava.AssetID); err != nil {
				return err
			}
		case KindImpairment:
			if _, err := tx.Exec(ctx,
				`UPDATE asset SET accumulated_depreciation = accumulated_depreciation + $1, updated_by = $2 WHERE id = $3`,
				ava.Amount, p.UserID, ava.AssetID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE asset_value_adjustment
			SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			    posted_voucher_id = $2, updated_by = $1
			WHERE id = $3`, p.UserID, voucherID, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionSubmit,
			audit.Diff{After: map[string]any{"voucher": voucherID, "kind": ava.Kind, "amount": ava.Amount.String()}}); err != nil {
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

// ---- Cancel ----

func (s *Service) Cancel(ctx context.Context, id string) (*ValueAdjustment, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("asset_value_adjustment: unauthenticated")
	}
	var out ValueAdjustment
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		ava, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if ava.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if _, err := ledger.CancelGL(ctx, tx, VoucherType, id, p.UserID); err != nil {
			return err
		}
		// Reverse asset mutation.
		switch ava.Kind {
		case KindRevaluation:
			if _, err := tx.Exec(ctx,
				`UPDATE asset SET gross_purchase_amount = gross_purchase_amount - $1 WHERE id = $2`,
				ava.Amount, ava.AssetID); err != nil {
				return err
			}
		case KindRevaluationDown:
			if _, err := tx.Exec(ctx,
				`UPDATE asset SET gross_purchase_amount = gross_purchase_amount + $1 WHERE id = $2`,
				ava.Amount, ava.AssetID); err != nil {
				return err
			}
		case KindImpairment:
			if _, err := tx.Exec(ctx,
				`UPDATE asset SET accumulated_depreciation = accumulated_depreciation - $1 WHERE id = $2`,
				ava.Amount, ava.AssetID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE asset_value_adjustment
			SET docstatus = 2, cancelled_at = now(), cancelled_by = $1, updated_by = $1
			WHERE id = $2`, p.UserID, id); err != nil {
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

func (s *Service) Get(ctx context.Context, id string) (*ValueAdjustment, error) {
	var out *ValueAdjustment
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		v, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]ValueAdjustment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM asset_value_adjustment WHERE company_id = $1
		ORDER BY adjustment_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]ValueAdjustment, 0, len(ids))
	for _, id := range ids {
		v, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// ---- helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*ValueAdjustment, error) {
	var (
		v                                            ValueAdjustment
		submittedAt, cancelledAt                     *time.Time
		surplus, loss, voucher, reason               *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, asset_id, adjustment_date, kind, amount, reason,
		       revaluation_surplus_account_id, impairment_loss_account_id,
		       posted_voucher_id, docstatus, submitted_at, cancelled_at, created_at, updated_at
		FROM asset_value_adjustment WHERE id = $1`, id).
		Scan(&v.ID, &v.Name, &v.CompanyID, &v.AssetID, &v.AdjustmentDate, &v.Kind, &v.Amount, &reason,
			&surplus, &loss, &voucher, &v.Docstatus, &submittedAt, &cancelledAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("asset_value_adjustment %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if reason != nil {
		v.Reason = *reason
	}
	if surplus != nil {
		v.RevaluationSurplusAccountID = *surplus
	}
	if loss != nil {
		v.ImpairmentLossAccountID = *loss
	}
	if voucher != nil {
		v.PostedVoucherID = *voucher
	}
	v.SubmittedAt = submittedAt
	v.CancelledAt = cancelledAt
	return &v, nil
}

func pickSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
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

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-asset-value-adjustments", Method: http.MethodGet,
		Path: "/assets/asset-value-adjustments", Summary: "List asset value adjustments",
		Tags: []string{"Assets / Value Adjustment"},
	}, func(ctx context.Context, _ *struct{}) (*avaListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		items, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &avaListOut{Body: avaListBody{Items: items}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-asset-value-adjustment", Method: http.MethodPost,
		Path: "/assets/asset-value-adjustments", Summary: "Create an asset value adjustment draft",
		Tags: []string{"Assets / Value Adjustment"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *avaCreateIn) (*avaOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &avaOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-asset-value-adjustment", Method: http.MethodGet,
		Path: "/assets/asset-value-adjustments/{id}", Summary: "Get an asset value adjustment",
		Tags: []string{"Assets / Value Adjustment"},
	}, func(ctx context.Context, in *avaGetIn) (*avaOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &avaOut{Body: *v}, nil
	})
	for _, op := range []struct {
		id, path, summary string
		fn                func(context.Context, string) (*ValueAdjustment, error)
		action            permission.Action
	}{
		{"submit-asset-value-adjustment", "submit", "Submit a value adjustment (posts GL voucher)", h.Service.Submit, permission.ActionSubmit},
		{"cancel-asset-value-adjustment", "cancel", "Cancel a value adjustment (reverses GL + asset balance)", h.Service.Cancel, permission.ActionCancel},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost,
			Path: "/assets/asset-value-adjustments/{id}/" + op.path, Summary: op.summary,
			Tags: []string{"Assets / Value Adjustment"},
		}, func(ctx context.Context, in *avaGetIn) (*avaOut, error) {
			if err := h.Perm.Check(ctx, Doctype, op.action); err != nil {
				return nil, httpx.MapError(err)
			}
			v, err := op.fn(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &avaOut{Body: *v}, nil
		})
	}
}

type (
	avaCreateIn struct{ Body AssetValueAdjustmentInput }
	avaOut      struct{ Body ValueAdjustment }
	avaListOut  struct{ Body avaListBody }
	avaListBody struct {
		Items []ValueAdjustment `json:"items"`
	}
	avaGetIn struct {
		ID string `path:"id"`
	}
)
