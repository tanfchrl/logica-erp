---
module: projects
display_name: "Modul Proyek"
version: "1"
documents:
  - name: project
    display_name: "Proyek"
    api_path: "/projects/projects"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The Projects module tracks engagements that span multiple transactions:
  client deliverables, internal initiatives, or cost-bucketed work. A
  Project groups related Sales Invoices, Purchase Invoices, and time
  entries under one identifier so margin and progress can be reported.
  The agent helps surface stale or over-budget projects and drafts new
  project records, but cost allocation rules and project closure remain
  human decisions because they affect revenue recognition.
suggested_prompts:
  - "Proyek aktif beserta progress dan budget"
  - "Proyek yang sudah lewat deadline tapi belum closed"
  - "Buat draft proyek baru"
  - "Margin proyek tertinggi kuartal ini"
  - "Proyek yang costnya sudah melebihi budget"
nudge_rules: []
---

# Modul Proyek

Modul Proyek di v1 berfungsi sebagai cost & revenue bucket untuk pekerjaan
yang melewati lebih dari satu dokumen transaksional. Cocok untuk perusahaan
service, konstruksi, atau internal initiative.

## Konsep

- Sebuah **Project** punya scope, tanggal mulai, tanggal target selesai,
  customer (opsional), dan budget total.
- Dokumen lain (Sales Invoice, Purchase Invoice, dll) bisa di-link ke
  Project untuk traceability.
- Status: Open, In Progress, On Hold, Closed.

## Aturan Bisnis

- Closing project membekukan posting biaya ke project tersebut.
- Margin proyek dihitung dari Σ Sales Invoice − Σ Purchase Invoice yang
  di-link, dibandingkan dengan budget.
- Agent boleh draft project baru, tapi tidak meng-close project — closing
  punya implikasi revenue recognition yang harus diaudit manusia.
