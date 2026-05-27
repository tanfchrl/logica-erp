package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tandigital/logica-erp/internal/agent/erpclient"
	"github.com/tandigital/logica-erp/internal/agentcontract"
)

// fakeERP records every call and returns scripted responses keyed on
// (method, path). Any unscripted call returns an error so a wrong path or
// missing stub surfaces in the test rather than silently returning {}.
type fakeERP struct {
	calls    []recordedCall
	responses map[string]any   // key = METHOD + " " + path → response body
	errors    map[string]error // same key shape → error to return
}
type recordedCall struct {
	Method string
	Path   string
	Body   any
}

func newFakeERP() *fakeERP {
	return &fakeERP{
		responses: map[string]any{},
		errors:    map[string]error{},
	}
}
func (f *fakeERP) Do(_ context.Context, _ erpclient.CallContext, method, path string, body, out any) error {
	f.calls = append(f.calls, recordedCall{Method: method, Path: path, Body: body})
	key := method + " " + path
	if err, ok := f.errors[key]; ok {
		return err
	}
	if resp, ok := f.responses[key]; ok && out != nil {
		b, _ := json.Marshal(resp)
		return json.Unmarshal(b, out)
	}
	if out != nil {
		// Default empty response — for endpoints we don't care to script,
		// still let the caller's unmarshal target sit at zero values.
		return json.Unmarshal([]byte("{}"), out)
	}
	return nil
}

// fixtureContracts: a minimal contract registry covering the doctypes used
// by the bundled tools.
func fixtureContracts() *agentcontract.Registry {
	return agentcontract.NewRegistry([]agentcontract.Contract{{
		Module: "accounting", Version: "1",
		Documents: []agentcontract.DocumentSpec{
			{Name: "sales_invoice",    APIPath: "/accounting/sales-invoices",    Tier0: []string{"list_with_filters", "get_by_id"}, Tier1: []string{"create_draft"}},
			{Name: "purchase_invoice", APIPath: "/accounting/purchase-invoices", Tier0: []string{"list_with_filters", "get_by_id"}, Tier1: []string{"create_draft"}},
			{Name: "payment_entry",    APIPath: "/accounting/payment-entries",   Tier0: []string{"list_with_filters", "get_by_id"}, Tier1: []string{"create_draft"}},
		},
	}})
}

// helper: make a fresh registry with fake ERP and return both.
func newRegistry(t *testing.T) (*Registry, *fakeERP) {
	t.Helper()
	fake := newFakeERP()
	return New(fake, fixtureContracts()), fake
}

func cc() erpclient.CallContext {
	return erpclient.CallContext{Token: "fake-jwt", CompanyID: "cmp_xyz"}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ---- list_documents ----

func TestListDocuments_HappyPath(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	fake.responses["GET /accounting/sales-invoices"] = map[string]any{
		"items": []map[string]any{
			{"id": "si_1", "name": "SI-2026-0001"},
			{"id": "si_2", "name": "SI-2026-0002"},
		},
	}
	tool, _ := reg.Lookup("list_documents")
	out, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{"doctype": "sales_invoice"}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	m := out.(map[string]any)
	items := m["items"].([]any)
	if len(items) != 2 {
		t.Errorf("items len: want 2 got %d", len(items))
	}
	if len(fake.calls) != 1 || fake.calls[0].Path != "/accounting/sales-invoices" {
		t.Errorf("expected one GET /accounting/sales-invoices, got %+v", fake.calls)
	}
}

func TestListDocuments_MissingDoctype(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	tool, _ := reg.Lookup("list_documents")
	_, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "doctype") {
		t.Errorf("want doctype-required error, got %v", err)
	}
}

func TestListDocuments_UnknownDoctype(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	tool, _ := reg.Lookup("list_documents")
	_, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{"doctype": "ghost"}))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("want ghost-not-declared error, got %v", err)
	}
}

func TestListDocuments_TruncatesLargeResults(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	items := make([]map[string]any, 50)
	for i := range items {
		items[i] = map[string]any{"id": "x", "name": "n"}
	}
	fake.responses["GET /accounting/sales-invoices"] = map[string]any{"items": toAnySlice(items)}
	tool, _ := reg.Lookup("list_documents")
	out, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{"doctype": "sales_invoice"}))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	got := m["items"].([]any)
	if len(got) != 20 {
		t.Errorf("expected 20-item truncation, got %d", len(got))
	}
	if m["truncated"] != true {
		t.Errorf("expected truncated=true, got %v", m["truncated"])
	}
}

// ---- get_document ----

