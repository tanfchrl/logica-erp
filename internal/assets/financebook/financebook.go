// Package financebook owns the finance_book master and per-asset book
// schedules. The primary book is the one Asset.PostDepreciation actually
// posts to GL — non-primary books (typically "Tax Book" per PMK-96/2018)
// are reporting-only in v1.
//
// CRUD on finance_book itself lives here. Per-asset book attachments
// (asset_finance_book + asset_finance_book_schedule) are created by the
// AttachBook method, typically right after the asset is submitted.
package financebook

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

	"github.com/tandigital/logica-erp/internal/assets/asset"
	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "finance_book"

type FinanceBook struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	Name      string    `json:"name"`
	IsPrimary bool      `json:"is_primary"`
	IsDeleted bool      `json:"is_deleted"`
	CreatedAt time.Time `json:"created_at"`
}

type FinanceBookInput struct {
	Name      string `json:"name"`
	IsPrimary bool   `json:"is_primary,omitempty"`
}

// AttachBookInput configures depreciation under a specific book for an
// existing submitted asset. The first-time attach generates the schedule.
type AttachBookInput struct {
	AssetID                      string `json:"asset_id"`
	FinanceBookID                string `json:"finance_book_id"`
	DepreciationMethod           string `json:"depreciation_method,omitempty"`
	DepreciationRatePct          string `json:"depreciation_rate_pct,omitempty"`
	UsefulLifeMonths             int    `json:"useful_life_months"`
	ProRataBasis                 *bool  `json:"pro_rata_basis,omitempty"`
	ExpectedValueAfterUsefulLife string `json:"expected_value_after_useful_life,omitempty"`
}

