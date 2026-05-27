package journalentry

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
		OperationID: "list-journal-entries",
		Method:      http.MethodGet,
		Path:        "/accounting/journal-entries",
		Summary:     "List journal entries",
		Tags:        []string{"Accounting / Journal Entry"},
	}, func(ctx context.Context, _ *struct{}) (*jeListOut, error) {
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
		return &jeListOut{Body: jeListBody{Items: ss}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-journal-entry",
		Method:        http.MethodPost,
		Path:          "/accounting/journal-entries",
		Summary:       "Create a Journal Entry draft",
		Tags:          []string{"Accounting / Journal Entry"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *jeCreateIn) (*jeCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		je, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &jeCreateOut{Body: *je}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-journal-entry",
		Method:      http.MethodGet,
		Path:        "/accounting/journal-entries/{id}",
		Summary:     "Get a Journal Entry",
		Tags:        []string{"Accounting / Journal Entry"},
	}, func(ctx context.Context, in *jeGetIn) (*jeCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		je, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &jeCreateOut{Body: *je}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "submit-journal-entry",
		Method:      http.MethodPost,
		Path:        "/accounting/journal-entries/{id}/submit",
		Summary:     "Submit a Journal Entry (posts to GL)",
		Tags:        []string{"Accounting / Journal Entry"},
	}, func(ctx context.Context, in *jeGetIn) (*jeCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		je, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &jeCreateOut{Body: *je}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "cancel-journal-entry",
		Method:      http.MethodPost,
		Path:        "/accounting/journal-entries/{id}/cancel",
		Summary:     "Cancel a Journal Entry (posts reversing GL entries)",
		Tags:        []string{"Accounting / Journal Entry"},
	}, func(ctx context.Context, in *jeGetIn) (*jeCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCancel); err != nil {
			return nil, httpx.MapError(err)
		}
		je, err := h.Service.Cancel(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &jeCreateOut{Body: *je}, nil
	})
}

type (
	jeCreateIn  struct{ Body JournalEntryCreateInput }
	jeCreateOut struct{ Body JournalEntry }
	jeGetIn     struct {
		ID string `path:"id"`
	}
	jeListOut  struct{ Body jeListBody }
	jeListBody struct {
		Items []JournalEntry `json:"items"`
	}
)
