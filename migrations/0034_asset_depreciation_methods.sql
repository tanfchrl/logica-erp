-- +goose Up
-- +goose StatementBegin

-- 0034: extend asset to support pro-rata + WDV depreciation
--
-- New columns on asset:
--   - pro_rata_basis (bool, default true) — first row is partial-month
--   - depreciation_rate_pct (numeric, optional) — annual % for WDV; if null,
--     the service derives it from gross/salvage/useful_life.
--
-- Also widens the asset_category check constraint to admit the same set
-- of methods the service accepts.

ALTER TABLE asset
  ADD COLUMN pro_rata_basis        boolean NOT NULL DEFAULT true,
  ADD COLUMN depreciation_rate_pct numeric(10,4);

ALTER TABLE asset_category
  DROP CONSTRAINT asset_category_default_depreciation_method_check;
ALTER TABLE asset_category
  ADD CONSTRAINT asset_category_default_depreciation_method_check
  CHECK (default_depreciation_method IN
    ('straight_line','written_down_value','declining_balance','manual'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE asset_category
  DROP CONSTRAINT asset_category_default_depreciation_method_check;
ALTER TABLE asset_category
  ADD CONSTRAINT asset_category_default_depreciation_method_check
  CHECK (default_depreciation_method IN ('straight_line','declining_balance'));

ALTER TABLE asset
  DROP COLUMN IF EXISTS pro_rata_basis,
  DROP COLUMN IF EXISTS depreciation_rate_pct;

-- +goose StatementEnd
