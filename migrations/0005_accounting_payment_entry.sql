-- +goose Up
-- +goose StatementBegin

CREATE TABLE payment_entry (
  id                          text PRIMARY KEY,
  name                        text NOT NULL,
  company_id                  text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  payment_type                text NOT NULL CHECK (payment_type IN ('receive','pay','internal_transfer')),
  party_type                  text CHECK (party_type IS NULL OR party_type IN ('customer','supplier','employee')),
  party_id                    text,
  posting_date                date NOT NULL,
  fiscal_year_id              text NOT NULL REFERENCES fiscal_year(id),
  paid_from_account_id        text NOT NULL REFERENCES account(id),
  paid_to_account_id          text NOT NULL REFERENCES account(id),
  paid_from_currency          text NOT NULL REFERENCES currency(code),
  paid_to_currency            text NOT NULL REFERENCES currency(code),
  paid_amount                 numeric(18,4) NOT NULL,
  received_amount             numeric(18,4) NOT NULL,
  source_exchange_rate        numeric(18,8) NOT NULL DEFAULT 1,
  target_exchange_rate        numeric(18,8) NOT NULL DEFAULT 1,
  base_paid_amount            numeric(18,4) NOT NULL,
  base_received_amount        numeric(18,4) NOT NULL,
  total_allocated_amount      numeric(18,4) NOT NULL DEFAULT 0,
  base_total_allocated_amount numeric(18,4) NOT NULL DEFAULT 0,
  unallocated_amount          numeric(18,4) NOT NULL DEFAULT 0,
  total_deductions            numeric(18,4) NOT NULL DEFAULT 0,
  reference_no                text,
  reference_date              date,
  remarks                     text,
  docstatus                   smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at                timestamptz,
  submitted_by                text REFERENCES users(id),
  cancelled_at                timestamptz,
  cancelled_by                text REFERENCES users(id),
  amended_from                text REFERENCES payment_entry(id),
  custom_fields               jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  created_by                  text NOT NULL REFERENCES users(id),
  updated_by                  text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX payment_entry_co_date_idx ON payment_entry (company_id, posting_date);
CREATE INDEX payment_entry_party_idx   ON payment_entry (party_type, party_id) WHERE party_id IS NOT NULL;
CREATE TRIGGER payment_entry_touch BEFORE UPDATE ON payment_entry
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE payment_entry_reference (
  id                    text PRIMARY KEY,
  payment_entry_id      text NOT NULL REFERENCES payment_entry(id) ON DELETE CASCADE,
  row_index             integer NOT NULL,
  reference_doctype     text NOT NULL,
  reference_id          text NOT NULL,
  reference_name        text NOT NULL,
  total_amount          numeric(18,4) NOT NULL,
  allocated_amount      numeric(18,4) NOT NULL,
  base_allocated_amount numeric(18,4) NOT NULL,
  UNIQUE (payment_entry_id, row_index)
);
CREATE INDEX per_ref_idx ON payment_entry_reference (reference_doctype, reference_id);

CREATE TABLE payment_entry_deduction (
  id                      text PRIMARY KEY,
  payment_entry_id        text NOT NULL REFERENCES payment_entry(id) ON DELETE CASCADE,
  row_index               integer NOT NULL,
  account_id              text NOT NULL REFERENCES account(id),
  description             text NOT NULL,
  amount                  numeric(18,4) NOT NULL,
  cost_center_id          text REFERENCES cost_center(id),
  withholding_tax_type_id text REFERENCES withholding_tax_type(id),
  UNIQUE (payment_entry_id, row_index)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_pe', 'payment_entry', NULL, 'PE-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_pe';
DROP TABLE IF EXISTS payment_entry_deduction;
DROP TABLE IF EXISTS payment_entry_reference;
DROP TABLE IF EXISTS payment_entry;
-- +goose StatementEnd
