-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- WAREHOUSE (tree, per-company)
-- ============================================================================
CREATE TABLE warehouse (
  id              text PRIMARY KEY,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  name            text NOT NULL,
  code            text,
  parent_id       text REFERENCES warehouse(id),
  lft             integer,
  rgt             integer,
  is_group        boolean NOT NULL DEFAULT false,
  warehouse_type  text,                         -- 'finished_goods','raw_material','wip','transit', etc.
  account_id      text REFERENCES account(id),  -- the asset account this warehouse posts to (stock-in-hand)
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX warehouse_co_idx ON warehouse (company_id);
CREATE TRIGGER warehouse_touch BEFORE UPDATE ON warehouse
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- ITEM enhancements: valuation method, default warehouse
-- ============================================================================
ALTER TABLE item ADD COLUMN valuation_method text NOT NULL DEFAULT 'FIFO'
  CHECK (valuation_method IN ('FIFO','MovingAverage','LIFO'));

ALTER TABLE item_default ADD COLUMN default_warehouse_id text REFERENCES warehouse(id);
ALTER TABLE item_default ADD COLUMN cost_of_goods_account_id text REFERENCES account(id);

-- Now wire stock_ledger_entry FKs that were left unattached in migration 0001.
ALTER TABLE stock_ledger_entry
  ADD CONSTRAINT sle_item_fk      FOREIGN KEY (item_id)      REFERENCES item(id),
  ADD CONSTRAINT sle_warehouse_fk FOREIGN KEY (warehouse_id) REFERENCES warehouse(id);

-- ============================================================================
-- STOCK ENTRY
-- ============================================================================
CREATE TABLE stock_entry (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  posting_date    date NOT NULL,
  posting_time    time NOT NULL DEFAULT '00:00:00',
  purpose         text NOT NULL CHECK (purpose IN ('material_receipt','material_issue','material_transfer','manufacture','repack')),
  fiscal_year_id  text NOT NULL REFERENCES fiscal_year(id),
  -- For manufacture: link to the work_order (FK added in Phase 4 migration)
  work_order_id   text,
  total_outgoing_value numeric(18,4) NOT NULL DEFAULT 0,
  total_incoming_value numeric(18,4) NOT NULL DEFAULT 0,
  remarks         text,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at    timestamptz,
  submitted_by    text REFERENCES users(id),
  cancelled_at    timestamptz,
  cancelled_by    text REFERENCES users(id),
  amended_from    text REFERENCES stock_entry(id),
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX stock_entry_co_date_idx ON stock_entry (company_id, posting_date);
CREATE TRIGGER stock_entry_touch BEFORE UPDATE ON stock_entry
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE stock_entry_item (
  id                 text PRIMARY KEY,
  stock_entry_id     text NOT NULL REFERENCES stock_entry(id) ON DELETE CASCADE,
  row_index          integer NOT NULL,
  item_id            text NOT NULL REFERENCES item(id),
  qty                numeric(18,6) NOT NULL,             -- always positive
  uom                text NOT NULL,
  source_warehouse_id text REFERENCES warehouse(id),    -- NULL for material_receipt
  target_warehouse_id text REFERENCES warehouse(id),    -- NULL for material_issue
  basic_rate         numeric(18,6),                      -- per-unit incoming rate; user-supplied for receipts
  basic_amount       numeric(18,4),                      -- qty * basic_rate
  valuation_rate     numeric(18,6),                      -- per-unit (computed at submit for outgoing)
  amount             numeric(18,4),                      -- valuation_rate * qty
  cost_center_id     text REFERENCES cost_center(id),
  expense_account_id text REFERENCES account(id),        -- used for material_issue (debit side)
  UNIQUE (stock_entry_id, row_index),
  CHECK (qty > 0)
);
CREATE INDEX sei_se_idx ON stock_entry_item (stock_entry_id);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_se', 'stock_entry', NULL, 'STE-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_se';
DROP TABLE IF EXISTS stock_entry_item;
DROP TABLE IF EXISTS stock_entry;
ALTER TABLE stock_ledger_entry
  DROP CONSTRAINT IF EXISTS sle_warehouse_fk,
  DROP CONSTRAINT IF EXISTS sle_item_fk;
ALTER TABLE item_default DROP COLUMN IF EXISTS cost_of_goods_account_id;
ALTER TABLE item_default DROP COLUMN IF EXISTS default_warehouse_id;
ALTER TABLE item DROP COLUMN IF EXISTS valuation_method;
DROP TABLE IF EXISTS warehouse;
-- +goose StatementEnd
