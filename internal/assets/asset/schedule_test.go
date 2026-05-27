package asset

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// helper: parse "2026-01-15" without sprinkling time.Parse everywhere.
func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func dec(s string) decimal.Decimal { v, _ := decimal.NewFromString(s); return v }

func TestBuildSchedule_StraightLine_NoProRata_NoSalvage(t *testing.T) {
	t.Parallel()
	// 12-month asset bought on the 1st: every row is exactly 1/12 of gross.
	rows, err := BuildSchedule(ScheduleParams{
		Gross:            dec("1200"),
		Salvage:          decimal.Zero,
		UsefulLifeMonths: 12,
		Method:           MethodStraightLine,
		PurchaseDate:     d("2026-01-01"),
		ProRataBasis:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 12 {
		t.Fatalf("rows: want 12, got %d", len(rows))
	}
	for i, r := range rows {
		if !r.DepreciationAmount.Equal(dec("100")) {
			t.Errorf("row %d: amount %s want 100", i, r.DepreciationAmount)
		}
	}
	if !rows[11].AccumulatedAfter.Equal(dec("1200")) {
		t.Errorf("final accumulated: want 1200, got %s", rows[11].AccumulatedAfter)
	}
}

func TestBuildSchedule_StraightLine_LastRowSnapsRoundingDrift(t *testing.T) {
	t.Parallel()
	// 7-month asset of 1000 — 1000/7 ≈ 142.8571 → rounded to 142.86.
	// Sum of 6 × 142.86 = 857.16; last row should snap to 142.84 to
	// total exactly 1000.
	rows, err := BuildSchedule(ScheduleParams{
		Gross: dec("1000"), Salvage: decimal.Zero, UsefulLifeMonths: 7,
		Method: MethodStraightLine, PurchaseDate: d("2026-01-01"), ProRataBasis: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rows[len(rows)-1].AccumulatedAfter.Equal(dec("1000")) {
		t.Errorf("final accumulated must equal gross, got %s", rows[len(rows)-1].AccumulatedAfter)
	}
}

func TestBuildSchedule_StraightLine_ProRataMidMonth(t *testing.T) {
	t.Parallel()
	// 12-month asset bought on 16 Mar (16 days owned of 31). Monthly nominal
	// = 100. First row = 100 × 16/31 ≈ 51.61. Subsequent rows = 100 each,
	// except the last which snaps to clear the books.
	rows, err := BuildSchedule(ScheduleParams{
		Gross: dec("1200"), Salvage: decimal.Zero, UsefulLifeMonths: 12,
		Method: MethodStraightLine, PurchaseDate: d("2026-03-16"), ProRataBasis: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := rows[0].DepreciationAmount
	if first.GreaterThanOrEqual(dec("100")) || first.LessThan(dec("50")) {
		t.Errorf("first row should be partial month, got %s", first)
	}
	if !rows[len(rows)-1].AccumulatedAfter.Equal(dec("1200")) {
		t.Errorf("final accumulated: want 1200, got %s", rows[len(rows)-1].AccumulatedAfter)
	}
	// First-row schedule date must be end-of-March 2026.
	if rows[0].ScheduleDate.Format("2006-01-02") != "2026-03-31" {
		t.Errorf("first schedule date: want 2026-03-31, got %s", rows[0].ScheduleDate.Format("2006-01-02"))
	}
}

func TestBuildSchedule_StraightLine_FirstDayOfMonthGetsFullPeriod(t *testing.T) {
	t.Parallel()
	// Buying on day 1 with pro-rata enabled should still produce a full first row.
	rows, _ := BuildSchedule(ScheduleParams{
		Gross: dec("1200"), Salvage: decimal.Zero, UsefulLifeMonths: 12,
		Method: MethodStraightLine, PurchaseDate: d("2026-01-01"), ProRataBasis: true,
	})
	if !rows[0].DepreciationAmount.Equal(dec("100")) {
		t.Errorf("day-1 pro-rata should yield full row, got %s", rows[0].DepreciationAmount)
	}
}

func TestBuildSchedule_StraightLine_WithSalvage(t *testing.T) {
	t.Parallel()
	// Gross 1200, salvage 200 → depreciable 1000 / 10 months = 100/mo.
	rows, _ := BuildSchedule(ScheduleParams{
		Gross: dec("1200"), Salvage: dec("200"), UsefulLifeMonths: 10,
		Method: MethodStraightLine, PurchaseDate: d("2026-01-01"), ProRataBasis: false,
	})
	if !rows[len(rows)-1].AccumulatedAfter.Equal(dec("1000")) {
		t.Errorf("final accumulated should equal depreciable (gross - salvage), got %s", rows[len(rows)-1].AccumulatedAfter)
	}
}

func TestBuildSchedule_WDV_ExplicitRate(t *testing.T) {
	t.Parallel()
	// 50% annual = 4.1667%/mo. First row on full 1000 - 0 base = ~41.67.
	rows, err := BuildSchedule(ScheduleParams{
		Gross: dec("1000"), Salvage: dec("100"), UsefulLifeMonths: 12,
		Method: MethodWrittenDownValue, PurchaseDate: d("2026-01-01"), ProRataBasis: false,
		DepreciationRatePct: dec("50"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 12 {
		t.Fatalf("rows: want 12, got %d", len(rows))
	}
	// First row's amount should be roughly base × monthly rate.
	// base = gross - salvage = 900; monthly rate = 50/12/100 = 0.04167; expect ≈ 37.5
	first := rows[0].DepreciationAmount
	if first.LessThan(dec("30")) || first.GreaterThan(dec("45")) {
		t.Errorf("WDV first row outside expected band, got %s", first)
	}
	// Final accumulated must equal gross - salvage exactly (last-row clamp).
	want := dec("900")
	if !rows[len(rows)-1].AccumulatedAfter.Equal(want) {
		t.Errorf("WDV final accumulated: want %s, got %s", want, rows[len(rows)-1].AccumulatedAfter)
	}
}

func TestBuildSchedule_WDV_DecliningAmounts(t *testing.T) {
	t.Parallel()
	// Successive rows should be monotonically non-increasing (except the
	// last-row clamp, which we exclude).
	rows, _ := BuildSchedule(ScheduleParams{
		Gross: dec("10000"), Salvage: dec("1000"), UsefulLifeMonths: 24,
		Method: MethodWrittenDownValue, PurchaseDate: d("2026-01-01"), ProRataBasis: false,
		DepreciationRatePct: dec("40"),
	})
	for i := 1; i < len(rows)-1; i++ {
		if rows[i].DepreciationAmount.GreaterThan(rows[i-1].DepreciationAmount) {
			t.Errorf("WDV row %d (%s) should be <= row %d (%s)",
				i, rows[i].DepreciationAmount, i-1, rows[i-1].DepreciationAmount)
		}
	}
}

func TestBuildSchedule_WDV_ZeroSalvageFallsBackToDoubleDeclining(t *testing.T) {
	t.Parallel()
	// salvage=0 makes the derived rate undefined → service falls back to 2/N
	// (double-declining sentinel). Must still build a valid schedule and
	// total to gross.
	rows, err := BuildSchedule(ScheduleParams{
		Gross: dec("12000"), Salvage: decimal.Zero, UsefulLifeMonths: 60,
		Method: MethodWrittenDownValue, PurchaseDate: d("2026-01-01"), ProRataBasis: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rows[len(rows)-1].AccumulatedAfter.Equal(dec("12000")) {
		t.Errorf("WDV zero-salvage final accumulated: want 12000, got %s", rows[len(rows)-1].AccumulatedAfter)
	}
}

func TestBuildSchedule_Manual_SeedsZeros(t *testing.T) {
	t.Parallel()
	rows, err := BuildSchedule(ScheduleParams{
		Gross: dec("1000"), Salvage: decimal.Zero, UsefulLifeMonths: 5,
		Method: MethodManual, PurchaseDate: d("2026-01-15"), ProRataBasis: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("manual: want 5 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if !r.DepreciationAmount.IsZero() {
			t.Errorf("manual row %d: amount must start at zero, got %s", i, r.DepreciationAmount)
		}
	}
}

func TestBuildSchedule_RejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		p    ScheduleParams
	}{
		{"zero months", ScheduleParams{Gross: dec("1000"), UsefulLifeMonths: 0, Method: MethodStraightLine, PurchaseDate: d("2026-01-01")}},
		{"zero gross", ScheduleParams{Gross: decimal.Zero, UsefulLifeMonths: 12, Method: MethodStraightLine, PurchaseDate: d("2026-01-01")}},
		{"negative salvage", ScheduleParams{Gross: dec("1000"), Salvage: dec("-1"), UsefulLifeMonths: 12, Method: MethodStraightLine, PurchaseDate: d("2026-01-01")}},
		{"salvage >= gross", ScheduleParams{Gross: dec("1000"), Salvage: dec("1000"), UsefulLifeMonths: 12, Method: MethodStraightLine, PurchaseDate: d("2026-01-01")}},
		{"unknown method", ScheduleParams{Gross: dec("1000"), UsefulLifeMonths: 12, Method: "voodoo", PurchaseDate: d("2026-01-01")}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if _, err := BuildSchedule(c.p); err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestProRataFactor_Edges(t *testing.T) {
	t.Parallel()
	// 31-day month, day 1 → 1.0
	if f := proRataFactor(d("2026-01-01")); !f.Equal(dec("1")) {
		t.Errorf("day 1: want 1.0, got %s", f)
	}
	// 31-day month, day 16 → 16/31
	got := proRataFactor(d("2026-01-16"))
	if got.GreaterThanOrEqual(dec("1")) || got.LessThanOrEqual(decimal.Zero) {
		t.Errorf("day 16 should be in (0,1), got %s", got)
	}
	// 28-day Feb (2026 is not a leap), day 28 → 1/28
	got = proRataFactor(d("2026-02-28"))
	want := decimal.NewFromInt(1).Div(decimal.NewFromInt(28))
	if !got.Equal(want) {
		t.Errorf("Feb-28: want %s, got %s", want, got)
	}
}
