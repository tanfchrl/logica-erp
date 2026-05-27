-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- ASSET MOVEMENT
-- ============================================================================
-- Tracks transfers of fixed assets between custodians or locations.
-- No GL impact — purely informational, but auditable.

CREATE TABLE asset_movement (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  asset_id        text NOT NULL REFERENCES asset(id) ON DELETE RESTRICT,
  movement_date   date NOT NULL,
  movement_type   text NOT NULL CHECK (movement_type IN ('issue','receipt','transfer')),
  -- Custodian = employee responsible. From/to are free-text strings rather than
  -- FK references for v1 so we don't force the user to pre-create custodians.
  -- A future migration can promote these into FKs.
  from_custodian  text,
  to_custodian    text NOT NULL,
  from_location   text,
  to_location     text NOT NULL,
  purpose         text NOT NULL DEFAULT '',
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
CREATE INDEX asset_movement_asset_idx ON asset_movement (asset_id, movement_date DESC);
CREATE INDEX asset_movement_co_idx    ON asset_movement (company_id, movement_date DESC);

-- Asset gets a current-state mirror of the latest movement so the asset
-- detail page can show "currently with X at Y" without a separate query.
ALTER TABLE asset
  ADD COLUMN current_custodian text,
  ADD COLUMN current_location  text;

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_amv', 'asset_movement', NULL, 'AMV-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_amv';
ALTER TABLE asset DROP COLUMN IF EXISTS current_custodian, DROP COLUMN IF EXISTS current_location;
DROP TABLE IF EXISTS asset_movement;
-- +goose StatementEnd
