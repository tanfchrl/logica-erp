---
module: assets
display_name: "Modul Fixed Assets"
version: "1"
documents:
  - name: asset
    display_name: "Fixed Asset"
    api_path: "/assets/assets"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: asset_category
    display_name: "Asset Category"
    api_path: "/assets/asset-categories"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
  - name: asset_movement
    display_name: "Asset Movement"
    api_path: "/assets/asset-movements"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The Assets module tracks fixed assets — capitalized items subject to
  depreciation. Each Asset has an acquisition cost, useful life, salvage
  value, and depreciation method (straight-line in v1). Monthly
  depreciation runs post to the General Ledger via automated Journal
  Entries. The agent can list, query, and draft new asset records, but
  disposal, revaluation, and the actual depreciation run are deterministic
  / human-driven workflows because they trigger ledger postings.
suggested_prompts:
  - "Daftar fixed asset per kategori"
  - "Asset yang akan fully depreciated dalam 12 bulan"
  - "Buat draft fixed asset baru"
  - "Nilai buku total asset per cost center"
  - "Asset yang belum di-set jadwal depresiasinya"
nudge_rules: []
---

# Modul Fixed Assets

Modul Fixed Assets melacak aset tetap yang dikapitalisasi dan disusutkan
selama umur ekonomisnya.

## Data Asset

Setiap asset punya:

- Acquisition cost dan tanggal perolehan.
- Useful life (bulan) dan salvage value.
- Depreciation method (straight-line di v1).
- Asset category dan cost center untuk alokasi beban depresiasi.
- Link ke akun aset, akumulasi depresiasi, dan beban depresiasi di COA.

## Aturan Bisnis

- Depreciation run bulanan menghasilkan Journal Entry: Dr Beban Depresiasi
  / Cr Akumulasi Depresiasi.
- Disposal asset (penjualan / write-off) menghapus nilai buku dan posting
  gain/loss ke GL — selalu human action.
- Agent boleh draft fixed asset baru, tapi konfigurasi jadwal depresiasi
  dan kategori akuntansi butuh judgement.
