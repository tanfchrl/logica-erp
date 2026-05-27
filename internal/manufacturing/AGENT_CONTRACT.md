---
module: manufacturing
display_name: "Modul Manufaktur"
version: "1"
documents:
  - name: bom
    display_name: "Bill of Materials (BOM)"
    api_path: "/manufacturing/boms"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: work_order
    display_name: "Work Order"
    api_path: "/manufacturing/work-orders"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The Manufacturing module covers the make-to-order and make-to-stock
  cycle. A Bill of Materials (BOM) defines the components and quantities
  needed to produce one unit of a finished good. A Work Order consumes
  raw materials per the BOM and produces finished goods, with downstream
  Stock Ledger impact on submit. The agent can read BOMs and Work Orders
  and draft new ones — actual production posting (material consumption,
  finished goods receipt) is human-submit only because it moves real
  inventory value.
suggested_prompts:
  - "BOM aktif untuk item finished good ini"
  - "Work Order yang masih in-progress lebih dari 7 hari"
  - "Buat draft work order untuk item ini sebanyak X unit"
  - "Buat draft BOM baru"
  - "Material requirement untuk work order minggu ini"
nudge_rules: []
---

# Modul Manufaktur

Modul Manufaktur mengelola produksi internal: dari resep produk (BOM)
sampai eksekusi produksi (Work Order).

## Bill of Materials (BOM)

- Mendefinisikan komponen (raw material + sub-assembly) dan kuantitas
  untuk menghasilkan satu unit finished good.
- Punya operasi (operations) opsional untuk hitung labor & overhead cost.
- Bisa multi-level: sub-assembly punya BOM sendiri.
- Hanya BOM yang `is_active = true` yang dipakai oleh Work Order.

## Work Order

- Eksekusi produksi: konsumsi raw material sesuai BOM, produksi finished
  good dalam jumlah `qty`.
- Lifecycle: Draft → In Progress → Completed → Closed.
- Pada submit, posting Stock Ledger: out untuk raw material, in untuk
  finished good. Selisih nilai jadi WIP / manufacturing variance.

## Aturan Bisnis

- Agent boleh draft BOM dan Work Order, tapi submit (yang memicu posting
  ke Stock Ledger dan GL) selalu human action.
- Perubahan BOM aktif yang sudah dipakai banyak Work Order historis harus
  hati-hati — biasanya buat BOM versi baru, bukan edit yang lama.
