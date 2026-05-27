-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- CONTACT (CRM People)
-- ============================================================================
-- A human contact attached to a customer / supplier / lead via the
-- (parent_doctype, parent_id) dynamic-link pattern. One parent can have
-- many contacts; the is_primary flag marks the default for
-- copy-to-Invoice-style flows.
--
-- For v1 we hard-restrict parent_doctype to the three CRM parents via a
-- CHECK constraint. Widening to (e.g.) employee or project later is a
-- one-line change.

CREATE TABLE contact (
  id              text PRIMARY KEY,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  parent_doctype  text NOT NULL CHECK (parent_doctype IN ('customer','supplier','lead')),
  parent_id       text NOT NULL,
  first_name      text NOT NULL,
  last_name       text,
  full_name       text GENERATED ALWAYS AS (
                    coalesce(first_name, '') ||
                    CASE WHEN last_name IS NOT NULL AND last_name <> ''
                         THEN ' ' || last_name ELSE '' END
                  ) STORED,
  email           text,
  phone           text,
  job_title       text,
  -- One primary per (parent_doctype, parent_id) — enforced via partial
  -- unique index. Service ensures setting is_primary on a new contact
  -- demotes the previous primary.
  is_primary      boolean NOT NULL DEFAULT false,
  is_deleted      boolean NOT NULL DEFAULT false,
  custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id)
);
CREATE INDEX contact_parent_idx ON contact (parent_doctype, parent_id)
  WHERE is_deleted = false;
CREATE INDEX contact_company_idx ON contact (company_id)
  WHERE is_deleted = false;
CREATE UNIQUE INDEX contact_one_primary_idx
  ON contact (parent_doctype, parent_id)
  WHERE is_primary = true AND is_deleted = false;
CREATE TRIGGER contact_touch BEFORE UPDATE ON contact
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS contact;
-- +goose StatementEnd
