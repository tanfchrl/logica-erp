# Phase 1B — Accounting backbone (procure-to-pay half + financial statements)

> Scope of this slice: **Purchase Invoice**, **Payment Entry `pay` mode**, **General Ledger** report, **Profit & Loss** report, **Balance Sheet** report, **AR / AP Ageing** reports, supplier + purchase-side seed.
> Deferred to Phase 1C / hardening: Debit Note + Credit Note (return variants), Bank Transaction + Reconciliation, Cash Flow report, full Indonesian COA template, tax-summary reports (PPN/PPh), frontend wiring for the full O2C + P2P UI.
> Phase 1B exit: a supplier + expense item exist; a Purchase Invoice with PPN Masukan submits and posts Dr Expense / Dr Tax Recoverable / Cr Payable; a Payment Entry `pay` with PPh withholding settles it and posts Dr AP / Cr Cash / Cr Withholding Payable; the P&L shows net loss = expense − income; the Balance Sheet validates Assets = Liabilities + Equity + Period Net Profit; AR and AP ageing reports surface outstanding invoices in correct buckets.

---

## 1. Purchase Invoice

Mirror of Sales Invoice. Same shape, opposite GL polarity.

### Schema (migration 0006)

```sql
CREATE TABLE purchase_invoice (
  id                          text PRIMARY KEY,
  name                        text NOT NULL,
  company_id                  text NOT NULL REFERENCES company(id),
  supplier_id                 text NOT NULL REFERENCES supplier(id),
  posting_date                date NOT NULL,
  due_date                    date NOT NULL,
  fiscal_year_id              text NOT NULL REFERENCES fiscal_year(id),
  currency                    text NOT NULL REFERENCES currency(code),
  exchange_rate               numeric(18,8) NOT NULL DEFAULT 1,
  tax_template_id             text REFERENCES tax_template(id),       -- is_sales = false
  supplier_invoice_no         text,                                    -- supplier's own invoice number (for Faktur Pajak matching)
  supplier_invoice_date       date,
  bill_no                     text,                                    -- internal bill ref if different
  -- totals
  net_total                   numeric(18,4) NOT NULL DEFAULT 0,
  total_taxes_and_charges     numeric(18,4) NOT NULL DEFAULT 0,
  grand_total                 numeric(18,4) NOT NULL DEFAULT 0,
  paid_amount                 numeric(18,4) NOT NULL DEFAULT 0,
  outstanding_amount          numeric(18,4) NOT NULL DEFAULT 0,
  base_net_total              numeric(18,4) NOT NULL DEFAULT 0,
  base_total_taxes_and_charges numeric(18,4) NOT NULL DEFAULT 0,
  base_grand_total            numeric(18,4) NOT NULL DEFAULT 0,
  base_paid_amount            numeric(18,4) NOT NULL DEFAULT 0,
  base_outstanding_amount     numeric(18,4) NOT NULL DEFAULT 0,
  remarks                     text,
  payable_account_id          text NOT NULL REFERENCES account(id),
  is_return                   boolean NOT NULL DEFAULT false,
  return_against              text REFERENCES purchase_invoice(id),
  docstatus                   smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at, submitted_by, cancelled_at, cancelled_by, amended_from,
  custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
  audit cols,
  UNIQUE (company_id, name)
);

CREATE TABLE purchase_invoice_item (
  -- mirror of sales_invoice_item, with expense_account_id instead of income_account_id
  ...
  expense_account_id text NOT NULL REFERENCES account(id),
  ...
);

CREATE TABLE purchase_invoice_tax (
  -- identical shape to sales_invoice_tax
);

CREATE TABLE purchase_invoice_withholding (
  -- identical shape to sales_invoice_withholding; booked at PE.pay time
);
```

### Submit-time GL postings

For a purchase invoice with net `N`, tax `T`, grand `G = N + T`:

- **Cr** Payable `G` (base) — party=supplier
- For each item line: **Dr** `expense_account` (line.amount, base)
- For each tax row: **Dr** `tax.account_id` (tax.amount, base) — note: purchase tax accounts are typically `account_type = 'tax'` ASSET accounts (Pajak Masukan), so debiting them increases the recoverable balance.

Sum invariant: `Dr (expenses + tax recoverable) = Cr Payable = G`.

### Cancel

Standard `ledger.CancelGL` posts inverse rows. Rejects when `paid_amount > 0` (cancel the PE first).

