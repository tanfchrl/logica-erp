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
	// Run executes the tool. erpcc carries the user's JWT + company id.
	// args is the raw arguments JSON from the LLM tool_call. Returns a
	// JSON-serialisable result (string, map, slice, etc.) or an error.
	Run func(ctx context.Context, erpcc erpclient.CallContext, args json.RawMessage) (any, error)
}

// Registry holds all loaded tools keyed by name.
type Registry struct {
	erp      *erpclient.Client
	registry *agentcontract.Registry
	tools    map[string]Tool
}

func New(erp *erpclient.Client, contractReg *agentcontract.Registry) *Registry {
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
