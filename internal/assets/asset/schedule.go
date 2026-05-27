package asset

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/platform/money"
)

// Depreciation method constants. Stored on asset.depreciation_method.
const (
	MethodStraightLine     = "straight_line"
	MethodWrittenDownValue = "written_down_value"
	MethodManual           = "manual"
)

// ScheduleParams is the pure input to schedule generation. All math lives
// inside BuildSchedule so tests don't need a DB.
type ScheduleParams struct {
	Gross               decimal.Decimal // gross_purchase_amount
	Salvage             decimal.Decimal // expected_value_after_useful_life
	UsefulLifeMonths    int
	Method              string          // straight_line | written_down_value | manual
	PurchaseDate        time.Time
	ProRataBasis        bool            // true = first row is partial-month based on days
	DepreciationRatePct decimal.Decimal // optional annual % for WDV; zero = derive from inputs
}

// ScheduleRow is a single (in-memory) row that the service later writes into
// depreciation_schedule. RowIndex is 1-based to match the existing DB layout.
type ScheduleRow struct {
	RowIndex           int
	ScheduleDate       time.Time
	DepreciationAmount decimal.Decimal
	AccumulatedAfter   decimal.Decimal
}

// BuildSchedule turns ScheduleParams into the row list that gets persisted
// on Submit. Returns an error for invalid inputs so the service can surface
// the same message it would have hit at the SQL layer.
func BuildSchedule(p ScheduleParams) ([]ScheduleRow, error) {
	if p.UsefulLifeMonths <= 0 {
		return nil, errors.New("useful_life_months: must be > 0")
	}
	if p.Gross.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("gross_purchase_amount: must be > 0")
	}
	if p.Salvage.IsNegative() || p.Salvage.GreaterThanOrEqual(p.Gross) {
		return nil, errors.New("expected_value_after_useful_life: must be in [0, gross)")
	}

	switch p.Method {
	case MethodStraightLine, "":
		return buildStraightLine(p), nil
	case MethodWrittenDownValue:
		return buildWDV(p)
	case MethodManual:
		// For manual, we still seed N rows so the persisted shape is
		// consistent — amounts are zero and the user (or a future
		// dedicated endpoint) fills them in before posting.
		return buildManual(p), nil
	}
	return nil, fmt.Errorf("depreciation_method: %q not supported", p.Method)
}

// ---- straight-line ----

func buildStraightLine(p ScheduleParams) []ScheduleRow {
	depreciable := p.Gross.Sub(p.Salvage)
	monthlyAmount := depreciable.Div(decimal.NewFromInt(int64(p.UsefulLifeMonths))).Round(money.Precision)

	rows := make([]ScheduleRow, 0, p.UsefulLifeMonths)
	acc := decimal.Zero

	// First row: optionally pro-rate the partial month.
	firstAmount := monthlyAmount
	firstDate := endOfMonth(p.PurchaseDate)
	if p.ProRataBasis {
		factor := proRataFactor(p.PurchaseDate)
		firstAmount = monthlyAmount.Mul(factor).Round(money.Precision)
	}
	acc = acc.Add(firstAmount)
	rows = append(rows, ScheduleRow{
		RowIndex: 1, ScheduleDate: firstDate,
		DepreciationAmount: firstAmount, AccumulatedAfter: acc,
	})

	// Middle rows: full monthlies on the end-of-month anchored date.
	current := firstDate
	for i := 1; i < p.UsefulLifeMonths-1; i++ {
		current = endOfMonth(current.AddDate(0, 1, 0))
		acc = acc.Add(monthlyAmount)
		rows = append(rows, ScheduleRow{
			RowIndex: i + 1, ScheduleDate: current,
			DepreciationAmount: monthlyAmount, AccumulatedAfter: acc,
		})
	}

	// Last row: snap to depreciable - already-accumulated to avoid drift.
	if p.UsefulLifeMonths > 1 {
		current = endOfMonth(current.AddDate(0, 1, 0))
		last := depreciable.Sub(acc)
		// Defensive: if pro-rata made earlier rows total more than depreciable
		// somehow, this clamps to zero rather than producing a negative entry.
		if last.IsNegative() {
			last = decimal.Zero
		}
		acc = acc.Add(last)
		rows = append(rows, ScheduleRow{
			RowIndex: p.UsefulLifeMonths, ScheduleDate: current,
			DepreciationAmount: last, AccumulatedAfter: acc,
		})
	}
	return rows
}

// ---- written-down-value ----