### Defaults resolution (mirroring SI)

1. `currency`: explicit → `supplier_default.default_currency` → `supplier.default_currency` → `company.default_currency`.
2. `payable_account_id`: explicit → `supplier_default.default_payable_account_id` → `company.default_payable_account_id`.
3. `tax_template_id`: explicit → `supplier_default.default_tax_template_id`. (Per-item override possible via `item_default.default_tax_template_id` but Phase 1B treats template as invoice-level only — same as SI.)
4. Per-line `expense_account_id`: input → `item_default.default_expense_account_id` → `company.default_expense_account_id`.

---

## 2. Payment Entry — `pay` mode

The existing `payment_entry` table is type-agnostic; only the service code needs to learn the second polarity.

### GL postings (pay)

For each reference: settle a Purchase Invoice. Aggregate:

- **Dr** `paid_to_account` (AP) `base_paid_amount` (= total allocated, base) — party=supplier
- **Cr** `paid_from_account` (Cash / Bank) `base_received_amount` (= paid − deductions, base)
- For each deduction: **Cr** `deduction.account_id` `deduction.amount` (e.g. Utang Pajak - PPh 23)

Net: total Dr (AP) = total Cr (Cash + Withholding Payable) = base_paid_amount.

### Reference validation (pay)

- `reference_doctype` must be `purchase_invoice`.
- Referenced PI must be `docstatus = 1`.
- `supplier_id` of the PI must equal `party_id` of the PE.
- `allocated_amount` must be > 0 and ≤ `outstanding_amount`.

### Maintenance

On submit: `purchase_invoice.paid_amount += allocated`, `outstanding_amount -= allocated` per reference.
On cancel: reverse + `ledger.CancelGL`.

### Polarity decided by `payment_type`, not by account types

The service trusts `payment_type` for direction (`receive` = customer collection, `pay` = supplier payment). The accounts must be set consistently by the caller (`paid_from_account = AR` for receive, `paid_from_account = Cash` for pay). The engine doesn't sniff account types.

---

## 3. General Ledger report

```
GET /accounting/reports/general-ledger
    ?company_id=<id>          (or X-Company-Id)
    &account_id=<id>          (optional; otherwise all accounts)
    &party_type=customer      (optional)
    &party_id=<id>            (optional)
    &voucher_type=<str>       (optional)
    &from_date=YYYY-MM-DD     (optional, defaults to FY start)
    &to_date=YYYY-MM-DD       (optional, defaults to today)
```

Response:
```json
{
  "company_id": "...",
  "from_date": "...",
  "to_date": "...",
  "rows": [
    { "posting_date": "2026-05-26", "account_id": "...", "account_name": "Piutang Usaha",
      "voucher_type": "Sales Invoice", "voucher_name": "SI-2026-0001",
      "party_type": "customer", "party_id": "...", "party_name": "PT ...",
      "debit": "5550000", "credit": "0",
      "balance": "5550000", "remarks": "..." }, ...
  ],
  "opening_balance": "0",
  "closing_balance": "0",
  "total_debit": "...", "total_credit": "..."
}
```

- When filtered by single `account_id`, includes per-row running balance and shows the opening/closing balance scalars.
- When multi-account, opening/closing are zero-filled and rows are returned in (posting_date, created_at) order.

Implementation: simple `SELECT * FROM gl_entry JOIN account ... WHERE ...`. Party name join is conditional (left join on customer or supplier depending on `party_type`).

---

## 4. Profit & Loss report

```
GET /accounting/reports/profit-and-loss
    ?company_id=<id>&from_date=...&to_date=...
```

Returns income + expense activity for the period:
```json
{
  "company_id": "...", "from_date": "...", "to_date": "...",
  "income":  [ { "account_id": "...", "account_name": "Penjualan", "amount": "5000000" }, ... ],
  "expense": [ { "account_id": "...", "account_name": "Beban Operasional", "amount": "1200000" }, ... ],
  "total_income":  "5000000",
  "total_expense": "1200000",
  "net_profit":    "3800000"
}
```

Implementation:
- For income accounts (`root_type = 'income'`): `amount = sum(credit) - sum(debit)` over the period. Positive value means net income.
- For expense accounts (`root_type = 'expense'`): `amount = sum(debit) - sum(credit)`. Positive means net expense.
- `net_profit = total_income - total_expense`.

