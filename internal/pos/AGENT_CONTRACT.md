---
module: pos
display_name: "Modul POS"
version: "1"
documents:
  - name: pos_invoice
    display_name: "POS Invoice"
    api_path: "/pos/invoices"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The POS module handles point-of-sale transactions for retail flows. A
  POS Invoice is a streamlined Sales Invoice plus an immediate Payment
  Entry, posting both revenue and cash receipt to the General Ledger on
  submit. The agent helps cashiers and managers query historical POS
  activity (daily totals, top items, returns) and draft new POS invoices.
  Submission stays a human action — every POS Invoice that submits posts
  to the Stock Ledger and General Ledger.
suggested_prompts:
  - "Total transaksi POS hari ini"
  - "Top 10 item terlaris minggu ini"
  - "POS Invoice yang di-void hari ini"
  - "Buat draft POS invoice untuk transaksi ini"
  - "Rata-rata nilai transaksi POS per cashier bulan ini"
nudge_rules: []
---

# Modul POS

Modul POS adalah varian ringan dari Sales Invoice yang dioptimalkan untuk
transaksi retail tunai/kartu di counter.

## POS Invoice

- Menggabungkan penjualan dan penerimaan pembayaran dalam satu dokumen.
- Wajib link ke warehouse default cashier dan POS profile yang
  mendefinisikan akun kas/bank tujuan.
- Pada submit posting: Dr Kas/Bank / Cr Pendapatan + PPN Keluaran, dan
  Stock Ledger out untuk item yang dijual.

## Aturan Bisnis

- Mode offline (kalau ada) tetap menyimpan transaksi sebagai draft sampai
  konektivitas pulih.
- Void POS Invoice butuh permission khusus — agent tidak melakukan void.
- Agent boleh draft POS Invoice (misal dari list barang di chat), tapi
  cashier yang submit di terminal POS.
