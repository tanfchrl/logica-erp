package dataimport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// Recipes registered at package init. Add new doctypes here.
func init() {
	register(customerRecipe())
	register(supplierRecipe())
	register(itemRecipe())
	register(accountRecipe())
	register(posInvoiceItemRecipe())
	register(purchaseInvoiceItemRecipe())
	register(salesInvoiceItemRecipe())
	register(journalEntryAccountRecipe())
}

// ---------- customer ----------

func customerRecipe() Recipe {
	return Recipe{
		Doctype: "customer", Label: "Customers",
		Description:   "Customer masters. NPWP must be exactly 16 digits (Indonesian format) or left blank.",
		CompanyScoped: false,
		Fields: []FieldDef{
			{Key: "display_name", Label: "Display name",       Required: true,  Type: "text"},
			{Key: "name",         Label: "Identifier (slug)",  Required: false, Type: "text", Description: "Internal name (default: display_name)"},
			{Key: "email",        Label: "Email",                                 Type: "email"},
			{Key: "phone",        Label: "Phone",                                 Type: "text"},
			{Key: "npwp",         Label: "NPWP (16 digits)",                      Type: "text"},
			{Key: "is_individual",Label: "Is individual",                         Type: "bool", Description: "true / false / yes / no / ya / tidak"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, _, userID string, row map[string]string) (string, error) {
			display := row["display_name"]
			name := row["name"]
			if name == "" {
				name = slugify(display)
			}
			isInd, err := parseBool(row["is_individual"])
			if err != nil {
				return display, fmt.Errorf("is_individual: %w", err)
			}
			npwp := strings.TrimSpace(row["npwp"])
			if npwp != "" && !is16Digits(npwp) {
				return display, errors.New("npwp must be 16 digits")
			}
			id := dbx.NewIDWithPrefix("cus")
			_, err = tx.Exec(ctx, `
				INSERT INTO customer (id, name, display_name, npwp, is_individual, email, phone, created_by, updated_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
				id, name, display, nullable(npwp), isInd, nullable(row["email"]), nullable(row["phone"]), userID)
			if err != nil && dbx.IsUniqueViolation(err) {
				return display, errors.New("customer with this identifier already exists")
			}
			return display, err
		},
	}
}

// ---------- supplier ----------

func supplierRecipe() Recipe {
	return Recipe{
		Doctype: "supplier", Label: "Suppliers",
		Description:   "Supplier masters. Same NPWP rules as customers.",
		CompanyScoped: false,
		Fields: []FieldDef{
			{Key: "display_name", Label: "Display name",      Required: true, Type: "text"},
			{Key: "name",         Label: "Identifier (slug)", Type: "text", Description: "Defaults to display_name slug"},
			{Key: "email",        Label: "Email",             Type: "email"},
			{Key: "phone",        Label: "Phone",             Type: "text"},
			{Key: "npwp",         Label: "NPWP (16 digits)",  Type: "text"},
			{Key: "is_individual",Label: "Is individual",     Type: "bool"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, _, userID string, row map[string]string) (string, error) {
			display := row["display_name"]
			name := row["name"]
			if name == "" {
				name = slugify(display)
			}
			isInd, err := parseBool(row["is_individual"])
			if err != nil {
				return display, fmt.Errorf("is_individual: %w", err)
			}
			npwp := strings.TrimSpace(row["npwp"])
			if npwp != "" && !is16Digits(npwp) {
				return display, errors.New("npwp must be 16 digits")
			}
			id := dbx.NewIDWithPrefix("sup")
			_, err = tx.Exec(ctx, `
				INSERT INTO supplier (id, name, display_name, npwp, is_individual, email, phone, created_by, updated_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
				id, name, display, nullable(npwp), isInd, nullable(row["email"]), nullable(row["phone"]), userID)
			if err != nil && dbx.IsUniqueViolation(err) {
				return display, errors.New("supplier with this identifier already exists")
			}
			return display, err
		},
	}
}

// ---------- item ----------

func itemRecipe() Recipe {
	return Recipe{
		Doctype: "item", Label: "Items",
		Description:   "Item / SKU masters. UOM defaults to 'Unit' if blank.",
		CompanyScoped: false,
		Fields: []FieldDef{
			{Key: "code",             Label: "Item code",       Required: true, Type: "text"},
			{Key: "name",             Label: "Item name",       Required: true, Type: "text"},
			{Key: "description",      Label: "Description",                     Type: "text"},
			{Key: "stock_uom",        Label: "Stock UOM",                       Type: "text", Description: "Default: Unit"},
			{Key: "standard_rate",    Label: "Standard rate",                   Type: "number"},
			{Key: "is_stock_item",    Label: "Is stock item",                   Type: "bool"},
			{Key: "is_sales_item",    Label: "Is sales item",                   Type: "bool"},
			{Key: "is_purchase_item", Label: "Is purchase item",                Type: "bool"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, _, userID string, row map[string]string) (string, error) {
			code := row["code"]
			name := row["name"]
			uom := row["stock_uom"]
			if uom == "" {
				uom = "Unit"
			}
			rate, err := parseDecimalString(row["standard_rate"])
			if err != nil {
				return code, fmt.Errorf("standard_rate: %w", err)
			}
			stock, err := parseBool(row["is_stock_item"])
			if err != nil { return code, fmt.Errorf("is_stock_item: %w", err) }
			sales, err := parseBool(row["is_sales_item"])
			if err != nil { return code, fmt.Errorf("is_sales_item: %w", err) }
			purch, err := parseBool(row["is_purchase_item"])
			if err != nil { return code, fmt.Errorf("is_purchase_item: %w", err) }

			id := dbx.NewIDWithPrefix("itm")
			_, err = tx.Exec(ctx, `
				INSERT INTO item (id, code, name, description, stock_uom, standard_rate,
				                  is_stock_item, is_sales_item, is_purchase_item, created_by, updated_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`,
				id, code, name, nullable(row["description"]), uom, rate,
				stock, sales, purch, userID)
			if err != nil && dbx.IsUniqueViolation(err) {
				return code, errors.New("item with this code already exists")
			}
			return code, err
		},
	}
}

// ---------- account (chart of accounts) ----------

func accountRecipe() Recipe {
	return Recipe{
		Doctype: "account", Label: "Chart of accounts",
		Description:   "GL accounts for the active company. parent_account_number must reference an existing account in the same company.",
		CompanyScoped: true,
		Fields: []FieldDef{
			{Key: "account_number",        Label: "Account number",        Required: true, Type: "text", LookupHint: "Unique within company"},
			{Key: "name",                  Label: "Account name",          Required: true, Type: "text"},
			{Key: "root_type",             Label: "Root type",             Required: true, Type: "select",
			 Options: []string{"asset", "liability", "equity", "income", "expense"}},
			{Key: "account_type",          Label: "Account type",                          Type: "text", Description: "e.g. Bank, Receivable, Tax, Stock"},
			{Key: "account_currency",      Label: "Currency",                              Type: "text", Description: "Default: IDR"},
			{Key: "is_group",              Label: "Is group",                              Type: "bool"},
			{Key: "parent_account_number", Label: "Parent account number",                 Type: "lookup", LookupHint: "Existing account_number in this company"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, companyID, userID string, row map[string]string) (string, error) {
			num := row["account_number"]
			name := row["name"]
			rootType := strings.ToLower(strings.TrimSpace(row["root_type"]))
			if !validRootType(rootType) {
				return num, fmt.Errorf("root_type must be one of asset/liability/equity/income/expense (got %q)", rootType)
			}
			currency := row["account_currency"]
			if currency == "" {
				currency = "IDR"
			}
			isGroup, err := parseBool(row["is_group"])
			if err != nil { return num, fmt.Errorf("is_group: %w", err) }

			var parentID *string
			if pn := strings.TrimSpace(row["parent_account_number"]); pn != "" {
				var id string
				if err := tx.QueryRow(ctx,
					`SELECT id FROM account WHERE company_id = $1 AND account_number = $2`, companyID, pn).Scan(&id); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return num, fmt.Errorf("parent account %q not found", pn)
					}
					return num, err
				}
				parentID = &id
			}

			id := dbx.NewIDWithPrefix("acc")
			_, err = tx.Exec(ctx, `
				INSERT INTO account (id, company_id, name, account_number, parent_id, is_group,
				                     root_type, account_type, account_currency, created_by, updated_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`,
				id, companyID, name, nullable(num), parentID, isGroup,
				rootType, nullable(row["account_type"]), currency, userID)
			if err != nil && dbx.IsUniqueViolation(err) {
				return num, errors.New("account with this name already exists in this company")
			}
			return num, err
		},
	}
}

// ---------- pos_invoice_item (lines into existing draft POS invoice) ----------

func posInvoiceItemRecipe() Recipe {
	return Recipe{
		Doctype: "pos_invoice_item", Label: "POS invoice — lines",
		Description: "Append line items to existing draft POS invoices. Parent referenced by name " +
			"(e.g. POS-2026-0001). After each row, parent totals are recomputed in the same tx.",
		CompanyScoped: true,
		Fields: []FieldDef{
			{Key: "pos_invoice_name", Label: "POS invoice name", Required: true, Type: "lookup",
				LookupHint: "Must be an existing draft (docstatus=0) POS invoice in this company"},
			{Key: "item_code",        Label: "Item code",        Required: true, Type: "lookup",
				LookupHint: "Must be an existing item.code"},
			{Key: "qty",              Label: "Quantity",         Required: true, Type: "number"},
			{Key: "rate",             Label: "Rate",             Required: true, Type: "number"},
			{Key: "tax_amount",       Label: "Tax amount",                       Type: "number", Description: "Optional, defaults to 0"},
			{Key: "uom",              Label: "UOM",                              Type: "text",   Description: "Optional, defaults to item's stock_uom"},
			{Key: "item_name",        Label: "Item name",                        Type: "text",   Description: "Optional, defaults to item master name"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, companyID, _ string, row map[string]string) (string, error) {
			parentName := strings.TrimSpace(row["pos_invoice_name"])
			itemCode   := strings.TrimSpace(row["item_code"])

			// 1) Locate the parent invoice; ensure draft.
			var parentID string
			var docstatus int
			if err := tx.QueryRow(ctx,
				`SELECT id, docstatus FROM pos_invoice WHERE company_id = $1 AND name = $2`,
				companyID, parentName).Scan(&parentID, &docstatus); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("pos_invoice %q not found in this company", parentName)
				}
				return parentName, err
			}
			if docstatus != 0 {
				return parentName, fmt.Errorf("pos_invoice %q is %s; lines can only be added to drafts",
					parentName, docstatusName(docstatus))
			}

			// 2) Look up item; pull defaults for name + uom.
			var (
				itemID   string
				itemName string
				stockUom string
			)
			if err := tx.QueryRow(ctx,
				`SELECT id, name, stock_uom FROM item WHERE code = $1`, itemCode).
				Scan(&itemID, &itemName, &stockUom); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("item %q not found", itemCode)
				}
				return parentName, err
			}
			if explicit := strings.TrimSpace(row["item_name"]); explicit != "" {
				itemName = explicit
			}
			uom := strings.TrimSpace(row["uom"])
			if uom == "" {
				uom = stockUom
			}

			// 3) Parse + compute numerics.
			qty, err := parseDecimalString(row["qty"])
			if err != nil { return parentName, fmt.Errorf("qty: %w", err) }
			rate, err := parseDecimalString(row["rate"])
			if err != nil { return parentName, fmt.Errorf("rate: %w", err) }
			taxAmt, err := parseDecimalString(row["tax_amount"])
			if err != nil { return parentName, fmt.Errorf("tax_amount: %w", err) }
			qtyF, _   := strconv.ParseFloat(qty, 64)
			rateF, _  := strconv.ParseFloat(rate, 64)
			taxF, _   := strconv.ParseFloat(taxAmt, 64)
			if qtyF <= 0  { return parentName, errors.New("qty must be positive") }
			if rateF < 0  { return parentName, errors.New("rate must be ≥ 0") }
			if taxF < 0   { return parentName, errors.New("tax_amount must be ≥ 0") }
			amount := qtyF * rateF
			total  := amount + taxF

			// 4) Allocate next row_index (max+1, default 1).
			var nextIdx int
			if err := tx.QueryRow(ctx,
				`SELECT coalesce(max(row_index), 0) + 1 FROM pos_invoice_item WHERE pos_invoice_id = $1`,
				parentID).Scan(&nextIdx); err != nil {
				return parentName, err
			}

			// 5) Insert the line.
			if _, err := tx.Exec(ctx, `
				INSERT INTO pos_invoice_item (id, pos_invoice_id, row_index, item_id, item_code, item_name,
				                              qty, uom, rate, amount, tax_amount, total)
				VALUES ($1,$2,$3,$4,$5,$6, $7,$8,$9,$10,$11,$12)`,
				dbx.NewIDWithPrefix("posli"), parentID, nextIdx, itemID, itemCode, itemName,
				qty, uom, rate, fmt.Sprintf("%.4f", amount), fmt.Sprintf("%.4f", taxF), fmt.Sprintf("%.4f", total)); err != nil {
				return parentName, err
			}

			// 6) Recompute parent totals from the full line set so they don't go stale.
			//    base_* columns mirror net/grand at 1:1 exchange rate; POS is always company
			//    currency, so this is correct.
			if _, err := tx.Exec(ctx, `
				UPDATE pos_invoice p SET
				  net_total               = sums.net,
				  total_taxes_and_charges = sums.tax,
				  grand_total             = sums.net + sums.tax,
				  base_net_total          = sums.net,
				  base_grand_total        = sums.net + sums.tax
				FROM (
				  SELECT coalesce(sum(amount), 0) AS net, coalesce(sum(tax_amount), 0) AS tax
				  FROM pos_invoice_item WHERE pos_invoice_id = $1
				) sums
				WHERE p.id = $1`, parentID); err != nil {
				return parentName, err
			}

			return fmt.Sprintf("%s · %s ×%g", parentName, itemCode, qtyF), nil
		},
	}
}