---

## 5. Balance Sheet report

```
GET /accounting/reports/balance-sheet
    ?company_id=<id>&as_of=YYYY-MM-DD     (defaults to today)
```

Returns:
```json
{
  "company_id": "...", "as_of": "...",
  "assets":      [ { "account_id": "...", "name": "Kas", "amount": "5550000" }, ... ],
  "liabilities": [ ... ],
  "equity":      [ ... ],
  "period_net_profit": "...",       // sum since last period_closing (Phase 1B: since FY start)
  "total_assets":      "...",
  "total_liabilities": "...",
  "total_equity":      "...",       // includes period_net_profit
  "balanced":          true
}
```

Implementation:
- Asset accounts: balance = `sum(debit) - sum(credit)` cumulative through `as_of`.
- Liability and Equity accounts: balance = `sum(credit) - sum(debit)` cumulative.
- Period Net Profit (Phase 1B simplification): `sum(income credit-debit) - sum(expense debit-credit)` over the current fiscal year ending at `as_of`. Added under Equity.
- `total_assets == total_liabilities + total_equity` enforced; report includes `balanced` flag.

(Periodic Closing Voucher in Phase 6 will let the user explicitly close income/expense to a retained-earnings account; until then the calculated "Period Net Profit" line carries it.)

---

## 6. AR / AP Ageing

```
GET /accounting/reports/accounts-receivable-ageing?company_id=...&as_of=YYYY-MM-DD
GET /accounting/reports/accounts-payable-ageing?company_id=...&as_of=YYYY-MM-DD
```

Buckets (days overdue against `due_date`):
- **current** (not yet overdue at `as_of`)
- **0–30** (1–30 days overdue)
- **31–60**
- **61–90**
- **90+**

Per row:
```json
{
  "party_id": "...", "party_name": "PT ...",
  "current": "0", "d_0_30": "550000", "d_31_60": "0", "d_61_90": "0", "d_90_plus": "0",
  "total_outstanding": "550000"
}
```

Underlying query: `sales_invoice` (for AR) or `purchase_invoice` (for AP), `WHERE docstatus = 1 AND outstanding_amount > 0 AND company_id = $1 AND posting_date <= $2`, grouped by party, conditional sum per bucket using `(as_of - due_date)` days.

---

## 7. Seed additions (Phase 1B)

- Sample supplier **`SUPP-001 — PT Pemasok Contoh`**, `supplier_default` linking the demo company's `Utang Usaha` payable account and `PPN Masukan 11%` purchase template.
- Sample expense item **`ITM-OFFICE — Perlengkapan Kantor`** (non-stock) with `item_default.default_expense_account = Beban Operasional` and `default_tax_template_id = PPN Masukan 11%`.
- Per-company naming series **`PI-.YYYY.-.####`**.
- Phase 1B doctypes added to `phase0Doctypes` permission grant: `purchase_invoice`.

---

## 8. What lands when this slice is accepted

The user can: configure a purchase tax template and a supplier; record a vendor invoice with PPN Masukan, submit it (Expense + Tax Recoverable debited, Payable credited); pay the supplier with PPh 23 withholding deducted (AP debited, Cash + Withholding Payable credited); the General Ledger drills into any account or party's activity; the P&L reflects expense and income with a net result; the Balance Sheet equation holds (Assets = Liabilities + Equity); AR and AP ageing reports show every outstanding invoice in the right bucket against `as_of`. The post-condition: cancelling everything in reverse zeroes the books.

---

## 9. Deferred (Phase 1C / hardening)

- **Debit Note + Credit Note**: `is_return=true` variants of PI/SI; references original via `return_against`. Submit posts inverse entries. About 30% of SI/PI code reused.
- **Bank Transaction + Reconciliation**: staging table + matching workflow against GL bank-account rows.
- **Cash Flow report** (direct method): requires categorizing bank/cash activity into operating/investing/financing — needs an `account_type` taxonomy pass.
- **Tax summary reports**: PPN summary (output VAT collected − input VAT recoverable, net payable), PPh summary (withheld per supplier per type).
- **Full Indonesian COA template**: expand the 20-account starter into a ~80-account SAK ETAP-style template, with sub-types matched to account_type taxonomy used by tax reports.
- **Frontend wiring**: invoice list + form, payment form, dashboard cards for AR/AP outstanding, simple TB/P&L/BS viewers.
