# Phase 1A — Accounting backbone (order-to-cash half)

> Scope of this slice: tax engine, Customer/Supplier/Item masters, **Sales Invoice**, **Payment Entry**, **Trial Balance** report.
> Deferred to Phase 1B: Purchase Invoice, Debit/Credit Notes, Bank Reconciliation, P&L, Balance Sheet, Cash Flow, AR/AP ageing, tax summaries, full Indonesian COA template, frontend wiring.
> Phase 1A exit: a Customer + Item exist; a Sales Invoice with PPN 11% submits and posts AR/Income/Tax GL; a Payment Entry partial-pays it; the Trial Balance still balances after every step; cancellations restore the books to zero impact.

---

## 1. Tax engine

A generic, rule-based engine that:
- Accepts a tax template and a list of invoice lines (net amount per line).
- Returns per-template-line tax amounts and the grand total.
- Supports **exclusive** (added to base) and **inclusive** (already in net price) modes.
- Supports `charge_type`: `on_net_total` (most common), `on_previous_amount` (cascading), `actual` (manual).
- Per-item overrides via `item_tax` rows.
- **Withholding** is a separate concept layered on top: a `WithholdingDeduction` reduces the cash received but does not change the invoice's grand total.

### Tables (added in migration 0003)

```sql
CREATE TABLE tax_category (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  description text
);

CREATE TABLE tax_template (
  id          text PRIMARY KEY,
  company_id  text NOT NULL REFERENCES company(id),
  name        text NOT NULL,
  is_sales    boolean NOT NULL,                   -- false = purchase template
  is_default  boolean NOT NULL DEFAULT false,
  tax_category_id text REFERENCES tax_category(id),
  is_deleted  boolean NOT NULL DEFAULT false,
  audit cols,
  UNIQUE (company_id, name)
);

CREATE TABLE tax_template_line (
  id                       text PRIMARY KEY,
  template_id              text NOT NULL REFERENCES tax_template(id) ON DELETE CASCADE,
  row_index                int NOT NULL,
  account_id               text NOT NULL REFERENCES account(id),
  description              text NOT NULL,
  rate                     numeric(9,4) NOT NULL,       -- percentage, e.g. 11.0000
  charge_type              text NOT NULL CHECK (charge_type IN ('on_net_total','on_previous_amount','actual')),
  included_in_basic_rate   boolean NOT NULL DEFAULT false,  -- inclusive vs exclusive
  cost_center_id           text REFERENCES cost_center(id)
);

CREATE TABLE item_tax (
  id              text PRIMARY KEY,
  item_id         text NOT NULL REFERENCES item(id) ON DELETE CASCADE,
  tax_category_id text REFERENCES tax_category(id),
  tax_template_id text REFERENCES tax_template(id),
  rate            numeric(9,4),                          -- if NULL, inherits template rate
  UNIQUE (item_id, tax_category_id)
);

CREATE TABLE withholding_tax_type (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,                     -- e.g. 'PPh 23 - Jasa Konsultasi'
  rate        numeric(9,4) NOT NULL,
  account_id  text NOT NULL REFERENCES account(id),     -- payable account
  threshold   numeric(18,4),                            -- optional minimum applicability
  category    text,                                     -- 'individual' | 'entity' | NULL
  is_deleted  boolean NOT NULL DEFAULT false,
  audit cols
);
```

### Go API (`internal/platform/tax`)

```go
type Line struct {
    ItemID    string
    NetAmount decimal.Decimal      // qty * unit_price after item-level discount
}

type Template struct {
    ID        string
    IsSales   bool
    Lines     []TemplateLine
}

type TemplateLine struct {
    ID                   string
    AccountID            string
    Description          string
    Rate                 decimal.Decimal
    ChargeType           ChargeType
    IncludedInBasicRate  bool
    CostCenterID         string
}

// Calculate returns the per-line and per-tax-line amounts and the grand total.
func Calculate(lines []Line, tpl Template) (Result, error)

type Result struct {
    Lines            []LineResult            // one per input Line: net, tax breakdown, total
    TaxRows          []TaxRowResult          // one per template line: account, rate, total tax
    NetTotal         decimal.Decimal
    GrandTotal       decimal.Decimal
    InTransactionCcy bool                    // always true; caller converts to base
}
```

**Algorithm.**
1. NetTotal = sum(line.NetAmount).
2. For each template line (in row_index order):
   - If `ChargeType == on_net_total`: tax = NetTotal * Rate / 100, distributed proportionally per input line.
   - If `ChargeType == on_previous_amount`: tax = previous-tax-line-total * Rate / 100.
   - If `ChargeType == actual`: tax pulled from a per-tax row in the invoice (not Phase 1A).
   - If `IncludedInBasicRate == true`: the net is already inclusive — back-compute the net portion, and the "tax" line records only the imputed tax (no addition to grand total).
