# Admin & Settings

This is the comprehensive admin surface that lives at `/settings/*` in the SPA, backed by ~70 endpoints under `/api/v1/admin/*`.

## Information architecture

| Group | Section | Backend doctype | Key endpoints |
|---|---|---|---|
| **General** | Appearance | (client-side prefs) | — |
| | Localization | (client-side prefs + i18next) | — |
| | Companies | `company` | `/accounting/companies` |
| **Users & access** | Users | `user` | `/admin/users{,/{id},/{id}/{password,roles,companies,sessions}}` |
| | Roles & permissions | `role`, `role_permission` | `/admin/roles{,/{id}/permissions}` + `/admin/doctypes` |
| | Sessions & devices | `user_session` | `/admin/users/{id}/sessions` |
| | API tokens | `api_token` | `/admin/api-tokens{,/{id}}` |
| **Finance** | Fiscal years | `fiscal_year` | `/admin/fiscal-years{,/{id}}` |
| | Tax templates | `tax_template`, `tax_category`, `withholding_tax_type` | `/accounting/tax-{templates,categories}`, `/accounting/withholding-tax-types` |
| | e-Faktur / Coretax | (CSV export) | `/accounting/exports/efaktur` |
| | Payroll configuration | `payroll_setting` | `/admin/payroll-settings` |
| | Numbering series | `naming_series` | `/admin/naming-series{,/{id},/{id}/reset,/preview}` |
| **Documents** | Print templates | `print_template`, `letterhead` | `/admin/letterheads{,/{id}}`, `/admin/print-templates{,/{id},/doctypes,/bundled/{doctype},/preview}` |
| **Communications** | Email (SMTP) | `smtp_config`, `email_log` | `/admin/smtp{,/test}`, `/admin/email-log` |
| | Email templates | `email_template` | `/admin/email-templates{,/{id}}` + `/admin/email/events` |
| | Notification rules | `notification_rule` | `/admin/notification-rules{,/{id},/events}` |
| **Integrations** | Payment gateways · Bank feeds · Marketplaces | `connector_config` (kind discriminator) | `/admin/connectors{,/{id},/providers}` |
| | Webhooks | `webhook_subscription`, `webhook_delivery` | `/admin/webhooks{,/{id},/{id}/test,/events,/deliveries{,/{id}/replay}}` |
| **Automation** | Approvals inbox | `approval_request` | `/admin/approvals/{pending,resolved,by-doc/{doctype}/{id},{id}/{approve,reject}}` |
| | Workflows | `workflow`, `workflow_state`, `workflow_transition`, `approval_rule` | `/admin/workflows{,/{id},/{id}/{states,transitions},/doctypes}`, `/admin/approval-rules{,/{id}}` |
| | System health | (aggregated query) | `/admin/system/health` |
| **Data** | Import / Export | `import_job` | `/admin/imports/{recipes,preview,commit,jobs}` |
| | Backups | — | _(not yet built — see backlog)_ |
| **System** | Audit log | `document_audit` | `/admin/audit-log{,/facets}` |

## Approval engine

When a submittable doctype (`sales_invoice`, `purchase_invoice`, `payment_entry`, `journal_entry`) calls `Submit()`, the engine:

