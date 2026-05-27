// Package audit appends document_audit rows inside the caller's transaction.
package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionSubmit Action = "submit"
	ActionCancel Action = "cancel"
	ActionAmend  Action = "amend"
	ActionDelete Action = "delete"
)

// Diff is the {before, after} payload stored against an audit row.
// Use nil for create (no before) or delete (no after).
type Diff struct {
	Before any `json:"before,omitempty"`
	After  any `json:"after,omitempty"`
}

// Record inserts a document_audit row using the given pgx.Tx.
func Record(ctx context.Context, tx pgx.Tx, doctype, documentID, userID string, action Action, diff Diff) error {
	payload, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO document_audit (id, doctype, document_id, action, changed_by, diff)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		dbx.NewIDWithPrefix("aud"), doctype, documentID, string(action), userID, payload)
	return err
}
