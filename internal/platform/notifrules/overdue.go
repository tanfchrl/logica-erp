// overdue.go — scheduled fire of "invoice.overdue" events. Runs as a
// goroutine alongside RunWorker; on each tick it scans for submitted
// sales_invoice rows past due_date with outstanding > 0 and fires the
// event once per (invoice, calendar day).
//
// Idempotency is enforced by checking notification_dispatch for an existing
// row with this event_key + document_id created today before firing.
// That keeps the engine self-contained — no extra marker table.
package notifrules

import (
	"context"
	"time"
)

// RunOverdueScan starts a background loop that fires invoice.overdue at
// most once per day per invoice. Stops when ctx is cancelled.
//
// `tick` is how often the scan runs; for production a daily run is plenty
// (3600s+ ok). For dev where you want to see it fire quickly, pass 30s.
func (d *Dispatcher) RunOverdueScan(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		tick = 1 * time.Hour
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	d.scanOverdue(ctx) // fire immediately on boot
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.scanOverdue(ctx)
		}
	}
}

func (d *Dispatcher) scanOverdue(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	type row struct {
		id, name, companyID, customerID string
		outstanding                     float64
		dueDate                         time.Time
	}

	// Outer scan: submitted SIs past due. Skip ones that already have a
	// dispatch row for invoice.overdue created today — that's our daily
	// dedup. Using EXISTS lets the query short-circuit on the index.
	rows, err := d.db.Query(ctx, `
		SELECT si.id, si.name, si.company_id, si.customer_id,
		       si.outstanding_amount::float8, si.due_date
		FROM sales_invoice si
		WHERE si.docstatus = 1
		  AND si.outstanding_amount > 0
		  AND si.due_date < current_date
		  AND NOT EXISTS (
		    SELECT 1 FROM notification_dispatch nd
		    WHERE nd.event_key = 'invoice.overdue'
		      AND nd.payload->>'document_id' = si.id
		      AND nd.created_at::date = current_date
		  )
		ORDER BY si.due_date
		LIMIT 500`)
	if err != nil {
		d.log.Error("overdue scan", "err", err)
		return
	}
	var work []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name, &r.companyID, &r.customerID, &r.outstanding, &r.dueDate); err != nil {
			rows.Close()
			d.log.Error("overdue scan scan", "err", err)
			return
		}
		work = append(work, r)
	}
	rows.Close()
	if len(work) == 0 {
		return
	}

	now := time.Now()
	for _, r := range work {
		daysOverdue := int(now.Sub(r.dueDate).Hours() / 24)
		// Re-issue via Fire so all the same matching + recipient-expansion
		// + dispatch-queuing semantics apply.
		d.Fire("invoice.overdue", map[string]any{
			"company_id":    r.companyID,
			"doctype":       "sales_invoice",
			"document_id":   r.id,
			"document_name": r.name,
			"customer_id":   r.customerID,
			"outstanding":   r.outstanding,
			"days_overdue":  daysOverdue,
			"due_date":      r.dueDate.Format("2006-01-02"),
		})
	}
}

