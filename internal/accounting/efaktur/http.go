package efaktur

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

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
		OperationID: "efaktur-csv-export",
		Method:      http.MethodGet,
		Path:        "/accounting/exports/efaktur",
		Summary:     "Export submitted Sales Invoices in e-Faktur CSV format",
		Tags:        []string{"Accounting / Exports"},
	}, func(ctx context.Context, in *efIn) (*efOut, error) {
		if err := h.Perm.Check(ctx, "sales_invoice", permission.ActionExport); err != nil {
			return nil, httpx.MapError(err)
		}
		co := in.CompanyID
		if co == "" {
			co = auth.CompanyFromContext(ctx)
		}
		if co == "" {
			return nil, huma.NewError(http.StatusBadRequest, "company_id or X-Company-Id required")
		}
		from, err := time.Parse("2006-01-02", in.FromDate)
		if err != nil {
			return nil, huma.NewError(http.StatusBadRequest, "from_date: "+err.Error())
		}
		to, err := time.Parse("2006-01-02", in.ToDate)
		if err != nil {
			return nil, huma.NewError(http.StatusBadRequest, "to_date: "+err.Error())
		}
		var buf bytes.Buffer
		n, err := h.Service.Write(ctx, &buf, co, from, to)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &efOut{
			ContentType:        "text/csv; charset=utf-8",
			ContentDisposition: fmt.Sprintf(`attachment; filename="efaktur_%s_%s.csv"`, from.Format("20060102"), to.Format("20060102")),
			RowCount:           fmt.Sprintf("%d", n),
			Body:               buf.Bytes(),
		}, nil
	})
}

type efIn struct {
	CompanyID string `query:"company_id"`
	FromDate  string `query:"from_date" required:"true"`
	ToDate    string `query:"to_date"   required:"true"`
}
type efOut struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	RowCount           string `header:"X-Row-Count"`
	Body               []byte
}