3. GrandTotal = NetTotal + sum(exclusive tax rows). Inclusive rows do not change the grand total.
4. Distribute each tax row's total back to the input lines proportionally to net amount, so per-line `tax_amount` columns are accurate.

**Edge cases enforced by tests.**
- Single line, 11% PPN exclusive → grand = net * 1.11.
- Single line, 11% PPN inclusive → grand = net; per-line tax = net - net/1.11.
- Multiple lines, proportional distribution rounds to 4 dp; residual goes to the largest line.

---

## 2. Masters

### Customer

```sql
CREATE TABLE customer_group (
  id        text PRIMARY KEY,
  name      text NOT NULL UNIQUE,
  parent_id text REFERENCES customer_group(id),
  lft int, rgt int,
  is_group  boolean NOT NULL DEFAULT false,
  is_deleted boolean NOT NULL DEFAULT false
);

CREATE TABLE customer (
  id              text PRIMARY KEY,
  name            text NOT NULL UNIQUE,           -- internal code
  display_name    text NOT NULL,
  customer_group_id text REFERENCES customer_group(id),
  territory_id    text,                            -- (territory table comes in Phase 2 selling)
  default_currency text REFERENCES currency(code),
  npwp            text,
  is_individual   boolean NOT NULL DEFAULT false,  -- affects withholding category
  email           text,
  phone           text,
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  audit cols,
  CHECK (npwp IS NULL OR npwp ~ '^[0-9]{16}$')
);

CREATE TABLE customer_default (
  customer_id    text NOT NULL REFERENCES customer(id) ON DELETE CASCADE,
  company_id     text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  default_receivable_account_id text REFERENCES account(id),
  default_currency text REFERENCES currency(code),
  default_tax_template_id text REFERENCES tax_template(id),
  PRIMARY KEY (customer_id, company_id)
);
```

### Supplier — identical shape to Customer, with `default_payable_account_id`.

### Item

Phase 1A only needs items for invoicing — the stock side (warehouses, valuation, stock_entry) comes in Phase 2.

```sql
CREATE TABLE item_group (
  id         text PRIMARY KEY,
  name       text NOT NULL UNIQUE,
  parent_id  text REFERENCES item_group(id),
  lft int, rgt int,
  is_group   boolean NOT NULL DEFAULT false,
  is_deleted boolean NOT NULL DEFAULT false
);

CREATE TABLE item (
  id               text PRIMARY KEY,
  code             text NOT NULL UNIQUE,           -- SKU/code
  name             text NOT NULL,
  description      text,
  item_group_id    text REFERENCES item_group(id),
  stock_uom        text NOT NULL DEFAULT 'Unit',
  is_stock_item    boolean NOT NULL DEFAULT false, -- Phase 1A items are services (false)
  is_sales_item    boolean NOT NULL DEFAULT true,
  is_purchase_item boolean NOT NULL DEFAULT true,
  standard_rate    numeric(18,4) NOT NULL DEFAULT 0,  -- transaction currency = company default
  is_deleted       boolean NOT NULL DEFAULT false,
  custom_fields    jsonb NOT NULL DEFAULT '{}'::jsonb,
  audit cols
);

CREATE TABLE item_default (
  item_id                 text NOT NULL REFERENCES item(id) ON DELETE CASCADE,
  company_id              text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  default_income_account_id  text REFERENCES account(id),
  default_expense_account_id text REFERENCES account(id),
  default_tax_template_id    text REFERENCES tax_template(id),
  PRIMARY KEY (item_id, company_id)
);
```

The full item master (variants, batches, serials, valuation_method, default_warehouse) lands in Phase 2.

---

## 3. Sales Invoice

