-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- MATERIAL REQUEST (MR)
-- ============================================================================
-- ERPNext-equivalent purchase requisition. Drives PO creation upstream and
-- stock-entry creation for transfer/issue/manufacture purposes.
--
-- Lifecycle:
--   Draft → submit → Pending → (per-purpose fulfilment) → Ordered / Issued /
--                                Transferred / Received / Manufactured
--   Submitted states can also transition: Stopped (recoverable), Cancelled.
--
-- ordered_qty / issued_qty / received_qty are driven by downstream documents
-- (PO submit, Stock Entry submit, Purchase Receipt submit). MR's own service
-- never mutates them — fulfilment is one-way write from the doc that consumed
-- the qty.

CREATE TABLE material_request (
  id               text PRIMARY KEY,
  name             text NOT NULL,
  company_id       text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  -- Purpose drives which downstream doctype consumes this MR. We support
  -- the four common purposes; subcontracting + customer-provided come
  -- later if there's demand.
  purpose          text NOT NULL CHECK (purpose IN
                       ('purchase','material_transfer','material_issue','manufacture')),
  transaction_date date NOT NULL,
  required_by_date date,
  -- Optional default warehouse — copied onto each line that doesn't override.
  set_warehouse_id text REFERENCES warehouse(id),
  -- For purpose='material_transfer', this is the source warehouse.
  from_warehouse_id text REFERENCES warehouse(id),
  status           text NOT NULL DEFAULT 'Draft',
  remarks          text,
  docstatus        smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at     timestamptz,
  submitted_by     text REFERENCES users(id),
  cancelled_at     timestamptz,
  cancelled_by     text REFERENCES users(id),
  stopped_at       timestamptz,
  stopped_by       text REFERENCES users(id),
  amended_from     text REFERENCES material_request(id),
  custom_fields    jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  created_by       text NOT NULL REFERENCES users(id),
  updated_by       text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX mr_co_idx        ON material_request (company_id, transaction_date);
CREATE INDEX mr_status_idx    ON material_request (company_id, status, docstatus);
CREATE INDEX mr_required_idx  ON material_request (required_by_date)
  WHERE docstatus = 1 AND status IN ('Pending','Partially Ordered');
CREATE TRIGGER mr_touch BEFORE UPDATE ON material_request
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE material_request_item (
  id                  text PRIMARY KEY,
  material_request_id text NOT NULL REFERENCES material_request(id) ON DELETE CASCADE,
  row_index           integer NOT NULL,
  item_id             text REFERENCES item(id),
  item_code           text NOT NULL,
  item_name           text NOT NULL,
  description         text,
  qty                 numeric(18,6) NOT NULL CHECK (qty > 0),
  uom                 text NOT NULL,
  rate                numeric(18,4) NOT NULL DEFAULT 0,  -- indicative; for purpose='purchase' fed to PO
  amount              numeric(18,4) NOT NULL DEFAULT 0,
  warehouse_id        text REFERENCES warehouse(id),
  required_by_date    date,
  -- Fulfilment counters — written by downstream submits, not MR itself.
  ordered_qty         numeric(18,6) NOT NULL DEFAULT 0,
  received_qty        numeric(18,6) NOT NULL DEFAULT 0,
  issued_qty          numeric(18,6) NOT NULL DEFAULT 0,
  transferred_qty     numeric(18,6) NOT NULL DEFAULT 0,
  UNIQUE (material_request_id, row_index)
);
CREATE INDEX mri_item_idx ON material_request_item (item_id);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_mr', 'material_request', NULL, 'MR-.YYYY.-.####', true);

-- ============================================================================
-- doc_link is the cross-doctype audit trail. PO's "created from MR" handler
-- writes a (material_request → purchase_order) row so reports/UI can show
-- "this PO came from MR-2026-0042". Schema lives in 0009; no change needed.
-- ============================================================================

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_mr';
DROP TABLE IF EXISTS material_request_item;
DROP TABLE IF EXISTS material_request;
-- +goose StatementEnd
