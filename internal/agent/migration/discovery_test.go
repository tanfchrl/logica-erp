package migration

import "testing"

func TestParseProfileArgs(t *testing.T) {
	args := `{"business_type":"Toko Bangunan","industry":"Trading","employees":12,"modules":["accounting","inventory"],"multicompany":false,"base_currency":"idr","ready":true}`
	p, ready, err := parseProfileArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ready {
		t.Errorf("ready = false, want true")
	}
	if p.BusinessType != "Toko Bangunan" {
		t.Errorf("business_type = %q", p.BusinessType)
	}
	if p.Industry != "trading" { // lowercased
		t.Errorf("industry = %q, want lowercased 'trading'", p.Industry)
	}
	if p.BaseCurrency != "IDR" { // uppercased
		t.Errorf("base_currency = %q, want 'IDR'", p.BaseCurrency)
	}
	if p.Employees != 12 || len(p.Modules) != 2 {
		t.Errorf("employees/modules wrong: %d / %v", p.Employees, p.Modules)
	}
}

func TestMergeProfile(t *testing.T) {
	// nil prev → returns a copy of next
	got := mergeProfile(nil, &SetupProfile{BusinessType: "Jasa", Industry: "services"})
	if got == nil || got.BusinessType != "Jasa" || got.Industry != "services" {
		t.Fatalf("nil-prev merge wrong: %+v", got)
	}

	// partial update accumulates: keep prev fields the update leaves zero,
	// overlay the non-zero ones.
	prev := &SetupProfile{BusinessType: "Jasa", Industry: "services", Employees: 5, BaseCurrency: "IDR"}
	next := &SetupProfile{Employees: 9, LegacySystem: "Excel"} // industry/business empty → keep
	m := mergeProfile(prev, next)
	if m.BusinessType != "Jasa" || m.Industry != "services" {
		t.Errorf("merge clobbered untouched fields: %+v", m)
	}
	if m.Employees != 9 {
		t.Errorf("employees not updated: %d", m.Employees)
	}
	if m.LegacySystem != "Excel" {
		t.Errorf("legacy_system not set: %q", m.LegacySystem)
	}
	if m.BaseCurrency != "IDR" {
		t.Errorf("base_currency lost: %q", m.BaseCurrency)
	}
	// prev must not be mutated
	if prev.Employees != 5 {
		t.Errorf("merge mutated prev in place: %d", prev.Employees)
	}
}

func TestApplyProfileDefaults(t *testing.T) {
	p := &SetupProfile{}
	applyProfileDefaults(p)
	if p.FiscalYearStart != "01-01" || p.BaseCurrency != "IDR" {
		t.Errorf("defaults not applied: %+v", p)
	}
	p2 := &SetupProfile{FiscalYearStart: "07-01", BaseCurrency: "USD"}
	applyProfileDefaults(p2)
	if p2.FiscalYearStart != "07-01" || p2.BaseCurrency != "USD" {
		t.Errorf("defaults overrode explicit values: %+v", p2)
	}
}

func TestValidRootType(t *testing.T) {
	for _, rt := range []string{"asset", "liability", "equity", "income", "expense"} {
		if !validRootType(rt) {
			t.Errorf("%q should be valid", rt)
		}
	}
	for _, rt := range []string{"", "assets", "revenue", "ASSET"} {
		if validRootType(rt) {
			t.Errorf("%q should be invalid", rt)
		}
	}
}
