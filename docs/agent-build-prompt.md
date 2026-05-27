# Build Prompt — Logica ERP Agentic Layer

> **How to use this document.** Hand this to an AI coding agent (e.g. Claude Code) **after** Logica ERP Phase 6 (Hardening) is complete and the OpenAPI spec is stable. This prompt is self-contained: it specifies the full agentic layer to be built on top of the running Logica ERP platform. Do not start this work before the ERP core API is stable — the agent service is an API client and cannot be built without a complete API to wrap.
>
> **Staying current.** This prompt is designed to remain accurate as new ERP modules ship. The agent service is architecturally self-updating: it discovers capabilities at runtime from `AGENT_CONTRACT.md` files (§7) and the living OpenAPI spec. Adding a new module to the ERP does not require editing this document — the module developer writes an `AGENT_CONTRACT.md` and the agent service picks it up automatically.
>
> **Prerequisite:** `logica-erp-build-prompt.md` Phases 0–6 complete. The ERP REST API is running, the OpenAPI spec at `GET /api/v1/openapi.json` is accurate and complete, and every module has an authored `AGENT_CONTRACT.md` (see §7).

---

> **Scope of this section.** This section specifies the design, safety model, architecture, and build instructions for the two agentic features validated in the UX prototype: the **Implementation & Migration Agent** (Goal 1) and the **Transactional Copilot** (Goal 2). It is written to remain accurate as the product evolves — see §7 for the self-updating contract mechanism.

---

### 1. The Governing Principle (immutable)

The agent never posts to the General Ledger or Stock Ledger. It **drafts and proposes**; the deterministic ERP core **validates and posts**. Every state-changing action the agent takes goes through the exact same REST API, permission middleware, and `submittable` document lifecycle that a human user uses. There is no privileged "agent path."

The practical consequence: the worst thing an agent can do is create a bad *draft* (`docstatus=0`). That draft has zero ledger impact. A human (or a configured policy rule) decides whether to submit it.

This is not a temporary constraint — it is the permanent product philosophy. Agent autonomy may expand over time (see §5), but never by bypassing the API or the document lifecycle.

---

### 2. Autonomy Tiers (the Policy Gate)

Every tool the agent can call is classified into one of three autonomy tiers. The classification is stored in the `AGENT_CONTRACT.md` for each document type (§7) and enforced server-side in the Policy Gate middleware.

| Tier | Name | What the agent can do | Human approval required? |
|---|---|---|---|
| 0 | **Read / Advise** | Query documents, run reports, explain data, surface insights and nudges. Zero write operations. | Never |
| 1 | **Draft** | Create or update documents in `docstatus=0`. No ledger posting. Reversible at zero cost. | No (but shown in approval queue for visibility) |
| 2 | **Submit** | Submit a document (`docstatus=1`), which posts to GL/Stock Ledger. | **Always** — goes through the human-in-the-loop approval queue. Never auto-submits. |

**v1 ceiling: Tier 1.** The agent in v1 operates at Tier 0 (reads) and Tier 1 (drafts) only. Tier 2 (auto-submit) is architecturally supported but policy-disabled in v1. It can be enabled per document type, per company, via admin configuration in a future release — after the agent's draft accuracy has been validated by audit log data.

**Policy Gate enforcement:** a `PolicyGate` middleware in the agent service intercepts every tool call before execution. It checks: the tool's tier, the acting user's permissions, any configured value thresholds (e.g. "Tier 1 allowed for POs under Rp 50 juta"), and whether the target company has enabled the tier. Violations are logged and rejected — the agent receives a structured error, not a silent failure.

---

### 3. Architecture

The agent layer is a **separate service** — a distinct Go binary (`/cmd/agent`) running in its own Docker container. It is an API client of the ERP core. It has no direct database access except to its own dedicated tables (`agent_audit_log`, `agent_conversation`, `agent_staging_*`). It cannot import or reference internal ERP packages directly; it communicates exclusively via the public REST API.