// buildWDV computes a declining-balance schedule. Each period's amount is
// `book_value × monthly_rate`; the last row clamps to drive book_value to
// the salvage value so the asset ends at the expected residual.
//
// Rate source (in order of preference):
//   - explicit DepreciationRatePct (annual)
//   - derived: annual_rate = 1 - (salvage/gross)^(1/years)
//
// monthly_rate = annual_rate / 12. (Compounding form (1+r)^(1/12)-1 would
// be marginally more accurate but the simple monthly form matches what
// Indonesian PMK-96 implementations use.)
func buildWDV(p ScheduleParams) ([]ScheduleRow, error) {
	var annualRate decimal.Decimal
	if p.DepreciationRatePct.IsPositive() {
		annualRate = p.DepreciationRatePct.Div(decimal.NewFromInt(100))
	} else {
		// Derive from gross / salvage / useful life. Both salvage and gross
		// are guarded > 0 by BuildSchedule's preflight, so the ratio is in
		// (0, 1) and the log is well-defined.
		years := float64(p.UsefulLifeMonths) / 12.0
		if years <= 0 {
			return nil, errors.New("useful_life_months: must be > 0")
		}
		s, _ := p.Salvage.Float64()
		g, _ := p.Gross.Float64()
		if s <= 0 {
			// Pure-WDV with zero salvage is mathematically un-bounded
			// (book_value approaches zero asymptotically). Fall back to a
			// double-declining sentinel: 2/N annual rate.
			annualRate = decimal.NewFromFloat(2.0 / years)
		} else {
			r := 1 - math.Pow(s/g, 1.0/years)
			annualRate = decimal.NewFromFloat(r)
		}
	}
	monthlyRate := annualRate.Div(decimal.NewFromInt(12))

	rows := make([]ScheduleRow, 0, p.UsefulLifeMonths)
	book := p.Gross
	acc := decimal.Zero

	for i := 0; i < p.UsefulLifeMonths; i++ {
		var schedDate time.Time
		if i == 0 {
			schedDate = endOfMonth(p.PurchaseDate)
		} else {
			schedDate = endOfMonth(rows[i-1].ScheduleDate.AddDate(0, 1, 0))
		}

		amount := book.Sub(p.Salvage).Mul(monthlyRate).Round(money.Precision)
		if amount.IsNegative() {
			amount = decimal.Zero
		}
		if i == 0 && p.ProRataBasis {
			amount = amount.Mul(proRataFactor(p.PurchaseDate)).Round(money.Precision)
		}

		// Last row: floor at depreciable - acc so total accumulated equals
		// gross - salvage exactly. Prevents the asymptotic tail leaving
		// fractional rupiahs on the books forever.
		if i == p.UsefulLifeMonths-1 {
			amount = p.Gross.Sub(p.Salvage).Sub(acc)
			if amount.IsNegative() {
				amount = decimal.Zero
			}
		}

		// Belt-and-braces: never let book drop below salvage.
		maxAllowed := book.Sub(p.Salvage)
		if amount.GreaterThan(maxAllowed) {
			amount = maxAllowed
		}

		book = book.Sub(amount)
		acc = acc.Add(amount)
		rows = append(rows, ScheduleRow{
			RowIndex: i + 1, ScheduleDate: schedDate,
			DepreciationAmount: amount, AccumulatedAfter: acc,
		})
	}
	return rows, nil
}

// ---- manual ----

// buildManual seeds N empty rows so the user can fill them in later via a
// dedicated edit endpoint. Each row is dated end-of-month from the purchase
// date forward. Amounts are zero — the caller has to populate them before
// PostDepreciation will have anything to post.
func buildManual(p ScheduleParams) []ScheduleRow {
	rows := make([]ScheduleRow, 0, p.UsefulLifeMonths)
	for i := 0; i < p.UsefulLifeMonths; i++ {
		schedDate := endOfMonth(p.PurchaseDate.AddDate(0, i, 0))
		rows = append(rows, ScheduleRow{
			RowIndex: i + 1, ScheduleDate: schedDate,
			DepreciationAmount: decimal.Zero, AccumulatedAfter: decimal.Zero,
		})
	}
	return rows
}

// ---- date helpers ----

// endOfMonth returns the last day of the given date's month.
func endOfMonth(t time.Time) time.Time {
	// Month-after first-of-month gives next month start; back off by 1 day.
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	next := first.AddDate(0, 1, 0)
	return next.AddDate(0, 0, -1)
}

// proRataFactor returns the fraction of the purchase month that should be
// depreciated. Examples in a 31-day month: purchase on the 1st → 1.0,
// purchase on the 16th → 16/31. Capped at 1 so weird inputs can't inflate
// the first row.
func proRataFactor(purchase time.Time) decimal.Decimal {
	monthEnd := endOfMonth(purchase)
	daysInMonth := monthEnd.Day()
	daysOwned := daysInMonth - purchase.Day() + 1
	if daysOwned <= 0 {
		return decimal.Zero
	}
	if daysOwned >= daysInMonth {
		return decimal.NewFromInt(1)
	}
	return decimal.NewFromInt(int64(daysOwned)).Div(decimal.NewFromInt(int64(daysInMonth)))
}
