---
module: support
display_name: "Modul Support"
version: "1"
documents:
  - name: issue
    display_name: "Issue / Tiket Support"
    api_path: "/support/issues"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The Support module captures customer-facing tickets: bug reports, feature
  requests, and how-to questions tied to a Customer record. Issues do not
  post to the ledger but they feed SLA reporting and inform downstream
  service revenue. The agent can list and triage issues and draft new
  tickets from user descriptions — closure and SLA escalation are operator
  decisions.
suggested_prompts:
  - "Issue yang belum di-assign hari ini"
  - "Tiket terbuka dari customer ini"
  - "Buat draft issue baru dari deskripsi ini"
  - "Issue yang SLA-nya akan terlewat dalam 24 jam"
  - "Top 5 customer dengan jumlah issue terbanyak bulan ini"
nudge_rules: []
---

# Modul Support

Modul Support menyimpan tiket customer sebagai dasar tracking SLA dan input
ke service quality metrics.

## Issue Lifecycle

1. **Open** — issue baru, belum di-assign.
2. **In Progress** — sudah di-assign ke agent support.
3. **Pending Customer** — menunggu balasan customer.
4. **Resolved** — solusi sudah diberikan, menunggu konfirmasi.
5. **Closed** — issue selesai.

## Aturan Bisnis

- Setiap issue terhubung ke satu Customer.
- SLA dihitung dari `created_at` sampai `resolved_at` atau `closed_at`.
- Agent boleh draft issue baru dan mengusulkan kategorisasi, tapi closure
  tetap aksi manual karena memengaruhi SLA report.
