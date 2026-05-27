// Package item implements the Item master (services only for Phase 1A; stock
// items come in Phase 2 with warehouses + valuation + variants).
package item

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

	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/customfield"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/permission"
)

const Doctype = "item"

type Item struct {
	ID              string          `json:"id"`
	Code            string          `json:"code"`
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	ItemGroupID     string          `json:"item_group_id,omitempty"`
	StockUOM        string          `json:"stock_uom"`
	IsStockItem     bool            `json:"is_stock_item"`
	IsSalesItem     bool            `json:"is_sales_item"`
	IsPurchaseItem  bool            `json:"is_purchase_item"`
	IsFixedAsset    bool            `json:"is_fixed_asset"`
	AssetCategoryID string          `json:"asset_category_id,omitempty"`
	StandardRate    decimal.Decimal `json:"standard_rate"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Defaults        []ItemDefault   `json:"defaults,omitempty"`
}

type ItemDefault struct {
	CompanyID                  string `json:"company_id"`
	DefaultIncomeAccountID     string `json:"default_income_account_id,omitempty"`
	DefaultExpenseAccountID    string `json:"default_expense_account_id,omitempty"`
	DefaultTaxTemplateID       string `json:"default_tax_template_id,omitempty"`
}

type ItemCreateInput struct {
	Code            string             `json:"code"`
	Name            string             `json:"name"`
	Description     string             `json:"description,omitempty"`
	ItemGroupID     string             `json:"item_group_id,omitempty"`
	StockUOM        string             `json:"stock_uom,omitempty"`
	IsStockItem     bool               `json:"is_stock_item,omitempty"`
	IsSalesItem     *bool              `json:"is_sales_item,omitempty"`
	IsPurchaseItem  *bool              `json:"is_purchase_item,omitempty"`
	IsFixedAsset    bool               `json:"is_fixed_asset,omitempty"`
	AssetCategoryID string             `json:"asset_category_id,omitempty"`
	StandardRate    string             `json:"standard_rate,omitempty"`
	CustomFields    map[string]any     `json:"custom_fields,omitempty"`
	Defaults        []ItemDefaultInput `json:"defaults,omitempty"`
}

type ItemDefaultInput struct {
	CompanyID                  string `json:"company_id"`
	DefaultIncomeAccountID     string `json:"default_income_account_id,omitempty"`
	DefaultExpenseAccountID    string `json:"default_expense_account_id,omitempty"`
	DefaultTaxTemplateID       string `json:"default_tax_template_id,omitempty"`
}

type Service struct{ db *dbx.DB }

func NewService(db *dbx.DB) *Service { return &Service{db: db} }

func (s *Service) Create(ctx context.Context, in ItemCreateInput) (*Item, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("item: unauthenticated")
	}
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if in.Code == "" {
		return nil, errors.New("item.code: required")
	}
	if in.Name == "" {
		return nil, errors.New("item.name: required")
	}
	if in.StockUOM == "" {
		in.StockUOM = "Unit"
	}
	rate := decimal.Zero
	if in.StandardRate != "" {
		r, err := decimal.NewFromString(in.StandardRate)
		if err != nil {
			return nil, fmt.Errorf("item.standard_rate: %w", err)
		}
		if r.IsNegative() {
			return nil, errors.New("item.standard_rate: must be >= 0")
		}
		rate = r.Round(4)
	}
	isSales := true
	if in.IsSalesItem != nil {
		isSales = *in.IsSalesItem
	}
	isPurchase := true
	if in.IsPurchaseItem != nil {
		isPurchase = *in.IsPurchaseItem
	}

	id := dbx.NewIDWithPrefix("itm")
	var it Item
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		var categoryOut *string
		err = tx.QueryRow(ctx, `
			INSERT INTO item (id, code, name, description, item_group_id, stock_uom,
			                 is_stock_item, is_sales_item, is_purchase_item,
			                 is_fixed_asset, asset_category_id,
			                 standard_rate,
			                 custom_fields, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)
			RETURNING id, code, name, coalesce(description,''), coalesce(item_group_id,''),
			          stock_uom, is_stock_item, is_sales_item, is_purchase_item,
			          is_fixed_asset, asset_category_id,
			          standard_rate,
			          created_at, updated_at`,
			id, in.Code, in.Name, nullable(in.Description), nullable(in.ItemGroupID), in.StockUOM,
			in.IsStockItem, isSales, isPurchase,
			in.IsFixedAsset, nullable(in.AssetCategoryID),
			rate, cf, p.UserID).Scan(
			&it.ID, &it.Code, &it.Name, &it.Description, &it.ItemGroupID,
			&it.StockUOM, &it.IsStockItem, &it.IsSalesItem, &it.IsPurchaseItem,
			&it.IsFixedAsset, &categoryOut,
			&it.StandardRate,
			&it.CreatedAt, &it.UpdatedAt)
		if categoryOut != nil {
			it.AssetCategoryID = *categoryOut
		}
		if err != nil {
			if dbx.IsUniqueViolation(err) {
				return errors.New("item: duplicate code")
			}
			return err
		}
		for _, d := range in.Defaults {
			if d.CompanyID == "" {
				return errors.New("item_default.company_id: required")
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO item_default (item_id, company_id, default_income_account_id, default_expense_account_id, default_tax_template_id)
				VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (item_id, company_id) DO UPDATE SET
				  default_income_account_id  = EXCLUDED.default_income_account_id,
				  default_expense_account_id = EXCLUDED.default_expense_account_id,
				  default_tax_template_id    = EXCLUDED.default_tax_template_id`,
				it.ID, d.CompanyID, nullable(d.DefaultIncomeAccountID), nullable(d.DefaultExpenseAccountID), nullable(d.DefaultTaxTemplateID))
			if err != nil {
				return err
			}
			it.Defaults = append(it.Defaults, ItemDefault{
				CompanyID:               d.CompanyID,
				DefaultIncomeAccountID:  d.DefaultIncomeAccountID,
				DefaultExpenseAccountID: d.DefaultExpenseAccountID,
				DefaultTaxTemplateID:    d.DefaultTaxTemplateID,
			})
		}
		return audit.Record(ctx, tx, Doctype, it.ID, p.UserID, audit.ActionCreate, audit.Diff{After: in})
	})
	return &it, err
}

