// Package valuation holds inventory valuation strategies. FIFO is the only
// strategy implemented for Phase 2; MovingAverage and LIFO follow the same
// Strategy interface and can be added without changing call sites.
package valuation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// OutgoingRate determines the per-unit valuation rate for an issue of `qty`
// units of `itemID` from `warehouseID` at `posting_datetime`, using the FIFO
// queue derived from earlier stock_ledger_entry rows.
//
// Returns the per-unit rate to use for this issue, plus the new on-hand qty
// and on-hand value after the issue (caller passes these into the SLE row).
//
// FIFO algorithm:
//   1. Walk SLE rows for (item, warehouse) in posting_datetime ASC order,
//      building a queue of (qty_remaining, rate) for each incoming row.
//   2. For each outgoing row in the past, consume from the queue head.
//   3. Stop when the queue head represents the receipts that should still
//      be on hand at `posting_datetime`.
//   4. Consume `qty` from the queue head(s) at their rates; weighted-average
//      across the consumed slices gives the issue rate.
//
// Phase 2 simplification: we treat each SLE row's `incoming_rate` as the lot
// rate (no batch splitting). For receipts that themselves came from issues
// (e.g. material returns) we use `valuation_rate` from the receipt row.
func OutgoingRate(ctx context.Context, tx pgx.Tx, itemID, warehouseID string, qty decimal.Decimal) (rate, balanceQty, balanceValue decimal.Decimal, err error) {
	if !qty.IsPositive() {
		return decimal.Zero, decimal.Zero, decimal.Zero, errors.New("valuation: qty must be > 0")
	}

	rows, err := tx.Query(ctx, `
		SELECT actual_qty, incoming_rate, valuation_rate, qty_after_transaction
		FROM stock_ledger_entry
		WHERE item_id = $1 AND warehouse_id = $2 AND is_cancelled = false
		ORDER BY posting_datetime, created_at, id`,
		itemID, warehouseID)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, err
	}
	defer rows.Close()

	type lot struct {
		qty  decimal.Decimal
		rate decimal.Decimal
	}
	var queue []lot
	for rows.Next() {
		var (
			act           decimal.Decimal
			incoming      *decimal.Decimal
			val           decimal.Decimal
			afterTxn      decimal.Decimal
		)
		if err := rows.Scan(&act, &incoming, &val, &afterTxn); err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, err
		}
		if act.IsPositive() {
			r := val
			if incoming != nil && incoming.IsPositive() {
				r = *incoming
			}
			queue = append(queue, lot{qty: act, rate: r})
		} else {
			// Outgoing: consume from queue head
			remain := act.Neg()
			for remain.IsPositive() && len(queue) > 0 {
				head := &queue[0]
				if head.qty.LessThanOrEqual(remain) {
					remain = remain.Sub(head.qty)
					queue = queue[1:]
				} else {
					head.qty = head.qty.Sub(remain)
					remain = decimal.Zero
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, err
	}

	// Compute on-hand from remaining queue.
	onHandQty := decimal.Zero
	onHandValue := decimal.Zero
	for _, l := range queue {
		onHandQty = onHandQty.Add(l.qty)
		onHandValue = onHandValue.Add(l.qty.Mul(l.rate))
	}
	if onHandQty.LessThan(qty) {
		return decimal.Zero, decimal.Zero, decimal.Zero,
			fmt.Errorf("valuation: insufficient stock for item %s in warehouse %s: have %s, need %s",
				itemID, warehouseID, onHandQty, qty)
	}

	// Consume `qty` from queue head(s); accumulate weighted-average issue rate.
	consumedValue := decimal.Zero
	remain := qty
	for remain.IsPositive() && len(queue) > 0 {
		head := &queue[0]
		if head.qty.LessThanOrEqual(remain) {
			consumedValue = consumedValue.Add(head.qty.Mul(head.rate))
			remain = remain.Sub(head.qty)
			queue = queue[1:]
		} else {
			consumedValue = consumedValue.Add(remain.Mul(head.rate))
			head.qty = head.qty.Sub(remain)
			remain = decimal.Zero
		}
	}
	issueRate := consumedValue.Div(qty)

	// New balance after this issue.
	newQty := onHandQty.Sub(qty)
	newValue := decimal.Zero
	for _, l := range queue {
		newValue = newValue.Add(l.qty.Mul(l.rate))
	}
	return issueRate, newQty, newValue, nil
}

// IncomingBalance computes the on-hand qty and value AFTER a receipt of `qty`
// units at `incomingRate`, using FIFO accumulation.
func IncomingBalance(ctx context.Context, tx pgx.Tx, itemID, warehouseID string, qty, incomingRate decimal.Decimal) (balanceQty, balanceValue decimal.Decimal, err error) {
	rows, err := tx.Query(ctx, `
		SELECT qty_after_transaction, stock_value
		FROM stock_ledger_entry
		WHERE item_id = $1 AND warehouse_id = $2 AND is_cancelled = false
		ORDER BY posting_datetime DESC, created_at DESC, id DESC
		LIMIT 1`, itemID, warehouseID)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	defer rows.Close()
	var lastQty, lastValue decimal.Decimal
	if rows.Next() {
		if err := rows.Scan(&lastQty, &lastValue); err != nil {
			return decimal.Zero, decimal.Zero, err
		}
	}
	return lastQty.Add(qty), lastValue.Add(qty.Mul(incomingRate)), nil
}
