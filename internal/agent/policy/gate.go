// Package policy implements the autonomy-tier gate that every agent tool
// call passes through before execution. See docs/agent-build-prompt.md §2.
//
// Tiers (immutable):
//
//	0 — read/advise: no writes, no approval needed
//	1 — draft:      writes docstatus=0; no GL/SL impact; logged to approval queue
//	2 — submit:     writes docstatus=1; auto-disabled in v1 (returns ErrTierDisabled)
//
// The gate is data-driven: per-doctype tier classifications come from the
// AGENT_CONTRACT.md registry. The gate itself contains no doctype-specific
// logic.
package policy

import (
	"errors"
	"fmt"

	"github.com/tandigital/logica-erp/internal/agentcontract"
)

type Tier int

const (
	Tier0 Tier = iota // read / advise
	Tier1             // draft
	Tier2             // submit — policy-disabled in v1
)

func (t Tier) String() string {
	switch t {
	case Tier0:
		return "tier0"
	case Tier1:
		return "tier1"
	case Tier2:
		return "tier2"
	}
	return "unknown"
}

// Standard errors callers should switch on.
var (
	// ErrUnknownTool is returned when a tool name isn't in any contract.
	ErrUnknownTool = errors.New("policy: tool not declared in any AGENT_CONTRACT.md")
	// ErrTierDisabled is returned for Tier 2 attempts in v1. Catchable so the
	// orchestrator can surface "this would require human submission" to the
	// user without crashing.
	ErrTierDisabled = errors.New("policy: tier not enabled by current configuration")
)

// Decision is the result of a gate check. Allowed=true means the tool may
// proceed; otherwise Reason carries the structured failure string written to
// the agent_audit_log as a policy_blocked event.
type Decision struct {
	Allowed bool
	Tier    Tier
	Doctype string
	Reason  string
}

// Config controls which tiers are enabled for this deployment. v1 default
// is Tier 0 + Tier 1; Tier 2 is opt-in per spec §2.
type Config struct {
	Tier1Enabled bool // default true
	Tier2Enabled bool // default false — opt-in per spec §2
}

// DefaultConfig returns the v1 default tier configuration.
func DefaultConfig() Config { return Config{Tier1Enabled: true, Tier2Enabled: false} }

// Gate evaluates whether the calling agent may invoke `toolName` on the given
// doctype.  The doctype + tier mapping comes from the contract registry.
type Gate struct {
	cfg      Config
	registry *agentcontract.Registry
}

func NewGate(cfg Config, reg *agentcontract.Registry) *Gate {
	return &Gate{cfg: cfg, registry: reg}
}

// Check classifies (doctype, toolName) and returns a Decision. The caller is
// responsible for recording a `policy_blocked` audit entry on Allowed=false.
func (g *Gate) Check(doctype, toolName string) Decision {
	doc := g.findDocSpec(doctype)
	if doc == nil {
		return Decision{Allowed: false, Doctype: doctype,
			Reason: fmt.Sprintf("no contract found for doctype %q", doctype)}
	}
	switch {
	case contains(doc.Tier0, toolName):
		// Tier 0 is always permitted — read-only operations.
		return Decision{Allowed: true, Tier: Tier0, Doctype: doctype}
	case contains(doc.Tier1, toolName):
		if !g.cfg.Tier1Enabled {
			return Decision{Allowed: false, Tier: Tier1, Doctype: doctype,
				Reason: "tier1 drafting disabled in this deployment"}
		}
		return Decision{Allowed: true, Tier: Tier1, Doctype: doctype}
	case contains(doc.Tier2, toolName):
		if !g.cfg.Tier2Enabled {
			return Decision{Allowed: false, Tier: Tier2, Doctype: doctype,
				Reason: "tier2 auto-submit is disabled in v1 — human must submit"}
		}
		return Decision{Allowed: true, Tier: Tier2, Doctype: doctype}
	}
	return Decision{Allowed: false, Doctype: doctype,
		Reason: fmt.Sprintf("tool %q not declared in %s contract", toolName, doctype)}
}

func (g *Gate) findDocSpec(doctype string) *agentcontract.DocumentSpec {
	if g.registry == nil {
		return nil
	}
	_, d, ok := g.registry.FindByDoctype(doctype)
	if !ok {
		return nil
	}
	return &d
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
