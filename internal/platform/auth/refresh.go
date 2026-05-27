package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

var rng = rand.Reader

// RefreshToken represents the cleartext token returned to the client; never persisted.
type RefreshToken struct {
	Token     string
	SessionID string
	UserID    string
	ExpiresAt time.Time
}

// IssueRefresh creates a new session row and returns the cleartext refresh token.
// rotatedFrom is the previous session id when rotating; empty on initial login.
func IssueRefresh(ctx context.Context, db *dbx.DB, userID string, rotatedFrom, userAgent string, ip net.IP, ttl time.Duration) (*RefreshToken, error) {
	raw := make([]byte, 32)
	if _, err := rng.Read(raw); err != nil {
		return nil, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tok))
	id := dbx.NewIDWithPrefix("ses")
	exp := time.Now().UTC().Add(ttl)

	err := db.Tx(ctx, func(tx pgx.Tx) error {
		// INSERT the new session FIRST — the rotated_to FK on the old session
		// requires the target row to exist before the UPDATE can reference it.
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_session (id, user_id, refresh_token_hash, expires_at, user_agent, ip)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			id, userID, hex.EncodeToString(hash[:]), exp, nullable(userAgent), nullableIP(ip)); err != nil {
			return err
		}
		if rotatedFrom != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE user_session SET rotated_to = $1, revoked_at = now() WHERE id = $2 AND revoked_at IS NULL`,
				id, rotatedFrom); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &RefreshToken{Token: tok, SessionID: id, UserID: userID, ExpiresAt: exp}, nil
}

// ValidateRefresh looks up the session by the presented cleartext token, enforces
// expiry and replay detection (re-using a rotated token revokes the chain).
func ValidateRefresh(ctx context.Context, db *dbx.DB, token string) (userID, sessionID string, err error) {
	hash := sha256.Sum256([]byte(token))
	var (
		uID, sID  string
		expiresAt time.Time
		rotatedTo *string
		revokedAt *time.Time
	)
	err = db.QueryRow(ctx, `
		SELECT id, user_id, expires_at, rotated_to, revoked_at
		FROM user_session WHERE refresh_token_hash = $1`,
		hex.EncodeToString(hash[:])).Scan(&sID, &uID, &expiresAt, &rotatedTo, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrInvalidRefresh
	}
	if err != nil {
		return "", "", err
	}
	if rotatedTo != nil {
		// Replay of an already-rotated token: revoke the whole chain anchored at this session.
		_, _ = db.Exec(ctx, `UPDATE user_session SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, uID)
		return "", "", ErrRefreshReplay
	}
	if revokedAt != nil {
		return "", "", ErrInvalidRefresh
	}
	if time.Now().After(expiresAt) {
		return "", "", ErrInvalidRefresh
	}
	return uID, sID, nil
}

// RevokeAll revokes every active session for a user (used on logout-all).
func RevokeAll(ctx context.Context, db *dbx.DB, userID string) error {
	_, err := db.Exec(ctx, `UPDATE user_session SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// RevokeSession revokes a single session by id.
func RevokeSession(ctx context.Context, db *dbx.DB, sessionID string) error {
	_, err := db.Exec(ctx, `UPDATE user_session SET revoked_at = now() WHERE id = $1`, sessionID)
	return err
}

var (
	ErrInvalidRefresh = errors.New("auth: invalid or expired refresh token")
	ErrRefreshReplay  = errors.New("auth: refresh token replay detected; chain revoked")
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableIP(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}
