---
module: crm
display_name: "Modul CRM"
version: "1"
documents:
  - name: lead
    display_name: "Lead"
    api_path: "/crm/leads"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: contact
    display_name: "Contact"
    api_path: "/crm/contacts"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: opportunity
    display_name: "Opportunity"
    api_path: "/crm/opportunities"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft, set_stage]
    tier2_tools: []
  - name: note
    display_name: "Note"
    api_path: "/crm/notes"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The CRM module manages the pre-sales pipeline. A Lead represents an
  unqualified contact captured from marketing channels, referrals, or
  inbound enquiries. The lifecycle is Lead → (qualification) → Customer
  conversion, after which downstream documents (Quotation, Sales Order,
  Sales Invoice) are owned by the Accounting / Selling flow. The agent
  helps surface stale leads, draft follow-up actions, and suggest
  conversion candidates — but never converts or submits without explicit
  human approval.
suggested_prompts:
  - "Lead yang belum di-follow up dalam 7 hari"
  - "Daftar lead dari sumber referral bulan ini"
  - "Buat draft lead baru untuk prospek ini"
  - "Lead mana yang paling potensial dikonversi minggu ini"
nudge_rules:
  - id: stale_leads_no_followup
    condition: stale_leads_no_followup
    message_template: "{count} Lead belum di-follow up dalam {days_threshold} hari."
    cta_label: "Lihat leads"
    cta_prompt: "Tampilkan lead yang belum di-follow up dalam 7 hari terakhir."
    priority: normal
  - id: opportunities_closing_soon
    condition: opportunities_closing_soon
    message_template: "{count} Opportunity dengan expected close dalam {days_window} hari ke depan."
    cta_label: "Lihat pipeline"
    cta_prompt: "Tampilkan opportunity yang expected close-nya dalam 14 hari ke depan."
    priority: normal
  - id: stale_opportunities
    condition: stale_opportunities
    message_template: "{count} Opportunity tidak diupdate lebih dari {days_threshold} hari."
    cta_label: "Lihat stale"
    cta_prompt: "Tampilkan opportunity yang stagnan, belum diupdate lebih dari 14 hari."
    priority: high
---

# Modul CRM

Modul CRM menangani siklus pre-sales sebelum prospek menjadi customer resmi
di sistem akuntansi. Fokus utama di v1 adalah pencatatan dan kualifikasi
Lead.

## Lead Lifecycle

1. **New** — lead baru masuk dari kanal apapun (form web, referral, event).
2. **Contacted** — sudah ada attempt komunikasi pertama.
3. **Qualified** — kebutuhan dan budget sudah valid.
4. **Converted** — dipromosikan menjadi Customer di modul Accounting.
5. **Lost** — tidak dilanjutkan dengan alasan tertulis.

## Aturan Bisnis

- Lead tidak posting ke ledger — tidak ada implikasi keuangan sampai
  dikonversi.
- Konversi Lead → Customer adalah tindakan eksplisit yang sebaiknya
  ditinjau manusia, terutama untuk memastikan NPWP dan data faktur lengkap.
- Agent boleh menyarankan follow-up dan membuat draft lead, tapi tidak
  melakukan konversi otomatis di v1.
