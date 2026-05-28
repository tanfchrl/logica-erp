// Package workflow exposes admin CRUD over the workflow / workflow_state /
// workflow_transition tables, plus the approval_rule definitions, and the
// runtime Engine that gates document submit on role. Engine.CheckSubmitRole
// is wired into every transactional doctype's Submit() (SI, PI, PE, JE, SO,
// PO, MR, PR, Stock Entry, BOM, Work Order, Asset, Payroll Entry, Period
// Closing Voucher, Timesheet).
package workflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const (
	DoctypeWorkflow     = "workflow"
	DoctypeApprovalRule = "approval_rule"
)

// SupportedDoctypes lists the doctypes a workflow / approval rule can target.
// Mirrors the submittable doctypes — the union of all doctypes that have a
// submit endpoint or a docstatus column.
var SupportedDoctypes = []string{
	"sales_invoice", "purchase_invoice", "payment_entry", "journal_entry",
	"period_closing_voucher", "stock_entry", "purchase_order", "sales_order",
	"bom", "work_order", "asset", "payroll_entry",
}

// ===========================================================================
// Types
// ===========================================================================

// AdminWorkflow / AdminState / AdminTransition are the JSON-tagged DTOs the
// admin API ships over the wire. The engine has its own minimal runtime
// structs in workflow.go.
type AdminWorkflow struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Doctype    string              `json:"doctype"`
	StateField string              `json:"state_field"`
	IsActive   bool                `json:"is_active"`
	States     []AdminState        `json:"states"`
	Transitions []AdminTransition  `json:"transitions"`
	CreatedAt  time.Time           `json:"created_at"`
}

type AdminState struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	Name       string `json:"name"`
	DocStatus  int    `json:"doc_status"`
	IsInitial  bool   `json:"is_initial"`
	IsTerminal bool   `json:"is_terminal"`
}

type AdminTransition struct {
	ID            string `json:"id"`
	WorkflowID    string `json:"workflow_id"`
	FromState     string `json:"from_state"`
	ToState       string `json:"to_state"`
	Action        string `json:"action"`
	AllowedRoleID string `json:"allowed_role_id,omitempty"`
}

type WorkflowInput struct {
	Name       string `json:"name"`
	Doctype    string `json:"doctype"`
	StateField string `json:"state_field,omitempty"`
	IsActive   *bool  `json:"is_active,omitempty"`
}

type StateInput struct {
	Name       string `json:"name"`
	DocStatus  int    `json:"doc_status,omitempty"`
	IsInitial  bool   `json:"is_initial,omitempty"`
	IsTerminal bool   `json:"is_terminal,omitempty"`
}

type TransitionInput struct {
	FromState     string `json:"from_state"`
	ToState       string `json:"to_state"`
	Action        string `json:"action"`
	AllowedRoleID string `json:"allowed_role_id,omitempty"`
}

