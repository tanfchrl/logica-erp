// Package ledger holds the GL and Stock Ledger posting helpers and types.
package ledger

import (
	"time"

	"github.com/shopspring/decimal"
)

// PartyType for GL entries linked to a customer/supplier/employee.
type PartyType string

const (
	PartyCustomer PartyType = "customer"
	PartySupplier PartyType = "supplier"
	PartyEmployee PartyType = "employee"
)

// Entry is a single GL row to post. Exactly one of Debit / Credit should be > 0 (in base currency).
type Entry struct {
	AccountID                string
	PartyType                PartyType // empty if not applicable
	PartyID                  string
	CostCenterID             string
	ProjectID                string
	Debit                    decimal.Decimal
	Credit                   decimal.Decimal
	AccountCurrency          string
	DebitInAccountCurrency   decimal.Decimal
	CreditInAccountCurrency  decimal.Decimal
	Against                  string
	Remarks                  string
}

// Voucher describes the source document being posted.
type Voucher struct {
	Type         string // e.g. "journal_entry", "sales_invoice"
	ID           string
	Name         string // human display id, e.g. "JE-2026-0001"
	CompanyID    string
	PostingDate  time.Time
	FiscalYearID string
	CreatedBy    string
}

// StockEntry is a single stock_ledger_entry row.
type StockEntry struct {
	ItemID              string
	WarehouseID         string
	BatchNo             string
	SerialNo            string
	ActualQty           decimal.Decimal // signed; positive = incoming, negative = outgoing
	IncomingRate        decimal.Decimal // only set for incoming rows
	QtyAfterTransaction decimal.Decimal // computed by valuation strategy
	ValuationRate       decimal.Decimal // per-unit, base currency
	StockValue          decimal.Decimal
	StockValueDiff      decimal.Decimal
	PostingDatetime     time.Time
}
