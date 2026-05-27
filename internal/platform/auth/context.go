package auth

import "context"

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxCompany
)

// Principal is the authenticated caller.
type Principal struct {
	UserID    string
	Companies []string
	Roles     []string
	Locale    string
	IsSystem  bool
}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxUser, p)
}

// FromContext returns the principal previously set by middleware; nil if absent.
func FromContext(ctx context.Context) *Principal {
	if v, ok := ctx.Value(ctxUser).(*Principal); ok {
		return v
	}
	return nil
}

func WithCompany(ctx context.Context, companyID string) context.Context {
	return context.WithValue(ctx, ctxCompany, companyID)
}

// CompanyFromContext returns the active company id set by middleware; "" if absent.
func CompanyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxCompany).(string); ok {
		return v
	}
	return ""
}
