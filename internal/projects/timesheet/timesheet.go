// Package timesheet implements the Timesheet document — a window of
// time-tracked work an employee logs against a project / task / activity.
// Submit does NOT post to GL; timesheets feed Sales Invoice generation in a
// future "Make Invoice from Timesheet" flow.
//
// Header totals (total_hours, total_billable_hours, total_billable_amount)
// are derived from entries on create / update / submit so the FE doesn't
// have to recompute them.
//
// Status machine:
//
//	Draft  ──submit──▶  Submitted  ──(SI item created)──▶  Billed
//	                          │
//	                          └─cancel▶  Cancelled (docstatus=2)
//
// Today the "Billed" transition is set externally by the SI-from-Timesheet
// flow (deferred). For v1 timesheets stay at "Submitted" until manually
// cancelled.
package timesheet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
)

const Doctype = "timesheet"

// Status enum — stored in timesheet.status.
const (
	StatusDraft     = "Draft"
	StatusSubmitted = "Submitted"
	StatusBilled    = "Billed"
	StatusCancelled = "Cancelled"
)

// ---- domain types ----

type Timesheet struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	CompanyID           string             `json:"company_id"`
	EmployeeID          string             `json:"employee_id,omitempty"`
	StartDate           time.Time          `json:"start_date"`
	EndDate             time.Time          `json:"end_date"`
	TotalHours          decimal.Decimal    `json:"total_hours"`
	TotalBillableHours  decimal.Decimal    `json:"total_billable_hours"`
	TotalBillableAmount decimal.Decimal    `json:"total_billable_amount"`
	TotalBilledAmount   decimal.Decimal    `json:"total_billed_amount"`
	Status              string             `json:"status"`
	CustomerID          string             `json:"customer_id,omitempty"`
	Remarks             string             `json:"remarks,omitempty"`
	Docstatus           submittable.Status `json:"docstatus"`
	SubmittedAt         *time.Time         `json:"submitted_at,omitempty"`
	CancelledAt         *time.Time         `json:"cancelled_at,omitempty"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	Entries             []TimesheetEntry   `json:"entries"`
}

type TimesheetEntry struct {
	ID                 string          `json:"id"`
	RowIndex           int             `json:"row_index"`
	ActivityTypeID     string          `json:"activity_type_id,omitempty"`
	ProjectID          string          `json:"project_id,omitempty"`
	TaskID             string          `json:"task_id,omitempty"`
	Description        string          `json:"description,omitempty"`
	FromTime           time.Time       `json:"from_time"`
	ToTime             time.Time       `json:"to_time"`
	Hours              decimal.Decimal `json:"hours"`
	IsBillable         bool            `json:"is_billable"`
	BillingRate        decimal.Decimal `json:"billing_rate"`
	BillingAmount      decimal.Decimal `json:"billing_amount"`
	SalesInvoiceItemID string          `json:"sales_invoice_item_id,omitempty"`
}

// ---- input shapes ----

type TSCreateInput struct {
	CompanyID  string           `json:"company_id,omitempty"`
	EmployeeID string           `json:"employee_id,omitempty"`
	StartDate  string           `json:"start_date"`
	EndDate    string           `json:"end_date"`
	CustomerID string           `json:"customer_id,omitempty"`
	Remarks    string           `json:"remarks,omitempty"`
	Entries    []TSEntryInput   `json:"entries"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

