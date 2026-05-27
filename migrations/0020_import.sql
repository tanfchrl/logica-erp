-- +goose Up
-- ============================================================================
-- IMPORT JOB — append-only record of every bulk-import attempt, with per-row
-- errors stored as a JSONB array so the UI can show "what went wrong" later.
-- ============================================================================

CREATE TABLE import_job (
  id            text PRIMARY KEY,
  doctype       text NOT NULL,
  company_id    text REFERENCES company(id) ON DELETE SET NULL,
  mapping       jsonb NOT NULL DEFAULT '{}'::jsonb,    -- CSV column → field key
  total_rows    int NOT NULL DEFAULT 0,
  success_rows  int NOT NULL DEFAULT 0,
  error_rows    int NOT NULL DEFAULT 0,
  status        text NOT NULL DEFAULT 'committed'
                CHECK (status IN ('preview','committed','failed')),
  errors        jsonb NOT NULL DEFAULT '[]'::jsonb,   -- [{row_no, message, field?}]
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    text NOT NULL REFERENCES users(id)
);
CREATE INDEX import_job_doctype_idx ON import_job (doctype, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS import_job;
