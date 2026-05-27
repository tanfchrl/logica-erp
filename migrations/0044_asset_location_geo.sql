-- +goose Up
-- +goose StatementBegin

-- 0044: lat/lng on asset_location for GIS integration
--
-- Plain numeric columns rather than PostGIS for now — the GIS platform
-- will read them via a feed. Adding PostGIS is a separate decision since
-- it requires the extension to be installed.
--
-- Precision: 7 decimals = ~1.1cm. CHECK constraints enforce valid bounds
-- so bad uploads can't poison the dataset.

ALTER TABLE asset_location
  ADD COLUMN latitude  numeric(10,7) CHECK (latitude  BETWEEN -90  AND 90),
  ADD COLUMN longitude numeric(11,7) CHECK (longitude BETWEEN -180 AND 180);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE asset_location
  DROP COLUMN IF EXISTS latitude,
  DROP COLUMN IF EXISTS longitude;
-- +goose StatementEnd