type TSEntryInput struct {
	ActivityTypeID string `json:"activity_type_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	Description    string `json:"description,omitempty"`
	FromTime       string `json:"from_time"` // RFC3339
	ToTime         string `json:"to_time"`
	IsBillable     bool   `json:"is_billable"`
	BillingRate    string `json:"billing_rate,omitempty"`
}

// ---- Service ----

type Service struct {
	db       *dbx.DB
	Workflow workflowGate
}

type workflowGate interface {
	CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error
}

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CreateDraft ----

func (s *Service) CreateDraft(ctx context.Context, in TSCreateInput) (*Timesheet, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("timesheet: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" {
		return nil, errors.New("timesheet.company_id: required")
	}
	sd, err := time.Parse("2006-01-02", in.StartDate)
	if err != nil {
		return nil, fmt.Errorf("timesheet.start_date: %w", err)
	}
	ed, err := time.Parse("2006-01-02", in.EndDate)
	if err != nil {
		return nil, fmt.Errorf("timesheet.end_date: %w", err)
	}
	if ed.Before(sd) {
		return nil, errors.New("timesheet.end_date: must be >= start_date")
	}
	if len(in.Entries) == 0 {
		return nil, errors.New("timesheet.entries: at least one required")
	}

	id := dbx.NewIDWithPrefix("ts")
	var out Timesheet
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		seriesID, pattern, err := pickNamingSeries(ctx, tx, Doctype, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, sd, nil)
		if err != nil {
			return err
		}
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}

		entries, totals, err := buildEntries(in.Entries)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO timesheet (
				id, name, company_id, employee_id, start_date, end_date,
				total_hours, total_billable_hours, total_billable_amount,
				status, customer_id, remarks,
				custom_fields, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)`,
			id, name, in.CompanyID, nullable(in.EmployeeID), sd, ed,
			totals.hours, totals.billableHours, totals.billableAmount,
			StatusDraft, nullable(in.CustomerID), nullable(in.Remarks),
			cf, p.UserID); err != nil {
			return err
		}
		for _, e := range entries {
			if _, err := tx.Exec(ctx, `
				INSERT INTO timesheet_entry (
					id, timesheet_id, row_index, activity_type_id, project_id, task_id,
					description, from_time, to_time, hours,
					is_billable, billing_rate, billing_amount
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				e.ID, id, e.RowIndex, nullable(e.ActivityTypeID), nullable(e.ProjectID), nullable(e.TaskID),
				nullable(e.Description), e.FromTime, e.ToTime, e.Hours,
				e.IsBillable, e.BillingRate, e.BillingAmount); err != nil {
				return err
			}
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

// ---- Update (draft-only) ----

// Update replaces the entries on a draft timesheet and recomputes header
// totals. The header fields (start/end/customer/remarks) are also rewritten.
func (s *Service) Update(ctx context.Context, id string, in TSCreateInput) (*Timesheet, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("timesheet: unauthenticated")
	}
	sd, err := time.Parse("2006-01-02", in.StartDate)
	if err != nil {
		return nil, fmt.Errorf("timesheet.start_date: %w", err)
	}
	ed, err := time.Parse("2006-01-02", in.EndDate)
	if err != nil {
		return nil, fmt.Errorf("timesheet.end_date: %w", err)
	}
	if ed.Before(sd) {
		return nil, errors.New("timesheet.end_date: must be >= start_date")
	}
	if len(in.Entries) == 0 {
		return nil, errors.New("timesheet.entries: at least one required")
	}
	var out Timesheet
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		ts, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if ts.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		entries, totals, err := buildEntries(in.Entries)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE timesheet SET
				employee_id = $2, start_date = $3, end_date = $4,
				total_hours = $5, total_billable_hours = $6, total_billable_amount = $7,
				customer_id = $8, remarks = $9, custom_fields = $10, updated_by = $11
			WHERE id = $1`,
			id, nullable(in.EmployeeID), sd, ed,
			totals.hours, totals.billableHours, totals.billableAmount,
			nullable(in.CustomerID), nullable(in.Remarks), cf, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM timesheet_entry WHERE timesheet_id = $1`, id); err != nil {
			return err
		}
		for _, e := range entries {
			if _, err := tx.Exec(ctx, `
				INSERT INTO timesheet_entry (
					id, timesheet_id, row_index, activity_type_id, project_id, task_id,
					description, from_time, to_time, hours,
					is_billable, billing_rate, billing_amount
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				e.ID, id, e.RowIndex, nullable(e.ActivityTypeID), nullable(e.ProjectID), nullable(e.TaskID),
				nullable(e.Description), e.FromTime, e.ToTime, e.Hours,
				e.IsBillable, e.BillingRate, e.BillingAmount); err != nil {
				return err
			}
		}
		if err := audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in}); err != nil {
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

func (s *Service) Submit(ctx context.Context, id string) (*Timesheet, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("timesheet: unauthenticated")
	}
	var out Timesheet
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		ts, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if ts.Docstatus != submittable.Draft {
			return submittable.ErrNotDraft
		}
		if s.Workflow != nil {
			if err := s.Workflow.CheckSubmitRole(ctx, tx, Doctype); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE timesheet
			SET docstatus = 1, submitted_at = now(), submitted_by = $1,
			    status = $2, updated_by = $1
			WHERE id = $3`, p.UserID, StatusSubmitted, id); err != nil {
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

// ---- Cancel ----

func (s *Service) Cancel(ctx context.Context, id string) (*Timesheet, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("timesheet: unauthenticated")
	}
	var out Timesheet
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		ts, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if ts.Docstatus != submittable.Submitted {
			return submittable.ErrNotSubmitted
		}
		// Refuse if any entry has been billed (i.e., linked to a SI line).
		// The SI cancellation path is responsible for un-linking those before
		// the timesheet can be cancelled.
		for _, e := range ts.Entries {
			if e.SalesInvoiceItemID != "" {
				return errors.New("timesheet: cannot cancel — entries already billed via Sales Invoice; cancel that invoice first")
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE timesheet
			SET docstatus = 2, cancelled_at = now(), cancelled_by = $1,
			    status = $2, updated_by = $1
			WHERE id = $3`, p.UserID, StatusCancelled, id); err != nil {
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

// ---- Read ----

func (s *Service) Get(ctx context.Context, id string) (*Timesheet, error) {
	var out *Timesheet
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		ts, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = ts
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, companyID string) ([]Timesheet, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM timesheet
		WHERE company_id = $1
		ORDER BY start_date DESC, name DESC LIMIT 200`, companyID)
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
	out := make([]Timesheet, 0, len(ids))
	for _, id := range ids {
		ts, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *ts)
	}
	return out, nil
}

// ---- Pure helpers ----

type entryTotals struct {
	hours          decimal.Decimal
	billableHours  decimal.Decimal
	billableAmount decimal.Decimal
}

// buildEntries validates each entry and computes hours + billing_amount.
// Hours are wall-clock — (to - from) in hours, rounded to 2 decimals.
// Billing only counts when is_billable=true; non-billable entries contribute
// hours to total_hours but zero to billable_amount.
func buildEntries(in []TSEntryInput) ([]TimesheetEntry, entryTotals, error) {
	out := make([]TimesheetEntry, len(in))
	tot := entryTotals{}
	for i, e := range in {
		from, err := time.Parse(time.RFC3339, e.FromTime)
		if err != nil {
			return nil, tot, fmt.Errorf("entries[%d].from_time: %w", i, err)
		}
		to, err := time.Parse(time.RFC3339, e.ToTime)
		if err != nil {
			return nil, tot, fmt.Errorf("entries[%d].to_time: %w", i, err)
		}
		if !to.After(from) {
			return nil, tot, fmt.Errorf("entries[%d]: to_time must be after from_time", i)
		}
		// Hours: minute precision (2 decimals on a 60-min basis).
		mins := decimal.NewFromInt(int64(to.Sub(from) / time.Minute))
		hours := mins.Div(decimal.NewFromInt(60)).Round(2)

		rate := decimal.Zero
		if e.IsBillable && e.BillingRate != "" {
			r, err := decimal.NewFromString(strings.TrimSpace(e.BillingRate))
			if err != nil {
				return nil, tot, fmt.Errorf("entries[%d].billing_rate: %w", i, err)
			}
			if r.IsNegative() {
				return nil, tot, fmt.Errorf("entries[%d].billing_rate: must be >= 0", i)
			}
			rate = r
		}
		amount := decimal.Zero
		if e.IsBillable {
			amount = hours.Mul(rate).Round(money.Precision)
		}

		out[i] = TimesheetEntry{
			ID: dbx.NewIDWithPrefix("tse"), RowIndex: i + 1,
			ActivityTypeID: e.ActivityTypeID, ProjectID: e.ProjectID, TaskID: e.TaskID,
			Description: e.Description,
			FromTime:    from, ToTime: to, Hours: hours,
			IsBillable:  e.IsBillable, BillingRate: rate, BillingAmount: amount,
		}
		tot.hours = tot.hours.Add(hours)
		if e.IsBillable {
			tot.billableHours = tot.billableHours.Add(hours)
			tot.billableAmount = tot.billableAmount.Add(amount)
		}
	}
	return out, tot, nil
}

// ---- load / DB helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*Timesheet, error) {
	var (
		ts                                       Timesheet
		employee, customer, remarks              *string
		submittedAt, cancelledAt                 *time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, employee_id, start_date, end_date,
		       total_hours, total_billable_hours, total_billable_amount, total_billed_amount,
		       status, customer_id, remarks,
		       docstatus, submitted_at, cancelled_at,
		       created_at, updated_at
		FROM timesheet WHERE id = $1`, id).
		Scan(&ts.ID, &ts.Name, &ts.CompanyID, &employee, &ts.StartDate, &ts.EndDate,
			&ts.TotalHours, &ts.TotalBillableHours, &ts.TotalBillableAmount, &ts.TotalBilledAmount,
			&ts.Status, &customer, &remarks,
			&ts.Docstatus, &submittedAt, &cancelledAt,
			&ts.CreatedAt, &ts.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("timesheet %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if employee != nil {
		ts.EmployeeID = *employee
	}
	if customer != nil {
		ts.CustomerID = *customer
	}
	if remarks != nil {
		ts.Remarks = *remarks
	}
	ts.SubmittedAt = submittedAt
	ts.CancelledAt = cancelledAt

	rows, err := tx.Query(ctx, `
		SELECT id, row_index, coalesce(activity_type_id,''), coalesce(project_id,''), coalesce(task_id,''),
		       coalesce(description,''), from_time, to_time, hours,
		       is_billable, billing_rate, billing_amount, coalesce(sales_invoice_item_id,'')
		FROM timesheet_entry WHERE timesheet_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e TimesheetEntry
		if err := rows.Scan(&e.ID, &e.RowIndex, &e.ActivityTypeID, &e.ProjectID, &e.TaskID,
			&e.Description, &e.FromTime, &e.ToTime, &e.Hours,
			&e.IsBillable, &e.BillingRate, &e.BillingAmount, &e.SalesInvoiceItemID); err != nil {
			return nil, err
		}
		ts.Entries = append(ts.Entries, e)
	}
	return &ts, nil
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
