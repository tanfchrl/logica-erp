-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- ASSET LOCATION
-- ============================================================================
-- Asset Location is a hierarchical master for physical places assets sit at
-- (e.g. "Jakarta HQ" → "Floor 5" → "Conference Room A"). It's separate from
-- warehouse because warehouses are stock-flow nodes; locations are
-- placement labels.
--
-- v1 keeps the existing free-text `current_location` /
-- asset_movement.{from_location,to_location} columns for backward compat.
-- The new FK columns are nullable; when set, the service mirrors
-- asset_location.name into the legacy text column on submit so reports
-- that already read the text path don't break.

CREATE TABLE asset_location (
  id          text PRIMARY KEY,
  company_id  text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  parent_id   text REFERENCES asset_location(id),
  name        text NOT NULL,
  -- Optional free text for the address; kept simple for v1 (no FK to a
  -- separate address table).
  address     text,
  is_group    boolean NOT NULL DEFAULT false,
  is_deleted  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (company_id, name)
);
CREATE INDEX asset_location_parent_idx ON asset_location (parent_id) WHERE is_deleted = false;
CREATE INDEX asset_location_co_idx     ON asset_location (company_id) WHERE is_deleted = false;

-- Asset gets a nullable FK alongside the existing text field.
ALTER TABLE asset
  ADD COLUMN current_location_id text REFERENCES asset_location(id);

-- Asset Movement gets nullable from/to FKs alongside the existing text fields.
ALTER TABLE asset_movement
  ADD COLUMN from_location_id text REFERENCES asset_location(id),
  ADD COLUMN to_location_id   text REFERENCES asset_location(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE asset_movement
  DROP COLUMN IF EXISTS from_location_id,
  DROP COLUMN IF EXISTS to_location_id;
ALTER TABLE asset
  DROP COLUMN IF EXISTS current_location_id;
DROP TABLE IF EXISTS asset_location;
-- +goose StatementEnd
