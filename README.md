# Logica ERP

Self-hosted, single-tenant, multi-company ERP for the Indonesian SME and mid-market. A product of **TAN Digital** (PT Teknologic Aksara Nusantara).

ERPNext v16 is the functional baseline (module list, document flows, accounting semantics). Logica ERP is an independent reimplementation on a modern stack — it does **not** port Frappe Framework, the DocType runtime, or Python code.

## Stack
- **Backend:** Go 1.23 · chi router wrapped by Huma v2 · pgx/v5 · goose migrations · River jobs · `shopspring/decimal` · `slog`
- **Database:** PostgreSQL 16
- **Frontend:** TanStack Router/Query/Table/Form · Vite · TypeScript strict · Tailwind · Radix UI · cmdk · framer-motion
- **PDF:** Gotenberg (Chromium HTML → PDF)
- **Deploy:** Docker Compose on a single VPS · Caddy reverse proxy

## Status

**Phases 0–6 backend complete.** Frontend shell + 17+ doctype list/create flows + 8 reports + comprehensive admin/settings surface live.

| Phase | Scope | Status |
|---|---|---|
| 0 | Foundation — auth, permissions, submittable lifecycle, GL/SLE ledgers, naming series, audit, custom fields | ✅ |
| 1 | Accounting backbone — COA, customers/suppliers/items, tax templates, SI/PI/PE/JE, period closing | ✅ |
| 2 | Stock + buying + selling — warehouses, stock entries, PO/SO | ✅ |
| 3 | CRM + projects — leads, projects/tasks/timesheets | ✅ |
| 4 | Manufacturing + assets — BOM, work orders, asset register | ✅ |
| 5 | HR & payroll + POS + helpdesk — employees, payroll (PPh21 TER + BPJS), POS, issues/SLA | ✅ |
| 6 | Hardening — print formats, e-Faktur CSV export, dashboards, workflows, crosscut (comments / attachments / notifications / search) | ✅ |
| 7 | **Admin & settings** — fiscal years, tax templates, numbering, SMTP, email templates, audit log, identity (users/roles/permissions), print templates + letterheads, workflows + approval engine, approvals inbox, import wizard, webhooks, API tokens, connectors (gateways/banks/marketplaces), notification rules, payroll config, e-Faktur runner, system health | ✅ |

See [`docs/`](docs/) for design documents per phase and the [admin & settings overview](docs/admin-settings.md).

## Indonesian specifics
- **Tax:** PPN (11%), PPh 21 (with TER tables A/B/C, versioned by effective date), PPh 22 / 23 / 26 / 4(2) withholding
- **BPJS:** Kesehatan (4% / 1% with salary cap), JHT (3.7% / 2%), JP (2% / 1% with cap), JKK + JKM (employer-only)
- **e-Faktur:** CSV export ready; direct Coretax DJP integration pending sandbox access
- **NPWP:** 16-digit format enforced
- **Currency:** IDR default, multi-currency aware (every line stores both transaction + base amounts)
- **Localization:** Bahasa Indonesia + English, Indonesian number/date formatting, default timezone `Asia/Jakarta`

## Quick start (dev)

```sh
cp .env.example .env
make up                   # postgres + gotenberg + minio
make migrate              # apply schema (currently 0001..0021)
make seed                 # bootstrap admin + demo company + Indonesian COA
make api                  # http://localhost:8080 — OpenAPI at /api/v1/docs
make web-install web-dev  # http://localhost:5173
```

Default admin is read from `LOGICA_BOOTSTRAP_ADMIN_*` in `.env`.

## Architecture highlights

