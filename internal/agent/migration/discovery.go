package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tandigital/logica-erp/internal/agent/llm"
	"github.com/tandigital/logica-erp/internal/agent/session"
)

// LLMResolver yields the per-company chat client. Implemented by the agent's
// agentllmconfig service; injected via SetLLM so this package doesn't depend
// on cmd/agent.
type LLMResolver interface {
	ForCompany(ctx context.Context, companyID string) *llm.Client
}

// SetLLM wires the resolver used by the conversational Discovery step and the
// LLM-assisted COA augmentation. Returns the service for chaining. Optional —
// without it Discovery returns a clear "no model" error and COA stays on the
// deterministic path.
func (s *Service) SetLLM(r LLMResolver) *Service { s.llm = r; return s }

// DiscoveryResult is one assistant turn of the Discovery interview.
type DiscoveryResult struct {
	Reply    string        `json:"reply"`              // assistant's next message
	Profile  *SetupProfile `json:"profile,omitempty"`  // best-effort profile so far
	Complete bool          `json:"complete"`           // true once Discovery advanced to COA
}

const discoverySystemPrompt = `You are the onboarding consultant for Logica ERP, an accounting/ERP product for Indonesian SMEs (PSAK, PPN, PPh, BPJS, e-Faktur).

Your job: run a short, friendly discovery interview to learn the customer's business, then record it. Speak the user's language; default to Bahasa Indonesia. Keep each message short — ask about one or two things at a time, never dump a long form.

Collect these fields:
- business_type: free text (e.g. Trading, Manufaktur, Jasa, Konstruksi)
- industry: map the answer to EXACTLY one of: trading, manufacturing, services, construction, other
- employees: integer headcount
- modules: which areas they need (e.g. accounting, sales, purchasing, inventory, manufacturing, payroll, pos, projects)
- multicompany: do they run more than one legal entity?
- fiscal_year_start: as MM-DD (most Indonesian SMEs use 01-01)
- base_currency: ISO 4217 (default IDR)
- legacy_system: what they migrate from (e.g. Excel, ERPNext, Accurate) — or "none"

Rules:
- Call the save_setup_profile tool EVERY time you learn or refine any field, passing all fields you know so far. Use ready=false while still interviewing.
- When business_type and industry are known and you have a reasonable picture, give a one-line summary, then call save_setup_profile with ready=true.
- Infer sensible defaults (fiscal_year_start 01-01, base_currency IDR) rather than interrogating the user about them; confirm briefly.
- Be warm and concise. Do not invent facts the user didn't give.`

