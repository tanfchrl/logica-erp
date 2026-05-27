// Package tools is the agent's tool registry. Tools are the concrete
// functions the LLM can invoke; each maps to an ERP REST endpoint and is
// classified by autonomy tier (read / draft / submit).
//
// Phase B ships a small fixed catalog of Tier-0 read tools that work for
// every doctype that exposes a list + get-by-id pair, plus a handful of
// well-known reports. Per-doctype Tier-1 drafting tools land in Phase C
// — they need richer schema metadata than the OpenAPI spec exposes today.
//
// Each tool implements a single Run method that takes the LLM-supplied
// JSON arguments and the user's auth context, calls the ERP API, and
// returns a JSON-serialisable result that gets fed back to the model as a
// tool-result message.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/tandigital/logica-erp/internal/agent/erpclient"
	"github.com/tandigital/logica-erp/internal/agent/llm"
	"github.com/tandigital/logica-erp/internal/agent/policy"
	"github.com/tandigital/logica-erp/internal/agentcontract"
)

// Tool is one callable function visible to the LLM.
type Tool struct {
	Name        string
	Description string
	Tier        policy.Tier
	// JSONSchema is the parameters block sent to the model. Must match
	// OpenAI function-calling shape: type=object, properties, required.
	JSONSchema map[string]any
	// TargetDoctype, when non-empty, is the doctype the policy gate keys
	// on for this tool — overrides any `doctype` field read from runtime
	// args. Composed tools (create_draft_payment_for_invoice, etc.) use
	// this to declare their write target without exposing it to the LLM.
	TargetDoctype string
	// Run executes the tool. erpcc carries the user's JWT + company id.
	// args is the raw arguments JSON from the LLM tool_call. Returns a
	// JSON-serialisable result (string, map, slice, etc.) or an error.
	Run func(ctx context.Context, erpcc erpclient.CallContext, args json.RawMessage) (any, error)
}

// ERPDoer is the slice of erpclient.Client the tool registry actually uses.
// Defined as an interface so tests can swap in a fake without spinning up
// an HTTP server. The concrete *erpclient.Client trivially satisfies it.
type ERPDoer interface {
	Do(ctx context.Context, cc erpclient.CallContext, method, path string, body any, out any) error
}

// Registry holds all loaded tools keyed by name.
type Registry struct {
	erp      ERPDoer
	registry *agentcontract.Registry
	tools    map[string]Tool
}

func New(erp ERPDoer, contractReg *agentcontract.Registry) *Registry {
	r := &Registry{erp: erp, registry: contractReg, tools: map[string]Tool{}}
	r.registerBuiltins()
	return r
}

// Names returns the registered tool names — used by ⌘K / debug surfaces.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	return out
}

// Lookup returns a tool by name, or false.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// LLMTools returns the OpenAI-compatible function descriptors. This is what
// the orchestrator hands to llm.Client.Chat() as `tools`.
func (r *Registry) LLMTools() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.JSONSchema,
			},
		})
	}
	return out
}

