-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- POS PROFILE
-- ============================================================================
CREATE TABLE pos_profile (
  id                   text PRIMARY KEY,
  name                 text NOT NULL UNIQUE,
  company_id           text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  warehouse_id         text REFERENCES warehouse(id),
  cash_account_id      text NOT NULL REFERENCES account(id),
  default_customer_id  text REFERENCES customer(id),
  default_tax_template_id text REFERENCES tax_template(id),
  income_account_id    text REFERENCES account(id),
  is_active            boolean NOT NULL DEFAULT true,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  created_by           text NOT NULL REFERENCES users(id),
  updated_by           text NOT NULL REFERENCES users(id)
);
CREATE TRIGGER pos_profile_touch BEFORE UPDATE ON pos_profile
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- POS INVOICE — light SI variant with immediate payment
-- ============================================================================
CREATE TABLE pos_invoice (
  id                  text PRIMARY KEY,
  name                text NOT NULL,
  company_id          text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  pos_profile_id      text NOT NULL REFERENCES pos_profile(id),
  customer_id         text NOT NULL REFERENCES customer(id),
  posting_date        date NOT NULL,
  posting_time        time NOT NULL DEFAULT now(),
  fiscal_year_id      text NOT NULL REFERENCES fiscal_year(id),
  currency            text NOT NULL REFERENCES currency(code),
  net_total           numeric(18,4) NOT NULL DEFAULT 0,
  total_taxes_and_charges numeric(18,4) NOT NULL DEFAULT 0,
  grand_total         numeric(18,4) NOT NULL DEFAULT 0,
  paid_amount         numeric(18,4) NOT NULL DEFAULT 0,
  -- snapshots
  base_net_total      numeric(18,4) NOT NULL DEFAULT 0,
  base_grand_total    numeric(18,4) NOT NULL DEFAULT 0,
  income_account_id   text NOT NULL REFERENCES account(id),
  cash_account_id     text NOT NULL REFERENCES account(id),
  is_offline          boolean NOT NULL DEFAULT false,           -- set if the slip originated from offline POS
  offline_key         text,                                     -- client-supplied idempotency key for offline sync
  docstatus           smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at        timestamptz,
  submitted_by        text REFERENCES users(id),
  cancelled_at        timestamptz,
  cancelled_by        text REFERENCES users(id),
  custom_fields       jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  created_by          text NOT NULL REFERENCES users(id),
  updated_by          text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name),
  UNIQUE (pos_profile_id, offline_key)
);
CREATE INDEX pos_invoice_co_idx ON pos_invoice (company_id, posting_date);
CREATE TRIGGER pos_invoice_touch BEFORE UPDATE ON pos_invoice
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE pos_invoice_item (
  id              text PRIMARY KEY,
  pos_invoice_id  text NOT NULL REFERENCES pos_invoice(id) ON DELETE CASCADE,
  row_index       integer NOT NULL,
  item_id         text REFERENCES item(id),
  item_code       text NOT NULL,
  item_name       text NOT NULL,
  qty             numeric(18,6) NOT NULL,
  uom             text NOT NULL,
  rate            numeric(18,4) NOT NULL,
  amount          numeric(18,4) NOT NULL,
  tax_amount      numeric(18,4) NOT NULL DEFAULT 0,
  total           numeric(18,4) NOT NULL,
  UNIQUE (pos_invoice_id, row_index)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_pos', 'pos_invoice', NULL, 'POS-.YYYY.-.####', true);

-- ============================================================================
-- HELPDESK
-- ============================================================================
CREATE TABLE service_level_agreement (
  id                  text PRIMARY KEY,
  name                text NOT NULL UNIQUE,
  default_priority    text NOT NULL DEFAULT 'Medium',
  response_time_hours integer NOT NULL DEFAULT 4,
  resolution_time_hours integer NOT NULL DEFAULT 24,
  is_default          boolean NOT NULL DEFAULT false,
  is_deleted          boolean NOT NULL DEFAULT false,
  created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE issue (
  id                  text PRIMARY KEY,
  name                text NOT NULL,
  company_id          text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  subject             text NOT NULL,
  description         text,
  status              text NOT NULL DEFAULT 'Open' CHECK (status IN ('Open','In Progress','On Hold','Resolved','Closed','Cancelled')),
  priority            text NOT NULL DEFAULT 'Medium' CHECK (priority IN ('Low','Medium','High','Urgent')),
  customer_id         text REFERENCES customer(id),
  contact_email       text,
  assigned_to_user_id text REFERENCES users(id),
  sla_id              text REFERENCES service_level_agreement(id),
  opened_at           timestamptz NOT NULL DEFAULT now(),
  response_due_at     timestamptz,
  resolution_due_at   timestamptz,
  first_responded_at  timestamptz,
  resolved_at         timestamptz,
  closed_at           timestamptz,
  resolution_remarks  text,
  custom_fields       jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  created_by          text NOT NULL REFERENCES users(id),
  updated_by          text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX issue_co_status_idx ON issue (company_id, status);
CREATE INDEX issue_assignee_idx  ON issue (assigned_to_user_id) WHERE assigned_to_user_id IS NOT NULL;
CREATE TRIGGER issue_touch BEFORE UPDATE ON issue
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_issue', 'issue', NULL, 'ISS-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE doctype IN ('pos_invoice','issue');
DROP TABLE IF EXISTS issue;
DROP TABLE IF EXISTS service_level_agreement;
DROP TABLE IF EXISTS pos_invoice_item;
DROP TABLE IF EXISTS pos_invoice;
DROP TABLE IF EXISTS pos_profile;
-- +goose StatementEnd