// ---------- purchase_invoice_item (lines into existing draft PI) ----------

func purchaseInvoiceItemRecipe() Recipe {
	return Recipe{
		Doctype: "purchase_invoice_item", Label: "Purchase invoice — lines",
		Description: "Append line items to existing draft purchase invoices. Multi-currency aware: " +
			"base amounts are computed from the parent's exchange_rate. Parent totals recomputed per row.",
		CompanyScoped: true,
		Fields: []FieldDef{
			{Key: "purchase_invoice_name",   Label: "Purchase invoice name",   Required: true, Type: "lookup",
				LookupHint: "Existing draft PI in this company (e.g. PI-2026-00018)"},
			{Key: "item_code",               Label: "Item code",               Required: true, Type: "lookup",
				LookupHint: "Existing item.code"},
			{Key: "expense_account_number",  Label: "Expense account number",  Required: true, Type: "lookup",
				LookupHint: "Existing account.account_number in this company"},
			{Key: "qty",                     Label: "Quantity",                Required: true, Type: "number"},
			{Key: "rate",                    Label: "Rate (txn currency)",     Required: true, Type: "number"},
			{Key: "tax_amount",              Label: "Tax amount",                              Type: "number", Description: "Optional, defaults to 0"},
			{Key: "uom",                     Label: "UOM",                                     Type: "text",   Description: "Optional, defaults to item's stock_uom"},
			{Key: "item_name",               Label: "Item name",                               Type: "text",   Description: "Optional, defaults to item master name"},
			{Key: "description",             Label: "Description",                             Type: "text"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, companyID, _ string, row map[string]string) (string, error) {
			parentName := strings.TrimSpace(row["purchase_invoice_name"])
			itemCode   := strings.TrimSpace(row["item_code"])
			expenseNum := strings.TrimSpace(row["expense_account_number"])

			// 1) Locate parent; ensure draft; pull exchange_rate for base-currency math.
			var (
				parentID  string
				docstatus int
				exchange  string
			)
			if err := tx.QueryRow(ctx, `
				SELECT id, docstatus, exchange_rate
				FROM purchase_invoice
				WHERE company_id = $1 AND name = $2`, companyID, parentName).
				Scan(&parentID, &docstatus, &exchange); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("purchase_invoice %q not found in this company", parentName)
				}
				return parentName, err
			}
			if docstatus != 0 {
				return parentName, fmt.Errorf("purchase_invoice %q is %s; lines can only be added to drafts",
					parentName, docstatusName(docstatus))
			}
			exF, _ := strconv.ParseFloat(exchange, 64)
			if exF <= 0 {
				exF = 1
			}

			// 2) Look up item; pull defaults for name + uom.
			var (
				itemID   string
				itemName string
				stockUom string
			)
			if err := tx.QueryRow(ctx,
				`SELECT id, name, stock_uom FROM item WHERE code = $1`, itemCode).
				Scan(&itemID, &itemName, &stockUom); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("item %q not found", itemCode)
				}
				return parentName, err
			}
			if explicit := strings.TrimSpace(row["item_name"]); explicit != "" {
				itemName = explicit
			}
			uom := strings.TrimSpace(row["uom"])
			if uom == "" {
				uom = stockUom
			}

			// 3) Look up expense account, scoped to company.
			var expenseAccountID string
			if err := tx.QueryRow(ctx,
				`SELECT id FROM account WHERE company_id = $1 AND account_number = $2`,
				companyID, expenseNum).Scan(&expenseAccountID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("expense account %q not found in this company", expenseNum)
				}
				return parentName, err
			}

			// 4) Parse + compute numerics.
			qty, err := parseDecimalString(row["qty"])
			if err != nil { return parentName, fmt.Errorf("qty: %w", err) }
			rate, err := parseDecimalString(row["rate"])
			if err != nil { return parentName, fmt.Errorf("rate: %w", err) }
			taxAmt, err := parseDecimalString(row["tax_amount"])
			if err != nil { return parentName, fmt.Errorf("tax_amount: %w", err) }
			qtyF, _   := strconv.ParseFloat(qty, 64)
			rateF, _  := strconv.ParseFloat(rate, 64)
			taxF, _   := strconv.ParseFloat(taxAmt, 64)
			if qtyF <= 0  { return parentName, errors.New("qty must be positive") }
			if rateF < 0  { return parentName, errors.New("rate must be ≥ 0") }
			if taxF < 0   { return parentName, errors.New("tax_amount must be ≥ 0") }

			amount := qtyF * rateF
			total  := amount + taxF
			baseAmount := amount * exF
			baseTax    := taxF * exF
			baseTotal  := baseAmount + baseTax

			// 5) Allocate row_index.
			var nextIdx int
			if err := tx.QueryRow(ctx,
				`SELECT coalesce(max(row_index), 0) + 1 FROM purchase_invoice_item WHERE purchase_invoice_id = $1`,
				parentID).Scan(&nextIdx); err != nil {
				return parentName, err
			}

			// 6) Insert line.
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_invoice_item (
				  id, purchase_invoice_id, row_index,
				  item_id, item_code, item_name, description,
				  qty, uom, rate, amount,
				  expense_account_id, tax_amount, total,
				  base_amount, base_tax_amount, base_total)
				VALUES ($1,$2,$3, $4,$5,$6,$7, $8,$9,$10,$11, $12,$13,$14, $15,$16,$17)`,
				dbx.NewIDWithPrefix("piline"), parentID, nextIdx,
				itemID, itemCode, itemName, nullable(strings.TrimSpace(row["description"])),
				qty, uom, rate, fmt.Sprintf("%.4f", amount),
				expenseAccountID, fmt.Sprintf("%.4f", taxF), fmt.Sprintf("%.4f", total),
				fmt.Sprintf("%.4f", baseAmount), fmt.Sprintf("%.4f", baseTax), fmt.Sprintf("%.4f", baseTotal)); err != nil {
				return parentName, err
			}

			// 7) Recompute parent totals from the full line set so they don't drift.
			//    Note: PI has separate purchase_invoice_tax rows for header-level taxes;
			//    we don't touch those here. Line-level tax_amount on lines is summed into
			//    total_taxes_and_charges, mirroring how the service computes new drafts.
			if _, err := tx.Exec(ctx, `
				UPDATE purchase_invoice p SET
				  net_total                    = sums.net,
				  total_taxes_and_charges      = sums.tax,
				  grand_total                  = sums.net + sums.tax,
				  outstanding_amount           = sums.net + sums.tax - p.paid_amount,
				  base_net_total               = sums.base_net,
				  base_total_taxes_and_charges = sums.base_tax,
				  base_grand_total             = sums.base_net + sums.base_tax,
				  base_outstanding_amount      = sums.base_net + sums.base_tax - p.base_paid_amount
				FROM (
				  SELECT
				    coalesce(sum(amount), 0)         AS net,
				    coalesce(sum(tax_amount), 0)     AS tax,
				    coalesce(sum(base_amount), 0)    AS base_net,
				    coalesce(sum(base_tax_amount),0) AS base_tax
				  FROM purchase_invoice_item WHERE purchase_invoice_id = $1
				) sums
				WHERE p.id = $1`, parentID); err != nil {
				return parentName, err
			}

			return fmt.Sprintf("%s · %s ×%g", parentName, itemCode, qtyF), nil
		},
	}
}

// ---------- sales_invoice_item (lines into existing draft SI) ----------

func salesInvoiceItemRecipe() Recipe {
	return Recipe{
		Doctype: "sales_invoice_item", Label: "Sales invoice — lines",
		Description: "Append line items to existing draft sales invoices. Multi-currency aware: " +
			"base amounts are computed from the parent's exchange_rate. Parent totals recomputed per row.",
		CompanyScoped: true,
		Fields: []FieldDef{
			{Key: "sales_invoice_name",     Label: "Sales invoice name",      Required: true, Type: "lookup",
				LookupHint: "Existing draft SI in this company (e.g. SI-2026-00042)"},
			{Key: "item_code",              Label: "Item code",               Required: true, Type: "lookup",
				LookupHint: "Existing item.code"},
			{Key: "income_account_number",  Label: "Income account number",   Required: true, Type: "lookup",
				LookupHint: "Existing account.account_number in this company"},
			{Key: "qty",                    Label: "Quantity",                Required: true, Type: "number"},
			{Key: "rate",                   Label: "Rate (txn currency)",     Required: true, Type: "number"},
			{Key: "tax_amount",             Label: "Tax amount",                              Type: "number", Description: "Optional, defaults to 0"},
			{Key: "uom",                    Label: "UOM",                                     Type: "text",   Description: "Optional, defaults to item's stock_uom"},
			{Key: "item_name",              Label: "Item name",                               Type: "text",   Description: "Optional, defaults to item master name"},
			{Key: "description",            Label: "Description",                             Type: "text"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, companyID, _ string, row map[string]string) (string, error) {
			parentName := strings.TrimSpace(row["sales_invoice_name"])
			itemCode   := strings.TrimSpace(row["item_code"])
			incomeNum  := strings.TrimSpace(row["income_account_number"])

			// 1) Parent + draft check + exchange rate.
			var (
				parentID  string
				docstatus int
				exchange  string
			)
			if err := tx.QueryRow(ctx, `
				SELECT id, docstatus, exchange_rate FROM sales_invoice
				WHERE company_id = $1 AND name = $2`, companyID, parentName).
				Scan(&parentID, &docstatus, &exchange); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("sales_invoice %q not found in this company", parentName)
				}
				return parentName, err
			}
			if docstatus != 0 {
				return parentName, fmt.Errorf("sales_invoice %q is %s; lines can only be added to drafts",
					parentName, docstatusName(docstatus))
			}
			exF, _ := strconv.ParseFloat(exchange, 64)
			if exF <= 0 {
				exF = 1
			}

			// 2) Item lookup.
			var (
				itemID, itemName, stockUom string
			)
			if err := tx.QueryRow(ctx,
				`SELECT id, name, stock_uom FROM item WHERE code = $1`, itemCode).
				Scan(&itemID, &itemName, &stockUom); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("item %q not found", itemCode)
				}
				return parentName, err
			}
			if explicit := strings.TrimSpace(row["item_name"]); explicit != "" {
				itemName = explicit
			}
			uom := strings.TrimSpace(row["uom"])
			if uom == "" {
				uom = stockUom
			}

			// 3) Income account lookup.
			var incomeAccountID string
			if err := tx.QueryRow(ctx,
				`SELECT id FROM account WHERE company_id = $1 AND account_number = $2`,
				companyID, incomeNum).Scan(&incomeAccountID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("income account %q not found in this company", incomeNum)
				}
				return parentName, err
			}

			// 4) Numerics.
			qty, err := parseDecimalString(row["qty"])
			if err != nil { return parentName, fmt.Errorf("qty: %w", err) }
			rate, err := parseDecimalString(row["rate"])
			if err != nil { return parentName, fmt.Errorf("rate: %w", err) }
			taxAmt, err := parseDecimalString(row["tax_amount"])
			if err != nil { return parentName, fmt.Errorf("tax_amount: %w", err) }
			qtyF, _  := strconv.ParseFloat(qty, 64)
			rateF, _ := strconv.ParseFloat(rate, 64)
			taxF, _  := strconv.ParseFloat(taxAmt, 64)
			if qtyF <= 0  { return parentName, errors.New("qty must be positive") }
			if rateF < 0  { return parentName, errors.New("rate must be ≥ 0") }
			if taxF < 0   { return parentName, errors.New("tax_amount must be ≥ 0") }

			amount := qtyF * rateF
			total  := amount + taxF
			baseAmount := amount * exF
			baseTax    := taxF * exF
			baseTotal  := baseAmount + baseTax

			// 5) Next row_index.
			var nextIdx int
			if err := tx.QueryRow(ctx,
				`SELECT coalesce(max(row_index), 0) + 1 FROM sales_invoice_item WHERE sales_invoice_id = $1`,
				parentID).Scan(&nextIdx); err != nil {
				return parentName, err
			}

			// 6) Insert line.
			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_invoice_item (
				  id, sales_invoice_id, row_index,
				  item_id, item_code, item_name, description,
				  qty, uom, rate, amount,
				  income_account_id, tax_amount, total,
				  base_amount, base_tax_amount, base_total)
				VALUES ($1,$2,$3, $4,$5,$6,$7, $8,$9,$10,$11, $12,$13,$14, $15,$16,$17)`,
				dbx.NewIDWithPrefix("siline"), parentID, nextIdx,
				itemID, itemCode, itemName, nullable(strings.TrimSpace(row["description"])),
				qty, uom, rate, fmt.Sprintf("%.4f", amount),
				incomeAccountID, fmt.Sprintf("%.4f", taxF), fmt.Sprintf("%.4f", total),
				fmt.Sprintf("%.4f", baseAmount), fmt.Sprintf("%.4f", baseTax), fmt.Sprintf("%.4f", baseTotal)); err != nil {
				return parentName, err
			}

			// 7) Recompute parent totals — including outstanding so AR-ageing stays correct.
			if _, err := tx.Exec(ctx, `
				UPDATE sales_invoice p SET
				  net_total                    = sums.net,
				  total_taxes_and_charges      = sums.tax,
				  grand_total                  = sums.net + sums.tax,
				  outstanding_amount           = sums.net + sums.tax - p.paid_amount,
				  base_net_total               = sums.base_net,
				  base_total_taxes_and_charges = sums.base_tax,
				  base_grand_total             = sums.base_net + sums.base_tax,
				  base_outstanding_amount      = sums.base_net + sums.base_tax - p.base_paid_amount
				FROM (
				  SELECT
				    coalesce(sum(amount), 0)         AS net,
				    coalesce(sum(tax_amount), 0)     AS tax,
				    coalesce(sum(base_amount), 0)    AS base_net,
				    coalesce(sum(base_tax_amount),0) AS base_tax
				  FROM sales_invoice_item WHERE sales_invoice_id = $1
				) sums
				WHERE p.id = $1`, parentID); err != nil {
				return parentName, err
			}

			return fmt.Sprintf("%s · %s ×%g", parentName, itemCode, qtyF), nil
		},
	}
}

