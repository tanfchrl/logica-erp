// Package task is the lightweight todo doctype. Tasks can hang off any
// record (Customer / Opportunity / Asset / etc.) via (parent_doctype,
// parent_id), or they can stay attached to a project via project_id.
// Either form works — the parent fields are independently nullable.
//
// Single status machine: Open → Working → Completed (Cancelled is
// terminal). Complete + Reopen are convenience shortcuts.
package task

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

const Doctype = "task"

// Status values stored on task.status.
const (
	StatusOpen      = "Open"
	StatusWorking   = "Working"
	StatusCompleted = "Completed"
	StatusCancelled = "Cancelled"
)

// parentAllowlist is the set of doctypes a task can attach to via the
// dynamic link. project_id stays as the original path; parent_doctype
// must be one of these when set.
var parentAllowlist = map[string]bool{
	"customer":     true,
	"supplier":     true,
	"lead":         true,
	"contact":      true,
	"opportunity":  true,
	"asset":        true,
	"purchase_order":   true,
	"sales_invoice":    true,
	"purchase_invoice": true,
}

type Task struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Subject       string     `json:"subject"`
	ProjectID     string     `json:"project_id,omitempty"`
	ParentDoctype string     `json:"parent_doctype,omitempty"`
	ParentID      string     `json:"parent_id,omitempty"`
	AssignedTo    string     `json:"assigned_to,omitempty"`
	Status        string     `json:"status"`
	Priority      string     `json:"priority"`
	DueDate       *time.Time `json:"due_date,omitempty"`
	Description   string     `json:"description,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CreatedBy     string     `json:"created_by"`
}

type TaskCreateInput struct {
	Subject       string `json:"subject"`
	ProjectID     string `json:"project_id,omitempty"`
	ParentDoctype string `json:"parent_doctype,omitempty"`
	ParentID      string `json:"parent_id,omitempty"`
	AssignedTo    string `json:"assigned_to,omitempty"`
	Priority      string `json:"priority,omitempty"`
	DueDate       string `json:"due_date,omitempty"`
	Description   string `json:"description,omitempty"`
}

type TaskUpdateInput struct {
	Subject     string `json:"subject"`
	AssignedTo  string `json:"assigned_to,omitempty"`
	Priority    string `json:"priority,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- CRUD ----

