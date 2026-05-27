package policy

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/agentcontract"
)

// fakeLimits is a fixed LimitProvider for the threshold tests.
type fakeLimits struct {
	global map[string]ValueLimit            // keyed by doctype
	byCo   map[string]map[string]ValueLimit // company → doctype → limit
}

func (f *fakeLimits) LimitFor(doctype, companyID string) (ValueLimit, bool) {
	if companyID != "" {
		if m, ok := f.byCo[companyID]; ok {
			if v, ok := m[doctype]; ok {
				return v, true
			}
		}
	}
	v, ok := f.global[doctype]
	return v, ok
}

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

func TestCheckPayload_BelowCapAllows(t *testing.T) {
	t.Parallel()
	g := NewGate(DefaultConfig(), fixtureRegistry())
	g.SetLimits(&fakeLimits{global: map[string]ValueLimit{
		"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
			MaxIDR: decimal.NewFromInt(50_000_000), Label: "Rp 50 juta"},
	}})
	d := g.CheckPayload("sales_invoice", "create_draft", "", map[string]any{
		"grand_total": "10000000", // 10M < 50M
	})
	if !d.Allowed {
		t.Errorf("expected allowed under cap, got reason=%q", d.Reason)
	}
}

func TestCheckPayload_AboveCapBlocks(t *testing.T) {
	t.Parallel()
	g := NewGate(DefaultConfig(), fixtureRegistry())
	g.SetLimits(&fakeLimits{global: map[string]ValueLimit{
		"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
			MaxIDR: decimal.NewFromInt(50_000_000), Label: "Rp 50 juta"},
	}})
	d := g.CheckPayload("sales_invoice", "create_draft", "", map[string]any{
		"grand_total": "75000000", // 75M > 50M
	})
	if d.Allowed {
		t.Fatal("expected block above cap")
	}
	if !containsSubstr(d.Reason, "exceeds") || !containsSubstr(d.Reason, "Rp 50 juta") {
		t.Errorf("reason should name the breach + the cap label, got %q", d.Reason)
	}
	if d.Tier != Tier1 {
		t.Errorf("decision Tier should still report the intended tier on block, got %s", d.Tier)
	}
}

func TestCheckPayload_NoLimitConfigured(t *testing.T) {
	t.Parallel()
	// No LimitProvider set → CheckPayload behaves like Check (allow).
	g := NewGate(DefaultConfig(), fixtureRegistry())
	d := g.CheckPayload("sales_invoice", "create_draft", "", map[string]any{
		"grand_total": "1000000000",
	})
	if !d.Allowed {
		t.Error("with no LimitProvider, all-tier-1 should be allowed")
	}
}

func TestCheckPayload_FieldAbsentAllows(t *testing.T) {
	t.Parallel()
	// If the payload doesn't carry the configured field, the cap can't be
	// evaluated — fail open. The server-side write endpoints have their
	// own validation; not the gate's job to second-guess shape.
	g := NewGate(DefaultConfig(), fixtureRegistry())
	g.SetLimits(&fakeLimits{global: map[string]ValueLimit{
		"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
			MaxIDR: decimal.NewFromInt(50_000_000)},
	}})
	d := g.CheckPayload("sales_invoice", "create_draft", "", map[string]any{
		// no grand_total here
		"customer_id": "cust_x",
	})
	if !d.Allowed {
		t.Errorf("missing field should pass the cap, got %q", d.Reason)
	}
}

func TestCheckPayload_NonNumericBlocks(t *testing.T) {
	t.Parallel()
	// A malformed value shouldn't silently pass — that'd let an upstream
	// bug bypass the cap. Mark as policy_blocked so operators notice.
	g := NewGate(DefaultConfig(), fixtureRegistry())
	g.SetLimits(&fakeLimits{global: map[string]ValueLimit{
		"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
			MaxIDR: decimal.NewFromInt(50_000_000)},
	}})
	d := g.CheckPayload("sales_invoice", "create_draft", "", map[string]any{
		"grand_total": []any{"why is this a list"},
	})
	if d.Allowed {
		t.Fatal("malformed value should block")
	}
	if !containsSubstr(d.Reason, "value-limit") {
		t.Errorf("reason should mention the failure mode, got %q", d.Reason)
	}
}

func TestCheckPayload_AcceptsMultipleNumericShapes(t *testing.T) {
	t.Parallel()
	// JSON parsers can hand us strings, float64, or json.Number depending
	// on the upstream. The cap check has to accept all three.
	g := NewGate(DefaultConfig(), fixtureRegistry())
	g.SetLimits(&fakeLimits{global: map[string]ValueLimit{
		"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
			MaxIDR: decimal.NewFromInt(100)},
	}})
	cases := []any{"50", 50.0, int(50), int64(50)}
	for _, v := range cases {
		d := g.CheckPayload("sales_invoice", "create_draft", "", map[string]any{"grand_total": v})
		if !d.Allowed {
			t.Errorf("expected allow for %T(%v), got %q", v, v, d.Reason)
		}
	}
}

func TestCheckPayload_PerCompanyOverrideWins(t *testing.T) {
	t.Parallel()
	// Global says 50M; one specific company is bumped to 200M.
	g := NewGate(DefaultConfig(), fixtureRegistry())
	g.SetLimits(&fakeLimits{
		global: map[string]ValueLimit{
			"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
				MaxIDR: decimal.NewFromInt(50_000_000), Label: "global"},
		},
		byCo: map[string]map[string]ValueLimit{
			"cmp_premium": {
				"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
					MaxIDR: decimal.NewFromInt(200_000_000), Label: "premium"},
			},
		},
	})

	// 75M under premium's 200M cap → allowed for cmp_premium...
	if d := g.CheckPayload("sales_invoice", "create_draft", "cmp_premium",
		map[string]any{"grand_total": "75000000"}); !d.Allowed {
		t.Errorf("premium override should allow 75M, got %q", d.Reason)
	}
	// ...blocked for any other company.
	if d := g.CheckPayload("sales_invoice", "create_draft", "cmp_other",
		map[string]any{"grand_total": "75000000"}); d.Allowed {
		t.Error("global cap should block 75M for cmp_other")
	}
}

func TestCheckPayload_Tier0Bypass(t *testing.T) {
	t.Parallel()
	// Tier-0 reads never carry a relevant amount; the cap should be
	// short-circuited so list/get tools don't get blocked by a stray
	// payload field with the right name.
	g := NewGate(DefaultConfig(), fixtureRegistry())
	g.SetLimits(&fakeLimits{global: map[string]ValueLimit{
		"sales_invoice": {Doctype: "sales_invoice", Field: "grand_total",
			MaxIDR: decimal.NewFromInt(1)},
	}})
	d := g.CheckPayload("sales_invoice", "list_with_filters", "", map[string]any{
		"grand_total": "999999999", // would breach if checked
	})
	if !d.Allowed {
		t.Errorf("Tier 0 should bypass value cap, got %q", d.Reason)
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
