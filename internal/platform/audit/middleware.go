package audit

import (
	"net/http"
	"strings"

	"github.com/tandigital/logica-erp/internal/platform/auth"
)

// urlSlugToDoctype maps the "plural" URL slug (the last-but-one segment of a
// detail URL like /accounting/customers/{id}) to the singular doctype name
// used by audit/permission systems. Keep this in lockstep with each pkg's
// Doctype const.
var urlSlugToDoctype = map[string]string{
	"customers":         "customer",
	"suppliers":         "supplier",
	"items":             "item",
	"accounts":          "account",
	"tax-templates":     "tax_template",
	"sales-invoices":    "sales_invoice",
	"purchase-invoices": "purchase_invoice",
	"journal-entries":   "journal_entry",
	"payment-entries":   "payment_entry",
	"employees":         "employee",
	"warehouses":        "warehouse",
	"leads":             "lead",
	"projects":          "project",
	"issues":            "issue",
	"boms":              "bom",
	"work-orders":       "work_order",
	"assets":            "asset",
}

// RecordViewMiddleware wraps every GET request and, after the response has
// been written, records a doc_view row if the URL matches a detail pattern
// like /{module}/{plural-slug}/{id}. Fire-and-forget; failures land in slog
// but never affect the user-facing response.
func RecordViewMiddleware(rec *ViewRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := &statusCapture{ResponseWriter: w, status: 200}
			next.ServeHTTP(ww, r)
			if r.Method != http.MethodGet || ww.status >= 300 {
				return
			}
			doctype, id, ok := matchDetailURL(r.URL.Path)
			if !ok {
				return
			}
			p := auth.FromContext(r.Context())
			if p == nil {
				return
			}
			rec.Record(r.Context(), doctype, id, p.UserID)
		})
	}
}

// matchDetailURL extracts (doctype, id) from a detail URL. Returns ok=false
// for anything that isn't a recognized /{module}/{slug}/{id} shape — list
// pages, action endpoints (/submit, /cancel, etc.), and unknown slugs are
// all rejected.
func matchDetailURL(path string) (doctype, id string, ok bool) {
	// Strip the /api/v1 prefix.
	if strings.HasPrefix(path, "/api/v1") {
		path = path[len("/api/v1"):]
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 3 {
		// Need exactly: module, slug, id. /assets/assets/{id} also matches.
		return "", "", false
	}
	slug := parts[1]
	id = parts[2]
	if id == "" || strings.HasPrefix(id, "by-") {
		return "", "", false
	}
	doctype, ok = urlSlugToDoctype[slug]
	if !ok {
		return "", "", false
	}
	return doctype, id, true
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
