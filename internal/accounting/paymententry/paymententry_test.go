package paymententry

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestComputeUnallocated(t *testing.T) {
	dec := func(s string) decimal.Decimal {
		d, err := decimal.NewFromString(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return d
	}

	cases := []struct {
		name           string
		paid           string
		allocated      string
		deductions     string
		wantUnallocated string
		wantErrSubstr  string
	}{
		// The motivating case: invoice 5,550,000 fully cleared by 5,000,000
		// cash + 550,000 PPh23. paid + deductions = allocated, unallocated 0.
		{"full clearance with withholding", "5000000", "5550000", "550000", "0", ""},

		// No withholding, partial settlement: cash 3,000,000 against a single
		// 3,000,000 allocation. paid = allocated, deductions = 0.
		{"partial without withholding", "3000000", "3000000", "0", "0", ""},

		// No withholding, full settlement.
		{"full without withholding", "5550000", "5550000", "0", "0", ""},

		// Advance: customer paid 7,000,000 against a 5,000,000 invoice.
		// Unallocated 2,000,000 sits as advance credit.
		{"advance overpayment", "7000000", "5000000", "0", "2000000", ""},

		// Over-allocation must be rejected. paid+deductions = 5,500,000 but
		// references claim 6,000,000.
		{"over-allocated rejects", "5000000", "6000000", "500000", "", "over-allocated"},

		// Zero everywhere: vacuously balanced.
		{"all zero", "0", "0", "0", "0", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := computeUnallocated(dec(tc.paid), dec(tc.allocated), dec(tc.deductions))
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil (unallocated=%s)", tc.wantErrSubstr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("want error containing %q, got %q", tc.wantErrSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(dec(tc.wantUnallocated)) {
				t.Fatalf("unallocated: want %s, got %s", tc.wantUnallocated, got)
			}
		})
	}
}
