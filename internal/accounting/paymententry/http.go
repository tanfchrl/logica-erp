package paymententry

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
		OperationID: "list-payment-entries", Method: http.MethodGet,
		Path: "/accounting/payment-entries", Summary: "List payment entries",
		Tags: []string{"Accounting / Payment Entry"},
	}, func(ctx context.Context, _ *struct{}) (*peListOut, error) {
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
		return &peListOut{Body: peListBody{Items: ps}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-payment-entry", Method: http.MethodPost,
		Path: "/accounting/payment-entries", Summary: "Create a Payment Entry draft",
		Tags: []string{"Accounting / Payment Entry"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *peCreateIn) (*peOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		pe, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &peOut{Body: *pe}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-payment-entry", Method: http.MethodGet,
		Path: "/accounting/payment-entries/{id}", Summary: "Get a Payment Entry",
		Tags: []string{"Accounting / Payment Entry"},
	}, func(ctx context.Context, in *peGetIn) (*peOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		pe, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &peOut{Body: *pe}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-payment-entry", Method: http.MethodPost,
		Path: "/accounting/payment-entries/{id}/submit", Summary: "Submit a Payment Entry (posts to GL, settles invoices)",
		Tags: []string{"Accounting / Payment Entry"},
	}, func(ctx context.Context, in *peGetIn) (*peOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		pe, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &peOut{Body: *pe}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "cancel-payment-entry", Method: http.MethodPost,
		Path: "/accounting/payment-entries/{id}/cancel", Summary: "Cancel a Payment Entry (reverses GL, restores AR)",
		Tags: []string{"Accounting / Payment Entry"},
	}, func(ctx context.Context, in *peGetIn) (*peOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCancel); err != nil {
			return nil, httpx.MapError(err)
		}
		pe, err := h.Service.Cancel(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &peOut{Body: *pe}, nil
	})
}

type (
	peCreateIn struct{ Body PaymentEntryCreateInput }
	peOut      struct{ Body PaymentEntry }
	peListOut  struct{ Body peListBody }
	peListBody struct {
		Items []PaymentEntry `json:"items"`
	}
	peGetIn struct {
		ID string `path:"id"`
	}
)
