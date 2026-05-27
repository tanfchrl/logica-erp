package nudges

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/agent/erpclient"
	"github.com/tandigital/logica-erp/internal/agentcontract"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// evalCooldown is the minimum gap between two full evaluator runs for the
// same user. The FE polls /nudges/active on a tighter cadence than this;
// the gate keeps us from re-walking 4 contracts × 5 predicates every minute.
const evalCooldown = 15 * time.Minute

// Nudge is a row in agent_nudge surfaced to the FE.
type Nudge struct {
	ID          string    `json:"id"`
	RuleID      string    `json:"rule_id"`
	UserID      string    `json:"user_id"`
	CompanyID   string    `json:"company_id,omitempty"`
	Priority    string    `json:"priority"`
	Message     string    `json:"message"`
	CTALabel    string    `json:"cta_label,omitempty"`
	CTAPrompt   string    `json:"cta_prompt,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	DismissedAt *time.Time `json:"dismissed_at,omitempty"`
}

// Evaluator owns the agent_nudge writes. Constructed once at boot and used
// by the HTTP layer.
type Evaluator struct {
	db       *dbx.DB
	contracts *agentcontract.Registry
}

func NewEvaluator(db *dbx.DB, contracts *agentcontract.Registry) *Evaluator {
	return &Evaluator{db: db, contracts: contracts}
}

// EnsureFresh runs the evaluator for (user, company) iff the user's most
// recent nudge is older than evalCooldown. Idempotent and cheap when the
// cooldown hasn't expired.
//
// Designed for on-demand polling — the FE calls /nudges/active, the
// handler calls EnsureFresh before reading the table. No background
// goroutine + no privileged credentials means we always run as the caller.
func (e *Evaluator) EnsureFresh(ctx context.Context, cc erpclient.CallContext, userID, companyID string) error {
	if userID == "" {
		return errors.New("nudges: user_id required")
	}
	// Check cooldown via MAX(created_at).
	var lastEval *time.Time
	err := e.db.QueryRow(ctx,
		`SELECT MAX(created_at) FROM agent_nudge WHERE user_id = $1 AND ($2 = '' OR company_id = $2)`,
		userID, companyID,
	).Scan(&lastEval)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if lastEval != nil && time.Since(*lastEval) < evalCooldown {
		return nil // still warm
	}
	return e.runAll(ctx, cc, userID, companyID)
}

// ForceRun bypasses the cooldown — used by tests + the dismiss flow if we
// ever want "re-evaluate now". Not exposed to the FE.
func (e *Evaluator) ForceRun(ctx context.Context, cc erpclient.CallContext, userID, companyID string) error {
	return e.runAll(ctx, cc, userID, companyID)
}

func (e *Evaluator) runAll(ctx context.Context, cc erpclient.CallContext, userID, companyID string) error {
	for _, c := range e.contracts.All() {
		for _, rule := range c.NudgeRules {
			pred, ok := Lookup(rule.ID)
			if !ok {
				slog.Warn("nudges: unknown predicate id in contract",
					"module", c.Module, "rule_id", rule.ID)
				continue
			}
			// Skip if an undismissed nudge already exists for this rule.
			// Resurfacing is gated on dismissal + data-state change.
			exists, err := e.hasOpenForRule(ctx, userID, companyID, rule.ID)
			if err != nil {
				slog.Warn("nudges: check existing failed", "rule_id", rule.ID, "err", err)
				continue
			}
			if exists {
				continue
			}
			m, err := pred.Run(ctx, cc)
			if err != nil {
				slog.Warn("nudges: predicate failed",
					"rule_id", rule.ID, "err", err)
				continue
			}
			if !m.Matches {
				continue
			}
			msg := renderTemplate(rule.MessageTemplate, m.Args)
			cta := renderTemplate(rule.CTAPrompt, m.Args)
			priority := rule.Priority
			if priority == "" {
				priority = "normal"
			}
			if err := e.insert(ctx, userID, companyID, rule.ID, priority, msg, rule.CTALabel, cta); err != nil {
				slog.Warn("nudges: insert failed", "rule_id", rule.ID, "err", err)
			}
		}
	}
	return nil
}

func (e *Evaluator) hasOpenForRule(ctx context.Context, userID, companyID, ruleID string) (bool, error) {
	var n int
	err := e.db.QueryRow(ctx, `
		SELECT count(*) FROM agent_nudge
		WHERE user_id = $1 AND rule_id = $2
		  AND ($3 = '' OR company_id = $3)
		  AND dismissed_at IS NULL`,
		userID, ruleID, companyID,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (e *Evaluator) insert(ctx context.Context, userID, companyID, ruleID, priority, msg, ctaLabel, ctaPrompt string) error {
	_, err := e.db.Exec(ctx, `
		INSERT INTO agent_nudge
		  (id, rule_id, user_id, company_id, priority, message, cta_label, cta_prompt)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		dbx.NewIDWithPrefix("nud"), ruleID, userID, nullStr(companyID), priority, msg, ctaLabel, ctaPrompt,
	)
	return err
}

// Active lists the caller's non-dismissed nudges. Highest priority first,
// then newest. Capped at 20 to keep responses small.
func (e *Evaluator) Active(ctx context.Context, userID, companyID string) ([]Nudge, error) {
	rows, err := e.db.Query(ctx, `
		SELECT id, rule_id, user_id, coalesce(company_id,''), priority, message,
		       coalesce(cta_label,''), coalesce(cta_prompt,''), created_at, dismissed_at
		FROM agent_nudge
		WHERE user_id = $1 AND ($2 = '' OR company_id = $2) AND dismissed_at IS NULL
		ORDER BY
		  CASE priority
		    WHEN 'urgent' THEN 0 WHEN 'high' THEN 1
		    WHEN 'normal' THEN 2 WHEN 'low' THEN 3 ELSE 4
		  END,
		  created_at DESC
		LIMIT 20`, userID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Nudge, 0)
	for rows.Next() {
		var n Nudge
		if err := rows.Scan(&n.ID, &n.RuleID, &n.UserID, &n.CompanyID, &n.Priority, &n.Message,
			&n.CTALabel, &n.CTAPrompt, &n.CreatedAt, &n.DismissedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// Dismiss marks one nudge as dismissed. Cross-user dismissals fail.
func (e *Evaluator) Dismiss(ctx context.Context, userID, id string) error {
	tag, err := e.db.Exec(ctx,
		`UPDATE agent_nudge SET dismissed_at = now()
		 WHERE id = $1 AND user_id = $2 AND dismissed_at IS NULL`,
		id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("nudges: not found or already dismissed")
	}
	return nil
}

// ---- helpers ----

// renderTemplate replaces `{key}` placeholders with stringified values from
// args. Anything we don't know stays as-is — better than crashing on a
// typo in a contract.
func renderTemplate(tpl string, args map[string]any) string {
	if tpl == "" || len(args) == 0 {
		return tpl
	}
	out := tpl
	for k, v := range args {
		out = strings.ReplaceAll(out, "{"+k+"}", fmt.Sprintf("%v", v))
	}
	return out
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
