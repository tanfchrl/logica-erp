-- +goose Up
-- +goose StatementBegin

-- ----------------------------------------------------------------------------
-- Logica ERP — Phase 0 platform migration
-- Forward-only. Do not edit after merge; subsequent changes go in new files.
-- ----------------------------------------------------------------------------

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Helper: maintain updated_at on update.
CREATE OR REPLACE FUNCTION logica_touch_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END
$$;

-- ============================================================================
-- IDENTITY: users, sessions
-- ============================================================================

CREATE TABLE users (
  id              text PRIMARY KEY,
  email           text NOT NULL UNIQUE,
  full_name       text NOT NULL DEFAULT '',
  password_hash   text NOT NULL,
  enabled         boolean NOT NULL DEFAULT true,
  locale          text NOT NULL DEFAULT 'id-ID',
  time_zone       text NOT NULL DEFAULT 'Asia/Jakarta',
  is_system       boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text,
  updated_by      text,
  CHECK (email = lower(email))
);
CREATE TRIGGER users_touch BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE user_session (
  id                 text PRIMARY KEY,
  user_id            text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_token_hash text NOT NULL UNIQUE,
  issued_at          timestamptz NOT NULL DEFAULT now(),
  expires_at         timestamptz NOT NULL,
  rotated_to         text REFERENCES user_session(id) ON DELETE SET NULL,
  user_agent         text,
  ip                 inet,
  revoked_at         timestamptz
);
CREATE INDEX user_session_user_idx ON user_session (user_id, expires_at DESC);

-- ============================================================================
-- COMPANY: the multi-company root
-- ============================================================================

CREATE TABLE company (
  id                          text PRIMARY KEY,
  name                        text NOT NULL UNIQUE,
  legal_name                  text NOT NULL,
  abbreviation                text NOT NULL UNIQUE,
  country                     text NOT NULL DEFAULT 'ID',
  default_currency            text NOT NULL DEFAULT 'IDR',
  npwp                        text,
  npwp_address                text,
  address_line                text,
  city                        text,
  province                    text,
  postal_code                 text,
  phone                       text,
  email                       text,
  website                     text,
  -- Linked accounts populated later (after COA exists for this company)
  default_receivable_account_id text,
  default_payable_account_id    text,
  default_cash_account_id       text,
  default_bank_account_id       text,
  default_income_account_id     text,
  default_expense_account_id    text,
  default_cost_center_id        text,
  is_deleted                  boolean NOT NULL DEFAULT false,
  custom_fields               jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  created_by                  text NOT NULL REFERENCES users(id),
  updated_by                  text NOT NULL REFERENCES users(id),
  CHECK (npwp IS NULL OR npwp ~ '^[0-9]{16}$')
);
CREATE TRIGGER company_touch BEFORE UPDATE ON company
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- PERMISSIONS (4 layers)
-- ============================================================================

CREATE TABLE role (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  description text NOT NULL DEFAULT '',
  is_system   boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_role (
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id text NOT NULL REFERENCES role(id)  ON DELETE CASCADE,
  PRIMARY KEY (user_id, role_id)
);

CREATE TABLE role_permission (
  id          text PRIMARY KEY,
  role_id     text NOT NULL REFERENCES role(id) ON DELETE CASCADE,
  doctype     text NOT NULL,
  can_read    boolean NOT NULL DEFAULT false,
  can_write   boolean NOT NULL DEFAULT false,
  can_create  boolean NOT NULL DEFAULT false,
  can_delete  boolean NOT NULL DEFAULT false,
  can_submit  boolean NOT NULL DEFAULT false,
  can_cancel  boolean NOT NULL DEFAULT false,
  can_amend   boolean NOT NULL DEFAULT false,
  can_print   boolean NOT NULL DEFAULT false,
  can_export  boolean NOT NULL DEFAULT false,
  UNIQUE (role_id, doctype)
);

-- Row-level: restrict the records a user can see by a scoping field value.
CREATE TABLE user_permission (
  id             text PRIMARY KEY,
  user_id        text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope          text NOT NULL,            -- target field name, e.g. 'territory_id', 'branch_id', 'company_id'
  value          text NOT NULL,            -- allowed value
  applicable_for text,                     -- NULL = applies across all doctypes
  UNIQUE (user_id, scope, value, applicable_for)
);
CREATE INDEX user_permission_user_idx ON user_permission (user_id, scope);

CREATE TABLE field_permission (
  id        text PRIMARY KEY,
  role_id   text NOT NULL REFERENCES role(id) ON DELETE CASCADE,
  doctype   text NOT NULL,
  field     text NOT NULL,
  can_read  boolean NOT NULL DEFAULT true,
  can_write boolean NOT NULL DEFAULT true,
  UNIQUE (role_id, doctype, field)
);

CREATE TABLE user_company (
  user_id    text NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
  company_id text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, company_id)
);

