// Package payroll holds the Indonesian payroll math: BPJS and PPh 21.
//
// Rates here reflect the rules in force at the time of writing. Long-term they
// should be stored as a versioned table keyed by effective_date; for Phase 5 MVP
// they live as exported constants so the calculator is easy to test.
package payroll

import "github.com/shopspring/decimal"

// ---- BPJS Kesehatan (Health) ----
// Employer 4% + Employee 1% of gross, capped at IDR 12,000,000 base salary.
var (
	BPJSKesehatanEmployerRate = decimal.NewFromFloat(0.04)
	BPJSKesehatanEmployeeRate = decimal.NewFromFloat(0.01)
	BPJSKesehatanCap          = decimal.NewFromInt(12_000_000)
)

// ---- BPJS Ketenagakerjaan (Employment) ----
// JHT (Jaminan Hari Tua): employer 3.7% + employee 2%, base = full gross, no cap.
// JKK (Jaminan Kecelakaan Kerja): employer only, varies 0.24%–1.74% by industry risk.
// JKM (Jaminan Kematian): employer only, 0.30%.
// JP  (Jaminan Pensiun): employer 2% + employee 1%, base capped at IDR 10,547,400/month.
var (
	BPJSJHTEmployerRate     = decimal.NewFromFloat(0.037)
	BPJSJHTEmployeeRate     = decimal.NewFromFloat(0.02)
	BPJSJKKDefaultRate      = decimal.NewFromFloat(0.0024) // low-risk default
	BPJSJKMRate             = decimal.NewFromFloat(0.003)
	BPJSJPEmployerRate      = decimal.NewFromFloat(0.02)
	BPJSJPEmployeeRate      = decimal.NewFromFloat(0.01)
	BPJSJPCap               = decimal.NewFromFloat(10_547_400)
)

// BPJSResult is the per-month BPJS breakdown for an employee.
type BPJSResult struct {
	// Employee deductions (withheld from salary).
	KesehatanEmployee decimal.Decimal
	JHTEmployee       decimal.Decimal
	JPEmployee        decimal.Decimal
	TotalEmployee     decimal.Decimal

	// Employer contributions (separate expense lines).
	KesehatanEmployer decimal.Decimal
	JHTEmployer       decimal.Decimal
	JKKEmployer       decimal.Decimal
	JKMEmployer       decimal.Decimal
	JPEmployer        decimal.Decimal
	TotalEmployer     decimal.Decimal
}

// CalculateBPJS computes per-month BPJS for a given gross monthly salary.
// jkkRate is the per-employer industry rate (0.0024 default for low-risk).
// Pass decimal.Zero for jkkRate to use BPJSJKKDefaultRate.
func CalculateBPJS(gross decimal.Decimal, jkkRate decimal.Decimal) BPJSResult {
	if jkkRate.IsZero() {
		jkkRate = BPJSJKKDefaultRate
	}
	kBase := minDec(gross, BPJSKesehatanCap)
	jpBase := minDec(gross, BPJSJPCap)

	r := BPJSResult{}
	r.KesehatanEmployee = kBase.Mul(BPJSKesehatanEmployeeRate).Round(4)
	r.JHTEmployee = gross.Mul(BPJSJHTEmployeeRate).Round(4)
	r.JPEmployee = jpBase.Mul(BPJSJPEmployeeRate).Round(4)
	r.TotalEmployee = r.KesehatanEmployee.Add(r.JHTEmployee).Add(r.JPEmployee)

	r.KesehatanEmployer = kBase.Mul(BPJSKesehatanEmployerRate).Round(4)
	r.JHTEmployer = gross.Mul(BPJSJHTEmployerRate).Round(4)
	r.JKKEmployer = gross.Mul(jkkRate).Round(4)
	r.JKMEmployer = gross.Mul(BPJSJKMRate).Round(4)
	r.JPEmployer = jpBase.Mul(BPJSJPEmployerRate).Round(4)
	r.TotalEmployer = r.KesehatanEmployer.Add(r.JHTEmployer).Add(r.JKKEmployer).Add(r.JKMEmployer).Add(r.JPEmployer)

	return r
}

func minDec(a, b decimal.Decimal) decimal.Decimal {
	if a.LessThan(b) {
		return a
	}
	return b
}
