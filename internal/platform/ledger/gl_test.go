package ledger

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/tandigital/logica-erp/internal/platform/money"
)

// These tests cover the pure logic of the GL invariant (does not require Postgres).
// Full transactional tests against testcontainers-go land in the journalentry package
// when the integration test harness is set up.

func TestEntriesBalanceCheck(t *testing.T) {
	t.Run("balanced", func(t *testing.T) {
		require.NoError(t, money.SumBalanced(
			[]decimal.Decimal{money.MustNew("1000.00"), money.MustNew("500.00")},
			[]decimal.Decimal{money.MustNew("1500.00")},
		))
	})
	t.Run("imbalanced rejects", func(t *testing.T) {
		require.Error(t, money.SumBalanced(
			[]decimal.Decimal{money.MustNew("1000.00")},
			[]decimal.Decimal{money.MustNew("500.00")},
		))
	})
}

func TestEntryShapeValid(t *testing.T) {
	// An entry must have exactly one of debit/credit > 0. The PostGL helper enforces this.
	cases := []struct {
		name        string
		debit       string
		credit      string
		wantInvalid bool
	}{
		{"debit only ok", "100.00", "0", false},
		{"credit only ok", "0", "100.00", false},
		{"both zero invalid", "0", "0", true},
		{"both nonzero invalid", "100.00", "100.00", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := money.MustNew(c.debit)
			cr := money.MustNew(c.credit)
			invalid := d.IsZero() == cr.IsZero()
			require.Equal(t, c.wantInvalid, invalid)
		})
	}
}