```
┌──────────────────────────────────────────────────────────┐
│                     Browser / Client                      │
└────────────────────────┬─────────────────────────────────┘
                         │  WebSocket (streaming)
                         │  REST /api/agent/v1
┌────────────────────────▼─────────────────────────────────┐
│                    Agent Service                          │
│                                                           │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────────┐ │
│  │ Conversation │  │ Tool Registry│  │  Policy Gate    │ │
│  │   Manager   │  │  (dynamic,   │  │  (tier check,   │ │
│  │             │  │  from API)   │  │  value limits,  │ │
│  └──────┬──────┘  └──────┬───────┘  │  company scope) │ │
│         │                │          └────────┬────────┘ │
│  ┌──────▼────────────────▼──────────────────▼────────┐  │
│  │              Orchestration Engine                  │  │
│  │     (ReAct loop: reason → tool call → observe)    │  │
│  └──────────────────────┬─────────────────────────────┘  │
│                         │                                 │
│  ┌──────────────────────▼──────┐  ┌────────────────────┐ │
│  │       LLM Gateway           │  │  Agent Audit Log   │ │
│  │  (LiteLLM → Anthropic API   │  │  (Postgres, own    │ │
│  │   or Ollama local fallback) │  │   tables, append-  │ │
│  └─────────────────────────────┘  │   only, immutable) │ │
│                                   └────────────────────┘ │
└──────────────────────────┬───────────────────────────────┘
                           │  REST calls (same as any user)
┌──────────────────────────▼───────────────────────────────┐
│              ERP Core API  (/api/v1)                      │
│   — permission middleware, document lifecycle, GL/SL      │
└──────────────────────────────────────────────────────────┘
```

**LLM Gateway (LiteLLM):** a sidecar container. The agent service calls LiteLLM's OpenAI-compatible endpoint. The model is configured via environment variables (`LITELLM_MODEL`, `ANTHROPIC_API_KEY`, `OLLAMA_BASE_URL`). Default model: `claude-sonnet-4-20250514`. Local fallback: `ollama/qwen2.5:14b` or `ollama/llama3.1:8b`. Switching models requires no code changes.

**Tool Registry:** at startup, the agent service calls `GET /api/v1/openapi.json` and `GET /api/v1/agent/contracts` to build its tool list. Tools are not hardcoded. When a new module ships and its `AGENT_CONTRACT.md` is authored (§7), the tools become available automatically on next agent service restart — no changes to the agent service itself.

**Conversation store:** each user session has a conversation in `agent_conversation` (Postgres). Full message history is maintained per session. Conversation context is injected into each LLM call. Context window management: summarise older turns when approaching model limits.

**Agent identity:** the agent service authenticates to the ERP API as the *acting user* — it uses the user's own JWT, forwarded from the browser. The agent never has a super-user credential. It literally cannot see or do anything the user cannot.

---

### 4. Goal 1 — Implementation & Migration Agent

The migration agent is a **stateful, multi-step workflow** that guides a new customer through ERP setup. It runs in a dedicated "Setup" UI (separate from the main shell), accessible only during the onboarding phase or explicitly from Settings by an admin.

#### Workflow steps (in order)

**Step 1 — Discovery Interview.** A conversational intake. The agent asks about: business type, industry, number of employees, modules needed, multi-company structure, fiscal year start, base currency, and legacy system. It does *not* present a settings form; it conducts a conversation and maps responses to configuration objects. Output: a `SetupProfile` struct stored in Postgres, used to drive all subsequent steps.

**Step 2 — Chart of Accounts Proposal.** The agent generates a proposed COA from the `SetupProfile`: correct account hierarchy, Indonesian PSAK-aligned structure, pre-populated tax accounts (PPN Masukan/Keluaran, PPh liability accounts for types 21/23/25/26), and industry-specific accounts for the detected sector. Output is presented as a reviewable, editable table — not applied until the user accepts. On accept, the agent calls `POST /api/v1/account/bulk-create` (a Tier 1 tool, batch draft). No GL entries are made at this step.

