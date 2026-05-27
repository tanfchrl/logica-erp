// Package pos implements POS Profile + POS Invoice.
//
// POS Invoice differs from Sales Invoice: it's paid immediately. Submit posts:
//   Dr Cash             grand_total
//   Cr <income_account> sum(amount per line)
//   Cr <tax.account>    tax (if pos_profile has a tax template)
//
// No AR is involved. The offline_key field lets a POS client supply an
// idempotency token; re-syncing the same key returns the existing record.
package pos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/tandigital/logica-erp/internal/accounting/taxtemplate"
	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/ledger"
	"github.com/tandigital/logica-erp/internal/platform/money"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/submittable"
	"github.com/tandigital/logica-erp/internal/platform/tax"
)

const (
	DoctypePOSProfile = "pos_profile"
	DoctypePOSInvoice = "pos_invoice"
	VoucherType       = "POS Invoice"
)

// ---- POS Profile ----

type POSProfile struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	CompanyID            string    `json:"company_id"`
	WarehouseID          string    `json:"warehouse_id,omitempty"`
	CashAccountID        string    `json:"cash_account_id"`
	DefaultCustomerID    string    `json:"default_customer_id,omitempty"`
	DefaultTaxTemplateID string    `json:"default_tax_template_id,omitempty"`
	IncomeAccountID      string    `json:"income_account_id,omitempty"`
	IsActive             bool      `json:"is_active"`
	CreatedAt            time.Time `json:"created_at"`
}

type POSProfileCreateInput struct {
	Name                 string `json:"name"`
	CompanyID            string `json:"company_id,omitempty"`
	WarehouseID          string `json:"warehouse_id,omitempty"`
	CashAccountID        string `json:"cash_account_id"`
	DefaultCustomerID    string `json:"default_customer_id,omitempty"`
	DefaultTaxTemplateID string `json:"default_tax_template_id,omitempty"`
	IncomeAccountID      string `json:"income_account_id,omitempty"`
}

// ---- POS Invoice ----

type POSInvoice struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	CompanyID            string             `json:"company_id"`
	POSProfileID         string             `json:"pos_profile_id"`
	CustomerID           string             `json:"customer_id"`
	PostingDate          time.Time          `json:"posting_date"`
	FiscalYearID         string             `json:"fiscal_year_id"`
	Currency             string             `json:"currency"`
	NetTotal             decimal.Decimal    `json:"net_total"`
	TotalTaxesAndCharges decimal.Decimal    `json:"total_taxes_and_charges"`
	GrandTotal           decimal.Decimal    `json:"grand_total"`
	PaidAmount           decimal.Decimal    `json:"paid_amount"`
	IncomeAccountID      string             `json:"income_account_id"`
	CashAccountID        string             `json:"cash_account_id"`
	IsOffline            bool               `json:"is_offline"`
	OfflineKey           string             `json:"offline_key,omitempty"`
	Docstatus            submittable.Status `json:"docstatus"`
	CreatedAt            time.Time          `json:"created_at"`
	Items                []POSInvoiceItem   `json:"items"`
}

type POSInvoiceItem struct {
	ID        string          `json:"id"`
	RowIndex  int             `json:"row_index"`
	ItemID    string          `json:"item_id,omitempty"`
	ItemCode  string          `json:"item_code"`
	ItemName  string          `json:"item_name"`
	Qty       decimal.Decimal `json:"qty"`
	UOM       string          `json:"uom"`
	Rate      decimal.Decimal `json:"rate"`
	Amount    decimal.Decimal `json:"amount"`
	TaxAmount decimal.Decimal `json:"tax_amount"`
	Total     decimal.Decimal `json:"total"`
}

type POSInvoiceCreateInput struct {
	CompanyID    string                       `json:"company_id,omitempty"`
	POSProfileID string                       `json:"pos_profile_id"`
	CustomerID   string                       `json:"customer_id,omitempty"`     // defaults from profile
	PostingDate  string                       `json:"posting_date,omitempty"`    // defaults to today
	OfflineKey   string                       `json:"offline_key,omitempty"`
	IsOffline    bool                         `json:"is_offline,omitempty"`
	Items        []POSInvoiceItemInput        `json:"items"`
}