// registerBuiltins wires the Phase-B tool catalog. New tools are added by
// editing this method — keeping the catalog small and reviewable in v1.
// Per-doctype tools come later by walking the contract registry.
func (r *Registry) registerBuiltins() {
	r.add(Tool{
		Name: "list_documents",
		Description: "List documents of a given doctype. Optional filters narrow the list. " +
			"Use this when the user asks about multiple records (e.g. 'sales invoices this month').",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("doctype", "string", "Canonical doctype slug from the AGENT_CONTRACT.md (e.g. sales_invoice)"),
			required("doctype"),
		),
		Run: r.runListDocuments,
	})
	r.add(Tool{
		Name: "get_document",
		Description: "Fetch one document by id. Use after list_documents has identified the record.",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("doctype", "string", "Canonical doctype slug (e.g. sales_invoice)"),
			schemaProp("id", "string", "The document ULID (e.g. si_01ABC...)"),
			required("doctype", "id"),
		),
		Run: r.runGetDocument,
	})
	r.add(Tool{
		Name: "get_timeline",
		Description: "Return the event/view/comment/approval timeline for one document.",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("doctype", "string", "Canonical doctype slug"),
			schemaProp("document_id", "string", "The document ULID"),
			required("doctype", "document_id"),
		),
		Run: r.runGetTimeline,
	})
	r.add(Tool{
		Name: "report_ar_aging",
		Description: "Run the Accounts Receivable Ageing report. Defaults to as_of=today.",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("as_of", "string", "YYYY-MM-DD; defaults to today"),
		),
		Run: r.runReportAR,
	})
	r.add(Tool{
		Name: "report_ap_aging",
		Description: "Run the Accounts Payable Ageing report. Defaults to as_of=today.",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("as_of", "string", "YYYY-MM-DD; defaults to today"),
		),
		Run: r.runReportAP,
	})
	r.add(Tool{
		Name: "report_balance_sheet",
		Description: "Run the Balance Sheet report as of a given date (defaults to today).",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("as_of", "string", "YYYY-MM-DD; defaults to today"),
		),
		Run: r.runReportBS,
	})
	r.add(Tool{
		Name: "report_profit_and_loss",
		Description: "Run the Profit & Loss report between two dates.",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("from_date", "string", "Period start, YYYY-MM-DD"),
			schemaProp("to_date", "string", "Period end, YYYY-MM-DD"),
			required("from_date", "to_date"),
		),
		Run: r.runReportPL,
	})
	r.add(Tool{
		Name: "global_search",
		Description: "Search across all indexed documents by free-text query. Use when the user mentions a record by partial name.",
		Tier: policy.Tier0,
		JSONSchema: schemaObject(
			schemaProp("q", "string", "Search query (matches document title/name/body)"),
			required("q"),
		),
		Run: r.runSearch,
	})
	r.add(Tool{
		Name: "create_draft",
		Description: "Create a new draft document (docstatus=0). Use only after you have all required fields. " +
			"The document is NOT submitted — a human reviews it and submits manually. Tier-1.",
		Tier: policy.Tier1,
		JSONSchema: schemaObject(
			schemaProp("doctype", "string", "Canonical doctype slug (e.g. sales_invoice). Must be in an AGENT_CONTRACT.md with create_draft in tier1_tools."),
			schemaProp("payload", "object", "The document fields, shape matching the doctype's CreateInput."),
			required("doctype", "payload"),
		),
		Run: r.runCreateDraft,
	})

	// Composed Tier-1 tools — higher-level than create_draft because they
	// fetch the source document, derive the payload, and POST in one step.
	// Closes spec §5 use cases for "buat X dari Y" prompts.
	r.add(Tool{
		Name: "create_draft_payment_for_invoice",
		Description: "Given a sales-invoice or purchase-invoice id, draft a Payment Entry that settles the invoice's outstanding amount. " +
			"Returns the new draft id. Tier-1.",
		Tier:          policy.Tier1,
		TargetDoctype: "payment_entry",
		JSONSchema: schemaObject(
			schemaProp("invoice_id", "string", "ID of the sales_invoice (si_...) or purchase_invoice (pi_...) to settle"),
			schemaProp("paid_from_account_id", "string", "Account paying (Bank/Kas for receipts; AP for payments)"),
			schemaProp("paid_to_account_id", "string", "Account receiving (AR for receipts; Bank/Kas for payments)"),
			schemaProp("paid_amount", "string", "Optional — defaults to the invoice's outstanding_amount"),
			required("invoice_id", "paid_from_account_id", "paid_to_account_id"),
		),
		Run: r.runCreateDraftPaymentForInvoice,
	})

	r.add(Tool{
		Name: "create_draft_credit_note_from_invoice",
		Description: "Given a submitted sales_invoice id, draft a credit note (is_return=true SI) that reverses its items. " +
			"Useful for refunds and returns. Tier-1.",
		Tier:          policy.Tier1,
		TargetDoctype: "sales_invoice",
		JSONSchema: schemaObject(
			schemaProp("sales_invoice_id", "string", "ID of the source sales invoice"),
			schemaProp("reason", "string", "Optional free-text note that becomes the new SI's remarks"),
			required("sales_invoice_id"),
		),
		Run: r.runCreateDraftCreditNoteFromInvoice,
	})
}

func (r *Registry) add(t Tool) { r.tools[t.Name] = t }

// ---- Tool implementations ----

func (r *Registry) runListDocuments(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct{ Doctype string `json:"doctype"` }
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Doctype == "" {
		return nil, errors.New("doctype is required")
	}
	_, doc, ok := r.registry.FindByDoctype(a.Doctype)
	if !ok {
		return nil, fmt.Errorf("doctype %q not declared in any AGENT_CONTRACT.md", a.Doctype)
	}
	var out map[string]any
	if err := r.erp.Do(ctx, cc, "GET", doc.APIPath, nil, &out); err != nil {
		return nil, err
	}
	// Truncate items to a small preview — the LLM doesn't need every field;
	// it can drill in via get_document. This also keeps token spend bounded.
	if items, ok := out["items"].([]any); ok {
		out["items"] = trimItems(items, 20)
		out["truncated"] = len(items) > 20
		out["total_returned"] = len(items)
	}
	return out, nil
}

