// Package workflow implements a small admin-configurable state machine that
// layers on top of the submittable lifecycle. Each doctype can have one
// workflow with named states and transitions guarded by roles.
//
// API:
//   engine.Apply(ctx, tx, doctype, doc_id, currentState, action) (newState, error)
//
// Doctype handlers call Apply inside their own transaction; the engine doesn't
// touch the document table directly — it just decides if the transition is
// legal and returns the new state name for the caller to write.
package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type Workflow struct {
	ID         string
	Doctype    string
	StateField string
	IsActive   bool
}

type State struct {
	Name       string
	DocStatus  int16
	IsInitial  bool
	IsTerminal bool
}

type Transition struct {
	FromState      string
	ToState        string
	Action         string
	AllowedRoleID  string // "" = anyone with write
}

var (
	ErrNoWorkflow       = errors.New("workflow: no active workflow for doctype")
	ErrInvalidAction    = errors.New("workflow: action not allowed from current state")
	ErrRoleNotPermitted = errors.New("workflow: caller role not permitted for this action")
)

type Engine struct {
	db *dbx.DB
}

func NewEngine(db *dbx.DB) *Engine { return &Engine{db: db} }

// CheckSubmitRole gates submit by role when a workflow exists for the doctype.
// It looks up the workflow's "submit" transition out of the initial state and
// verifies the caller holds the transition's allowed_role_id. If no workflow
// exists for the doctype, returns nil (no-op).
//
// This is the minimum-viable wire-in for the workflow runtime — it doesn't yet
// drive state transitions on the document (that needs a per-doctype `state`
// column). Use this in service.Submit() AFTER the existing approval gate.
func (e *Engine) CheckSubmitRole(ctx context.Context, tx pgx.Tx, doctype string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("workflow: unauthenticated")
	}
	if p.IsSystem {
		return nil
	}
	var (
		wfID    string
		initial string
	)
	err := tx.QueryRow(ctx, `
		SELECT w.id, coalesce(s.name,'')
		FROM workflow w
		LEFT JOIN workflow_state s ON s.workflow_id = w.id AND s.is_initial = true
		WHERE w.doctype = $1 AND w.is_active = true
		LIMIT 1`, doctype).Scan(&wfID, &initial)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no workflow for this doctype → permissive
	}
	if err != nil {
		return err
	}
	if initial == "" {
		return nil // workflow defined but missing initial state — don't block submit
	}
	// Find a transition with action='submit' from initial state. If absent,
	// the workflow doesn't restrict who can submit (only its other actions
	// are role-gated).
	var allowedRoleID *string
	err = tx.QueryRow(ctx, `
		SELECT allowed_role_id
		FROM workflow_transition
		WHERE workflow_id = $1 AND from_state = $2 AND action = 'submit'`,
		wfID, initial).Scan(&allowedRoleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if allowedRoleID == nil || *allowedRoleID == "" {
		return nil
	}
	if !contains(p.Roles, *allowedRoleID) {
		return fmt.Errorf("%w: workflow requires role %s to submit %s", ErrRoleNotPermitted, *allowedRoleID, doctype)
	}
	return nil
}

// Apply validates and returns the new state name. Inserts an audit row using the supplied tx.
func (e *Engine) Apply(ctx context.Context, tx pgx.Tx, doctype, currentState, action string) (newState string, docStatus int16, err error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return "", 0, errors.New("workflow: unauthenticated")
	}
	var wfID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM workflow WHERE doctype = $1 AND is_active = true`, doctype).Scan(&wfID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrNoWorkflow
		}
		return "", 0, err
	}
	var (
		toState       string
		allowedRoleID *string
	)
	if err := tx.QueryRow(ctx, `
		SELECT to_state, allowed_role_id
		FROM workflow_transition
		WHERE workflow_id = $1 AND from_state = $2 AND action = $3`,
		wfID, currentState, action).Scan(&toState, &allowedRoleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, fmt.Errorf("%w: %s -[%s]-> ?", ErrInvalidAction, currentState, action)
		}
		return "", 0, err
	}
	if allowedRoleID != nil && *allowedRoleID != "" && !contains(p.Roles, *allowedRoleID) && !p.IsSystem {
		return "", 0, ErrRoleNotPermitted
	}
	// Resolve docstatus of the new state.
	var ds int16
	if err := tx.QueryRow(ctx,
		`SELECT doc_status FROM workflow_state WHERE workflow_id = $1 AND name = $2`,
		wfID, toState).Scan(&ds); err != nil {
		return "", 0, fmt.Errorf("workflow: target state %q not defined", toState)
	}
	return toState, ds, nil
}

// Configure (admin): create/replace a workflow + its states + transitions in one shot.
type Config struct {
	Name        string       `json:"name"`
	Doctype     string       `json:"doctype"`
	StateField  string       `json:"state_field,omitempty"`
	States      []State      `json:"states"`
	Transitions []Transition `json:"transitions"`
}

func (e *Engine) Configure(ctx context.Context, cfg Config) (string, error) {
	if cfg.Doctype == "" || cfg.Name == "" || len(cfg.States) == 0 {
		return "", errors.New("workflow: name/doctype/states required")
	}
	if cfg.StateField == "" {
		cfg.StateField = "status"
	}
	wfID := dbx.NewIDWithPrefix("wf")
	err := e.db.Tx(ctx, func(tx pgx.Tx) error {
		// Replace any prior workflow for this doctype.
		if _, err := tx.Exec(ctx, `DELETE FROM workflow WHERE doctype = $1`, cfg.Doctype); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO workflow (id, name, doctype, state_field) VALUES ($1,$2,$3,$4)`,
			wfID, cfg.Name, cfg.Doctype, cfg.StateField); err != nil {
			return err
		}
		for _, st := range cfg.States {
			if _, err := tx.Exec(ctx, `
				INSERT INTO workflow_state (id, workflow_id, name, doc_status, is_initial, is_terminal)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				dbx.NewIDWithPrefix("wfs"), wfID, st.Name, st.DocStatus, st.IsInitial, st.IsTerminal); err != nil {
				return err
			}
		}
		for _, tr := range cfg.Transitions {
			role := any(nil)
			if tr.AllowedRoleID != "" {
				role = tr.AllowedRoleID
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO workflow_transition (id, workflow_id, from_state, to_state, action, allowed_role_id)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				dbx.NewIDWithPrefix("wft"), wfID, tr.FromState, tr.ToState, tr.Action, role); err != nil {
				return err
			}
		}
		return nil
	})
	return wfID, err
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
