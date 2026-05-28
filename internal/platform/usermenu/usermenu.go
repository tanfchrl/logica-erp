// Package usermenu owns the per-user "Starred" sidebar section. Each row
// is one starred list-page (path + label). No company scoping — the menu
// follows the user across companies.
package usermenu

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
)

type Star struct {
	Path      string    `json:"path"`
	Label     string    `json:"label"`
	Position  int       `json:"position"`
	StarredAt time.Time `json:"starred_at"`
}

type StarInput struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) List(ctx context.Context, userID string) ([]Star, error) {
	rows, err := s.db.Query(ctx, `
		SELECT path, label, position, starred_at
		FROM user_starred_menu
		WHERE user_id = $1
		ORDER BY position, starred_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Star{}
	for rows.Next() {
		var st Star
		if err := rows.Scan(&st.Path, &st.Label, &st.Position, &st.StarredAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Add upserts (idempotent): re-starring an already-starred path bumps the
// label (in case the doctype's display name has changed) without touching
// starred_at or position.
func (s *Service) Add(ctx context.Context, userID string, in StarInput) (*Star, error) {
	path := strings.TrimSpace(in.Path)
	label := strings.TrimSpace(in.Label)
	if path == "" || label == "" {
		return nil, errors.New("starred_menu: path + label required")
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("starred_menu: path must start with /")
	}
	var st Star
	err := s.db.QueryRow(ctx, `
		INSERT INTO user_starred_menu (user_id, path, label)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, path) DO UPDATE
		  SET label = EXCLUDED.label
		RETURNING path, label, position, starred_at`,
		userID, path, label,
	).Scan(&st.Path, &st.Label, &st.Position, &st.StarredAt)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Service) Remove(ctx context.Context, userID, path string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM user_starred_menu WHERE user_id = $1 AND path = $2`, userID, path)
	return err
}

// ---- HTTP ----

type Handler struct{ Service *Service }

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-starred-menu", Method: http.MethodGet,
		Path: "/me/starred-menu", Summary: "List the caller's starred sidebar items",
		Tags: []string{"Platform / Starred Menu"},
	}, func(ctx context.Context, _ *struct{}) (*starListOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		stars, err := h.Service.List(ctx, p.UserID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &starListOut{Body: starListBody{Items: stars}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "add-starred-menu", Method: http.MethodPost,
		Path: "/me/starred-menu", Summary: "Star a sidebar item",
		Tags: []string{"Platform / Starred Menu"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *starAddIn) (*starOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		st, err := h.Service.Add(ctx, p.UserID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &starOut{Body: *st}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "remove-starred-menu", Method: http.MethodDelete,
		Path: "/me/starred-menu", Summary: "Unstar a sidebar item (path supplied as query)",
		Tags: []string{"Platform / Starred Menu"},
	}, func(ctx context.Context, in *starRemoveIn) (*struct{ Body map[string]string }, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		if strings.TrimSpace(in.Path) == "" {
			return nil, huma.NewError(http.StatusBadRequest, "path query param required")
		}
		if err := h.Service.Remove(ctx, p.UserID, in.Path); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
}

type (
	starAddIn    struct{ Body StarInput }
	starRemoveIn struct {
		Path string `query:"path" doc:"the path to unstar"`
	}
	starOut      struct{ Body Star }
	starListOut  struct{ Body starListBody }
	starListBody struct {
		Items []Star `json:"items"`
	}
)
