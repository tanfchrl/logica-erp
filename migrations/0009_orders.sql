-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- doc_link: generic parent→child fulfilment tracking
-- ============================================================================
CREATE TABLE doc_link (
  parent_doctype  text NOT NULL,
  parent_id       text NOT NULL,
  child_doctype   text NOT NULL,
  child_id        text NOT NULL,
  qty_linked      numeric(18,6),
  amount_linked   numeric(18,4),
  PRIMARY KEY (parent_doctype, parent_id, child_doctype, child_id)
);
CREATE INDEX doc_link_child_idx ON doc_link (child_doctype, child_id);

-- ============================================================================
-- PURCHASE ORDER
-- ============================================================================
CREATE TABLE purchase_order (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  supplier_id     text NOT NULL REFERENCES supplier(id),
  transaction_date date NOT NULL,
  required_by_date date,
  currency        text NOT NULL REFERENCES currency(code),
  exchange_rate   numeric(18,8) NOT NULL DEFAULT 1,
  total           numeric(18,4) NOT NULL DEFAULT 0,
  base_total      numeric(18,4) NOT NULL DEFAULT 0,
  status          text NOT NULL DEFAULT 'Draft',          -- Draft|To Receive|To Bill|Completed|Cancelled
  remarks         text,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at    timestamptz,
  submitted_by    text REFERENCES users(id),
  cancelled_at    timestamptz,
  cancelled_by    text REFERENCES users(id),
  amended_from    text REFERENCES purchase_order(id),
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX po_co_idx ON purchase_order (company_id, transaction_date);
CREATE TRIGGER po_touch BEFORE UPDATE ON purchase_order
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE purchase_order_item (
  id                text PRIMARY KEY,
  purchase_order_id text NOT NULL REFERENCES purchase_order(id) ON DELETE CASCADE,
  row_index         integer NOT NULL,
  item_id           text REFERENCES item(id),
  item_code         text NOT NULL,
  item_name         text NOT NULL,
  description       text,
  qty               numeric(18,6) NOT NULL,
  uom               text NOT NULL,
  rate              numeric(18,4) NOT NULL,
  amount            numeric(18,4) NOT NULL,
  warehouse_id      text REFERENCES warehouse(id),
  received_qty      numeric(18,6) NOT NULL DEFAULT 0,
  billed_qty        numeric(18,6) NOT NULL DEFAULT 0,
  UNIQUE (purchase_order_id, row_index)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_po', 'purchase_order', NULL, 'PO-.YYYY.-.####', true);

-- ============================================================================
-- SALES ORDER
-- ============================================================================
CREATE TABLE sales_order (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  customer_id     text NOT NULL REFERENCES customer(id),
  transaction_date date NOT NULL,
  delivery_date   date,
  currency        text NOT NULL REFERENCES currency(code),
  exchange_rate   numeric(18,8) NOT NULL DEFAULT 1,
  total           numeric(18,4) NOT NULL DEFAULT 0,
  base_total      numeric(18,4) NOT NULL DEFAULT 0,
  status          text NOT NULL DEFAULT 'Draft',
  remarks         text,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at    timestamptz,
  submitted_by    text REFERENCES users(id),
  cancelled_at    timestamptz,
  cancelled_by    text REFERENCES users(id),
  amended_from    text REFERENCES sales_order(id),
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX so_co_idx ON sales_order (company_id, transaction_date);
CREATE TRIGGER so_touch BEFORE UPDATE ON sales_order
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE sales_order_item (
  id              text PRIMARY KEY,
  sales_order_id  text NOT NULL REFERENCES sales_order(id) ON DELETE CASCADE,
  row_index       integer NOT NULL,
  item_id         text REFERENCES item(id),
  item_code       text NOT NULL,
  item_name       text NOT NULL,
  description     text,
  qty             numeric(18,6) NOT NULL,
  uom             text NOT NULL,
  rate            numeric(18,4) NOT NULL,
  amount          numeric(18,4) NOT NULL,
  warehouse_id    text REFERENCES warehouse(id),
  delivered_qty   numeric(18,6) NOT NULL DEFAULT 0,
  billed_qty      numeric(18,6) NOT NULL DEFAULT 0,
  UNIQUE (sales_order_id, row_index)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_so', 'sales_order', NULL, 'SO-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id IN ('nms_default_po','nms_default_so');
DROP TABLE IF EXISTS sales_order_item;
DROP TABLE IF EXISTS sales_order;
DROP TABLE IF EXISTS purchase_order_item;
DROP TABLE IF EXISTS purchase_order;
DROP TABLE IF EXISTS doc_link;
-- +goose StatementEnd
