---
module: hr
display_name: "Modul HR"
version: "1"
documents:
  - name: employee
    display_name: "Karyawan"
    api_path: "/hr/employees"
    tier0_tools: [list_with_filters, get_by_id]
    tier1_tools: [create_draft]
    tier2_tools: []
system_context: |
  The HR module owns employee master data: identity (NIK, NPWP), employment
  status, department, position, hire/termination dates, and the link to the
  accounting cost center for payroll allocation. The agent can list and
  query employees and draft new employee records — but employment status
  changes, salary structure, and termination flows require human review
  because they have downstream implications on payroll, PPh 21, and BPJS.
suggested_prompts:
  - "Daftar karyawan aktif per departemen"
  - "Karyawan yang ulang tahun bulan ini"
  - "Buat draft data karyawan baru"
  - "Headcount per cost center"
  - "Karyawan yang masa probationnya berakhir bulan depan"
nudge_rules: []
---

# Modul HR

Modul HR di v1 fokus pada employee master data sebagai prasyarat untuk
payroll dan alokasi biaya tenaga kerja ke cost center.

## Data Karyawan

Setiap karyawan punya:

- Identitas wajib pajak: NIK dan NPWP (untuk PPh 21).
- Employment status: probation, permanent, contract, terminated.
- Department dan position untuk struktur organisasi.
- Cost center untuk alokasi beban gaji di General Ledger.
- Tanggal join dan tanggal exit (jika applicable).

## Aturan Bisnis

- Karyawan baru bisa dibuat sebagai draft oleh agent, tapi onboarding
  formal (assign role, payroll setup) tetap aksi manual.
- Status termination tidak boleh diubah oleh agent — punya konsekuensi
  hukum dan finansial (severance, PPh 21 final).
- Data NPWP karyawan dipakai di payroll untuk hitung PPh 21 — pastikan
  format valid sebelum submit.