type ApprovalRule struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Doctype        string    `json:"doctype"`
	CompanyID      string    `json:"company_id,omitempty"`
	ConditionField string    `json:"condition_field,omitempty"`
	ConditionOp    string    `json:"condition_op,omitempty"`
	ConditionValue string    `json:"condition_value,omitempty"`
	RequiredRoleID string    `json:"required_role_id"`
	Sequence       int       `json:"sequence"`
	IsActive       bool      `json:"is_active"`
	Description    string    `json:"description,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ApprovalRuleInput struct {
	Name           string `json:"name"`
	Doctype        string `json:"doctype"`
	CompanyID      string `json:"company_id,omitempty"`
	ConditionField string `json:"condition_field,omitempty"`
	ConditionOp    string `json:"condition_op,omitempty"`
	ConditionValue string `json:"condition_value,omitempty"`
	RequiredRoleID string `json:"required_role_id"`
	Sequence       int    `json:"sequence,omitempty"`
	IsActive       *bool  `json:"is_active,omitempty"`
	Description    string `json:"description,omitempty"`
}

// ===========================================================================
// Service
// ===========================================================================

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- Workflows ----

func (s *Service) ListWorkflows(ctx context.Context) ([]AdminWorkflow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, doctype, state_field, is_active, created_at
		FROM workflow ORDER BY doctype`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AdminWorkflow, 0)
	for rows.Next() {
		var w AdminWorkflow
		if err := rows.Scan(&w.ID, &w.Name, &w.Doctype, &w.StateField, &w.IsActive, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Hydrate states + transitions for each.
	for i := range out {
		states, err := s.listStates(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].States = states
		trans, err := s.listTransitions(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Transitions = trans
	}
	return out, nil
}

func (s *Service) GetWorkflow(ctx context.Context, id string) (*AdminWorkflow, error) {
	var w AdminWorkflow
	err := s.db.QueryRow(ctx, `
		SELECT id, name, doctype, state_field, is_active, created_at
		FROM workflow WHERE id = $1`, id).
		Scan(&w.ID, &w.Name, &w.Doctype, &w.StateField, &w.IsActive, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("workflow %s: not found", id)
	}
	if err != nil {
		return nil, err
	}
	w.States, err = s.listStates(ctx, id)
	if err != nil {
		return nil, err
	}
	w.Transitions, err = s.listTransitions(ctx, id)
	return &w, err
}

func (s *Service) CreateWorkflow(ctx context.Context, in WorkflowInput) (*AdminWorkflow, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Doctype = strings.TrimSpace(in.Doctype)
	if in.Name == "" || in.Doctype == "" {
		return nil, errors.New("workflow: name and doctype required")
	}
	if !isSupportedDoctype(in.Doctype) {
		return nil, fmt.Errorf("workflow: doctype %q is not workflow-enabled", in.Doctype)
	}
	stateField := in.StateField
	if stateField == "" {
		stateField = "status"
	}
	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}
	id := dbx.NewIDWithPrefix("wf")
	_, err := s.db.Exec(ctx, `
		INSERT INTO workflow (id, name, doctype, state_field, is_active)
		VALUES ($1,$2,$3,$4,$5)`, id, in.Name, in.Doctype, stateField, active)
	if err != nil {
		if dbx.IsUniqueViolation(err) {
			return nil, errors.New("workflow: doctype already has a workflow (one per doctype)")
		}
		return nil, err
	}
	return s.GetWorkflow(ctx, id)
}

func (s *Service) UpdateWorkflow(ctx context.Context, id string, in WorkflowInput) (*AdminWorkflow, error) {
	cur, err := s.GetWorkflow(ctx, id)
	if err != nil {
		return nil, err
	}
	name := cur.Name
	if in.Name != "" {
		name = in.Name
	}
	stateField := cur.StateField
	if in.StateField != "" {
		stateField = in.StateField
	}
	active := cur.IsActive
	if in.IsActive != nil {
		active = *in.IsActive
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE workflow SET name = $2, state_field = $3, is_active = $4 WHERE id = $1`,
		id, name, stateField, active); err != nil {
		return nil, err
	}
	return s.GetWorkflow(ctx, id)
}

func (s *Service) DeleteWorkflow(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM workflow WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("workflow: not found")
	}
	return nil
}

// ---- States ----

func (s *Service) listStates(ctx context.Context, wfID string) ([]AdminState, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, workflow_id, name, doc_status, is_initial, is_terminal
		FROM workflow_state WHERE workflow_id = $1
		ORDER BY is_initial DESC, doc_status, name`, wfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AdminState, 0)
	for rows.Next() {
		var st AdminState
		if err := rows.Scan(&st.ID, &st.WorkflowID, &st.Name, &st.DocStatus, &st.IsInitial, &st.IsTerminal); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Service) AddState(ctx context.Context, wfID string, in StateInput) (*AdminState, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("state.name: required")
	}
	if in.DocStatus < 0 || in.DocStatus > 2 {
		return nil, errors.New("state.doc_status: must be 0, 1 or 2")
	}
	id := dbx.NewIDWithPrefix("wfs")
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Enforce at most one initial state per workflow.
		if in.IsInitial {
			if _, err := tx.Exec(ctx, `
				UPDATE workflow_state SET is_initial = false WHERE workflow_id = $1`, wfID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO workflow_state (id, workflow_id, name, doc_status, is_initial, is_terminal)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			id, wfID, in.Name, in.DocStatus, in.IsInitial, in.IsTerminal)
		if err != nil && dbx.IsUniqueViolation(err) {
			return errors.New("state: name already exists in this workflow")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &AdminState{ID: id, WorkflowID: wfID, Name: in.Name, DocStatus: in.DocStatus, IsInitial: in.IsInitial, IsTerminal: in.IsTerminal}, nil
}

func (s *Service) DeleteState(ctx context.Context, stateID string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM workflow_state WHERE id = $1`, stateID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("state: not found")
	}
	return nil
}

// ---- Transitions ----

func (s *Service) listTransitions(ctx context.Context, wfID string) ([]AdminTransition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, workflow_id, from_state, to_state, action, coalesce(allowed_role_id,'')
		FROM workflow_transition WHERE workflow_id = $1
		ORDER BY from_state, action`, wfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AdminTransition, 0)
	for rows.Next() {
		var t AdminTransition
		if err := rows.Scan(&t.ID, &t.WorkflowID, &t.FromState, &t.ToState, &t.Action, &t.AllowedRoleID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) AddTransition(ctx context.Context, wfID string, in TransitionInput) (*AdminTransition, error) {
	in.FromState = strings.TrimSpace(in.FromState)
	in.ToState = strings.TrimSpace(in.ToState)
	in.Action = strings.TrimSpace(in.Action)
	if in.FromState == "" || in.ToState == "" || in.Action == "" {
		return nil, errors.New("transition: from_state, to_state, action required")
	}
	id := dbx.NewIDWithPrefix("wft")
	_, err := s.db.Exec(ctx, `
		INSERT INTO workflow_transition (id, workflow_id, from_state, to_state, action, allowed_role_id)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		id, wfID, in.FromState, in.ToState, in.Action, nullable(in.AllowedRoleID))
	if err != nil {
		if dbx.IsUniqueViolation(err) {
			return nil, fmt.Errorf("transition: (%s, %s) already defined", in.FromState, in.Action)
		}
		return nil, err
	}
	return &AdminTransition{ID: id, WorkflowID: wfID, FromState: in.FromState, ToState: in.ToState, Action: in.Action, AllowedRoleID: in.AllowedRoleID}, nil
}

func (s *Service) DeleteTransition(ctx context.Context, transitionID string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM workflow_transition WHERE id = $1`, transitionID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("transition: not found")
	}
	return nil
}

// ---- Approval rules ----

func (s *Service) ListApprovalRules(ctx context.Context) ([]ApprovalRule, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, doctype, coalesce(company_id,''),
		       coalesce(condition_field,''), coalesce(condition_op,''), coalesce(condition_value,''),
		       required_role_id, sequence, is_active, description, updated_at
		FROM approval_rule
		ORDER BY doctype, sequence, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ApprovalRule, 0)
	for rows.Next() {
		var r ApprovalRule
		if err := rows.Scan(&r.ID, &r.Name, &r.Doctype, &r.CompanyID,
			&r.ConditionField, &r.ConditionOp, &r.ConditionValue,
			&r.RequiredRoleID, &r.Sequence, &r.IsActive, &r.Description, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) UpsertApprovalRule(ctx context.Context, id string, in ApprovalRuleInput) (*ApprovalRule, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("approval_rule: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Doctype = strings.TrimSpace(in.Doctype)
	if in.Name == "" || in.Doctype == "" || in.RequiredRoleID == "" {
		return nil, errors.New("approval_rule: name, doctype, required_role_id required")
	}
	if !isSupportedDoctype(in.Doctype) {
		return nil, fmt.Errorf("approval_rule: doctype %q not workflow-enabled", in.Doctype)
	}
	if in.ConditionOp != "" && !validOp(in.ConditionOp) {
		return nil, fmt.Errorf("approval_rule: condition_op %q invalid", in.ConditionOp)
	}
	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}
	seq := in.Sequence
	if seq == 0 {
		seq = 100
	}
	if id == "" {
		id = dbx.NewIDWithPrefix("apr")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO approval_rule (id, name, doctype, company_id,
		                           condition_field, condition_op, condition_value,
		                           required_role_id, sequence, is_active, description,
		                           created_by, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,now())
		ON CONFLICT (id) DO UPDATE SET
		  name = EXCLUDED.name, doctype = EXCLUDED.doctype, company_id = EXCLUDED.company_id,
		  condition_field = EXCLUDED.condition_field, condition_op = EXCLUDED.condition_op,
		  condition_value = EXCLUDED.condition_value,
		  required_role_id = EXCLUDED.required_role_id, sequence = EXCLUDED.sequence,
		  is_active = EXCLUDED.is_active, description = EXCLUDED.description,
		  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		id, in.Name, in.Doctype, nullable(in.CompanyID),
		nullable(in.ConditionField), nullable(in.ConditionOp), nullable(in.ConditionValue),
		in.RequiredRoleID, seq, active, in.Description, p.UserID)
	if err != nil {
		return nil, err
	}
	return s.getApprovalRule(ctx, id)
}

func (s *Service) DeleteApprovalRule(ctx context.Context, id string) error {
	ct, err := s.db.Exec(ctx, `DELETE FROM approval_rule WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("approval_rule: not found")
	}
	return nil
}

func (s *Service) getApprovalRule(ctx context.Context, id string) (*ApprovalRule, error) {
	var r ApprovalRule
	err := s.db.QueryRow(ctx, `
		SELECT id, name, doctype, coalesce(company_id,''),
		       coalesce(condition_field,''), coalesce(condition_op,''), coalesce(condition_value,''),
		       required_role_id, sequence, is_active, description, updated_at
		FROM approval_rule WHERE id = $1`, id).
		Scan(&r.ID, &r.Name, &r.Doctype, &r.CompanyID,
			&r.ConditionField, &r.ConditionOp, &r.ConditionValue,
			&r.RequiredRoleID, &r.Sequence, &r.IsActive, &r.Description, &r.UpdatedAt)
	return &r, err
}

// ---- Helpers ----

func isSupportedDoctype(dt string) bool {
	for _, d := range SupportedDoctypes {
		if d == dt {
			return true
		}
	}
	return false
}

func validOp(op string) bool {
	switch op {
	case "=", "<>", ">", ">=", "<", "<=":
		return true
	}
	return false
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ===========================================================================
// HTTP
// ===========================================================================

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	// ---- Workflows ----
	huma.Register(api, huma.Operation{
		OperationID: "list-workflows", Method: http.MethodGet,
		Path: "/admin/workflows", Summary: "List workflows",
		Tags: []string{"Admin / Workflow"},
	}, func(ctx context.Context, _ *struct{}) (*wfListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ws, err := h.Service.ListWorkflows(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &wfListOut{Body: wfListBody{Items: ws}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-workflow", Method: http.MethodGet,
		Path: "/admin/workflows/{id}", Summary: "Get a workflow with states + transitions",
		Tags: []string{"Admin / Workflow"},
	}, func(ctx context.Context, in *wfByID) (*wfItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.GetWorkflow(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &wfItemOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-workflow", Method: http.MethodPost,
		Path: "/admin/workflows", Summary: "Create a workflow",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *wfCreateIn) (*wfItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.CreateWorkflow(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &wfItemOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-workflow", Method: http.MethodPut,
		Path: "/admin/workflows/{id}", Summary: "Update a workflow",
		Tags: []string{"Admin / Workflow"},
	}, func(ctx context.Context, in *wfUpdateIn) (*wfItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		w, err := h.Service.UpdateWorkflow(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &wfItemOut{Body: *w}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-workflow", Method: http.MethodDelete,
		Path: "/admin/workflows/{id}", Summary: "Delete a workflow",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *wfByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteWorkflow(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// ---- States ----
	huma.Register(api, huma.Operation{
		OperationID: "add-workflow-state", Method: http.MethodPost,
		Path: "/admin/workflows/{id}/states", Summary: "Add a state",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *stateCreateIn) (*stateItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		st, err := h.Service.AddState(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &stateItemOut{Body: *st}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-workflow-state", Method: http.MethodDelete,
		Path: "/admin/workflow-states/{id}", Summary: "Delete a state",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *wfByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteState(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// ---- Transitions ----
	huma.Register(api, huma.Operation{
		OperationID: "add-workflow-transition", Method: http.MethodPost,
		Path: "/admin/workflows/{id}/transitions", Summary: "Add a transition",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *transitionCreateIn) (*transitionItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		t, err := h.Service.AddTransition(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &transitionItemOut{Body: *t}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-workflow-transition", Method: http.MethodDelete,
		Path: "/admin/workflow-transitions/{id}", Summary: "Delete a transition",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *wfByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteTransition(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// ---- Approval rules ----
	huma.Register(api, huma.Operation{
		OperationID: "list-approval-rules", Method: http.MethodGet,
		Path: "/admin/approval-rules", Summary: "List approval rules",
		Tags: []string{"Admin / Workflow"},
	}, func(ctx context.Context, _ *struct{}) (*aprListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeApprovalRule, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		rs, err := h.Service.ListApprovalRules(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &aprListOut{Body: aprListBody{Items: rs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-approval-rule", Method: http.MethodPost,
		Path: "/admin/approval-rules", Summary: "Create an approval rule",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *aprCreateIn) (*aprItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeApprovalRule, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.UpsertApprovalRule(ctx, "", in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &aprItemOut{Body: *r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-approval-rule", Method: http.MethodPut,
		Path: "/admin/approval-rules/{id}", Summary: "Update an approval rule",
		Tags: []string{"Admin / Workflow"},
	}, func(ctx context.Context, in *aprUpdateIn) (*aprItemOut, error) {
		if err := h.Perm.Check(ctx, DoctypeApprovalRule, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.UpsertApprovalRule(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &aprItemOut{Body: *r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-approval-rule", Method: http.MethodDelete,
		Path: "/admin/approval-rules/{id}", Summary: "Delete an approval rule",
		Tags: []string{"Admin / Workflow"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *wfByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, DoctypeApprovalRule, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.DeleteApprovalRule(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	// ---- Catalog ----
	huma.Register(api, huma.Operation{
		OperationID: "list-workflow-doctypes", Method: http.MethodGet,
		Path: "/admin/workflows/doctypes", Summary: "Doctypes a workflow / rule can target",
		Tags: []string{"Admin / Workflow"},
	}, func(ctx context.Context, _ *struct{}) (*wfDtListOut, error) {
		if err := h.Perm.Check(ctx, DoctypeWorkflow, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		return &wfDtListOut{Body: wfDtListBody{Items: SupportedDoctypes}}, nil
	})
}

type (
	wfListOut  struct{ Body wfListBody }
	wfListBody struct {
		Items []AdminWorkflow `json:"items"`
	}
	wfItemOut struct{ Body AdminWorkflow }
	wfByID    struct {
		ID string `path:"id"`
	}
	wfCreateIn struct{ Body WorkflowInput }
	wfUpdateIn struct {
		ID   string `path:"id"`
		Body WorkflowInput
	}

	stateItemOut struct{ Body AdminState }
	stateCreateIn struct {
		ID   string `path:"id"`
		Body StateInput
	}
	transitionItemOut struct{ Body AdminTransition }
	transitionCreateIn struct {
		ID   string `path:"id"`
		Body TransitionInput
	}

	aprListOut  struct{ Body aprListBody }
	aprListBody struct {
		Items []ApprovalRule `json:"items"`
	}
	aprItemOut  struct{ Body ApprovalRule }
	aprCreateIn struct{ Body ApprovalRuleInput }
	aprUpdateIn struct {
		ID   string `path:"id"`
		Body ApprovalRuleInput
	}

	wfDtListOut  struct{ Body wfDtListBody }
	wfDtListBody struct {
		Items []string `json:"items"`
	}
)
