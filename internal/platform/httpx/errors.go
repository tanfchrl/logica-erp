// Package httpx wires the HTTP layer: Huma setup, error mapping, request id, auth + company middleware.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/i18n"
	"github.com/tandigital/logica-erp/internal/platform/ledger"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// Error builds a Huma error with our stable code embedded. The frontend reads .code to localize.
func Error(status int, code, msg string, fields map[string]string) huma.StatusError {
	detail := map[string]any{"code": code}
	if len(fields) > 0 {
		detail["fields"] = fields
	}
	return huma.NewError(status, msg, &huma.ErrorDetail{Location: "code", Message: code, Value: detail})
}

// MapError translates package-level errors into Huma errors with stable codes.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, auth.ErrInvalidRefresh):
		return Error(http.StatusUnauthorized, i18n.CodeRefreshInvalid, "invalid or expired refresh token", nil)
	case errors.Is(err, auth.ErrRefreshReplay):
		return Error(http.StatusUnauthorized, i18n.CodeRefreshReplay, "refresh token replay detected; all sessions revoked", nil)
	case errors.Is(err, permission.ErrUnauthenticated):
		return Error(http.StatusUnauthorized, i18n.CodeUnauthenticated, "not authenticated", nil)
	case errors.Is(err, permission.ErrForbidden):
		return Error(http.StatusForbidden, i18n.CodeForbidden, err.Error(), nil)
	case errors.Is(err, ledger.ErrImbalanced):
		return Error(http.StatusUnprocessableEntity, i18n.CodeLedgerImbalanced, err.Error(), nil)
	}
	// Unmapped error: log it (for ops) and return the message in the response (developer-friendly during build phase).
	slog.Error("httpx: unmapped error", "err", err.Error())
	return huma.NewError(http.StatusInternalServerError, err.Error())
}