**Step 3 — Data Migration (staging pipeline).** The user uploads legacy data files (CSV, XLSX — customers, suppliers, items, open invoices, opening stock). The agent:
1. Profiles each file: detected columns, row count, sample values.
2. Proposes column→field mappings using LLM classification.
3. Runs deterministic validation against ERP field rules (NPWP format, required fields, currency codes, duplicate detection).
4. Produces a **Data Quality Report**: critical errors (must fix), warnings (review recommended), info (auto-handled). The LLM explains each issue in plain Bahasa Indonesia.
5. Staging data lives in `agent_staging_*` tables — isolated from production data until explicitly committed.
6. User resolves issues (the agent suggests fixes for each category), then approves the batch. Agent calls the bulk-import API to commit. All staging data is committed in a single database transaction; failure rolls back entirely.

**Step 4 — Opening Balances.** The user provides a trial balance (the closing balances from their old system). The agent:
1. Maps trial balance accounts to the new COA (LLM-assisted, user-confirmable).
2. Creates draft Journal Entries for opening balances.
3. **Runs the reconciliation proof:** verifies total debits = total credits in the draft entries, and that the opening stock value matches the Inventory account balance. If they don't reconcile, the agent explains the gap in plain language and suggests which line to investigate.
4. On user approval, the Journal Entries are submitted (Tier 2 — this is one of the very few auto-submit flows permitted, because opening-balance JEs are by nature a one-time setup action and are always human-reviewed before approval).

**Step 5 — Go-Live Readiness.** A structured checklist the agent evaluates against the current system state: COA complete (all required account types present), tax templates configured, at least one warehouse defined, user accounts and roles created, opening balances reconciled (debit = credit), print format for invoice configured, NPWP fields filled on Company master. For each failing item, the agent explains what is missing and links directly to the relevant settings screen. Output: a readiness score and a printable/shareable report.

#### Migration agent constraints
- All file uploads are scanned for size (max 50 MB) and type (CSV/XLSX only) before processing.
- The staging area is isolated. No staging record affects production GL or stock until committed.
- The agent never commits staging data without explicit user approval on the Data Quality Report screen.
- Opening balance JEs are the only Tier 2 action permitted for the migration agent, and only after the reconciliation proof passes.
- All steps are resumable — a setup session persists in Postgres and can be continued across browser sessions.

---

### 5. Goal 2 — Transactional Copilot

The copilot is the in-app, day-to-day agent. It is available in the main ERP shell via:
- The **Copilot panel** — a persistent right-side panel on every module page (collapsible).
- The **⌘K command palette** — "AI actions" section, visually distinct from navigation and deterministic actions.
- **Ambient nudges** — proactive, dismissible suggestion bars that appear when the agent detects an actionable pattern (overdue invoices, pending approvals, unreconciled transactions).

#### Capability tiers at launch

**Tier 0 — Conversational query (always on).** Natural-language queries over the user's own data. Examples: "AR aging untuk bulan ini", "invoice mana yang overdue lebih dari 30 hari", "total pembelian dari supplier X tahun ini". Implementation: the agent translates the query to a structured request against the report builder API — it picks a doctype, filters, group-by, and sort from the metadata. It **never generates raw SQL**. The response is returned as formatted data plus a natural-language summary.

**Tier 0 — Explanation & guidance.** "Jelaskan cara kerja PPh 23 di sistem ini", "kenapa journal entry ini tidak balance?", "apa artinya status Overdue di invoice ini?". The agent answers from its knowledge of the ERP's own data model (injected as system context) and the user's actual document data where relevant.

**Tier 0 — Proactive nudges.** The agent service runs a background job (every 15 minutes via River) that evaluates a set of nudge rules against the current data and writes pending nudges to `agent_nudge` table. The frontend polls for nudges and displays them as dismissible bars. Nudge rules are defined in Go — not LLM-generated — for predictability. Examples: "2 invoices jatuh tempo hari ini", "3 PO menunggu approval", "bank reconciliation belum dilakukan sejak 7 hari". The LLM is only used to phrase the nudge text naturally.

**Tier 1 — Document drafting.** The copilot can draft documents on instruction. Examples: "buat sales invoice untuk PT Mitratel dari delivery note DN-0234", "buat purchase order ke supplier X untuk item-item yang stoknya di bawah reorder point". The agent queries the relevant source documents, assembles the draft payload, and calls `POST /api/v1/{doctype}` with `docstatus=0`. The resulting draft is surfaced in the Copilot panel with a "Review & Open" button. The user opens, reviews, and submits manually. The agent never submits.

