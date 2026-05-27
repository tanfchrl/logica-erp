-- +goose Up
-- +goose StatementBegin

-- 0028: Purchase Order lifecycle columns
--
-- The PO header was minimal in 0009 (created before there was a service to
-- exercise it). The end-to-end PO flow needs:
--
--   - net_total / total_taxes_and_charges / grand_total split (was just `total`)
--   - taxes child table (mirrors purchase_invoice_tax)
--   - per-line required_by_date (ERPNext supports header + per-line)
--   - lifecycle audit timestamps: held / closed / stopped
--   - payment_terms_template_id + terms_and_conditions free-text (Buying §16)
--   - submitted_by / cancelled_by already exist
--
-- The status column already exists with default 'Draft'; we widen the enum
-- via the service layer (no DB constraint — matches the rest of the codebase).

ALTER TABLE purchase_order
  ADD COLUMN net_total                 numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN total_taxes_and_charges   numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN grand_total               numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN base_net_total            numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN base_total_taxes_and_charges numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN base_grand_total          numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN fiscal_year_id            text REFERENCES fiscal_year(id),
  ADD COLUMN tax_template_id           text REFERENCES tax_template(id),
  ADD COLUMN held_at                   timestamptz,
  ADD COLUMN held_by                   text REFERENCES users(id),
  ADD COLUMN closed_at                 timestamptz,
  ADD COLUMN closed_by                 text REFERENCES users(id),
  ADD COLUMN stopped_at                timestamptz,
  ADD COLUMN stopped_by                text REFERENCES users(id),
  ADD COLUMN terms_and_conditions      text,
  ADD COLUMN payment_terms             text,
  ADD COLUMN letterhead_id             text REFERENCES letterhead(id);

-- Back-fill grand_total from the legacy `total` column so historic rows show
-- the right number once the service starts reading the new fields.
UPDATE purchase_order SET
  grand_total      = total,
  net_total        = total,
  base_grand_total = base_total,
  base_net_total   = base_total
WHERE grand_total = 0 AND total <> 0;

-- Per-line required_by lets a single PO carry items that arrive on different
-- dates (common when consolidating MR items from multiple departments).
ALTER TABLE purchase_order_item
  ADD COLUMN required_by_date date,
  ADD COLUMN description      text,
  ADD COLUMN cost_center_id   text REFERENCES cost_center(id),
  ADD COLUMN tax_amount       numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN total            numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN base_amount      numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN base_tax_amount  numeric(18,4) NOT NULL DEFAULT 0,
  ADD COLUMN base_total       numeric(18,4) NOT NULL DEFAULT 0;

-- Tax rows on a PO (proportional, same engine as PI). Optional — a PO can
-- ship without a tax template if the supplier hasn't issued PPN yet.
CREATE TABLE purchase_order_tax (
  id                     text PRIMARY KEY,
  purchase_order_id      text NOT NULL REFERENCES purchase_order(id) ON DELETE CASCADE,
  row_index              integer NOT NULL,
  account_id             text NOT NULL REFERENCES account(id),
  description            text NOT NULL DEFAULT '',
  rate                   numeric(18,6) NOT NULL DEFAULT 0,
  charge_type            text NOT NULL,
  included_in_basic_rate boolean NOT NULL DEFAULT false,
  tax_amount             numeric(18,4) NOT NULL DEFAULT 0,
  base_tax_amount        numeric(18,4) NOT NULL DEFAULT 0,
  cost_center_id         text REFERENCES cost_center(id),
  UNIQUE (purchase_order_id, row_index)
);

CREATE INDEX po_status_idx     ON purchase_order (company_id, status, transaction_date);
CREATE INDEX po_supplier_idx   ON purchase_order (supplier_id, docstatus);
CREATE INDEX po_required_by_idx ON purchase_order (required_by_date) WHERE docstatus = 1 AND status IN ('To Receive', 'To Receive and Bill');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS purchase_order_tax;
DROP INDEX IF EXISTS po_required_by_idx;
DROP INDEX IF EXISTS po_supplier_idx;
DROP INDEX IF EXISTS po_status_idx;

ALTER TABLE purchase_order_item
  DROP COLUMN IF EXISTS required_by_date,
  DROP COLUMN IF EXISTS description,
  DROP COLUMN IF EXISTS cost_center_id,
  DROP COLUMN IF EXISTS tax_amount,
  DROP COLUMN IF EXISTS total,
  DROP COLUMN IF EXISTS base_amount,
  DROP COLUMN IF EXISTS base_tax_amount,
  DROP COLUMN IF EXISTS base_total;

ALTER TABLE purchase_order
  DROP COLUMN IF EXISTS net_total,
  DROP COLUMN IF EXISTS total_taxes_and_charges,
  DROP COLUMN IF EXISTS grand_total,
  DROP COLUMN IF EXISTS base_net_total,
  DROP COLUMN IF EXISTS base_total_taxes_and_charges,
  DROP COLUMN IF EXISTS base_grand_total,
  DROP COLUMN IF EXISTS fiscal_year_id,
  DROP COLUMN IF EXISTS tax_template_id,
  DROP COLUMN IF EXISTS held_at,
  DROP COLUMN IF EXISTS held_by,
  DROP COLUMN IF EXISTS closed_at,
  DROP COLUMN IF EXISTS closed_by,
  DROP COLUMN IF EXISTS stopped_at,
  DROP COLUMN IF EXISTS stopped_by,
  DROP COLUMN IF EXISTS terms_and_conditions,
  DROP COLUMN IF EXISTS payment_terms,
  DROP COLUMN IF EXISTS letterhead_id;

-- +goose StatementEnd
