-- +goose Up
-- +goose StatementBegin

CREATE TABLE period_closing_voucher (
  id                          text PRIMARY KEY,
  name                        text NOT NULL,
  company_id                  text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  fiscal_year_id              text NOT NULL REFERENCES fiscal_year(id),
  posting_date                date NOT NULL,
  closing_account_id          text NOT NULL REFERENCES account(id),  -- Retained Earnings
  remarks                     text,
  docstatus                   smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at                timestamptz,
  submitted_by                text REFERENCES users(id),
  cancelled_at                timestamptz,
  cancelled_by                text REFERENCES users(id),
  amended_from                text REFERENCES period_closing_voucher(id),
  custom_fields               jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  created_by                  text NOT NULL REFERENCES users(id),
  updated_by                  text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name),
  UNIQUE (company_id, fiscal_year_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER pcv_touch BEFORE UPDATE ON period_closing_voucher
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_pcv', 'period_closing_voucher', NULL, 'PCV-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_pcv';
DROP TABLE IF EXISTS period_closing_voucher;
-- +goose StatementEnd
