-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- BOM (single-level for Phase 4 MVP; multi-level via chained BOMs is additive)
-- ============================================================================
CREATE TABLE bom (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  item_id         text NOT NULL REFERENCES item(id),       -- the finished good
  quantity        numeric(18,6) NOT NULL DEFAULT 1,        -- output qty per BOM run
  uom             text NOT NULL,
  is_active       boolean NOT NULL DEFAULT true,
  is_default      boolean NOT NULL DEFAULT false,
  total_cost      numeric(18,4) NOT NULL DEFAULT 0,         -- sum of input costs at submit time
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at    timestamptz,
  submitted_by    text REFERENCES users(id),
  cancelled_at    timestamptz,
  cancelled_by    text REFERENCES users(id),
  amended_from    text REFERENCES bom(id),
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX bom_item_idx ON bom (item_id, is_active);
CREATE TRIGGER bom_touch BEFORE UPDATE ON bom
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE bom_item (
  id          text PRIMARY KEY,
  bom_id      text NOT NULL REFERENCES bom(id) ON DELETE CASCADE,
  row_index   integer NOT NULL,
  item_id     text NOT NULL REFERENCES item(id),
  qty         numeric(18,6) NOT NULL,
  uom         text NOT NULL,
  rate        numeric(18,4) NOT NULL DEFAULT 0,     -- snapshot cost per unit at BOM submit time
  amount      numeric(18,4) NOT NULL DEFAULT 0,     -- qty * rate
  UNIQUE (bom_id, row_index),
  CHECK (qty > 0)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_bom', 'bom', NULL, 'BOM-.YYYY.-.####', true);

-- ============================================================================
-- WORK ORDER
-- ============================================================================
CREATE TABLE work_order (
  id                   text PRIMARY KEY,
  name                 text NOT NULL,
  company_id           text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  bom_id               text NOT NULL REFERENCES bom(id),
  item_id              text NOT NULL REFERENCES item(id),     -- finished item (denormalised)
  qty                  numeric(18,6) NOT NULL,                 -- to manufacture
  source_warehouse_id  text NOT NULL REFERENCES warehouse(id), -- raw materials from
  target_warehouse_id  text NOT NULL REFERENCES warehouse(id), -- finished goods to
  status               text NOT NULL DEFAULT 'Draft',         -- Draft|In Process|Completed|Cancelled
  produced_qty         numeric(18,6) NOT NULL DEFAULT 0,
  planned_start_date   date,
  planned_end_date     date,
  actual_start_date    date,
  actual_end_date      date,
  total_cost           numeric(18,4) NOT NULL DEFAULT 0,
  docstatus            smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at         timestamptz,
  submitted_by         text REFERENCES users(id),
  cancelled_at         timestamptz,
  cancelled_by         text REFERENCES users(id),
  amended_from         text REFERENCES work_order(id),
  custom_fields        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  created_by           text NOT NULL REFERENCES users(id),
  updated_by           text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name),
  CHECK (qty > 0)
);
CREATE INDEX wo_co_idx ON work_order (company_id, status);
CREATE TRIGGER wo_touch BEFORE UPDATE ON work_order
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- Now wire the FK from stock_entry.work_order_id (deferred from migration 0008).
ALTER TABLE stock_entry
  ADD CONSTRAINT stock_entry_work_order_fk FOREIGN KEY (work_order_id) REFERENCES work_order(id);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_wo', 'work_order', NULL, 'WO-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id IN ('nms_default_bom','nms_default_wo');
ALTER TABLE stock_entry DROP CONSTRAINT IF EXISTS stock_entry_work_order_fk;
DROP TABLE IF EXISTS work_order;
DROP TABLE IF EXISTS bom_item;
DROP TABLE IF EXISTS bom;
-- +goose StatementEnd
