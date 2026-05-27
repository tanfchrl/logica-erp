package policy

import (
	"errors"
	"testing"

	"github.com/tandigital/logica-erp/internal/agentcontract"
)

// fixtureRegistry returns a small registry covering the gate's full state
// space: a doctype with all three tiers populated + a "list-only" doctype
// with only tier 0. Tests use these to assert each tier transition.
func fixtureRegistry() *agentcontract.Registry {
	return agentcontract.NewRegistry([]agentcontract.Contract{{
		Module:  "accounting",
		Version: "1",
		Documents: []agentcontract.DocumentSpec{
			{
				Name:    "sales_invoice",
				APIPath: "/accounting/sales-invoices",
				Tier0:   []string{"list_with_filters", "get_by_id"},
				Tier1:   []string{"create_draft"},
				Tier2:   []string{"auto_submit"},
			},
			{
				Name:    "report_only_doc",
				APIPath: "/reports/something",
				Tier0:   []string{"list_with_filters"},
			},
		},
	}})
}

func TestGate_Tier0_AlwaysAllowed(t *testing.T) {
	t.Parallel()
	g := NewGate(DefaultConfig(), fixtureRegistry())
	cases := []struct{ doctype, tool string }{
		{"sales_invoice", "list_with_filters"},
		{"sales_invoice", "get_by_id"},
		{"report_only_doc", "list_with_filters"},
	}
	for _, c := range cases {
		d := g.Check(c.doctype, c.tool)
		if !d.Allowed {
			t.Errorf("expected allowed for %s/%s, got reason=%q", c.doctype, c.tool, d.Reason)
		}
		if d.Tier != Tier0 {
			t.Errorf("expected Tier0 for %s/%s, got %s", c.doctype, c.tool, d.Tier)
		}
	}
}

func TestGate_Tier1_GatedByConfig(t *testing.T) {
	t.Parallel()
	// Tier1Enabled=true (default) → allowed.
	g := NewGate(DefaultConfig(), fixtureRegistry())
	if d := g.Check("sales_invoice", "create_draft"); !d.Allowed || d.Tier != Tier1 {
		t.Errorf("default: expected Tier1 allowed, got allowed=%v tier=%s reason=%q",
			d.Allowed, d.Tier, d.Reason)
	}

	// Disabled → rejected.
	g2 := NewGate(Config{Tier1Enabled: false}, fixtureRegistry())
	d := g2.Check("sales_invoice", "create_draft")
	if d.Allowed {
		t.Error("expected Tier1 rejected when Tier1Enabled=false")
	}
	if d.Tier != Tier1 {
		t.Errorf("expected decision Tier=1 even on reject, got %s", d.Tier)
	}
	if d.Reason == "" {
		t.Error("expected a structured reason on Tier1 reject")
	}
}

func TestGate_Tier2_DisabledByDefault(t *testing.T) {
	t.Parallel()
	g := NewGate(DefaultConfig(), fixtureRegistry())
	d := g.Check("sales_invoice", "auto_submit")
	if d.Allowed {
		t.Fatal("Tier2 must NOT be allowed by default per spec §2")
	}
	if d.Tier != Tier2 {
		t.Errorf("expected Tier2 decision even on reject, got %s", d.Tier)
	}
}

func TestGate_Tier2_OptInOverride(t *testing.T) {
	t.Parallel()
	g := NewGate(Config{Tier1Enabled: true, Tier2Enabled: true}, fixtureRegistry())
	d := g.Check("sales_invoice", "auto_submit")
	if !d.Allowed || d.Tier != Tier2 {
		t.Errorf("expected Tier2 allowed when opted in, got allowed=%v tier=%s reason=%q",
			d.Allowed, d.Tier, d.Reason)
	}
}

func TestGate_UnknownDoctype(t *testing.T) {
	t.Parallel()
	g := NewGate(DefaultConfig(), fixtureRegistry())
	d := g.Check("not_a_doctype", "list_with_filters")
	if d.Allowed {
		t.Fatal("unknown doctype must be rejected")
	}
	if d.Reason == "" || !containsSubstr(d.Reason, "not_a_doctype") {
		t.Errorf("reason should name the doctype, got %q", d.Reason)
	}
}

func TestGate_UnknownToolName(t *testing.T) {
	t.Parallel()
	g := NewGate(DefaultConfig(), fixtureRegistry())
	d := g.Check("sales_invoice", "does_not_exist")
	if d.Allowed {
		t.Fatal("unknown tool must be rejected")
	}
}

func TestGate_DoctypeWithoutTier1(t *testing.T) {
	t.Parallel()
	g := NewGate(DefaultConfig(), fixtureRegistry())
	// report_only_doc has no tier1 tools — invoking create_draft on it is
	// the same as an unknown tool for that doctype.
	d := g.Check("report_only_doc", "create_draft")
	if d.Allowed {
		t.Fatal("doctype without tier1 must reject create_draft")
	}
}

func TestGate_NilRegistry(t *testing.T) {
	t.Parallel()
	// Defensive: a nil registry shouldn't panic, just deny every call.
	g := NewGate(DefaultConfig(), nil)
	d := g.Check("sales_invoice", "list_with_filters")
	if d.Allowed {
		t.Fatal("nil registry must deny all")
	}
}

func TestErrConstants(t *testing.T) {
	t.Parallel()
	// Standard error sentinels callers may switch on must remain exported.
	if !errors.Is(ErrUnknownTool, ErrUnknownTool) {
		t.Error("ErrUnknownTool sentinel broken")
	}
	if !errors.Is(ErrTierDisabled, ErrTierDisabled) {
		t.Error("ErrTierDisabled sentinel broken")
	}
}

// Table-driven coverage of the bucket+scope semantics so refactors of
// contains/findDocSpec can't silently change behavior.
func TestGate_Table(t *testing.T) {
	t.Parallel()
	reg := fixtureRegistry()
	type tc struct {
		name        string
		cfg         Config
		doctype     string
		tool        string
		wantAllowed bool
		wantTier    Tier
	}
	cases := []tc{
		{"tier0 read", DefaultConfig(), "sales_invoice", "list_with_filters", true, Tier0},
		{"tier0 get",  DefaultConfig(), "sales_invoice", "get_by_id", true, Tier0},
		{"tier1 default", DefaultConfig(), "sales_invoice", "create_draft", true, Tier1},
		{"tier1 disabled", Config{Tier1Enabled: false}, "sales_invoice", "create_draft", false, Tier1},
		{"tier2 default-denied", DefaultConfig(), "sales_invoice", "auto_submit", false, Tier2},
		{"tier2 enabled", Config{Tier1Enabled: true, Tier2Enabled: true}, "sales_invoice", "auto_submit", true, Tier2},
		{"unknown tool", DefaultConfig(), "sales_invoice", "voodoo", false, 0},
		{"unknown doctype", DefaultConfig(), "ghost", "list_with_filters", false, 0},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g := NewGate(c.cfg, reg)
			d := g.Check(c.doctype, c.tool)
			if d.Allowed != c.wantAllowed {
				t.Errorf("Allowed: want %v got %v (reason=%q)", c.wantAllowed, d.Allowed, d.Reason)
			}
			if c.wantAllowed && d.Tier != c.wantTier {
				t.Errorf("Tier: want %s got %s", c.wantTier, d.Tier)
			}
		})
	}
}

func containsSubstr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