1. Reads active `approval_rule` rows for `(doctype, company)`
2. Evaluates each rule's condition against payload fields (e.g. `grand_total > 50000000`)
3. For each matching rule with no existing approved request:
   - INSERTs a pending `approval_request` row **in a separate transaction** (so it survives the caller's rollback)
   - Fires `approval.requested` to the notification dispatcher
   - Returns `ErrApprovalRequired` from `CheckSubmit`

4. The caller's `Submit()` returns an HTTP error to the user. Approvers see the pending request in `/settings/approvals` (or directly on the doc page via `<ApprovalWidget>`).

5. When an approver hits Approve / Reject:
   - `Decide()` updates status, records `decided_by` + `decided_at`
   - Fires `approval.decided` to the dispatcher
   - User re-submits the doc; this time `CheckSubmit` finds the approved request and allows submit to proceed

Rules are written in the UI at `/settings/workflows` → Approval rules. Common shape:

```yaml
name:             "PI over Rp 50M"
doctype:          purchase_invoice
condition_field:  grand_total
condition_op:     >=
condition_value:  "50000000"
required_role_id: <Finance Manager role id>
```

## Notification dispatcher

`Dispatcher.Fire(eventKey, payload)` is non-blocking. It spawns a goroutine with a fresh 30s context that:

1. Loads active `notification_rule` rows for `eventKey` matching the payload's company
2. Evaluates each rule's condition (same shape as approval rules)
3. Expands recipients (`user:<id>` → direct, `role:<id>` → all enabled users with that role)
4. Fans out across channels:
   - **in_app** → INSERT into the existing `notification` table
   - **email** → `email.Service.SendTemplated(eventKey, recipient_email, vars)` (renders `email_template` if one exists; otherwise default subject/body)
   - **whatsapp** → logged-only (transport not yet wired)

Events currently fired by the system:

| Event | Source |
|---|---|
| `invoice.issued` | `salesinvoice.Submit()` after commit |
| `invoice.payment_received` | `paymententry.Submit()` when type=receive |
| `payment.made` | `paymententry.Submit()` when type=pay |
| `approval.requested` | `ApprovalEngine.CheckSubmit()` when pending rows created |
| `approval.decided` | `ApprovalEngine.Decide()` after approve/reject |

Add more by passing a `Notifier` field to the relevant service struct (same pattern as Approvals).

## Webhook delivery contract

Outbound POST to the subscriber's URL with these headers:

```
Content-Type: application/json
User-Agent: Logica-ERP/0.1 webhooks
X-Logica-Event: invoice.issued
X-Logica-Signature: sha256=<hmac-sha256 of body, hex>
X-Logica-Delivery-Attempt: 1
```

Verify the signature server-side:

```js
const sig = req.headers['x-logica-signature'].replace('sha256=', '');
const expected = crypto.createHmac('sha256', SECRET).update(req.rawBody).digest('hex');
crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(expected));
```

Each attempt is recorded in `webhook_delivery` (queued / succeeded / failed) with the response code + error message. The UI has a Replay button that re-fires the original payload as a new attempt.

## API token format

`lt_<64 hex chars>` — generated server-side via `crypto/rand` + SHA-256-hashed before storage. Only the hash + first 8 plaintext chars are persisted; the plaintext is shown ONCE in the create response.

The bearer middleware accepts either a JWT or an `lt_…` token. Token validation:

- Hash is looked up in `api_token` where `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now()) AND user.enabled = true`
- On hit: load principal, fire-and-forget update `last_used_at = now()`
- On miss: `401 unauthenticated · invalid or expired api token`

## CSV import wizard

`/settings/import-export`. 5-step flow: pick doctype → upload CSV → map columns → validate → commit.

Each row is its own micro-transaction — partial success is captured. The validate step runs the same recipe code as commit but rolls back at the end, so users see precise per-row error messages before any data lands.

Built-in recipes:

| Recipe | Targets |
|---|---|
| Customers / Suppliers / Items | Standalone master records |
| Chart of accounts | Company-scoped; parent lookup by `account_number` |
| POS invoice — lines | Lines into existing draft POS invoices |
| Purchase invoice — lines | Multi-currency aware; expense account lookup; parent totals recomputed per row |
| Sales invoice — lines | Same shape as PI with income account |
| Journal entry — lines | Debit/credit XOR validation; party type/id pair validation; balance check still at submit |

Adding a new recipe is ~30 lines of Go — see `internal/platform/dataimport/recipes.go`.

## Honest limits

- **Workflow runtime engine** (`Engine.Apply`) exists but isn't yet called by doctype services. Only the approval engine gates submit today.
- **WhatsApp** transport unwired — notification rules with `channels: [whatsapp]` are logged-only.
- **Email delivery** needs both SMTP configured AND a matching `email_template` for the event; otherwise dispatcher logs the skip.
- **Backups section** is the only one that's still a stub — needs S3/B2 wiring outside the app.
- **API token scopes** field exists in DB but enforcement is always "full access". Per-scope filtering comes when scoped tokens are a real need.
- **Connector integrations** — credentials are stored end-to-end; per-provider SDK work (actually calling Midtrans, parsing BCA OFX, etc.) is each its own piece of work.
- **Approval widget** is mounted on SI + JE forms today; PI / PE forms don't have bespoke editors yet (use the generic create flow), so the widget hasn't been slotted there.
