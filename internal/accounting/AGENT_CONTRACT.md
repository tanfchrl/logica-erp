---
module: accounting
display_name: "Modul Akuntansi"
version: "1"
documents:
  - name: customer
    display_name: "Customer"
    api_path: "/accounting/customers"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: supplier
    display_name: "Supplier"
    api_path: "/accounting/suppliers"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: item
    display_name: "Item"
    api_path: "/accounting/items"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: account
    display_name: "Account (Chart of Accounts)"
    api_path: "/accounting/accounts"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: []
    tier2_tools: []
  - name: tax_template
    display_name: "Tax Template"
    api_path: "/accounting/tax-templates"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: []
    tier2_tools: []
  - name: sales_invoice
    display_name: "Sales Invoice"
    api_path: "/accounting/sales-invoices"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: purchase_invoice
    display_name: "Purchase Invoice"
    api_path: "/accounting/purchase-invoices"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: journal_entry
    display_name: "Journal Entry"
    api_path: "/accounting/journal-entries"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: payment_entry
    display_name: "Payment Entry"
    api_path: "/accounting/payment-entries"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The Accounting module is the deterministic core of Logica ERP and posts to
  the General Ledger. Document flow is: master data (Customer, Supplier, Item,
  Account, Tax Template) feeds transactional documents (Sales Invoice,
  Purchase Invoice, Journal Entry, Payment Entry). The chart of accounts is
  PSAK-aligned with Indonesian tax accounts pre-populated (PPN Masukan/Keluaran
  and PPh 21/23/25/26 liabilities). Tax is always applied via tax templates —
  never compute PPN or PPh manually. The agent drafts in docstatus=0 only;
  ledger posting is exclusively a human submit action.
suggested_prompts:
  - "Tampilkan AR aging bulan ini"
  - "Sales Invoice yang overdue lebih dari 30 hari"
  - "Buat draft sales invoice untuk customer ini"
  - "Total pembelian dari supplier ini tahun ini"
  - "Cek payment entry yang belum di-reconcile minggu ini"
nudge_rules: []
---

# Modul Akuntansi

Modul Akuntansi adalah jantung deterministik dari Logica ERP. Semua dokumen
transaksional di modul ini pada akhirnya akan posting ke General Ledger ketika
disubmit oleh user (docstatus=1). Agent hanya membuat draft (docstatus=0) dan
tidak pernah melakukan submit secara otomatis.

## Master Data

- **Customer / Supplier**: data master pihak ketiga, termasuk NPWP dan alamat
  faktur. Wajib lengkap sebelum dipakai pada dokumen transaksional yang
  memerlukan e-Faktur.
- **Item**: katalog barang/jasa, dengan referensi ke akun pendapatan, akun
  HPP, dan default tax template.
- **Account**: Chart of Accounts berbasis PSAK. Struktur akun ditentukan saat
  setup awal dan jarang berubah di runtime — agent tidak membuat akun baru.
- **Tax Template**: kombinasi PPN Keluaran, PPN Masukan, PPh 21/23/25/26
  yang dipakai dokumen transaksional. Pembuatan tax template butuh judgement
  pajak — di luar scope copilot.

## Dokumen Transaksional

- **Sales Invoice**: penagihan ke customer. Posting Dr Piutang / Cr Pendapatan
  + PPN Keluaran ketika disubmit.
- **Purchase Invoice**: tagihan dari supplier. Posting Dr Beban/Aset + PPN
  Masukan / Cr Hutang ketika disubmit.
- **Journal Entry**: jurnal manual untuk koreksi, accrual, dan opening
  balance. Harus seimbang (debit = kredit) sebelum boleh disubmit.
- **Payment Entry**: penerimaan atau pengeluaran kas/bank, biasanya
  di-allocate ke Sales/Purchase Invoice yang outstanding.

## Aturan Bisnis Penting

- Semua dokumen mengikuti lifecycle `docstatus` (0=draft, 1=submitted,
  2=cancelled). Hanya dokumen submitted yang muncul di laporan keuangan.
- Period closing membekukan posting di periode yang sudah closed.
- Agent tidak boleh menghitung PPN/PPh manual — selalu pakai tax template
  yang sudah dikonfigurasi.
