// Package submittable encodes the draft/submit/cancel lifecycle shared by every financial document.
//
// Documents carry docstatus (0 draft, 1 submitted, 2 cancelled). Submit posts to ledgers
// and is irreversible except through Cancel, which posts reversing ledger entries.
// Cancelled documents are not editable; Amend clones to a new draft linked via amended_from.
package submittable

import "errors"

// Status mirrors the database docstatus column.
type Status int16

const (
	Draft     Status = 0
	Submitted Status = 1
	Cancelled Status = 2
)

func (s Status) String() string {
	switch s {
	case Draft:
		return "draft"
	case Submitted:
		return "submitted"
	case Cancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Errors used by lifecycle transitions.
var (
	ErrNotDraft     = errors.New("submittable: not in draft state")
	ErrNotSubmitted = errors.New("submittable: not in submitted state")
	ErrNotCancelled = errors.New("submittable: not in cancelled state")
)