// DiscoveryChat advances the Step-1 interview by one assistant turn. It feeds
// the conversation (persisted on the migration session) plus the known profile
// to the model, lets the model record fields via the save_setup_profile tool,
// and — once the model marks the profile ready — completes Discovery and moves
// the wizard to the COA step.
func (s *Service) DiscoveryChat(ctx context.Context, userID, sessionID, companyID, userMsg string) (*DiscoveryResult, error) {
	st, err := s.LoadState(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	userMsg = strings.TrimSpace(userMsg)
	if userMsg == "" {
		return nil, errors.New("migration: message is required")
	}
	if s.llm == nil {
		return nil, errors.New("migration: AI model not wired on this server")
	}
	client := s.llm.ForCompany(ctx, companyID)
	if client == nil || !client.Configured() {
		return nil, errors.New("migration: no AI model configured — add an Anthropic API key in Settings → AI Model")
	}

	history, err := s.sess.History(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	msgs := []llm.Message{{Role: "system", Content: discoverySystemPrompt}}
	if st.Profile != nil {
		if b, err := json.Marshal(st.Profile); err == nil {
			msgs = append(msgs, llm.Message{Role: "system", Content: "Profile gathered so far (JSON): " + string(b)})
		}
	}
	for _, m := range history {
		if m.Role == "user" || m.Role == "assistant" {
			msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
		}
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: userMsg})

	resp, err := client.Chat(ctx, llm.Request{
		Messages:    msgs,
		Tools:       []llm.Tool{saveProfileTool()},
		Temperature: 0.3,
	})
	if err != nil {
		return nil, fmt.Errorf("migration: discovery chat: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("migration: empty model response")
	}
	choice := resp.Choices[0]
	reply := strings.TrimSpace(choice.Message.Content)

	var extracted *SetupProfile
	ready := false
	for _, tc := range choice.Message.ToolCalls {
		if tc.Function.Name != "save_setup_profile" {
			continue
		}
		p, r, perr := parseProfileArgs(tc.Function.Arguments)
		if perr != nil {
			continue
		}
		extracted = p
		if r {
			ready = true
		}
	}
	if extracted != nil {
		st.Profile = mergeProfile(st.Profile, extracted)
	}

	// Persist the exchange so the next turn has full context.
	turn := len(history) + 1
	_ = s.sess.AppendMessage(ctx, session.Message{SessionID: sessionID, Turn: turn, Role: "user", Content: userMsg})
	if reply == "" {
		if ready {
			reply = "Terima kasih! Profil setup sudah lengkap. Lanjut ke Chart of Accounts."
		} else {
			reply = "Baik, sudah saya catat."
		}
	}
	_ = s.sess.AppendMessage(ctx, session.Message{SessionID: sessionID, Turn: turn + 1, Role: "assistant", Content: reply})

	// Advance only when the model says ready AND the minimum is actually known.
	complete := false
	if ready && st.Profile != nil && st.Profile.BusinessType != "" && st.Profile.Industry != "" {
		applyProfileDefaults(st.Profile)
		st.completeStep(StepDiscovery)
		st.CurrentStep = StepCOA
		complete = true
	}
	if _, err := s.saveAndReturn(ctx, sessionID, st); err != nil {
		return nil, err
	}

	return &DiscoveryResult{Reply: reply, Profile: st.Profile, Complete: complete}, nil
}

func saveProfileTool() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "save_setup_profile",
			Description: "Record the customer's setup profile. Call this whenever you learn or refine any field, passing every field you know so far. Set ready=true ONLY when business_type and industry are known and the interview can move on.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"business_type":     map[string]any{"type": "string", "description": "Free text, e.g. Trading, Manufaktur, Jasa, Konstruksi"},
					"industry":          map[string]any{"type": "string", "enum": []string{"trading", "manufacturing", "services", "construction", "other"}},
					"employees":         map[string]any{"type": "integer"},
					"modules":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"multicompany":      map[string]any{"type": "boolean"},
					"fiscal_year_start": map[string]any{"type": "string", "description": "MM-DD, e.g. 01-01"},
					"base_currency":     map[string]any{"type": "string", "description": "ISO 4217, e.g. IDR"},
					"legacy_system":     map[string]any{"type": "string"},
					"ready":             map[string]any{"type": "boolean", "description": "true only when the interview is complete"},
				},
				"required": []string{"ready"},
			},
		},
	}
}

func parseProfileArgs(args string) (*SetupProfile, bool, error) {
	var raw struct {
		BusinessType    string   `json:"business_type"`
		Industry        string   `json:"industry"`
		Employees       int      `json:"employees"`
		Modules         []string `json:"modules"`
		Multicompany    bool     `json:"multicompany"`
		FiscalYearStart string   `json:"fiscal_year_start"`
		BaseCurrency    string   `json:"base_currency"`
		LegacySystem    string   `json:"legacy_system"`
		Ready           bool     `json:"ready"`
	}
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return nil, false, err
	}
	return &SetupProfile{
		BusinessType:    strings.TrimSpace(raw.BusinessType),
		Industry:        strings.ToLower(strings.TrimSpace(raw.Industry)),
		Employees:       raw.Employees,
		Modules:         raw.Modules,
		Multicompany:    raw.Multicompany,
		FiscalYearStart: strings.TrimSpace(raw.FiscalYearStart),
		BaseCurrency:    strings.ToUpper(strings.TrimSpace(raw.BaseCurrency)),
		LegacySystem:    strings.TrimSpace(raw.LegacySystem),
	}, raw.Ready, nil
}

