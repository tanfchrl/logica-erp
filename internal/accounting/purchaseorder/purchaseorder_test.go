package purchaseorder

import (
	"testing"

	"github.com/shopspring/decimal"
)

func line(qty, received, billed int) PurchaseOrderLine {
	return PurchaseOrderLine{
		Qty:         decimal.NewFromInt(int64(qty)),
		ReceivedQty: decimal.NewFromInt(int64(received)),
		BilledQty:   decimal.NewFromInt(int64(billed)),
	}
}

func TestRecomputeStatus_Table(t *testing.T) {
	t.Parallel()
	type tc struct {
		name  string
		lines []PurchaseOrderLine
		want  string
	}
	cases := []tc{
		{
			name:  "nothing received or billed → To Receive and Bill",
			lines: []PurchaseOrderLine{line(10, 0, 0)},
			want:  StatusToReceiveAndBill,
		},
		{
			name:  "fully received, nothing billed → To Bill",
			lines: []PurchaseOrderLine{line(10, 10, 0)},
			want:  StatusToBill,
		},
		{
			name:  "fully billed, nothing received → To Receive",
			lines: []PurchaseOrderLine{line(10, 0, 10)},
			want:  StatusToReceive,
		},
		{
			name:  "fully received + billed → Completed",
			lines: []PurchaseOrderLine{line(10, 10, 10)},
			want:  StatusCompleted,
		},
		{
			name: "partial — one line complete, the other not → To Receive and Bill",
			lines: []PurchaseOrderLine{
				line(10, 10, 10), // done
				line(5, 0, 0),    // pending
			},
			want: StatusToReceiveAndBill,
		},
		{
			name: "all lines received but one is partially billed → To Bill",
			lines: []PurchaseOrderLine{
				line(10, 10, 5),
				line(5, 5, 5),
			},
			want: StatusToBill,
		},
		{
			name: "over-receive shouldn't break recompute (defensive)",
			// We don't allow over-receipt in the service, but if data drift
			// ever puts received > qty, recompute should still classify it
			// as "received complete" rather than crash or loop.
			lines: []PurchaseOrderLine{line(10, 12, 12)},
			want:  StatusCompleted,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			po := &PurchaseOrder{Items: c.lines}
			got := RecomputeStatus(po)
			if got != c.want {
				t.Errorf("status: want %s got %s", c.want, got)
			}
		})
	}
}

func TestHasDownstreamFulfilment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		po   *PurchaseOrder
		want bool
	}{
		{"fresh draft", &PurchaseOrder{Items: []PurchaseOrderLine{line(5, 0, 0)}}, false},
		{"received only", &PurchaseOrder{Items: []PurchaseOrderLine{line(5, 1, 0)}}, true},
		{"billed only", &PurchaseOrder{Items: []PurchaseOrderLine{line(5, 0, 1)}}, true},
		{"both", &PurchaseOrder{Items: []PurchaseOrderLine{line(5, 5, 5)}}, true},
		{"mixed across lines", &PurchaseOrder{Items: []PurchaseOrderLine{line(5, 0, 0), line(2, 1, 0)}}, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := hasDownstreamFulfilment(c.po); got != c.want {
				t.Errorf("hasDownstreamFulfilment = %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseDec_AcceptsEmpty(t *testing.T) {
	t.Parallel()
	// Empty string is treated as zero so empty optional inputs from the FE
	// don't error out the create path.
	got, err := parseDec("")
	if err != nil {
		t.Fatalf("empty string should parse, got %v", err)
	}
	if !got.IsZero() {
		t.Errorf("empty should yield zero, got %s", got)
	}
}

func TestParseDec_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	got, err := parseDec("  123.45  ")
	if err != nil {
		t.Fatalf("trim failed: %v", err)
	}
	if got.String() != "123.45" {
		t.Errorf("want 123.45 got %s", got)
	}
}
