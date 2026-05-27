package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/config"
)

// bootstrap seeds an admin user, a "system_administrator" role with full
// permissions on every Phase 0 doctype, a demo company, the current fiscal
// year, and a small Indonesian-style Chart of Accounts. Idempotent.
func bootstrap(ctx context.Context, db *sql.DB, cfg config.Config) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	roleID, err := ensureRole(ctx, tx, "system_administrator", "Full system administrator")
	if err != nil {
		return err
	}
	if err := ensureFullPermissions(ctx, tx, roleID); err != nil {
		return err
	}

	userID, err := ensureAdminUser(ctx, tx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_role (user_id, role_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		userID, roleID); err != nil {
		return err
	}

	companyID, err := ensureDemoCompany(ctx, tx, userID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_company (user_id, company_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		userID, companyID); err != nil {
		return err
	}

	if err := ensureFiscalYear(ctx, tx, companyID); err != nil {
		return err
	}
	if err := ensureCOA(ctx, tx, companyID, userID); err != nil {
		return err
	}
	if err := ensureJournalEntrySeries(ctx, tx, companyID); err != nil {
		return err
	}
	if err := ensurePhase1Series(ctx, tx, companyID); err != nil {
		return err
	}
	if err := ensurePhase1TaxAndMasters(ctx, tx, companyID, userID); err != nil {
		return err
	}

	return tx.Commit()
}

func ensurePhase1Series(ctx context.Context, tx *sql.Tx, companyID string) error {
	for _, p := range []struct{ doctype, pattern string }{
		{"sales_invoice", "SI-.YYYY.-.####"},
		{"payment_entry", "PE-.YYYY.-.####"},
		{"purchase_invoice", "PI-.YYYY.-.####"},
	} {
		var exists int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM naming_series WHERE doctype = $1 AND company_id = $2`, p.doctype, companyID).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
			VALUES ($1,$2,$3,$4,true)`,
			newID("nms"), p.doctype, companyID, p.pattern); err != nil {
			return err
		}
	}
	return nil
}

