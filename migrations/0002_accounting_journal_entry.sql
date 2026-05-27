-- +goose Up
-- +goose StatementBegin

-- Phase 0 exit slice requires Journal Entry — kept in its own migration so the
-- platform migration stays domain-agnostic.

CREATE TABLE journal_entry (
  id                text PRIMARY KEY,
  name              text NOT NULL,                       -- naming series rendered, e.g. JE-2026-0001
  company_id        text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  posting_date      date NOT NULL,
  fiscal_year_id    text NOT NULL REFERENCES fiscal_year(id),
  voucher_type      text NOT NULL DEFAULT 'Journal Entry',
  currency          text NOT NULL REFERENCES currency(code),
  exchange_rate     numeric(18,8) NOT NULL DEFAULT 1,    -- transaction currency -> base
  total_debit       numeric(18,4) NOT NULL DEFAULT 0,
  total_credit      numeric(18,4) NOT NULL DEFAULT 0,
  user_remark       text,
  docstatus         smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at      timestamptz,
  submitted_by      text REFERENCES users(id),
  cancelled_at      timestamptz,
  cancelled_by      text REFERENCES users(id),
  amended_from      text REFERENCES journal_entry(id),
  custom_fields     jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  created_by        text NOT NULL REFERENCES users(id),
  updated_by        text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX journal_entry_co_date_idx ON journal_entry (company_id, posting_date);
CREATE INDEX journal_entry_status_idx  ON journal_entry (docstatus);
CREATE TRIGGER journal_entry_touch BEFORE UPDATE ON journal_entry
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE journal_entry_account (
  id                text PRIMARY KEY,
  journal_entry_id  text NOT NULL REFERENCES journal_entry(id) ON DELETE CASCADE,
  row_index         integer NOT NULL,
  account_id        text NOT NULL REFERENCES account(id),
  party_type        text CHECK (party_type IS NULL OR party_type IN ('customer','supplier','employee')),
  party_id          text,
  cost_center_id    text REFERENCES cost_center(id),
  project_id        text,
  debit             numeric(18,4) NOT NULL DEFAULT 0,
  credit            numeric(18,4) NOT NULL DEFAULT 0,
  debit_in_account_currency  numeric(18,4) NOT NULL DEFAULT 0,
  credit_in_account_currency numeric(18,4) NOT NULL DEFAULT 0,
  reference         text,
  CHECK ((debit = 0) OR (credit = 0)),
  CHECK (debit >= 0 AND credit >= 0)
);
CREATE INDEX jea_je_idx ON journal_entry_account (journal_entry_id, row_index);

-- Seed naming series for Journal Entry (company-agnostic; one per company is created at seed time).
-- The series_id is generated server-side; this is just the default pattern stored as a template row.
INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_je', 'journal_entry', NULL, 'JE-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_je';
DROP TABLE IF EXISTS journal_entry_account;
DROP TABLE IF EXISTS journal_entry;
-- +goose StatementEnd
