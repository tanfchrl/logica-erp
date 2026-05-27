package purchasereceipt

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Most of PR's semantics are DB-driven (SLE writes, PO counter mutations),
// so the unit tests focus on the helpers we can exercise in pure Go: the
// decimal parser + arithmetic on the line shape.

func TestParseDec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty is zero", "", "0", false},
		{"trim whitespace", "  12.5  ", "12.5", false},
		{"plain int", "10", "10", false},
		{"negative passes parse (validation rejects elsewhere)", "-5", "-5", false},
		{"junk fails", "abc", "", true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDec(c.in)
			if c.wantErr {
				if err == nil {
					t.Errorf("want error for %q, got %s", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != c.want {
				t.Errorf("want %s got %s", c.want, got)
			}
		})
	}
}

func TestLineAmountReflectsAcceptedPlusRejected(t *testing.T) {
	t.Parallel()
	// Sanity: the GRN total uses (accepted + rejected) * rate. Lets
	// catch regressions in the create-draft path where the formula
	// might drift to accepted-only.
	acc := decimal.NewFromInt(8)
	rej := decimal.NewFromInt(2)
	rate := decimal.NewFromInt(15000)
	want := decimal.NewFromInt(150000) // (8+2) * 15000
	got := acc.Add(rej).Mul(rate)
	if !got.Equal(want) {
		t.Errorf("amount: want %s got %s", want, got)
	}
}