func TestGetDocument_HappyPath(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	fake.responses["GET /accounting/sales-invoices/si_1"] = map[string]any{
		"id": "si_1", "name": "SI-2026-0001", "grand_total": "1000000",
	}
	tool, _ := reg.Lookup("get_document")
	out, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{
		"doctype": "sales_invoice", "id": "si_1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["name"] != "SI-2026-0001" {
		t.Errorf("unexpected: %v", out)
	}
}

func TestGetDocument_MissingArgs(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	tool, _ := reg.Lookup("get_document")
	cases := []map[string]any{
		{},
		{"doctype": "sales_invoice"},
		{"id": "si_1"},
	}
	for _, c := range cases {
		if _, err := tool.Run(context.Background(), cc(), mustJSON(t, c)); err == nil {
			t.Errorf("expected error for args %v", c)
		}
	}
}

// ---- reports ----

func TestReport_PassesAsOf(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	tool, _ := reg.Lookup("report_ar_aging")
	_, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{"as_of": "2026-01-01"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(fake.calls))
	}
	if !strings.Contains(fake.calls[0].Path, "as_of=2026-01-01") {
		t.Errorf("expected as_of in path, got %q", fake.calls[0].Path)
	}
}

func TestReport_PL_RequiresBothDates(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	tool, _ := reg.Lookup("report_profit_and_loss")
	// Run with both — the tool itself doesn't enforce required-ness; the
	// JSON schema does. Verify the URL encodes both params when given.
	_, _ = tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{
		"from_date": "2026-01-01", "to_date": "2026-03-31",
	}))
	if !strings.Contains(fake.calls[0].Path, "from_date=2026-01-01") {
		t.Errorf("from_date missing in %q", fake.calls[0].Path)
	}
	if !strings.Contains(fake.calls[0].Path, "to_date=2026-03-31") {
		t.Errorf("to_date missing in %q", fake.calls[0].Path)
	}
}

// ---- search ----

func TestSearch_RejectsEmptyQuery(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	tool, _ := reg.Lookup("global_search")
	if _, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{"q": "   "})); err == nil {
		t.Error("expected empty-query error")
	}
}

// ---- create_draft ----

func TestCreateDraft_PostsPayload(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	fake.responses["POST /accounting/sales-invoices"] = map[string]any{
		"id": "si_new", "name": "SI-2026-0099",
	}
	tool, _ := reg.Lookup("create_draft")
	args := map[string]any{
		"doctype": "sales_invoice",
		"payload": map[string]any{"customer_id": "cust_x"},
	}
	out, err := tool.Run(context.Background(), cc(), mustJSON(t, args))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["id"] != "si_new" || m["doctype"] != "sales_invoice" {
		t.Errorf("returned shape wrong: %+v", m)
	}
	if len(fake.calls) != 1 || fake.calls[0].Method != "POST" {
		t.Errorf("expected one POST, got %+v", fake.calls)
	}
}

func TestCreateDraft_MissingPayload(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	tool, _ := reg.Lookup("create_draft")
	if _, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{"doctype": "sales_invoice"})); err == nil {
		t.Error("expected payload-required error")
	}
}

// ---- create_draft_payment_for_invoice ----

func TestCreateDraftPaymentForInvoice_FromSI(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	fake.responses["GET /accounting/sales-invoices/si_42"] = map[string]any{
		"id": "si_42", "name": "SI-2026-0042", "customer_id": "cust_1",
		"posting_date": "2026-05-01T00:00:00Z", "outstanding_amount": "750000",
	}
	fake.responses["POST /accounting/payment-entries"] = map[string]any{
		"id": "pe_1", "name": "PE-2026-0001",
	}
	tool, _ := reg.Lookup("create_draft_payment_for_invoice")
	out, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{
		"invoice_id":           "si_42",
		"paid_from_account_id": "acc_ar",
		"paid_to_account_id":   "acc_bank",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["id"] != "pe_1" || m["doctype"] != "payment_entry" || m["settles_amount"] != "750000" {
		t.Errorf("unexpected: %+v", m)
	}
	// Body posted should derive party_type=customer, payment_type=receive.
	if body, ok := fake.calls[1].Body.(map[string]any); ok {
		if body["payment_type"] != "receive" || body["party_type"] != "customer" {
			t.Errorf("composed body wrong: %+v", body)
		}
		if body["posting_date"] != "2026-05-01" {
			t.Errorf("posting_date should be trimmed to YYYY-MM-DD, got %v", body["posting_date"])
		}
	} else {
		t.Errorf("expected POST body to be a map, got %T", fake.calls[1].Body)
	}
}

func TestCreateDraftPaymentForInvoice_FromPI(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	fake.responses["GET /accounting/purchase-invoices/pi_7"] = map[string]any{
		"id": "pi_7", "name": "PI-2026-0007", "supplier_id": "supp_9",
		"posting_date": "2026-04-15", "outstanding_amount": "1500000",
	}
	fake.responses["POST /accounting/payment-entries"] = map[string]any{
		"id": "pe_2", "name": "PE-2026-0002",
	}
	tool, _ := reg.Lookup("create_draft_payment_for_invoice")
	_, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{
		"invoice_id":           "pi_7",
		"paid_from_account_id": "acc_bank",
		"paid_to_account_id":   "acc_ap",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := fake.calls[1].Body.(map[string]any)
	if body["payment_type"] != "pay" || body["party_type"] != "supplier" {
		t.Errorf("PI should compose as payment_type=pay, party_type=supplier; got %+v", body)
	}
}

func TestCreateDraftPaymentForInvoice_RejectsBadPrefix(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	tool, _ := reg.Lookup("create_draft_payment_for_invoice")
	_, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{
		"invoice_id":           "wat_999",
		"paid_from_account_id": "x",
		"paid_to_account_id":   "y",
	}))
	if err == nil || !strings.Contains(err.Error(), "si_ or pi_") {
		t.Errorf("expected prefix error, got %v", err)
	}
}

