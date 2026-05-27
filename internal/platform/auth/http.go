package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type Handler struct {
	DB                  *dbx.DB
	Signer              *Signer
	RefreshTTL          time.Duration
	CookieDomain        string
	CookieSecure        bool
}

const refreshCookieName = "logica_refresh"

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-login",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "Log in with email and password",
		Tags:        []string{"Auth"},
	}, func(ctx context.Context, in *loginIn) (*loginOut, error) {
		ua, ip := metaFrom(in)
		userID, companies, roles, err := authenticate(ctx, h.DB, in.Body.Email, in.Body.Password, ip)
		if err != nil {
			if errors.Is(err, ErrIPNotAllowed) {
				return nil, huma.NewError(http.StatusForbidden, "ip_not_allowed")
			}
			return nil, huma.NewError(http.StatusUnauthorized, "bad_credentials")
		}
		tok, err := h.Signer.Issue(userID, companies, roles)
		if err != nil {
			return nil, err
		}
		rt, err := IssueRefresh(ctx, h.DB, userID, "", ua, ip, h.RefreshTTL)
		if err != nil {
			return nil, err
		}
		out := &loginOut{Body: loginBody{
			AccessToken: tok,
			TokenType:   "Bearer",
			ExpiresIn:   int(h.Signer.ttl.Seconds()),
			Companies:   companies,
			Roles:       roles,
		}}
		out.SetCookie = h.cookie(rt.Token, rt.ExpiresAt)
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "auth-refresh",
		Method:      http.MethodPost,
		Path:        "/auth/refresh",
		Summary:     "Rotate the access token using the refresh cookie",
		Tags:        []string{"Auth"},
	}, func(ctx context.Context, in *refreshIn) (*loginOut, error) {
		raw := readRefreshCookie(in.Cookie)
		if raw == "" {
			return nil, huma.NewError(http.StatusUnauthorized, "refresh_invalid")
		}
		userID, sessionID, err := ValidateRefresh(ctx, h.DB, raw)
		if err != nil {
			if errors.Is(err, ErrRefreshReplay) {
				return nil, huma.NewError(http.StatusUnauthorized, "refresh_replay")
			}
			return nil, huma.NewError(http.StatusUnauthorized, "refresh_invalid")
		}
		companies, roles, err := loadCompaniesRoles(ctx, h.DB, userID)
		if err != nil {
			return nil, err
		}
		tok, err := h.Signer.Issue(userID, companies, roles)
		if err != nil {
			return nil, err
		}
		ua, ip := metaFrom(in)
		rt, err := IssueRefresh(ctx, h.DB, userID, sessionID, ua, ip, h.RefreshTTL)
		if err != nil {
			return nil, err
		}
		out := &loginOut{Body: loginBody{
			AccessToken: tok,
			TokenType:   "Bearer",
			ExpiresIn:   int(h.Signer.ttl.Seconds()),
			Companies:   companies,
			Roles:       roles,
		}}
		out.SetCookie = h.cookie(rt.Token, rt.ExpiresAt)
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "auth-logout",
		Method:      http.MethodPost,
		Path:        "/auth/logout",
		Summary:     "Revoke the current refresh token",
		Tags:        []string{"Auth"},
	}, func(ctx context.Context, in *refreshIn) (*logoutOut, error) {
		raw := readRefreshCookie(in.Cookie)
		if raw != "" {
			if _, sessionID, err := ValidateRefresh(ctx, h.DB, raw); err == nil {
				_ = RevokeSession(ctx, h.DB, sessionID)
			}
		}
		out := &logoutOut{}
		out.SetCookie = h.cookie("", time.Unix(0, 0))
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "auth-me",
		Method:      http.MethodGet,
		Path:        "/auth/me",
		Summary:     "Return the current user (requires Bearer)",
		Tags:        []string{"Auth"},
	}, func(ctx context.Context, _ *struct{}) (*meOut, error) {
		p := FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		var email, fullName, locale string
		err := h.DB.QueryRow(ctx, `SELECT email, full_name, locale FROM users WHERE id = $1`, p.UserID).Scan(&email, &fullName, &locale)
		if err != nil {
			return nil, err
		}
		return &meOut{Body: meBody{
			ID: p.UserID, Email: email, FullName: fullName, Locale: locale,
			Companies: p.Companies, Roles: p.Roles, IsSystem: p.IsSystem,
		}}, nil
	})
}

