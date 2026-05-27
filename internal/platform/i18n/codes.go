// Package i18n holds the stable error codes returned by the API.
// Codes are language-agnostic; the frontend looks them up against translation tables.
package i18n

const (
	CodeUnauthenticated    = "unauthenticated"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeValidationFailed   = "validation_failed"
	CodeMissingCompany     = "missing_company"
	CodeBadCredentials     = "bad_credentials"
	CodeAccountDisabled    = "account_disabled"
	CodeRefreshInvalid     = "refresh_invalid"
	CodeRefreshReplay      = "refresh_replay"
	CodeLedgerImbalanced   = "ledger_imbalanced"
	CodeDuplicate          = "duplicate"
	CodeConflict           = "conflict"
	CodeInternal           = "internal"
)
