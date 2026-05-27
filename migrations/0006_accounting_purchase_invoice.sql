-- +goose Up
-- +goose StatementBegin

CREATE TABLE purchase_invoice (
  id                          text PRIMARY KEY,
  name                        text NOT NULL,
  company_id                  text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  supplier_id                 text NOT NULL REFERENCES supplier(id),
  posting_date                date NOT NULL,
  due_date                    date NOT NULL,
  fiscal_year_id              text NOT NULL REFERENCES fiscal_year(id),
  currency                    text NOT NULL REFERENCES currency(code),
  exchange_rate               numeric(18,8) NOT NULL DEFAULT 1,
  tax_template_id             text REFERENCES tax_template(id),
  supplier_invoice_no         text,
  supplier_invoice_date       date,
  bill_no                     text,
  -- totals (transaction currency)
  net_total                   numeric(18,4) NOT NULL DEFAULT 0,
  total_taxes_and_charges     numeric(18,4) NOT NULL DEFAULT 0,
  grand_total                 numeric(18,4) NOT NULL DEFAULT 0,
  paid_amount                 numeric(18,4) NOT NULL DEFAULT 0,
  outstanding_amount          numeric(18,4) NOT NULL DEFAULT 0,
  -- base-currency snapshots
  base_net_total              numeric(18,4) NOT NULL DEFAULT 0,
  base_total_taxes_and_charges numeric(18,4) NOT NULL DEFAULT 0,
  base_grand_total            numeric(18,4) NOT NULL DEFAULT 0,
  base_paid_amount            numeric(18,4) NOT NULL DEFAULT 0,
  base_outstanding_amount     numeric(18,4) NOT NULL DEFAULT 0,
  remarks                     text,
  payable_account_id          text NOT NULL REFERENCES account(id),
  is_return                   boolean NOT NULL DEFAULT false,
  return_against              text REFERENCES purchase_invoice(id),
  docstatus                   smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at                timestamptz,
  submitted_by                text REFERENCES users(id),
  cancelled_at                timestamptz,
  cancelled_by                text REFERENCES users(id),
  amended_from                text REFERENCES purchase_invoice(id),
  custom_fields               jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  created_by                  text NOT NULL REFERENCES users(id),
  updated_by                  text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX purchase_invoice_co_date_idx     ON purchase_invoice (company_id, posting_date);
CREATE INDEX purchase_invoice_supplier_idx    ON purchase_invoice (supplier_id, docstatus);
CREATE INDEX purchase_invoice_outstanding_idx ON purchase_invoice (company_id, supplier_id, docstatus) WHERE docstatus = 1 AND outstanding_amount > 0;
CREATE TRIGGER purchase_invoice_touch BEFORE UPDATE ON purchase_invoice
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE purchase_invoice_item (
  id                  text PRIMARY KEY,
  purchase_invoice_id text NOT NULL REFERENCES purchase_invoice(id) ON DELETE CASCADE,
  row_index           integer NOT NULL,
  item_id             text REFERENCES item(id),
  item_code           text NOT NULL,
  item_name           text NOT NULL,
  description         text,
  qty                 numeric(18,6) NOT NULL,
  uom                 text NOT NULL,
  rate                numeric(18,4) NOT NULL,
  amount              numeric(18,4) NOT NULL,
  expense_account_id  text NOT NULL REFERENCES account(id),
  cost_center_id      text REFERENCES cost_center(id),
  tax_amount          numeric(18,4) NOT NULL DEFAULT 0,
  total               numeric(18,4) NOT NULL,
  base_amount         numeric(18,4) NOT NULL,
  base_tax_amount     numeric(18,4) NOT NULL DEFAULT 0,
  base_total          numeric(18,4) NOT NULL,
  UNIQUE (purchase_invoice_id, row_index)
);
CREATE INDEX pii_invoice_idx ON purchase_invoice_item (purchase_invoice_id);

CREATE TABLE purchase_invoice_tax (
  id                     text PRIMARY KEY,
  purchase_invoice_id    text NOT NULL REFERENCES purchase_invoice(id) ON DELETE CASCADE,
  row_index              integer NOT NULL,
  account_id             text NOT NULL REFERENCES account(id),
  description            text NOT NULL,
  rate                   numeric(9,4) NOT NULL,
  charge_type            text NOT NULL,
  included_in_basic_rate boolean NOT NULL DEFAULT false,
  tax_amount             numeric(18,4) NOT NULL,
  base_tax_amount        numeric(18,4) NOT NULL,
  cost_center_id         text REFERENCES cost_center(id),
  UNIQUE (purchase_invoice_id, row_index)
);

CREATE TABLE purchase_invoice_withholding (
  id                       text PRIMARY KEY,
  purchase_invoice_id      text NOT NULL REFERENCES purchase_invoice(id) ON DELETE CASCADE,
  withholding_tax_type_id  text NOT NULL REFERENCES withholding_tax_type(id),
  rate                     numeric(9,4) NOT NULL,
  amount                   numeric(18,4) NOT NULL,
  base_amount              numeric(18,4) NOT NULL,
  account_id               text NOT NULL REFERENCES account(id)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_pi', 'purchase_invoice', NULL, 'PI-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_pi';
DROP TABLE IF EXISTS purchase_invoice_withholding;
DROP TABLE IF EXISTS purchase_invoice_tax;
DROP TABLE IF EXISTS purchase_invoice_item;
DROP TABLE IF EXISTS purchase_invoice;
-- +goose StatementEnd