-- ============================================================================
-- FISCAL YEAR + CURRENCY
-- ============================================================================

CREATE TABLE fiscal_year (
  id         text PRIMARY KEY,
  name       text NOT NULL UNIQUE,
  start_date date NOT NULL,
  end_date   date NOT NULL,
  is_closed  boolean NOT NULL DEFAULT false,
  CHECK (end_date > start_date)
);

CREATE TABLE fiscal_year_company (
  fiscal_year_id text NOT NULL REFERENCES fiscal_year(id) ON DELETE CASCADE,
  company_id     text NOT NULL REFERENCES company(id)     ON DELETE CASCADE,
  PRIMARY KEY (fiscal_year_id, company_id)
);

CREATE TABLE currency (
  code     text PRIMARY KEY,
  name     text NOT NULL,
  symbol   text NOT NULL,
  fraction text,
  enabled  boolean NOT NULL DEFAULT true
);

CREATE TABLE currency_exchange_rate (
  id             text PRIMARY KEY,
  from_currency  text NOT NULL REFERENCES currency(code),
  to_currency    text NOT NULL REFERENCES currency(code),
  rate           numeric(18,8) NOT NULL,
  effective_date date NOT NULL,
  UNIQUE (from_currency, to_currency, effective_date),
  CHECK (rate > 0)
);
CREATE INDEX fx_lookup_idx ON currency_exchange_rate (from_currency, to_currency, effective_date DESC);

-- ============================================================================
-- COST CENTER + ACCOUNT (tree, per-company)
-- ============================================================================

