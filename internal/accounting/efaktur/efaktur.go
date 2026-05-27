// Package efaktur exports submitted Sales Invoices in a CSV layout compatible
// with the Indonesian DJP e-Faktur desktop application.
//
// The real e-Faktur format has separate FK / LT / OF / FAPR records arranged
// as a header + line layout. For Phase 6 MVP this emits a simplified one-row-per-
// invoice CSV with the canonical columns; a follow-up can swap in the full
// multi-record CSV / XML once the user confirms the exact e-Faktur version.
package efaktur

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

// Write streams the CSV rows for all submitted SI in [from, to] with a tax_invoice_number.
// Returns the number of rows written (excluding the header).
func (s *Service) Write(ctx context.Context, w io.Writer, companyID string, from, to time.Time) (int, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{
		"KD_JENIS_TRANSAKSI", "FG_PENGGANTI", "NOMOR_FAKTUR", "TANGGAL_FAKTUR",
		"NPWP_LAWAN_TRANSAKSI", "NAMA_LAWAN_TRANSAKSI",
		"JUMLAH_DPP", "JUMLAH_PPN", "JUMLAH_PPNBM",
		"REFERENSI", "MATA_UANG",
	}
	if err := cw.Write(header); err != nil {
		return 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT si.tax_invoice_number, si.posting_date,
		       coalesce(c.npwp, ''), c.display_name,
		       si.net_total, si.total_taxes_and_charges,
		       si.name, si.currency
		FROM sales_invoice si
		JOIN customer c ON c.id = si.customer_id
		WHERE si.company_id = $1
		  AND si.docstatus = 1
		  AND si.is_return = false
		  AND si.tax_invoice_number IS NOT NULL AND si.tax_invoice_number <> ''
		  AND si.posting_date BETWEEN $2 AND $3
		ORDER BY si.posting_date, si.name`,
		companyID, from, to)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			taxInvoiceNo, customerNPWP, customerName, siName, currency string
			postingDate                                                time.Time
			dpp, ppn                                                   string
		)
		if err := rows.Scan(&taxInvoiceNo, &postingDate, &customerNPWP, &customerName, &dpp, &ppn, &siName, &currency); err != nil {
			return count, err
		}
		// KD_JENIS_TRANSAKSI '01' = Penyerahan kepada Pihak yang BUKAN Pemungut PPN; FG_PENGGANTI '0' = normal.
		out := []string{
			"01", "0", normalizeFakturNo(taxInvoiceNo), postingDate.Format("02/01/2006"),
			customerNPWP, customerName,
			stripTrailingZeros(dpp), stripTrailingZeros(ppn), "0",
			siName, currency,
		}
		if err := cw.Write(out); err != nil {
			return count, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	return count, nil
}

// normalizeFakturNo strips dots and dashes (DJP wants a 16-digit string).
func normalizeFakturNo(s string) string {
	r := strings.ReplaceAll(s, ".", "")
	r = strings.ReplaceAll(r, "-", "")
	return r
}

// stripTrailingZeros converts "1234.0000" → "1234" to match DJP's integer-amount expectation.
func stripTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	return s
}

// Format a date in DD/MM/YYYY for templated direct callers if needed.
func FormatTanggalFaktur(t time.Time) string {
	return fmt.Sprintf("%02d/%02d/%04d", t.Day(), int(t.Month()), t.Year())
}
