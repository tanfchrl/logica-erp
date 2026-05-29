# Journal Entry

**Doctype:** `journal_entry` · **Tables:** `journal_entry`, `journal_entry_account`
· **Migration:** `0002_accounting_journal_entry.sql` · **Code:**
`internal/accounting/journalentry`

A free-form, manually-balanced posting to the General Ledger. The lowest-level
accounting document: every line names an account and a debit **or** a credit
(never both), and the lines must balance in base currency before submit.

## Endpoints

| Method | Path | Action |
|---|---|---|
| `POST` | `/accounting/journal-entries` | Create draft |
| `GET`  | `/accounting/journal-entries/{id}` | Fetch |
| `GET`  | `/accounting/journal-entries` | List for active company (`X-Company-Id`) |
| `POST` | `/accounting/journal-entries/{id}/submit` | Post to GL |
| `POST` | `/accounting/journal-entries/{id}/cancel` | Reverse GL |

## Lifecycle

`CreateDraft` (docstatus 0, no GL impact) → `Submit` (docstatus 1, posts
`gl_entry` rows via `ledger.PostGL`) → `Cancel` (docstatus 2, posts inverse
entries). On submit the GL invariant (Σdebit = Σcredit in base currency) is
re-checked inside the posting transaction. Submit also fires the
`journal_entry.submitted` notifier event and upserts a global-search row.

## Header fields — `journal_entry`

| Column | Type | Source | Notes |
|---|---|---|---|
| `id` | text PK | system | ULID, `je_` prefix |
| `name` | text | system | rendered from `naming_series` (`JE-.YYYY.-.####`); unique per company |
| `company_id` | text FK | input | restrict-delete |
| `posting_date` | date | input | drives `fiscal_year_id` resolution |
| `fiscal_year_id` | text FK | resolved | the FY containing `posting_date` |
| `voucher_type` | text | input | default `Journal Entry` |
| `currency` | text FK | input | transaction currency (`currency.code`) |
| `exchange_rate` | numeric(18,8) | input | transaction → base; default 1 |
| `total_debit` | numeric(18,4) | derived | Σ line debits (transaction ccy) |
| `total_credit` | numeric(18,4) | derived | Σ line credits (transaction ccy) |
| `user_remark` | text | input | nullable; indexed into global search |
| `docstatus` | smallint | system | 0/1/2 |
| `submitted_at` / `submitted_by` | timestamptz / text FK | system | set on submit |
| `cancelled_at` / `cancelled_by` | timestamptz / text FK | system | set on cancel |
| `amended_from` | text FK | system | self-ref; set when amending a cancelled JE |
| `custom_fields` | jsonb | input | validated against definitions |
| `created_at` / `updated_at` / `created_by` / `updated_by` | — | system | audit columns; `updated_at` via trigger |

## Line fields — `journal_entry_account`

| Column | Type | Source | Notes |
|---|---|---|---|
| `id` | text PK | system | ULID, `jea_` prefix |
| `journal_entry_id` | text FK | system | cascade-delete with parent |
| `row_index` | integer | input | unique per parent |
| `account_id` | text FK | input | GL account to post against |
| `party_type` | text | input | nullable; `customer` \| `supplier` \| `employee` |
| `party_id` | text | input | nullable; party subledger key |
| `cost_center_id` | text FK | input | nullable |
| `project_id` | text | input | nullable |
| `debit` | numeric(18,4) | input | transaction ccy; mutually exclusive with `credit` |
| `credit` | numeric(18,4) | input | transaction ccy |
| `debit_in_account_currency` | numeric(18,4) | input/derived | account-currency leg |
| `credit_in_account_currency` | numeric(18,4) | input/derived | account-currency leg |
| `reference` | text | input | free-text per-line memo |

DB constraints: `(debit = 0) OR (credit = 0)` and `debit >= 0 AND credit >= 0` —
a line is one-sided and non-negative.

## Validation

- At least two lines; each line one-sided (debit XOR credit), non-negative.
- Σdebit must equal Σcredit in base currency (enforced by `ledger.PostGL`; an
  imbalance returns `422 ledger_imbalanced`).
- `account_id` must belong to `company_id`; `party_type`/`party_id` validated
  when present.

## GL posting

One voucher of type `Journal Entry`. Each line posts verbatim — `debit`/`credit`
in base currency (× `exchange_rate`) plus the account-currency legs. Cancel
posts the exact inverse; original and reversal both remain visible in the GL so
they net to zero (see the `is_cancelled` note in project state).

## Related

Naming series seeded as `nms_default_je` (`JE-.YYYY.-.####`). Used directly by
Period Closing Voucher and as the posting primitive other documents wrap.
