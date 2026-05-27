-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- TAX ENGINE
-- ============================================================================

CREATE TABLE tax_category (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  description text,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tax_template (
  id              text PRIMARY KEY,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  name            text NOT NULL,
  is_sales        boolean NOT NULL,
  is_default      boolean NOT NULL DEFAULT false,
  tax_category_id text REFERENCES tax_category(id),
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX tax_template_co_idx ON tax_template (company_id, is_sales);
CREATE TRIGGER tax_template_touch BEFORE UPDATE ON tax_template
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE tax_template_line (
  id                     text PRIMARY KEY,
  template_id            text NOT NULL REFERENCES tax_template(id) ON DELETE CASCADE,
  row_index              integer NOT NULL,
  account_id             text NOT NULL REFERENCES account(id),
  description            text NOT NULL,
  rate                   numeric(9,4) NOT NULL,
  charge_type            text NOT NULL CHECK (charge_type IN ('on_net_total','on_previous_amount','actual')),
  included_in_basic_rate boolean NOT NULL DEFAULT false,
  cost_center_id         text REFERENCES cost_center(id),
  UNIQUE (template_id, row_index)
);

CREATE TABLE withholding_tax_type (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  rate        numeric(9,4) NOT NULL,
  account_id  text NOT NULL REFERENCES account(id),
  threshold   numeric(18,4),
  category    text CHECK (category IS NULL OR category IN ('individual','entity')),
  is_deleted  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  created_by  text NOT NULL REFERENCES users(id),
  updated_by  text NOT NULL REFERENCES users(id)
);
CREATE TRIGGER wht_touch BEFORE UPDATE ON withholding_tax_type
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- ITEM (minimal — Phase 2 adds variants, batches, valuation, warehouse links)
-- ============================================================================

CREATE TABLE item_group (
  id         text PRIMARY KEY,
  name       text NOT NULL UNIQUE,
  parent_id  text REFERENCES item_group(id),
  lft        integer,
  rgt        integer,
  is_group   boolean NOT NULL DEFAULT false,
  is_deleted boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE item (
  id               text PRIMARY KEY,
  code             text NOT NULL UNIQUE,
  name             text NOT NULL,
  description      text,
  item_group_id    text REFERENCES item_group(id),
  stock_uom        text NOT NULL DEFAULT 'Unit',
  is_stock_item    boolean NOT NULL DEFAULT false,
  is_sales_item    boolean NOT NULL DEFAULT true,
  is_purchase_item boolean NOT NULL DEFAULT true,
  standard_rate    numeric(18,4) NOT NULL DEFAULT 0,
  is_deleted       boolean NOT NULL DEFAULT false,
  custom_fields    jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  created_by       text NOT NULL REFERENCES users(id),
  updated_by       text NOT NULL REFERENCES users(id)
);
CREATE TRIGGER item_touch BEFORE UPDATE ON item
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE item_default (
  item_id                     text NOT NULL REFERENCES item(id) ON DELETE CASCADE,
  company_id                  text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  default_income_account_id   text REFERENCES account(id),
  default_expense_account_id  text REFERENCES account(id),
  default_tax_template_id     text REFERENCES tax_template(id),
  PRIMARY KEY (item_id, company_id)
);

-- item_tax: per-item override (lookup by template+category)
CREATE TABLE item_tax (
  id              text PRIMARY KEY,
  item_id         text NOT NULL REFERENCES item(id) ON DELETE CASCADE,
  tax_category_id text REFERENCES tax_category(id),
  tax_template_id text REFERENCES tax_template(id),
  rate            numeric(9,4),
  UNIQUE (item_id, tax_category_id)
);

-- ============================================================================
-- CUSTOMER + SUPPLIER (globally shared, per-company defaults)
-- ============================================================================

CREATE TABLE customer_group (
  id         text PRIMARY KEY,
  name       text NOT NULL UNIQUE,
  parent_id  text REFERENCES customer_group(id),
  lft        integer,
  rgt        integer,
  is_group   boolean NOT NULL DEFAULT false,
  is_deleted boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customer (
  id                text PRIMARY KEY,
  name              text NOT NULL UNIQUE,
  display_name      text NOT NULL,
  customer_group_id text REFERENCES customer_group(id),
  territory_id      text,
  default_currency  text REFERENCES currency(code),
  npwp              text,
  is_individual     boolean NOT NULL DEFAULT false,
  email             text,
  phone             text,
  is_deleted        boolean NOT NULL DEFAULT false,
  custom_fields     jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  created_by        text NOT NULL REFERENCES users(id),
  updated_by        text NOT NULL REFERENCES users(id),
  CHECK (npwp IS NULL OR npwp ~ '^[0-9]{16}$')
);
CREATE TRIGGER customer_touch BEFORE UPDATE ON customer
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE customer_default (
  customer_id                   text NOT NULL REFERENCES customer(id) ON DELETE CASCADE,
  company_id                    text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  default_receivable_account_id text REFERENCES account(id),
  default_currency              text REFERENCES currency(code),
  default_tax_template_id       text REFERENCES tax_template(id),
  PRIMARY KEY (customer_id, company_id)
);

CREATE TABLE supplier_group (
  id         text PRIMARY KEY,
  name       text NOT NULL UNIQUE,
  parent_id  text REFERENCES supplier_group(id),
  lft        integer,
  rgt        integer,
  is_group   boolean NOT NULL DEFAULT false,
  is_deleted boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE supplier (
  id                text PRIMARY KEY,
  name              text NOT NULL UNIQUE,
  display_name      text NOT NULL,
  supplier_group_id text REFERENCES supplier_group(id),
  default_currency  text REFERENCES currency(code),
  npwp              text,
  is_individual     boolean NOT NULL DEFAULT false,
  email             text,
  phone             text,
  is_deleted        boolean NOT NULL DEFAULT false,
  custom_fields     jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  created_by        text NOT NULL REFERENCES users(id),
  updated_by        text NOT NULL REFERENCES users(id),
  CHECK (npwp IS NULL OR npwp ~ '^[0-9]{16}$')
);
CREATE TRIGGER supplier_touch BEFORE UPDATE ON supplier
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE supplier_default (
  supplier_id                text NOT NULL REFERENCES supplier(id) ON DELETE CASCADE,
  company_id                 text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  default_payable_account_id text REFERENCES account(id),
  default_currency           text REFERENCES currency(code),
  default_tax_template_id    text REFERENCES tax_template(id),
  PRIMARY KEY (supplier_id, company_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS supplier_default;
DROP TABLE IF EXISTS supplier;
DROP TABLE IF EXISTS supplier_group;
DROP TABLE IF EXISTS customer_default;
DROP TABLE IF EXISTS customer;
DROP TABLE IF EXISTS customer_group;
DROP TABLE IF EXISTS item_tax;
DROP TABLE IF EXISTS item_default;
DROP TABLE IF EXISTS item;
DROP TABLE IF EXISTS item_group;
DROP TABLE IF EXISTS withholding_tax_type;
DROP TABLE IF EXISTS tax_template_line;
DROP TABLE IF EXISTS tax_template;
DROP TABLE IF EXISTS tax_category;
-- +goose StatementEnd
