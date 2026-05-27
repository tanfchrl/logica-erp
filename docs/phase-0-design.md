# Phase 0 — Foundation & Design Document

> Status: **Draft, awaiting review.** Per the build brief §11, Phase 0 implementation does not begin until this document is reviewed.
> Owner: TAN Digital (PT Teknologic Aksara Nusantara) · Product: Logica ERP · Phase target: foundation only.

---

## 0. Decisions log

### 0.1 Locked by the brief (do not revisit)

| Area | Decision |
|---|---|
| Backend language | Go 1.23+ |
| Database | PostgreSQL 16+, single DB |
| Money | `numeric(18,4)` in DB, `shopspring/decimal` in Go. Floats banned in financial paths. |
| Frontend | TanStack Router + Query + Table + Form, Vite, TypeScript strict |
| API | REST/JSON under `/api/v1`, OpenAPI 3.1 |
| Auth | JWT access tokens + rotating refresh token in httpOnly cookie |
| Tenancy | Single-tenant install, multi-company within install |
| Entity model | Hybrid: fixed schema per domain + `custom_fields JSONB` extensibility |
| GL invariant | Per-voucher debit = credit in base currency, enforced inside posting transaction |
| Lifecycle | `docstatus` 0/1/2; cancel = reversing entries, never delete |
| Locale default | Bahasa Indonesia, IDR |
| Permissions | 4 layers: RBAC, row-level, field-level, multi-company scope |
| Deployment | Docker Compose on single VPS |

### 0.2 Selected during Phase 0 design

| Area | Decision | Rationale |
|---|---|---|
| HTTP framework | **Huma v2** on top of `chi` | Generates OpenAPI 3.1 from typed handlers, keeps the stdlib `net/http` shape, no need to manually maintain a spec file. |
| SQL access | **sqlc** for queries, **pgx/v5** as driver | Type-safe queries without ORM weight; pgx handles `numeric`/`jsonb` well. |
| Migrations | **goose** | Idiomatic Go, simple CLI, supports both SQL-only and Go-glue migrations. Forward-only per brief. |
| Background jobs | **River** | Postgres-backed, no Redis required for default install (per brief). |
| Decimal lib | `shopspring/decimal` | De-facto standard; converts cleanly to/from pgx `numeric`. |
| Logging | stdlib `log/slog` with JSON handler | Structured by default, no extra dep. |
| Metrics | `prometheus/client_golang` exposed at `/metrics` | Standard. |
| Test framework | stdlib `testing` + `testify/require` + **testcontainers-go** for Postgres | Real Postgres, no mocks for ledger math. |
| UI primitives | **Ark UI** (headless) + Tailwind | Better data-table / combobox primitives for a dense data app; framework-agnostic, actively maintained. |
| Icons | `lucide-react` | Wide coverage, tree-shakeable. |
| i18n | `i18next` + `react-i18next` on frontend; backend returns error codes + params, never localized strings | Keeps the API portable, frontend owns presentation. |
| PDF engine | **Gotenberg** (separate Docker service) | Highest fidelity for invoice/Faktur layouts; templates are HTML+CSS. `PrintRenderer` interface decouples it. |
| Object storage | Local filesystem default, S3-compat adapter via interface | Brief requirement. MinIO supported. |
| Primary keys | **ULID** (`text` column, fixed 26 chars) for surrogate IDs across all tables | Sortable, URL-safe, no DB-internal sequence leakage, friendly in logs. |
| Display IDs | Separate `name` column for naming-series strings (e.g. `INV-2026-0001`); unique per `(doctype, company_id)` | Mirrors ERPNext's `name` convention without coupling primary keys to fiscal series. |
| Naming series syntax | ERPNext-style placeholders: `INV-.YYYY.-.####`; `.YYYY.` resolves to fiscal year start year, `.MM.`, `.DD.`, `.####` width-padded counter | Familiar to ERPNext users; the parser is ~50 lines. |
| ID exposure in API | ULIDs in `id`, document `name` in `name` | Both unique; the `name` is what users see. |
| Time | UTC in DB, `timestamptz`; render in user timezone on the client | One-line policy, no surprises. |
| Frontend build | Vite + pnpm workspace | One frontend package today, leaves room for a separate POS bundle later. |

### 0.3 Domain decisions

