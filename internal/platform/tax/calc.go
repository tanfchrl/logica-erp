// Package tax holds the rule-based tax engine. It is pure: no DB access, no I/O.
// Callers fetch a Template from their repo and pass it in alongside the lines.
package tax

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

type ChargeType string

const (
	ChargeOnNetTotal        ChargeType = "on_net_total"
	ChargeOnPreviousAmount  ChargeType = "on_previous_amount"
	ChargeActual            ChargeType = "actual"
)

// Line is one input invoice line.
type Line struct {
	Key       string          // opaque caller key (e.g. row index as string) used to look up the result
	NetAmount decimal.Decimal // qty * unit_price, already net of item-level discount, in transaction currency
}

// Template is a tax template snapshot.
type Template struct {
	ID      string
	IsSales bool
	Lines   []TemplateLine
}

// TemplateLine is one row of a tax_template (a single tax/charge).
type TemplateLine struct {
	ID                  string
	AccountID           string
	Description         string
	Rate                decimal.Decimal // percent, e.g. 11 means 11%
	ChargeType          ChargeType
	IncludedInBasicRate bool
	CostCenterID        string
	// Actual is used only when ChargeType = ChargeActual; ignored otherwise.
	Actual decimal.Decimal
}

// Result is the calculated breakdown.
type Result struct {
	Lines      []LineResult // one per input Line, in input order
	TaxRows    []TaxRowResult // one per template line, in input order
	NetTotal   decimal.Decimal
	TaxTotal   decimal.Decimal
	GrandTotal decimal.Decimal
}

type LineResult struct {
	Key        string
	NetAmount  decimal.Decimal
	TaxAmount  decimal.Decimal // sum of allocated tax across all rows
	Total      decimal.Decimal // NetAmount + TaxAmount (transaction currency)
}

type TaxRowResult struct {
	TemplateLineID      string
	AccountID           string
	Description         string
	Rate                decimal.Decimal
	ChargeType          ChargeType
	IncludedInBasicRate bool
	CostCenterID        string
	TaxAmount           decimal.Decimal // total tax for this template line
	PerLine             map[string]decimal.Decimal // allocation back to each input line by Key
}

// Calculate runs the tax engine. It returns a Result snapshot ready to persist on the invoice.
// Inputs MUST already be in transaction currency. Base-currency conversion is the caller's job.
func Calculate(lines []Line, tpl Template) (Result, error) {
	if len(lines) == 0 {
		return Result{}, errors.New("tax: at least one line required")
	}

	netTotal := decimal.Zero
	for _, l := range lines {
		if l.NetAmount.IsNegative() {
			return Result{}, fmt.Errorf("tax: line %q has negative net amount", l.Key)
		}
		netTotal = netTotal.Add(l.NetAmount)
	}

	result := Result{
		Lines:    make([]LineResult, len(lines)),
		TaxRows:  make([]TaxRowResult, 0, len(tpl.Lines)),
		NetTotal: netTotal.Round(precision),
	}
	for i, l := range lines {
		result.Lines[i] = LineResult{Key: l.Key, NetAmount: l.NetAmount.Round(precision)}
	}

	if len(tpl.Lines) == 0 {
		result.GrandTotal = result.NetTotal
		for i := range result.Lines {
			result.Lines[i].Total = result.Lines[i].NetAmount
		}
		return result, nil
	}

	// Previous-row amount cache for cascading taxes.
	prevTaxTotal := decimal.Zero
	cumulativeTax := decimal.Zero

	for _, tl := range tpl.Lines {
		var rowTotal decimal.Decimal
		switch tl.ChargeType {
		case ChargeOnNetTotal:
			rowTotal = netTotal.Mul(tl.Rate).Div(hundred)
		case ChargeOnPreviousAmount:
			if prevTaxTotal.IsZero() {
				rowTotal = decimal.Zero
			} else {
				rowTotal = prevTaxTotal.Mul(tl.Rate).Div(hundred)
			}
		case ChargeActual:
			rowTotal = tl.Actual
		default:
			return Result{}, fmt.Errorf("tax: unknown charge_type %q", tl.ChargeType)
		}
		rowTotal = rowTotal.Round(precision)

		row := TaxRowResult{
			TemplateLineID:      tl.ID,
			AccountID:           tl.AccountID,
			Description:         tl.Description,
			Rate:                tl.Rate,
			ChargeType:          tl.ChargeType,
			IncludedInBasicRate: tl.IncludedInBasicRate,
			CostCenterID:        tl.CostCenterID,
			TaxAmount:           rowTotal,
			PerLine:             make(map[string]decimal.Decimal, len(lines)),
		}

		// Distribute the row's tax across input lines proportionally to net amount.
		// Residual (from rounding) goes to the largest line (or the last one on a tie).
		if rowTotal.IsZero() || netTotal.IsZero() {
			for _, l := range lines {
				row.PerLine[l.Key] = decimal.Zero
			}
		} else {
			assigned := decimal.Zero
			maxIdx := 0
			for i, l := range lines {
				if lines[maxIdx].NetAmount.LessThan(l.NetAmount) {
					maxIdx = i
				}
				share := rowTotal.Mul(l.NetAmount).Div(netTotal).Round(precision)
				row.PerLine[l.Key] = share
				assigned = assigned.Add(share)
			}
			if !assigned.Equal(rowTotal) {
				diff := rowTotal.Sub(assigned)
				row.PerLine[lines[maxIdx].Key] = row.PerLine[lines[maxIdx].Key].Add(diff)
			}
		}

		// Track grand-total impact.
		if !tl.IncludedInBasicRate {
			cumulativeTax = cumulativeTax.Add(rowTotal)
		}
		prevTaxTotal = rowTotal

		// Accumulate per-line tax_amount.
		for k, v := range row.PerLine {
			for i := range result.Lines {
				if result.Lines[i].Key == k {
					result.Lines[i].TaxAmount = result.Lines[i].TaxAmount.Add(v)
					break
				}
			}
		}

		result.TaxRows = append(result.TaxRows, row)
	}

	result.TaxTotal = cumulativeTax
	result.GrandTotal = netTotal.Add(cumulativeTax).Round(precision)
	for i := range result.Lines {
		result.Lines[i].Total = result.Lines[i].NetAmount.Add(result.Lines[i].TaxAmount)
	}
	return result, nil
}

const precision = 4

var hundred = decimal.NewFromInt(100)
