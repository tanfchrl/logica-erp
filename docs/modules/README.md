# Module specs

Field-level specifications for each Logica doctype. Per the build brief, every
core doctype gets a spec doc here describing its fields, lifecycle, validation,
and GL/SLE side effects — the contract a doctype's Go struct, table, and HTTP
handlers all implement.

These are **descriptive of the shipped code**, not aspirational. When a doctype
changes, update its spec in the same change. The source of truth is, in order:
the migration (column types + constraints), the domain struct, and the
`Submit()`/`Cancel()` posting logic.

## Conventions used in every spec

- **Field tables** list the persisted columns. The *Source* column says where a
  field's value comes from:
  - `input` — supplied by the caller on create.
  - `derived` — computed by the service (totals, base-currency snapshots).
  - `resolved` — looked up from a master/default (e.g. receivable account from
    the customer's per-company default).
  - `system` — lifecycle/audit columns set by the platform (`docstatus`,
    `submitted_at`, `created_by`, …).
- **Money** is `numeric(18,4)`; **quantity** is `numeric(18,6)`; **exchange
  rate** is `numeric(18,8)`. Floats are never used in financial paths.
- **`base_*` columns** are the base-currency (company default currency)
  snapshot of the transaction-currency figure, frozen at submit using the
  document's `exchange_rate`.
- **Lifecycle**: `docstatus` is `0` Draft, `1` Submitted, `2` Cancelled. Submit
  posts to the ledger; Cancel posts inverse entries (rows are never deleted).
  See [decisions](../../docs/phase-0-design.md).
- **Naming**: `name` (e.g. `SI-2026-0001`) is rendered from the per-company
  `naming_series` row at create; it is separate from the ULID `id`.
- Every transactional row carries `company_id`; every table has
  `custom_fields jsonb` validated against `custom_field_definition`.

## Index

### Accounting
- [Journal Entry](accounting/journal-entry.md) — `journal_entry`
- [Sales Invoice](accounting/sales-invoice.md) — `sales_invoice`
- [Purchase Invoice](accounting/purchase-invoice.md) — `purchase_invoice`
- [Payment Entry](accounting/payment-entry.md) — `payment_entry`

_Remaining doctypes (masters, stock, HR/payroll, manufacturing, CRM) are
specified as their specs are written — start from the four highest-traffic
accounting documents above._
