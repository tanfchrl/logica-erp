package company

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-companies",
		Method:      http.MethodGet,
		Path:        "/accounting/companies",
		Summary:     "List companies the caller can access",
		Tags:        []string{"Accounting / Company"},
	}, func(ctx context.Context, _ *struct{}) (*companyListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		cs, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &companyListOut{Body: CompanyList{Items: cs}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-company",
		Method:        http.MethodPost,
		Path:          "/accounting/companies",
		Summary:       "Create a company",
		Tags:          []string{"Accounting / Company"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *companyCreateIn) (*companyCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &companyCreateOut{Body: *c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-company",
		Method:      http.MethodGet,
		Path:        "/accounting/companies/{id}",
		Summary:     "Get a company",
		Tags:        []string{"Accounting / Company"},
	}, func(ctx context.Context, in *companyGetIn) (*companyCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		c, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &companyCreateOut{Body: *c}, nil
	})
}

type (
	companyCreateIn  struct{ Body CompanyCreateInput }
	companyCreateOut struct{ Body Company }
	companyListOut   struct{ Body CompanyList }
	CompanyList  struct {
		Items []Company `json:"items"`
	}
	companyGetIn struct {
		ID string `path:"id"`
	}
)
