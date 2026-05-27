-- +goose Up
-- +goose StatementBegin

-- 0036: mark items as fixed-asset capitalisations
--
-- When is_fixed_asset = true on an item:
--   * PI submit creates one draft asset per unit (qty = number of assets).
--   * The form copies asset_category_id forward; the category's defaults
--     (depreciation method, useful life, GL accounts) flow into each new
--     asset draft.

ALTER TABLE item
  ADD COLUMN is_fixed_asset    boolean NOT NULL DEFAULT false,
  ADD COLUMN asset_category_id text REFERENCES asset_category(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE item
  DROP COLUMN IF EXISTS is_fixed_asset,
  DROP COLUMN IF EXISTS asset_category_id;
-- +goose StatementEnd
