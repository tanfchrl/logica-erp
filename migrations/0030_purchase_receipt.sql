-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- PURCHASE RECEIPT (GRN)
-- ============================================================================
-- ERPNext-equivalent first-class GRN. Replaces the generic stock_entry
-- material_receipt path for purchases — stock_entry stays for transfers,
-- issues, and manufacture.
--
-- Submit writes stock_ledger_entry rows (one per line per warehouse where
-- qty > 0) and bumps the linked PO's received_qty. v1 deliberately skips
-- the Dr Stock / Cr SRBNB clearing-account GL leg — that lands when
-- Buying Settings + per-warehouse SRBNB account come online.

CREATE TABLE purchase_receipt (
  id                       text PRIMARY KEY,
  name                     text NOT NULL,
  company_id               text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  supplier_id              text NOT NULL REFERENCES supplier(id),
  posting_date             date NOT NULL,
  posting_datetime         timestamptz NOT NULL DEFAULT now(),
  -- Optional source PO. If set, line items must reference rows on this PO
  -- and the service guards qty/warehouse against the PO.
  against_purchase_order_id text REFERENCES purchase_order(id),
  -- Supplier's own delivery-note number, for paper-trail matching.
  supplier_delivery_note   text,
  status                   text NOT NULL DEFAULT 'Draft',  -- Draft|To Bill|Completed|Return Issued|Cancelled
  remarks                  text,
  docstatus                smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at             timestamptz,
  submitted_by             text REFERENCES users(id),
  cancelled_at             timestamptz,
  cancelled_by             text REFERENCES users(id),
  amended_from             text REFERENCES purchase_receipt(id),
  fiscal_year_id           text REFERENCES fiscal_year(id),
  total_value              numeric(18,4) NOT NULL DEFAULT 0,
  custom_fields            jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  created_by               text NOT NULL REFERENCES users(id),
  updated_by               text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX pr_co_idx        ON purchase_receipt (company_id, posting_date);
CREATE INDEX pr_supplier_idx  ON purchase_receipt (supplier_id, docstatus);
CREATE INDEX pr_po_idx        ON purchase_receipt (against_purchase_order_id)
  WHERE against_purchase_order_id IS NOT NULL;
CREATE TRIGGER pr_touch BEFORE UPDATE ON purchase_receipt
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE purchase_receipt_item (
  id                       text PRIMARY KEY,
  purchase_receipt_id      text NOT NULL REFERENCES purchase_receipt(id) ON DELETE CASCADE,
  row_index                integer NOT NULL,
  item_id                  text NOT NULL REFERENCES item(id),
  item_code                text NOT NULL,
  item_name                text NOT NULL,
  description              text,
  uom                      text NOT NULL,
  rate                     numeric(18,4) NOT NULL DEFAULT 0,
  -- Accepted vs rejected split — the GRN's most useful affordance. The
  -- rejected_warehouse can be null if rejected_qty is zero.
  accepted_qty             numeric(18,6) NOT NULL DEFAULT 0 CHECK (accepted_qty >= 0),
  rejected_qty             numeric(18,6) NOT NULL DEFAULT 0 CHECK (rejected_qty >= 0),
  accepted_warehouse_id    text NOT NULL REFERENCES warehouse(id),
  rejected_warehouse_id    text REFERENCES warehouse(id),
  -- Optional traceback to the PO line being fulfilled.
  against_po_id            text REFERENCES purchase_order(id),
  against_po_row_index     integer,
  -- Stock-ledger snapshot at submit (set by the service).
  valuation_rate           numeric(18,4) NOT NULL DEFAULT 0,
  amount                   numeric(18,4) NOT NULL DEFAULT 0,
  cost_center_id           text REFERENCES cost_center(id),
  UNIQUE (purchase_receipt_id, row_index),
  CHECK (accepted_qty > 0 OR rejected_qty > 0),
  CHECK (rejected_qty = 0 OR rejected_warehouse_id IS NOT NULL)
);
CREATE INDEX pri_item_idx     ON purchase_receipt_item (item_id);
CREATE INDEX pri_against_idx  ON purchase_receipt_item (against_po_id, against_po_row_index)
  WHERE against_po_id IS NOT NULL;

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_pr', 'purchase_receipt', NULL, 'GRN-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_pr';
DROP TABLE IF EXISTS purchase_receipt_item;
DROP TABLE IF EXISTS purchase_receipt;
-- +goose StatementEnd