// ensurePhase1TaxAndMasters seeds: PPN Masukan account, PPN Keluaran 11% template,
// PPN Masukan 11% template, PPh 23 withholding type, sample customer, sample item.
func ensurePhase1TaxAndMasters(ctx context.Context, tx *sql.Tx, companyID, userID string) error {
	// Look up accounts created by ensureCOA.
	accIDs := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT name, id FROM account WHERE company_id = $1`, companyID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n, id string
		if err := rows.Scan(&n, &id); err != nil {
			rows.Close()
			return err
		}
		accIDs[n] = id
	}
	rows.Close()

	// Add "Pajak Masukan" asset account if missing.
	pajakMasukan, ok := accIDs["Pajak Masukan"]
	if !ok {
		pajakMasukan = newID("acc")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO account (id, company_id, name, parent_id, is_group, root_type, account_type, account_currency, created_by, updated_by)
			VALUES ($1,$2,'Pajak Masukan', (SELECT id FROM account WHERE name='Aset Lancar' AND company_id=$2), false, 'asset', 'tax', 'IDR', $3, $3)`,
			pajakMasukan, companyID, userID); err != nil {
			return fmt.Errorf("seed: add Pajak Masukan account: %w", err)
		}
		accIDs["Pajak Masukan"] = pajakMasukan
		fmt.Println("added account: Pajak Masukan")
	}

	utangPPN := accIDs["Utang Pajak - PPN"]
	utangPPh := accIDs["Utang Pajak - PPh"]
	if utangPPN == "" || utangPPh == "" {
		return errors.New("seed: Utang Pajak accounts missing")
	}

	// PPN Keluaran 11% (sales template).
	ppnOut := newID("txt")
	if exists, err := templateExists(ctx, tx, companyID, "PPN Keluaran 11%"); err != nil {
		return err
	} else if !exists {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tax_template (id, company_id, name, is_sales, is_default, created_by, updated_by)
			VALUES ($1,$2,'PPN Keluaran 11%', true, true, $3, $3)`,
			ppnOut, companyID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tax_template_line (id, template_id, row_index, account_id, description, rate, charge_type)
			VALUES ($1,$2,1,$3,'PPN Keluaran 11%',11,'on_net_total')`,
			newID("txtl"), ppnOut, utangPPN); err != nil {
			return err
		}
		fmt.Println("seeded tax template: PPN Keluaran 11%")
	} else {
		_ = tx.QueryRowContext(ctx, `SELECT id FROM tax_template WHERE company_id = $1 AND name = $2`, companyID, "PPN Keluaran 11%").Scan(&ppnOut)
	}

	// PPN Masukan 11% (purchase template) — for Phase 1B but seeded now.
	if exists, err := templateExists(ctx, tx, companyID, "PPN Masukan 11%"); err != nil {
		return err
	} else if !exists {
		id := newID("txt")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tax_template (id, company_id, name, is_sales, is_default, created_by, updated_by)
			VALUES ($1,$2,'PPN Masukan 11%', false, true, $3, $3)`,
			id, companyID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tax_template_line (id, template_id, row_index, account_id, description, rate, charge_type)
			VALUES ($1,$2,1,$3,'PPN Masukan 11%',11,'on_net_total')`,
			newID("txtl"), id, pajakMasukan); err != nil {
			return err
		}
		fmt.Println("seeded tax template: PPN Masukan 11%")
	}

	// PPh 23 - Jasa (2%).
	var pphID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM withholding_tax_type WHERE name = $1`, "PPh 23 - Jasa").Scan(&pphID)
	if errors.Is(err, sql.ErrNoRows) {
		pphID = newID("wht")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO withholding_tax_type (id, name, rate, account_id, category, created_by, updated_by)
			VALUES ($1,'PPh 23 - Jasa', 2, $2, 'entity', $3, $3)`,
			pphID, utangPPh, userID); err != nil {
			return err
		}
		fmt.Println("seeded withholding type: PPh 23 - Jasa")
	} else if err != nil {
		return err
	}

	// Sample customer.
	custID := newID("cust")
	err = tx.QueryRowContext(ctx, `SELECT id FROM customer WHERE name = $1`, "CUST-001").Scan(&custID)
	if errors.Is(err, sql.ErrNoRows) {
		custID = newID("cust")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO customer (id, name, display_name, default_currency, created_by, updated_by)
			VALUES ($1,'CUST-001','PT Pelanggan Contoh','IDR',$2,$2)`,
			custID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO customer_default (customer_id, company_id, default_receivable_account_id, default_currency, default_tax_template_id)
			VALUES ($1,$2,$3,'IDR',$4)`,
			custID, companyID, accIDs["Piutang Usaha"], ppnOut); err != nil {
			return err
		}
		fmt.Println("seeded customer: PT Pelanggan Contoh")
	} else if err != nil {
		return err
	}

	// Look up PPN Masukan template for supplier_default.
	var ppnIn string
	_ = tx.QueryRowContext(ctx, `SELECT id FROM tax_template WHERE company_id = $1 AND name = $2`, companyID, "PPN Masukan 11%").Scan(&ppnIn)

	// Sample supplier.
	suppID := newID("supp")
	err = tx.QueryRowContext(ctx, `SELECT id FROM supplier WHERE name = $1`, "SUPP-001").Scan(&suppID)
	if errors.Is(err, sql.ErrNoRows) {
		suppID = newID("supp")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO supplier (id, name, display_name, default_currency, created_by, updated_by)
			VALUES ($1,'SUPP-001','PT Pemasok Contoh','IDR',$2,$2)`,
			suppID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO supplier_default (supplier_id, company_id, default_payable_account_id, default_currency, default_tax_template_id)
			VALUES ($1,$2,$3,'IDR',$4)`,
			suppID, companyID, accIDs["Utang Usaha"], nullIfEmpty(ppnIn)); err != nil {
			return err
		}
		fmt.Println("seeded supplier: PT Pemasok Contoh")
	} else if err != nil {
		return err
	}

	// Sample expense item.
	expItemID := newID("itm")
	err = tx.QueryRowContext(ctx, `SELECT id FROM item WHERE code = $1`, "ITM-OFFICE").Scan(&expItemID)
	if errors.Is(err, sql.ErrNoRows) {
		expItemID = newID("itm")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item (id, code, name, description, stock_uom, is_stock_item, is_sales_item, is_purchase_item, standard_rate, created_by, updated_by)
			VALUES ($1,'ITM-OFFICE','Perlengkapan Kantor','Office supplies (non-stock expense item)','Unit',false,false,true,500000,$2,$2)`,
			expItemID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item_default (item_id, company_id, default_expense_account_id, default_tax_template_id)
			VALUES ($1,$2,$3,$4)`,
			expItemID, companyID, accIDs["Beban Operasional"], nullIfEmpty(ppnIn)); err != nil {
			return err
		}
		fmt.Println("seeded item: Perlengkapan Kantor")
	} else if err != nil {
		return err
	}

	// Sample (existing) consultancy item.
	itemID := newID("itm")
	err = tx.QueryRowContext(ctx, `SELECT id FROM item WHERE code = $1`, "ITM-CONS").Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		itemID = newID("itm")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item (id, code, name, description, stock_uom, is_stock_item, is_sales_item, standard_rate, created_by, updated_by)
			VALUES ($1,'ITM-CONS','Layanan Konsultasi','Konsultasi per jam','Jam',false,true,1000000,$2,$2)`,
			itemID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item_default (item_id, company_id, default_income_account_id, default_tax_template_id)
			VALUES ($1,$2,$3,$4)`,
			itemID, companyID, accIDs["Penjualan"], ppnOut); err != nil {
			return err
		}
		fmt.Println("seeded item: Layanan Konsultasi")
	} else if err != nil {
		return err
	}

	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func templateExists(ctx context.Context, tx *sql.Tx, companyID, name string) (bool, error) {
	var x int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM tax_template WHERE company_id = $1 AND name = $2`, companyID, name).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func ensureRole(ctx context.Context, tx *sql.Tx, name, desc string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM role WHERE name = $1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID("role")
	_, err = tx.ExecContext(ctx,
		`INSERT INTO role (id, name, description, is_system) VALUES ($1,$2,$3,true)`, id, name, desc)
	return id, err
}

var phase0Doctypes = []string{
	"company", "account", "cost_center",
	"journal_entry",
	"role", "user_permission", "field_permission",
	// Phase 1A doctypes
	"customer", "supplier", "item",
	"tax_category", "tax_template", "withholding_tax_type",
	"sales_invoice", "payment_entry",
	"report",
	// Phase 1B
	"purchase_invoice",
	// Phase 1C
	"period_closing_voucher",
	// Phase 2
	"warehouse", "stock_entry", "purchase_order", "sales_order",
	// Procurement v1 (post-launch additions)
	"material_request", "purchase_receipt", "buying_settings",
	// Phase 3
	"lead", "project", "task", "timesheet",
	// Phase 4
	"bom", "work_order", "asset",
	// Phase 5
	"employee", "department", "designation", "attendance", "leave_application", "expense_claim",
	"salary_component", "salary_structure", "payroll_entry", "salary_slip",
	"pos_profile", "pos_invoice",
	"issue", "service_level_agreement",
	// Phase 6
	"workflow", "notification", "comment", "attachment",
	// Admin / settings
	"naming_series",
	"smtp_config", "email_template",
	"audit_log",
	"fiscal_year",
	"user", "role_permission",
	"letterhead", "print_template",
	"approval_rule",
	"import_job",
	"webhook_subscription", "api_token", "connector_config", "notification_rule", "payroll_setting",
}

func ensureFullPermissions(ctx context.Context, tx *sql.Tx, roleID string) error {
	for _, dt := range phase0Doctypes {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM role_permission WHERE role_id = $1 AND doctype = $2`, roleID, dt).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO role_permission (id, role_id, doctype,
				can_read, can_write, can_create, can_delete, can_submit, can_cancel, can_amend, can_print, can_export)
			VALUES ($1,$2,$3, true, true, true, true, true, true, true, true, true)`,
			newID("rp"), roleID, dt)
		if err != nil {
			return err
		}
	}
	return nil
}

func ensureAdminUser(ctx context.Context, tx *sql.Tx, email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", errors.New("seed: LOGICA_BOOTSTRAP_ADMIN_EMAIL is empty")
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&id)
	if err == nil {
		fmt.Printf("admin user %s already exists\n", email)
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if password == "" {
		return "", errors.New("seed: LOGICA_BOOTSTRAP_ADMIN_PASSWORD is empty for first-time bootstrap")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	id = newID("usr")
	_, err = tx.ExecContext(ctx, `
		INSERT INTO users (id, email, full_name, password_hash, enabled, locale, time_zone, is_system)
		VALUES ($1,$2,'Administrator',$3,true,'id-ID','Asia/Jakarta',true)`,
		id, email, hash)
	if err != nil {
		return "", err
	}
	fmt.Printf("created admin user %s\n", email)
	return id, nil
}

func ensureDemoCompany(ctx context.Context, tx *sql.Tx, userID string) (string, error) {
	const name = "Demo Indonesia"
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM company WHERE name = $1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID("cmp")
	_, err = tx.ExecContext(ctx, `
		INSERT INTO company (id, name, legal_name, abbreviation, country, default_currency,
		                     created_by, updated_by)
		VALUES ($1,$2,$3,'DEMO','ID','IDR',$4,$4)`,
		id, name, "PT Demo Indonesia", userID)
	if err != nil {
		return "", err
	}
	fmt.Printf("created demo company %q\n", name)
	return id, nil
}

func ensureFiscalYear(ctx context.Context, tx *sql.Tx, companyID string) error {
	year := time.Now().UTC().Year()
	name := fmt.Sprintf("%d", year)
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM fiscal_year WHERE name = $1`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = newID("fy")
		_, err = tx.ExecContext(ctx, `
			INSERT INTO fiscal_year (id, name, start_date, end_date)
			VALUES ($1,$2, make_date($3,1,1), make_date($3,12,31))`,
			id, name, year)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO fiscal_year_company (fiscal_year_id, company_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		id, companyID)
	return err
}

