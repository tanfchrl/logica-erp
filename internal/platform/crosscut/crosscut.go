// Package crosscut bundles the cross-cutting features any doctype can use:
// comments, attachments, notifications, and global search.
//
// Each is a thin service over a single table. They're combined into one
// package because together they make up the "social/audit" surface of every
// document and would otherwise spawn 4 near-empty packages.
package crosscut

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// ---- COMMENTS ----

type Comment struct {
	ID              string    `json:"id"`
	Doctype         string    `json:"doctype"`
	DocumentID      string    `json:"document_id"`
	ParentCommentID string    `json:"parent_comment_id,omitempty"`
	Body            string    `json:"body"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedBy       string    `json:"created_by"`
}

type CommentCreateInput struct {
	Doctype         string `json:"doctype"`
	DocumentID      string `json:"document_id"`
	ParentCommentID string `json:"parent_comment_id,omitempty"`
	Body            string `json:"body"`
}

func (s *Service) CreateComment(ctx context.Context, in CommentCreateInput) (*Comment, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("comment: unauthenticated")
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Doctype == "" || in.DocumentID == "" || in.Body == "" {
		return nil, errors.New("comment: doctype/document_id/body required")
	}
	id := dbx.NewIDWithPrefix("cmt")
	var c Comment
	err := s.db.QueryRow(ctx, `
		INSERT INTO document_comment (id, doctype, document_id, parent_comment_id, body, created_by)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, doctype, document_id, coalesce(parent_comment_id,''), body, created_at, created_by`,
		id, in.Doctype, in.DocumentID, nullable(in.ParentCommentID), in.Body, p.UserID).
		Scan(&c.ID, &c.Doctype, &c.DocumentID, &c.ParentCommentID, &c.Body, &c.CreatedAt, &c.CreatedBy)
	return &c, err
}

func (s *Service) ListComments(ctx context.Context, doctype, documentID string) ([]Comment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, doctype, document_id, coalesce(parent_comment_id,''), body, created_at, created_by
		FROM document_comment WHERE doctype = $1 AND document_id = $2 ORDER BY created_at`,
		doctype, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.Doctype, &c.DocumentID, &c.ParentCommentID, &c.Body, &c.CreatedAt, &c.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- ATTACHMENTS ----

type Attachment struct {
	ID            string    `json:"id"`
	Doctype       string    `json:"doctype"`
	DocumentID    string    `json:"document_id"`
	FileName      string    `json:"file_name"`
	FileSize      int64     `json:"file_size"`
	ContentType   string    `json:"content_type"`
	StorageKey    string    `json:"storage_key"`
	StorageDriver string    `json:"storage_driver"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by"`
}

// AttachmentCreateInput records an attachment after the file has been uploaded to storage.
// (The actual file upload endpoint is wired separately; this records metadata.)
type AttachmentCreateInput struct {
	Doctype     string `json:"doctype"`
	DocumentID  string `json:"document_id"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	ContentType string `json:"content_type"`
	StorageKey  string `json:"storage_key"`
}

func (s *Service) RecordAttachment(ctx context.Context, in AttachmentCreateInput) (*Attachment, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("attachment: unauthenticated")
	}
	if in.Doctype == "" || in.DocumentID == "" || in.FileName == "" || in.StorageKey == "" {
		return nil, errors.New("attachment: doctype/document_id/file_name/storage_key required")
	}
	id := dbx.NewIDWithPrefix("att")
	var a Attachment
	err := s.db.QueryRow(ctx, `
		INSERT INTO document_attachment (id, doctype, document_id, file_name, file_size, content_type, storage_key, storage_driver, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'local',$8)
		RETURNING id, doctype, document_id, file_name, file_size, content_type, storage_key, storage_driver, created_at, created_by`,
		id, in.Doctype, in.DocumentID, in.FileName, in.FileSize, in.ContentType, in.StorageKey, p.UserID).
		Scan(&a.ID, &a.Doctype, &a.DocumentID, &a.FileName, &a.FileSize, &a.ContentType, &a.StorageKey, &a.StorageDriver, &a.CreatedAt, &a.CreatedBy)
	return &a, err
}

func (s *Service) ListAttachments(ctx context.Context, doctype, documentID string) ([]Attachment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, doctype, document_id, file_name, file_size, content_type, storage_key, storage_driver, created_at, created_by
		FROM document_attachment WHERE doctype = $1 AND document_id = $2 ORDER BY created_at DESC`,
		doctype, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.Doctype, &a.DocumentID, &a.FileName, &a.FileSize, &a.ContentType, &a.StorageKey, &a.StorageDriver, &a.CreatedAt, &a.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- NOTIFICATIONS ----

type Notification struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	Subject        string     `json:"subject"`
	Body           string     `json:"body,omitempty"`
	LinkDoctype    string     `json:"link_doctype,omitempty"`
	LinkDocumentID string     `json:"link_document_id,omitempty"`
	IsRead         bool       `json:"is_read"`
	ReadAt         *time.Time `json:"read_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type NotificationCreateInput struct {
	UserID         string `json:"user_id"`
	Subject        string `json:"subject"`
	Body           string `json:"body,omitempty"`
	LinkDoctype    string `json:"link_doctype,omitempty"`
	LinkDocumentID string `json:"link_document_id,omitempty"`
}

// CreateNotification can be called either from HTTP (admin notifications) or from any service
// after a meaningful event (e.g. invoice submitted). Returns the created row.
func (s *Service) CreateNotification(ctx context.Context, in NotificationCreateInput) (*Notification, error) {
	if in.UserID == "" || in.Subject == "" {
		return nil, errors.New("notification: user_id and subject required")
	}
	id := dbx.NewIDWithPrefix("ntf")
	var n Notification
	err := s.db.QueryRow(ctx, `
		INSERT INTO notification (id, user_id, subject, body, link_doctype, link_document_id)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, user_id, subject, coalesce(body,''), coalesce(link_doctype,''), coalesce(link_document_id,''), is_read, read_at, created_at`,
		id, in.UserID, in.Subject, nullable(in.Body), nullable(in.LinkDoctype), nullable(in.LinkDocumentID)).
		Scan(&n.ID, &n.UserID, &n.Subject, &n.Body, &n.LinkDoctype, &n.LinkDocumentID, &n.IsRead, &n.ReadAt, &n.CreatedAt)
	return &n, err
}

func (s *Service) ListNotifications(ctx context.Context, onlyUnread bool) ([]Notification, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("notifications: unauthenticated")
	}
	q := `SELECT id, user_id, subject, coalesce(body,''), coalesce(link_doctype,''), coalesce(link_document_id,''), is_read, read_at, created_at
	      FROM notification WHERE user_id = $1`
	if onlyUnread {
		q += ` AND is_read = false`
	}
	q += ` ORDER BY created_at DESC LIMIT 100`
	rows, err := s.db.Query(ctx, q, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Subject, &n.Body, &n.LinkDoctype, &n.LinkDocumentID, &n.IsRead, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Service) MarkAllRead(ctx context.Context) error {
	p := auth.FromContext(ctx)
	if p == nil {
		return errors.New("notifications: unauthenticated")
	}
	_, err := s.db.Exec(ctx, `UPDATE notification SET is_read = true, read_at = now() WHERE user_id = $1 AND is_read = false`, p.UserID)
	return err
}

// ---- GLOBAL SEARCH ----

type SearchHit struct {
	Doctype    string    `json:"doctype"`
	DocumentID string    `json:"document_id"`
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	Snippet    string    `json:"snippet,omitempty"`
	CompanyID  string    `json:"company_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// IndexDocument upserts a search row. Callers invoke this after submit/update.
func (s *Service) IndexDocument(ctx context.Context, tx pgx.Tx, doctype, documentID, name, title, body, companyID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO search_index (doctype, document_id, name, title, body, company_id, ts, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6, to_tsvector('simple', coalesce($3,'') || ' ' || coalesce($4,'') || ' ' || coalesce($5,'')), now())
		ON CONFLICT (doctype, document_id) DO UPDATE SET
		  name = EXCLUDED.name,
		  title = EXCLUDED.title,
		  body = EXCLUDED.body,
		  company_id = EXCLUDED.company_id,
		  ts = EXCLUDED.ts,
		  updated_at = now()`,
		doctype, documentID, name, title, nullable(body), nullable(companyID))
	return err
}

func (s *Service) Search(ctx context.Context, q string, companyID string, limit int) ([]SearchHit, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	// Use websearch_to_tsquery for natural-feeling queries; fall back to plain prefix matching if empty.
	rows, err := s.db.Query(ctx, `
		SELECT doctype, document_id, name, title, coalesce(body,''), coalesce(company_id,''), updated_at
		FROM search_index
		WHERE ts @@ websearch_to_tsquery('simple', $1)
		  AND ($2 = '' OR company_id = $2)
		ORDER BY ts_rank(ts, websearch_to_tsquery('simple', $1)) DESC, updated_at DESC
		LIMIT $3`, q, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var body string
		if err := rows.Scan(&h.Doctype, &h.DocumentID, &h.Name, &h.Title, &body, &h.CompanyID, &h.UpdatedAt); err != nil {
			return nil, err
		}
		// Naive snippet: first 120 chars of body.
		if len(body) > 120 {
			h.Snippet = body[:120] + "…"
		} else {
			h.Snippet = body
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "create-comment", Method: http.MethodPost,
		Path: "/comments", Summary: "Add a comment to a document",
		Tags: []string{"Cross-cut / Comments"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *cmtCreateIn) (*cmtOut, error) {
		if err := h.Perm.Check(ctx, in.Body.Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.CreateComment(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &cmtOut{Body: *c}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-comments", Method: http.MethodGet,
		Path: "/comments", Summary: "List comments on a document",
		Tags: []string{"Cross-cut / Comments"},
	}, func(ctx context.Context, in *cmtListIn) (*cmtListOut, error) {
		if err := h.Perm.Check(ctx, in.Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		cs, err := h.Service.ListComments(ctx, in.Doctype, in.DocumentID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &cmtListOut{Body: cmtListBody{Items: cs}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "record-attachment", Method: http.MethodPost,
		Path: "/attachments", Summary: "Record an attachment after upload",
		Tags: []string{"Cross-cut / Attachments"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *attCreateIn) (*attOut, error) {
		if err := h.Perm.Check(ctx, in.Body.Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		a, err := h.Service.RecordAttachment(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &attOut{Body: *a}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-attachments", Method: http.MethodGet,
		Path: "/attachments", Summary: "List attachments on a document",
		Tags: []string{"Cross-cut / Attachments"},
	}, func(ctx context.Context, in *attListIn) (*attListOut, error) {
		if err := h.Perm.Check(ctx, in.Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		as, err := h.Service.ListAttachments(ctx, in.Doctype, in.DocumentID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &attListOut{Body: attListBody{Items: as}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "list-notifications", Method: http.MethodGet,
		Path: "/notifications", Summary: "List notifications for the current user",
		Tags: []string{"Cross-cut / Notifications"},
	}, func(ctx context.Context, in *ntfListIn) (*ntfListOut, error) {
		ns, err := h.Service.ListNotifications(ctx, in.UnreadOnly)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &ntfListOut{Body: ntfListBody{Items: ns}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "mark-all-notifications-read", Method: http.MethodPost,
		Path: "/notifications/mark-all-read", Summary: "Mark all notifications as read",
		Tags: []string{"Cross-cut / Notifications"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body map[string]string }, error) {
		if err := h.Service.MarkAllRead(ctx); err != nil {
			return nil, httpx.MapError(err)
		}
		return &struct{ Body map[string]string }{Body: map[string]string{"status": "ok"}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "global-search", Method: http.MethodGet,
		Path: "/search", Summary: "Search across indexed documents",
		Tags: []string{"Cross-cut / Search"},
	}, func(ctx context.Context, in *searchIn) (*searchOut, error) {
		co := auth.CompanyFromContext(ctx)
		hits, err := h.Service.Search(ctx, in.Q, co, in.Limit)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &searchOut{Body: searchBody{Query: in.Q, Hits: hits}}, nil
	})
}

type (
	cmtCreateIn struct{ Body CommentCreateInput }
	cmtOut      struct{ Body Comment }
	cmtListIn   struct {
		Doctype    string `query:"doctype" required:"true"`
		DocumentID string `query:"document_id" required:"true"`
	}
	cmtListOut struct{ Body cmtListBody }
	cmtListBody struct {
		Items []Comment `json:"items"`
	}
	attCreateIn struct{ Body AttachmentCreateInput }
	attOut      struct{ Body Attachment }
	attListIn   struct {
		Doctype    string `query:"doctype" required:"true"`
		DocumentID string `query:"document_id" required:"true"`
	}
	attListOut struct{ Body attListBody }
	attListBody struct {
		Items []Attachment `json:"items"`
	}
	ntfListIn struct {
		UnreadOnly bool `query:"unread_only"`
	}
	ntfListOut struct{ Body ntfListBody }
	ntfListBody struct {
		Items []Notification `json:"items"`
	}
	searchIn struct {
		Q     string `query:"q" required:"true"`
		Limit int    `query:"limit"`
	}
	searchOut struct{ Body searchBody }
	searchBody struct {
		Query string      `json:"query"`
		Hits  []SearchHit `json:"hits"`
	}
)

// Unused but referenced to keep `fmt` honest for future error formatting.
var _ = fmt.Sprintf
