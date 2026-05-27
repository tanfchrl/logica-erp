package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const (
	HeaderAuth      = "Authorization"
	HeaderCompany   = "X-Company-Id"
	HeaderRequestID = "X-Request-Id"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxBearer
)

// BearerFromContext returns the raw bearer token (no "Bearer " prefix) that
// the Auth middleware stashed on the request context. Used by the agent
// service to forward the acting user's JWT to ERP API calls — the agent
// never holds its own credential.
func BearerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxBearer).(string)
	return v
}

// RequestID middleware sets X-Request-Id (in/out) and puts it on the context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AccessLog logs structured request lines.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.status),
				slog.Duration("dur", time.Since(start)),
				slog.String("request_id", reqIDFrom(r.Context())),
			)
		})
	}
}

// Auth wraps a handler with bearer-token validation. Public paths are matched by prefix.
func Auth(db *dbx.DB, signer *auth.Signer, publicPrefixes []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r.URL.Path, publicPrefixes) {
				next.ServeHTTP(w, r)
				return
			}
			h := r.Header.Get(HeaderAuth)
			if !strings.HasPrefix(h, "Bearer ") {
				writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "missing bearer token")
				return
			}
			tok := strings.TrimPrefix(h, "Bearer ")

			// Two-track validation: API tokens get the lt_ prefix; everything
			// else is treated as a JWT.
			var (
				userID      string
				tokenScopes []string
			)
			if strings.HasPrefix(tok, "lt_") {
				uid, scopes, err := verifyAPIToken(r.Context(), db, tok)
				if err != nil {
					writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired api token")
					return
				}
				userID = uid
				tokenScopes = scopes
			} else {
				claims, err := signer.Verify(tok)
				if err != nil {
					writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "invalid token")
					return
				}
				userID = claims.Subject
			}

			p, err := permission.LoadPrincipal(r.Context(), db, userID)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "user no longer valid")
				return
			}
			// Attach token scopes (nil for JWT path = full access; ["*"] from the
			// token also means no restriction — we normalise both to nil).
			if len(tokenScopes) > 0 && !(len(tokenScopes) == 1 && tokenScopes[0] == "*") {
				p.Scopes = tokenScopes
			}
			ctx := auth.WithPrincipal(r.Context(), p)
			// Stash the raw bearer token so downstream handlers (notably the
			// agent service's tool dispatcher) can forward it to the ERP API.
			ctx = context.WithValue(ctx, ctxBearer, tok)

			if companyID := r.Header.Get(HeaderCompany); companyID != "" {
				if !containsString(p.Companies, companyID) {
					writeJSONError(w, http.StatusForbidden, "forbidden", "no access to company")
					return
				}
				ctx = auth.WithCompany(ctx, companyID)
			} else if len(p.Companies) == 1 {
				ctx = auth.WithCompany(ctx, p.Companies[0])
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isPublic(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func reqIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRequestID).(string); ok {
		return v
	}
	return ""
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}

// verifyAPIToken validates an "lt_<hex>" personal-access token against api_token.
// Returns the owning user id on success. Side-effect: updates last_used_at as a
// fire-and-forget so request latency isn't dragged by it.
//
// Auth rejection happens if: token unknown, revoked, expired, or the owning
// user is disabled. permission.LoadPrincipal (called by the caller) is the
// final gate on the user being usable.
func verifyAPIToken(ctx context.Context, db *dbx.DB, plaintext string) (string, []string, error) {
	sum := sha256.Sum256([]byte(plaintext))
	hashHex := hex.EncodeToString(sum[:])

	var (
		id     string
		userID string
		scopes []string
	)
	err := db.QueryRow(ctx, `
		SELECT t.id, t.user_id, t.scopes
		FROM api_token t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
		  AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > now())
		  AND u.enabled = true`, hashHex).Scan(&id, &userID, &scopes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, errors.New("api_token: not found or expired")
		}
		return "", nil, err
	}

	// Fire-and-forget last-used touch. Don't block the request; if it fails the
	// audit value of last_used_at degrades but auth still succeeded.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(bgCtx, `UPDATE api_token SET last_used_at = now() WHERE id = $1`, id)
	}()

	return userID, scopes, nil
}