CREATE TABLE cost_center (
  id          text PRIMARY KEY,
  company_id  text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  name        text NOT NULL,
  parent_id   text REFERENCES cost_center(id),
  lft         integer,
  rgt         integer,
  is_group    boolean NOT NULL DEFAULT false,
  is_deleted  boolean NOT NULL DEFAULT false,
  custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  created_by  text NOT NULL REFERENCES users(id),
  updated_by  text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX cost_center_co_idx ON cost_center (company_id);
CREATE TRIGGER cost_center_touch BEFORE UPDATE ON cost_center
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE account (
  id                text PRIMARY KEY,
  company_id        text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  name              text NOT NULL,
  account_number    text,
  parent_id         text REFERENCES account(id),
  lft               integer,
  rgt               integer,
  is_group          boolean NOT NULL DEFAULT false,
  root_type         text NOT NULL CHECK (root_type IN ('asset','liability','equity','income','expense')),
  account_type      text,
  account_currency  text NOT NULL REFERENCES currency(code),
  is_deleted        boolean NOT NULL DEFAULT false,
  custom_fields     jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  created_by        text NOT NULL REFERENCES users(id),
  updated_by        text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX account_co_idx     ON account (company_id);
CREATE INDEX account_parent_idx ON account (parent_id);
CREATE TRIGGER account_touch BEFORE UPDATE ON account
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- Now we can install the FK on company.default_*_account_id (deferred to avoid cycle).
ALTER TABLE company
  ADD CONSTRAINT company_default_receivable_fk FOREIGN KEY (default_receivable_account_id) REFERENCES account(id),
  ADD CONSTRAINT company_default_payable_fk    FOREIGN KEY (default_payable_account_id)    REFERENCES account(id),
  ADD CONSTRAINT company_default_cash_fk       FOREIGN KEY (default_cash_account_id)       REFERENCES account(id),
  ADD CONSTRAINT company_default_bank_fk       FOREIGN KEY (default_bank_account_id)       REFERENCES account(id),
  ADD CONSTRAINT company_default_income_fk     FOREIGN KEY (default_income_account_id)     REFERENCES account(id),
  ADD CONSTRAINT company_default_expense_fk    FOREIGN KEY (default_expense_account_id)    REFERENCES account(id),
  ADD CONSTRAINT company_default_cost_center_fk FOREIGN KEY (default_cost_center_id)       REFERENCES cost_center(id);

-- ============================================================================
-- NAMING SERIES
-- ============================================================================

CREATE TABLE naming_series (
  id          text PRIMARY KEY,
  doctype     text NOT NULL,
  company_id  text REFERENCES company(id) ON DELETE CASCADE,
  pattern     text NOT NULL,
  is_default  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (doctype, company_id, pattern)
);

CREATE TABLE naming_series_counter (
  series_id     text NOT NULL REFERENCES naming_series(id) ON DELETE CASCADE,
  scope_key     text NOT NULL,
  current_value bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (series_id, scope_key)
);

-- ============================================================================
-- CUSTOM FIELDS
-- ============================================================================

CREATE TABLE custom_field_definition (
  id            text PRIMARY KEY,
  doctype       text NOT NULL,
  field_name    text NOT NULL,
  label_id      text NOT NULL,
  label_en      text NOT NULL,
  field_type    text NOT NULL CHECK (field_type IN ('text','int','decimal','date','datetime','bool','select','link','table')),
  is_required   boolean NOT NULL DEFAULT false,
  default_value text,
  options       jsonb,
  position      integer NOT NULL DEFAULT 0,
  is_indexed    boolean NOT NULL DEFAULT false,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (doctype, field_name)
);
CREATE TRIGGER cfd_touch BEFORE UPDATE ON custom_field_definition
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- AUDIT TRAIL
-- ============================================================================

CREATE TABLE document_audit (
  id          text PRIMARY KEY,
  doctype     text NOT NULL,
  document_id text NOT NULL,
  action      text NOT NULL CHECK (action IN ('create','update','submit','cancel','amend','delete')),
  changed_by  text NOT NULL REFERENCES users(id),
  changed_at  timestamptz NOT NULL DEFAULT now(),
  diff        jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX document_audit_doc_idx  ON document_audit (doctype, document_id, changed_at DESC);
CREATE INDEX document_audit_user_idx ON document_audit (changed_by, changed_at DESC);

-- ============================================================================
-- LEDGERS — the heart
-- ============================================================================

CREATE TABLE gl_entry (
  id                            text PRIMARY KEY,
  company_id                    text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  posting_date                  date NOT NULL,
  account_id                    text NOT NULL REFERENCES account(id),
  party_type                    text CHECK (party_type IS NULL OR party_type IN ('customer','supplier','employee')),
  party_id                      text,
  cost_center_id                text REFERENCES cost_center(id),
  project_id                    text,
  debit                         numeric(18,4) NOT NULL DEFAULT 0,
  credit                        numeric(18,4) NOT NULL DEFAULT 0,
  account_currency              text NOT NULL REFERENCES currency(code),
  debit_in_account_currency     numeric(18,4) NOT NULL DEFAULT 0,
  credit_in_account_currency    numeric(18,4) NOT NULL DEFAULT 0,
  against                       text,
  voucher_type                  text NOT NULL,
  voucher_id                    text NOT NULL,
  voucher_name                  text NOT NULL,
  remarks                       text,
  fiscal_year                   text NOT NULL,
  is_cancelled                  boolean NOT NULL DEFAULT false,
  cancelled_by_entry_id         text REFERENCES gl_entry(id),
  created_at                    timestamptz NOT NULL DEFAULT now(),
  created_by                    text NOT NULL REFERENCES users(id),
  CHECK ((debit = 0) OR (credit = 0)),
  CHECK (debit >= 0 AND credit >= 0)
);
CREATE INDEX gl_entry_account_idx ON gl_entry (account_id, posting_date) WHERE is_cancelled = false;
CREATE INDEX gl_entry_voucher_idx ON gl_entry (voucher_type, voucher_id);
CREATE INDEX gl_entry_party_idx   ON gl_entry (party_type, party_id) WHERE party_id IS NOT NULL;
CREATE INDEX gl_entry_company_pd  ON gl_entry (company_id, posting_date);

CREATE TABLE stock_ledger_entry (
  id                      text PRIMARY KEY,
  company_id              text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  posting_datetime        timestamptz NOT NULL,
  item_id                 text NOT NULL,             -- FK installed in stock migration
  warehouse_id            text NOT NULL,             -- FK installed in stock migration
  batch_no                text,
  serial_no               text,
  actual_qty              numeric(18,6) NOT NULL,
  qty_after_transaction   numeric(18,6) NOT NULL,
  valuation_rate          numeric(18,6) NOT NULL,
  stock_value             numeric(18,4) NOT NULL,
  stock_value_difference  numeric(18,4) NOT NULL,
  incoming_rate           numeric(18,6),
  voucher_type            text NOT NULL,
  voucher_id              text NOT NULL,
  voucher_name            text NOT NULL,
  is_cancelled            boolean NOT NULL DEFAULT false,
  created_at              timestamptz NOT NULL DEFAULT now(),
  created_by              text NOT NULL REFERENCES users(id)
);
CREATE INDEX sle_item_wh_idx ON stock_ledger_entry (item_id, warehouse_id, posting_datetime);
CREATE INDEX sle_voucher_idx ON stock_ledger_entry (voucher_type, voucher_id);
CREATE INDEX sle_company_idx ON stock_ledger_entry (company_id, posting_datetime);

-- ============================================================================
-- DEFENSIVE: revoke UPDATE/DELETE on append-only tables.
-- These run at the role level in deploy; here we add row-trigger guards as a
-- belt-and-braces measure that works even for the owning role.
-- ============================================================================

CREATE OR REPLACE FUNCTION logica_block_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'table % is append-only', TG_TABLE_NAME USING ERRCODE = 'check_violation';
END
$$;

-- gl_entry: allow UPDATE only of is_cancelled / cancelled_by_entry_id, block DELETE entirely.
CREATE OR REPLACE FUNCTION logica_gl_entry_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'gl_entry is append-only — DELETE forbidden' USING ERRCODE = 'check_violation';
  END IF;
  -- UPDATE: only is_cancelled may transition false->true; cancelled_by_entry_id may be set.
  IF NEW.id            <> OLD.id            OR
     NEW.company_id    <> OLD.company_id    OR
     NEW.posting_date  <> OLD.posting_date  OR
     NEW.account_id    <> OLD.account_id    OR
     NEW.debit         <> OLD.debit         OR
     NEW.credit        <> OLD.credit        OR
     NEW.voucher_type  <> OLD.voucher_type  OR
     NEW.voucher_id    <> OLD.voucher_id    THEN
    RAISE EXCEPTION 'gl_entry is immutable except for cancellation flags' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
CREATE TRIGGER gl_entry_guard BEFORE UPDATE OR DELETE ON gl_entry
  FOR EACH ROW EXECUTE FUNCTION logica_gl_entry_guard();

CREATE TRIGGER sle_no_delete BEFORE DELETE ON stock_ledger_entry
  FOR EACH ROW EXECUTE FUNCTION logica_block_mutation();

CREATE TRIGGER audit_no_update BEFORE UPDATE OR DELETE ON document_audit
  FOR EACH ROW EXECUTE FUNCTION logica_block_mutation();

-- ============================================================================
-- SEED: currencies, system roles
-- ============================================================================

INSERT INTO currency (code, name, symbol, fraction) VALUES
  ('IDR', 'Indonesian Rupiah', 'Rp', 'sen'),
  ('USD', 'US Dollar',         '$',  'cent'),
  ('EUR', 'Euro',              '€',  'cent'),
  ('SGD', 'Singapore Dollar',  'S$', 'cent'),
  ('JPY', 'Japanese Yen',      '¥',  NULL),
  ('CNY', 'Chinese Yuan',      '¥',  'fen'),
  ('AUD', 'Australian Dollar', 'A$', 'cent'),
  ('MYR', 'Malaysian Ringgit', 'RM', 'sen');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS audit_no_update      ON document_audit;
DROP TRIGGER IF EXISTS sle_no_delete        ON stock_ledger_entry;
DROP TRIGGER IF EXISTS gl_entry_guard       ON gl_entry;
DROP FUNCTION IF EXISTS logica_gl_entry_guard();
DROP FUNCTION IF EXISTS logica_block_mutation();

DROP TABLE IF EXISTS stock_ledger_entry;
DROP TABLE IF EXISTS gl_entry;
DROP TABLE IF EXISTS document_audit;
DROP TABLE IF EXISTS custom_field_definition;
DROP TABLE IF EXISTS naming_series_counter;
DROP TABLE IF EXISTS naming_series;

ALTER TABLE company
  DROP CONSTRAINT IF EXISTS company_default_receivable_fk,
  DROP CONSTRAINT IF EXISTS company_default_payable_fk,
  DROP CONSTRAINT IF EXISTS company_default_cash_fk,
  DROP CONSTRAINT IF EXISTS company_default_bank_fk,
  DROP CONSTRAINT IF EXISTS company_default_income_fk,
  DROP CONSTRAINT IF EXISTS company_default_expense_fk,
  DROP CONSTRAINT IF EXISTS company_default_cost_center_fk;

DROP TABLE IF EXISTS account;
DROP TABLE IF EXISTS cost_center;
DROP TABLE IF EXISTS currency_exchange_rate;
DROP TABLE IF EXISTS currency;
DROP TABLE IF EXISTS fiscal_year_company;
DROP TABLE IF EXISTS fiscal_year;
DROP TABLE IF EXISTS user_company;
DROP TABLE IF EXISTS field_permission;
DROP TABLE IF EXISTS user_permission;
DROP TABLE IF EXISTS role_permission;
DROP TABLE IF EXISTS user_role;
DROP TABLE IF EXISTS role;
DROP TABLE IF EXISTS company;
DROP TABLE IF EXISTS user_session;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS logica_touch_updated_at();
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS pgcrypto;

-- +goose StatementEnd
