// Package nudges implements the ambient-suggestion engine described in
// spec §5 "Proactive nudges" + §6 "Ambient nudge bar".
//
// Design choice: rather than parse a condition DSL string from
// AGENT_CONTRACT.md, each rule references a named Go predicate by id. The
// contract's `condition` field is the predicate name; the implementation
// lives in this file. Trade-off: less expressive than a full expression
// language, but zero parser surface (no injection risk from LLM-authored
// conditions) and predicates execute deterministically in tested Go code.
//
// Predicates always query the ERP via the calling user's JWT — never a
// privileged service account — to preserve the spec §1 invariant that the
// agent acts as the user.
package nudges

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/agent/erpclient"
)

// Match is one evaluator result. Empty Args is fine for templates with no
// substitutions.
type Match struct {
	Matches bool
	Args    map[string]any
}

// Predicate is a registered named check. Run is called per (user, company)
// — never invented; the function is the source of truth for what triggers
// a particular rule id.
type Predicate struct {
	ID  string
	Run func(ctx context.Context, cc erpclient.CallContext) (Match, error)
}

// registry is the in-process lookup. Predicates self-register via init().
var registry = map[string]Predicate{}

func register(p Predicate) { registry[p.ID] = p }

// Lookup returns a predicate by id, or false if no predicate is registered
// for that name. AGENT_CONTRACT.md ids that don't match here are silently
// skipped (with a log warning) so a stale contract doesn't crash the
// evaluator.
func Lookup(id string) (Predicate, bool) {
	p, ok := registry[id]
	return p, ok
}

// ------------------------------------------------------------------------
// Registered predicates
// ------------------------------------------------------------------------

func init() {
	register(Predicate{ID: "overdue_sales_invoices", Run: overdueSalesInvoices})
	register(Predicate{ID: "unpaid_purchase_invoices_due_soon", Run: unpaidPurchaseInvoicesDueSoon})
	register(Predicate{ID: "aged_drafts_unsubmitted", Run: agedDraftsUnsubmitted})
	register(Predicate{ID: "stale_leads_no_followup", Run: staleLeadsNoFollowup})
	register(Predicate{ID: "po_overdue_receipt", Run: poOverdueReceipt})
}

// poOverdueReceipt: submitted POs whose required_by_date < today AND status
// is one of the "to receive" buckets. Args: count.
func poOverdueReceipt(ctx context.Context, cc erpclient.CallContext) (Match, error) {
	items, err := listItems(ctx, cc, "/accounting/purchase-orders")
	if err != nil {
		return Match{}, err
	}
	today := time.Now().UTC().Format("2006-01-02")
	var count int
	for _, it := range items {
		ds, _ := it["docstatus"].(float64)
		if int(ds) != 1 {
			continue
		}
		status, _ := it["status"].(string)
		// Only flag POs still expecting a receipt. Don't pester for ones
		// that are intentionally on-hold or closed.
		switch status {
		case "To Receive", "To Receive and Bill":
		default:
			continue
		}
		req := stringField(it, "required_by_date")
		if len(req) < 10 {
			continue
		}
		if req[:10] < today {
			count++
		}
	}
	if count == 0 {
		return Match{}, nil
	}
	return Match{Matches: true, Args: map[string]any{"count": count}}, nil
}

// overdueSalesInvoices: count submitted SIs whose due_date < today AND
// outstanding_amount > 0. Args: count, amount_idr.
func overdueSalesInvoices(ctx context.Context, cc erpclient.CallContext) (Match, error) {
	items, err := listItems(ctx, cc, "/accounting/sales-invoices")
	if err != nil {
		return Match{}, err
	}
	today := time.Now().UTC().Format("2006-01-02")
	var count int
	var total decimal.Decimal
	for _, it := range items {
		ds, _ := it["docstatus"].(float64)
		if int(ds) != 1 {
			continue
		}
		out, _ := decimal.NewFromString(stringField(it, "outstanding_amount"))
		if out.Sign() <= 0 {
			continue
		}
		due := stringField(it, "due_date")
		if len(due) >= 10 && due[:10] < today {
			count++
			total = total.Add(out)
		}
	}
	if count == 0 {
		return Match{}, nil
	}
	return Match{
		Matches: true,
		Args: map[string]any{
			"count":      count,
			"amount_idr": formatIDR(total),
		},
	}, nil
}