// --- helpers ---

// ErrIPNotAllowed — login source IP is outside the user's configured allowlist.
var ErrIPNotAllowed = errors.New("auth: source ip not in user allowlist")

func authenticate(ctx context.Context, db *dbx.DB, email, password string, sourceIP net.IP) (string, []string, []string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var (
		id, hash   string
		enabled    bool
		allowlist  []*net.IPNet
	)
	// Use textual representation for CIDR scan; pgx returns *net.IPNet but the
	// cidr[] type marshalling differs by driver version. Use string array + Parse.
	var rawAllowlist []string
	err := db.QueryRow(ctx, `
		SELECT id, password_hash, enabled, coalesce(array(SELECT host(c) || '/' || masklen(c) FROM unnest(ip_allowlist) c), '{}')
		FROM users WHERE email = $1`, email).Scan(&id, &hash, &enabled, &rawAllowlist)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, nil, ErrInvalidRefresh
		}
		return "", nil, nil, err
	}
	if !enabled {
		return "", nil, nil, errors.New("account_disabled")
	}
	ok, err := VerifyPassword(password, hash)
	if err != nil || !ok {
		return "", nil, nil, errors.New("bad_credentials")
	}

	// Parse allowlist + enforce. Empty list = no restriction.
	for _, s := range rawAllowlist {
		if _, n, err := net.ParseCIDR(s); err == nil {
			allowlist = append(allowlist, n)
		}
	}
	if len(allowlist) > 0 {
		if sourceIP == nil {
			return "", nil, nil, ErrIPNotAllowed
		}
		matched := false
		for _, n := range allowlist {
			if n.Contains(sourceIP) {
				matched = true
				break
			}
		}
		if !matched {
			return "", nil, nil, ErrIPNotAllowed
		}
	}

	companies, roles, err := loadCompaniesRoles(ctx, db, id)
	return id, companies, roles, err
}

func loadCompaniesRoles(ctx context.Context, db *dbx.DB, userID string) ([]string, []string, error) {
	var companies, roles []string
	rows, err := db.Query(ctx, `SELECT company_id FROM user_company WHERE user_id = $1`, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, nil, err
		}
		companies = append(companies, c)
	}
	rows.Close()
	rows, err = db.Query(ctx, `SELECT role_id FROM user_role WHERE user_id = $1`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, nil, err
		}
		roles = append(roles, r)
	}
	return companies, roles, nil
}

func (h *Handler) cookie(value string, exp time.Time) http.Cookie {
	c := http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
	}
	if h.CookieDomain != "" {
		c.Domain = h.CookieDomain
	}
	if value == "" {
		c.MaxAge = -1
	}
	return c
}

func readRefreshCookie(header string) string {
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ";") {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, refreshCookieName+"=") {
			return strings.TrimPrefix(p, refreshCookieName+"=")
		}
	}
	return ""
}

func metaFrom(in any) (string, net.IP) {
	type meta interface {
		UserAgent() string
		RemoteIP() net.IP
	}
	if m, ok := in.(meta); ok {
		return m.UserAgent(), m.RemoteIP()
	}
	return "", nil
}

// ---- HTTP types ----

type loginIn struct {
	Body loginReq
}
type loginReq struct {
	Email    string `json:"email"    doc:"email" required:"true"`
	Password string `json:"password" doc:"password" required:"true" minLength:"8"`
}
type loginOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      loginBody
}
type loginBody struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int      `json:"expires_in"`
	Companies   []string `json:"companies"`
	Roles       []string `json:"roles"`
}
type refreshIn struct {
	Cookie string `header:"Cookie"`
}
type logoutOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}
type meOut struct {
	Body meBody
}
type meBody struct {
	ID        string   `json:"id"`
	Email     string   `json:"email"`
	FullName  string   `json:"full_name"`
	Locale    string   `json:"locale"`
	Companies []string `json:"companies"`
	Roles     []string `json:"roles"`
	IsSystem  bool     `json:"is_system"`
}