**Tier 1 — Bulk draft actions.** "Buat payment reminder email untuk semua invoice overdue bulan ini." The agent creates multiple Communication Log drafts. Each is shown in the approval queue with a bulk-approve action.

#### What the copilot explicitly does NOT do in v1
- Auto-submit any ledger-posting document (Sales Invoice, Purchase Invoice, Payment Entry, Journal Entry, Stock Entry). This is a hard policy, not a configuration option.
- Answer questions about data outside the acting user's permission scope.
- Generate SQL queries or call any endpoint not in the tool registry.
- Retain memory across separate user sessions beyond what is stored in `agent_conversation` (no implicit "you mentioned last week…" without explicit session context).

#### Approval queue
Every Tier 1 draft created by the agent is also written to `agent_approval_queue`. The approval queue is accessible from the home dashboard ("AI Drafts menunggu review") and from the ⌘K palette. Approving a draft from the queue opens the document form pre-loaded — the human clicks Submit themselves. The queue entry records which agent session created the draft, the prompt that triggered it, and the timestamp. This data feeds the accuracy audit that will justify future Tier 2 enablement.

---

### 6. Frontend UX — Agent Surfaces

These surfaces extend §6 and follow the same design system (indigo accent for the shell, **violet/purple accent for all agent UI** — visually distinct so users never confuse an AI action with a deterministic one).

#### Copilot panel
- Persistent right-side panel (320px), collapsible to a tab indicator.
- Header: violet pulse indicator, "Logica AI Copilot" label, model name, session ID.
- **Quick actions bar:** context-sensitive chips generated from the active module's `AGENT_CONTRACT.md` `suggested_prompts` field (§7). Chips update when the user navigates to a new module — they are not hardcoded in the frontend.
- **Chat area:** message thread, agent and user turns, typing indicator, agent response rendered as markdown with inline document references (clickable, open the document in a new tab).
- **Inline proposals:** agent-created draft documents surface as compact cards inside the chat — document type, name, key amounts, status "Draft". A "Review & Submit" button opens the document form.
- **Input:** text input, send on Enter, Shift+Enter for newline.

#### ⌘K command palette — AI section
- "Tindakan AI" group, visually separated (violet background on active row vs indigo for navigation).
- Items sourced from: (a) static well-known agent actions (AR aging, overdue summary), (b) `suggested_prompts` from the current module's contract, (c) recent agent sessions.
- Keyboard shortcut hints on every item.
- Selecting an AI action sends the prompt to the copilot panel and opens it if collapsed.

