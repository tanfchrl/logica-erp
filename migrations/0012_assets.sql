-- +goose Up
-- +goose StatementBegin

CREATE TABLE asset_category (
  id                          text PRIMARY KEY,
  name                        text NOT NULL UNIQUE,
  default_depreciation_method text NOT NULL DEFAULT 'straight_line'
    CHECK (default_depreciation_method IN ('straight_line','declining_balance')),
  total_useful_life_months    integer NOT NULL DEFAULT 60,
  asset_account_id            text REFERENCES account(id),
  accumulated_depreciation_account_id text REFERENCES account(id),
  depreciation_expense_account_id     text REFERENCES account(id),
  is_deleted boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE asset (
  id                              text PRIMARY KEY,
  name                            text NOT NULL,
  company_id                      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  asset_name                      text NOT NULL,
  asset_category_id               text REFERENCES asset_category(id),
  purchase_date                   date NOT NULL,
  gross_purchase_amount           numeric(18,4) NOT NULL,
  expected_value_after_useful_life numeric(18,4) NOT NULL DEFAULT 0,
  useful_life_months              integer NOT NULL,
  depreciation_method             text NOT NULL DEFAULT 'straight_line',
  asset_account_id                text NOT NULL REFERENCES account(id),
  accumulated_depreciation_account_id text NOT NULL REFERENCES account(id),
  depreciation_expense_account_id     text NOT NULL REFERENCES account(id),
  cost_center_id                  text REFERENCES cost_center(id),
  accumulated_depreciation        numeric(18,4) NOT NULL DEFAULT 0,
  status                          text NOT NULL DEFAULT 'Draft',  -- Draft|Submitted|Partially Depreciated|Fully Depreciated|Sold|Scrapped|Cancelled
  next_depreciation_date          date,
  docstatus                       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at                    timestamptz,
  submitted_by                    text REFERENCES users(id),
  cancelled_at                    timestamptz,
  cancelled_by                    text REFERENCES users(id),
  amended_from                    text REFERENCES asset(id),
  custom_fields                   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at                      timestamptz NOT NULL DEFAULT now(),
  updated_at                      timestamptz NOT NULL DEFAULT now(),
  created_by                      text NOT NULL REFERENCES users(id),
  updated_by                      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name),
  CHECK (useful_life_months > 0),
  CHECK (gross_purchase_amount > 0)
);
CREATE INDEX asset_status_idx ON asset (company_id, status);
CREATE TRIGGER asset_touch BEFORE UPDATE ON asset
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE depreciation_schedule (
  id            text PRIMARY KEY,
  asset_id      text NOT NULL REFERENCES asset(id) ON DELETE CASCADE,
  row_index     integer NOT NULL,
  schedule_date date NOT NULL,
  depreciation_amount   numeric(18,4) NOT NULL,
  accumulated_after     numeric(18,4) NOT NULL,
  is_posted             boolean NOT NULL DEFAULT false,
  posted_voucher_id     text,                     -- gl_entry voucher group
  posted_at             timestamptz,
  UNIQUE (asset_id, row_index)
);
CREATE INDEX dep_sched_asset_idx ON depreciation_schedule (asset_id, schedule_date);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_asset', 'asset', NULL, 'ASSET-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_asset';
DROP TABLE IF EXISTS depreciation_schedule;
DROP TABLE IF EXISTS asset;
DROP TABLE IF EXISTS asset_category;
-- +goose StatementEnd