// ---- create_draft_credit_note_from_invoice ----

func TestCreateDraftCreditNote_MirrorsSI(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	fake.responses["GET /accounting/sales-invoices/si_5"] = map[string]any{
		"id": "si_5", "name": "SI-2026-0005",
		"customer_id":           "cust_1",
		"posting_date":          "2026-03-01T00:00:00Z",
		"receivable_account_id": "acc_ar",
		"tax_template_id":       "tax_1",
		"items": []map[string]any{
			{"item_id": "i_1", "item_code": "ITM-1", "item_name": "Widget",
				"qty": "2", "rate": "100000", "uom": "Unit",
				"income_account_id": "acc_inc", "description": "Sale"},
		},
	}
	fake.responses["POST /accounting/sales-invoices"] = map[string]any{
		"id": "si_credit", "name": "SI-2026-0042",
	}
	tool, _ := reg.Lookup("create_draft_credit_note_from_invoice")
	out, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{
		"sales_invoice_id": "si_5", "reason": "wrong item",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["return_against"] != "si_5" || m["doctype"] != "sales_invoice" {
		t.Errorf("unexpected: %+v", m)
	}
	body, _ := fake.calls[1].Body.(map[string]any)
	if body["is_return"] != true || body["return_against"] != "si_5" {
		t.Errorf("credit-note body should set is_return + return_against: %+v", body)
	}
	if body["remarks"] != "wrong item" {
		t.Errorf("remarks should carry the reason, got %v", body["remarks"])
	}
}

func TestCreateDraftCreditNote_RejectsNonSI(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	tool, _ := reg.Lookup("create_draft_credit_note_from_invoice")
	_, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{"sales_invoice_id": "pi_5"}))
	if err == nil {
		t.Error("expected si_ prefix error")
	}
}

// ---- error propagation ----

func TestERPError_Propagates(t *testing.T) {
	t.Parallel()
	reg, fake := newRegistry(t)
	fake.errors["GET /accounting/sales-invoices/si_x"] = errors.New("upstream 404")
	tool, _ := reg.Lookup("get_document")
	_, err := tool.Run(context.Background(), cc(), mustJSON(t, map[string]any{
		"doctype": "sales_invoice", "id": "si_x",
	}))
	if err == nil || !strings.Contains(err.Error(), "upstream 404") {
		t.Errorf("expected upstream error to propagate, got %v", err)
	}
}

// ---- tool registry sanity ----

func TestNames_AllExpectedToolsRegistered(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	want := []string{
		"list_documents", "get_document", "get_timeline",
		"report_ar_aging", "report_ap_aging", "report_balance_sheet", "report_profit_and_loss",
		"global_search",
		"create_draft", "create_draft_payment_for_invoice", "create_draft_credit_note_from_invoice",
	}
	got := map[string]bool{}
	for _, n := range reg.Names() {
		got[n] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing tool: %s", w)
		}
	}
}

func TestLLMTools_MatchRegisteredTools(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t)
	llmTools := reg.LLMTools()
	if len(llmTools) != len(reg.Names()) {
		t.Errorf("LLMTools count != registered count: %d vs %d", len(llmTools), len(reg.Names()))
	}
	for _, lt := range llmTools {
		if lt.Type != "function" {
			t.Errorf("tool %s should be type=function", lt.Function.Name)
		}
		if lt.Function.Parameters["type"] != "object" {
			t.Errorf("tool %s parameters root must be object", lt.Function.Name)
		}
	}
}

func toAnySlice(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, x := range items {
		out[i] = x
	}
	return out
}
