// Package employee implements the Employee master.
package employee

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "employee"

var sixteenDigits = regexp.MustCompile(`^[0-9]{16}$`)

type Employee struct {
	ID                       string     `json:"id"`
	Name                     string     `json:"name"`
	CompanyID                string     `json:"company_id"`
	EmployeeName             string     `json:"employee_name"`
	Gender                   string     `json:"gender,omitempty"`
	DateOfBirth              *time.Time `json:"date_of_birth,omitempty"`
	DateOfJoining            time.Time  `json:"date_of_joining"`
	DateOfRelieving          *time.Time `json:"date_of_relieving,omitempty"`
	DesignationID            string     `json:"designation_id,omitempty"`
	DepartmentID             string     `json:"department_id,omitempty"`
	ReportsToID              string     `json:"reports_to_id,omitempty"`
	Status                   string     `json:"status"`
	NIK                      string     `json:"nik,omitempty"`
	NPWP                     string     `json:"npwp,omitempty"`
	PTKPStatus               string     `json:"ptkp_status,omitempty"`
	BPJSKesehatanNo          string     `json:"bpjs_kesehatan_no,omitempty"`
	BPJSTKNo                 string     `json:"bpjs_tk_no,omitempty"`
	BankName                 string     `json:"bank_name,omitempty"`
	BankAccountNo            string     `json:"bank_account_no,omitempty"`
	BankAccountName          string     `json:"bank_account_name,omitempty"`
	PayrollPayableAccountID  string     `json:"payroll_payable_account_id,omitempty"`
	Email                    string     `json:"email,omitempty"`
	Phone                    string     `json:"phone,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

type EmployeeCreateInput struct {
	CompanyID                string `json:"company_id,omitempty"`
	EmployeeName             string `json:"employee_name"`
	Gender                   string `json:"gender,omitempty"`
	DateOfBirth              string `json:"date_of_birth,omitempty"`
	DateOfJoining            string `json:"date_of_joining"`
	DesignationID            string `json:"designation_id,omitempty"`
	DepartmentID             string `json:"department_id,omitempty"`
	NIK                      string `json:"nik,omitempty"`
	NPWP                     string `json:"npwp,omitempty"`
	PTKPStatus               string `json:"ptkp_status,omitempty"`
	BPJSKesehatanNo          string `json:"bpjs_kesehatan_no,omitempty"`
	BPJSTKNo                 string `json:"bpjs_tk_no,omitempty"`
	BankName                 string `json:"bank_name,omitempty"`
	BankAccountNo            string `json:"bank_account_no,omitempty"`
	BankAccountName          string `json:"bank_account_name,omitempty"`
	PayrollPayableAccountID  string `json:"payroll_payable_account_id,omitempty"`
	Email                    string `json:"email,omitempty"`
	Phone                    string `json:"phone,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in EmployeeCreateInput) (*Employee, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("employee: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("employee.company_id: required")
	}
	in.EmployeeName = strings.TrimSpace(in.EmployeeName)
	if in.EmployeeName == "" {
		return nil, errors.New("employee.employee_name: required")
	}
	doj, err := time.Parse("2006-01-02", in.DateOfJoining)
	if err != nil {
		return nil, fmt.Errorf("date_of_joining: %w", err)
	}
	if in.NIK != "" && !sixteenDigits.MatchString(in.NIK) {
		return nil, errors.New("employee.nik: must be 16 digits")
	}
	if in.NPWP != "" && !sixteenDigits.MatchString(in.NPWP) {
		return nil, errors.New("employee.npwp: must be 16 digits")
	}

	id := dbx.NewIDWithPrefix("emp")
	var e Employee
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, doj, nil)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO employee (
				id, name, company_id, employee_name, gender, date_of_birth, date_of_joining,
				designation_id, department_id, nik, npwp, ptkp_status,
				bpjs_kesehatan_no, bpjs_tk_no, bank_name, bank_account_no, bank_account_name,
				payroll_payable_account_id, email, phone, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$21)
			RETURNING id, name, company_id, employee_name, coalesce(gender,''), date_of_birth, date_of_joining, date_of_relieving,
			          coalesce(designation_id,''), coalesce(department_id,''), coalesce(reports_to_id,''), status,
			          coalesce(nik,''), coalesce(npwp,''), coalesce(ptkp_status,''),
			          coalesce(bpjs_kesehatan_no,''), coalesce(bpjs_tk_no,''),
			          coalesce(bank_name,''), coalesce(bank_account_no,''), coalesce(bank_account_name,''),
			          coalesce(payroll_payable_account_id,''), coalesce(email,''), coalesce(phone,''),
			          created_at, updated_at`,
			id, name, in.CompanyID, in.EmployeeName, nullable(in.Gender), nullableDate(in.DateOfBirth), doj,
			nullable(in.DesignationID), nullable(in.DepartmentID),
			nullable(in.NIK), nullable(in.NPWP), nullable(in.PTKPStatus),
			nullable(in.BPJSKesehatanNo), nullable(in.BPJSTKNo),
			nullable(in.BankName), nullable(in.BankAccountNo), nullable(in.BankAccountName),
			nullable(in.PayrollPayableAccountID), nullable(in.Email), nullable(in.Phone), p.UserID).
			Scan(&e.ID, &e.Name, &e.CompanyID, &e.EmployeeName, &e.Gender, &e.DateOfBirth, &e.DateOfJoining, &e.DateOfRelieving,
				&e.DesignationID, &e.DepartmentID, &e.ReportsToID, &e.Status,
				&e.NIK, &e.NPWP, &e.PTKPStatus,
				&e.BPJSKesehatanNo, &e.BPJSTKNo,
				&e.BankName, &e.BankAccountNo, &e.BankAccountName,
				&e.PayrollPayableAccountID, &e.Email, &e.Phone, &e.CreatedAt, &e.UpdatedAt)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, Doctype, e.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &e, err
}

func (s *Service) Get(ctx context.Context, id string) (*Employee, error) {
	var e Employee
	err := s.db.QueryRow(ctx, `
		SELECT id, name, company_id, employee_name, coalesce(gender,''), date_of_birth, date_of_joining, date_of_relieving,
		       coalesce(designation_id,''), coalesce(department_id,''), coalesce(reports_to_id,''), status,
		       coalesce(nik,''), coalesce(npwp,''), coalesce(ptkp_status,''),
		       coalesce(bpjs_kesehatan_no,''), coalesce(bpjs_tk_no,''),
		       coalesce(bank_name,''), coalesce(bank_account_no,''), coalesce(bank_account_name,''),
		       coalesce(payroll_payable_account_id,''), coalesce(email,''), coalesce(phone,''),
		       created_at, updated_at
		FROM employee WHERE id = $1 AND is_deleted = false`, id).
		Scan(&e.ID, &e.Name, &e.CompanyID, &e.EmployeeName, &e.Gender, &e.DateOfBirth, &e.DateOfJoining, &e.DateOfRelieving,
			&e.DesignationID, &e.DepartmentID, &e.ReportsToID, &e.Status,
			&e.NIK, &e.NPWP, &e.PTKPStatus,
			&e.BPJSKesehatanNo, &e.BPJSTKNo,
			&e.BankName, &e.BankAccountNo, &e.BankAccountName,
			&e.PayrollPayableAccountID, &e.Email, &e.Phone, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("employee %s not found", id)
	}
	return &e, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]Employee, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM employee WHERE company_id = $1 AND is_deleted = false ORDER BY employee_name`, companyID)
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
	out := make([]Employee, 0, len(ids))
	for _, id := range ids {
		e, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, nil
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
func nullableDate(s string) any {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return t
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-employees", Method: http.MethodGet,
		Path: "/hr/employees", Summary: "List employees", Tags: []string{"HR / Employee"},
	}, func(ctx context.Context, _ *struct{}) (*empListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		es, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &empListOut{Body: empListBody{Items: es}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-employee", Method: http.MethodPost,
		Path: "/hr/employees", Summary: "Create an employee",
		Tags: []string{"HR / Employee"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *empCreateIn) (*empOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		e, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &empOut{Body: *e}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-employee", Method: http.MethodGet,
		Path: "/hr/employees/{id}", Summary: "Get an employee", Tags: []string{"HR / Employee"},
	}, func(ctx context.Context, in *empGetIn) (*empOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		e, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &empOut{Body: *e}, nil
	})
}

type (
	empCreateIn struct{ Body EmployeeCreateInput }
	empOut      struct{ Body Employee }
	empListOut  struct{ Body empListBody }
	empListBody struct {
		Items []Employee `json:"items"`
	}
	empGetIn struct {
		ID string `path:"id"`
	}
)
