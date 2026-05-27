package materialrequest

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/accounting/purchaseorder"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-material-requests", Method: http.MethodGet,
		Path: "/accounting/material-requests", Summary: "List material requests",
		Tags: []string{"Accounting / Material Request"},
	}, func(ctx context.Context, _ *struct{}) (*mrListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ms, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &mrListOut{Body: mrListBody{Items: ms}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-material-request", Method: http.MethodPost,
		Path: "/accounting/material-requests", Summary: "Create a Material Request draft",
		Tags: []string{"Accounting / Material Request"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *mrCreateIn) (*mrOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		mr, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &mrOut{Body: *mr}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-material-request", Method: http.MethodGet,
		Path: "/accounting/material-requests/{id}", Summary: "Get a Material Request",
		Tags: []string{"Accounting / Material Request"},
	}, func(ctx context.Context, in *mrGetIn) (*mrOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		mr, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &mrOut{Body: *mr}, nil
	})

	for _, op := range []struct {
		id, path, summary string
		fn                func(context.Context, string) (*MaterialRequest, error)
		action            permission.Action
	}{
		{"submit-material-request", "submit", "Submit a Material Request", h.Service.Submit, permission.ActionSubmit},
		{"cancel-material-request", "cancel", "Cancel a Material Request",  h.Service.Cancel, permission.ActionCancel},
		{"stop-material-request",   "stop",   "Stop a Material Request",    h.Service.Stop,   permission.ActionSubmit},
		{"reopen-material-request", "reopen", "Reopen a stopped Material Request", h.Service.Reopen, permission.ActionSubmit},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost,
			Path: "/accounting/material-requests/{id}/" + op.path, Summary: op.summary,
			Tags: []string{"Accounting / Material Request"},
		}, func(ctx context.Context, in *mrGetIn) (*mrOut, error) {
			if err := h.Perm.Check(ctx, Doctype, op.action); err != nil {
				return nil, httpx.MapError(err)
			}
			mr, err := op.fn(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &mrOut{Body: *mr}, nil
		})
	}

	huma.Register(api, huma.Operation{
		OperationID: "create-po-from-material-request",
		Method:      http.MethodPost,
		Path:        "/accounting/material-requests/{id}/create-purchase-order",
		Summary:     "Create a draft Purchase Order from the remaining qty of this MR",
		Tags:        []string{"Accounting / Material Request"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *mrCreatePOIn) (*mrCreatePOOut, error) {
		// User needs Create permission on PO to materialise one.
		if err := h.Perm.Check(ctx, purchaseorder.Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		body := in.Body
		body.MaterialRequestID = in.ID
		po, err := h.Service.CreatePOFromMR(ctx, body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &mrCreatePOOut{Body: *po}, nil
	})
}

type (
	mrCreateIn struct{ Body CreateInput }
	mrOut      struct{ Body MaterialRequest }
	mrListOut  struct{ Body mrListBody }
	mrListBody struct {
		Items []MaterialRequest `json:"items"`
	}
	mrGetIn struct {
		ID string `path:"id"`
	}
	mrCreatePOIn struct {
		ID   string `path:"id"`
		Body CreatePOFromMRInput
	}
	mrCreatePOOut struct {
		Body purchaseorder.PurchaseOrder
	}
)