- **Submittable doctype lifecycle:** `docstatus 0=Draft, 1=Submitted, 2=Cancelled`. Cancel posts offsetting GL entries — originals are never deleted.
- **4-layer permission engine:** RBAC → row-level (user_permission) → field-level (field_permission) → multi-company scoping.
- **ULID PKs** with type prefixes (`si_`, `pi_`, `usr_`, …) for sortable, debuggable IDs.
- **Append-only ledgers:** `gl_entry` + `stock_ledger_entry`. All reports derive from these, never from doc snapshots.
- **Approval engine:** rules evaluated at submit time; pending requests survive submit rollback (committed in a separate tx so requesters' inboxes are populated even when their submit fails).
- **Notification dispatcher:** async fan-out (in_app / email / whatsapp); rules match against event payload with the same condition shape as approvals.
- **Bearer auth** accepts both JWT and `lt_<hex>` API tokens; tokens stored only as SHA-256 hashes.
- **Generic admin patterns:** doctype-driven list + create UI from a single `DoctypeConfig` registry; only line-heavy docs (SI, JE) have bespoke forms.
- **CSV bulk import wizard** with per-doctype recipes covering customers/suppliers/items/COA + parent-child loaders for POS/PI/SI/JE lines.

## Repo layout

```
cmd/
  api/      — HTTP server entry point
  worker/   — River background worker
  logica/   — CLI (migrate, seed, backup, restore)

internal/
  platform/         — cross-cutting infra
    auth/           — JWT + argon2id + bearer middleware
    permission/     — 4-layer engine with per-call principal cache
    ledger/         — append-only GL + SLE posting
    submittable/    — Draft → Submitted → Cancelled state machine
    naming/         — "SI-.YYYY.-.####" patterns + admin CRUD
    audit/          — document_audit + filterable query API
    httpx/          — chi + Huma + auth middleware + JSON error mapping
    dbx/            — pgx pool + ULID gen + helpful error shims
    email/          — SMTP + per-event templates + send log
    print/          — Gotenberg renderer + DB-stored templates + letterheads
    workflow/       — state machine + approval engine + per-doc widget hooks
    identity/       — admin CRUD: users / roles / role_permission matrix / sessions
    apitokens/      — lt_<hash> personal access tokens (wired into Auth middleware)
    webhooks/       — HMAC-signed outbound delivery + log + replay
    connectors/     — generic credential store for gateways / banks / marketplaces / shipping
    notifrules/     — notification rule storage + async dispatcher
    sysinsights/    — failed-deliveries + stuck-approvals dashboard
    payrollconfig/  — BPJS rates + PPh21 TER table, versioned by effective_from
    dataimport/     — CSV bulk import wizard with per-doctype recipes

  accounting/       — company / account / customer / supplier / item / tax / SI / PI / PE / JE / period closing / fiscal year / e-Faktur / reports
  stock/            — warehouse / stock_entry
  crm/              — lead
  projects/         — project
  manufacturing/    — bom / work_order
  assets/           — asset
  hr/               — employee / payroll
  pos/              — pos_profile / pos_invoice
  support/          — issue + SLA

migrations/         — goose SQL, forward-only (0001..0021)
seed/               — bootstrap data (Indonesian COA, naming series, fiscal year)

web/                — TanStack SPA, Mintlify design language
  src/
    routes/                — page components (Dashboard, Items, generic ListView/CreateForm, bespoke SI/JE forms)
    routes/settings/       — 27 admin sections in one IA
    components/            — Button, Card, DataTable, PageHeader, ApprovalWidget, etc.
    shell/                 — AppShell, Sidebar, TopChrome, CommandPalette
    lib/                   — api fetch wrapper, auth, i18n, format, cn

deploy/             — docker-compose.yml, Caddyfile, install.sh, Dockerfiles
docs/               — design docs per phase + admin & settings overview
```

## API surface

OpenAPI spec at `/api/v1/openapi.json`. Interactive docs at `/api/v1/docs`. Approximate counts:
- **~150 doctype endpoints** across all modules (list / get / create / update / submit / cancel / amend / print)
- **~70 admin endpoints** under `/admin/*` — see [admin & settings overview](docs/admin-settings.md)
- **~7 reports** (trial balance, P&L, balance sheet, cash flow, AR/AP ageing, GL, PPN summary)

## Testing

```sh
make test          # go test ./...
make test-cover    # with coverage report
make web-typecheck # tsc --noEmit
make web-build     # vite build (production bundle)
```

## License

Proprietary — © TAN Digital (PT Teknologic Aksara Nusantara).
