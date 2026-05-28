package nudges

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tandigital/logica-erp/internal/agentcontract"
)

// findRepoRoot walks up from this test file until it finds the go.mod, so
// the contract files can be read with a stable relative path regardless of
// where `go test` is invoked from.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	t.Fatalf("could not find go.mod above %s", cwd)
	return ""
}

func loadContract(t *testing.T, root, module string) *agentcontract.Contract {
	t.Helper()
	path := filepath.Join(root, "internal", module, "AGENT_CONTRACT.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	c, err := agentcontract.Parse(data)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return c
}

// TestContractFlow_OverdueInvoiceNudgeToReminderPrompt is the deterministic
// "overdue nudge → draft reminder" flow test called out in
// docs/agent-build-prompt.md §10. It walks the wiring from contract file →
// predicate registry → CTA prompt, asserting that:
//
//  1. The accounting contract declares an `overdue_sales_invoices` nudge rule.
//  2. That rule id is registered as a predicate in this package — so when
//     the background job loads the rule it can actually evaluate it.
//  3. The CTA prompt is the exact reminder string the design doc promises,
//     so the LLM picks up "draft payment reminder" intent when the user
//     clicks the nudge bar.
//  4. The reminder string can be templated against the predicate's args
//     (count, amount_idr) without losing placeholders.
//
// LLM calls are not involved — only the orchestration plumbing that decides
// what message the LLM eventually sees.
func TestContractFlow_OverdueInvoiceNudgeToReminderPrompt(t *testing.T) {
	root := findRepoRoot(t)
	c := loadContract(t, root, "accounting")

	const ruleID = "overdue_sales_invoices"
	var rule *agentcontract.NudgeRule
	for i := range c.NudgeRules {
		if c.NudgeRules[i].ID == ruleID {
			rule = &c.NudgeRules[i]
			break
		}
	}
	if rule == nil {
		t.Fatalf("accounting contract: nudge rule %q missing", ruleID)
	}

	// 2. Predicate is registered — runtime can evaluate this rule.
	if _, ok := Lookup(rule.ID); !ok {
		t.Fatalf("nudge rule %q not registered in predicates: would silently no-op at runtime", rule.ID)
	}

	// 3. CTA prompt is the explicit reminder text. If a future edit drops the
	// "payment reminder" intent, this test fails and the LLM's understanding
	// of the CTA changes silently — which is exactly what the conversation
	// test is here to prevent.
	if !strings.Contains(strings.ToLower(rule.CTAPrompt), "payment reminder") {
		t.Errorf("overdue rule CTA prompt should request a payment reminder, got %q", rule.CTAPrompt)
	}
	if !strings.Contains(strings.ToLower(rule.CTAPrompt), "overdue") {
		t.Errorf("overdue rule CTA prompt should reference overdue invoices, got %q", rule.CTAPrompt)
	}
	if rule.Priority != "high" {
		t.Errorf("overdue rule priority should be high, got %q", rule.Priority)
	}

	// 4. Message template renders against predicate args. The predicate
	// emits {count, amount_idr}; if a contract author renames a placeholder
	// without updating the predicate (or vice versa), the rendered text
	// would leave a literal "{count}" on screen — caught here.
	rendered := renderTemplate(rule.MessageTemplate, map[string]any{
		"count":      3,
		"amount_idr": "Rp 12.345.678",
	})
	if strings.Contains(rendered, "{") {
		t.Errorf("rendered message still contains placeholders: %q", rendered)
	}
	if !strings.Contains(rendered, "3") {
		t.Errorf("rendered message lost the count: %q", rendered)
	}
}

// TestContractFlow_EveryNudgeRulePredicateExists ensures every nudge rule
// shipping in a module contract has a matching predicate in this package —
// the "stale contract silently skipped at runtime" failure mode that
// AGENT_CONTRACT.md authors won't notice until a customer files a bug.
func TestContractFlow_EveryNudgeRulePredicateExists(t *testing.T) {
	root := findRepoRoot(t)
	modules := []string{"accounting", "stock", "crm", "hr", "manufacturing", "projects", "assets", "support", "pos"}
	for _, m := range modules {
		c := loadContract(t, root, m)
		for _, r := range c.NudgeRules {
			if _, ok := Lookup(r.ID); !ok {
				t.Errorf("module %q declares nudge %q with no registered predicate", m, r.ID)
			}
		}
	}
}
