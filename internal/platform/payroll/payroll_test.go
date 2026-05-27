package payroll

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestBPJS_AboveAllCaps(t *testing.T) {
	// Gross 15M: Kesehatan capped at 12M base; JP capped at 10,547,400; JHT uncapped.
	r := CalculateBPJS(dec("15000000"), decimal.Zero)

	// Kesehatan employee = 12,000,000 × 0.01 = 120,000
	require.Equal(t, "120000", r.KesehatanEmployee.StringFixed(0))
	// Kesehatan employer = 12,000,000 × 0.04 = 480,000
	require.Equal(t, "480000", r.KesehatanEmployer.StringFixed(0))
	// JHT employee = 15M × 0.02 = 300,000
	require.Equal(t, "300000", r.JHTEmployee.StringFixed(0))
	// JHT employer = 15M × 0.037 = 555,000
	require.Equal(t, "555000", r.JHTEmployer.StringFixed(0))
	// JP capped at 10,547,400 × 0.01 = 105,474
	require.Equal(t, "105474", r.JPEmployee.StringFixed(0))
	require.Equal(t, "210948", r.JPEmployer.StringFixed(0))
}

func TestBPJS_BelowCaps(t *testing.T) {
	// Gross 5M: under both caps.
	r := CalculateBPJS(dec("5000000"), decimal.Zero)

	require.Equal(t, "50000", r.KesehatanEmployee.StringFixed(0))   // 1%
	require.Equal(t, "200000", r.KesehatanEmployer.StringFixed(0))  // 4%
	require.Equal(t, "100000", r.JHTEmployee.StringFixed(0))        // 2%
	require.Equal(t, "185000", r.JHTEmployer.StringFixed(0))        // 3.7%
	require.Equal(t, "50000", r.JPEmployee.StringFixed(0))          // 1%
	require.Equal(t, "100000", r.JPEmployer.StringFixed(0))         // 2%
	require.Equal(t, "12000", r.JKKEmployer.StringFixed(0))         // 0.24%
	require.Equal(t, "15000", r.JKMEmployer.StringFixed(0))         // 0.3%
}

// PPh 21 test: a TK/0 employee earning Rp 10,000,000/month.
//
// Expected calculation:
//   gross_annual = 120,000,000
//   biaya_jabatan = min(5% × 120M, 6M) = 6,000,000
//   BPJS employee (per CalculateBPJS @ 10M): JHT 200K + JP 100K + Kesehatan 100K = 400K/month
//     → annual 4,800,000
//   netto = 120M - 6M - 4.8M = 109,200,000
//   PKP   = 109,200,000 - 54,000,000 = 55,200,000 → round down to 1000 = 55,200,000
//   PPh21 annual = 55,200,000 × 5% = 2,760,000 (entirely in first bracket)
//   PPh21 monthly = 230,000
func TestPPh21_TK0_10M(t *testing.T) {
	bpjs := CalculateBPJS(dec("10000000"), decimal.Zero)
	r, err := CalculatePPh21(dec("10000000"), bpjs.TotalEmployee, "TK/0")
	require.NoError(t, err)

	require.Equal(t, "120000000", r.GrossAnnual.StringFixed(0))
	require.Equal(t, "6000000",   r.BiayaJabatanAnnual.StringFixed(0))
	require.Equal(t, "4800000",   r.BPJSEmployeeAnnual.StringFixed(0))
	require.Equal(t, "109200000", r.NettoAnnual.StringFixed(0))
	require.Equal(t, "54000000",  r.PTKPApplied.StringFixed(0))
	require.Equal(t, "55200000",  r.PKPAnnual.StringFixed(0))
	require.Equal(t, "2760000",   r.PPhAnnual.StringFixed(0))
	require.Equal(t, "230000",    r.PPhMonthly.StringFixed(0))
}

// PPh 21 test: a K/2 employee (married, 2 dependents) earning Rp 30,000,000/month.
// Crosses into the 15% bracket.
//
//   gross_annual  = 360,000,000
//   biaya_jabatan = min(5% × 360M, 6M) = 6,000,000
//   BPJS employee @ 30M (cap kicks in on Kesehatan + JP):
//     Kesehatan: 12M × 1% = 120,000
//     JHT: 30M × 2% = 600,000
//     JP: 10,547,400 × 1% = 105,474
//     total = 825,474/mo → annual 9,905,688
//   netto = 360M - 6M - 9,905,688 = 344,094,312
//   PKP   = 344,094,312 - 67,500,000 = 276,594,312 → 276,594,000
//   PPh21:
//     5% × 60M               = 3,000,000
//     15% × (250M-60M=190M)  = 28,500,000
//     25% × (276,594,000 - 250M = 26,594,000) = 6,648,500
//     total = 38,148,500
//   monthly = 38,148,500 / 12 = 3,179,041.666... → rounded to 3,179,042
func TestPPh21_K2_30M(t *testing.T) {
	bpjs := CalculateBPJS(dec("30000000"), decimal.Zero)
	r, err := CalculatePPh21(dec("30000000"), bpjs.TotalEmployee, "K/2")
	require.NoError(t, err)

	require.Equal(t, "360000000", r.GrossAnnual.StringFixed(0))
	require.Equal(t, "6000000",   r.BiayaJabatanAnnual.StringFixed(0))
	require.Equal(t, "9905688",   r.BPJSEmployeeAnnual.StringFixed(0))
	require.Equal(t, "67500000",  r.PTKPApplied.StringFixed(0))
	require.Equal(t, "276594000", r.PKPAnnual.StringFixed(0))
	require.Equal(t, "38148500",  r.PPhAnnual.StringFixed(0))
	require.Equal(t, "3179042",   r.PPhMonthly.StringFixed(0))
}

func TestPPh21_BelowPTKP(t *testing.T) {
	// 4M/mo × 12 = 48M annual; netto < 54M PTKP → no tax.
	r, err := CalculatePPh21(dec("4000000"), decimal.Zero, "TK/0")
	require.NoError(t, err)
	require.Equal(t, "0", r.PKPAnnual.StringFixed(0))
	require.Equal(t, "0", r.PPhMonthly.StringFixed(0))
}

func TestPPh21_TopBracketHit(t *testing.T) {
	// 500M/mo × 12 = 6B annual. After everything we land deep in the 35% bracket.
	bpjs := CalculateBPJS(dec("500000000"), decimal.Zero)
	r, err := CalculatePPh21(dec("500000000"), bpjs.TotalEmployee, "TK/0")
	require.NoError(t, err)
	require.True(t, r.PPhAnnual.GreaterThan(dec("1500000000")), "expected very large annual PPh, got %s", r.PPhAnnual)
}