// mergeProfile overlays the non-zero fields of next onto prev so the interview
// accumulates rather than clobbering on a partial update.
func mergeProfile(prev, next *SetupProfile) *SetupProfile {
	if next == nil {
		return prev
	}
	if prev == nil {
		out := *next
		return &out
	}
	out := *prev
	if next.BusinessType != "" {
		out.BusinessType = next.BusinessType
	}
	if next.Industry != "" {
		out.Industry = next.Industry
	}
	if next.Employees != 0 {
		out.Employees = next.Employees
	}
	if len(next.Modules) > 0 {
		out.Modules = next.Modules
	}
	out.Multicompany = next.Multicompany
	if next.FiscalYearStart != "" {
		out.FiscalYearStart = next.FiscalYearStart
	}
	if next.BaseCurrency != "" {
		out.BaseCurrency = next.BaseCurrency
	}
	if next.LegacySystem != "" {
		out.LegacySystem = next.LegacySystem
	}
	return &out
}

func applyProfileDefaults(p *SetupProfile) {
	if p.FiscalYearStart == "" {
		p.FiscalYearStart = "01-01"
	}
	if p.BaseCurrency == "" {
		p.BaseCurrency = "IDR"
	}
}

// ---- LLM-assisted COA augmentation ----

// llmAugmentCOA asks the model for accounts a business like this needs beyond
// the deterministic base + overlay. Best-effort: returns nil on any problem
// (no resolver, unconfigured model, API error, malformed output). Returned
// accounts are guaranteed to be new (no account_number collision with
// `existing`) and minimally valid.
func (s *Service) llmAugmentCOA(ctx context.Context, companyID string, profile *SetupProfile, existing []COAAccount) []COAAccount {
	if s.llm == nil || profile == nil {
		return nil
	}
	client := s.llm.ForCompany(ctx, companyID)
	if client == nil || !client.Configured() {
		return nil
	}

	have := make(map[string]bool, len(existing))
	nums := make([]string, 0, len(existing))
	for _, a := range existing {
		have[a.AccountNumber] = true
		nums = append(nums, a.AccountNumber+" "+a.Name)
	}
	profJSON, _ := json.Marshal(profile)

	sys := `You extend an Indonesian PSAK chart of accounts for a specific business. Numbering: 1xxx assets, 2xxx liabilities, 3xxx equity, 4xxx income, 5xxx expenses; leaf accounts hang under the nearest group. Propose ONLY accounts this particular business needs that are not already present — typically 0 to 8. Do not duplicate existing numbers. Keep names in Bahasa Indonesia. If nothing meaningful is missing, return an empty list.`
	usr := fmt.Sprintf("Business profile (JSON): %s\n\nAccounts already in the proposal:\n%s\n\nCall propose_additional_accounts with any genuinely useful additions.",
		string(profJSON), strings.Join(nums, "\n"))

	resp, err := client.Chat(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: usr},
		},
		Tools:       []llm.Tool{addAccountsTool()},
		Temperature: 0.2,
	})
	if err != nil || len(resp.Choices) == 0 {
		return nil
	}

	var out []COAAccount
	for _, tc := range resp.Choices[0].Message.ToolCalls {
		if tc.Function.Name != "propose_additional_accounts" {
			continue
		}
		var raw struct {
			Accounts []COAAccount `json:"accounts"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &raw); err != nil {
			continue
		}
		for _, a := range raw.Accounts {
			a.AccountNumber = strings.TrimSpace(a.AccountNumber)
			a.Name = strings.TrimSpace(a.Name)
			a.RootType = strings.ToLower(strings.TrimSpace(a.RootType))
			if a.AccountNumber == "" || a.Name == "" || have[a.AccountNumber] || !validRootType(a.RootType) {
				continue
			}
			have[a.AccountNumber] = true // guard against intra-batch dupes
			out = append(out, a)
		}
	}
	return out
}

func addAccountsTool() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        "propose_additional_accounts",
			Description: "Return accounts to add to the chart of accounts beyond what's already present. May be empty.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"accounts": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"account_number":        map[string]any{"type": "string"},
								"name":                  map[string]any{"type": "string"},
								"root_type":             map[string]any{"type": "string", "enum": []string{"asset", "liability", "equity", "income", "expense"}},
								"account_type":          map[string]any{"type": "string"},
								"is_group":              map[string]any{"type": "boolean"},
								"parent_account_number": map[string]any{"type": "string"},
							},
							"required": []string{"account_number", "name", "root_type"},
						},
					},
				},
				"required": []string{"accounts"},
			},
		},
	}
}

func validRootType(rt string) bool {
	switch rt {
	case "asset", "liability", "equity", "income", "expense":
		return true
	}
	return false
}
