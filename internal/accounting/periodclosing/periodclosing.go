// Package periodclosing implements the Period Closing Voucher.
//
// On submit, sums the net activity per income+expense account over the fiscal
// year up to posting_date and posts an offsetting JE that zeroes them out, with
// the net (period_net_profit) flowing into the configured retained-earnings (closing) account.
//
// Cancel reverses via the standard ledger.CancelGL path.
package periodclosing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	Doctype     = "period_closing_voucher"
	VoucherType = "Period Closing Voucher"
)

type PeriodClosingVoucher struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	CompanyID        string             `json:"company_id"`
	FiscalYearID     string             `json:"fiscal_year_id"`
	PostingDate      time.Time          `json:"posting_date"`
	ClosingAccountID string             `json:"closing_account_id"`
	Remarks          string             `json:"remarks,omitempty"`
	Docstatus        submittable.Status `json:"docstatus"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

type PCVCreateInput struct {
	CompanyID        string `json:"company_id,omitempty"`
	FiscalYearID     string `json:"fiscal_year_id"`
	PostingDate      string `json:"posting_date"`
	ClosingAccountID string `json:"closing_account_id"`
	Remarks          string `json:"remarks,omitempty"`
}

type Service struct {
	db        *dbx.DB
	Approvals approvalChecker
	Workflow  workflowGate
	// Notifier is optional. Submit() fires period_closing.submitted after commit.
	Notifier notifier
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

type workflowGate interface {
	CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error
}

type notifier interface {
	Fire(eventKey string, payload map[string]any)
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) CreateDraft(ctx context.Context, in PCVCreateInput) (*PeriodClosingVoucher, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("period_closing_voucher: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" || in.FiscalYearID == "" || in.ClosingAccountID == "" {
		return nil, errors.New("period_closing_voucher: company_id, fiscal_year_id, closing_account_id are required")
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("posting_date: %w", err)
	}

	id := dbx.NewIDWithPrefix("pcv")
	var out PeriodClosingVoucher
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickNamingSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, pd, nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO period_closing_voucher (id, name, company_id, fiscal_year_id, posting_date,
			                                   closing_account_id, remarks, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
			id, name, in.CompanyID, in.FiscalYearID, pd, in.ClosingAccountID, nullable(in.Remarks), p.UserID); err != nil {
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

func (s *Service) Submit(ctx context.Context, id string) (*PeriodClosingVoucher, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("period_closing_voucher: unauthenticated")
	}
	var out PeriodClosingVoucher
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pcv, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pcv.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, "period_closing_voucher"); err != nil {
				return err
			}
		}
		if s.Approvals != nil {
			if err := s.Approvals.CheckSubmit(ctx, tx, "period_closing_voucher", pcv.ID, pcv.Name, pcv.CompanyID,
				map[string]any{"posting_date": pcv.PostingDate.Format("2006-01-02")}); err != nil {
				return err
			}
		}

		// Fiscal year window.
		var fyStart time.Time
		if err := tx.QueryRow(ctx,
			`SELECT start_date FROM fiscal_year WHERE id = $1`, pcv.FiscalYearID).Scan(&fyStart); err != nil {
			return fmt.Errorf("fiscal_year: %w", err)
		}

		// Net activity per income+expense account in the period.
		// For each non-zero account, post the inverse to flatten it.
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.root_type, a.account_currency,
			       coalesce(sum(g.debit), 0)  AS dr,
			       coalesce(sum(g.credit), 0) AS cr
			FROM account a
			LEFT JOIN gl_entry g
			  ON g.account_id = a.id
			 AND g.company_id = $1
			 AND g.posting_date BETWEEN $2 AND $3
			WHERE a.company_id = $1 AND a.is_group = false AND a.is_deleted = false
			  AND a.root_type IN ('income','expense')
			GROUP BY a.id, a.root_type, a.account_currency
			HAVING coalesce(sum(g.debit), 0) <> coalesce(sum(g.credit), 0)`,
			pcv.CompanyID, fyStart, pcv.PostingDate)
		if err != nil {
			return err
		}
		defer rows.Close()

		entries := []ledger.Entry{}
		netToClosing := decimal.Zero // positive = profit (Cr Retained Earnings); negative = loss (Dr RE)
		for rows.Next() {
			var (
				accID, rootType, accCur string
				dr, cr                  decimal.Decimal
			)
			if err := rows.Scan(&accID, &rootType, &accCur, &dr, &cr); err != nil {
				return err
			}
			net := dr.Sub(cr) // for expense > 0; for income < 0
			// To zero out an expense (Dr-positive) we post Cr. To zero income (Cr-positive) we post Dr.
			if net.IsPositive() {
				entries = append(entries, ledger.Entry{
					AccountID:               accID,
					Credit:                  net,
					AccountCurrency:         accCur,
					CreditInAccountCurrency: net,
					Against:                 pcv.ClosingAccountID,
					Remarks:                 pcv.Name + " — close expense",
				})
				// Net profit perspective: an expense net of (dr-cr) > 0 means a loss contributor → +to closing as Dr.
				netToClosing = netToClosing.Add(net)
			} else if net.IsNegative() {
				abs := net.Neg()
				entries = append(entries, ledger.Entry{
					AccountID:              accID,
					Debit:                  abs,
					AccountCurrency:        accCur,
					DebitInAccountCurrency: abs,
					Against:                pcv.ClosingAccountID,
					Remarks:                pcv.Name + " — close income",
				})
				netToClosing = netToClosing.Sub(abs)
			}
			_ = rootType // currently unused but kept for future per-type handling
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if len(entries) == 0 {
			return errors.New("period_closing_voucher: no income/expense activity to close")
		}

		// Counter-leg to retained earnings.
		var closingCurrency string
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, pcv.ClosingAccountID).Scan(&closingCurrency); err != nil {
			return fmt.Errorf("closing_account: %w", err)
		}
		// netToClosing:
		//   > 0  → loss → Dr RE
		//   < 0  → profit → Cr RE
		if netToClosing.IsPositive() {
			entries = append(entries, ledger.Entry{
				AccountID:              pcv.ClosingAccountID,
				Debit:                  netToClosing,
				AccountCurrency:        closingCurrency,
				DebitInAccountCurrency: netToClosing,
				Remarks:                pcv.Name + " — retained earnings",
			})
		} else {
			abs := netToClosing.Neg()
			entries = append(entries, ledger.Entry{
				AccountID:               pcv.ClosingAccountID,
				Credit:                  abs,
				AccountCurrency:         closingCurrency,
				CreditInAccountCurrency: abs,
				Remarks:                 pcv.Name + " — retained earnings",
			})
		}

		v := ledger.Voucher{
			Type: VoucherType, ID: pcv.ID, Name: pcv.Name,
			CompanyID: pcv.CompanyID, PostingDate: pcv.PostingDate, FiscalYearID: pcv.FiscalYearID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE period_closing_voucher SET docstatus = 1, submitted_at = now(), submitted_by = $1, updated_by = $1 WHERE id = $2`,
			p.UserID, id); err != nil {
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
	if err == nil && s.Notifier != nil {
		s.Notifier.Fire("period_closing.submitted", map[string]any{
			"company_id":            out.CompanyID,
			"doctype":               Doctype,
			"document_id":           out.ID,
			"document_name":         out.Name,
			"fiscal_year_id":        out.FiscalYearID,
			"summary":               fmt.Sprintf("Period closing %s submitted", out.Name),
			"PeriodClosingVoucher": out,
		})
	}
	return &out, err
}

func (s *Service) Cancel(ctx context.Context, id string) (*PeriodClosingVoucher, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("period_closing_voucher: unauthenticated")
	}
	var out PeriodClosingVoucher
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pcv, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if pcv.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		if _, err := ledger.CancelGL(ctx, tx, VoucherType, id, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE period_closing_voucher SET docstatus = 2, cancelled_at = now(), cancelled_by = $1, updated_by = $1 WHERE id = $2`,
			p.UserID, id); err != nil {
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

// List returns all PCVs for the given company, newest first.
func (s *Service) List(ctx context.Context, companyID string) ([]PeriodClosingVoucher, error) {
	if companyID == "" {
		return nil, errors.New("period_closing_voucher: company_id required")
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, name, company_id, fiscal_year_id, posting_date, closing_account_id,
		       coalesce(remarks,''), docstatus, created_at, updated_at
		FROM period_closing_voucher
		WHERE company_id = $1
		ORDER BY posting_date DESC, created_at DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PeriodClosingVoucher, 0)
	for rows.Next() {
		var v PeriodClosingVoucher
		if err := rows.Scan(&v.ID, &v.Name, &v.CompanyID, &v.FiscalYearID, &v.PostingDate, &v.ClosingAccountID,
			&v.Remarks, &v.Docstatus, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (*PeriodClosingVoucher, error) {
	var out *PeriodClosingVoucher
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

func load(ctx context.Context, tx pgx.Tx, id string) (*PeriodClosingVoucher, error) {
	var (
		v       PeriodClosingVoucher
		remarks *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, fiscal_year_id, posting_date, closing_account_id, remarks, docstatus, created_at, updated_at
		FROM period_closing_voucher WHERE id = $1`, id).
		Scan(&v.ID, &v.Name, &v.CompanyID, &v.FiscalYearID, &v.PostingDate, &v.ClosingAccountID, &remarks, &v.Docstatus, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("period_closing_voucher %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if remarks != nil {
		v.Remarks = *remarks
	}
	return &v, nil
}

func pickNamingSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
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
		OperationID: "list-period-closing-vouchers",
		Method:      http.MethodGet,
		Path:        "/accounting/period-closing-vouchers",
		Summary:     "List Period Closing Vouchers for the active company",
		Tags:        []string{"Accounting / Period Closing"},
	}, func(ctx context.Context, _ *struct{}) (*pcvListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		vs, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &pcvListOut{Body: pcvListBody{Items: vs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-period-closing-voucher",
		Method:        http.MethodPost,
		Path:          "/accounting/period-closing-vouchers",
		Summary:       "Create a Period Closing Voucher draft",
		Tags:          []string{"Accounting / Period Closing"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *pcvCreateIn) (*pcvOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &pcvOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-period-closing-voucher",
		Method:      http.MethodPost,
		Path:        "/accounting/period-closing-vouchers/{id}/submit",
		Summary:     "Submit a Period Closing Voucher (closes income/expense to retained earnings)",
		Tags:        []string{"Accounting / Period Closing"},
	}, func(ctx context.Context, in *pcvGetIn) (*pcvOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &pcvOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "cancel-period-closing-voucher",
		Method:      http.MethodPost,
		Path:        "/accounting/period-closing-vouchers/{id}/cancel",
		Summary:     "Cancel a Period Closing Voucher",
		Tags:        []string{"Accounting / Period Closing"},
	}, func(ctx context.Context, in *pcvGetIn) (*pcvOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCancel); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.Cancel(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &pcvOut{Body: *v}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-period-closing-voucher",
		Method:      http.MethodGet,
		Path:        "/accounting/period-closing-vouchers/{id}",
		Summary:     "Get a Period Closing Voucher",
		Tags:        []string{"Accounting / Period Closing"},
	}, func(ctx context.Context, in *pcvGetIn) (*pcvOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		v, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &pcvOut{Body: *v}, nil
	})
}

type (
	pcvCreateIn struct{ Body PCVCreateInput }
	pcvOut      struct{ Body PeriodClosingVoucher }
	pcvGetIn    struct {
		ID string `path:"id"`
	}
	pcvListOut  struct{ Body pcvListBody }
	pcvListBody struct {
		Items []PeriodClosingVoucher `json:"items"`
	}
)
