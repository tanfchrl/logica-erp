// approval_http.go — HTTP handlers for the approvals inbox + per-doc widget.
// Kept in its own file to keep admin.go focused on configuration concerns.
package workflow

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

// ApprovalHandler wraps the runtime engine with HTTP plumbing.
type ApprovalHandler struct {
	Engine *ApprovalEngine
	Perm   *permission.Engine
}

func RegisterApprovals(api huma.API, h *ApprovalHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-pending-approvals",
		Method:      http.MethodGet,
		Path:        "/admin/approvals/pending",
		Summary:     "Approval requests waiting on the caller",
		Tags:        []string{"Admin / Approvals"},
	}, func(ctx context.Context, _ *struct{}) (*aprInboxOut, error) {
		// Authenticated only; everyone with a role can see their own queue.
		rows, err := h.Engine.PendingForCaller(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &aprInboxOut{Body: aprInboxBody{Items: rows}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-resolved-approvals",
		Method:      http.MethodGet,
		Path:        "/admin/approvals/resolved",
		Summary:     "Approval requests the caller has decided on recently",
		Tags:        []string{"Admin / Approvals"},
	}, func(ctx context.Context, _ *struct{}) (*aprInboxOut, error) {
		rows, err := h.Engine.ResolvedByCaller(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &aprInboxOut{Body: aprInboxBody{Items: rows}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-approvals-by-doc",
		Method:      http.MethodGet,
		Path:        "/admin/approvals/by-doc/{doctype}/{document_id}",
		Summary:     "Approval status for a specific document",
		Tags:        []string{"Admin / Approvals"},
	}, func(ctx context.Context, in *aprByDocIn) (*aprInboxOut, error) {
		if err := h.Perm.Check(ctx, DoctypeApprovalRule, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		rows, err := h.Engine.ListByDocument(ctx, in.Doctype, in.DocumentID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &aprInboxOut{Body: aprInboxBody{Items: rows}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "approve-request",
		Method:      http.MethodPost,
		Path:        "/admin/approvals/{id}/approve",
		Summary:     "Approve an approval_request",
		Tags:        []string{"Admin / Approvals"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *aprDecideIn) (*struct{}, error) {
		if err := h.Engine.Decide(ctx, in.ID, true, in.Body.Note); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "reject-request",
		Method:      http.MethodPost,
		Path:        "/admin/approvals/{id}/reject",
		Summary:     "Reject an approval_request",
		Tags:        []string{"Admin / Approvals"}, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *aprDecideIn) (*struct{}, error) {
		if err := h.Engine.Decide(ctx, in.ID, false, in.Body.Note); err != nil {
			return nil, httpx.MapError(err)
		}
		return nil, nil
	})
}

type (
	aprInboxOut  struct{ Body aprInboxBody }
	aprInboxBody struct {
		Items []InboxRow `json:"items"`
	}
	aprByDocIn struct {
		Doctype    string `path:"doctype"`
		DocumentID string `path:"document_id"`
	}
	aprDecideIn struct {
		ID   string `path:"id"`
		Body aprDecideBody
	}
	aprDecideBody struct {
		Note string `json:"note,omitempty"`
	}
)
