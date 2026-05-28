package salesorder

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

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
		OperationID: "list-sales-orders", Method: http.MethodGet,
		Path: "/accounting/sales-orders", Summary: "List sales orders",
		Tags: []string{"Accounting / Sales Order"},
	}, func(ctx context.Context, _ *struct{}) (*soListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ss, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &soListOut{Body: soListBody{Items: ss}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-sales-order", Method: http.MethodPost,
		Path: "/accounting/sales-orders", Summary: "Create a Sales Order draft",
		Tags: []string{"Accounting / Sales Order"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *soCreateIn) (*soOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		so, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &soOut{Body: *so}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-sales-order", Method: http.MethodGet,
		Path: "/accounting/sales-orders/{id}", Summary: "Get a Sales Order",
		Tags: []string{"Accounting / Sales Order"},
	}, func(ctx context.Context, in *soGetIn) (*soOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		so, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &soOut{Body: *so}, nil
	})

	for _, op := range []struct {
		id, path, summary string
		fn                func(context.Context, string) (*SalesOrder, error)
		action            permission.Action
	}{
		{"submit-sales-order", "submit", "Submit a Sales Order", h.Service.Submit, permission.ActionSubmit},
		{"cancel-sales-order", "cancel", "Cancel a Sales Order", h.Service.Cancel, permission.ActionCancel},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost,
			Path: "/accounting/sales-orders/{id}/" + op.path, Summary: op.summary,
			Tags: []string{"Accounting / Sales Order"},
		}, func(ctx context.Context, in *soGetIn) (*soOut, error) {
			if err := h.Perm.Check(ctx, Doctype, op.action); err != nil {
				return nil, httpx.MapError(err)
			}
			so, err := op.fn(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &soOut{Body: *so}, nil
		})
	}
}

type (
	soCreateIn struct{ Body SOCreateInput }
	soOut      struct{ Body SalesOrder }
	soListOut  struct{ Body soListBody }
	soListBody struct {
		Items []SalesOrder `json:"items"`
	}
	soGetIn struct {
		ID string `path:"id"`
	}
)
