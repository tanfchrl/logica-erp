-- +goose Up
-- Optional per-user IP allowlist (CIDR blocks). Empty = no restriction.
-- Login is rejected when the user has any CIDRs set and the source IP doesn't match.
ALTER TABLE users ADD COLUMN ip_allowlist cidr[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE users DROP COLUMN ip_allowlist;