// ItemUpdateInput mirrors ItemCreateInput. Code is immutable (it's the natural
// key referenced by transactions); everything else — name, group, UOM,
// valuation method, standard rate, flags, custom fields, and per-company
// defaults — can be edited freely. Sub-tables are replaced wholesale (delete
// then re-insert) inside the same tx; we do not attempt to diff them.
type ItemUpdateInput struct {
	Name            string             `json:"name"`
	Description     string             `json:"description,omitempty"`
	ItemGroupID     string             `json:"item_group_id,omitempty"`
	StockUOM        string             `json:"stock_uom,omitempty"`
	IsStockItem     bool               `json:"is_stock_item,omitempty"`
	IsSalesItem     *bool              `json:"is_sales_item,omitempty"`
	IsPurchaseItem  *bool              `json:"is_purchase_item,omitempty"`
	IsFixedAsset    bool               `json:"is_fixed_asset,omitempty"`
	AssetCategoryID string             `json:"asset_category_id,omitempty"`
	StandardRate    string             `json:"standard_rate,omitempty"`
	CustomFields    map[string]any     `json:"custom_fields,omitempty"`
	Defaults        []ItemDefaultInput `json:"defaults,omitempty"`
}

func (s *Service) Update(ctx context.Context, id string, in ItemUpdateInput) (*Item, error) {
	p := auth.FromContext(ctx)
	if p == nil {
		return nil, errors.New("item: unauthenticated")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("item.name: required")
	}
	if in.StockUOM == "" {
		in.StockUOM = "Unit"
	}
	rate := decimal.Zero
	if in.StandardRate != "" {
		r, err := decimal.NewFromString(in.StandardRate)
		if err != nil {
			return nil, fmt.Errorf("item.standard_rate: %w", err)
		}
		if r.IsNegative() {
			return nil, errors.New("item.standard_rate: must be >= 0")
		}
		rate = r.Round(4)
	}
	isSales := true
	if in.IsSalesItem != nil {
		isSales = *in.IsSalesItem
	}
	isPurchase := true
	if in.IsPurchaseItem != nil {
		isPurchase = *in.IsPurchaseItem
	}

	var before Item
	if err := s.db.QueryRow(ctx, `SELECT id, code FROM item WHERE id = $1 AND is_deleted = false`, id).
		Scan(&before.ID, &before.Code); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("item %s not found", id)
		}
		return nil, err
	}

	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		cf, err := customfield.EnsureTxValidator(ctx, tx, Doctype, in.CustomFields)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE item SET
			  name              = $2,
			  description       = $3,
			  item_group_id     = $4,
			  stock_uom         = $5,
			  is_stock_item     = $6,
			  is_sales_item     = $7,
			  is_purchase_item  = $8,
			  is_fixed_asset    = $9,
			  asset_category_id = $10,
			  standard_rate     = $11,
			  custom_fields     = $12,
			  updated_by        = $13,
			  updated_at        = now()
			WHERE id = $1 AND is_deleted = false`,
			id, in.Name, nullable(in.Description), nullable(in.ItemGroupID), in.StockUOM,
			in.IsStockItem, isSales, isPurchase,
			in.IsFixedAsset, nullable(in.AssetCategoryID),
			rate, cf, p.UserID)
		if err != nil {
			return err
		}
		// Replace sub-table rows wholesale: delete then re-insert within the tx.
		// item_price and item_uom tables don't exist in the current schema; if
		// they get added later, mirror this same delete-then-insert pattern.
		if _, err := tx.Exec(ctx, `DELETE FROM item_default WHERE item_id = $1`, id); err != nil {
			return err
		}
		for _, d := range in.Defaults {
			if d.CompanyID == "" {
				return errors.New("item_default.company_id: required")
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO item_default (item_id, company_id, default_income_account_id, default_expense_account_id, default_tax_template_id)
				VALUES ($1,$2,$3,$4,$5)`,
				id, d.CompanyID, nullable(d.DefaultIncomeAccountID), nullable(d.DefaultExpenseAccountID), nullable(d.DefaultTaxTemplateID)); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, Doctype, id, p.UserID, audit.ActionUpdate, audit.Diff{After: in})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*Item, error) {
	var it Item
	var categoryOut *string
	err := s.db.QueryRow(ctx, `
		SELECT id, code, name, coalesce(description,''), coalesce(item_group_id,''),
		       stock_uom, is_stock_item, is_sales_item, is_purchase_item,
		       is_fixed_asset, asset_category_id,
		       standard_rate, created_at, updated_at
		FROM item WHERE id = $1 AND is_deleted = false`, id).
		Scan(&it.ID, &it.Code, &it.Name, &it.Description, &it.ItemGroupID,
			&it.StockUOM, &it.IsStockItem, &it.IsSalesItem, &it.IsPurchaseItem,
			&it.IsFixedAsset, &categoryOut,
			&it.StandardRate,
			&it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("item %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if categoryOut != nil {
		it.AssetCategoryID = *categoryOut
	}
	rows, err := s.db.Query(ctx, `
		SELECT company_id, coalesce(default_income_account_id,''), coalesce(default_expense_account_id,''),
		       coalesce(default_tax_template_id,'')
		FROM item_default WHERE item_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d ItemDefault
		if err := rows.Scan(&d.CompanyID, &d.DefaultIncomeAccountID, &d.DefaultExpenseAccountID, &d.DefaultTaxTemplateID); err != nil {
			return nil, err
		}
		it.Defaults = append(it.Defaults, d)
	}
	return &it, rows.Err()
}

func (s *Service) List(ctx context.Context) ([]Item, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, code, name, coalesce(description,''), coalesce(item_group_id,''),
		       stock_uom, is_stock_item, is_sales_item, is_purchase_item,
		       is_fixed_asset, coalesce(asset_category_id, ''),
		       standard_rate, created_at, updated_at
		FROM item WHERE is_deleted = false ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Code, &it.Name, &it.Description, &it.ItemGroupID,
			&it.StockUOM, &it.IsStockItem, &it.IsSalesItem, &it.IsPurchaseItem,
			&it.IsFixedAsset, &it.AssetCategoryID,
			&it.StandardRate,
			&it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ResolveDefault returns the per-company default for the item.
func (s *Service) ResolveDefault(ctx context.Context, itemID, companyID string) (ItemDefault, error) {
	var d ItemDefault
	d.CompanyID = companyID
	err := s.db.QueryRow(ctx, `
		SELECT coalesce(default_income_account_id,''),
		       coalesce(default_expense_account_id,''),
		       coalesce(default_tax_template_id,'')
		FROM item_default WHERE item_id = $1 AND company_id = $2`,
		itemID, companyID).Scan(&d.DefaultIncomeAccountID, &d.DefaultExpenseAccountID, &d.DefaultTaxTemplateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	}
	return d, err
}

// ---- HTTP ----

type Handler struct {
	Service *Service
	Perm    *permission.Engine
}

func Register(api huma.API, h *Handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-items",
		Method:      http.MethodGet,
		Path:        "/accounting/items",
		Summary:     "List items",
		Tags:        []string{"Accounting / Item"},
	}, func(ctx context.Context, _ *struct{}) (*itemListOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		its, err := h.Service.List(ctx)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &itemListOut{Body: itemListBody{Items: its}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID:   "create-item",
		Method:        http.MethodPost,
		Path:          "/accounting/items",
		Summary:       "Create an item",
		Tags:          []string{"Accounting / Item"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *itemCreateIn) (*itemCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionCreate); err != nil {
			return nil, httpx.MapError(err)
		}
		it, err := h.Service.Create(ctx, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &itemCreateOut{Body: *it}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "get-item",
		Method:      http.MethodGet,
		Path:        "/accounting/items/{id}",
		Summary:     "Get an item",
		Tags:        []string{"Accounting / Item"},
	}, func(ctx context.Context, in *itemGetIn) (*itemCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionRead); err != nil {
			return nil, httpx.MapError(err)
		}
		it, err := h.Service.Get(ctx, in.ID)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &itemCreateOut{Body: *it}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "update-item",
		Method:      http.MethodPut,
		Path:        "/accounting/items/{id}",
		Summary:     "Update an item",
		Tags:        []string{"Accounting / Item"},
	}, func(ctx context.Context, in *itemUpdateIn) (*itemCreateOut, error) {
		if err := h.Perm.Check(ctx, Doctype, permission.ActionWrite); err != nil {
			return nil, httpx.MapError(err)
		}
		it, err := h.Service.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, httpx.MapError(err)
		}
		return &itemCreateOut{Body: *it}, nil
	})
}

type (
	itemCreateIn  struct{ Body ItemCreateInput }
	itemCreateOut struct{ Body Item }
	itemListOut   struct{ Body itemListBody }
	itemListBody  struct {
		Items []Item `json:"items"`
	}
	itemGetIn struct {
		ID string `path:"id"`
	}
	itemUpdateIn struct {
		ID   string `path:"id"`
		Body ItemUpdateInput
	}
)

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
