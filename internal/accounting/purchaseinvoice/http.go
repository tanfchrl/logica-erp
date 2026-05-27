package purchaseinvoice

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
	Service *Service
	Perm    *permission.Engine
	// Optional. When both are set, /print is registered and serves the PDF
	// rendered via the admin's chosen DB template + letterhead.
	DB         *dbx.DB
	PrintAdmin *print.AdminService
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-purchase-invoices", Method: http.MethodGet,
		Path: "/accounting/purchase-invoices", Summary: "List purchase invoices",
		Tags: []string{"Accounting / Purchase Invoice"},
	}, func(ctx context.Context, _ *struct{}) (*piListOut, error) {
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
		return &piListOut{Body: piListBody{Items: ps}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "create-purchase-invoice", Method: http.MethodPost,
		Path: "/accounting/purchase-invoices", Summary: "Create a Purchase Invoice draft",
		Tags: []string{"Accounting / Purchase Invoice"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *piCreateIn) (*piOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		pi, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &piOut{Body: *pi}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-purchase-invoice", Method: http.MethodGet,
		Path: "/accounting/purchase-invoices/{id}", Summary: "Get a Purchase Invoice",
		Tags: []string{"Accounting / Purchase Invoice"},
	}, func(ctx context.Context, in *piGetIn) (*piOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		pi, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &piOut{Body: *pi}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit-purchase-invoice", Method: http.MethodPost,
		Path: "/accounting/purchase-invoices/{id}/submit", Summary: "Submit a Purchase Invoice (posts to GL)",
		Tags: []string{"Accounting / Purchase Invoice"},
	}, func(ctx context.Context, in *piGetIn) (*piOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		pi, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &piOut{Body: *pi}, nil
	})
	if h.PrintAdmin != nil && h.DB != nil {
		huma.Register(api, huma.Operation{
			OperationID: "print-purchase-invoice", Method: http.MethodGet,
			Path:        "/accounting/purchase-invoices/{id}/print",
			Summary:     "Render a Purchase Invoice to PDF using the admin's configured template",
			Tags:        []string{"Accounting / Purchase Invoice"},
		}, func(ctx context.Context, in *piGetIn) (*piPdfOut, error) {
			if err := h.Perm.Check(ctx, Doctype, permission.ActionPrint); err != nil {
				return nil, httpx.MapError(err)
			}
			pi, err := h.Service.Get(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			var supplier struct {
				DisplayName string
				NPWP        string
			}
			_ = h.DB.QueryRow(ctx,
				`SELECT display_name, coalesce(npwp,'') FROM supplier WHERE id = $1`, pi.SupplierID).
				Scan(&supplier.DisplayName, &supplier.NPWP)
			var company struct {
				LegalName, NPWP, AddressLine string
			}
			_ = h.DB.QueryRow(ctx,
				`SELECT legal_name, coalesce(npwp,''), coalesce(address_line,'') FROM company WHERE id = $1`, pi.CompanyID).
				Scan(&company.LegalName, &company.NPWP, &company.AddressLine)
			ctxMap := map[string]any{
				"Invoice":     pi,
				"Supplier":    supplier,
				"Company":     company,
				"PostingDate": pi.PostingDate.Format("2006-01-02"),
				"DueDate":     pi.DueDate.Format("2006-01-02"),
				"GeneratedAt": time.Now().UTC().Format(time.RFC3339),
			}
			resolved, err := h.PrintAdmin.Resolve(ctx, Doctype, pi.CompanyID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			if resolved == nil {
				return nil, huma.NewError(http.StatusNotFound,
					"no print template configured for purchase_invoice — create one in Settings → Print templates")
			}
			pdf, err := h.PrintAdmin.RenderToPDF(ctx, resolved, ctxMap)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &piPdfOut{
				ContentType:        "application/pdf",
				ContentDisposition: `inline; filename="` + pi.Name + `.pdf"`,
				Body:               pdf,
			}, nil
		})
	}

	huma.Register(api, huma.Operation{
		OperationID: "cancel-purchase-invoice", Method: http.MethodPost,
		Path: "/accounting/purchase-invoices/{id}/cancel", Summary: "Cancel a Purchase Invoice (posts reversing GL entries)",
		Tags: []string{"Accounting / Purchase Invoice"},
	}, func(ctx context.Context, in *piGetIn) (*piOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCancel); err != nil {
			return nil, httpx.MapError(err)
		}
		pi, err := h.Service.Cancel(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &piOut{Body: *pi}, nil
	})
}

type (
	piCreateIn struct{ Body PurchaseInvoiceCreateInput }
	piOut      struct{ Body PurchaseInvoice }
	piListOut  struct{ Body piListBody }
	piListBody struct {
		Items []PurchaseInvoice `json:"items"`
	}
	piGetIn struct {
		ID string `path:"id"`
	}
	piPdfOut struct {
		ContentType        string `header:"Content-Type"`
		ContentDisposition string `header:"Content-Disposition"`
		Body               []byte
	}
)
