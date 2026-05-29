package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
)

// PricingEntry is per-million-token cost in USD (the upstream-vendor unit)
// plus a configurable IDR rate to convert. Operators can override either via
// AGENT_PRICING_OVERRIDES_JSON or AGENT_PRICING_USD_TO_IDR (default 16000).
type PricingEntry struct {
	Model       string  `json:"model"`
	InputPerM   float64 `json:"input_per_million_usd"`
	OutputPerM  float64 `json:"output_per_million_usd"`
}

// defaultPricing is the small built-in catalog — covers the most common
// models a Logica deployment is likely to route through. Operators bring
// new entries (or fix outdated rates) via env override.
var defaultPricing = []PricingEntry{
	// Anthropic — current Claude 4.x family (per-million-token USD). Adjust
	// via AGENT_PRICING_OVERRIDES_JSON if Anthropic changes rates.
	{"claude-opus-4-8", 15.00, 75.00},
	{"claude-sonnet-4-6", 3.00, 15.00},
	{"claude-haiku-4-5", 1.00, 5.00},
	{"claude-haiku-4-5-20251001", 1.00, 5.00},
	// Prior Claude generations kept so historical usage rows still price.
	{"claude-sonnet-4-5", 3.00, 15.00},
	{"claude-sonnet-4-5-20250929", 3.00, 15.00},
	{"claude-opus-4-1", 15.00, 75.00},
	{"claude-opus-4-1-20250805", 15.00, 75.00},

	// Self-hosted / local — zero per-token cost.
	{"ollama-qwen", 0, 0},
	{"ollama-llama", 0, 0},
	{"qwen2.5:14b", 0, 0},
	{"llama3.1:8b", 0, 0},
}

// loadPricing returns the effective pricing table — defaults merged with
// AGENT_PRICING_OVERRIDES_JSON (an array of PricingEntry). Operators can
// override defaults or add new models entirely.
func loadPricing() map[string]PricingEntry {
	out := make(map[string]PricingEntry, len(defaultPricing))
	for _, e := range defaultPricing {
		out[e.Model] = e
	}
	if raw := os.Getenv("AGENT_PRICING_OVERRIDES_JSON"); raw != "" {
		var ovs []PricingEntry
		if err := json.Unmarshal([]byte(raw), &ovs); err == nil {
			for _, e := range ovs {
				out[e.Model] = e
			}
		}
	}
	return out
}

func usdToIDR() decimal.Decimal {
	if raw := os.Getenv("AGENT_PRICING_USD_TO_IDR"); raw != "" {
		if d, err := decimal.NewFromString(raw); err == nil && d.Sign() > 0 {
			return d
		}
	}
	return decimal.NewFromInt(16000)
}

// UsageRow is one (day, user, model) aggregate.
type UsageRow struct {
	Day        string `json:"day"`        // YYYY-MM-DD
	UserID     string `json:"user_id"`
	UserEmail  string `json:"user_email,omitempty"`
	Model      string `json:"model"`
	Calls      int    `json:"calls"`
	TokensIn   int    `json:"tokens_in"`
	TokensOut  int    `json:"tokens_out"`
	CostIDR    string `json:"cost_idr"`   // string, not float, to preserve precision
}

// UsageSummary bundles the rows + computed totals.
type UsageSummary struct {
	Rows       []UsageRow `json:"rows"`
	TotalCalls int        `json:"total_calls"`
	TotalIn    int        `json:"total_in"`
	TotalOut   int        `json:"total_out"`
	TotalIDR   string     `json:"total_idr"`
}

// CostService aggregates agent_audit_log into UsageRows + computes IDR cost
// per (day, user, model). Pricing comes from loadPricing() at construction;
// in v1 we don't hot-reload — restart agent to pick up env changes.
type CostService struct {
	db      *dbx.DB
	pricing map[string]PricingEntry
	idrRate decimal.Decimal
}

func NewCostService(db *dbx.DB) *CostService {
	return &CostService{db: db, pricing: loadPricing(), idrRate: usdToIDR()}
}

