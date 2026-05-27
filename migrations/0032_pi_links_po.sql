-- +goose Up
-- +goose StatementBegin

-- 0032: Link Purchase Invoice rows back to Purchase Orders / Receipts
--
-- v1 PI didn't carry PO/GRN refs because there was no PO doctype yet. Now
-- that PO + GRN exist, we add:
--
--   - purchase_invoice.against_purchase_order_id        (optional)
--   - purchase_invoice.against_purchase_receipt_id      (optional)
--   - purchase_invoice_item.against_po_id + row_index   (per-line traceback)
--
-- The service writes these on create; submit uses them to bump PO billed_qty
-- and enforce the Buying Settings over-billing tolerance.

ALTER TABLE purchase_invoice
  ADD COLUMN against_purchase_order_id   text REFERENCES purchase_order(id),
  ADD COLUMN against_purchase_receipt_id text REFERENCES purchase_receipt(id);

ALTER TABLE purchase_invoice_item
  ADD COLUMN against_po_id        text REFERENCES purchase_order(id),
  ADD COLUMN against_po_row_index integer;

CREATE INDEX pi_against_po_idx  ON purchase_invoice (against_purchase_order_id)
  WHERE against_purchase_order_id IS NOT NULL;
CREATE INDEX pi_against_pr_idx  ON purchase_invoice (against_purchase_receipt_id)
  WHERE against_purchase_receipt_id IS NOT NULL;
CREATE INDEX pii_against_po_idx ON purchase_invoice_item (against_po_id, against_po_row_index)
  WHERE against_po_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS pii_against_po_idx;
DROP INDEX IF EXISTS pi_against_pr_idx;
DROP INDEX IF EXISTS pi_against_po_idx;
ALTER TABLE purchase_invoice_item
  DROP COLUMN IF EXISTS against_po_id,
  DROP COLUMN IF EXISTS against_po_row_index;
ALTER TABLE purchase_invoice
  DROP COLUMN IF EXISTS against_purchase_order_id,
  DROP COLUMN IF EXISTS against_purchase_receipt_id;
-- +goose StatementEnd
