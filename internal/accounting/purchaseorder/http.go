package purchaseorder

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
	// Optional. When both are set, /print is registered.
	DB         *dbx.DB
	PrintAdmin *print.AdminService
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-purchase-orders", Method: http.MethodGet,
		Path: "/accounting/purchase-orders", Summary: "List purchase orders",
		Tags: []string{"Accounting / Purchase Order"},
	}, func(ctx context.Context, _ *struct{}) (*poListOut, error) {
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
		return &poListOut{Body: poListBody{Items: ps}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-purchase-order", Method: http.MethodPost,
		Path: "/accounting/purchase-orders", Summary: "Create a Purchase Order draft",
		Tags: []string{"Accounting / Purchase Order"}, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *poCreateIn) (*poOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		po, err := h.Service.CreateDraft(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &poOut{Body: *po}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-purchase-order", Method: http.MethodGet,
		Path: "/accounting/purchase-orders/{id}", Summary: "Get a Purchase Order",
		Tags: []string{"Accounting / Purchase Order"},
	}, func(ctx context.Context, in *poGetIn) (*poOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		po, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &poOut{Body: *po}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "submit-purchase-order", Method: http.MethodPost,
		Path: "/accounting/purchase-orders/{id}/submit", Summary: "Submit a Purchase Order (commits the order; no GL impact)",
		Tags: []string{"Accounting / Purchase Order"},
	}, func(ctx context.Context, in *poGetIn) (*poOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		po, err := h.Service.Submit(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &poOut{Body: *po}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "cancel-purchase-order", Method: http.MethodPost,
		Path: "/accounting/purchase-orders/{id}/cancel", Summary: "Cancel a Purchase Order",
		Tags: []string{"Accounting / Purchase Order"},
	}, func(ctx context.Context, in *poGetIn) (*poOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCancel); err != nil {
			return nil, httpx.MapError(err)
		}
		po, err := h.Service.Cancel(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &poOut{Body: *po}, nil
	})

	for _, op := range []struct {
		id, path, summary string
		fn                func(context.Context, string) (*PurchaseOrder, error)
	}{
		{"hold-purchase-order", "hold", "Hold a Purchase Order (pause receipts/invoicing)", h.Service.Hold},
		{"close-purchase-order", "close", "Close a Purchase Order (deliberately end remaining fulfilment)", h.Service.Close},
		{"stop-purchase-order", "stop", "Stop a Purchase Order (halt further receipts/invoices)", h.Service.Stop},
		{"reopen-purchase-order", "reopen", "Reopen a held/closed/stopped Purchase Order", h.Service.Reopen},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost,
			Path:    "/accounting/purchase-orders/{id}/" + op.path,
			Summary: op.summary,
			Tags:    []string{"Accounting / Purchase Order"},
		}, func(ctx context.Context, in *poGetIn) (*poOut, error) {
			// Hold/Close/Stop/Reopen are not docstatus transitions — they
			// gate via Submit perm because they affect a Submitted doc.
			if err := h.Perm.Check(ctx, Doctype, permission.ActionSubmit); err != nil {
				return nil, httpx.MapError(err)
			}
			po, err := op.fn(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &poOut{Body: *po}, nil
		})
	}

	if h.PrintAdmin != nil && h.DB != nil {
		huma.Register(api, huma.Operation{
			OperationID: "print-purchase-order", Method: http.MethodGet,
			Path:        "/accounting/purchase-orders/{id}/print",
			Summary:     "Render a Purchase Order to PDF",
			Tags:        []string{"Accounting / Purchase Order"},
		}, func(ctx context.Context, in *poGetIn) (*poPdfOut, error) {
			if err := h.Perm.Check(ctx, Doctype, permission.ActionPrint); err != nil {
				return nil, httpx.MapError(err)
			}
			po, err := h.Service.Get(ctx, in.ID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			var supplier struct {
				DisplayName string
				NPWP        string
			}
			_ = h.DB.QueryRow(ctx,
				`SELECT display_name, coalesce(npwp,'') FROM supplier WHERE id = $1`, po.SupplierID).
				Scan(&supplier.DisplayName, &supplier.NPWP)
			var company struct {
				LegalName, NPWP, AddressLine string
			}
			_ = h.DB.QueryRow(ctx,
				`SELECT legal_name, coalesce(npwp,''), coalesce(address_line,'') FROM company WHERE id = $1`, po.CompanyID).
				Scan(&company.LegalName, &company.NPWP, &company.AddressLine)
			ctxMap := map[string]any{
				"Order":           po,
				"Supplier":        supplier,
				"Company":         company,
				"TransactionDate": po.TransactionDate.Format("2006-01-02"),
				"GeneratedAt":     time.Now().UTC().Format(time.RFC3339),
			}
			resolved, err := h.PrintAdmin.Resolve(ctx, Doctype, po.CompanyID)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			if resolved == nil {
				return nil, huma.NewError(http.StatusNotFound,
					"no print template configured for purchase_order — create one in Settings → Print templates")
			}
			pdf, err := h.PrintAdmin.RenderToPDF(ctx, resolved, ctxMap)
			if err != nil {
				return nil, httpx.MapError(err)
			}
			return &poPdfOut{
				ContentType:        "application/pdf",
				ContentDisposition: `inline; filename="` + po.Name + `.pdf"`,
				Body:               pdf,
			}, nil
		})
	}
}

type (
	poCreateIn struct{ Body POCreateInput }
	poOut      struct{ Body PurchaseOrder }
	poListOut  struct{ Body poListBody }
	poListBody struct {
		Items []PurchaseOrder `json:"items"`
	}
	poGetIn struct {
		ID string `path:"id"`
	}
	poPdfOut struct {
		ContentType        string `header:"Content-Type"`
		ContentDisposition string `header:"Content-Disposition"`
		Body               []byte
	}
)
