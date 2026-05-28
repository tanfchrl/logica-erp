package timesheet

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
		OperationID: "list-timesheets", Method: http.MethodGet,
		Path: "/projects/timesheets", Summary: "List timesheets",
		Tags: []string{"Projects / Timesheet"},
	}, func(ctx context.Context, _ *struct{}) (*tsListOut, error) {
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
		return &tsListOut{Body: tsListBody{Items: ss}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-timesheet", Method: http.MethodPost,
		Path: "/projects/timesheets", Summary: "Create a Timesheet draft",
		Tags: []string{"Projects / Timesheet"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *tsCreateIn) (*tsOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		ts, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &tsOut{Body: *ts}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-timesheet", Method: http.MethodGet,
		Path: "/projects/timesheets/{id}", Summary: "Get a Timesheet",
		Tags: []string{"Projects / Timesheet"},
	}, func(ctx context.Context, in *tsGetIn) (*tsOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		ts, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &tsOut{Body: *ts}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-timesheet", Method: http.MethodPut,
		Path: "/projects/timesheets/{id}", Summary: "Update a draft Timesheet",
		Tags: []string{"Projects / Timesheet"},
	}, func(ctx context.Context, in *tsUpdateIn) (*tsOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		ts, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &tsOut{Body: *ts}, nil
	})

	for _, op := range []struct {
		id, path, summary string
		fn                func(context.Context, string) (*Timesheet, error)
		action            permission.Action
	}{
		{"submit-timesheet", "submit", "Submit a Timesheet", h.Service.Submit, permission.ActionSubmit},
		{"cancel-timesheet", "cancel", "Cancel a Timesheet", h.Service.Cancel, permission.ActionCancel},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost,
			Path: "/projects/timesheets/{id}/" + op.path, Summary: op.summary,
			Tags: []string{"Projects / Timesheet"},
		}, func(ctx context.Context, in *tsGetIn) (*tsOut, error) {
			if err := h.Perm.Check(ctx, Doctype, op.action); err != nil {
				return nil, httpx.MapError(err)
			}
			ts, err := op.fn(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &tsOut{Body: *ts}, nil
		})
	}
}

type (
	tsCreateIn struct{ Body TSCreateInput }
	tsUpdateIn struct {
		ID   string `path:"id"`
		Body TSCreateInput
	}
	tsOut      struct{ Body Timesheet }
	tsListOut  struct{ Body tsListBody }
	tsListBody struct {
		Items []Timesheet `json:"items"`
	}
	tsGetIn struct {
		ID string `path:"id"`
	}
)