// unpaidPurchaseInvoicesDueSoon: PIs with docstatus=1, outstanding > 0,
// due_date within the next 7 days. Args: count, days_window=7.
func unpaidPurchaseInvoicesDueSoon(ctx context.Context, cc erpclient.CallContext) (Match, error) {
	items, err := listItems(ctx, cc, "/accounting/purchase-invoices")
	if err != nil {
		return Match{}, err
	}
	today := time.Now().UTC()
	cutoff := today.AddDate(0, 0, 7).Format("2006-01-02")
	todayStr := today.Format("2006-01-02")
	var count int
	for _, it := range items {
		ds, _ := it["docstatus"].(float64)
		if int(ds) != 1 {
			continue
		}
		out, _ := decimal.NewFromString(stringField(it, "outstanding_amount"))
		if out.Sign() <= 0 {
			continue
		}
		due := stringField(it, "due_date")
		if len(due) < 10 {
			continue
		}
		d := due[:10]
		if d >= todayStr && d <= cutoff {
			count++
		}
	}
	if count == 0 {
		return Match{}, nil
	}
	return Match{
		Matches: true,
		Args:    map[string]any{"count": count, "days_window": 7},
	}, nil
}

// agedDraftsUnsubmitted: drafts (docstatus=0) older than 3 days. Surfaces
// across SI + PI + JE so the user sees them as a single nudge.
func agedDraftsUnsubmitted(ctx context.Context, cc erpclient.CallContext) (Match, error) {
	endpoints := []string{
		"/accounting/sales-invoices",
		"/accounting/purchase-invoices",
		"/accounting/journal-entries",
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -3)
	var count int
	for _, p := range endpoints {
		items, err := listItems(ctx, cc, p)
		if err != nil {
			continue // partial failure shouldn't kill the whole nudge
		}
		for _, it := range items {
			ds, _ := it["docstatus"].(float64)
			if int(ds) != 0 {
				continue
			}
			created := stringField(it, "created_at")
			if t, err := time.Parse(time.RFC3339Nano, created); err == nil && t.Before(cutoff) {
				count++
			}
		}
	}
	if count == 0 {
		return Match{}, nil
	}
	return Match{
		Matches: true,
		Args:    map[string]any{"count": count, "days_threshold": 3},
	}, nil
}

// staleLeadsNoFollowup: leads whose status isn't 'converted' and whose
// updated_at is more than 7 days old. Surfaces the CRM hygiene problem.
func staleLeadsNoFollowup(ctx context.Context, cc erpclient.CallContext) (Match, error) {
	items, err := listItems(ctx, cc, "/crm/leads")
	if err != nil {
		return Match{}, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -7)
	var count int
	for _, it := range items {
		status, _ := it["status"].(string)
		if strings.EqualFold(status, "converted") {
			continue
		}
		updated := stringField(it, "updated_at")
		if t, err := time.Parse(time.RFC3339Nano, updated); err == nil && t.Before(cutoff) {
			count++
		}
	}
	if count == 0 {
		return Match{}, nil
	}
	return Match{
		Matches: true,
		Args:    map[string]any{"count": count, "days_threshold": 7},
	}, nil
}

// ---- shared helpers ----

func listItems(ctx context.Context, cc erpclient.CallContext, path string) ([]map[string]any, error) {
	var resp map[string]any
	// Construct an ad-hoc erpclient via the call context — the worker
	// doesn't hold an erpclient instance. Each predicate creates one
	// lazily; the cost is a single struct allocation per call.
	// Note: the actual *http.Client is reused via the package var below.
	return doList(ctx, cc, path, &resp)
}

// erpc is a package-level erpclient reused by every predicate. Set by the
// evaluator at boot via SetClient(). Avoids re-allocating the http.Client.
var erpc *erpclient.Client

// SetClient wires the package-level ERP client. Called once from cmd/agent.
func SetClient(c *erpclient.Client) { erpc = c }

func doList(ctx context.Context, cc erpclient.CallContext, path string, _ *map[string]any) ([]map[string]any, error) {
	if erpc == nil {
		return nil, fmt.Errorf("nudges: erp client not configured (call nudges.SetClient at boot)")
	}
	var resp map[string]any
	if err := erpc.Do(ctx, cc, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	items, _ := resp["items"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func stringField(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

// formatIDR returns a short Rp-formatted total like "Rp 12.345.678". Used
// in nudge message templates so the bar reads naturally.
func formatIDR(d decimal.Decimal) string {
	s := d.Truncate(0).String()
	if strings.HasPrefix(s, "-") {
		return "-Rp " + groupThousands(s[1:])
	}
	return "Rp " + groupThousands(s)
}

func groupThousands(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	first := n % 3
	if first == 0 {
		first = 3
	}
	out := s[:first]
	for i := first; i < n; i += 3 {
		out += "." + s[i:i+3]
	}
	return out
}