```sql
CREATE TABLE sales_invoice (
  id                          text PRIMARY KEY,
  name                        text NOT NULL,
  company_id                  text NOT NULL REFERENCES company(id),
  customer_id                 text NOT NULL REFERENCES customer(id),
  posting_date                date NOT NULL,
  due_date                    date NOT NULL,
  fiscal_year_id              text NOT NULL REFERENCES fiscal_year(id),
  currency                    text NOT NULL REFERENCES currency(code),
  exchange_rate               numeric(18,8) NOT NULL DEFAULT 1,
  tax_template_id             text REFERENCES tax_template(id),
  -- Indonesian compliance
  tax_invoice_number          text,                              -- Faktur Pajak serial
  -- totals (transaction currency)
  net_total                   numeric(18,4) NOT NULL DEFAULT 0,
  total_taxes_and_charges     numeric(18,4) NOT NULL DEFAULT 0,
  grand_total                 numeric(18,4) NOT NULL DEFAULT 0,
  -- payment tracking (transaction currency)
  paid_amount                 numeric(18,4) NOT NULL DEFAULT 0,
  outstanding_amount          numeric(18,4) NOT NULL DEFAULT 0,
  -- base currency snapshots
  base_grand_total            numeric(18,4) NOT NULL DEFAULT 0,
  base_outstanding_amount     numeric(18,4) NOT NULL DEFAULT 0,
  remarks                     text,
  receivable_account_id       text NOT NULL REFERENCES account(id),
  is_return                   boolean NOT NULL DEFAULT false,    -- credit note when true
  return_against              text REFERENCES sales_invoice(id),
  docstatus                   smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at, submitted_by, cancelled_at, cancelled_by, amended_from,
  custom_fields, audit cols,
  UNIQUE (company_id, name)
);

CREATE TABLE sales_invoice_item (
  id                 text PRIMARY KEY,
  sales_invoice_id   text NOT NULL REFERENCES sales_invoice(id) ON DELETE CASCADE,
  row_index          int NOT NULL,
  item_id            text REFERENCES item(id),
  item_code          text NOT NULL,                  -- snapshot at submit time
  item_name          text NOT NULL,
  description        text,
  qty                numeric(18,6) NOT NULL,
  uom                text NOT NULL,
  rate               numeric(18,4) NOT NULL,         -- per-unit, transaction currency
  amount             numeric(18,4) NOT NULL,         -- qty * rate (net)
  income_account_id  text NOT NULL REFERENCES account(id),
  cost_center_id     text REFERENCES cost_center(id),
  tax_amount         numeric(18,4) NOT NULL DEFAULT 0,  -- proportional share of taxes
  total              numeric(18,4) NOT NULL,         -- amount + tax_amount
  base_amount        numeric(18,4) NOT NULL,
  base_tax_amount    numeric(18,4) NOT NULL DEFAULT 0,
  base_total         numeric(18,4) NOT NULL
);

CREATE TABLE sales_invoice_tax (
  id                  text PRIMARY KEY,
  sales_invoice_id    text NOT NULL REFERENCES sales_invoice(id) ON DELETE CASCADE,
  row_index           int NOT NULL,
  account_id          text NOT NULL REFERENCES account(id),
  description         text NOT NULL,
  rate                numeric(9,4) NOT NULL,
  charge_type         text NOT NULL,
  included_in_basic_rate boolean NOT NULL DEFAULT false,
  tax_amount          numeric(18,4) NOT NULL,
  base_tax_amount     numeric(18,4) NOT NULL,
  cost_center_id      text REFERENCES cost_center(id)
);

CREATE TABLE sales_invoice_withholding (
  id                       text PRIMARY KEY,
  sales_invoice_id         text NOT NULL REFERENCES sales_invoice(id) ON DELETE CASCADE,
  withholding_tax_type_id  text NOT NULL REFERENCES withholding_tax_type(id),
  rate                     numeric(9,4) NOT NULL,
  amount                   numeric(18,4) NOT NULL,
  base_amount              numeric(18,4) NOT NULL,
  account_id               text NOT NULL REFERENCES account(id)
);
```

### Submit-time GL postings

For an invoice with net `N`, tax `T`, withholding `W`, grand total `G = N + T`:

