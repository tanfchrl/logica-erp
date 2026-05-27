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
	"strings"

	"github.com/shopspring/decimal"

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

// ValueLimit caps a Tier-1 draft's numeric field. If the value extracted
// from the payload exceeds MaxIDR, the tool is rejected with a policy_blocked
// reason. Field is the JSON key in the create payload (e.g. "grand_total"
// for sales_invoice, "paid_amount" for payment_entry). Label is shown to
// the user and in the audit log.
type ValueLimit struct {
	Doctype string
	Field   string
	MaxIDR  decimal.Decimal
	Label   string
}

// LimitProvider fetches active ValueLimits at gate-check time. Pluggable so
// the gate doesn't need to know about the DB; cmd/agent supplies a
// DB-backed implementation. A nil provider means "no value caps configured".
type LimitProvider interface {
	LimitFor(doctype, companyID string) (ValueLimit, bool)
}

// Gate evaluates whether the calling agent may invoke `toolName` on the given
// doctype.  The doctype + tier mapping comes from the contract registry.
type Gate struct {
	cfg      Config
	registry *agentcontract.Registry
	limits   LimitProvider
}

func NewGate(cfg Config, reg *agentcontract.Registry) *Gate {
	return &Gate{cfg: cfg, registry: reg}
}

// SetLimits wires a LimitProvider; safe to call once at boot. Subsequent
// CheckPayload calls will consult it for Tier-1 + Tier-2 dispatches.
func (g *Gate) SetLimits(p LimitProvider) { g.limits = p }

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

// CheckPayload is the richer gate used for write tools — it runs the same
// tier check as Check, and on Tier 1 + Tier 2 dispatches it additionally
// asserts the payload's caps. Read-only Tier 0 tools never carry a
// payload-relevant field; CheckPayload delegates to Check for them.
//
// companyID is the acting user's active company (X-Company-Id header) so
// per-company overrides win over the global default.
//
// payload is the raw map the tool will POST. For composed tools the
// orchestrator passes the SOURCE doc fields (e.g. the SI being settled by
// a payment) since the relevant amount lives there.
func (g *Gate) CheckPayload(doctype, toolName, companyID string, payload map[string]any) Decision {
	d := g.Check(doctype, toolName)
	if !d.Allowed || d.Tier == Tier0 || g.limits == nil {
		return d
	}
	lim, ok := g.limits.LimitFor(doctype, companyID)
	if !ok {
		return d
	}
	raw, has := payload[lim.Field]
	if !has {
		return d
	}
	v, err := toDecimal(raw)
	if err != nil {
		// Malformed value — don't reject silently. Mark as blocked so the
		// operator notices the upstream bug.
		return Decision{Allowed: false, Tier: d.Tier, Doctype: doctype,
			Reason: fmt.Sprintf("value-limit check: field %q is not numeric (%v)", lim.Field, err)}
	}
	if v.GreaterThan(lim.MaxIDR) {
		label := lim.Label
		if label == "" {
			label = fmt.Sprintf("the configured Rp %s limit", lim.MaxIDR.StringFixed(0))
		}
		return Decision{Allowed: false, Tier: d.Tier, Doctype: doctype,
			Reason: fmt.Sprintf("%s value %s exceeds %s", lim.Field, v.StringFixed(0), label)}
	}
	return d
}

// toDecimal accepts the loosely-typed JSON shapes a payload can carry:
// a string, a json.Number (rendered to string), or a float64. Returns an
// error for anything else so a typo in field name doesn't silently bypass
// the cap.
func toDecimal(v any) (decimal.Decimal, error) {
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return decimal.Zero, nil
		}
		return decimal.NewFromString(s)
	case float64:
		return decimal.NewFromFloat(x), nil
	case int:
		return decimal.NewFromInt(int64(x)), nil
	case int64:
		return decimal.NewFromInt(x), nil
	}
	return decimal.Zero, fmt.Errorf("unsupported numeric type %T", v)
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
