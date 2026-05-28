package customfield

import (
	"strings"
	"testing"
)

func TestCoerce_Select(t *testing.T) {
	def := Definition{FieldType: TypeSelect, SelectValues: []string{"low", "medium", "high"}}

	if v, err := coerce(def, "high"); err != nil || v != "high" {
		t.Fatalf("allowed value rejected: %v / %v", v, err)
	}
	if _, err := coerce(def, "ultra"); err == nil {
		t.Fatal("disallowed value should be rejected")
	} else if !strings.Contains(err.Error(), "not in allowed values") {
		t.Fatalf("unexpected error: %v", err)
	}

	// No constraint => any string accepted.
	defAny := Definition{FieldType: TypeSelect}
	if v, err := coerce(defAny, "anything"); err != nil || v != "anything" {
		t.Fatalf("unconstrained select rejected: %v / %v", v, err)
	}
}

func TestCoerce_Link(t *testing.T) {
	def := Definition{FieldType: TypeLink, LinkDoctype: "customer"}

	ok := map[string]any{"type": "customer", "id": "cust_1"}
	if v, err := coerce(def, ok); err != nil || v == nil {
		t.Fatalf("matching link rejected: %v / %v", v, err)
	}

	wrongType := map[string]any{"type": "supplier", "id": "supp_1"}
	if _, err := coerce(def, wrongType); err == nil {
		t.Fatal("wrong link doctype should be rejected")
	}

	missing := map[string]any{"type": "customer"}
	if _, err := coerce(def, missing); err == nil {
		t.Fatal("missing id should be rejected")
	}
}

func TestParseOptions(t *testing.T) {
	sv, ld, err := parseOptions([]byte(`{"values":["a","b","c"]}`))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(sv) != 3 || sv[1] != "b" || ld != "" {
		t.Fatalf("select parse mismatched: %v / %q", sv, ld)
	}

	sv, ld, err = parseOptions([]byte(`{"doctype":"customer"}`))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(sv) != 0 || ld != "customer" {
		t.Fatalf("link parse mismatched: %v / %q", sv, ld)
	}

	if _, _, err := parseOptions([]byte(`{bad json`)); err == nil {
		t.Fatal("malformed JSON should error")
	}
}
