# Purchase Invoice

**Doctype:** `purchase_invoice` · **Tables:** `purchase_invoice`,
`purchase_invoice_item`, `purchase_invoice_tax`, `purchase_invoice_withholding`
· **Migration:** `0006_accounting_purchase_invoice.sql` · **Code:**
`internal/accounting/purchaseinvoice`

A payable document and the mirror of [Sales Invoice](sales-invoice.md): records a
supplier's bill, recovers input VAT (PPN Masukan), optionally records PPh
withholding, and posts to the GL on submit. `outstanding_amount` decreases as
Payment Entries (`payment_type=pay`) settle it.

## Endpoints

| Method | Path | Action |
|---|---|---|
| `POST` | `/accounting/purchase-invoices` | Create draft |
| `GET`  | `/accounting/purchase-invoices/{id}` | Fetch |
| `GET`  | `/accounting/purchase-invoices` | List for active company |
| `POST` | `/accounting/purchase-invoices/{id}/submit` | Post to GL |
| `POST` | `/accounting/purchase-invoices/{id}/cancel` | Reverse GL |

## Lifecycle

`CreateDraft` → `Submit` → `Cancel`. On submit: tax computed, GL posted,
`outstanding_amount` set to `grand_total`, `bill.received` notifier fires, a
global-search row is upserted, and — for any line whose item is a fixed asset —
a **draft Asset** is auto-created post-commit (failures logged, not unwound).
Submit honours per-company **Buying Settings** (over-billing tolerance, "PO
required") and may be gated by approval rule / workflow. **Cancel is rejected
when any payment exists.** `is_return` + `return_against` issues a Debit Note
(polarity swapped); rejects sales templates.

## Header fields — `purchase_invoice`

| Column | Type | Source | Notes |
|---|---|---|---|
| `id` | text PK | system | ULID |
| `name` | text | system | `PI-.YYYY.-.####`; unique per company |
| `company_id` | text FK | input | restrict-delete |
| `supplier_id` | text FK | input | party |
| `posting_date` | date | input | drives FY |
| `due_date` | date | input | |
| `fiscal_year_id` | text FK | resolved | FY of `posting_date` |
| `currency` | text FK | input/resolved | defaults to supplier/company currency |
| `exchange_rate` | numeric(18,8) | input | transaction → base; default 1 |
| `tax_template_id` | text FK | input/resolved | nullable; supplier default purchase template |
| `supplier_invoice_no` | text | input | supplier's own document number |
| `supplier_invoice_date` | date | input | supplier's document date |
| `bill_no` | text | input | nullable alternate reference |
| `net_total` | numeric(18,4) | derived | Σ line `amount` |
| `total_taxes_and_charges` | numeric(18,4) | derived | Σ tax rows |
| `grand_total` | numeric(18,4) | derived | `net_total + total_taxes_and_charges` |
| `paid_amount` | numeric(18,4) | system | maintained by Payment Entry |
| `outstanding_amount` | numeric(18,4) | system | decremented by payments |
| `base_*` totals | numeric(18,4) | derived | base-ccy snapshots |
| `remarks` | text | input | nullable; indexed into search |
| `payable_account_id` | text FK | resolved | AP account from supplier per-company default |
| `is_return` | boolean | input | debit-note flag |
| `return_against` | text FK | input | self-ref to original PI |
| `docstatus` / `submitted_*` / `cancelled_*` / `amended_from` | — | system | lifecycle |
| `custom_fields` | jsonb | input | validated |
| audit columns | — | system | |

## Item fields — `purchase_invoice_item`

Same shape as Sales Invoice items, except the GL account is an **expense**
account: `expense_account_id` is resolved from the item's per-company
`default_expense_account_id`. Columns: `row_index`, `item_id` (nullable),
`item_code`, `item_name`, `description`, `qty`, `uom`, `rate`, `amount`,
expense account, `cost_center_id`, `tax_amount`, `total`, and `base_*` snapshots.

## Tax / withholding

`purchase_invoice_tax` mirrors the sales tax table but posts to a **tax
recoverable** (input VAT) account. `purchase_invoice_withholding` records PPh to
withhold; realised on payment via Payment Entry deductions.

## GL posting (submit)

**Dr Expense** per item line + **Dr Tax Recoverable** per tax row / **Cr
Payable** for `grand_total` (party = supplier). Balanced by construction. Debit
notes invert every leg; cancel posts the inverse voucher.

## Related

Settled by [Payment Entry](payment-entry.md) (`payment_type=pay`). May be linked
from a Purchase Order / Purchase Receipt via `doc_link`. Fixed-asset lines spawn
draft `asset` records.
