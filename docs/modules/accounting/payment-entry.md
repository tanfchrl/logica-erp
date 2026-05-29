# Payment Entry

**Doctype:** `payment_entry` · **Tables:** `payment_entry`,
`payment_entry_reference`, `payment_entry_deduction` · **Migration:**
`0005_accounting_payment_entry.sql` · **Code:** `internal/accounting/paymententry`

Records cash moving in or out and settles it against one or more invoices. A
`receive` payment clears Sales Invoices; a `pay` payment clears Purchase
Invoices. Withholding (PPh) and other settle-on-top amounts are modelled as
**deductions**.

## Endpoints

| Method | Path | Action |
|---|---|---|
| `POST` | `/accounting/payment-entries` | Create draft |
| `GET`  | `/accounting/payment-entries/{id}` | Fetch |
| `GET`  | `/accounting/payment-entries` | List for active company |
| `POST` | `/accounting/payment-entries/{id}/submit` | Post to GL + maintain invoices |
| `POST` | `/accounting/payment-entries/{id}/cancel` | Reverse GL + invoice maintenance |

## The allocation invariant

```
total_allocated_amount + unallocated_amount = paid_amount + total_deductions
```

`paid_amount` is the cash that actually moves (source currency); deductions
settle invoice value **on top of** cash. Example — an SI of 5,550,000 cleared by
4,500,000 cash + 550,000 + 500,000 PPh: `paid=5,000,000`, `deductions=550,000`,
`allocated=5,550,000`, `unallocated=0`. Computed by `computeUnallocated(paid,
allocated, deductions)`; over-allocation is rejected. Overpayment lands in
`unallocated_amount` (customer/supplier advance).

> Cross-currency with non-zero deductions is **undefined** in v1 — deduction
> amounts are treated as base currency. Fine for the all-IDR SME case.

## Lifecycle

`CreateDraft` → `Submit` → `Cancel`. Submit posts GL **and** atomically updates
each referenced invoice's `paid_amount` / `outstanding_amount`; cancel reverses
both the GL and the invoice maintenance (via `applyReferenceUpdates`, sign +1 on
submit / −1 on cancel). Submit may be gated by approval/workflow and fires a
notifier event.

## Header fields — `payment_entry`

| Column | Type | Source | Notes |
|---|---|---|---|
| `id` | text PK | system | ULID |
| `name` | text | system | `PE-.YYYY.-.####`; unique per company |
| `company_id` | text FK | input | restrict-delete |
| `payment_type` | text | input | `receive` \| `pay` \| `internal_transfer` |
| `party_type` | text | input | nullable; `customer` \| `supplier` \| `employee` |
| `party_id` | text | input | nullable |
| `posting_date` | date | input | drives FY |
| `fiscal_year_id` | text FK | resolved | FY of `posting_date` |
| `paid_from_account_id` | text FK | input | source account (AR for receive, cash for pay) |
| `paid_to_account_id` | text FK | input | destination account (cash for receive, AP for pay) |
| `paid_from_currency` / `paid_to_currency` | text FK | resolved | account currencies |
| `paid_amount` | numeric(18,4) | input | cash moving, source ccy |
| `received_amount` | numeric(18,4) | input/derived | cash on target side |
| `source_exchange_rate` / `target_exchange_rate` | numeric(18,8) | input | to base; default 1 |
| `base_paid_amount` / `base_received_amount` | numeric(18,4) | derived | base-ccy snapshots |
| `total_allocated_amount` / `base_total_allocated_amount` | numeric(18,4) | derived | Σ reference allocations |
| `unallocated_amount` | numeric(18,4) | derived | per the invariant above |
| `total_deductions` | numeric(18,4) | derived | Σ deduction amounts |
| `reference_no` | text | input | nullable; bank/transfer reference |
| `reference_date` | date | input | nullable |
| `remarks` | text | input | nullable |
| `docstatus` / `submitted_*` / `cancelled_*` / `amended_from` | — | system | lifecycle |
| `custom_fields` | jsonb | input | validated |
| audit columns | — | system | |

## Reference fields — `payment_entry_reference`

One row per settled invoice: `row_index`, `reference_doctype`
(`sales_invoice` \| `purchase_invoice`), `reference_id`, `reference_name`,
`total_amount` (invoice grand total), `allocated_amount`,
`base_allocated_amount`. Polarity is enforced: `receive` may only reference sales
invoices, `pay` only purchase invoices.

## Deduction fields — `payment_entry_deduction`

`row_index`, `account_id` (the withholding-payable / charge account),
`description`, `amount`, optional `cost_center_id`, optional
`withholding_tax_type_id`.

## GL posting (submit)

- **receive**: **Dr** cash (`paid_amount`) + **Dr** deduction accounts / **Cr**
  AR for `paid + deductions` (party = customer).
- **pay**: **Dr** AP for `paid + deductions` (party = supplier) / **Cr** cash
  (`received_amount`) + **Cr** each deduction account.

Balanced by construction. Cancel posts the exact inverse and restores each
referenced invoice's outstanding amount.

## Related

References [Sales Invoice](sales-invoice.md) / [Purchase Invoice](purchase-invoice.md).
Deductions reference `withholding_tax_type`. Feeds AR/AP ageing and the PPN/PPh
reports.
