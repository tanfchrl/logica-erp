package money

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSumBalanced_Balanced(t *testing.T) {
	dr := []decimal.Decimal{MustNew("1000.00"), MustNew("500.00")}
	cr := []decimal.Decimal{MustNew("1500.00")}
	require.NoError(t, SumBalanced(dr, cr))
}

func TestSumBalanced_Imbalanced(t *testing.T) {
	dr := []decimal.Decimal{MustNew("1000.00")}
	cr := []decimal.Decimal{MustNew("999.99")}
	require.Error(t, SumBalanced(dr, cr))
}

func TestSumBalanced_WithinEpsilon(t *testing.T) {
	// 0.0001 difference is exactly at the epsilon threshold (Precision = 4 → epsilon = 0.0001).
	// We treat "within or equal to epsilon" as balanced.
	dr := []decimal.Decimal{MustNew("1000.0001")}
	cr := []decimal.Decimal{MustNew("1000.0000")}
	require.NoError(t, SumBalanced(dr, cr))
}

func TestValidate_RejectsNegative(t *testing.T) {
	require.ErrorIs(t, Validate(MustNew("-1.00")), ErrNegative)
	require.NoError(t, Validate(MustNew("0")))
	require.NoError(t, Validate(MustNew("1.00")))
}

func TestNew_RoundsToPrecision(t *testing.T) {
	d, err := New("1.23456789")
	require.NoError(t, err)
	require.Equal(t, "1.2346", d.String())
}
