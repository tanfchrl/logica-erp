package purchasereceipt

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
		OperationID: "list-purchase-receipts", Method: http.MethodGet,
		Path: "/stock/purchase-receipts", Summary: "List purchase receipts",
		Tags: []string{"Stock / Purchase Receipt"},
	}, func(ctx context.Context, _ *struct{}) (*prListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		co := auth.CompanyFromContext(ctx)
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "X-Company-Id required")
		}
		ps, err := h.Service.List(ctx, co)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prListOut{Body: prListBody{Items: ps}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-purchase-receipt", Method: http.MethodPost,
		Path: "/stock/purchase-receipts", Summary: "Create a Purchase Receipt draft",
		Tags: []string{"Stock / Purchase Receipt"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *prCreateIn) (*prOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		pr, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prOut{Body: *pr}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-purchase-receipt", Method: http.MethodGet,
		Path: "/stock/purchase-receipts/{id}", Summary: "Get a Purchase Receipt",
		Tags: []string{"Stock / Purchase Receipt"},
	}, func(ctx context.Context, in *prGetIn) (*prOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		pr, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &prOut{Body: *pr}, nil
	})

	for _, op := range []struct {
		id, path, summary string
		fn                func(context.Context, string) (*PurchaseReceipt, error)
		action            permission.Action
	}{
		{"submit-purchase-receipt", "submit", "Submit a Purchase Receipt (posts stock ledger)", h.Service.Submit, permission.ActionSubmit},
		{"cancel-purchase-receipt", "cancel", "Cancel a Purchase Receipt (reverses stock ledger + PO received_qty)", h.Service.Cancel, permission.ActionCancel},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost,
			Path: "/stock/purchase-receipts/{id}/" + op.path, Summary: op.summary,
			Tags: []string{"Stock / Purchase Receipt"},
		}, func(ctx context.Context, in *prGetIn) (*prOut, error) {
			if err := h.Perm.Check(ctx, Doctype, op.action); err != nil {
				return nil, httpx.MapError(err)
			}
			pr, err := op.fn(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &prOut{Body: *pr}, nil
		})
	}
}

type (
	prCreateIn struct{ Body PRCreateInput }
	prOut      struct{ Body PurchaseReceipt }
	prListOut  struct{ Body prListBody }
	prListBody struct {
		Items []PurchaseReceipt `json:"items"`
	}
	prGetIn struct {
		ID string `path:"id"`
	}
)
