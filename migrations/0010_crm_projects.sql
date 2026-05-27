-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- LEAD
-- ============================================================================
CREATE TABLE lead (
  id              text PRIMARY KEY,
  name            text NOT NULL UNIQUE,         -- internal code
  lead_name       text NOT NULL,                -- company / person name
  contact_email   text,
  contact_phone   text,
  source          text,                         -- 'website','referral','event','cold_call'
  status          text NOT NULL DEFAULT 'Open', -- Open|Contacted|Interested|Converted|Lost
  territory_id    text,
  converted_customer_id text REFERENCES customer(id),
  remarks         text,
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id)
);
CREATE TRIGGER lead_touch BEFORE UPDATE ON lead
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- PROJECT + TASK + TIMESHEET
-- ============================================================================
CREATE TABLE project (
  id              text PRIMARY KEY,
  name            text NOT NULL UNIQUE,
  project_name    text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  customer_id     text REFERENCES customer(id),
  status          text NOT NULL DEFAULT 'Open', -- Open|Completed|Cancelled
  start_date      date,
  expected_end_date date,
  actual_end_date date,
  estimated_costing numeric(18,4) NOT NULL DEFAULT 0,
  total_billable_amount numeric(18,4) NOT NULL DEFAULT 0,
  total_billed_amount   numeric(18,4) NOT NULL DEFAULT 0,
  remarks         text,
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id)
);
CREATE INDEX project_co_idx ON project (company_id);
CREATE TRIGGER project_touch BEFORE UPDATE ON project
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE task (
  id              text PRIMARY KEY,
  name            text NOT NULL UNIQUE,
  subject         text NOT NULL,
  project_id      text REFERENCES project(id),
  parent_task_id  text REFERENCES task(id),
  status          text NOT NULL DEFAULT 'Open',  -- Open|Working|Completed|Cancelled
  priority        text NOT NULL DEFAULT 'Medium',
  exp_start_date  date,
  exp_end_date    date,
  act_start_date  date,
  act_end_date    date,
  description     text,
  depends_on      text[],                        -- task ids this task depends on
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id)
);
CREATE INDEX task_project_idx ON task (project_id);
CREATE INDEX task_parent_idx  ON task (parent_task_id);
CREATE TRIGGER task_touch BEFORE UPDATE ON task
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE activity_type (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  default_rate numeric(18,4) NOT NULL DEFAULT 0,
  is_deleted  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE timesheet (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  employee_id     text,                          -- FK in Phase 5 employee table
  start_date      date NOT NULL,
  end_date        date NOT NULL,
  total_hours     numeric(10,2) NOT NULL DEFAULT 0,
  total_billable_hours numeric(10,2) NOT NULL DEFAULT 0,
  total_billable_amount numeric(18,4) NOT NULL DEFAULT 0,
  total_billed_amount   numeric(18,4) NOT NULL DEFAULT 0,
  status          text NOT NULL DEFAULT 'Draft', -- Draft|Submitted|Billed|Cancelled
  customer_id     text REFERENCES customer(id),
  remarks         text,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  submitted_at    timestamptz,
  submitted_by    text REFERENCES users(id),
  cancelled_at    timestamptz,
  cancelled_by    text REFERENCES users(id),
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE TRIGGER ts_touch BEFORE UPDATE ON timesheet
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE timesheet_entry (
  id              text PRIMARY KEY,
  timesheet_id    text NOT NULL REFERENCES timesheet(id) ON DELETE CASCADE,
  row_index       integer NOT NULL,
  activity_type_id text REFERENCES activity_type(id),
  project_id      text REFERENCES project(id),
  task_id         text REFERENCES task(id),
  description     text,
  from_time       timestamptz NOT NULL,
  to_time         timestamptz NOT NULL,
  hours           numeric(10,2) NOT NULL,
  is_billable     boolean NOT NULL DEFAULT true,
  billing_rate    numeric(18,4) NOT NULL DEFAULT 0,
  billing_amount  numeric(18,4) NOT NULL DEFAULT 0,
  sales_invoice_item_id text,                    -- set when billed
  UNIQUE (timesheet_id, row_index),
  CHECK (to_time > from_time)
);
CREATE INDEX te_project_idx ON timesheet_entry (project_id);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default) VALUES
  ('nms_default_lead',    'lead',      NULL, 'LEAD-.YYYY.-.####', true),
  ('nms_default_project', 'project',   NULL, 'PROJ-.YYYY.-.####', true),
  ('nms_default_task',    'task',      NULL, 'TASK-.YYYY.-.####', true),
  ('nms_default_ts',      'timesheet', NULL, 'TS-.YYYY.-.####',   true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE doctype IN ('lead','project','task','timesheet');
DROP TABLE IF EXISTS timesheet_entry;
DROP TABLE IF EXISTS timesheet;
DROP TABLE IF EXISTS activity_type;
DROP TABLE IF EXISTS task;
DROP TABLE IF EXISTS project;
DROP TABLE IF EXISTS lead;
-- +goose StatementEnd