#### Ambient nudge bar
- Appears below the page toolbar, above the data table — never in the middle of content.
- One nudge at a time (highest priority). A "See all" expands to a list.
- Dismissible per nudge (persisted — dismissed nudges don't reappear unless the data state changes).
- CTA button triggers the copilot with a pre-formed prompt (e.g. clicking "Tindak lanjuti" on an overdue nudge sends "Buat payment reminder untuk [invoice list]" to the copilot).

#### Setup wizard (migration agent)
- A separate full-screen flow — no sidebar, minimal chrome, maximum focus.
- Left panel: step progress tracker (steps 1–5), animated progress bar, current step highlighted.
- Main area: conversational chat plus structured proposal cards (COA table, data quality report, reconciliation summary, readiness checklist).
- Input: chat textarea with send button.
- The proposal cards are interactive: accept, edit, reject. Accepting a COA proposal or a staging commit triggers the API call.
- The setup wizard is accessible from Settings → Implementation Wizard (admin only) even after go-live, to support re-runs or company additions.

---

### 7. The Self-Updating Contract — `AGENT_CONTRACT.md`

**This is the mechanism that keeps the agentic layer relevant as the product evolves.**

Every module in `/internal/<module>/` must contain an `AGENT_CONTRACT.md` file. This file is the single source of truth for what the agent can do with that module. When a new module ships, its developer writes this file. The agent service reads all `AGENT_CONTRACT.md` files at startup (they are embedded in the binary via `go:embed`) and registers the declared tools automatically.

The contract format is structured YAML front-matter followed by descriptive prose. A developer shipping a new module only has to fill in this file — no changes to the agent service are needed.

```yaml
---
# AGENT_CONTRACT.md — mandatory for every module
# Read by the agent service at startup to register tools and context.

module: selling
display_name: "Modul Penjualan"
version: "1"   # increment when breaking changes occur

# Documents in this module the agent may interact with
documents:
  - name: sales_order
    display_name: "Sales Order"
    api_path: "/api/v1/sales-order"
    tier0_tools:            # read / query / explain
      - list_with_filters
      - get_by_id
      - get_fulfilment_status
      - run_report:aged_receivables
    tier1_tools:            # create/update draft only
      - create_draft
      - update_draft_line_items
      - create_draft_invoice_from_order  # linked-document creation
    tier2_tools: []         # auto-submit — disabled in v1; list here for future use

  - name: sales_invoice
    display_name: "Sales Invoice"
    api_path: "/api/v1/sales-invoice"
    tier0_tools:
      - list_with_filters
      - get_by_id
      - get_payment_status
    tier1_tools:
      - create_draft
      - create_draft_reminder_communication
    tier2_tools: []

# Context injected into the agent's system prompt when this module is active
system_context: |
  The Selling module manages the order-to-cash cycle: Quotation → Sales Order
  → Delivery Note → Sales Invoice → Payment Entry. When a Sales Order is fully
  delivered, the agent should proactively suggest creating a Sales Invoice.
  Always check `fulfilment_pct` and `billing_pct` before drafting invoices.
  Currency is IDR by default; check `currency` field for multi-currency orders.
  Tax is applied via `taxes_and_charges` template — never calculate tax manually.

# Suggested quick-action prompts surfaced in the Copilot panel and ⌘K
suggested_prompts:
  - "Tampilkan SO yang sudah dikirim tapi belum ditagih"
  - "Buat sales invoice dari SO terbaru untuk customer ini"
  - "Analisis pipeline penjualan bulan ini"
  - "Cek overdue receivables dan buat reminder"

# Nudge rules evaluated by the background nudge job
nudge_rules:
  - id: so_delivered_not_invoiced
    condition: "sales_order.fulfilment_pct >= 100 AND sales_order.billing_pct < 100 AND age_days > 3"
    message_template: "{count} Sales Order sudah dikirim penuh tapi belum ditagih."
    cta_label: "Buat Invoice"
    cta_prompt: "Buatkan draft sales invoice untuk SO yang sudah fully delivered"
    priority: high

  - id: overdue_invoices
    condition: "sales_invoice.status = 'Overdue'"
    message_template: "{count} Sales Invoice melewati jatuh tempo (total {amount_idr})."
    cta_label: "Tindak lanjut"
    cta_prompt: "Buat payment reminder untuk semua invoice overdue"
    priority: high
---

**# Module description for agent context**
[Descriptive prose about the module's purpose, document flows, key business rules,
and anything an agent needs to know to behave correctly. Written by the module
developer. The agent service injects this as part of its system context when the
user is active in this module.]
```

**Contract validation:** a CI check (`make agent-contract-lint`) validates every `AGENT_CONTRACT.md` against its JSON schema on every commit. A module that ships without a valid contract will fail CI. This is the enforcement mechanism.

**API endpoint:** `GET /api/v1/agent/contracts` returns the merged, validated contracts for all modules as JSON. The agent service calls this at startup and on a periodic refresh (every 10 minutes). This means deploying an updated contract requires only a code deploy — no agent service restart.

**Adding a new module:** the module developer fills in the `AGENT_CONTRACT.md`, passes CI, and deploys. The agent service picks up the new tools on next refresh. No changes to the agent service, the prompt, or this document.

---

### 8. Agent Audit Log (non-negotiable)

The `agent_audit_log` table is append-only and immutable. It records:

| Column | Description |
|---|---|
| `id` | UUID |
| `session_id` | agent conversation session |
| `user_id` | acting user (the human, not a service account) |
| `company_id` | company scope |
| `turn` | sequential turn number within session |
| `event_type` | `prompt`, `tool_call`, `tool_result`, `proposal`, `human_approved`, `human_rejected`, `policy_blocked` |
| `payload` | JSONB — full content for each event type |
| `model` | LLM model used |
| `tokens_in` / `tokens_out` | token counts for cost tracking |
| `latency_ms` | LLM call duration |
| `created_at` | immutable timestamp |

This log is the product's trust foundation. It is exposed in the admin UI under Settings → AI Audit Log. Filtering by user, session, event type, and date range. Export to CSV. It is never deletable from the UI — only via a manual database operation with a documented process.

**Token cost tracking:** a summary view aggregates `tokens_in + tokens_out` by day/user/model and displays estimated cost (LiteLLM reports costs per call). This drives the per-client AI usage analytics that inform pricing.

---

### 9. Agent Deployment

Add to `docker-compose.yml`:

```yaml
agent:
  build:
    context: .
    dockerfile: Dockerfile.agent       # multi-stage, distroless final
  restart: unless-stopped
  environment:
    - DATABASE_URL=${DATABASE_URL}     # agent's own tables only
    - ERP_API_BASE=http://api:8080     # internal network call to ERP core
    - LITELLM_BASE_URL=http://litellm:4000
    - AGENT_JWT_SECRET=${JWT_SECRET}   # same secret, to validate forwarded tokens
  depends_on:
    - api
    - litellm
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost:8090/healthz"]

litellm:
  image: ghcr.io/berriai/litellm:main-latest
  restart: unless-stopped
  environment:
    - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
    - OLLAMA_API_BASE=${OLLAMA_BASE_URL:-""}   # empty = Anthropic only
  volumes:
    - ./deploy/litellm_config.yaml:/app/config.yaml
  command: ["--config", "/app/config.yaml", "--port", "4000"]
```

`deploy/litellm_config.yaml` declares the model routing: Anthropic as primary, Ollama as fallback (if `OLLAMA_BASE_URL` is set). The agent service is unaware of which model is actually serving — it only calls LiteLLM.

For clients who require fully on-premise LLM inference, provide `docker-compose.llm.yml` as an additive override that adds an Ollama container. Document that this requires a VPS upgrade to at least 8 GB RAM (16 GB recommended for a 14B model). This is opt-in and explicitly not the default.

---

### 10. Agent Quality & Testing

- **Unit tests:** every tool wrapper function is unit-tested with a mocked API client.
- **Policy gate tests:** exhaustive test matrix — every tool × every tier × every edge case (value threshold, company scope, missing permission). The gate must be tested to be trusted.
- **Conversation tests:** golden-file tests for key agent flows (migration step 1→2, overdue nudge → draft reminder). LLM calls are mocked in tests; the orchestration logic is tested deterministically.
- **Reconciliation proof tests (migration agent):** the opening-balance reconciliation must be tested with intentionally broken inputs (unbalanced trial balances, missing account mappings, zero amounts). The agent must surface the correct error in each case.
- **Audit log completeness test:** an integration test that runs a full agent session and asserts that every prompt, tool call, and result produced a corresponding audit log entry.
- **No LLM calls in CI:** all tests mock the LiteLLM gateway. Real LLM calls are only made in staging/production. This keeps CI fast and free.

---

### 11. Agent "Do Nots" (reinforcing §11)

- Do not give the agent direct database access. It uses the REST API, period.
- Do not create a privileged agent service account with elevated permissions. The agent acts as the user.
- Do not allow auto-submit of ledger-posting documents in v1. This is a hard product decision.
- Do not hardcode document type names, field names, or module names inside the agent service. They come from the contract registry.
- Do not use the LLM to generate or interpret raw SQL. All queries go through the report-builder API.
- Do not build separate agent-specific report queries. The agent uses the same report builder every human user uses — same permissions, same data access.
- Do not retain personally identifiable data from conversations beyond what is needed for the audit log. Conversation history is session-scoped; old sessions may be purged after a configurable retention period (default: 90 days).
- Do not ship the agent service before Phase 6 is complete. The agent is only as good as the API it wraps.
