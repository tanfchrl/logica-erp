package salesinvoice

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/print"
)

type Handler struct {
	Service    *Service
	Perm       *permission.Engine
	DB         *dbx.DB
	PrintRenderer print.Renderer
	// PrintAdmin is optional: when set, the print endpoint will consult the
	// admin DB template/letterhead first, falling back to the bundled HTML.
	PrintAdmin *print.AdminService
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-sales-invoices", Method: http.MethodGet,
		Path: "/accounting/sales-invoices", Summary: "List sales invoices",
		Tags: []string{"Accounting / Sales Invoice"},
	}, func(ctx context.Context, _ *struct{}) (*siListOut, error) {
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
		return &siListOut{Body: siListBody{Items: ss}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-sales-invoice", Method: http.MethodPost,
		Path: "/accounting/sales-invoices", Summary: "Create a Sales Invoice draft",
		Tags: []string{"Accounting / Sales Invoice"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *siCreateIn) (*siOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		si, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &siOut{Body: *si}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-sales-invoice", Method: http.MethodGet,
		Path: "/accounting/sales-invoices/{id}", Summary: "Get a Sales Invoice",
		Tags: []string{"Accounting / Sales Invoice"},
	}, func(ctx context.Context, in *siGetIn) (*siOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		si, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &siOut{Body: *si}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-sales-invoice", Method: http.MethodPost,
		Path: "/accounting/sales-invoices/{id}/submit", Summary: "Submit a Sales Invoice (posts to GL)",
		Tags: []string{"Accounting / Sales Invoice"},
	}, func(ctx context.Context, in *siGetIn) (*siOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		si, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &siOut{Body: *si}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "cancel-sales-invoice", Method: http.MethodPost,
		Path: "/accounting/sales-invoices/{id}/cancel", Summary: "Cancel a Sales Invoice (posts reversing GL entries)",
		Tags: []string{"Accounting / Sales Invoice"},
	}, func(ctx context.Context, in *siGetIn) (*siOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCancel); err != nil {
			return nil, httpx.MapError(err)
		}
		si, err := h.Service.Cancel(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &siOut{Body: *si}, nil
	})

	if h.PrintRenderer != nil && h.DB != nil {
		huma.Register(api, huma.Operation{
			OperationID: "print-sales-invoice",
			Method:      http.MethodGet,
			Path:        "/accounting/sales-invoices/{id}/print",
			Summary:     "Render a Sales Invoice to PDF (Indonesian Faktur Pajak layout)",
			Tags:        []string{"Accounting / Sales Invoice"},
		}, func(ctx context.Context, in *siGetIn) (*pdfOut, error) {
			if err := h.Perm.Check(ctx, Doctype, permission.ActionPrint); err != nil {
				return nil, httpx.MapError(err)
			}
			si, err := h.Service.Get(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			// Build template context with customer + company joins.
			ctxMap := map[string]any{
				"Invoice":     si,
				"PostingDate": si.PostingDate.Format("2006-01-02"),
				"DueDate":     si.DueDate.Format("2006-01-02"),
				"GeneratedAt": time.Now().UTC().Format(time.RFC3339),
			}
			var customer struct {
				DisplayName string
				NPWP        string
			}
			_ = h.DB.QueryRow(ctx,
				`SELECT display_name, coalesce(npwp,'') FROM customer WHERE id = $1`, si.CustomerID).
				Scan(&customer.DisplayName, &customer.NPWP)
			ctxMap["Customer"] = customer

			var company struct {
				LegalName   string
				NPWP        string
				AddressLine string
			}
			_ = h.DB.QueryRow(ctx,
				`SELECT legal_name, coalesce(npwp,''), coalesce(address_line,'') FROM company WHERE id = $1`, si.CompanyID).
				Scan(&company.LegalName, &company.NPWP, &company.AddressLine)
			ctxMap["Company"] = company

			// 1) Prefer a custom DB template+letterhead if the admin configured one.
			var pdf []byte
			if h.PrintAdmin != nil {
				resolved, _ := h.PrintAdmin.Resolve(ctx, Doctype, si.CompanyID)
				if resolved != nil {
					ctxMap["Company"] = company
					b, rerr := h.PrintAdmin.RenderToPDF(ctx, resolved, ctxMap)
					if rerr == nil {
						pdf = b
					}
				}
			}
			// 2) Fallback to the bundled template if no admin override (or it errored).
			if pdf == nil {
				html, err := print.RenderSalesInvoiceHTML(ctxMap)
				if err != nil {
					return nil, httpx.MapError(err)
				}
				b, err := h.PrintRenderer.RenderHTML(ctx, html, print.Options{})
				if err != nil {
					return nil, httpx.MapError(err)
				}
				pdf = b
			}
			return &pdfOut{
				ContentType:        "application/pdf",
				ContentDisposition: `inline; filename="` + si.Name + `.pdf"`,
				Body:               pdf,
			}, nil
		})
	}
}

type pdfOut struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

type (
	siCreateIn struct{ Body SalesInvoiceCreateInput }
	siOut      struct{ Body SalesInvoice }
	siListOut  struct{ Body siListBody }
	siListBody struct {
		Items []SalesInvoice `json:"items"`
	}
	siGetIn struct {
		ID string `path:"id"`
	}
)
