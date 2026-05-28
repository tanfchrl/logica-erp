-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- SAVED VIEW
-- ============================================================================
-- Per-user per-doctype saved view config — filters, sort, group, column
-- set. Generic platform feature; CRM is the first consumer but POs / MRs /
-- assets get them for free.
--
-- Scope:
--   * is_shared = false (default) → only the owner sees it
--   * is_shared = true            → everyone in the company sees it
--
-- Body shape is opaque JSONB: the FE writes a shape that matches what the
-- ListView consumes ({ filters: [...], sort: {...}, columns: [...] }).
-- Migrating the shape later doesn't need a server change.

CREATE TABLE saved_view (
  id          text PRIMARY KEY,
  company_id  text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  doctype     text NOT NULL,
  name        text NOT NULL,
  is_shared   boolean NOT NULL DEFAULT false,
  body        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (company_id, user_id, doctype, name)
);
CREATE INDEX saved_view_lookup_idx ON saved_view (company_id, doctype, user_id);
CREATE INDEX saved_view_shared_idx ON saved_view (company_id, doctype) WHERE is_shared = true;
CREATE TRIGGER saved_view_touch BEFORE UPDATE ON saved_view
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS saved_view;
-- +goose StatementEnd
