package print

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"time"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

var tpl = template.Must(template.ParseFS(templateFS, "templates/*.gohtml"))

// BundledTemplateSource returns the raw text of the bundled template for the
// given doctype. Used by the admin UI as the starting point when a user
// chooses "Customize". Returns ("", false) if no bundled template exists.
func BundledTemplateSource(doctype string) (string, bool) {
	name := "templates/" + doctype + ".gohtml"
	b, err := templateFS.ReadFile(name)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// RenderSalesInvoice executes the sales_invoice template against the provided context map.
// The caller passes Invoice / Customer / Company / PostingDate / DueDate fields directly.
func RenderSalesInvoiceHTML(ctx map[string]any) ([]byte, error) {
	if ctx["GeneratedAt"] == nil {
		ctx["GeneratedAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "sales_invoice.gohtml", ctx); err != nil {
		return nil, fmt.Errorf("print: render sales_invoice: %w", err)
	}
	return buf.Bytes(), nil
}
