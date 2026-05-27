-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- DEPARTMENT + DESIGNATION (simple per-company hierarchies)
-- ============================================================================
CREATE TABLE department (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  parent_id   text REFERENCES department(id),
  lft         integer,
  rgt         integer,
  is_group    boolean NOT NULL DEFAULT false,
  is_deleted  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE designation (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  description text,
  is_deleted  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================
-- EMPLOYEE
-- ============================================================================
CREATE TABLE employee (
  id              text PRIMARY KEY,
  name            text NOT NULL,                       -- employee id, e.g. EMP-0001
  company_id      text NOT NULL REFERENCES company(id),
  employee_name   text NOT NULL,
  gender          text CHECK (gender IS NULL OR gender IN ('male','female')),
  date_of_birth   date,
  date_of_joining date NOT NULL,
  date_of_relieving date,
  designation_id  text REFERENCES designation(id),
  department_id   text REFERENCES department(id),
  reports_to_id   text REFERENCES employee(id),
  status          text NOT NULL DEFAULT 'Active' CHECK (status IN ('Active','Inactive','Left','Suspended')),
  -- Indonesian payroll fields
  nik             text,                                 -- 16-digit NIK (KTP)
  npwp            text,                                 -- 16-digit NPWP
  ptkp_status     text CHECK (ptkp_status IS NULL OR ptkp_status IN ('TK/0','TK/1','TK/2','TK/3','K/0','K/1','K/2','K/3')),
  bpjs_kesehatan_no text,
  bpjs_tk_no      text,                                 -- BPJS Ketenagakerjaan
  -- Bank details for payroll
  bank_name       text,
  bank_account_no text,
  bank_account_name text,
  -- Default accounts
  payroll_payable_account_id text REFERENCES account(id),
  -- Misc
  email           text,
  phone           text,
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name),
  CHECK (nik  IS NULL OR nik  ~ '^[0-9]{16}$'),
  CHECK (npwp IS NULL OR npwp ~ '^[0-9]{16}$')
);
CREATE INDEX employee_co_idx ON employee (company_id, status);
CREATE TRIGGER employee_touch BEFORE UPDATE ON employee
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_emp', 'employee', NULL, 'EMP-.YYYY.-.####', true);

-- ============================================================================
-- ATTENDANCE
-- ============================================================================
CREATE TABLE attendance (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id),
  employee_id     text NOT NULL REFERENCES employee(id),
  attendance_date date NOT NULL,
  status          text NOT NULL CHECK (status IN ('Present','Absent','Half Day','On Leave','Work From Home')),
  in_time         time,
  out_time        time,
  working_hours   numeric(5,2) NOT NULL DEFAULT 0,
  remarks         text,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (employee_id, attendance_date)
);
CREATE INDEX attendance_co_date_idx ON attendance (company_id, attendance_date);
CREATE TRIGGER attendance_touch BEFORE UPDATE ON attendance
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- LEAVE
-- ============================================================================
CREATE TABLE leave_type (
  id              text PRIMARY KEY,
  name            text NOT NULL UNIQUE,
  max_days_allowed integer NOT NULL DEFAULT 12,
  is_carry_forward boolean NOT NULL DEFAULT false,
  is_paid          boolean NOT NULL DEFAULT true,
  is_deleted       boolean NOT NULL DEFAULT false
);

CREATE TABLE leave_allocation (
  id              text PRIMARY KEY,
  employee_id     text NOT NULL REFERENCES employee(id),
  leave_type_id   text NOT NULL REFERENCES leave_type(id),
  from_date       date NOT NULL,
  to_date         date NOT NULL,
  total_leaves_allocated numeric(5,2) NOT NULL,
  total_leaves_taken     numeric(5,2) NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (employee_id, leave_type_id, from_date, to_date)
);

CREATE TABLE leave_application (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id),
  employee_id     text NOT NULL REFERENCES employee(id),
  leave_type_id   text NOT NULL REFERENCES leave_type(id),
  from_date       date NOT NULL,
  to_date         date NOT NULL,
  total_days      numeric(5,2) NOT NULL,
  half_day        boolean NOT NULL DEFAULT false,
  reason          text,
  status          text NOT NULL DEFAULT 'Open' CHECK (status IN ('Open','Approved','Rejected','Cancelled')),
  approver_id     text REFERENCES users(id),
  approved_at     timestamptz,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name),
  CHECK (to_date >= from_date)
);
CREATE INDEX leave_app_emp_idx ON leave_application (employee_id, from_date);
CREATE TRIGGER leave_app_touch BEFORE UPDATE ON leave_application
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_leaveapp', 'leave_application', NULL, 'LA-.YYYY.-.####', true);

-- ============================================================================
-- EXPENSE CLAIM (simple)
-- ============================================================================
CREATE TABLE expense_claim (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id),
  employee_id     text NOT NULL REFERENCES employee(id),
  posting_date    date NOT NULL,
  total_claimed_amount numeric(18,4) NOT NULL DEFAULT 0,
  total_sanctioned_amount numeric(18,4) NOT NULL DEFAULT 0,
  status          text NOT NULL DEFAULT 'Draft',
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
CREATE TRIGGER expense_claim_touch BEFORE UPDATE ON expense_claim
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE expense_claim_item (
  id              text PRIMARY KEY,
  expense_claim_id text NOT NULL REFERENCES expense_claim(id) ON DELETE CASCADE,
  row_index       integer NOT NULL,
  expense_date    date NOT NULL,
  expense_type    text NOT NULL,
  description     text,
  expense_account_id text NOT NULL REFERENCES account(id),
  amount          numeric(18,4) NOT NULL,
  sanctioned_amount numeric(18,4) NOT NULL DEFAULT 0,
  UNIQUE (expense_claim_id, row_index)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_exp', 'expense_claim', NULL, 'EXP-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE doctype IN ('employee','leave_application','expense_claim');
DROP TABLE IF EXISTS expense_claim_item;
DROP TABLE IF EXISTS expense_claim;
DROP TABLE IF EXISTS leave_application;
DROP TABLE IF EXISTS leave_allocation;
DROP TABLE IF EXISTS leave_type;
DROP TABLE IF EXISTS attendance;
DROP TABLE IF EXISTS employee;
DROP TABLE IF EXISTS designation;
DROP TABLE IF EXISTS department;
-- +goose StatementEnd
