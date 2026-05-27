// Package money holds money helpers. The chosen primitive is shopspring/decimal.
// Code that handles monetary values MUST go through this package or take decimal.Decimal directly.
// Floats are banned by the linter outside of designated escapes.
package money

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Amount precision used across the system. Aligned with numeric(18,4) in Postgres.
const Precision = 4

// Zero is the zero value at the system precision.
var Zero = decimal.Zero

// New parses a decimal-friendly string ("1234.56") into an Amount rounded to Precision.
func New(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("money: parse %q: %w", s, err)
	}
	return d.Round(Precision), nil
}

// MustNew is like New but panics on error. Use only with literals.
func MustNew(s string) decimal.Decimal {
	d, err := New(s)
	if err != nil {
		panic(err)
	}
	return d
}

// SumBalanced checks that the sum of debits equals the sum of credits within an epsilon
// equal to one unit at Precision (i.e. 0.0001 for Precision=4).
// Returns nil if balanced; otherwise an error reporting the diff.
func SumBalanced(debits, credits []decimal.Decimal) error {
	dSum := decimal.Zero
	for _, d := range debits {
		dSum = dSum.Add(d)
	}
	cSum := decimal.Zero
	for _, c := range credits {
		cSum = cSum.Add(c)
	}
	diff := dSum.Sub(cSum).Abs()
	epsilon := decimal.New(1, -int32(Precision))
	if diff.GreaterThan(epsilon) {
		return fmt.Errorf("ledger imbalance: debit %s vs credit %s (diff %s)", dSum.String(), cSum.String(), diff.String())
	}
	return nil
}

// ErrNegative is returned by Validate when an amount is negative.
var ErrNegative = errors.New("money: negative amount not allowed")

// Validate rejects negative amounts. Use on inputs that must be >= 0.
func Validate(d decimal.Decimal) error {
	if d.IsNegative() {
		return ErrNegative
	}
	return nil
}
