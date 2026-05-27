-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- SALARY COMPONENT (earning or deduction)
-- ============================================================================
CREATE TABLE salary_component (
  id              text PRIMARY KEY,
  name            text NOT NULL UNIQUE,
  component_type  text NOT NULL CHECK (component_type IN ('earning','deduction')),
  is_taxable      boolean NOT NULL DEFAULT true,            -- earnings included in PPh21 gross
  account_id      text NOT NULL REFERENCES account(id),     -- expense (earning) or liability (deduction)
  is_deleted      boolean NOT NULL DEFAULT false,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================
-- SALARY STRUCTURE — per-employee combination of components with amounts
-- ============================================================================
CREATE TABLE salary_structure (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  employee_id     text NOT NULL REFERENCES employee(id),
  from_date       date NOT NULL,
  to_date         date,
  is_active       boolean NOT NULL DEFAULT true,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE TRIGGER ss_touch BEFORE UPDATE ON salary_structure
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE salary_structure_component (
  id                  text PRIMARY KEY,
  salary_structure_id text NOT NULL REFERENCES salary_structure(id) ON DELETE CASCADE,
  row_index           integer NOT NULL,
  salary_component_id text NOT NULL REFERENCES salary_component(id),
  amount              numeric(18,4) NOT NULL,
  UNIQUE (salary_structure_id, row_index)
);

-- ============================================================================
-- PAYROLL ENTRY — batch payroll run for a period (one or more employees)
-- ============================================================================
CREATE TABLE payroll_entry (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  posting_date    date NOT NULL,
  fiscal_year_id  text NOT NULL REFERENCES fiscal_year(id),
  start_date      date NOT NULL,
  end_date        date NOT NULL,
  payment_account_id text NOT NULL REFERENCES account(id),  -- cash/bank from which net pay is disbursed
  total_gross     numeric(18,4) NOT NULL DEFAULT 0,
  total_deductions numeric(18,4) NOT NULL DEFAULT 0,
  total_net       numeric(18,4) NOT NULL DEFAULT 0,
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
CREATE TRIGGER pe_touch_2 BEFORE UPDATE ON payroll_entry
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- ============================================================================
-- SALARY SLIP — per-employee result of a payroll run
-- ============================================================================
CREATE TABLE salary_slip (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  payroll_entry_id text NOT NULL REFERENCES payroll_entry(id) ON DELETE CASCADE,
  employee_id     text NOT NULL REFERENCES employee(id),
  posting_date    date NOT NULL,
  start_date      date NOT NULL,
  end_date        date NOT NULL,
  gross_pay       numeric(18,4) NOT NULL DEFAULT 0,
  total_deductions numeric(18,4) NOT NULL DEFAULT 0,
  net_pay         numeric(18,4) NOT NULL DEFAULT 0,
  pph21_amount    numeric(18,4) NOT NULL DEFAULT 0,
  bpjs_employee_total numeric(18,4) NOT NULL DEFAULT 0,
  bpjs_employer_total numeric(18,4) NOT NULL DEFAULT 0,
  remarks         text,
  docstatus       smallint NOT NULL DEFAULT 0 CHECK (docstatus IN (0,1,2)),
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX slip_pe_idx ON salary_slip (payroll_entry_id);
CREATE TRIGGER slip_touch BEFORE UPDATE ON salary_slip
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

CREATE TABLE salary_slip_component (
  id              text PRIMARY KEY,
  salary_slip_id  text NOT NULL REFERENCES salary_slip(id) ON DELETE CASCADE,
  row_index       integer NOT NULL,
  salary_component_id text REFERENCES salary_component(id),
  component_name  text NOT NULL,                 -- snapshot of name (also covers system components like 'PPh 21', 'BPJS Kesehatan Employee')
  component_type  text NOT NULL CHECK (component_type IN ('earning','deduction')),
  amount          numeric(18,4) NOT NULL,
  account_id      text NOT NULL REFERENCES account(id),
  UNIQUE (salary_slip_id, row_index)
);

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default) VALUES
  ('nms_default_ss',     'salary_structure', NULL, 'SS-.YYYY.-.####',   true),
  ('nms_default_payrun', 'payroll_entry',    NULL, 'PAYRUN-.YYYY.-.####', true),
  ('nms_default_slip',   'salary_slip',      NULL, 'SAL-.YYYY.-.####',  true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE doctype IN ('salary_structure','payroll_entry','salary_slip');
DROP TABLE IF EXISTS salary_slip_component;
DROP TABLE IF EXISTS salary_slip;
DROP TABLE IF EXISTS payroll_entry;
DROP TABLE IF EXISTS salary_structure_component;
DROP TABLE IF EXISTS salary_structure;
DROP TABLE IF EXISTS salary_component;
-- +goose StatementEnd
