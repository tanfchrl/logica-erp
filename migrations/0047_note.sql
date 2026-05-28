-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- NOTE
-- ============================================================================
-- Free-form text attached to any record via the same dynamic-link pattern
-- as contact. Notes are user-emitted (vs audit_log which is
-- system-emitted); the Timeline component merges both streams.
--
-- v1 keeps parent_doctype as free text so notes can attach to anything
-- (Opportunity / Customer / Asset / etc.). Service-layer validation
-- gates accepted values via a code-side allowlist that we widen as new
-- doctypes opt in.

CREATE TABLE note (
  id              text PRIMARY KEY,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  parent_doctype  text NOT NULL,
  parent_id       text NOT NULL,
  body            text NOT NULL,
  is_deleted      boolean NOT NULL DEFAULT false,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id)
);
CREATE INDEX note_parent_idx ON note (parent_doctype, parent_id, created_at DESC)
  WHERE is_deleted = false;
CREATE INDEX note_company_idx ON note (company_id, created_at DESC)
  WHERE is_deleted = false;
CREATE TRIGGER note_touch BEFORE UPDATE ON note
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS note;
-- +goose StatementEnd
