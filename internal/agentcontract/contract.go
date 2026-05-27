// Package agentcontract defines the schema for AGENT_CONTRACT.md files and
// the registry/aggregator that exposes them at GET /api/v1/agent/contracts.
//
// Each ERP module ships one AGENT_CONTRACT.md file declaring which documents
// the agent may interact with, at which autonomy tier, plus the system-prompt
// context, suggested prompts, and nudge rules. The agent service reads these
// at startup to populate its tool registry — no doctype names are hardcoded
// in the agent itself.
//
// Format: YAML front-matter followed by descriptive markdown prose. The
// front-matter ends with `---` on its own line; everything after is the
// module description, injected into the agent's system context.
//
// See docs/agent-build-prompt.md §7 for the canonical specification.
package agentcontract

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Contract is the parsed contents of an AGENT_CONTRACT.md file.
type Contract struct {
	Module        string         `yaml:"module"         json:"module"`
	DisplayName   string         `yaml:"display_name"   json:"display_name"`
	Version       string         `yaml:"version"        json:"version"`
	Documents     []DocumentSpec `yaml:"documents"      json:"documents"`
	SystemContext string         `yaml:"system_context" json:"system_context,omitempty"`
	Suggested     []string       `yaml:"suggested_prompts" json:"suggested_prompts,omitempty"`
	NudgeRules    []NudgeRule    `yaml:"nudge_rules"    json:"nudge_rules,omitempty"`

	// ProseBody is everything after the closing `---` of the front-matter:
	// human-readable description that the agent injects as system context
	// when this module is active.
	ProseBody string `yaml:"-" json:"prose_body,omitempty"`
}

// DocumentSpec is one doctype in a module's contract.
type DocumentSpec struct {
	Name        string   `yaml:"name"         json:"name"`         // canonical doctype, e.g. "sales_invoice"
	DisplayName string   `yaml:"display_name" json:"display_name"`
	APIPath     string   `yaml:"api_path"     json:"api_path"`     // e.g. "/accounting/sales-invoices"
	Tier0       []string `yaml:"tier0_tools"  json:"tier0_tools,omitempty"`
	Tier1       []string `yaml:"tier1_tools"  json:"tier1_tools,omitempty"`
	Tier2       []string `yaml:"tier2_tools"  json:"tier2_tools,omitempty"`
}

// NudgeRule is one entry in the module's nudge_rules array. The condition
// is a tiny expression DSL evaluated by the background nudge job; see
// internal/agent/nudge for the parser.
type NudgeRule struct {
	ID              string `yaml:"id"               json:"id"`
	Condition       string `yaml:"condition"        json:"condition"`
	MessageTemplate string `yaml:"message_template" json:"message_template"`
	CTALabel        string `yaml:"cta_label"        json:"cta_label,omitempty"`
	CTAPrompt       string `yaml:"cta_prompt"       json:"cta_prompt,omitempty"`
	Priority        string `yaml:"priority"         json:"priority,omitempty"`
}

// Parse splits the file into front-matter YAML and prose body. Both `---`
// delimiters are required; missing them is an error so authors notice.
func Parse(data []byte) (*Contract, error) {
	const sep = "---"
	// Skip a leading "---" if present (some editors auto-insert it).
	src := string(data)
	src = strings.TrimLeft(src, "\uFEFF") // strip UTF-8 BOM if present
	src = strings.TrimSpace(src)
	if !strings.HasPrefix(src, sep) {
		return nil, errors.New("AGENT_CONTRACT.md: front-matter must start with '---'")
	}
	rest := strings.TrimPrefix(src, sep)
	rest = strings.TrimLeft(rest, "\r\n")
	// Find the closing ---
	idx := indexLine(rest, sep)
	if idx < 0 {
		return nil, errors.New("AGENT_CONTRACT.md: front-matter must end with '---'")
	}
	frontMatter := rest[:idx]
	prose := strings.TrimSpace(rest[idx+len(sep):])

	var c Contract
	if err := yaml.Unmarshal([]byte(frontMatter), &c); err != nil {
		return nil, fmt.Errorf("AGENT_CONTRACT.md: yaml: %w", err)
	}
	c.ProseBody = prose
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate runs the schema checks enforced by `make agent-contract-lint`.
func (c *Contract) Validate() error {
	if c.Module == "" {
		return errors.New("module is required")
	}
	if c.Version == "" {
		return errors.New("version is required (use '1' for new modules)")
	}
	if len(c.Documents) == 0 {
		return errors.New("at least one document is required")
	}
	seen := make(map[string]bool, len(c.Documents))
	for i, d := range c.Documents {
		if d.Name == "" {
			return fmt.Errorf("documents[%d].name is required", i)
		}
		if seen[d.Name] {
			return fmt.Errorf("documents[%d].name %q duplicates an earlier entry", i, d.Name)
		}
		seen[d.Name] = true
		if d.APIPath == "" || !strings.HasPrefix(d.APIPath, "/") {
			return fmt.Errorf("documents[%d].api_path must be an absolute path", i)
		}
	}
	for i, r := range c.NudgeRules {
		if r.ID == "" || r.Condition == "" || r.MessageTemplate == "" {
			return fmt.Errorf("nudge_rules[%d]: id, condition, message_template required", i)
		}
		if r.Priority != "" {
			switch r.Priority {
			case "low", "normal", "high", "urgent":
			default:
				return fmt.Errorf("nudge_rules[%d].priority must be low|normal|high|urgent", i)
			}
		}
	}
	return nil
}

// indexLine returns the byte index of a line equal to s, or -1. Used to find
// the closing front-matter delimiter without a regex.
func indexLine(text, s string) int {
	off := 0
	for {
		nl := bytes.IndexByte([]byte(text[off:]), '\n')
		var line string
		if nl < 0 {
			line = text[off:]
		} else {
			line = text[off : off+nl]
		}
		if strings.TrimSpace(line) == s {
			return off
		}
		if nl < 0 {
			return -1
		}
		off += nl + 1
	}
}
