-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- ASSET VALUE ADJUSTMENT (revaluation up / impairment)
-- ============================================================================
-- PSAK 16 revaluation model + PSAK 36 impairment in one doctype:
--
--   kind = 'revaluation'      → positive amount; Dr Asset / Cr Revaluation Surplus
--   kind = 'impairment'       → positive amount; Dr Impairment Loss / Cr Accumulated Depreciation
--   kind = 'revaluation_down' → positive amount; Dr Revaluation Surplus / Cr Asset
--                               (use impairment when no surplus exists to offset)
--
-- Effects on asset balances (applied on Submit):
--   revaluation      → gross_purchase_amount += amount
--   revaluation_down → gross_purchase_amount -= amount
--   impairment       → accumulated_depreciation += amount
--                     (PSAK 36 lets you book impairment either as a
--                      contra-asset or as a write-down of gross; we pick
--                      the contra path so depreciation_schedule stays valid.)

CREATE TABLE asset_value_adjustment (
  id                          text PRIMARY KEY,
  name                        text NOT NULL,
  company_id                  text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  asset_id                    text NOT NULL REFERENCES asset(id) ON DELETE RESTRICT,
  adjustment_date             date NOT NULL,
  kind                        text NOT NULL CHECK (kind IN ('revaluation','impairment','revaluation_down')),
  amount                      numeric(18,4) NOT NULL CHECK (amount > 0),
  reason                      text NOT NULL DEFAULT '',
  -- GL leg targets — per-adjustment overrides; service falls back to
  -- company-level defaults below.
  revaluation_surplus_account_id text REFERENCES account(id),
  impairment_loss_account_id     text REFERENCES account(id),
  posted_voucher_id           text,
  docstatus                   smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at                timestamptz,
  submitted_by                text REFERENCES users(id),
  cancelled_at                timestamptz,
  cancelled_by                text REFERENCES users(id),
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  created_by                  text NOT NULL REFERENCES users(id),
  updated_by                  text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX ava_asset_idx ON asset_value_adjustment (asset_id, adjustment_date DESC);

-- Company-level default accounts for the two new legs. Per-adjustment
-- columns above fall back to these when empty.
ALTER TABLE company
  ADD COLUMN revaluation_surplus_account_id text REFERENCES account(id),
  ADD COLUMN impairment_loss_account_id     text REFERENCES account(id);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_ava', 'asset_value_adjustment', NULL, 'AVA-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_ava';
ALTER TABLE company
  DROP COLUMN IF EXISTS revaluation_surplus_account_id,
  DROP COLUMN IF EXISTS impairment_loss_account_id;
DROP TABLE IF EXISTS asset_value_adjustment;
-- +goose StatementEnd
