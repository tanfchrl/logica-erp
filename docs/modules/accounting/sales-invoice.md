# Sales Invoice

**Doctype:** `sales_invoice` · **Tables:** `sales_invoice`,
`sales_invoice_item`, `sales_invoice_tax`, `sales_invoice_withholding` ·
**Migration:** `0004_accounting_sales_invoice.sql` · **Code:**
`internal/accounting/salesinvoice`

A receivable document: bills a customer for items/services, applies a tax
template (PPN), optionally records PPh withholding, and posts to the General
Ledger on submit. Outstanding amount is maintained as Payment Entries settle it.

## Endpoints

| Method | Path | Action |
|---|---|---|
| `POST` | `/accounting/sales-invoices` | Create draft |
| `GET`  | `/accounting/sales-invoices/{id}` | Fetch |
| `GET`  | `/accounting/sales-invoices` | List for active company |
| `POST` | `/accounting/sales-invoices/{id}/submit` | Post to GL |
| `POST` | `/accounting/sales-invoices/{id}/cancel` | Reverse GL |
| `GET`  | `/accounting/sales-invoices/{id}/print` | Faktur Pajak PDF (Gotenberg) |

## Lifecycle

`CreateDraft` → `Submit` → `Cancel`. On submit: tax engine computes per-line tax,
GL is posted, `outstanding_amount` is initialised to `grand_total`, the
`invoice.issued` notifier event fires, and a global-search row is upserted.
Submit may be gated by an approval rule and/or workflow when configured for the
`(doctype, company)`. **Cancel is rejected if any payment has been applied**
(`paid_amount > 0`).

Set `is_return = true` + `return_against` to issue a **Credit Note**: submit
swaps Dr/Cr polarity on every leg; the original must be a submitted invoice for
the same customer.

## Header fields — `sales_invoice`

| Column | Type | Source | Notes |
|---|---|---|---|
| `id` | text PK | system | ULID |
| `name` | text | system | `SI-.YYYY.-.####`; unique per company |
| `company_id` | text FK | input | restrict-delete |
| `customer_id` | text FK | input | party |
| `posting_date` | date | input | drives FY |
| `due_date` | date | input | payment terms |
| `fiscal_year_id` | text FK | resolved | FY of `posting_date` |
| `currency` | text FK | input/resolved | defaults to customer/company currency |
| `exchange_rate` | numeric(18,8) | input | transaction → base; default 1 |
| `tax_template_id` | text FK | input/resolved | nullable; falls back to customer default sales template |
| `tax_invoice_number` | text | input | nullable; Faktur Pajak no. (feeds e-Faktur export) |
| `net_total` | numeric(18,4) | derived | Σ line `amount` |
| `total_taxes_and_charges` | numeric(18,4) | derived | Σ tax rows |
| `grand_total` | numeric(18,4) | derived | `net_total + total_taxes_and_charges` |
| `paid_amount` | numeric(18,4) | system | maintained by Payment Entry |
| `outstanding_amount` | numeric(18,4) | system | `grand_total` at submit, decremented by payments |
| `base_net_total` / `base_total_taxes_and_charges` / `base_grand_total` / `base_paid_amount` / `base_outstanding_amount` | numeric(18,4) | derived | base-ccy snapshots |
| `remarks` | text | input | nullable; indexed into search |
| `receivable_account_id` | text FK | resolved | AR account from customer per-company default |
| `is_return` | boolean | input | credit-note flag |
| `return_against` | text FK | input | self-ref to original SI |
| `docstatus` / `submitted_*` / `cancelled_*` / `amended_from` | — | system | lifecycle |
| `custom_fields` | jsonb | input | validated |
| audit columns | — | system | `created_*`, `updated_*` |

## Item fields — `sales_invoice_item`

| Column | Type | Source | Notes |
|---|---|---|---|
| `row_index` | integer | input | unique per invoice |
| `item_id` | text FK | input | nullable (free-text line allowed) |
| `item_code` / `item_name` | text | input/resolved | snapshot from item master |
| `description` | text | input | nullable |
| `qty` | numeric(18,6) | input | |
| `uom` | text | input | |
| `rate` | numeric(18,4) | input | unit price (transaction ccy) |
| `amount` | numeric(18,4) | derived | `qty × rate` |
| `income_account_id` | text FK | input/resolved | revenue account (item default) |
| `cost_center_id` | text FK | input | nullable |
| `tax_amount` | numeric(18,4) | derived | tax distributed to this line |
| `total` | numeric(18,4) | derived | `amount + tax_amount` |
| `base_amount` / `base_tax_amount` / `base_total` | numeric(18,4) | derived | base-ccy snapshots |

## Tax fields — `sales_invoice_tax`

Computed by `internal/platform/tax.Calculate` from the resolved template. One row
per template line: `account_id`, `description`, `rate` (numeric(9,4)),
`charge_type` (`on_net_total` \| `on_previous_amount` \| `actual`),
`included_in_basic_rate`, `tax_amount`, `base_tax_amount`, optional
`cost_center_id`. Proportional per-line distribution, residual to the
largest line.

## Withholding fields — `sales_invoice_withholding`

Optional PPh rows: `withholding_tax_type_id`, `rate`, `amount`, `base_amount`,
`account_id`. Withholding-payable postings are realised when the payment is
recorded (see Payment Entry deductions).

## GL posting (submit)

Per item line **Dr** `receivable_account_id` / **Cr** `income_account_id`, and
per tax row **Cr** the tax account — i.e. **Dr AR `grand_total`**, **Cr Income**
per line, **Cr Tax Payable** per tax row. Balanced by construction. Credit notes
invert every leg. Cancel posts the exact inverse voucher.

## Related

Settled by [Payment Entry](payment-entry.md) (`payment_type=receive`). Tax via
`tax_template`; withholding via `withholding_tax_type`. Print template resolved
per `(doctype, company)`; e-Faktur CSV export reads `tax_invoice_number`.
