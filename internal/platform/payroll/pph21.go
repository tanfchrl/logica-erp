package payroll

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// PPh 21 — Indonesian individual income tax withheld at source.
//
// Method here: classic annualized progressive calculation, divided by 12 for the
// monthly withholding. This is mathematically consistent with the year-end
// reconciliation and easier to audit than the 2024 TER tables.
//
// Inputs per month:
//   gross_monthly        — employee taxable income for the month
//   bpjs_employee_total  — JHT/JP/Kesehatan employee portions for the month (deductible from netto)
//   ptkp_status          — TK/0, TK/1..3, K/0..3
//
// Algorithm:
//   1. gross_annual           = gross_monthly × 12
//   2. biaya_jabatan_annual   = min(5% × gross_annual, 6,000,000)
//   3. bpjs_employee_annual   = bpjs_employee_total × 12
//   4. netto_annual           = gross_annual − biaya_jabatan_annual − bpjs_employee_annual
//   5. pkp_annual             = max(0, netto_annual − ptkp_amount)  rounded down to nearest 1000
//   6. pph_annual             = progressive(pkp_annual)
//   7. pph_monthly            = pph_annual / 12

// PTKP (Penghasilan Tidak Kena Pajak) values for 2024+.
var PTKP = map[string]decimal.Decimal{
	"TK/0": decimal.NewFromInt(54_000_000),
	"TK/1": decimal.NewFromInt(58_500_000),
	"TK/2": decimal.NewFromInt(63_000_000),
	"TK/3": decimal.NewFromInt(67_500_000),
	"K/0":  decimal.NewFromInt(58_500_000),
	"K/1":  decimal.NewFromInt(63_000_000),
	"K/2":  decimal.NewFromInt(67_500_000),
	"K/3":  decimal.NewFromInt(72_000_000),
}

// PPh21Bracket is one slice of the progressive table.
type PPh21Bracket struct {
	UpTo decimal.Decimal // upper bound (inclusive). Use decimal.Zero for the top open bracket.
	Rate decimal.Decimal // e.g. 0.05 for 5%
}

// PPh21Brackets — UU HPP rates effective 2022 onward.
var PPh21Brackets = []PPh21Bracket{
	{UpTo: decimal.NewFromInt(60_000_000),       Rate: decimal.NewFromFloat(0.05)},
	{UpTo: decimal.NewFromInt(250_000_000),      Rate: decimal.NewFromFloat(0.15)},
	{UpTo: decimal.NewFromInt(500_000_000),      Rate: decimal.NewFromFloat(0.25)},
	{UpTo: decimal.NewFromInt(5_000_000_000),    Rate: decimal.NewFromFloat(0.30)},
	{UpTo: decimal.Zero,                         Rate: decimal.NewFromFloat(0.35)}, // open bracket
}

// BiayaJabatan limits.
var (
	BiayaJabatanRate       = decimal.NewFromFloat(0.05)
	BiayaJabatanAnnualMax  = decimal.NewFromInt(6_000_000)
)

// PPh21Result is the per-month breakdown.
type PPh21Result struct {
	GrossAnnual        decimal.Decimal
	BiayaJabatanAnnual decimal.Decimal
	BPJSEmployeeAnnual decimal.Decimal
	NettoAnnual        decimal.Decimal
	PTKPApplied        decimal.Decimal
	PKPAnnual          decimal.Decimal
	PPhAnnual          decimal.Decimal
	PPhMonthly         decimal.Decimal
}

// CalculatePPh21 returns the per-month withholding for the given inputs.
// If ptkpStatus is not in the PTKP table, falls back to TK/0.
func CalculatePPh21(grossMonthly, bpjsEmployeeMonthly decimal.Decimal, ptkpStatus string) (PPh21Result, error) {
	if grossMonthly.IsNegative() {
		return PPh21Result{}, errors.New("pph21: gross cannot be negative")
	}
	ptkpAmount, ok := PTKP[ptkpStatus]
	if !ok {
		ptkpAmount = PTKP["TK/0"]
	}

	twelve := decimal.NewFromInt(12)
	grossAnnual := grossMonthly.Mul(twelve)
	biayaJabatanAnnual := minDec(grossAnnual.Mul(BiayaJabatanRate), BiayaJabatanAnnualMax)
	bpjsEmpAnnual := bpjsEmployeeMonthly.Mul(twelve)
	nettoAnnual := grossAnnual.Sub(biayaJabatanAnnual).Sub(bpjsEmpAnnual)

	pkpAnnual := nettoAnnual.Sub(ptkpAmount)
	if pkpAnnual.IsNegative() {
		pkpAnnual = decimal.Zero
	}
	// Round PKP down to nearest 1,000 per DJP rules.
	thousand := decimal.NewFromInt(1000)
	pkpAnnual = pkpAnnual.Div(thousand).Truncate(0).Mul(thousand)

	pphAnnual := progressivePPh21(pkpAnnual)
	pphMonthly := pphAnnual.Div(twelve).Round(0) // monthly withholding rounded to whole rupiah

	return PPh21Result{
		GrossAnnual:        grossAnnual,
		BiayaJabatanAnnual: biayaJabatanAnnual,
		BPJSEmployeeAnnual: bpjsEmpAnnual,
		NettoAnnual:        nettoAnnual,
		PTKPApplied:        ptkpAmount,
		PKPAnnual:          pkpAnnual,
		PPhAnnual:          pphAnnual,
		PPhMonthly:         pphMonthly,
	}, nil
}

// progressivePPh21 computes the annual PPh 21 from the progressive brackets.
func progressivePPh21(pkp decimal.Decimal) decimal.Decimal {
	tax := decimal.Zero
	remaining := pkp
	prev := decimal.Zero
	for _, b := range PPh21Brackets {
		if remaining.LessThanOrEqual(decimal.Zero) {
			break
		}
		var slice decimal.Decimal
		if b.UpTo.IsZero() { // open top bracket
			slice = remaining
		} else {
			width := b.UpTo.Sub(prev)
			if remaining.LessThan(width) {
				slice = remaining
			} else {
				slice = width
			}
			prev = b.UpTo
		}
		tax = tax.Add(slice.Mul(b.Rate))
		remaining = remaining.Sub(slice)
	}
	return tax.Round(0)
}

// FormatPPh21 returns a one-line human summary, useful for slip remarks.
func (r PPh21Result) String() string {
	return fmt.Sprintf("PPh21: gross/yr=%s, biaya jabatan=%s, BPJS=%s, netto=%s, PTKP=%s, PKP=%s, PPh/yr=%s → /mo=%s",
		r.GrossAnnual, r.BiayaJabatanAnnual, r.BPJSEmployeeAnnual, r.NettoAnnual,
		r.PTKPApplied, r.PKPAnnual, r.PPhAnnual, r.PPhMonthly)
}