// A starter Chart of Accounts. Indonesian SME-style grouping; expanded in Phase 1
// with the full template per the design doc.
var starterCOA = []struct {
	Name     string
	Parent   string
	Type     string  // root_type
	SubType  string  // account_type
	IsGroup  bool
}{
	{"Application of Funds (Assets)", "", "asset", "", true},
	{"Aset Lancar", "Application of Funds (Assets)", "asset", "", true},
	{"Kas", "Aset Lancar", "asset", "cash", false},
	{"Bank", "Aset Lancar", "asset", "bank", false},
	{"Piutang Usaha", "Aset Lancar", "asset", "receivable", false},
	{"Persediaan", "Aset Lancar", "asset", "stock", false},
	{"Aset Tetap", "Application of Funds (Assets)", "asset", "", true},

	{"Source of Funds (Liabilities)", "", "liability", "", true},
	{"Kewajiban Lancar", "Source of Funds (Liabilities)", "liability", "", true},
	{"Utang Usaha", "Kewajiban Lancar", "liability", "payable", false},
	{"Utang Pajak - PPN", "Kewajiban Lancar", "liability", "tax", false},
	{"Utang Pajak - PPh", "Kewajiban Lancar", "liability", "tax", false},

	{"Equity", "", "equity", "", true},
	{"Modal Disetor", "Equity", "equity", "", false},
	{"Laba Ditahan", "Equity", "equity", "", false},

	{"Income", "", "income", "", true},
	{"Penjualan", "Income", "income", "", false},

	{"Expenses", "", "expense", "", true},
	{"Harga Pokok Penjualan", "Expenses", "expense", "cogs", false},
	{"Beban Operasional", "Expenses", "expense", "", false},
}

