---
module: stock
display_name: "Modul Stock"
version: "1"
documents:
  - name: warehouse
    display_name: "Warehouse / Gudang"
    api_path: "/stock/warehouses"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The Stock module models physical inventory locations and (in later
  phases) stock movements posting to the Stock Ledger. In v1 the agent
  interacts with the Warehouse master only — listing, querying, and
  drafting new warehouses. Stock movements, transfers, and valuation
  posting are out of scope for the copilot until those documents land.
  Warehouses are linked to the General Ledger via their inventory account
  for stock valuation.
suggested_prompts:
  - "Daftar warehouse aktif"
  - "Warehouse untuk cabang tertentu"
  - "Buat draft warehouse baru"
  - "Warehouse mana yang belum terhubung ke akun inventory"
nudge_rules: []
---

# Modul Stock

Modul Stock di v1 menyediakan Warehouse master sebagai prasyarat untuk
stock movement (Stock Entry, Delivery Note, Purchase Receipt) yang akan
hadir di fase berikutnya.

## Warehouse

Setiap warehouse punya:

- Nama dan kode unik per company.
- Lokasi fisik (alamat, kota).
- Inventory account di Chart of Accounts untuk posting nilai persediaan
  ke General Ledger.
- Parent warehouse (struktur hierarkis untuk multi-cabang).

## Aturan Bisnis

- Warehouse tanpa inventory account tidak boleh dipakai di dokumen yang
  memposting Stock Ledger.
- Agent boleh draft warehouse baru, tapi assignment inventory account
  butuh judgement akuntansi.
