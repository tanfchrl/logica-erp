// Package apitokens implements personal access tokens for the API.
//
// On create: generate a "lt_<32 random bytes hex>" plaintext, store only its
// SHA-256 hash and the first 8 chars (for the UI). The plaintext is returned
// once in the create response; the UI must show it then forget it.
//
// Auth integration with the bearer-token middleware is a follow-up (handler
// recognises only JWT today). Tokens recorded here are surfaced + revocable
// immediately; making them actually authenticate is a one-line change in
// httpx.Auth once exposed.
package apitokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "api_token"

type APIToken struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	UserID     string    `json:"user_id"`
	UserEmail  string    `json:"user_email,omitempty"`
	Scopes     []string  `json:"scopes"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	RevokedAt  time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type APITokenCreateInput struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes,omitempty" doc:"['*'] for full access (the only option today)"`
	ExpiresAt string   `json:"expires_at,omitempty" doc:"RFC3339; omit for no expiry"`
}

type APITokenCreateResult struct {
	Token       APIToken `json:"token"`
	Plaintext   string `json:"plaintext" doc:"Shown ONCE. Store it now; never recoverable."`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// List returns tokens owned by the caller (or all tokens for admins).
func (s *Service) List(ctx context.Context, all bool) ([]APIToken, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("api_token: unauthenticated")
	}
	q := `
		SELECT t.id, t.name, t.prefix, t.user_id, coalesce(u.email,''), t.scopes,
		       coalesce(t.expires_at, 'epoch'::timestamptz),
		       coalesce(t.last_used_at, 'epoch'::timestamptz),
		       coalesce(t.revoked_at, 'epoch'::timestamptz),
		       t.created_at
		FROM api_token t
		LEFT JOIN users u ON u.id = t.user_id`
	args := []any{}
	if !all && !p.IsSystem {
		q += " WHERE t.user_id = $1"
		args = append(args, p.UserID)
	}
	q += " ORDER BY t.revoked_at NULLS FIRST, t.created_at DESC"
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]APIToken, 0)
	for rows.Next() {
		var t APIToken
		var exp, used, rev time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.UserID, &t.UserEmail, &t.Scopes,
			&exp, &used, &rev, &t.CreatedAt); err != nil {
			return nil, err
		}
		if exp.Year() > 1970 { t.ExpiresAt = exp }
		if used.Year() > 1970 { t.LastUsedAt = used }
		if rev.Year() > 1970 { t.RevokedAt = rev }
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, in APITokenCreateInput) (*APITokenCreateResult, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("api_token: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("api_token.name: required")
	}
	var expiresAt any
	if in.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, in.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("expires_at: %w", err)
		}
		expiresAt = t
	}
	scopes := in.Scopes
	if len(scopes) == 0 {
		scopes = []string{"*"}
	}

	// Generate plaintext + hash.
	rb := make([]byte, 32)
	if _, err := rand.Read(rb); err != nil {
		return nil, err
	}
	plaintext := "lt_" + hex.EncodeToString(rb)
	hash := sha256.Sum256([]byte(plaintext))
	hashHex := hex.EncodeToString(hash[:])
	prefix := plaintext[:11] // "lt_" + 8 chars

	id := dbx.NewIDWithPrefix("apit")
	var t APIToken
	err := s.db.QueryRow(ctx, `
		INSERT INTO api_token (id, name, token_hash, prefix, user_id, scopes, expires_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, name, prefix, user_id, scopes, coalesce(expires_at,'epoch'::timestamptz), created_at`,
		id, in.Name, hashHex, prefix, p.UserID, scopes, expiresAt, p.UserID).
		Scan(&t.ID, &t.Name, &t.Prefix, &t.UserID, &t.Scopes, &t.ExpiresAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	if t.ExpiresAt.Year() < 1971 { t.ExpiresAt = time.Time{} }
	return &APITokenCreateResult{Token: t, Plaintext: plaintext}, nil
}

func (s *Service) Revoke(ctx context.Context, id string) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("api_token: unauthenticated")
	}
	var ownerID string
	if err := s.db.QueryRow(ctx, `SELECT user_id FROM api_token WHERE id = $1`, id).Scan(&ownerID); err != nil {
		return err
	}
	if ownerID != p.UserID && !p.IsSystem {
		return errors.New("api_token: only the owner or a system user can revoke")
	}
	ct, err := s.db.Exec(ctx, `UPDATE api_token SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("api_token: already revoked or not found")
	}
	return nil
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-api-tokens", Method: http.MethodGet,
		Path: "/admin/api-tokens", Summary: "List the caller's API tokens",
		Tags: []string{"Admin / API Tokens"},
	}, func(ctx context.Context, in *atListIn) (*atListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ts, err := h.Service.List(ctx, in.All)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &atListOut{Body: atListBody{Items: ts}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-api-token", Method: http.MethodPost,
		Path: "/admin/api-tokens", Summary: "Issue a new API token (plaintext returned once)",
		Tags: []string{"Admin / API Tokens"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *atCreateIn) (*atCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		r, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &atCreateOut{Body: *r}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "revoke-api-token", Method: http.MethodDelete,
		Path: "/admin/api-tokens/{id}", Summary: "Revoke an API token",
		Tags: []string{"Admin / API Tokens"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *atByID) (*struct{}, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionDelete); err != nil {
			return nil, httpx.MapError(err)
		}
		if err := h.Service.Revoke(ctx, in.ID); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})
}

type (
	atListIn struct {
		All bool `query:"all" doc:"System users only: include every user's tokens"`
	}
	atListOut  struct{ Body atListBody }
	atListBody struct {
		Items []APIToken `json:"items"`
	}
	atCreateIn  struct{ Body APITokenCreateInput }
	atCreateOut struct{ Body APITokenCreateResult }
	atByID struct {
		ID string `path:"id"`
	}
)
