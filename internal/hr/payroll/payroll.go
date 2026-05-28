// Package payroll implements the Payroll Entry + Salary Slip documents.
//
// CreateDraft for Payroll Entry: scans all active Salary Structures for the
// company whose date range intersects [start, end], builds one Salary Slip per
// employee, computes BPJS + PPh 21 from internal/platform/payroll, and stores
// everything as draft.
//
// Submit: posts GL — for each employee, Dr Salary Expense (gross + employer BPJS),
// Cr each deduction-account (PPh 21 payable, BPJS payable both sides), Cr the
// payroll payable / cash account for net pay. All inside one balanced voucher.
package payroll

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
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	calc "github.com/tandigital/logica-erp/internal/platform/payroll"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const (
	DoctypePayrollEntry = "payroll_entry"
	DoctypeSalarySlip   = "salary_slip"
	VoucherType         = "Payroll Entry"
)

type PayrollEntry struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	CompanyID         string             `json:"company_id"`
	PostingDate       time.Time          `json:"posting_date"`
	FiscalYearID      string             `json:"fiscal_year_id"`
	StartDate         time.Time          `json:"start_date"`
	EndDate           time.Time          `json:"end_date"`
	PaymentAccountID  string             `json:"payment_account_id"`
	TotalGross        decimal.Decimal    `json:"total_gross"`
	TotalDeductions   decimal.Decimal    `json:"total_deductions"`
	TotalNet          decimal.Decimal    `json:"total_net"`
	Docstatus         submittable.Status `json:"docstatus"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	Slips             []SalarySlip       `json:"slips,omitempty"`
}

type SalarySlip struct {
	ID                string                `json:"id"`
	Name              string                `json:"name"`
	CompanyID         string                `json:"company_id"`
	PayrollEntryID    string                `json:"payroll_entry_id"`
	EmployeeID        string                `json:"employee_id"`
	PostingDate       time.Time             `json:"posting_date"`
	StartDate         time.Time             `json:"start_date"`
	EndDate           time.Time             `json:"end_date"`
	GrossPay          decimal.Decimal       `json:"gross_pay"`
	TotalDeductions   decimal.Decimal       `json:"total_deductions"`
	NetPay            decimal.Decimal       `json:"net_pay"`
	PPh21Amount       decimal.Decimal       `json:"pph21_amount"`
	BPJSEmployeeTotal decimal.Decimal       `json:"bpjs_employee_total"`
	BPJSEmployerTotal decimal.Decimal       `json:"bpjs_employer_total"`
	Components        []SalarySlipComponent `json:"components"`
}

type SalarySlipComponent struct {
	ID                 string          `json:"id"`
	RowIndex           int             `json:"row_index"`
	SalaryComponentID  string          `json:"salary_component_id,omitempty"`
	ComponentName      string          `json:"component_name"`
	ComponentType      string          `json:"component_type"`
	Amount             decimal.Decimal `json:"amount"`
	AccountID          string          `json:"account_id"`
}

type PayrollEntryCreateInput struct {
	CompanyID        string `json:"company_id,omitempty"`
	PostingDate      string `json:"posting_date"`
	StartDate        string `json:"start_date"`
	EndDate          string `json:"end_date"`
	PaymentAccountID string `json:"payment_account_id"`
	// Account IDs that the payroll calc posts deductions to.
	PPh21PayableAccountID            string  `json:"pph21_payable_account_id"`
	BPJSKesehatanPayableAccountID    string  `json:"bpjs_kesehatan_payable_account_id"`
	BPJSKetenagakerjaanPayableAccountID string `json:"bpjs_tk_payable_account_id"`
	SalaryExpenseAccountID           string  `json:"salary_expense_account_id"`
	BPJSExpenseAccountID             string  `json:"bpjs_expense_account_id"`
	JKKRate                          string  `json:"jkk_rate,omitempty"`     // optional override (default 0.24%)
}

type Service struct {
	db        *dbx.DB
	Approvals approvalChecker
	Workflow  workflowGate
}

type approvalChecker interface {
	CheckSubmit(ctx context.Context, tx pgx.Tx, doctype, docID, docName, companyID string, fields map[string]any) error
}

type workflowGate interface {
	CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) CreateDraft(ctx context.Context, in PayrollEntryCreateInput) (*PayrollEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("payroll_entry: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("payroll_entry.company_id: required")
	}
	pd, err := time.Parse("2006-01-02", in.PostingDate)
	if err != nil {
		return nil, fmt.Errorf("posting_date: %w", err)
	}
	sd, err := time.Parse("2006-01-02", in.StartDate)
	if err != nil {
		return nil, fmt.Errorf("start_date: %w", err)
	}
	ed, err := time.Parse("2006-01-02", in.EndDate)
	if err != nil {
		return nil, fmt.Errorf("end_date: %w", err)
	}
	if !ed.After(sd) {
		return nil, errors.New("end_date must be after start_date")
	}
	if in.PaymentAccountID == "" || in.PPh21PayableAccountID == "" ||
		in.BPJSKesehatanPayableAccountID == "" || in.BPJSKetenagakerjaanPayableAccountID == "" ||
		in.SalaryExpenseAccountID == "" || in.BPJSExpenseAccountID == "" {
		return nil, errors.New("payroll_entry: all account ids are required")
	}
	jkkRate := decimal.Zero
	if in.JKKRate != "" {
		r, err := decimal.NewFromString(in.JKKRate)
		if err != nil {
			return nil, fmt.Errorf("jkk_rate: %w", err)
		}
		jkkRate = r
	}

	id := dbx.NewIDWithPrefix("payrun")
	var out PayrollEntry
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var fyID string
		if err := tx.QueryRow(ctx, `
			SELECT fy.id FROM fiscal_year fy
			JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
			WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
			ORDER BY fy.start_date DESC LIMIT 1`, in.CompanyID, pd).Scan(&fyID); err != nil {
			return fmt.Errorf("fiscal_year: %w", err)
		}
		seriesID, pattern, err := pickSeries(ctx, tx, DoctypePayrollEntry, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, pd, nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO payroll_entry (id, name, company_id, posting_date, fiscal_year_id, start_date, end_date, payment_account_id, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
			id, name, in.CompanyID, pd, fyID, sd, ed, in.PaymentAccountID, p.UserID); err != nil {
			return err
		}

		// For each active salary structure in this company intersecting [sd, ed]:
		rows, err := tx.Query(ctx, `
			SELECT ss.id, ss.employee_id, e.ptkp_status
			FROM salary_structure ss
			JOIN employee e ON e.id = ss.employee_id AND e.is_deleted = false
			WHERE ss.company_id = $1 AND ss.is_active = true
			  AND ss.from_date <= $3
			  AND (ss.to_date IS NULL OR ss.to_date >= $2)`,
			in.CompanyID, sd, ed)
		if err != nil {
			return err
		}
		type structRef struct {
			structureID, employeeID string
			ptkpStatus              *string
		}
		var structures []structRef
		for rows.Next() {
			var s structRef
			if err := rows.Scan(&s.structureID, &s.employeeID, &s.ptkpStatus); err != nil {
				rows.Close()
				return err
			}
			structures = append(structures, s)
		}
		rows.Close()

		slipSeriesID, slipPattern, err := pickSeries(ctx, tx, DoctypeSalarySlip, in.CompanyID)
		if err != nil {
			return err
		}

		totalGross := decimal.Zero
		totalDed := decimal.Zero
		totalNet := decimal.Zero

		for _, sref := range structures {
			ptkp := "TK/0"
			if sref.ptkpStatus != nil {
				ptkp = *sref.ptkpStatus
			}

			// Sum earnings/deductions on the structure to derive gross.
			compRows, err := tx.Query(ctx, `
				SELECT ssc.salary_component_id, sc.name, sc.component_type, sc.account_id, ssc.amount, sc.is_taxable
				FROM salary_structure_component ssc
				JOIN salary_component sc ON sc.id = ssc.salary_component_id
				WHERE ssc.salary_structure_id = $1
				ORDER BY ssc.row_index`, sref.structureID)
			if err != nil {
				return err
			}
			type comp struct {
				id, name, ctype, acct string
				amt                   decimal.Decimal
				taxable               bool
			}
			var comps []comp
			for compRows.Next() {
				var c comp
				if err := compRows.Scan(&c.id, &c.name, &c.ctype, &c.acct, &c.amt, &c.taxable); err != nil {
					compRows.Close()
					return err
				}
				comps = append(comps, c)
			}
			compRows.Close()

			grossTaxable := decimal.Zero
			grossPay := decimal.Zero
			structDed := decimal.Zero
			for _, c := range comps {
				if c.ctype == "earning" {
					grossPay = grossPay.Add(c.amt)
					if c.taxable {
						grossTaxable = grossTaxable.Add(c.amt)
					}
				} else {
					structDed = structDed.Add(c.amt)
				}
			}

			// BPJS based on gross taxable.
			bpjs := calc.CalculateBPJS(grossTaxable, jkkRate)
			// PPh 21 based on gross taxable, after employee BPJS deductible.
			pph, err := calc.CalculatePPh21(grossTaxable, bpjs.TotalEmployee, ptkp)
			if err != nil {
				return err
			}

			totalDedSlip := structDed.Add(bpjs.TotalEmployee).Add(pph.PPhMonthly)
			netPay := grossPay.Sub(totalDedSlip)

			slipID := dbx.NewIDWithPrefix("slip")
			slipName, err := naming.Next(ctx, tx, slipSeriesID, slipPattern, pd, nil)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO salary_slip (
					id, name, company_id, payroll_entry_id, employee_id, posting_date, start_date, end_date,
					gross_pay, total_deductions, net_pay, pph21_amount, bpjs_employee_total, bpjs_employer_total,
					created_by, updated_by
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)`,
				slipID, slipName, in.CompanyID, id, sref.employeeID, pd, sd, ed,
				grossPay, totalDedSlip, netPay, pph.PPhMonthly, bpjs.TotalEmployee, bpjs.TotalEmployer, p.UserID); err != nil {
				return err
			}

			// Slip components: structure components + BPJS lines + PPh 21 line.
			rowIdx := 1
			insertComp := func(cid, name, ctype, acct string, amt decimal.Decimal) error {
				if amt.IsZero() {
					return nil
				}
				_, err := tx.Exec(ctx, `
					INSERT INTO salary_slip_component (id, salary_slip_id, row_index, salary_component_id, component_name, component_type, amount, account_id)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
					dbx.NewIDWithPrefix("sslc"), slipID, rowIdx, nullable(cid), name, ctype, amt, acct)
				rowIdx++
				return err
			}
			for _, c := range comps {
				if err := insertComp(c.id, c.name, c.ctype, c.acct, c.amt); err != nil {
					return err
				}
			}
			// BPJS employee deductions go to Kesehatan/Ketenagakerjaan payable accounts.
			if err := insertComp("", "BPJS Kesehatan (Employee)", "deduction", in.BPJSKesehatanPayableAccountID, bpjs.KesehatanEmployee); err != nil {
				return err
			}
			if err := insertComp("", "BPJS JHT (Employee)", "deduction", in.BPJSKetenagakerjaanPayableAccountID, bpjs.JHTEmployee); err != nil {
				return err
			}
			if err := insertComp("", "BPJS JP (Employee)", "deduction", in.BPJSKetenagakerjaanPayableAccountID, bpjs.JPEmployee); err != nil {
				return err
			}
			if err := insertComp("", "PPh 21", "deduction", in.PPh21PayableAccountID, pph.PPhMonthly); err != nil {
				return err
			}

			totalGross = totalGross.Add(grossPay)
			totalDed = totalDed.Add(totalDedSlip)
			totalNet = totalNet.Add(netPay)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE payroll_entry SET total_gross = $1, total_deductions = $2, total_net = $3 WHERE id = $4`,
			totalGross, totalDed, totalNet, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, DoctypePayrollEntry, id, p.UserID, audit.ActionCreate, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := loadPayrollEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

func (s *Service) Submit(ctx context.Context, id string) (*PayrollEntry, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("payroll_entry: unauthenticated")
	}
	var out PayrollEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pe, err := loadPayrollEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		if pe.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, "payroll_entry"); err != nil {
				return err
			}
		}
		if s.Approvals != nil {
			net, _ := pe.TotalNet.Float64()
			if err := s.Approvals.CheckSubmit(ctx, tx, "payroll_entry", pe.ID, pe.Name, pe.CompanyID,
				map[string]any{"total_net": net, "amount": net}); err != nil {
				return err
			}
		}

		var paymentCur string
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, pe.PaymentAccountID).Scan(&paymentCur); err != nil {
			return err
		}

		entries := []ledger.Entry{}
		acctCurCache := map[string]string{pe.PaymentAccountID: paymentCur}
		acctCur := func(acctID string) (string, error) {
			if c, ok := acctCurCache[acctID]; ok {
				return c, nil
			}
			var c string
			if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, acctID).Scan(&c); err != nil {
				return "", err
			}
			acctCurCache[acctID] = c
			return c, nil
		}

		// For each slip: Dr earning accounts (gross), Cr each deduction account, Cr Payment account (net).
		for _, slip := range pe.Slips {
			// Sum earning components → Dr their accounts (these are normally Salary Expense lines, but can be split per component).
			for _, c := range slip.Components {
				cur, err := acctCur(c.AccountID)
				if err != nil {
					return err
				}
				if c.ComponentType == "earning" {
					entries = append(entries, ledger.Entry{
						AccountID:              c.AccountID,
						Debit:                  c.Amount,
						AccountCurrency:        cur,
						DebitInAccountCurrency: c.Amount,
						PartyType:              ledger.PartyEmployee,
						PartyID:                slip.EmployeeID,
						Remarks:                slip.Name + " — " + c.ComponentName,
					})
				} else {
					entries = append(entries, ledger.Entry{
						AccountID:               c.AccountID,
						Credit:                  c.Amount,
						AccountCurrency:         cur,
						CreditInAccountCurrency: c.Amount,
						PartyType:               ledger.PartyEmployee,
						PartyID:                 slip.EmployeeID,
						Remarks:                 slip.Name + " — " + c.ComponentName,
					})
				}
			}
			// Cr Cash/Bank for net pay.
			entries = append(entries, ledger.Entry{
				AccountID:               pe.PaymentAccountID,
				Credit:                  slip.NetPay,
				AccountCurrency:         paymentCur,
				CreditInAccountCurrency: slip.NetPay,
				PartyType:               ledger.PartyEmployee,
				PartyID:                 slip.EmployeeID,
				Remarks:                 slip.Name + " — net pay",
			})

			// Mark slip submitted.
			if _, err := tx.Exec(ctx,
				`UPDATE salary_slip SET docstatus = 1, updated_by = $1 WHERE id = $2`,
				p.UserID, slip.ID); err != nil {
				return err
			}
		}

		v := ledger.Voucher{
			Type: VoucherType, ID: pe.ID, Name: pe.Name,
			CompanyID: pe.CompanyID, PostingDate: pe.PostingDate, FiscalYearID: pe.FiscalYearID, CreatedBy: p.UserID,
		}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE payroll_entry SET docstatus = 1, submitted_at = now(), submitted_by = $1, updated_by = $1 WHERE id = $2`,
			p.UserID, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, DoctypePayrollEntry, id, p.UserID, audit.ActionSubmit, audit.Diff{}); err != nil {
			return err
		}
		loaded, err := loadPayrollEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

func (s *Service) Get(ctx context.Context, id string) (*PayrollEntry, error) {
	var out *PayrollEntry
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		pe, err := loadPayrollEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		out = pe
		return nil
	})
	return out, err
}

// ---- Salary Component CRUD (light) ----

type SalaryComponentCreateInput struct {
	Name          string `json:"name"`
	ComponentType string `json:"component_type"`     // earning | deduction
	IsTaxable     *bool  `json:"is_taxable,omitempty"`
	AccountID     string `json:"account_id"`
}

func (s *Service) CreateComponent(ctx context.Context, in SalaryComponentCreateInput) (map[string]any, error) {
	if in.Name == "" || in.AccountID == "" || (in.ComponentType != "earning" && in.ComponentType != "deduction") {
		return nil, errors.New("salary_component: name/account_id/component_type required (type must be earning|deduction)")
	}
	taxable := true
	if in.IsTaxable != nil {
		taxable = *in.IsTaxable
	}
	id := dbx.NewIDWithPrefix("salc")
	_, err := s.db.Exec(ctx, `
		INSERT INTO salary_component (id, name, component_type, is_taxable, account_id)
		VALUES ($1,$2,$3,$4,$5)`,
		id, in.Name, in.ComponentType, taxable, in.AccountID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": in.Name, "component_type": in.ComponentType, "is_taxable": taxable, "account_id": in.AccountID}, nil
}

// ---- Salary Structure CRUD ----

type SalaryStructureCreateInput struct {
	CompanyID  string                            `json:"company_id,omitempty"`
	EmployeeID string                            `json:"employee_id"`
	FromDate   string                            `json:"from_date"`
	Components []SalaryStructureComponentInput   `json:"components"`
}

type SalaryStructureComponentInput struct {
	SalaryComponentID string `json:"salary_component_id"`
	Amount            string `json:"amount"`
}

func (s *Service) CreateStructure(ctx context.Context, in SalaryStructureCreateInput) (map[string]any, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("salary_structure: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" || in.EmployeeID == "" {
		return nil, errors.New("salary_structure: company_id and employee_id required")
	}
	fd, err := time.Parse("2006-01-02", in.FromDate)
	if err != nil {
		return nil, fmt.Errorf("from_date: %w", err)
	}
	if len(in.Components) == 0 {
		return nil, errors.New("salary_structure.components: required")
	}
	id := dbx.NewIDWithPrefix("ss")
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickSeries(ctx, tx, "salary_structure", in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, fd, nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO salary_structure (id, name, company_id, employee_id, from_date, docstatus, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,1,$6,$6)`,
			id, name, in.CompanyID, in.EmployeeID, fd, p.UserID); err != nil {
			return err
		}
		for i, c := range in.Components {
			amt, err := decimal.NewFromString(c.Amount)
			if err != nil {
				return fmt.Errorf("components[%d].amount: %w", i, err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO salary_structure_component (id, salary_structure_id, row_index, salary_component_id, amount)
				VALUES ($1,$2,$3,$4,$5)`,
				dbx.NewIDWithPrefix("ssc"), id, i+1, c.SalaryComponentID, amt.Round(money.Precision)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

// ---- helpers ----

func loadPayrollEntry(ctx context.Context, tx pgx.Tx, id string) (*PayrollEntry, error) {
	var pe PayrollEntry
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, posting_date, fiscal_year_id, start_date, end_date, payment_account_id,
		       total_gross, total_deductions, total_net, docstatus, created_at, updated_at
		FROM payroll_entry WHERE id = $1`, id).
		Scan(&pe.ID, &pe.Name, &pe.CompanyID, &pe.PostingDate, &pe.FiscalYearID, &pe.StartDate, &pe.EndDate, &pe.PaymentAccountID,
			&pe.TotalGross, &pe.TotalDeductions, &pe.TotalNet, &pe.Docstatus, &pe.CreatedAt, &pe.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("payroll_entry %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	// Collect slip headers first, THEN load their components — pgx forbids issuing
	// another query while a rows cursor is still active on the same conn.
	rows, err := tx.Query(ctx, `
		SELECT id, name, company_id, payroll_entry_id, employee_id, posting_date, start_date, end_date,
		       gross_pay, total_deductions, net_pay, pph21_amount, bpjs_employee_total, bpjs_employer_total
		FROM salary_slip WHERE payroll_entry_id = $1 ORDER BY name`, id)
	if err != nil {
		return nil, err
	}
	var slips []SalarySlip
	for rows.Next() {
		var slip SalarySlip
		if err := rows.Scan(&slip.ID, &slip.Name, &slip.CompanyID, &slip.PayrollEntryID, &slip.EmployeeID,
			&slip.PostingDate, &slip.StartDate, &slip.EndDate,
			&slip.GrossPay, &slip.TotalDeductions, &slip.NetPay,
			&slip.PPh21Amount, &slip.BPJSEmployeeTotal, &slip.BPJSEmployerTotal); err != nil {
			rows.Close()
			return nil, err
		}
		slips = append(slips, slip)
	}
	rows.Close()

	for i := range slips {
		crows, err := tx.Query(ctx, `
			SELECT id, row_index, coalesce(salary_component_id,''), component_name, component_type, amount, account_id
			FROM salary_slip_component WHERE salary_slip_id = $1 ORDER BY row_index`, slips[i].ID)
		if err != nil {
			return nil, err
		}
		for crows.Next() {
			var c SalarySlipComponent
			if err := crows.Scan(&c.ID, &c.RowIndex, &c.SalaryComponentID, &c.ComponentName, &c.ComponentType, &c.Amount, &c.AccountID); err != nil {
				crows.Close()
				return nil, err
			}
			slips[i].Components = append(slips[i].Components, c)
		}
		crows.Close()
	}
	pe.Slips = slips
	return &pe, nil
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
		OperationID:   "create-salary-component",
		Method:        http.MethodPost,
		Path:          "/hr/salary-components",
		Summary:       "Create a salary component (earning/deduction)",
		Tags:          []string{"HR / Payroll"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *scCreateIn) (*scOut, error) {
		if err := h.Perm.Check(ctx, DoctypePayrollEntry, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.CreateComponent(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &scOut{Body: r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-salary-structure",
		Method:        http.MethodPost,
		Path:          "/hr/salary-structures",
		Summary:       "Create a salary structure for an employee",
		Tags:          []string{"HR / Payroll"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *ssCreateIn) (*ssOut, error) {
		if err := h.Perm.Check(ctx, DoctypePayrollEntry, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.CreateStructure(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &ssOut{Body: r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-payroll-entry",
		Method:        http.MethodPost,
		Path:          "/hr/payroll-entries",
		Summary:       "Create a payroll run (generates Salary Slips with BPJS + PPh 21)",
		Tags:          []string{"HR / Payroll"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *peCreateIn) (*peOut, error) {
		if err := h.Perm.Check(ctx, DoctypePayrollEntry, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		pe, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &peOut{Body: *pe}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-payroll-entry",
		Method:      http.MethodPost,
		Path:        "/hr/payroll-entries/{id}/submit",
		Summary:     "Submit a payroll run (posts GL)",
		Tags:        []string{"HR / Payroll"},
	}, func(ctx context.Context, in *peGetIn) (*peOut, error) {
		if err := h.Perm.Check(ctx, DoctypePayrollEntry, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		pe, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &peOut{Body: *pe}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-payroll-entry",
		Method:      http.MethodGet,
		Path:        "/hr/payroll-entries/{id}",
		Summary:     "Get a payroll run",
		Tags:        []string{"HR / Payroll"},
	}, func(ctx context.Context, in *peGetIn) (*peOut, error) {
		if err := h.Perm.Check(ctx, DoctypePayrollEntry, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		pe, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &peOut{Body: *pe}, nil
	})
}

type (
	scCreateIn struct{ Body SalaryComponentCreateInput }
	scOut      struct{ Body map[string]any }
	ssCreateIn struct{ Body SalaryStructureCreateInput }
	ssOut      struct{ Body map[string]any }
	peCreateIn struct{ Body PayrollEntryCreateInput }
	peOut      struct{ Body PayrollEntry }
	peGetIn    struct {
		ID string `path:"id"`
	}
)
