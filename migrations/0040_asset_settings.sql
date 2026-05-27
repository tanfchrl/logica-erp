-- +goose Up
-- +goose StatementBegin

-- 0040: Asset Settings per-company singleton
--
-- Mirrors buying_settings — one row per company; service guarantees that
-- via UNIQUE(company_id) + UPSERT semantics.

CREATE TABLE asset_settings (
  id                              text PRIMARY KEY,
  company_id                      text NOT NULL UNIQUE REFERENCES company(id) ON DELETE CASCADE,
  -- When false, PI submit's auto-create hook is silently skipped even if
  -- an item has is_fixed_asset=true. Lets a company turn off auto-create
  -- without editing each item.
  auto_create_assets_from_pi      boolean NOT NULL DEFAULT true,
  -- When set, the asset detail page suggests attaching this finance book
  -- to every new asset on submit. Service can use it later to auto-attach.
  default_finance_book_id         text REFERENCES finance_book(id),
  -- Asset Settings → Fixed Asset Register defaults.
  register_show_zero_nbv          boolean NOT NULL DEFAULT false,
  register_group_by               text    NOT NULL DEFAULT 'category'
                                   CHECK (register_group_by IN ('category','status','location','none')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  text REFERENCES users(id)
);
CREATE TRIGGER asset_settings_touch BEFORE UPDATE ON asset_settings
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS asset_settings;
-- +goose StatementEnd
