// Package payrollconfig manages the payroll_setting per-company table: BPJS
// rates + PPh21 TER brackets. The Indonesian DJP updates TER rates annually;
// this package lets admins edit them without a migration.
package payrollconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "payroll_setting"

type PayrollSetting struct {
	ID                       string          `json:"id"`
	CompanyID                string          `json:"company_id"`
	EffectiveFrom            time.Time       `json:"effective_from"`
	BpjsKesehatanEmployer    decimal.Decimal `json:"bpjs_kesehatan_employer"`
	BpjsKesehatanEmployee    decimal.Decimal `json:"bpjs_kesehatan_employee"`
	BpjsKesehatanCap         decimal.Decimal `json:"bpjs_kesehatan_cap"`
	BpjsJhtEmployer          decimal.Decimal `json:"bpjs_jht_employer"`
	BpjsJhtEmployee          decimal.Decimal `json:"bpjs_jht_employee"`
	BpjsJpEmployer           decimal.Decimal `json:"bpjs_jp_employer"`
	BpjsJpEmployee           decimal.Decimal `json:"bpjs_jp_employee"`
	BpjsJpCap                decimal.Decimal `json:"bpjs_jp_cap"`
	BpjsJkkEmployer          decimal.Decimal `json:"bpjs_jkk_employer"`
	BpjsJkmEmployer          decimal.Decimal `json:"bpjs_jkm_employer"`
	Pph21TER                 json.RawMessage `json:"pph21_ter"`
	UpdatedAt                time.Time       `json:"updated_at"`
}