type POSInvoiceItemInput struct {
	ItemID   string `json:"item_id,omitempty"`
	ItemCode string `json:"item_code,omitempty"`
	ItemName string `json:"item_name,omitempty"`
	Qty      string `json:"qty"`
	UOM      string `json:"uom,omitempty"`
	Rate     string `json:"rate"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) CreateProfile(ctx context.Context, in POSProfileCreateInput) (*POSProfile, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("pos_profile: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" || in.Name == "" || in.CashAccountID == "" {
		return nil, errors.New("pos_profile: company_id, name, cash_account_id required")
	}
	id := dbx.NewIDWithPrefix("pos")
	var prof POSProfile
	err := s.db.QueryRow(ctx, `
		INSERT INTO pos_profile (id, name, company_id, warehouse_id, cash_account_id,
		                         default_customer_id, default_tax_template_id, income_account_id,
		                         created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
		RETURNING id, name, company_id, coalesce(warehouse_id,''), cash_account_id,
		          coalesce(default_customer_id,''), coalesce(default_tax_template_id,''), coalesce(income_account_id,''),
		          is_active, created_at`,
		id, in.Name, in.CompanyID, nullable(in.WarehouseID), in.CashAccountID,
		nullable(in.DefaultCustomerID), nullable(in.DefaultTaxTemplateID), nullable(in.IncomeAccountID),
		p.UserID).
		Scan(&prof.ID, &prof.Name, &prof.CompanyID, &prof.WarehouseID, &prof.CashAccountID,
			&prof.DefaultCustomerID, &prof.DefaultTaxTemplateID, &prof.IncomeAccountID,
			&prof.IsActive, &prof.CreatedAt)
	return &prof, err
}

// CreateAndSubmit creates a POS Invoice in one shot and submits it (the typical POS flow).
// If offline_key is supplied and already exists for this profile, returns the existing record (idempotent).
func (s *Service) CreateAndSubmit(ctx context.Context, in POSInvoiceCreateInput) (*POSInvoice, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("pos_invoice: unauthenticated")
	}
	if in.CompanyID == "" {
		in.CompanyID = auth.CompanyFromContext(ctx)
	}
	if in.CompanyID == "" || in.POSProfileID == "" {
		return nil, errors.New("pos_invoice: company_id, pos_profile_id required")
	}
	if len(in.Items) == 0 {
		return nil, errors.New("pos_invoice.items: required")
	}
	pd := time.Now().UTC().Truncate(24 * time.Hour)
	if in.PostingDate != "" {
		t, err := time.Parse("2006-01-02", in.PostingDate)
		if err != nil {
			return nil, fmt.Errorf("posting_date: %w", err)
		}
		pd = t
	}

	id := dbx.NewIDWithPrefix("posi")
	var out POSInvoice
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Idempotency: if offline_key already exists for this profile, return existing.
		if in.OfflineKey != "" {
			var existingID string
			err := tx.QueryRow(ctx,
				`SELECT id FROM pos_invoice WHERE pos_profile_id = $1 AND offline_key = $2`,
				in.POSProfileID, in.OfflineKey).Scan(&existingID)
			if err == nil {
				loaded, err := loadInvoice(ctx, tx, existingID)
				if err != nil {
					return err
				}
				out = *loaded
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}

		var prof POSProfile
		err := tx.QueryRow(ctx, `
			SELECT id, company_id, coalesce(warehouse_id,''), cash_account_id,
			       coalesce(default_customer_id,''), coalesce(default_tax_template_id,''), coalesce(income_account_id,'')
			FROM pos_profile WHERE id = $1 AND is_active = true`, in.POSProfileID).
			Scan(&prof.ID, &prof.CompanyID, &prof.WarehouseID, &prof.CashAccountID,
				&prof.DefaultCustomerID, &prof.DefaultTaxTemplateID, &prof.IncomeAccountID)
		if err != nil {
			return fmt.Errorf("pos_profile lookup: %w", err)
		}

		customerID := in.CustomerID
		if customerID == "" {
			customerID = prof.DefaultCustomerID
		}
		if customerID == "" {
			return errors.New("pos_invoice.customer_id: required (or set default on profile)")
		}
		incomeAcct := prof.IncomeAccountID
		if incomeAcct == "" {
			if err := tx.QueryRow(ctx, `SELECT default_income_account_id FROM company WHERE id = $1`, in.CompanyID).Scan(&incomeAcct); err != nil || incomeAcct == "" {
				return errors.New("pos_invoice: income account required (set on profile or company)")
			}
		}

		var fyID, currency string
		if err := tx.QueryRow(ctx, `
			SELECT fy.id FROM fiscal_year fy
			JOIN fiscal_year_company fyc ON fyc.fiscal_year_id = fy.id
			WHERE fyc.company_id = $1 AND $2 BETWEEN fy.start_date AND fy.end_date
			ORDER BY fy.start_date DESC LIMIT 1`, in.CompanyID, pd).Scan(&fyID); err != nil {
			return fmt.Errorf("fiscal year: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT default_currency FROM company WHERE id = $1`, in.CompanyID).Scan(&currency); err != nil {
			return err
		}

		seriesID, pattern, err := pickSeries(ctx, tx, DoctypePOSInvoice, in.CompanyID)
		if err != nil {
			return err
		}
		name, err := naming.Next(ctx, tx, seriesID, pattern, pd, nil)
		if err != nil {
			return err
		}

		// Build draft items + tax calc.
		draftItems := make([]POSInvoiceItem, len(in.Items))
		calcLines := make([]tax.Line, len(in.Items))
		for i, ln := range in.Items {
			qty, err := decimal.NewFromString(strings.TrimSpace(ln.Qty))
			if err != nil || !qty.IsPositive() {
				return fmt.Errorf("items[%d].qty: must be > 0", i)
			}
			rate, err := decimal.NewFromString(strings.TrimSpace(ln.Rate))
			if err != nil || rate.IsNegative() {
				return fmt.Errorf("items[%d].rate: must be >= 0", i)
			}
			amount := qty.Mul(rate).Round(money.Precision)

			code, label, uom := ln.ItemCode, ln.ItemName, ln.UOM
			if ln.ItemID != "" {
				var c, n, u string
				if err := tx.QueryRow(ctx, `SELECT code, name, stock_uom FROM item WHERE id = $1`, ln.ItemID).Scan(&c, &n, &u); err == nil {
					if code == "" {
						code = c
					}
					if label == "" {
						label = n
					}
					if uom == "" {
						uom = u
					}
				}
			}
			if code == "" {
				return fmt.Errorf("items[%d].item_code: required", i)
			}
			if uom == "" {
				uom = "Unit"
			}
			rowID := dbx.NewIDWithPrefix("posii")
			draftItems[i] = POSInvoiceItem{
				ID: rowID, RowIndex: i + 1,
				ItemID: ln.ItemID, ItemCode: code, ItemName: label,
				Qty: qty, UOM: uom, Rate: rate, Amount: amount,
			}
			calcLines[i] = tax.Line{Key: rowID, NetAmount: amount}
		}

		var taxResult tax.Result
		if prof.DefaultTaxTemplateID != "" {
			tpl, err := taxtemplate.LoadForCalc(ctx, tx, prof.DefaultTaxTemplateID)
			if err != nil {
				return err
			}
			if !tpl.IsSales {
				return errors.New("pos: profile tax template must be a sales template")
			}
			taxResult, err = tax.Calculate(calcLines, tpl)
			if err != nil {
				return err
			}
		} else {
			taxResult = tax.Result{Lines: make([]tax.LineResult, len(calcLines))}
			netTotal := decimal.Zero
			for i, l := range calcLines {
				taxResult.Lines[i] = tax.LineResult{Key: l.Key, NetAmount: l.NetAmount, Total: l.NetAmount}
				netTotal = netTotal.Add(l.NetAmount)
			}
			taxResult.NetTotal = netTotal
			taxResult.GrandTotal = netTotal
		}

		// Merge tax back to items.
		taxByKey := map[string]decimal.Decimal{}
		for _, lr := range taxResult.Lines {
			taxByKey[lr.Key] = lr.TaxAmount
		}
		for i := range draftItems {
			t := taxByKey[draftItems[i].ID]
			draftItems[i].TaxAmount = t
			draftItems[i].Total = draftItems[i].Amount.Add(t)
		}

		net := taxResult.NetTotal
		taxTotal := taxResult.TaxTotal
		grand := taxResult.GrandTotal

		if _, err := tx.Exec(ctx, `
			INSERT INTO pos_invoice (
				id, name, company_id, pos_profile_id, customer_id, posting_date, fiscal_year_id, currency,
				net_total, total_taxes_and_charges, grand_total, paid_amount,
				base_net_total, base_grand_total,
				income_account_id, cash_account_id, is_offline, offline_key,
				docstatus, submitted_at, submitted_by, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$9,$11,$12,$13,$14,$15, 1, now(), $16, $16, $16)`,
			id, name, in.CompanyID, in.POSProfileID, customerID, pd, fyID, currency,
			net, taxTotal, grand,
			incomeAcct, prof.CashAccountID, in.IsOffline, nullable(in.OfflineKey),
			p.UserID); err != nil {
			return err
		}

		for _, it := range draftItems {
			if _, err := tx.Exec(ctx, `
				INSERT INTO pos_invoice_item (id, pos_invoice_id, row_index, item_id, item_code, item_name, qty, uom, rate, amount, tax_amount, total)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
				it.ID, id, it.RowIndex, nullable(it.ItemID), it.ItemCode, it.ItemName, it.Qty, it.UOM, it.Rate, it.Amount, it.TaxAmount, it.Total); err != nil {
				return err
			}
		}

		// GL: Dr Cash grand_total, Cr Income (sum of net per line), Cr Tax (per tax row).
		entries := []ledger.Entry{}
		var cashCur, incomeCur string
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, prof.CashAccountID).Scan(&cashCur); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, incomeAcct).Scan(&incomeCur); err != nil {
			return err
		}
		entries = append(entries, ledger.Entry{
			AccountID: prof.CashAccountID, Debit: grand, AccountCurrency: cashCur, DebitInAccountCurrency: grand, Remarks: name + " — cash",
		})
		entries = append(entries, ledger.Entry{
			AccountID: incomeAcct, Credit: net, AccountCurrency: incomeCur, CreditInAccountCurrency: net, Remarks: name + " — income",
		})
		for _, tr := range taxResult.TaxRows {
			if tr.TaxAmount.IsZero() {
				continue
			}
			var cur string
			if err := tx.QueryRow(ctx, `SELECT account_currency FROM account WHERE id = $1`, tr.AccountID).Scan(&cur); err != nil {
				return err
			}
			entries = append(entries, ledger.Entry{
				AccountID: tr.AccountID, Credit: tr.TaxAmount, AccountCurrency: cur, CreditInAccountCurrency: tr.TaxAmount,
				Remarks: name + " — " + tr.Description,
			})
		}

		v := ledger.Voucher{Type: VoucherType, ID: id, Name: name, CompanyID: in.CompanyID, PostingDate: pd, FiscalYearID: fyID, CreatedBy: p.UserID}
		if _, err := ledger.PostGL(ctx, tx, v, entries); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, DoctypePOSInvoice, id, p.UserID, audit.ActionSubmit, audit.Diff{After: in}); err != nil {
			return err
		}
		loaded, err := loadInvoice(ctx, tx, id)
		if err != nil {
			return err
		}
		out = *loaded
		return nil
	})
	return &out, err
}

// ---- helpers ----

func loadInvoice(ctx context.Context, tx pgx.Tx, id string) (*POSInvoice, error) {
	var (
		inv     POSInvoice
		offline *string
	)
	err := tx.QueryRow(ctx, `
		SELECT id, name, company_id, pos_profile_id, customer_id, posting_date, fiscal_year_id, currency,
		       net_total, total_taxes_and_charges, grand_total, paid_amount,
		       income_account_id, cash_account_id, is_offline, offline_key, docstatus, created_at
		FROM pos_invoice WHERE id = $1`, id).
		Scan(&inv.ID, &inv.Name, &inv.CompanyID, &inv.POSProfileID, &inv.CustomerID, &inv.PostingDate, &inv.FiscalYearID, &inv.Currency,
			&inv.NetTotal, &inv.TotalTaxesAndCharges, &inv.GrandTotal, &inv.PaidAmount,
			&inv.IncomeAccountID, &inv.CashAccountID, &inv.IsOffline, &offline, &inv.Docstatus, &inv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("pos_invoice %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if offline != nil {
		inv.OfflineKey = *offline
	}
	rows, err := tx.Query(ctx, `
		SELECT id, row_index, coalesce(item_id,''), item_code, item_name, qty, uom, rate, amount, tax_amount, total
		FROM pos_invoice_item WHERE pos_invoice_id = $1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it POSInvoiceItem
		if err := rows.Scan(&it.ID, &it.RowIndex, &it.ItemID, &it.ItemCode, &it.ItemName, &it.Qty, &it.UOM, &it.Rate, &it.Amount, &it.TaxAmount, &it.Total); err != nil {
			return nil, err
		}
		inv.Items = append(inv.Items, it)
	}
	return &inv, nil
}

func pickSeries(ctx context.Context, tx pgx.Tx, doctype, companyID string) (string, string, error) {
	var id, pat string
	err := tx.QueryRow(ctx, `
		SELECT id, pattern FROM naming_series
		WHERE doctype = $1 AND is_default = true AND (company_id = $2 OR company_id IS NULL)
		ORDER BY company_id NULLS LAST LIMIT 1`, doctype, companyID).Scan(&id, &pat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("no series for %s", doctype)
	}
	return id, pat, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-pos-profile",
		Method:        http.MethodPost,
		Path:          "/pos/profiles",
		Summary:       "Create a POS Profile",
		Tags:          []string{"POS"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *posProfCreateIn) (*posProfOut, error) {
		if err := h.Perm.Check(ctx, DoctypePOSProfile, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		prof, err := h.Service.CreateProfile(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &posProfOut{Body: *prof}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-and-submit-pos-invoice",
		Method:        http.MethodPost,
		Path:          "/pos/invoices",
		Summary:       "Create + submit a POS Invoice (cash in, income out)",
		Tags:          []string{"POS"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *posInvCreateIn) (*posInvOut, error) {
		if err := h.Perm.Check(ctx, DoctypePOSInvoice, permission.ActionSubmit); err != nil {
			return nil, httpx.MapError(err)
		}
		inv, err := h.Service.CreateAndSubmit(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &posInvOut{Body: *inv}, nil
	})
}

type (
	posProfCreateIn struct{ Body POSProfileCreateInput }
	posProfOut      struct{ Body POSProfile }
	posInvCreateIn  struct{ Body POSInvoiceCreateInput }
	posInvOut       struct{ Body POSInvoice }
)
