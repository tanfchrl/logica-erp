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
nudge_rules: []
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