type PayrollInput struct {
	EffectiveFrom            string          `json:"effective_from" doc:"YYYY-MM-DD"`
	BpjsKesehatanEmployer    string          `json:"bpjs_kesehatan_employer,omitempty"`
	BpjsKesehatanEmployee    string          `json:"bpjs_kesehatan_employee,omitempty"`
	BpjsKesehatanCap         string          `json:"bpjs_kesehatan_cap,omitempty"`
	BpjsJhtEmployer          string          `json:"bpjs_jht_employer,omitempty"`
	BpjsJhtEmployee          string          `json:"bpjs_jht_employee,omitempty"`
	BpjsJpEmployer           string          `json:"bpjs_jp_employer,omitempty"`
	BpjsJpEmployee           string          `json:"bpjs_jp_employee,omitempty"`
	BpjsJpCap                string          `json:"bpjs_jp_cap,omitempty"`
	BpjsJkkEmployer          string          `json:"bpjs_jkk_employer,omitempty"`
	BpjsJkmEmployer          string          `json:"bpjs_jkm_employer,omitempty"`
	Pph21TER                 json.RawMessage `json:"pph21_ter,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// List returns all settings for the active company, newest effective_from first.
func (s *Service) List(ctx context.Context) ([]PayrollSetting, error) {
	co := auth.CompanyFromContext(ctx)
	if co == "" {
		return nil, errors.New("payroll_setting: X-Company-Id required")
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, effective_from,
		       bpjs_kesehatan_employer, bpjs_kesehatan_employee, bpjs_kesehatan_cap,
		       bpjs_jht_employer, bpjs_jht_employee,
		       bpjs_jp_employer, bpjs_jp_employee, bpjs_jp_cap,
		       bpjs_jkk_employer, bpjs_jkm_employer,
		       pph21_ter, updated_at
		FROM payroll_setting WHERE company_id = $1 ORDER BY effective_from DESC`, co)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PayrollSetting, 0)
	for rows.Next() {
		var st PayrollSetting
		if err := rows.Scan(&st.ID, &st.CompanyID, &st.EffectiveFrom,
			&st.BpjsKesehatanEmployer, &st.BpjsKesehatanEmployee, &st.BpjsKesehatanCap,
			&st.BpjsJhtEmployer, &st.BpjsJhtEmployee,
			&st.BpjsJpEmployer, &st.BpjsJpEmployee, &st.BpjsJpCap,
			&st.BpjsJkkEmployer, &st.BpjsJkmEmployer,
			&st.Pph21TER, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Upsert creates or updates a payroll_setting for the active company keyed by
// (company_id, effective_from).
func (s *Service) Upsert(ctx context.Context, id string, in PayrollInput) (*PayrollSetting, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("payroll_setting: unauthenticated")
	}
	co := auth.CompanyFromContext(ctx)
	if co == "" {
		return nil, errors.New("payroll_setting: X-Company-Id required")
	}
	if in.EffectiveFrom == "" {
		return nil, errors.New("effective_from required")
	}
	ef, err := time.Parse("2006-01-02", in.EffectiveFrom)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = dbx.NewIDWithPrefix("payset")
	}
	ter := in.Pph21TER
	if len(ter) == 0 {
		ter = json.RawMessage("[]")
	}

	// Defaults applied in Go so empty inputs don't break numeric coercion.
	def := func(v, d string) string { if v == "" { return d }; return v }
	_, err = s.db.Exec(ctx, `
		INSERT INTO payroll_setting (id, company_id, effective_from,
		  bpjs_kesehatan_employer, bpjs_kesehatan_employee, bpjs_kesehatan_cap,
		  bpjs_jht_employer, bpjs_jht_employee,
		  bpjs_jp_employer, bpjs_jp_employee, bpjs_jp_cap,
		  bpjs_jkk_employer, bpjs_jkm_employer, pph21_ter, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15,now())
		ON CONFLICT (company_id, effective_from) DO UPDATE SET
		  bpjs_kesehatan_employer = EXCLUDED.bpjs_kesehatan_employer,
		  bpjs_kesehatan_employee = EXCLUDED.bpjs_kesehatan_employee,
		  bpjs_kesehatan_cap = EXCLUDED.bpjs_kesehatan_cap,
		  bpjs_jht_employer = EXCLUDED.bpjs_jht_employer,
		  bpjs_jht_employee = EXCLUDED.bpjs_jht_employee,
		  bpjs_jp_employer = EXCLUDED.bpjs_jp_employer,
		  bpjs_jp_employee = EXCLUDED.bpjs_jp_employee,
		  bpjs_jp_cap = EXCLUDED.bpjs_jp_cap,
		  bpjs_jkk_employer = EXCLUDED.bpjs_jkk_employer,
		  bpjs_jkm_employer = EXCLUDED.bpjs_jkm_employer,
		  pph21_ter = EXCLUDED.pph21_ter,
		  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		id, co, ef,
		def(in.BpjsKesehatanEmployer, "0.04"), def(in.BpjsKesehatanEmployee, "0.01"), def(in.BpjsKesehatanCap, "12000000"),
		def(in.BpjsJhtEmployer, "0.037"), def(in.BpjsJhtEmployee, "0.02"),
		def(in.BpjsJpEmployer, "0.02"), def(in.BpjsJpEmployee, "0.01"), def(in.BpjsJpCap, "10042300"),
		def(in.BpjsJkkEmployer, "0.0024"), def(in.BpjsJkmEmployer, "0.0030"),
		ter, p.UserID)
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

func (s *Service) get(ctx context.Context, id string) (*PayrollSetting, error) {
	var st PayrollSetting
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, effective_from,
		       bpjs_kesehatan_employer, bpjs_kesehatan_employee, bpjs_kesehatan_cap,
		       bpjs_jht_employer, bpjs_jht_employee,
		       bpjs_jp_employer, bpjs_jp_employee, bpjs_jp_cap,
		       bpjs_jkk_employer, bpjs_jkm_employer,
		       pph21_ter, updated_at
		FROM payroll_setting WHERE id = $1`, id).
		Scan(&st.ID, &st.CompanyID, &st.EffectiveFrom,
			&st.BpjsKesehatanEmployer, &st.BpjsKesehatanEmployee, &st.BpjsKesehatanCap,
			&st.BpjsJhtEmployer, &st.BpjsJhtEmployee,
			&st.BpjsJpEmployer, &st.BpjsJpEmployee, &st.BpjsJpCap,
			&st.BpjsJkkEmployer, &st.BpjsJkmEmployer,
			&st.Pph21TER, &st.UpdatedAt)
	return &st, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-payroll-settings", Method: http.MethodGet,
		Path: "/admin/payroll-settings", Summary: "Active-company payroll settings (BPJS rates + TER)",
		Tags: []string{"Admin / Payroll"},
	}, func(ctx context.Context, _ *struct{}) (*pcListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		s, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &pcListOut{Body: pcListBody{Items: s}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "upsert-payroll-setting", Method: http.MethodPut,
		Path: "/admin/payroll-settings", Summary: "Create or update a payroll setting (keyed by effective_from)",
		Tags: []string{"Admin / Payroll"},
	}, func(ctx context.Context, in *pcSaveIn) (*pcItemOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		s, err := h.Service.Upsert(ctx, in.Body.ID, in.Body.PayrollInput)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &pcItemOut{Body: *s}, nil
	})
}

type (
	pcListOut  struct{ Body pcListBody }
	pcListBody struct {
		Items []PayrollSetting `json:"items"`
	}
	pcItemOut struct{ Body PayrollSetting }
	pcSaveIn  struct{ Body pcSaveBody }
	pcSaveBody struct {
		ID string `json:"id,omitempty"`
		PayrollInput
	}
)
