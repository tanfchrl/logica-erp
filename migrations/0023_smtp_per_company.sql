-- +goose Up
-- Promote smtp_config to per-company config. NULL company_id = workspace-wide
-- fallback. The dispatcher picks the most-specific row.

ALTER TABLE smtp_config DROP CONSTRAINT smtp_config_id_check;
ALTER TABLE smtp_config ALTER COLUMN id DROP DEFAULT;

ALTER TABLE smtp_config ADD COLUMN company_id text REFERENCES company(id) ON DELETE CASCADE;

-- One config per (company_id NULL or value). The coalesce trick is needed
-- because Postgres treats NULL = NULL as false for uniqueness.
CREATE UNIQUE INDEX smtp_config_company_uniq ON smtp_config (coalesce(company_id, '__workspace__'));

-- +goose Down
DROP INDEX IF EXISTS smtp_config_company_uniq;
ALTER TABLE smtp_config DROP COLUMN company_id;
ALTER TABLE smtp_config ALTER COLUMN id SET DEFAULT 'smtp_singleton';
ALTER TABLE smtp_config ADD CONSTRAINT smtp_config_id_check CHECK (id = 'smtp_singleton');
