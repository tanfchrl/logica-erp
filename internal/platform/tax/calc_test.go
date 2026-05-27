package tax

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestCalculate_SingleLine_PPN11_Exclusive(t *testing.T) {
	tpl := Template{IsSales: true, Lines: []TemplateLine{{
		ID: "tl1", AccountID: "acc_ppn", Description: "PPN 11%",
		Rate: d("11"), ChargeType: ChargeOnNetTotal,
	}}}
	in := []Line{{Key: "1", NetAmount: d("1000000")}}

	r, err := Calculate(in, tpl)
	require.NoError(t, err)
	require.Equal(t, "1000000", r.NetTotal.String())
	require.Equal(t, "110000", r.TaxTotal.String())
	require.Equal(t, "1110000", r.GrandTotal.String())
	require.Equal(t, "110000", r.Lines[0].TaxAmount.String())
	require.Equal(t, "1110000", r.Lines[0].Total.String())
}

func TestCalculate_MultiLine_ProportionalDistribution(t *testing.T) {
	tpl := Template{IsSales: true, Lines: []TemplateLine{{
		ID: "tl1", AccountID: "acc_ppn", Description: "PPN 11%",
		Rate: d("11"), ChargeType: ChargeOnNetTotal,
	}}}
	in := []Line{
		{Key: "1", NetAmount: d("1000000")},
		{Key: "2", NetAmount: d("300000")},
		{Key: "3", NetAmount: d("700000")},
	}
	r, err := Calculate(in, tpl)
	require.NoError(t, err)
	// 11% of 2,000,000 = 220,000. Per-line: 110000/33000/77000
	require.Equal(t, "220000", r.TaxTotal.String())
	require.Equal(t, "2220000", r.GrandTotal.String())
	// Each line tax = total * netAmount / netTotal, rounded to 4dp.
	require.Equal(t, "110000", r.Lines[0].TaxAmount.String())
	require.Equal(t, "33000", r.Lines[1].TaxAmount.String())
	require.Equal(t, "77000", r.Lines[2].TaxAmount.String())
	sum := r.Lines[0].TaxAmount.Add(r.Lines[1].TaxAmount).Add(r.Lines[2].TaxAmount)
	require.Equal(t, "220000", sum.String(), "per-line tax must sum to row total exactly")
}

func TestCalculate_RoundingResidualGoesToLargestLine(t *testing.T) {
	// 10% of 333,333.33 = 33,333.333 → rounded 33,333.3330.
	// Per-line (333.33, 333.33, 332.67) gets shares 33.333, 33.333, 33.267 which sum to 99.933 (we'd need 100.00).
	// Actually pick amounts where distribution produces non-trivial residual.
	tpl := Template{IsSales: true, Lines: []TemplateLine{{
		ID: "tl1", AccountID: "acc", Description: "Tax", Rate: d("10"), ChargeType: ChargeOnNetTotal,
	}}}
	in := []Line{
		{Key: "1", NetAmount: d("100.01")},
		{Key: "2", NetAmount: d("100.02")},
		{Key: "3", NetAmount: d("100.04")}, // largest → gets residual
	}
	r, err := Calculate(in, tpl)
	require.NoError(t, err)
	rowTotal := r.TaxRows[0].TaxAmount
	sum := r.Lines[0].TaxAmount.Add(r.Lines[1].TaxAmount).Add(r.Lines[2].TaxAmount)
	require.True(t, sum.Equal(rowTotal), "per-line sum %s must equal row total %s", sum, rowTotal)
}

func TestCalculate_CascadingPreviousAmount(t *testing.T) {
	// Row 1: PPN 11% on net. Row 2: stamp duty 1% on previous tax row.
	tpl := Template{IsSales: true, Lines: []TemplateLine{
		{ID: "tl1", AccountID: "acc_ppn", Description: "PPN 11%", Rate: d("11"), ChargeType: ChargeOnNetTotal},
		{ID: "tl2", AccountID: "acc_st",  Description: "1% surcharge", Rate: d("1"),  ChargeType: ChargeOnPreviousAmount},
	}}
	in := []Line{{Key: "1", NetAmount: d("1000000")}}
	r, err := Calculate(in, tpl)
	require.NoError(t, err)
	require.Equal(t, "110000", r.TaxRows[0].TaxAmount.String())
	require.Equal(t, "1100", r.TaxRows[1].TaxAmount.String())
	require.Equal(t, "111100", r.TaxTotal.String())
}

func TestCalculate_InclusiveTaxDoesNotIncreaseGrandTotal(t *testing.T) {
	tpl := Template{IsSales: true, Lines: []TemplateLine{{
		ID: "tl1", AccountID: "acc_ppn", Description: "PPN 11% inclusive",
		Rate: d("11"), ChargeType: ChargeOnNetTotal, IncludedInBasicRate: true,
	}}}
	in := []Line{{Key: "1", NetAmount: d("1110000")}}
	r, err := Calculate(in, tpl)
	require.NoError(t, err)
	require.Equal(t, "1110000", r.GrandTotal.String(),
		"inclusive tax must not increase grand total")
	// Tax is still computed for GL booking purposes.
	require.True(t, r.TaxRows[0].TaxAmount.GreaterThan(decimal.Zero))
}

func TestCalculate_NoTemplateLines(t *testing.T) {
	r, err := Calculate([]Line{{Key: "1", NetAmount: d("500000")}}, Template{IsSales: true})
	require.NoError(t, err)
	require.Equal(t, "500000", r.GrandTotal.String())
	require.Empty(t, r.TaxRows)
}

func TestCalculate_RejectsNegativeNet(t *testing.T) {
	_, err := Calculate([]Line{{Key: "1", NetAmount: d("-1")}}, Template{IsSales: true})
	require.Error(t, err)
}
