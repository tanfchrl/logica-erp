// admin.go exposes a paginated, filterable read API over document_audit.
package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "audit_log"

type Entry struct {
	ID         string          `json:"id"`
	Doctype    string          `json:"doctype"`
	DocumentID string          `json:"document_id"`
	Action     string          `json:"action"`
	ChangedBy  string          `json:"changed_by"`
	UserEmail  string          `json:"user_email"`
	UserName   string          `json:"user_name"`
	ChangedAt  time.Time       `json:"changed_at"`
	Diff       json.RawMessage `json:"diff,omitempty"`
}

type QueryParams struct {
	Doctype    string
	DocumentID string
	UserID     string
	Action     string
	Since      time.Time
	Until      time.Time
	Limit      int
}

type QueryService struct{ db *dbx.DB }

func NewQueryService(db *dbx.DB) *QueryService { return &QueryService{db: db} }

func (s *QueryService) Query(ctx context.Context, p QueryParams) ([]Entry, error) {
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 100
	}
	q := `
		SELECT a.id, a.doctype, a.document_id, a.action, a.changed_by,
		       coalesce(u.email,''), coalesce(u.full_name,''),
		       a.changed_at, a.diff
		FROM document_audit a
		LEFT JOIN users u ON u.id = a.changed_by
		WHERE ($1 = '' OR a.doctype = $1)
		  AND ($2 = '' OR a.document_id = $2)
		  AND ($3 = '' OR a.changed_by = $3)
		  AND ($4 = '' OR a.action = $4)
		  AND ($5::timestamptz IS NULL OR a.changed_at >= $5)
		  AND ($6::timestamptz IS NULL OR a.changed_at <  $6)
		ORDER BY a.changed_at DESC
		LIMIT $7`
	var since, until any
	if !p.Since.IsZero() {
		since = p.Since
	}
	if !p.Until.IsZero() {
		until = p.Until
	}
	rows, err := s.db.Query(ctx, q,
		p.Doctype, p.DocumentID, p.UserID, p.Action, since, until, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Doctype, &e.DocumentID, &e.Action, &e.ChangedBy,
			&e.UserEmail, &e.UserName, &e.ChangedAt, &e.Diff); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Distinct returns the distinct values of column c (one of: doctype, action)
// so the UI can populate filter dropdowns from real data.
func (s *QueryService) Distinct(ctx context.Context, c string) ([]string, error) {
	switch c {
	case "doctype", "action":
	default:
		return nil, huma.NewError(http.StatusBadRequest, "unsupported column")
	}
	rows, err := s.db.Query(ctx, "SELECT DISTINCT "+c+" FROM document_audit WHERE "+c+" <> '' ORDER BY "+c)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- HTTP ----

type AdminHandler struct {
	Service *QueryService
	Perm    *permission.Engine
}

func RegisterAdmin(api huma.API, h *AdminHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-audit-log", Method: http.MethodGet,
		Path: "/admin/audit-log", Summary: "Query the document audit log",
		Tags: []string{"Admin / Audit"},
	}, func(ctx context.Context, in *queryIn) (*queryOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		var since, until time.Time
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "since: "+err.Error())
			}
			since = t
		}
		if in.Until != "" {
			t, err := time.Parse(time.RFC3339, in.Until)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "until: "+err.Error())
			}
			until = t
		}
		es, err := h.Service.Query(ctx, QueryParams{
			Doctype: in.Doctype, DocumentID: in.DocumentID, UserID: in.UserID,
			Action: strings.ToLower(in.Action), Since: since, Until: until, Limit: in.Limit,
		})
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &queryOut{Body: queryBody{Items: es}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "audit-log-facets", Method: http.MethodGet,
		Path: "/admin/audit-log/facets", Summary: "Distinct values to populate filters",
		Tags: []string{"Admin / Audit"},
	}, func(ctx context.Context, _ *struct{}) (*facetsOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		doctypes, err := h.Service.Distinct(ctx, "doctype")
		if err != nil {
			return nil, httpx.MapError(err)
		}
		actions, err := h.Service.Distinct(ctx, "action")
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &facetsOut{Body: facetsBody{Doctypes: doctypes, Actions: actions}}, nil
	})
}

type (
	queryIn struct {
		Doctype    string `query:"doctype"`
		DocumentID string `query:"document_id"`
		UserID     string `query:"user_id"`
		Action     string `query:"action"`
		Since      string `query:"since" doc:"RFC3339"`
		Until      string `query:"until" doc:"RFC3339"`
		Limit      int    `query:"limit"`
	}
	queryOut  struct{ Body queryBody }
	queryBody struct {
		Items []Entry `json:"items"`
	}
	facetsOut  struct{ Body facetsBody }
	facetsBody struct {
		Doctypes []string `json:"doctypes"`
		Actions  []string `json:"actions"`
	}
)
