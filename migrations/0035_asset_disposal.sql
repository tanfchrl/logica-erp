-- +goose Up
-- +goose StatementBegin

-- 0035: asset disposal columns
--
-- Captures the sale or scrap event. After disposal:
--   * acc dep + gross are cleared from the books
--   * any remaining unposted schedule rows are cancelled (Asset 3's batch
--     run skips them via the asset.status guard).
--
-- gain_account_id / loss_account_id default-fallback to company-level
-- accounts in the service; columns allow per-asset overrides.

ALTER TABLE asset
  ADD COLUMN disposed_at           timestamptz,
  ADD COLUMN disposed_by           text REFERENCES users(id),
  ADD COLUMN disposal_kind         text  CHECK (disposal_kind IN ('sale','scrap')),
  ADD COLUMN disposal_proceeds     numeric(18,4) NOT NULL DEFAULT 0
                                   CHECK (disposal_proceeds >= 0),
  ADD COLUMN disposal_voucher_id   text,
  ADD COLUMN gain_account_id       text REFERENCES account(id),
  ADD COLUMN loss_account_id       text REFERENCES account(id),
  ADD COLUMN disposal_cash_account_id text REFERENCES account(id);

-- Company-level gain/loss-on-disposal defaults. Per-asset override at the
-- columns above falls back to these when set.
ALTER TABLE company
  ADD COLUMN gain_on_disposal_account_id text REFERENCES account(id),
  ADD COLUMN loss_on_disposal_account_id text REFERENCES account(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE asset
  DROP COLUMN IF EXISTS disposed_at,
  DROP COLUMN IF EXISTS disposed_by,
  DROP COLUMN IF EXISTS disposal_kind,
  DROP COLUMN IF EXISTS disposal_proceeds,
  DROP COLUMN IF EXISTS disposal_voucher_id,
  DROP COLUMN IF EXISTS gain_account_id,
  DROP COLUMN IF EXISTS loss_account_id,
  DROP COLUMN IF EXISTS disposal_cash_account_id;

ALTER TABLE company
  DROP COLUMN IF EXISTS gain_on_disposal_account_id,
  DROP COLUMN IF EXISTS loss_on_disposal_account_id;
-- +goose StatementEnd