func ensureCOA(ctx context.Context, tx *sql.Tx, companyID, userID string) error {
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM account WHERE company_id = $1`, companyID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	idsByName := map[string]string{}
	for _, n := range starterCOA {
		id := newID("acc")
		var parent any
		if n.Parent != "" {
			p, ok := idsByName[n.Parent]
			if !ok {
				return fmt.Errorf("seed: parent account %q not found", n.Parent)
			}
			parent = p
		}
		subType := any(nil)
		if n.SubType != "" {
			subType = n.SubType
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO account (id, company_id, name, parent_id, is_group, root_type, account_type, account_currency, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'IDR',$8,$8)`,
			id, companyID, n.Name, parent, n.IsGroup, n.Type, subType, userID)
		if err != nil {
			return fmt.Errorf("seed COA %q: %w", n.Name, err)
		}
		idsByName[n.Name] = id
	}
	if id, ok := idsByName["Piutang Usaha"]; ok {
		if _, err := tx.ExecContext(ctx, `UPDATE company SET default_receivable_account_id = $1 WHERE id = $2`, id, companyID); err != nil {
			return err
		}
	}
	if id, ok := idsByName["Utang Usaha"]; ok {
		if _, err := tx.ExecContext(ctx, `UPDATE company SET default_payable_account_id = $1 WHERE id = $2`, id, companyID); err != nil {
			return err
		}
	}
	if id, ok := idsByName["Kas"]; ok {
		if _, err := tx.ExecContext(ctx, `UPDATE company SET default_cash_account_id = $1 WHERE id = $2`, id, companyID); err != nil {
			return err
		}
	}
	if id, ok := idsByName["Bank"]; ok {
		if _, err := tx.ExecContext(ctx, `UPDATE company SET default_bank_account_id = $1 WHERE id = $2`, id, companyID); err != nil {
			return err
		}
	}
	if id, ok := idsByName["Penjualan"]; ok {
		if _, err := tx.ExecContext(ctx, `UPDATE company SET default_income_account_id = $1 WHERE id = $2`, id, companyID); err != nil {
			return err
		}
	}
	if id, ok := idsByName["Beban Operasional"]; ok {
		if _, err := tx.ExecContext(ctx, `UPDATE company SET default_expense_account_id = $1 WHERE id = $2`, id, companyID); err != nil {
			return err
		}
	}
	fmt.Printf("seeded %d accounts\n", len(starterCOA))
	return nil
}

// ensureJournalEntrySeries copies the global default series (seeded in migration 0002)
// into a per-company series so each company increments independently.
func ensureJournalEntrySeries(ctx context.Context, tx *sql.Tx, companyID string) error {
	var exists int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM naming_series WHERE doctype = 'journal_entry' AND company_id = $1`, companyID).Scan(&exists)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
		VALUES ($1, 'journal_entry', $2, 'JE-.YYYY.-.####', true)`,
		newID("nms"), companyID)
	return err
}

// ---- helpers ----

// createUser is exposed for `logica user-add`.
func createUser(ctx context.Context, db *sql.DB, email, password string, isSystem bool) (string, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	id := newID("usr")
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, email, full_name, password_hash, enabled, locale, time_zone, is_system)
		VALUES ($1,$2,$3,$4,true,'id-ID','Asia/Jakarta',$5)`,
		id, strings.ToLower(email), email, hash, isSystem)
	return id, err
}

func newID(prefix string) string {
	u := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
	return prefix + "_" + u
}