// AssetBookView is the read-side payload for the asset detail page: one
// row per attached book with its full schedule.
type AssetBookView struct {
	BookID              string             `json:"book_id"`
	BookName            string             `json:"book_name"`
	IsPrimary           bool               `json:"is_primary"`
	DepreciationMethod  string             `json:"depreciation_method"`
	DepreciationRatePct decimal.Decimal    `json:"depreciation_rate_pct"`
	UsefulLifeMonths    int                `json:"useful_life_months"`
	ProRataBasis        bool               `json:"pro_rata_basis"`
	Schedule            []AssetBookSchedRow `json:"schedule"`
}
type AssetBookSchedRow struct {
	RowIndex           int             `json:"row_index"`
	ScheduleDate       time.Time       `json:"schedule_date"`
	DepreciationAmount decimal.Decimal `json:"depreciation_amount"`
	AccumulatedAfter   decimal.Decimal `json:"accumulated_after"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- finance_book CRUD ----

func (s *Service) Create(ctx context.Context, companyID string, in FinanceBookInput) (*FinanceBook, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("finance_book: unauthenticated")
	}
	if companyID == "" {
		return nil, errors.New("finance_book.company_id: required")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("finance_book.name: required")
	}
	id := dbx.NewIDWithPrefix("fb")
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// If is_primary, clear any other primary first (only one per company).
		if in.IsPrimary {
			if _, err := tx.Exec(ctx,
				`UPDATE finance_book SET is_primary = false WHERE company_id = $1 AND is_primary = true`,
				companyID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO finance_book (id, company_id, name, is_primary)
			VALUES ($1, $2, $3, $4)`, id, companyID, in.Name, in.IsPrimary); err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*FinanceBook, error) {
	var b FinanceBook
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, name, is_primary, is_deleted, created_at
		FROM finance_book WHERE id = $1`, id).
		Scan(&b.ID, &b.CompanyID, &b.Name, &b.IsPrimary, &b.IsDeleted, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("finance_book %s not found", id)
	}
	return &b, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]FinanceBook, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, name, is_primary, is_deleted, created_at
		FROM finance_book WHERE company_id = $1 AND is_deleted = false
		ORDER BY is_primary DESC, name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FinanceBook
	for rows.Next() {
		var b FinanceBook
		if err := rows.Scan(&b.ID, &b.CompanyID, &b.Name, &b.IsPrimary, &b.IsDeleted, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("finance_book: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		var isPrimary bool
		if err := tx.QueryRow(ctx,
			`SELECT is_primary FROM finance_book WHERE id = $1`, id).Scan(&isPrimary); err != nil {
			return err
		}
		if isPrimary {
			return errors.New("finance_book: cannot delete the primary book")
		}
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM asset_finance_book WHERE finance_book_id = $1`, id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("finance_book: %d asset(s) reference this book — detach them first", count)
		}
		tag, err := tx.Exec(ctx,
			`UPDATE finance_book SET is_deleted = true WHERE id = $1 AND is_deleted = false`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("finance_book %s not found", id)
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

// ---- per-asset book attach ----

// AttachBook adds (or replaces) a per-(asset, book) depreciation schedule.
// Re-attaching wipes the old schedule and regenerates from scratch. Schedule
// generation reuses asset.BuildSchedule so the math stays identical to the
// primary-book pipeline.
func (s *Service) AttachBook(ctx context.Context, in AttachBookInput) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("finance_book: unauthenticated")
	}
	if in.AssetID == "" || in.FinanceBookID == "" {
		return errors.New("attach: asset_id + finance_book_id required")
	}
	if in.UsefulLifeMonths <= 0 {
		return errors.New("attach: useful_life_months must be > 0")
	}

	rate := decimal.Zero
	if in.DepreciationRatePct != "" {
		r, err := decimal.NewFromString(in.DepreciationRatePct)
		if err != nil || r.IsNegative() {
			return errors.New("attach: depreciation_rate_pct must be non-negative")
		}
		rate = r
	}
	salvage := decimal.Zero
	if in.ExpectedValueAfterUsefulLife != "" {
		sv, err := decimal.NewFromString(in.ExpectedValueAfterUsefulLife)
		if err != nil || sv.IsNegative() {
			return errors.New("attach: expected_value_after_useful_life must be non-negative")
		}
		salvage = sv
	}
	method := in.DepreciationMethod
	if method == "" {
		method = asset.MethodStraightLine
	}
	proRata := true
	if in.ProRataBasis != nil {
		proRata = *in.ProRataBasis
	}

	// Pull the asset's gross + purchase date — needed for BuildSchedule.
	var gross decimal.Decimal
	var purchaseDate time.Time
	var assetCo string
	var docstatus int16
	if err := s.db.QueryRow(ctx, `
		SELECT gross_purchase_amount, purchase_date, company_id, docstatus
		FROM asset WHERE id = $1`, in.AssetID).
		Scan(&gross, &purchaseDate, &assetCo, &docstatus); err != nil {
		return fmt.Errorf("asset %s: %w", in.AssetID, err)
	}
	if docstatus != 1 {
		return errors.New("attach: asset must be submitted")
	}
	if salvage.GreaterThanOrEqual(gross) {
		return errors.New("attach: expected_value_after_useful_life must be < gross")
	}

	// Book must belong to the same company.
	var bookCo string
	if err := s.db.QueryRow(ctx,
		`SELECT company_id FROM finance_book WHERE id = $1 AND is_deleted = false`,
		in.FinanceBookID).Scan(&bookCo); err != nil {
		return fmt.Errorf("finance_book %s: %w", in.FinanceBookID, err)
	}
	if bookCo != assetCo {
		return errors.New("attach: finance_book belongs to a different company than the asset")
	}

	rows, err := asset.BuildSchedule(asset.ScheduleParams{
		Gross: gross, Salvage: salvage, UsefulLifeMonths: in.UsefulLifeMonths,
		Method: method, PurchaseDate: purchaseDate, ProRataBasis: proRata,
		DepreciationRatePct: rate,
	})
	if err != nil {
		return err
	}

	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Upsert the attachment row. Re-attaching replaces the schedule
		// entirely, so we delete any prior rows by FK cascade.
		var afbID string
		err := tx.QueryRow(ctx, `
			SELECT id FROM asset_finance_book
			WHERE asset_id = $1 AND finance_book_id = $2`,
			in.AssetID, in.FinanceBookID).Scan(&afbID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			afbID = dbx.NewIDWithPrefix("afb")
			var rateArg any
			if rate.IsPositive() {
				rateArg = rate
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO asset_finance_book (
					id, asset_id, finance_book_id,
					depreciation_method, depreciation_rate_pct, useful_life_months,
					pro_rata_basis, expected_value_after_useful_life
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				afbID, in.AssetID, in.FinanceBookID,
				method, rateArg, in.UsefulLifeMonths, proRata, salvage); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			// Existing — wipe schedule + update config.
			if _, err := tx.Exec(ctx,
				`DELETE FROM asset_finance_book_schedule WHERE asset_finance_book_id = $1`, afbID); err != nil {
				return err
			}
			var rateArg any
			if rate.IsPositive() {
				rateArg = rate
			}
			if _, err := tx.Exec(ctx, `
				UPDATE asset_finance_book SET
				  depreciation_method = $1, depreciation_rate_pct = $2,
				  useful_life_months = $3, pro_rata_basis = $4,
				  expected_value_after_useful_life = $5
				WHERE id = $6`,
				method, rateArg, in.UsefulLifeMonths, proRata, salvage, afbID); err != nil {
				return err
			}
		}
		// Insert new schedule rows.
		for _, r := range rows {
			sid := dbx.NewIDWithPrefix("afbs")
			if _, err := tx.Exec(ctx, `
				INSERT INTO asset_finance_book_schedule
				  (id, asset_finance_book_id, row_index, schedule_date, depreciation_amount, accumulated_after)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				sid, afbID, r.RowIndex, r.ScheduleDate, r.DepreciationAmount, r.AccumulatedAfter); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, Doctype, afbID, p.UserID, "attach_book",
			audit.Diff{After: map[string]any{
				"asset_id": in.AssetID, "finance_book_id": in.FinanceBookID,
				"method": method, "rate_pct": rate.String(),
			}})
	})
}

// ListAssetBooks returns the rendered view for the asset detail page.
func (s *Service) ListAssetBooks(ctx context.Context, assetID string) ([]AssetBookView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT afb.id, fb.id, fb.name, fb.is_primary,
		       afb.depreciation_method, afb.depreciation_rate_pct,
		       afb.useful_life_months, afb.pro_rata_basis
		FROM asset_finance_book afb
		JOIN finance_book fb ON fb.id = afb.finance_book_id
		WHERE afb.asset_id = $1
		ORDER BY fb.is_primary DESC, fb.name`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type rec struct {
		afbID, bookID, bookName, method string
		isPrimary, proRata              bool
		rate                            *decimal.Decimal
		life                            int
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.afbID, &r.bookID, &r.bookName, &r.isPrimary,
			&r.method, &r.rate, &r.life, &r.proRata); err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}

	out := make([]AssetBookView, 0, len(recs))
	for _, r := range recs {
		sched, err := s.loadSchedule(ctx, r.afbID)
		if err != nil {
			return nil, err
		}
		rate := decimal.Zero
		if r.rate != nil {
			rate = *r.rate
		}
		out = append(out, AssetBookView{
			BookID: r.bookID, BookName: r.bookName, IsPrimary: r.isPrimary,
			DepreciationMethod: r.method, DepreciationRatePct: rate,
			UsefulLifeMonths: r.life, ProRataBasis: r.proRata, Schedule: sched,
		})
	}
	return out, nil
}

func (s *Service) loadSchedule(ctx context.Context, afbID string) ([]AssetBookSchedRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT row_index, schedule_date, depreciation_amount, accumulated_after
		FROM asset_finance_book_schedule WHERE asset_finance_book_id = $1
		ORDER BY row_index`, afbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetBookSchedRow
	for rows.Next() {
		var r AssetBookSchedRow
		if err := rows.Scan(&r.RowIndex, &r.ScheduleDate, &r.DepreciationAmount, &r.AccumulatedAfter); err != nil {
			return nil, err
		}
		out = append(out, r)
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
		OperationID: "list-finance-books", Method: http.MethodGet,
		Path: "/assets/finance-books", Summary: "List finance books for the active company",
		Tags: []string{"Assets / Finance Book"},
	}, func(ctx context.Context, _ *struct{}) (*fbListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		bs, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &fbListOut{Body: fbListBody{Items: bs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-finance-book", Method: http.MethodPost,
		Path: "/assets/finance-books", Summary: "Create a finance book",
		Tags: []string{"Assets / Finance Book"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *fbCreateIn) (*fbOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		b, err := h.Service.Create(ctx, co, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &fbOut{Body: *b}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-finance-book", Method: http.MethodDelete,
		Path: "/assets/finance-books/{id}", Summary: "Soft-delete a finance book",
		Tags: []string{"Assets / Finance Book"},
	}, func(ctx context.Context, in *fbGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "attach-asset-finance-book", Method: http.MethodPost,
		Path: "/assets/assets/{id}/finance-books", Summary: "Attach (or update) a finance-book schedule for an asset",
		Tags: []string{"Assets / Finance Book"},
	}, func(ctx context.Context, in *fbAttachIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		body := in.Body
		body.AssetID = in.ID
		if err := h.Service.AttachBook(ctx, body); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-asset-finance-books", Method: http.MethodGet,
		Path: "/assets/assets/{id}/finance-books", Summary: "List per-book depreciation schedules for an asset",
		Tags: []string{"Assets / Finance Book"},
	}, func(ctx context.Context, in *fbGetIn) (*fbViewOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.ListAssetBooks(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &fbViewOut{Body: fbViewBody{Items: v}}, nil
	})
}

type (
	fbCreateIn struct{ Body FinanceBookInput }
	fbOut      struct{ Body FinanceBook }
	fbGetIn    struct {
		ID string `path:"id"`
	}
	fbListOut  struct{ Body fbListBody }
	fbListBody struct {
		Items []FinanceBook `json:"items"`
	}
	fbAttachIn struct {
		ID   string `path:"id"`
		Body AttachBookInput
	}
	fbViewOut struct{ Body fbViewBody }
	fbViewBody struct {
		Items []AssetBookView `json:"items"`
	}
)
