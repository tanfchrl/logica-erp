package materialrequest

import (
	"testing"

	"github.com/shopspring/decimal"
)

// mkLine builds a fixture line. Counters are set explicitly so each test
// stays self-documenting.
func mkLine(qty, ordered, received, issued, transferred int) MaterialRequestLine {
	d := func(n int) decimal.Decimal { return decimal.NewFromInt(int64(n)) }
	return MaterialRequestLine{
		Qty: d(qty), OrderedQty: d(ordered), ReceivedQty: d(received),
		IssuedQty: d(issued), TransferredQty: d(transferred),
	}
}

func TestRecomputeStatus_PurchasePurpose(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		lines   []MaterialRequestLine
		want    string
		current string
	}{
		{"no progress", []MaterialRequestLine{mkLine(10, 0, 0, 0, 0)}, StatusPending, StatusPending},
		{"partially ordered", []MaterialRequestLine{mkLine(10, 4, 0, 0, 0)}, StatusPartiallyOrdered, StatusPending},
		{"fully ordered, none received", []MaterialRequestLine{mkLine(10, 10, 0, 0, 0)}, StatusOrdered, StatusPending},
		{"fully ordered + fully received", []MaterialRequestLine{mkLine(10, 10, 10, 0, 0)}, StatusReceived, StatusOrdered},
		// Edge: mixed lines should fall back to partial until ALL are done.
		{"mixed lines", []MaterialRequestLine{mkLine(5, 5, 5, 0, 0), mkLine(3, 0, 0, 0, 0)}, StatusPartiallyOrdered, StatusPending},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			mr := &MaterialRequest{Purpose: PurposePurchase, Status: c.current, Items: c.lines}
			if got := RecomputeStatus(mr); got != c.want {
				t.Errorf("status: want %s got %s", c.want, got)
			}
		})
	}
}

func TestRecomputeStatus_NonPurchasePurposes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		purpose string
		lines   []MaterialRequestLine
		want    string
	}{
		{PurposeMaterialIssue,    []MaterialRequestLine{mkLine(10, 0, 0, 10, 0)}, StatusIssued},
		{PurposeMaterialIssue,    []MaterialRequestLine{mkLine(10, 0, 0, 4, 0)},  StatusPartiallyOrdered},
		{PurposeMaterialTransfer, []MaterialRequestLine{mkLine(10, 0, 0, 0, 10)}, StatusTransferred},
		{PurposeMaterialTransfer, []MaterialRequestLine{mkLine(10, 0, 0, 0, 3)},  StatusPartiallyOrdered},
		{PurposeManufacture,      []MaterialRequestLine{mkLine(10, 10, 0, 0, 0)}, StatusOrdered},
		{PurposeManufacture,      []MaterialRequestLine{mkLine(10, 0, 0, 0, 0)},  StatusPending},
	}
	for _, c := range cases {
		c := c
		t.Run(c.purpose, func(t *testing.T) {
			t.Parallel()
			mr := &MaterialRequest{Purpose: c.purpose, Status: StatusPending, Items: c.lines}
			if got := RecomputeStatus(mr); got != c.want {
				t.Errorf("purpose=%s want %s got %s", c.purpose, c.want, got)
			}
		})
	}
}

func TestRecomputeStatus_ManualStatesAreSticky(t *testing.T) {
	t.Parallel()
	// Cancelled / Stopped / Draft must round-trip unchanged — recompute is
	// only consulted *after* a manual state is cleared via Reopen.
	for _, s := range []string{StatusDraft, StatusStopped, StatusCancelled} {
		mr := &MaterialRequest{Purpose: PurposePurchase, Status: s,
			Items: []MaterialRequestLine{mkLine(10, 10, 10, 0, 0)}}
		if got := RecomputeStatus(mr); got != s {
			t.Errorf("manual state %s should be sticky, got %s", s, got)
		}
	}
}

func TestHasDownstreamFulfilment(t *testing.T) {
	t.Parallel()
	mr := &MaterialRequest{Items: []MaterialRequestLine{mkLine(10, 0, 0, 0, 0)}}
	if hasDownstreamFulfilment(mr) {
		t.Error("untouched MR should not block cancel")
	}
	for _, ctr := range []struct {
		name string
		l    MaterialRequestLine
	}{
		{"ordered", mkLine(10, 1, 0, 0, 0)},
		{"received", mkLine(10, 0, 1, 0, 0)},
		{"issued", mkLine(10, 0, 0, 1, 0)},
		{"transferred", mkLine(10, 0, 0, 0, 1)},
	} {
		ctr := ctr
		t.Run(ctr.name, func(t *testing.T) {
			t.Parallel()
			mr := &MaterialRequest{Items: []MaterialRequestLine{ctr.l}}
			if !hasDownstreamFulfilment(mr) {
				t.Errorf("MR with %s>0 should block cancel", ctr.name)
			}
		})
	}
}

func TestValidPurpose(t *testing.T) {
	t.Parallel()
	good := []string{PurposePurchase, PurposeMaterialTransfer, PurposeMaterialIssue, PurposeManufacture}
	for _, g := range good {
		if !validPurpose(g) {
			t.Errorf("%s should be valid", g)
		}
	}
	bad := []string{"", "subcontracting", "anything_else", "PURCHASE"}
	for _, b := range bad {
		if validPurpose(b) {
			t.Errorf("%q should be invalid", b)
		}
	}
}