func (s *Service) Create(ctx context.Context, in TaskCreateInput) (*Task, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("task: unauthenticated")
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, errors.New("task.subject: required")
	}
	// At least one parent: either project or (parent_doctype + parent_id).
	if in.ProjectID == "" && (in.ParentDoctype == "" || in.ParentID == "") {
		return nil, errors.New("task: project_id or (parent_doctype + parent_id) required")
	}
	if in.ParentDoctype != "" {
		if !parentAllowlist[in.ParentDoctype] {
			return nil, fmt.Errorf("task.parent_doctype: %q not allowed", in.ParentDoctype)
		}
		if in.ParentID == "" {
			return nil, errors.New("task.parent_id: required when parent_doctype is set")
		}
	}
	priority := in.Priority
	if priority == "" {
		priority = "Medium"
	}
	var due *time.Time
	if in.DueDate != "" {
		t, err := time.Parse("2006-01-02", in.DueDate)
		if err != nil {
			return nil, fmt.Errorf("task.due_date: %w", err)
		}
		due = &t
	}

	id := dbx.NewIDWithPrefix("task")
	var out Task
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// task.name is UNIQUE — generate via the existing naming series.
		seriesID, pattern, err := pickSeries(ctx, tx, Doctype, "")
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, time.Now().UTC(), nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task (
				id, name, subject, project_id, parent_doctype, parent_id,
				assigned_to, priority, due_date, description,
				created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
			id, name, in.Subject,
			nullable(in.ProjectID), nullable(in.ParentDoctype), nullable(in.ParentID),
			nullable(in.AssignedTo), priority, nullableTime(due), nullable(in.Description),
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

func (s *Service) Update(ctx context.Context, id string, in TaskUpdateInput) (*Task, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("task: unauthenticated")
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, errors.New("task.subject: required")
	}
	if in.Status != "" {
		switch in.Status {
		case StatusOpen, StatusWorking, StatusCompleted, StatusCancelled:
		default:
			return nil, fmt.Errorf("task.status: invalid %q", in.Status)
		}
	}
	var due *time.Time
	if in.DueDate != "" {
		t, err := time.Parse("2006-01-02", in.DueDate)
		if err != nil {
			return nil, fmt.Errorf("task.due_date: %w", err)
		}
		due = &t
	}
	var out Task
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		existing, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		newStatus := in.Status
		if newStatus == "" {
			newStatus = existing.Status
		}
		newPriority := in.Priority
		if newPriority == "" {
			newPriority = existing.Priority
		}
		tag, err := tx.Exec(ctx, `
			UPDATE task SET
			  subject = $1, assigned_to = $2, priority = $3, due_date = $4,
			  description = $5, status = $6,
			  updated_by = $7
			WHERE id = $8 AND is_deleted = false`,
			in.Subject, nullable(in.AssignedTo), newPriority, nullableTime(due),
			nullable(in.Description), newStatus, p.UserID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("task %s not found", id)
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

// Complete is a one-click shortcut — sets status=Completed without making
// the caller send subject/etc. Mirror of "tick the checkbox".
func (s *Service) Complete(ctx context.Context, id string) (*Task, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("task: unauthenticated")
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE task SET status = $1, act_end_date = current_date, updated_by = $2
		WHERE id = $3 AND is_deleted = false`,
		StatusCompleted, p.UserID, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Reopen(ctx context.Context, id string) (*Task, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("task: unauthenticated")
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE task SET status = $1, act_end_date = NULL, updated_by = $2
		WHERE id = $3 AND is_deleted = false`,
		StatusOpen, p.UserID, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("task: unauthenticated")
	}
	return s.db.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE task SET is_deleted = true, updated_by = $1 WHERE id = $2 AND is_deleted = false`,
			p.UserID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("task %s not found", id)
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionDelete, audit.Diff{})
	})
}

func (s *Service) Get(ctx context.Context, id string) (*Task, error) {
	var out *Task
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		t, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

// List returns tasks. Filter by parent (parent_doctype + parent_id), or
// by project_id, or — with neither — return everything the caller can see.
// Most-recent first, capped at 200.
func (s *Service) List(ctx context.Context, parentDoctype, parentID, projectID, assignedTo string) ([]Task, error) {
	args := []any{}
	q := `SELECT id FROM task WHERE is_deleted = false`
	if parentDoctype != "" && parentID != "" {
		args = append(args, parentDoctype, parentID)
		q += fmt.Sprintf(` AND parent_doctype = $%d AND parent_id = $%d`, len(args)-1, len(args))
	}
	if projectID != "" {
		args = append(args, projectID)
		q += fmt.Sprintf(` AND project_id = $%d`, len(args))
	}
	if assignedTo != "" {
		args = append(args, assignedTo)
		q += fmt.Sprintf(` AND assigned_to = $%d`, len(args))
	}
	q += ` ORDER BY due_date NULLS LAST, created_at DESC LIMIT 200`
	rows, err := s.db.Query(ctx, q, args...)
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
	out := make([]Task, 0, len(ids))
	for _, id := range ids {
		t, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

// ---- helpers ----

func load(ctx context.Context, tx pgx.Tx, id string) (*Task, error) {
	var (
		t                                                     Task
		projectID, parentDoctype, parentID, assignedTo, desc *string
		due                                                   *time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, subject, project_id, parent_doctype, parent_id,
		       assigned_to, status, priority, due_date, description,
		       created_at, updated_at, created_by
		FROM task WHERE id = $1`, id).
		Scan(&t.ID, &t.Name, &t.Subject, &projectID, &parentDoctype, &parentID,
			&assignedTo, &t.Status, &t.Priority, &due, &desc,
			&t.CreatedAt, &t.UpdatedAt, &t.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("task %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if projectID != nil {
		t.ProjectID = *projectID
	}
	if parentDoctype != nil {
		t.ParentDoctype = *parentDoctype
	}
	if parentID != nil {
		t.ParentID = *parentID
	}
	if assignedTo != nil {
		t.AssignedTo = *assignedTo
	}
	if desc != nil {
		t.Description = *desc
	}
	t.DueDate = due
	return &t, nil
}

func pickSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `
		SELECT id, pattern FROM naming_series
		WHERE doctype = $1 AND is_default = true AND (company_id = $2 OR company_id IS NULL)
		ORDER BY company_id NULLS LAST LIMIT 1`, doctype, nullable(companyID)).Scan(&id, &pat)
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

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-tasks", Method: http.MethodGet,
		Path: "/projects/tasks", Summary: "List tasks (filter by parent / project / assignee)",
		Tags: []string{"Projects / Task"},
	}, func(ctx context.Context, in *taskListIn) (*taskListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ts, err := h.Service.List(ctx, in.ParentDoctype, in.ParentID, in.ProjectID, in.AssignedTo)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &taskListOut{Body: taskListBody{Items: ts}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-task", Method: http.MethodPost,
		Path: "/projects/tasks", Summary: "Create a task",
		Tags: []string{"Projects / Task"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *taskCreateIn) (*taskOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &taskOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-task", Method: http.MethodGet,
		Path: "/projects/tasks/{id}", Summary: "Get a task",
		Tags: []string{"Projects / Task"},
	}, func(ctx context.Context, in *taskGetIn) (*taskOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &taskOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-task", Method: http.MethodPut,
		Path: "/projects/tasks/{id}", Summary: "Update a task",
		Tags: []string{"Projects / Task"},
	}, func(ctx context.Context, in *taskUpdateIn) (*taskOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &taskOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "complete-task", Method: http.MethodPost,
		Path: "/projects/tasks/{id}/complete", Summary: "Mark a task completed",
		Tags: []string{"Projects / Task"},
	}, func(ctx context.Context, in *taskGetIn) (*taskOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.Complete(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &taskOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "reopen-task", Method: http.MethodPost,
		Path: "/projects/tasks/{id}/reopen", Summary: "Reopen a completed task",
		Tags: []string{"Projects / Task"},
	}, func(ctx context.Context, in *taskGetIn) (*taskOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.Reopen(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &taskOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-task", Method: http.MethodDelete,
		Path: "/projects/tasks/{id}", Summary: "Soft-delete a task",
		Tags: []string{"Projects / Task"},
	}, func(ctx context.Context, in *taskGetIn) (*struct{ Body map[string]string }, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Delete(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

type (
	taskCreateIn struct{ Body TaskCreateInput }
	taskUpdateIn struct {
		ID   string `path:"id"`
		Body TaskUpdateInput
	}
	taskGetIn struct {
		ID string `path:"id"`
	}
	taskListIn struct {
		ParentDoctype string `query:"parent_doctype"`
		ParentID      string `query:"parent_id"`
		ProjectID     string `query:"project_id"`
		AssignedTo    string `query:"assigned_to"`
	}
	taskOut     struct{ Body Task }
	taskListOut struct{ Body taskListBody }
	taskListBody struct {
		Items []Task `json:"items"`
	}
)
