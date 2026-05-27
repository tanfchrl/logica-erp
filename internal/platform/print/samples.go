package print

// SampleContext returns a curated set of placeholder values per doctype so the
// admin preview shows what a real document would look like. The map is the
// `.Invoice`/`.Customer`/`.Company` shape that existing bundled templates
// (and any user-authored template that mirrors them) consume.
func SampleContext(doctype string) map[string]any {
	switch doctype {
	case "sales_invoice":
		return map[string]any{
			"Invoice": map[string]any{
				"Name":             "SI-2026-00042",
				"TaxInvoiceNumber": "010.000-26.00000042",
				"Docstatus":        1,
				"GrandTotal":       "12.485.000,00",
				"Currency":         "IDR",
				"Items": []map[string]any{
					{"RowIndex": 1, "ItemCode": "ITM-001", "ItemName": "Konsultasi Pajak", "Description": "Bulan Mei 2026",
						"Qty": "10", "UOM": "Jam", "Rate": "750.000,00", "Amount": "7.500.000,00", "TaxAmount": "825.000,00", "Total": "8.325.000,00"},
					{"RowIndex": 2, "ItemCode": "ITM-014", "ItemName": "Audit Laporan Keuangan", "Description": "Triwulan I",
						"Qty": "1",  "UOM": "Paket", "Rate": "3.750.000,00", "Amount": "3.750.000,00", "TaxAmount": "412.500,00", "Total": "4.162.500,00"},
				},
				"NetTotal":   "11.250.000,00",
				"TotalTax":   "1.237.500,00",
				"Remarks":    "Termin pembayaran 30 hari sejak tanggal faktur.",
			},
			"Customer": map[string]any{
				"DisplayName": "PT Sinar Mas Indonesia",
				"NPWP":        "01.234.567.8-901.000",
				"AddressLine": "Jl. Sudirman Kav. 21, Jakarta Selatan 12190",
			},
			"PostingDate": "2026-05-15",
			"DueDate":     "2026-06-14",
		}
	case "purchase_invoice":
		return map[string]any{
			"Invoice":  map[string]any{"Name": "PI-2026-00018", "Docstatus": 1, "GrandTotal": "6.660.000,00"},
			"Supplier": map[string]any{"DisplayName": "CV Mitra Sentosa", "NPWP": "02.345.678.9-012.000"},
			"PostingDate": "2026-05-12", "DueDate": "2026-06-11",
		}
	case "payment_entry":
		return map[string]any{
			"Payment": map[string]any{"Name": "PE-2026-00075", "Amount": "8.325.000,00", "Mode": "Transfer Bank"},
			"Party":   map[string]any{"DisplayName": "PT Sinar Mas Indonesia"},
			"PostingDate": "2026-05-30",
		}
	case "journal_entry":
		return map[string]any{
			"Journal": map[string]any{"Name": "JE-2026-00112", "TotalDebit": "1.000.000,00", "TotalCredit": "1.000.000,00"},
			"PostingDate": "2026-05-20",
		}
	}
	return map[string]any{
		"Doctype":     doctype,
		"PostingDate": "2026-05-15",
	}
}

// SampleCompany used by the letterhead preview when no real company is in scope.
func SampleCompany() map[string]any {
	return map[string]any{
		"Name":        "Demo Indonesia",
		"LegalName":   "PT Demo Indonesia",
		"NPWP":        "00.000.000.0-000.000",
		"AddressLine": "Jl. Contoh No. 1, Jakarta",
		"Phone":       "+62 21 5555 0000",
		"Email":       "ops@demo.id",
		"Website":     "demo.id",
	}
}