// Summary aggregates rows where created_at is in [since, until). Both
// inclusive of since, exclusive of until — same convention as the audit
// query endpoint. Caller controls the window size; we don't enforce caps
// because this is admin-only and the underlying agent_audit_log query
// uses partition pruning.
func (s *CostService) Summary(ctx context.Context, since, until time.Time) (*UsageSummary, error) {
	if since.IsZero() {
		since = time.Now().AddDate(0, 0, -30) // default: last 30 days
	}
	if until.IsZero() {
		until = time.Now().Add(24 * time.Hour)
	}
	rows, err := s.db.Query(ctx, `
		SELECT to_char(date_trunc('day', a.created_at), 'YYYY-MM-DD') AS day,
		       a.user_id, coalesce(u.email,''),
		       coalesce(a.model, '(none)') AS model,
		       count(*) AS calls,
		       sum(a.tokens_in)::int  AS in_total,
		       sum(a.tokens_out)::int AS out_total
		FROM agent_audit_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.created_at >= $1 AND a.created_at < $2
		  AND (a.tokens_in > 0 OR a.tokens_out > 0)
		GROUP BY day, a.user_id, u.email, a.model
		ORDER BY day DESC, calls DESC`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &UsageSummary{Rows: make([]UsageRow, 0)}
	totalIDR := decimal.Zero
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Day, &r.UserID, &r.UserEmail, &r.Model, &r.Calls, &r.TokensIn, &r.TokensOut); err != nil {
			return nil, err
		}
		idr := s.cost(r.Model, r.TokensIn, r.TokensOut)
		r.CostIDR = idr.Truncate(2).String()
		out.Rows = append(out.Rows, r)
		out.TotalCalls += r.Calls
		out.TotalIn += r.TokensIn
		out.TotalOut += r.TokensOut
		totalIDR = totalIDR.Add(idr)
	}
	out.TotalIDR = totalIDR.Truncate(2).String()
	return out, rows.Err()
}

// cost computes IDR cost for one (model, tokens_in, tokens_out) line.
// Unknown models cost zero — the UI surfaces that as "(none)" so an admin
// can spot the gap and add a pricing override.
func (s *CostService) cost(model string, in, out int) decimal.Decimal {
	p, ok := s.pricing[model]
	if !ok {
		return decimal.Zero
	}
	million := decimal.NewFromInt(1_000_000)
	usdIn := decimal.NewFromFloat(p.InputPerM).Mul(decimal.NewFromInt(int64(in))).Div(million)
	usdOut := decimal.NewFromFloat(p.OutputPerM).Mul(decimal.NewFromInt(int64(out))).Div(million)
	return usdIn.Add(usdOut).Mul(s.idrRate)
}

// Pricing returns the effective pricing table — the UI uses this for the
// "configured models" admin sidebar.
func (s *CostService) Pricing() []PricingEntry {
	out := make([]PricingEntry, 0, len(s.pricing))
	for _, e := range s.pricing {
		out = append(out, e)
	}
	return out
}

// ---- HTTP ----

func RegisterCosts(api huma.API, svc *CostService) {
	huma.Register(api, huma.Operation{
		OperationID: "agent-usage-summary",
		Method:      http.MethodGet,
		Path:        "/admin/usage",
		Summary:     "Token + cost aggregates from agent_audit_log (system administrators only)",
		Tags:        []string{"Agent / Admin"},
	}, func(ctx context.Context, in *usageIn) (*usageOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		if !p.IsSystem {
			return nil, huma.NewError(http.StatusForbidden, "system administrators only")
		}
		var since, until time.Time
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "since: "+err.Error())
			}
			since = t
		}
		if in.Until != "" {
			t, err := time.Parse(time.RFC3339, in.Until)
			if err != nil {
				return nil, huma.NewError(http.StatusBadRequest, "until: "+err.Error())
			}
			until = t
		}
		s, err := svc.Summary(ctx, since, until)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &usageOut{Body: *s}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "agent-pricing",
		Method:      http.MethodGet,
		Path:        "/admin/usage/pricing",
		Summary:     "Effective per-model pricing table (built-ins + AGENT_PRICING_OVERRIDES_JSON merged)",
		Tags:        []string{"Agent / Admin"},
	}, func(ctx context.Context, _ *struct{}) (*pricingOut, error) {
		p := auth.FromContext(ctx)
		if p == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "unauthenticated")
		}
		if !p.IsSystem {
			return nil, huma.NewError(http.StatusForbidden, "system administrators only")
		}
		return &pricingOut{Body: pricingBody{
			USDToIDR: svc.idrRate.String(),
			Models:   sortPricing(svc.Pricing()),
		}}, nil
	})
}

func sortPricing(in []PricingEntry) []PricingEntry {
	// Stable-ish sort by model name so the UI list doesn't shuffle on
	// every poll.
	out := append([]PricingEntry(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && strings.ToLower(out[j].Model) < strings.ToLower(out[j-1].Model); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

type (
	usageIn struct {
		Since string `query:"since" doc:"RFC3339; default = now-30d"`
		Until string `query:"until" doc:"RFC3339; default = now+1d"`
	}
	usageOut    struct{ Body UsageSummary }
	pricingOut  struct{ Body pricingBody }
	pricingBody struct {
		USDToIDR string         `json:"usd_to_idr"`
		Models   []PricingEntry `json:"models"`
	}
)