func (r *Registry) runGetDocument(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct {
		Doctype string `json:"doctype"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Doctype == "" || a.ID == "" {
		return nil, errors.New("doctype and id are required")
	}
	_, doc, ok := r.registry.FindByDoctype(a.Doctype)
	if !ok {
		return nil, fmt.Errorf("doctype %q not declared", a.Doctype)
	}
	var out map[string]any
	if err := r.erp.Do(ctx, cc, "GET", doc.APIPath+"/"+a.ID, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Registry) runGetTimeline(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct {
		Doctype    string `json:"doctype"`
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("doctype", a.Doctype)
	q.Set("document_id", a.DocumentID)
	var out map[string]any
	if err := r.erp.Do(ctx, cc, "GET", "/platform/timeline?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Registry) runReportAR(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	return r.runReport(ctx, cc, args, "/accounting/reports/accounts-receivable-ageing")
}

func (r *Registry) runReportAP(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	return r.runReport(ctx, cc, args, "/accounting/reports/accounts-payable-ageing")
}

func (r *Registry) runReportBS(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	return r.runReport(ctx, cc, args, "/accounting/reports/balance-sheet")
}

func (r *Registry) runReportPL(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct {
		FromDate string `json:"from_date"`
		ToDate   string `json:"to_date"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	q := url.Values{}
	if a.FromDate != "" {
		q.Set("from_date", a.FromDate)
	}
	if a.ToDate != "" {
		q.Set("to_date", a.ToDate)
	}
	var out map[string]any
	if err := r.erp.Do(ctx, cc, "GET", "/accounting/reports/profit-and-loss?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Registry) runReport(ctx context.Context, cc erpclient.CallContext, args json.RawMessage, path string) (any, error) {
	var a struct {
		AsOf string `json:"as_of"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	q := url.Values{}
	if a.AsOf != "" {
		q.Set("as_of", a.AsOf)
	}
	var out map[string]any
	full := path
	if enc := q.Encode(); enc != "" {
		full += "?" + enc
	}
	if err := r.erp.Do(ctx, cc, "GET", full, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// runCreateDraft POSTs the supplied payload to the doctype's api_path with
// no submit. Returns the created document id + name so the orchestrator can
// surface them as a proposal and enqueue an agent_approval_queue row.
func (r *Registry) runCreateDraft(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct {
		Doctype string         `json:"doctype"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Doctype == "" {
		return nil, errors.New("doctype is required")
	}
	if len(a.Payload) == 0 {
		return nil, errors.New("payload is required")
	}
	_, doc, ok := r.registry.FindByDoctype(a.Doctype)
	if !ok {
		return nil, fmt.Errorf("doctype %q not declared", a.Doctype)
	}
	var out map[string]any
	if err := r.erp.Do(ctx, cc, "POST", doc.APIPath, a.Payload, &out); err != nil {
		return nil, err
	}
	// Return the small fields the orchestrator + audit log care about.
	pick := map[string]any{
		"id":      out["id"],
		"name":    out["name"],
		"doctype": a.Doctype,
		"path":    doc.APIPath,
	}
	return pick, nil
}

// runCreateDraftPaymentForInvoice composes a Payment Entry from a SI or PI.
// Choosing payment_type, paid_from/to defaults, and the references[] entry
// are mechanical given the source document — exactly the kind of work the
// generic create_draft would force the model to figure out from scratch.
func (r *Registry) runCreateDraftPaymentForInvoice(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct {
		InvoiceID          string `json:"invoice_id"`
		PaidFromAccountID  string `json:"paid_from_account_id"`
		PaidToAccountID    string `json:"paid_to_account_id"`
		PaidAmount         string `json:"paid_amount"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.InvoiceID == "" || a.PaidFromAccountID == "" || a.PaidToAccountID == "" {
		return nil, errors.New("invoice_id, paid_from_account_id, paid_to_account_id required")
	}

	// Resolve source-doctype + endpoint from the id prefix. We don't lean on
	// the contract registry here because the operation is intrinsic to the
	// id shape (si_... / pi_...) and registry lookup adds nothing.
	var (
		srcPath      string
		paymentType  string
		partyType    string
		refDoctype   string
	)
	switch {
	case strings.HasPrefix(a.InvoiceID, "si_"):
		srcPath, paymentType, partyType, refDoctype = "/accounting/sales-invoices", "receive", "customer", "sales_invoice"
	case strings.HasPrefix(a.InvoiceID, "pi_"):
		srcPath, paymentType, partyType, refDoctype = "/accounting/purchase-invoices", "pay", "supplier", "purchase_invoice"
	default:
		return nil, fmt.Errorf("invoice_id %q must start with si_ or pi_", a.InvoiceID)
	}

	var src map[string]any
	if err := r.erp.Do(ctx, cc, "GET", srcPath+"/"+a.InvoiceID, nil, &src); err != nil {
		return nil, fmt.Errorf("fetch source invoice: %w", err)
	}

	outstanding, _ := src["outstanding_amount"].(string)
	if a.PaidAmount == "" {
		a.PaidAmount = outstanding
	}
	postingDate, _ := src["posting_date"].(string)
	if len(postingDate) >= 10 {
		postingDate = postingDate[:10] // strip the trailing T00:00:00Z
	}

	partyIDField := "customer_id"
	if partyType == "supplier" {
		partyIDField = "supplier_id"
	}
	partyID, _ := src[partyIDField].(string)

	payload := map[string]any{
		"payment_type":          paymentType,
		"party_type":            partyType,
		"party_id":              partyID,
		"posting_date":          postingDate,
		"paid_from_account_id":  a.PaidFromAccountID,
		"paid_to_account_id":    a.PaidToAccountID,
		"paid_amount":           a.PaidAmount,
		"references": []map[string]any{{
			"reference_doctype": refDoctype,
			"reference_id":      a.InvoiceID,
			"allocated_amount":  a.PaidAmount,
		}},
	}

	var out map[string]any
	if err := r.erp.Do(ctx, cc, "POST", "/accounting/payment-entries", payload, &out); err != nil {
		return nil, err
	}
	return map[string]any{
		"id":              out["id"],
		"name":            out["name"],
		"doctype":         "payment_entry",
		"path":            "/accounting/payment-entries",
		"source_invoice":  a.InvoiceID,
		"settles_amount":  a.PaidAmount,
	}, nil
}

// runCreateDraftCreditNoteFromInvoice mirrors a submitted SI as a return.
// Lines come from the source; amounts go through unchanged — the
// is_return + return_against fields are what flip the GL impact.
func (r *Registry) runCreateDraftCreditNoteFromInvoice(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct {
		SalesInvoiceID string `json:"sales_invoice_id"`
		Reason         string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.SalesInvoiceID == "" {
		return nil, errors.New("sales_invoice_id required")
	}
	if !strings.HasPrefix(a.SalesInvoiceID, "si_") {
		return nil, fmt.Errorf("sales_invoice_id %q must start with si_", a.SalesInvoiceID)
	}

	var src map[string]any
	if err := r.erp.Do(ctx, cc, "GET", "/accounting/sales-invoices/"+a.SalesInvoiceID, nil, &src); err != nil {
		return nil, fmt.Errorf("fetch source invoice: %w", err)
	}

	srcItems, _ := src["items"].([]any)
	lines := make([]map[string]any, 0, len(srcItems))
	for _, it := range srcItems {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		lines = append(lines, map[string]any{
			"item_id":             m["item_id"],
			"item_code":           m["item_code"],
			"item_name":           m["item_name"],
			"qty":                 m["qty"],
			"rate":                m["rate"],
			"uom":                 m["uom"],
			"income_account_id":   m["income_account_id"],
			"description":         m["description"],
		})
	}

	postingDate, _ := src["posting_date"].(string)
	if len(postingDate) >= 10 {
		postingDate = postingDate[:10]
	}

	payload := map[string]any{
		"customer_id":           src["customer_id"],
		"posting_date":          postingDate,
		"due_date":              postingDate, // credit note settles same-day by convention
		"receivable_account_id": src["receivable_account_id"],
		"tax_template_id":       src["tax_template_id"],
		"is_return":             true,
		"return_against":        a.SalesInvoiceID,
		"items":                 lines,
		"remarks":               a.Reason,
	}

	var out map[string]any
	if err := r.erp.Do(ctx, cc, "POST", "/accounting/sales-invoices", payload, &out); err != nil {
		return nil, err
	}
	return map[string]any{
		"id":                out["id"],
		"name":              out["name"],
		"doctype":           "sales_invoice",
		"path":              "/accounting/sales-invoices",
		"return_against":    a.SalesInvoiceID,
	}, nil
}

func (r *Registry) runSearch(ctx context.Context, cc erpclient.CallContext, args json.RawMessage) (any, error) {
	var a struct {
		Q string `json:"q"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Q) == "" {
		return nil, errors.New("q is required")
	}
	q := url.Values{}
	q.Set("q", a.Q)
	q.Set("limit", "10")
	var out map[string]any
	if err := r.erp.Do(ctx, cc, "GET", "/search?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- schema helpers ----

func schemaObject(props ...any) map[string]any {
	m := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	var req []string
	for _, p := range props {
		switch v := p.(type) {
		case schemaProperty:
			m["properties"].(map[string]any)[v.Name] = map[string]any{
				"type":        v.Type,
				"description": v.Description,
			}
		case requiredList:
			req = append(req, v...)
		}
	}
	if len(req) > 0 {
		m["required"] = req
	}
	return m
}

type schemaProperty struct {
	Name        string
	Type        string
	Description string
}

func schemaProp(name, typ, desc string) schemaProperty {
	return schemaProperty{Name: name, Type: typ, Description: desc}
}

type requiredList []string

func required(names ...string) requiredList { return requiredList(names) }

func trimItems(items []any, max int) []any {
	if len(items) <= max {
		return items
	}
	return items[:max]
}