- **Dr** Receivable `G` (base) — party=customer
- For each item line: **Cr** `income_account` (line.amount, base)
- For each tax row: **Cr** `tax.account_id` (tax.amount, base)
- *(Withholding is NOT booked at invoice time. It's booked at payment time, deducted from cash received.)*

The invariant: sum(debits) = sum(credits) = base grand total. Enforced by `ledger.PostGL`.

### Cancel: standard `ledger.CancelGL` posts inverse rows.

### Outstanding amount maintenance: updated only by Payment Entry submit/cancel.

---

## 4. Payment Entry

```sql
CREATE TABLE payment_entry (
  id                          text PRIMARY KEY,
  name                        text NOT NULL,
  company_id                  text NOT NULL REFERENCES company(id),
  payment_type                text NOT NULL CHECK (payment_type IN ('receive','pay','internal_transfer')),
  party_type                  text CHECK (party_type IN ('customer','supplier','employee')),
  party_id                    text,                              -- nullable for internal_transfer
  posting_date                date NOT NULL,
  fiscal_year_id              text NOT NULL REFERENCES fiscal_year(id),
  -- accounts
  paid_from_account_id        text NOT NULL REFERENCES account(id),  -- receive: AR; pay: cash/bank
  paid_to_account_id          text NOT NULL REFERENCES account(id),  -- receive: cash/bank; pay: AP
  paid_from_currency          text NOT NULL REFERENCES currency(code),
  paid_to_currency            text NOT NULL REFERENCES currency(code),
  -- amounts
  paid_amount                 numeric(18,4) NOT NULL,            -- in paid_from_currency for 'receive', else paid_to_currency
  received_amount             numeric(18,4) NOT NULL,            -- in paid_to_currency
  source_exchange_rate        numeric(18,8) NOT NULL DEFAULT 1,
  target_exchange_rate        numeric(18,8) NOT NULL DEFAULT 1,
  base_paid_amount            numeric(18,4) NOT NULL,
  base_received_amount        numeric(18,4) NOT NULL,
  total_allocated_amount      numeric(18,4) NOT NULL DEFAULT 0,  -- in party currency
  base_total_allocated_amount numeric(18,4) NOT NULL DEFAULT 0,
  unallocated_amount          numeric(18,4) NOT NULL DEFAULT 0,
  total_deductions            numeric(18,4) NOT NULL DEFAULT 0,  -- withholding + fees, in base currency
  reference_no                text,                              -- bank ref, cheque no.
  reference_date              date,
  remarks                     text,
  docstatus                   smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at, submitted_by, cancelled_at, cancelled_by, amended_from,
  custom_fields, audit cols,
  UNIQUE (company_id, name)
);

CREATE TABLE payment_entry_reference (
  id                  text PRIMARY KEY,
  payment_entry_id    text NOT NULL REFERENCES payment_entry(id) ON DELETE CASCADE,
  row_index           int NOT NULL,
  reference_doctype   text NOT NULL,           -- 'sales_invoice' | 'purchase_invoice' | 'journal_entry'
  reference_id        text NOT NULL,
  reference_name      text NOT NULL,           -- snapshot
  total_amount        numeric(18,4) NOT NULL,  -- outstanding before this PE, in party currency
  allocated_amount    numeric(18,4) NOT NULL,  -- portion of paid_amount applied
  base_allocated_amount numeric(18,4) NOT NULL
);

CREATE TABLE payment_entry_deduction (
  id               text PRIMARY KEY,
  payment_entry_id text NOT NULL REFERENCES payment_entry(id) ON DELETE CASCADE,
  row_index        int NOT NULL,
  account_id       text NOT NULL REFERENCES account(id),  -- e.g. PPh 23 payable
  description      text NOT NULL,
  amount           numeric(18,4) NOT NULL,                -- base currency
  cost_center_id   text REFERENCES cost_center(id),
  withholding_tax_type_id text REFERENCES withholding_tax_type(id)
);
```

### Submit-time GL postings (for `payment_type = 'receive'`)

Customer pays an invoice: cash in, AR down, withholding (if any) recognized as payable.

For each reference row:
- **Dr** paid_to_account (cash/bank) `allocated - sum(deductions allocated to this ref)` (base)
- **Cr** paid_from_account (receivable) `allocated` (base) — party=customer
- For each deduction line: **Dr** cash/bank reduced by `deduction.amount`; **Cr** deduction.account_id `deduction.amount` (base)

Simpler aggregate form (what the implementation uses):
- **Dr** cash/bank `base_received_amount` (= base_paid - base_deductions)
- For each deduction: **Dr** deduction.account_id `amount` (e.g. PPh 23 receivable on payer side; for receive, it's deductible — but the seller posts a receivable to a "withholding receivable" account because they will claim it back as a tax credit)
- **Cr** receivable `base_paid_amount` (= sum of allocated_amounts)

Net invariant: total debits == total credits == base_paid_amount.

For `payment_type = 'pay'`: swap. Cash out, AP down, withholding payable booked.

### Reference allocation rules

- `allocated_amount` per reference must be > 0 and <= the reference invoice's current `outstanding_amount`.
- Sum of allocated amounts must equal `paid_amount - deductions` (no unallocated unless explicit `unallocated_amount` field).
- On submit: each referenced invoice's `paid_amount` += allocated, `outstanding_amount` -= allocated, atomically inside the same transaction.
- On cancel: reverse the maintenance.

---

## 5. Trial Balance report

```
GET /api/v1/accounting/reports/trial-balance
    ?company_id=<id>             (required; falls back to X-Company-Id header)
    &from_date=YYYY-MM-DD        (optional; defaults to FY start covering to_date)
    &to_date=YYYY-MM-DD          (optional; defaults to today)
    &include_zero=false          (optional)
```

Returns:
```json
{
  "company_id": "...",
  "from_date": "2026-01-01",
  "to_date":   "2026-05-26",
  "rows": [
    {
      "account_id": "...",
      "account_name": "Kas",
      "account_number": null,
      "root_type": "asset",
      "opening_debit": "0.00",
      "opening_credit": "0.00",
      "period_debit":  "50000000.00",
      "period_credit": "0.00",
      "closing_debit": "50000000.00",
      "closing_credit": "0.00"
    },
    ...
  ],
  "totals": {
    "opening_debit": "0.00", "opening_credit": "0.00",
    "period_debit":  "50000000.00", "period_credit": "50000000.00",
    "closing_debit": "50000000.00", "closing_credit": "50000000.00"
  }
}
```

Implementation: a single CTE-based query over `gl_entry`:
```
WITH opening AS (
  SELECT account_id,
         sum(debit) - sum(credit) AS net
  FROM gl_entry
  WHERE company_id = $1 AND posting_date < $2 AND is_cancelled = false
  GROUP BY account_id
),
period AS (
  SELECT account_id, sum(debit) AS dr, sum(credit) AS cr
  FROM gl_entry
  WHERE company_id = $1 AND posting_date BETWEEN $2 AND $3 AND is_cancelled = false
  GROUP BY account_id
)
SELECT a.id, a.name, a.account_number, a.root_type,
       coalesce(o.net, 0)  AS opening_net,
       coalesce(p.dr,  0)  AS period_dr,
       coalesce(p.cr,  0)  AS period_cr
FROM account a
LEFT JOIN opening o ON o.account_id = a.id
LEFT JOIN period  p ON p.account_id = a.id
WHERE a.company_id = $1 AND a.is_group = false
ORDER BY a.name;
```

Per-row split into opening_debit/credit and closing_debit/credit is done in Go.

Invariant: `sum(closing_debit) == sum(closing_credit)` for the whole company. The report includes an assertion in dev; in prod, the same assertion runs as a nightly check.

---

## 6. Seed additions (Phase 1A)

- `tax_template "PPN Keluaran 11%"` (is_sales=true): one line — account `Utang Pajak - PPN`, rate `11`, charge_type `on_net_total`, included `false`.
- `tax_template "PPN Masukan 11%"` (is_sales=false): mirror, account a new `Pajak Masukan` asset account.
- `withholding_tax_type "PPh 23 - Jasa"`: rate 2%, account `Utang Pajak - PPh`.
- One sample `customer "CUST-001 — PT Pelanggan Contoh"`, `customer_default` linking to demo company's receivable and PPN 11% sales template.
- One sample `item "ITM-CONS — Layanan Konsultasi"` with `standard_rate = 1,000,000`, `item_default.default_income_account = Penjualan`, `default_tax_template = PPN Keluaran 11%`.
- Add `Pajak Masukan` to the COA (asset, sub-type `tax`).
- Default naming series: `SI-.YYYY.-.####`, `PE-.YYYY.-.####` per company.

---

## 7. API endpoints landing this session

- `POST /accounting/tax-categories`, `GET /accounting/tax-categories`
- `POST /accounting/tax-templates`, `GET /accounting/tax-templates`, `GET /.../{id}`
- `POST /accounting/withholding-tax-types`, `GET .../`
- `POST /accounting/customers`, `GET .../`, `GET .../{id}`
- `POST /accounting/suppliers`, `GET .../`, `GET .../{id}`
- `POST /accounting/items`, `GET .../`, `GET .../{id}`
- `POST /accounting/sales-invoices`, `GET .../`, `GET .../{id}`, `POST .../{id}/submit`, `POST .../{id}/cancel`
- `POST /accounting/payment-entries`, `GET .../`, `GET .../{id}`, `POST .../{id}/submit`, `POST .../{id}/cancel`
- `GET /accounting/reports/trial-balance`

---

## 8. What lands when this slice is accepted

The user can: configure a sales tax template; create a customer and a service item; raise an invoice with PPN, submit it (AR/income/tax post to GL); receive partial payment with PPh withholding, submit it (AR drawn down, cash up, withholding receivable up); pull a trial balance that balances and reflects every posting; cancel any document and the books return to zero impact. All four permission layers enforced; OpenAPI spec advertises every endpoint; naming series advance atomically.