| Area | Decision |
|---|---|
| Master sharing | **Global masters with per-company defaults.** Items and Customers live in shared tables; `item_default` / `customer_default` child tables hold per-company overrides (default warehouse, income account, price list). |
| NPWP | **16-digit only**, validated as 16 numeric chars. Legacy 15-digit values are an import problem, not a runtime concern. |
| PPN rate | Configurable rate master keyed by effective date. Seeded with current Indonesian standard rate; the system never hardcodes a percentage. |
| Withholding tax (PPh 21/23/25/26) | First-class concept on Payment Entry and at line-level on Sales/Purchase Invoice. Withholding generates GL entries to a withholding-payable account. |
| Inter-company transactions | **Out of scope for v1.** Documented as v2 item. |
| Live integrations (Coretax, gateways, WhatsApp) | **Interfaces only in v1.** No live calls. |
| Workflow engine | Data model defined in Phase 0 (so we don't migrate later), runtime polish lands in Phase 6 per the brief. |

---

## 1. Repository layout

```
LogicaERP/
├── cmd/
│   ├── api/             # HTTP server entrypoint
│   ├── worker/          # River worker entrypoint
│   └── logica/          # CLI: migrate, seed, backup, restore, user-add
├── internal/
│   ├── platform/        # Cross-cutting infra (no domain logic)
│   │   ├── auth/        # JWT, refresh tokens, password hashing
│   │   ├── permission/  # 4-layer permission engine
│   │   ├── ledger/      # GL + Stock ledger posting helpers, invariants
│   │   ├── submittable/ # Draft/Submit/Cancel lifecycle, amend
│   │   ├── metadata/    # Field defs, list views, naming series, workflow
│   │   ├── customfield/ # JSONB custom-fields validation + indexing
│   │   ├── audit/       # Document change log
│   │   ├── naming/      # Naming series parser + atomic counter
│   │   ├── money/       # Decimal helpers, FX conversion
│   │   ├── i18n/        # Error code registry
│   │   ├── httpx/       # Huma setup, middleware, error mapping
│   │   ├── dbx/         # pgx pool, transaction helpers
│   │   ├── jobs/        # River setup, job registration
│   │   ├── storage/     # File storage interface (local + S3)
│   │   ├── print/       # PrintRenderer interface + Gotenberg adapter
│   │   └── notify/      # In-app + email + (later) WhatsApp seam
│   ├── accounting/      # Phase 1
│   ├── stock/           # Phase 2
│   ├── buying/          # Phase 2
│   ├── selling/         # Phase 2
│   ├── crm/             # Phase 3
│   ├── projects/        # Phase 3
│   ├── manufacturing/   # Phase 4
│   ├── assets/          # Phase 4
│   ├── hr/              # Phase 5
│   ├── pos/             # Phase 5
│   └── support/         # Phase 5
├── migrations/          # goose SQL migrations, forward-only
├── seed/                # Demo data, COA templates (ID + generic)
├── web/                 # TanStack frontend (Vite)
│   ├── src/
│   │   ├── routes/      # File-based routing
│   │   ├── features/    # Per-module UI
│   │   ├── platform/    # Form renderer, list view, perm-aware components
│   │   └── lib/         # API client (generated from OpenAPI), i18n, utils
│   ├── public/locales/  # id-ID, en-US
│   └── package.json
├── deploy/
│   ├── docker-compose.yml          # prod
│   ├── docker-compose.dev.yml      # dev (postgres + minio + gotenberg)
│   ├── Caddyfile
│   ├── api.Dockerfile
│   ├── web.Dockerfile
│   └── install.sh                  # one-shot Ubuntu VPS bootstrap
├── docs/
│   ├── phase-0-design.md           # this file
│   ├── adr/                        # architectural decisions
│   └── modules/                    # per-module field-level specs
├── Makefile                        # up/down/migrate/seed/logs/backup/restore
├── .env.example
├── .golangci.yml
├── go.mod
└── README.md
```

Per-module Go package convention (e.g. `internal/accounting/`):
```
accounting/
├── domain.go        # structs (the Go view of the table rows)
├── repo.go          # sqlc-generated queries re-exported via repo interface
├── service.go       # business logic: submit/cancel, GL posting, validation
├── http.go          # Huma handlers, request/response types
├── jobs.go          # River job handlers owned by this module
├── service_test.go  # unit tests
└── http_test.go     # integration tests (real Postgres via testcontainers)
```

---

## 2. Database conventions

- All tables use `snake_case`. Primary key is `id text PRIMARY KEY` (ULID), generated server-side.
- Every transactional/master table includes:
  - `created_at timestamptz NOT NULL DEFAULT now()`
  - `updated_at timestamptz NOT NULL DEFAULT now()` (trigger maintains)
  - `created_by text NOT NULL REFERENCES users(id)`
  - `updated_by text NOT NULL REFERENCES users(id)`
  - `custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb`
- Every multi-company table includes `company_id text NOT NULL REFERENCES company(id)` and an index on `(company_id)` plus typically a unique on `(company_id, name)`.
- Submittable documents add `docstatus smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2))`, `submitted_at timestamptz`, `submitted_by text`, `cancelled_at timestamptz`, `cancelled_by text`, `amended_from text REFERENCES <self>(id)`.
- Money columns are always `numeric(18,4) NOT NULL DEFAULT 0`. Quantity columns are `numeric(18,6)` (allow finer item granularity).
- Foreign keys are `ON DELETE RESTRICT` by default. Masters are soft-deleted (`is_deleted boolean NOT NULL DEFAULT false`); transactional documents are never deleted (cancel instead).
- All `jsonb` columns destined for filtering use `GIN` indexes on the relevant subpaths.
- Tree tables (Account, Warehouse, Cost Center, Item Group, etc.) use `parent_id text` plus nested-set `lft int` / `rgt int` for fast subtree queries. Rebuild on edit inside a transaction.

---

## 3. Cross-cutting platform

### 3.1 Auth

- `users` table: `id`, `email` (unique, lower-cased), `password_hash` (argon2id), `full_name`, `enabled bool`, `locale`, `time_zone`, audit cols.
- `user_session` table: `id` (ULID), `user_id`, `refresh_token_hash` (sha256 of token), `issued_at`, `expires_at`, `rotated_to text` (chain), `user_agent`, `ip`, `revoked_at`.
- Access tokens: JWT, **15 minute lifetime**, `HS256` signed with key from env, claims = `sub`, `companies` (array), `roles` (array), `iat`, `exp`, `jti`.
- Refresh tokens: 256-bit random, **30 day lifetime**, stored as sha256, rotated on every use, parent token revoked on rotation. Reuse of a rotated token revokes the whole chain (replay detection).
- Refresh cookie: `httpOnly`, `Secure`, `SameSite=Lax`, path `/api/v1/auth`.
- Endpoints: `POST /auth/login`, `POST /auth/refresh`, `POST /auth/logout`, `GET /auth/me`.

### 3.2 Permission model — schema

Four layers, all enforced server-side inside one composable check.

```sql
-- 1. RBAC
CREATE TABLE role (
  id text PRIMARY KEY,
  name text NOT NULL UNIQUE,
  description text,
  is_system boolean NOT NULL DEFAULT false
);

CREATE TABLE user_role (
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id text NOT NULL REFERENCES role(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, role_id)
);

-- One row per (role, doctype) granting actions; missing = denied.
CREATE TABLE role_permission (
  id text PRIMARY KEY,
  role_id text NOT NULL REFERENCES role(id) ON DELETE CASCADE,
  doctype text NOT NULL,         -- e.g. 'sales_invoice'
  can_read boolean NOT NULL DEFAULT false,
  can_write boolean NOT NULL DEFAULT false,
  can_create boolean NOT NULL DEFAULT false,
  can_delete boolean NOT NULL DEFAULT false,
  can_submit boolean NOT NULL DEFAULT false,
  can_cancel boolean NOT NULL DEFAULT false,
  can_amend boolean NOT NULL DEFAULT false,
  can_print boolean NOT NULL DEFAULT false,
  can_export boolean NOT NULL DEFAULT false,
  UNIQUE (role_id, doctype)
);

-- 2. Row-level (user-permission). Restricts which records a user can see by linking the user to allowed values of a scoping field.
-- Example: user X allowed only for territory IN ('Jakarta','Bandung'); user Y only for branch 'HQ'.
CREATE TABLE user_permission (
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope text NOT NULL,        -- the field name on the target doctype, e.g. 'territory'
  value text NOT NULL,        -- the allowed value, e.g. a territory id
  applicable_for text,        -- optional: limit this rule to a single doctype; NULL = applies to all
  UNIQUE (user_id, scope, value, applicable_for)
);

-- 3. Field-level
CREATE TABLE field_permission (
  id text PRIMARY KEY,
  role_id text NOT NULL REFERENCES role(id) ON DELETE CASCADE,
  doctype text NOT NULL,
  field text NOT NULL,
  can_read boolean NOT NULL DEFAULT true,
  can_write boolean NOT NULL DEFAULT true,
  UNIQUE (role_id, doctype, field)
);

-- 4. Multi-company access
CREATE TABLE user_company (
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  company_id text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, company_id)
);
```

**Enforcement.** A `permission.Engine` in `internal/platform/permission` exposes:

```go
// Action checks at the doctype level.
Check(ctx, user, doctype, action) error          // RBAC + company scope

// Returns SQL predicates to AND into list queries.
RowFilter(ctx, user, doctype) (sql, args, error) // row-level + company scope

// Field-level filters for read and write.
ReadableFields(ctx, user, doctype, fields []string) []string
WritableFields(ctx, user, doctype, fields []string) []string
```

Every Huma handler calls `Check` (and optionally `RowFilter` for list endpoints). The frontend gets the same metadata via `GET /metadata/permissions` and uses it to hide fields and actions, but the server is authoritative.

### 3.3 Custom fields layer

```sql
CREATE TABLE custom_field_definition (
  id text PRIMARY KEY,
  doctype text NOT NULL,
  field_name text NOT NULL,                 -- the JSONB key, snake_case
  label_id text NOT NULL,                   -- Bahasa label
  label_en text NOT NULL,
  field_type text NOT NULL,                 -- text, int, decimal, date, datetime, bool, select, link, table
  is_required boolean NOT NULL DEFAULT false,
  default_value text,
  options jsonb,                            -- select options, link target doctype, validation regex, min/max
  position int NOT NULL DEFAULT 0,
  is_indexed boolean NOT NULL DEFAULT false, -- if true, create a partial GIN expression index
  UNIQUE (doctype, field_name)
);
```

- Stored in the core table's `custom_fields jsonb` column.
- On read: API merges `custom_fields` into the response shape.
- On write: validated against `custom_field_definition` rows for that doctype before INSERT/UPDATE.
- Indexing: admins flagging a field with `is_indexed=true` triggers a migration helper that creates a `CREATE INDEX ... USING GIN ((custom_fields->'field_name'))`. Forward-only.
- The `link` field type stores `{type, id}` and is validated against the target doctype's existence.

### 3.4 Metadata service

A single read-only service that powers form generation, list views, and permission-aware UI. Backed by a mix of seed data and `custom_field_definition`.

```
GET /metadata/doctype/{name}    -> fields, custom fields, naming series rules, perms for current user
GET /metadata/listview/{name}   -> columns, filters, default sort
GET /metadata/naming-series     -> all configured series
GET /metadata/workflow/{name}   -> state machine for this doctype
GET /metadata/permissions       -> compact bitmap of (doctype, action) the current user can do
```

The Go side keeps per-doctype "field manifests" as values (not generated from runtime schema). New doctypes register their manifest at startup; custom fields are merged in at query time.

### 3.5 Audit trail

```sql
CREATE TABLE document_audit (
  id text PRIMARY KEY,
  doctype text NOT NULL,
  document_id text NOT NULL,
  action text NOT NULL,            -- create, update, submit, cancel, amend, delete (masters only)
  changed_by text NOT NULL REFERENCES users(id),
  changed_at timestamptz NOT NULL DEFAULT now(),
  diff jsonb NOT NULL              -- {before: {...}, after: {...}} with only changed fields
);
CREATE INDEX document_audit_doc_idx ON document_audit (doctype, document_id, changed_at DESC);
```

All writes go through a `submittable.Transact` helper that captures the before/after diff and inserts the audit row in the same transaction. Append-only; no UPDATE/DELETE permitted (revoked at the DB role level).

### 3.6 Naming series

```sql
CREATE TABLE naming_series (
  id text PRIMARY KEY,
  doctype text NOT NULL,
  company_id text REFERENCES company(id),   -- NULL = applies to all companies
  pattern text NOT NULL,                    -- e.g. 'INV-.YYYY.-.####'
  is_default boolean NOT NULL DEFAULT false,
  UNIQUE (doctype, company_id, pattern)
);

CREATE TABLE naming_series_counter (
  series_id text NOT NULL REFERENCES naming_series(id) ON DELETE CASCADE,
  scope_key text NOT NULL,         -- the resolved scope, e.g. '2026' for YYYY series
  current_value bigint NOT NULL,
  PRIMARY KEY (series_id, scope_key)
);
```

`naming.Next(ctx, doctype, companyID) (string, error)` does an atomic `INSERT ... ON CONFLICT (series_id, scope_key) DO UPDATE SET current_value = naming_series_counter.current_value + 1 RETURNING current_value` inside the document's submit transaction. Series may be edited but the counter is never reset by the application.

### 3.7 Submittable lifecycle

```go
package submittable

type Doc interface {
    Doctype() string
    ID() string
    CompanyID() string
    Docstatus() int16
}

// Helpers; all operate inside a pgx.Tx the caller already opened.
func Save(ctx, tx, doc Doc, perm Engine, audit Recorder) error      // upsert draft
func Submit(ctx, tx, doc Submittable, perm, audit, ledger, naming) error
func Cancel(ctx, tx, doc Submittable, perm, audit, ledger) error    // posts reversing entries
func Amend(ctx, tx, doc Submittable) (Doc, error)                   // clones a cancelled doc to draft
```

The `Submittable` interface adds:
```go
MakeGLEntries(ctx, tx) ([]ledger.Entry, error)
MakeStockEntries(ctx, tx) ([]ledger.StockEntry, error)   // empty for non-stock docs
Validate(ctx) error
OnSubmit(ctx, tx) error                                  // optional hooks
OnCancel(ctx, tx) error
```

Cancellation always posts the *exact inverse* of the original entries (same accounts, swapped debit/credit, `against_voucher` linked) — it never deletes ledger rows.

### 3.8 General ledger

```sql
CREATE TABLE gl_entry (
  id text PRIMARY KEY,
  company_id text NOT NULL REFERENCES company(id),
  posting_date date NOT NULL,
  account_id text NOT NULL REFERENCES account(id),
  party_type text,                          -- 'customer' | 'supplier' | 'employee' | NULL
  party_id text,
  cost_center_id text REFERENCES cost_center(id),
  project_id text REFERENCES project(id),
  debit numeric(18,4) NOT NULL DEFAULT 0,   -- base currency
  credit numeric(18,4) NOT NULL DEFAULT 0,  -- base currency
  account_currency text NOT NULL,
  debit_in_account_currency numeric(18,4) NOT NULL DEFAULT 0,
  credit_in_account_currency numeric(18,4) NOT NULL DEFAULT 0,
  against text,                              -- against accounts string for reports
  voucher_type text NOT NULL,
  voucher_id text NOT NULL,
  voucher_name text NOT NULL,                -- human-readable doc name
  remarks text,
  fiscal_year text NOT NULL,
  is_cancelled boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((debit = 0) OR (credit = 0)),       -- one side per row
  CHECK (debit >= 0 AND credit >= 0)
);
CREATE INDEX gl_entry_account_idx ON gl_entry (account_id, posting_date);
CREATE INDEX gl_entry_voucher_idx ON gl_entry (voucher_type, voucher_id);
CREATE INDEX gl_entry_party_idx   ON gl_entry (party_type, party_id) WHERE party_id IS NOT NULL;
```

`ledger.PostGL(ctx, tx, companyID, voucherType, voucherID, entries []Entry)`:
1. Sums debits and credits in base currency.
2. Rejects the transaction if `|sum_debit - sum_credit| > 0.005` (or rounds to base-currency precision).
3. Inserts all rows in one batch.
4. On cancel: re-posts each row with debit/credit swapped and `is_cancelled = true` flags on the *original* rows updated atomically.

Account balances are derived: `SELECT sum(debit-credit) FROM gl_entry WHERE account_id = ? AND posting_date <= ? AND is_cancelled = false`. A periodic-closing-snapshot table will be added in Phase 6 for performance — schema field is reserved, table is created in Phase 1 but populated only on demand.

### 3.9 Stock ledger

```sql
CREATE TABLE stock_ledger_entry (
  id text PRIMARY KEY,
  company_id text NOT NULL REFERENCES company(id),
  posting_datetime timestamptz NOT NULL,
  item_id text NOT NULL REFERENCES item(id),
  warehouse_id text NOT NULL REFERENCES warehouse(id),
  batch_no text,
  serial_no text,
  actual_qty numeric(18,6) NOT NULL,        -- signed
  qty_after_transaction numeric(18,6) NOT NULL,
  valuation_rate numeric(18,6) NOT NULL,    -- per-unit, in base currency
  stock_value numeric(18,4) NOT NULL,
  stock_value_difference numeric(18,4) NOT NULL,
  incoming_rate numeric(18,6),              -- for receipts; NULL for issues
  voucher_type text NOT NULL,
  voucher_id text NOT NULL,
  voucher_name text NOT NULL,
  is_cancelled boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sle_item_wh_idx ON stock_ledger_entry (item_id, warehouse_id, posting_datetime);
CREATE INDEX sle_voucher_idx ON stock_ledger_entry (voucher_type, voucher_id);
```

Valuation strategies (`FIFO`, `MovingAverage`, `LIFO`) implemented in `internal/platform/ledger/valuation/` as a strategy interface. Per-item override; FIFO default. The stock ledger and the GL (stock-in-hand account) must reconcile — a `ledger.ReconcileStockAndGL(ctx, companyID, asOf)` check is run nightly and on demand.

---

## 4. Phase 1 ERD (Accounting backbone)

Only the **shape** is shown here; full field-level specs land in `docs/modules/accounting/*.md` before implementation per phase.

### 4.1 Masters

```sql
CREATE TABLE company (
  id text PRIMARY KEY,
  name text NOT NULL UNIQUE,
  legal_name text NOT NULL,
  abbreviation text NOT NULL UNIQUE,            -- used as default series prefix
  country text NOT NULL DEFAULT 'ID',
  default_currency text NOT NULL DEFAULT 'IDR',
  npwp text,                                     -- 16-digit when present
  npwp_address text,
  ...address/contact cols...,
  default_receivable_account text,               -- FK installed after account table exists
  default_payable_account text,
  default_cost_center_id text,
  custom_fields jsonb NOT NULL DEFAULT '{}',
  audit cols
);

CREATE TABLE fiscal_year (
  id text PRIMARY KEY,
  name text NOT NULL UNIQUE,                     -- e.g. '2026'
  start_date date NOT NULL,
  end_date date NOT NULL,
  is_closed boolean NOT NULL DEFAULT false,
  CHECK (end_date > start_date)
);

CREATE TABLE fiscal_year_company (                -- which companies use this fiscal year
  fiscal_year_id text NOT NULL REFERENCES fiscal_year(id),
  company_id text NOT NULL REFERENCES company(id),
  PRIMARY KEY (fiscal_year_id, company_id)
);

CREATE TABLE currency (
  code text PRIMARY KEY,                          -- ISO 4217
  name text NOT NULL,
  symbol text NOT NULL,
  fraction text,
  enabled boolean NOT NULL DEFAULT true
);

CREATE TABLE currency_exchange_rate (
  id text PRIMARY KEY,
  from_currency text NOT NULL REFERENCES currency(code),
  to_currency text NOT NULL REFERENCES currency(code),
  rate numeric(18,8) NOT NULL,
  effective_date date NOT NULL,
  UNIQUE (from_currency, to_currency, effective_date)
);

CREATE TABLE cost_center (
  id text PRIMARY KEY,
  company_id text NOT NULL REFERENCES company(id),
  name text NOT NULL,
  parent_id text REFERENCES cost_center(id),
  lft int, rgt int,
  is_group boolean NOT NULL DEFAULT false,
  is_deleted boolean NOT NULL DEFAULT false,
  UNIQUE (company_id, name)
);

CREATE TABLE account (
  id text PRIMARY KEY,
  company_id text NOT NULL REFERENCES company(id),
  name text NOT NULL,                              -- e.g. 'Cash - PT XYZ'
  account_number text,
  parent_id text REFERENCES account(id),
  lft int, rgt int,
  is_group boolean NOT NULL DEFAULT false,
  root_type text NOT NULL CHECK (root_type IN ('asset','liability','equity','income','expense')),
  account_type text,                               -- 'receivable','payable','bank','cash','stock','tax','cogs','fixed_asset', etc.
  account_currency text NOT NULL,
  is_deleted boolean NOT NULL DEFAULT false,
  UNIQUE (company_id, name)
);

CREATE TABLE party (                               -- thin parent for Customer / Supplier / Employee for unified GL party handling
  id text PRIMARY KEY,
  party_type text NOT NULL CHECK (party_type IN ('customer','supplier','employee'))
);

CREATE TABLE customer (
  id text PRIMARY KEY REFERENCES party(id),
  name text NOT NULL UNIQUE,
  display_name text NOT NULL,
  customer_group_id text,
  territory_id text,
  default_currency text REFERENCES currency(code),
  default_price_list_id text,
  npwp text,
  ...contact/address...,
  is_deleted boolean NOT NULL DEFAULT false,
  custom_fields jsonb NOT NULL DEFAULT '{}',
  audit cols
);

CREATE TABLE customer_default (                    -- per-company defaults for shared Customer
  customer_id text NOT NULL REFERENCES customer(id),
  company_id text NOT NULL REFERENCES company(id),
  default_receivable_account_id text REFERENCES account(id),
  default_price_list_id text,
  default_currency text REFERENCES currency(code),
  PRIMARY KEY (customer_id, company_id)
);

CREATE TABLE supplier ( ... same shape ... );
CREATE TABLE supplier_default ( ... );
```

### 4.2 Tax engine

```sql
CREATE TABLE tax_category (
  id text PRIMARY KEY,
  name text NOT NULL UNIQUE
);

CREATE TABLE tax_template (
  id text PRIMARY KEY,
  company_id text NOT NULL REFERENCES company(id),
  name text NOT NULL,
  is_sales boolean NOT NULL,                       -- vs purchase
  UNIQUE (company_id, name)
);

CREATE TABLE tax_template_line (
  id text PRIMARY KEY,
  template_id text NOT NULL REFERENCES tax_template(id) ON DELETE CASCADE,
  account_id text NOT NULL REFERENCES account(id),
  description text NOT NULL,
  rate numeric(9,4) NOT NULL,                       -- e.g. 12.0000 for PPN
  charge_type text NOT NULL CHECK (charge_type IN ('on_net_total','on_previous_amount','actual')),
  included_in_base_rate boolean NOT NULL DEFAULT false,
  cost_center_id text REFERENCES cost_center(id),
  row_index int NOT NULL
);

CREATE TABLE item_tax (                              -- per-item tax overrides
  id text PRIMARY KEY,
  item_id text NOT NULL REFERENCES item(id),
  tax_category_id text REFERENCES tax_category(id),
  tax_template_id text REFERENCES tax_template(id),
  rate numeric(9,4)
);

CREATE TABLE withholding_tax_type (                  -- PPh 21/23/25/26 modelled as types
  id text PRIMARY KEY,
  name text NOT NULL UNIQUE,                         -- e.g. 'PPh 23'
  rate numeric(9,4) NOT NULL,
  account_id text NOT NULL REFERENCES account(id),
  threshold numeric(18,4),                           -- optional minimum applicability
  category text                                      -- 'individual' | 'entity' | NULL
);
```

### 4.3 Documents (Phase 1)

For each:
- Header table + child line table.
- `docstatus`, audit, naming series, custom fields.
- Submit posts to GL inside the helper.

**Journal Entry** — pure GL voucher. Header + lines (`account_id`, `party_type`, `party_id`, `debit`, `credit`, `cost_center_id`, `project_id`, `against_voucher_type`, `against_voucher_id`). Submit asserts balance and posts.

**Sales Invoice** — header (customer, posting_date, due_date, currency, exchange_rate, totals, taxes_and_charges_total, grand_total, paid_amount, outstanding_amount, naming `name`, tax_invoice_number for Faktur) + `sales_invoice_item` lines + `sales_invoice_tax` lines + `sales_invoice_withholding` lines. Submit posts: Dr Receivable, Cr Income (per item), Cr Tax Payable, optional Cr Withholding Payable.

**Purchase Invoice** — mirror of Sales Invoice. Submit posts: Dr Expense (or Stock-In-Hand if item is_stock_item), Dr Tax Recoverable, Cr Payable, optional Cr Withholding Payable on payment side.

**Payment Entry** — pays one or more invoices. Header + `payment_entry_reference` lines (target invoice + allocated amount) + `payment_entry_deduction` lines (withholding, fees). Submit posts: Dr/Cr Cash/Bank, settles Receivable/Payable, records withholding.

**Debit Note / Credit Note** — same shape as Purchase/Sales Invoice with `is_return=true` and `return_against` pointer; submit posts inverse entries.

**Bank Transaction** + **Bank Reconciliation** — staging table from bank statement import + matching workflow against `gl_entry` rows on bank accounts.

**Period Closing Voucher** — closes income/expense to retained earnings at year-end.

### 4.4 Reports (Phase 1, derived from `gl_entry`)

- General Ledger
- Trial Balance
- Balance Sheet (root_type-driven hierarchy)
- Profit & Loss
- Cash Flow (direct method using account_type tags)
- Accounts Receivable / Payable Ageing (party_type=customer/supplier buckets)
- Tax reports: VAT (PPN) summary, PPh withholding summary
- All accept `company_id`, `from_date`, `to_date`, `cost_center_id`, `project_id` filters

---

## 5. Phase 2 ERD (Inventory + Buying + Selling) — sketch

Only the table outlines; full spec lands when Phase 2 design opens.

**Stock masters.** `item` (global; `is_stock_item`, `has_variants`, `has_batch_no`, `has_serial_no`, `default_uom`, `stock_uom`, `valuation_method`), `item_default` (per-company overrides), `item_price` (price-list-keyed), `item_variant` + `item_attribute` for variants, `item_barcode`, `uom`, `uom_conversion`, `item_group` (tree), `brand`.

**Warehouse.** `warehouse` (tree, per-company), `warehouse_type`.

**Stock documents.** `stock_entry` (header) + `stock_entry_item` (lines) with `purpose` IN (`material_receipt`,`material_issue`,`material_transfer`,`manufacture`,`repack`). `delivery_note` + lines. `purchase_receipt` + lines. `stock_reconciliation` + lines. `material_request` + lines. `pick_list`. `landed_cost_voucher`.

**Buying.** `supplier` (global) + `supplier_default` (per-company). `request_for_quotation`, `supplier_quotation`, `purchase_order` + `purchase_order_item`.

**Selling.** `quotation`, `sales_order` + `sales_order_item`, `pricing_rule`, `promotional_scheme`, `sales_person` (tree), `territory` (tree).

**Document linkage.** A generic `doc_link` table tracks fulfilment chains:
```sql
CREATE TABLE doc_link (
  parent_doctype text NOT NULL,
  parent_id text NOT NULL,
  child_doctype text NOT NULL,
  child_id text NOT NULL,
  qty_linked numeric(18,6),
  amount_linked numeric(18,4),
  PRIMARY KEY (parent_doctype, parent_id, child_doctype, child_id)
);
```
Plus per-line `delivered_qty`, `billed_qty`, `received_qty`, `billed_amount` columns on the source documents — denormalized for fast "% delivered / billed" rendering, kept consistent inside the linking transaction.

---

## 6. API conventions

- Base path: `/api/v1`.
- Auth: `Authorization: Bearer <jwt>` on every call except `/auth/*` and `/healthz`/`/readyz`/`/metrics`.
- Active company: `X-Company-Id` header. Server validates against `user_company`. Missing header on multi-company users returns `400 missing_company`.
- Locale: `Accept-Language` honored; defaults to user profile, then `id-ID`.
- All write endpoints accept `Idempotency-Key` header; the platform stores `(user_id, key) -> response` for 24h.
- Resource shape: `/{module}/{doctype}` plural-singular convention: `/accounting/sales-invoices`, `/stock/items`.
- Standard endpoints per doctype:
  - `GET    /<doctype>`            list with `?filters=`, `?sort=`, `?page=`, `?page_size=`, `?fields=`
  - `POST   /<doctype>`            create draft
  - `GET    /<doctype>/{id}`       read one
  - `PUT    /<doctype>/{id}`       update draft
  - `DELETE /<doctype>/{id}`       delete draft (or soft-delete master)
  - `POST   /<doctype>/{id}/submit`
  - `POST   /<doctype>/{id}/cancel`
  - `POST   /<doctype>/{id}/amend` returns the new draft
- Filter syntax: `?filters=[["posting_date",">=","2026-01-01"],["customer_id","=","cust_01..."]]` (JSON-encoded list of triples).
- Errors: `{"error":{"code":"validation_failed","message":"...","fields":{"customer_id":"required"}}}`. Codes are stable; messages are localized.
- Pagination: cursor by default for large lists (`?cursor=`), offset for compatibility (`?page=`).
- All money in responses is in **transaction currency** with a sibling field for base currency (`amount`, `base_amount`).
- All datetimes are RFC 3339 in UTC; dates are `YYYY-MM-DD`.
- OpenAPI 3.1 is served at `/api/v1/openapi.json`; Stoplight Elements UI at `/api/v1/docs` in non-prod.

---

## 7. Frontend conventions

- TanStack Router file-based, layouts per module.
- TanStack Query for all server state; mutations call generated TS client (codegen'd from OpenAPI).
- TanStack Form for everything; one generic `<DocForm doctype="sales_invoice">` reads the metadata service and lays out the form, with module-specific overrides for hand-tuned UX (Sales Invoice line editor, POS screen, etc.).
- TanStack Table for all list views.
- Ark UI primitives + Tailwind tokens (CSS variables) for theming. Dense compound components.
- All text via `t('key')`; no inline literals. Lint enforced.
- Permission-aware components: `<Can action="submit" doctype="sales_invoice">…</Can>` reads the `/metadata/permissions` payload cached in React Query.
- Money inputs use a `<DecimalInput currency={txnCurrency} precision={2}>` component; floats are not passed around in TS state.

---

## 8. Testing strategy

| Layer | Approach |
|---|---|
| Pure math (decimal, FX, FIFO/MA/LIFO, tax calc) | Plain unit tests, table-driven. Property tests for the ledger balance invariant. |
| Repo / sqlc queries | Integration tests against a real Postgres via `testcontainers-go`. Schema migrated fresh per test package. |
| Service layer (submit/cancel cycle) | Integration tests. Asserted invariants: GL balanced, cancel produces inverse entries, stock+GL reconcile, custom-field validation, permission denial. |
| HTTP / Huma handlers | Integration with httptest, real DB, real perm engine. Snapshot tests for OpenAPI spec to catch unintended drift. |
| Frontend | Vitest + Testing Library for components; Playwright for one happy-path E2E per phase exit. |
| CI | GitHub Actions: lint + unit + integration on every PR; nightly E2E. |

Per-phase exit gate: full procure-to-pay / order-to-cash (or the phase's named flow) runs green in CI against seeded data.

---

## 9. Deferred / out-of-scope for v1

- Inter-company transactions (consolidation, IC eliminations).
- Live Coretax / DJP integration. (e-Faktur CSV/XML export only.)
- Live payment gateway integrations.
- Live WhatsApp messaging. (Interface only.)
- Multi-tenant isolation (single-tenant by decision).
- A fully dynamic schema engine. The JSONB custom-fields layer is the only dynamic part.
- Subcontracting depth, advanced manufacturing capacity planning beyond MVP — refined in Phase 4.

---

## 10. What lands when this doc is approved

1. Confirm or amend any of §0 selected decisions and §3–§5 schema choices.
2. Approve the repo layout in §1.
3. Then the agent scaffolds: `go.mod`, `cmd/api` skeleton, `migrations/0001_platform.sql` (users, sessions, roles, permissions, company, fiscal year, currency, naming series, custom field defs, audit, GL, SL), `deploy/docker-compose.dev.yml` (Postgres + Gotenberg + MinIO), `Makefile`, `.golangci.yml`, GitHub Actions CI, Vite/TanStack web shell with login screen — and lands the Phase 0 exit slice (login → create company → post balanced Journal Entry → GL reflects it → permissions deny a non-permitted user).

No code yet. Awaiting review.
