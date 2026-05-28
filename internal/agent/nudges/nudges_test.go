package nudges

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestLookup_RegisteredBuiltins(t *testing.T) {
	want := []string{
		"overdue_sales_invoices",
		"unpaid_purchase_invoices_due_soon",
		"aged_drafts_unsubmitted",
		"stale_leads_no_followup",
		"po_overdue_receipt",
		"mr_pending_ordering",
		"opportunities_closing_soon",
		"stale_opportunities",
	}
	for _, id := range want {
		p, ok := Lookup(id)
		if !ok {
			t.Errorf("Lookup(%q) missing — built-in predicate must be registered", id)
			continue
		}
		if p.ID != id {
			t.Errorf("Lookup(%q).ID = %q", id, p.ID)
		}
		if p.Run == nil {
			t.Errorf("Lookup(%q).Run is nil", id)
		}
	}
}

func TestLookup_Unknown(t *testing.T) {
	if _, ok := Lookup("does_not_exist"); ok {
		t.Fatal("Lookup of unknown id should return ok=false")
	}
}

func TestStringField(t *testing.T) {
	m := map[string]any{"a": "x", "b": 42}
	if got := stringField(m, "a"); got != "x" {
		t.Errorf("stringField(a) = %q", got)
	}
	if got := stringField(m, "b"); got != "" {
		t.Errorf("non-string value should yield empty, got %q", got)
	}
	if got := stringField(m, "missing"); got != "" {
		t.Errorf("missing key should yield empty, got %q", got)
	}
}

func TestFormatIDR(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0", "Rp 0"},
		{"999", "Rp 999"},
		{"1000", "Rp 1.000"},
		{"12345", "Rp 12.345"},
		{"12345678", "Rp 12.345.678"},
		{"1000000000", "Rp 1.000.000.000"},
		{"-5000", "-Rp 5.000"},
		{"5550000.50", "Rp 5.550.000"}, // truncated to integer rupiah
	}
	for _, c := range cases {
		d, err := decimal.NewFromString(c.in)
		if err != nil {
			t.Fatalf("decimal(%q): %v", c.in, err)
		}
		if got := formatIDR(d); got != c.want {
			t.Errorf("formatIDR(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGroupThousands(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"1", "1"},
		{"12", "12"},
		{"123", "123"},
		{"1234", "1.234"},
		{"123456", "123.456"},
		{"1234567", "1.234.567"},
	}
	for _, c := range cases {
		if got := groupThousands(c.in); got != c.want {
			t.Errorf("groupThousands(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderTemplate(t *testing.T) {
	cases := []struct {
		name string
		tpl  string
		args map[string]any
		want string
	}{
		{"empty template", "", map[string]any{"a": 1}, ""},
		{"nil args", "{x}", nil, "{x}"},
		{"single substitution", "{count} invoices overdue", map[string]any{"count": 3}, "3 invoices overdue"},
		{"multiple substitutions", "{count} × {kind}", map[string]any{"count": 2, "kind": "PO"}, "2 × PO"},
		{"unknown placeholder stays", "{count} of {unknown}", map[string]any{"count": 5}, "5 of {unknown}"},
		{"string and number mix", "{name}: Rp {amount}", map[string]any{"name": "PT X", "amount": 1000}, "PT X: Rp 1000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderTemplate(c.tpl, c.args); got != c.want {
				t.Errorf("renderTemplate = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNullStr(t *testing.T) {
	if v := nullStr(""); v != nil {
		t.Errorf("empty string should map to nil, got %v", v)
	}
	if v := nullStr("x"); v != "x" {
		t.Errorf("non-empty should pass through, got %v", v)
	}
}