// ---------- journal_entry_account (lines into existing draft JE) ----------

func journalEntryAccountRecipe() Recipe {
	return Recipe{
		Doctype: "journal_entry_account", Label: "Journal entry — lines",
		Description: "Append debit/credit rows to existing draft journal entries. " +
			"Each row must be exactly one of debit OR credit (not both); base columns are derived from the parent's exchange_rate. " +
			"Parent total_debit / total_credit are recomputed per row.",
		CompanyScoped: true,
		Fields: []FieldDef{
			{Key: "journal_entry_name", Label: "Journal entry name", Required: true, Type: "lookup",
				LookupHint: "Existing draft JE in this company (e.g. JE-2026-00112)"},
			{Key: "account_number",     Label: "Account number",     Required: true, Type: "lookup",
				LookupHint: "Existing account.account_number in this company"},
			{Key: "debit",              Label: "Debit",                              Type: "number", Description: "Mutually exclusive with credit"},
			{Key: "credit",             Label: "Credit",                             Type: "number", Description: "Mutually exclusive with debit"},
			{Key: "party_type",         Label: "Party type",                         Type: "select",
				Options: []string{"", "customer", "supplier", "employee"}},
			{Key: "party_id",           Label: "Party id",                           Type: "text", Description: "Required if party_type is set"},
			{Key: "reference",          Label: "Reference",                          Type: "text"},
		},
		Build: func(ctx context.Context, tx pgx.Tx, companyID, _ string, row map[string]string) (string, error) {
			parentName := strings.TrimSpace(row["journal_entry_name"])
			accountNum := strings.TrimSpace(row["account_number"])

			// 1) Parent + draft check + exchange rate.
			var (
				parentID  string
				docstatus int
				exchange  string
			)
			if err := tx.QueryRow(ctx, `
				SELECT id, docstatus, exchange_rate FROM journal_entry
				WHERE company_id = $1 AND name = $2`, companyID, parentName).
				Scan(&parentID, &docstatus, &exchange); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("journal_entry %q not found in this company", parentName)
				}
				return parentName, err
			}
			if docstatus != 0 {
				return parentName, fmt.Errorf("journal_entry %q is %s; lines can only be added to drafts",
					parentName, docstatusName(docstatus))
			}
			exF, _ := strconv.ParseFloat(exchange, 64)
			if exF <= 0 {
				exF = 1
			}

			// 2) Account lookup, company-scoped.
			var accountID string
			if err := tx.QueryRow(ctx,
				`SELECT id FROM account WHERE company_id = $1 AND account_number = $2`,
				companyID, accountNum).Scan(&accountID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return parentName, fmt.Errorf("account %q not found in this company", accountNum)
				}
				return parentName, err
			}

			// 3) Debit/credit XOR validation.
			debit, err := parseDecimalString(row["debit"])
			if err != nil { return parentName, fmt.Errorf("debit: %w", err) }
			credit, err := parseDecimalString(row["credit"])
			if err != nil { return parentName, fmt.Errorf("credit: %w", err) }
			debitF, _  := strconv.ParseFloat(debit, 64)
			creditF, _ := strconv.ParseFloat(credit, 64)
			if debitF < 0 || creditF < 0 {
				return parentName, errors.New("debit and credit must be ≥ 0")
			}
			if debitF == 0 && creditF == 0 {
				return parentName, errors.New("row must have a non-zero debit or credit")
			}
			if debitF > 0 && creditF > 0 {
				return parentName, errors.New("debit and credit are mutually exclusive — set exactly one")
			}

			// 4) Party validation: type + id come as a pair.
			partyType := strings.TrimSpace(strings.ToLower(row["party_type"]))
			partyID   := strings.TrimSpace(row["party_id"])
			if partyType != "" {
				switch partyType {
				case "customer", "supplier", "employee":
				default:
					return parentName, fmt.Errorf("party_type %q invalid (allowed: customer, supplier, employee)", partyType)
				}
				if partyID == "" {
					return parentName, errors.New("party_id required when party_type is set")
				}
			} else if partyID != "" {
				return parentName, errors.New("party_type required when party_id is set")
			}

			// 5) Base-currency mirrors. JE has one exchange rate per voucher, so the line's
			//    txn-currency and account-currency values are equal here (this importer
			//    doesn't model multi-currency account columns separately).
			baseDebit  := debitF * exF
			baseCredit := creditF * exF

			// 6) Allocate row_index.
			var nextIdx int
			if err := tx.QueryRow(ctx,
				`SELECT coalesce(max(row_index), 0) + 1 FROM journal_entry_account WHERE journal_entry_id = $1`,
				parentID).Scan(&nextIdx); err != nil {
				return parentName, err
			}

			// 7) Insert line. Columns: debit/credit (in base currency for the GL) and
			//    debit_in_account_currency/credit_in_account_currency (txn currency).
			//    See ledger/types.go for the convention.
			if _, err := tx.Exec(ctx, `
				INSERT INTO journal_entry_account (
				  id, journal_entry_id, row_index, account_id, party_type, party_id,
				  debit, credit, debit_in_account_currency, credit_in_account_currency, reference)
				VALUES ($1,$2,$3,$4,$5,$6, $7,$8,$9,$10,$11)`,
				dbx.NewIDWithPrefix("jeline"), parentID, nextIdx, accountID, nullable(partyType), nullable(partyID),
				fmt.Sprintf("%.4f", baseDebit), fmt.Sprintf("%.4f", baseCredit),
				debit, credit, nullable(strings.TrimSpace(row["reference"]))); err != nil {
				return parentName, err
			}

			// 8) Recompute parent totals. JE balance check happens on submit; here we just
			//    keep the header amounts truthful for the list view.
			if _, err := tx.Exec(ctx, `
				UPDATE journal_entry p SET
				  total_debit  = sums.dr,
				  total_credit = sums.cr
				FROM (
				  SELECT coalesce(sum(debit),  0) AS dr,
				         coalesce(sum(credit), 0) AS cr
				  FROM journal_entry_account WHERE journal_entry_id = $1
				) sums
				WHERE p.id = $1`, parentID); err != nil {
				return parentName, err
			}

			side := "Dr"
			amount := debitF
			if creditF > 0 {
				side = "Cr"
				amount = creditF
			}
			return fmt.Sprintf("%s · %s %s %g", parentName, accountNum, side, amount), nil
		},
	}
}

func docstatusName(ds int) string {
	switch ds {
	case 0: return "draft"
	case 1: return "submitted"
	case 2: return "cancelled"
	}
	return fmt.Sprintf("docstatus=%d", ds)
}

// ---------- helpers ----------

func slugify(s string) string {
	out := strings.ToLower(strings.TrimSpace(s))
	out = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '-', r == '_', r == '.':
			return '-'
		}
		return -1
	}, out)
	// collapse adjacent hyphens
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

func is16Digits(s string) bool {
	if len(s) != 16 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func validRootType(s string) bool {
	switch s {
	case "asset", "liability", "equity", "income", "expense":
		return true
	}
	return false
}
