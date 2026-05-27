-- +goose Up
-- +goose StatementBegin

-- Seed role_permission for the asset-module doctypes added across 0034-0040
-- so existing databases don't strand non-system roles. Same pattern as
-- 0033_seed_procurement_role_permissions.sql.

DO $$
DECLARE
  r record;
  new_doctypes text[] := ARRAY[
    'asset_category',
    'asset_movement',
    'asset_value_adjustment',
    'finance_book',
    'asset_settings'
  ];
  dt text;
BEGIN
  -- System roles: full grant.
  FOR r IN SELECT id FROM role WHERE is_system = true LOOP
    FOREACH dt IN ARRAY new_doctypes LOOP
      INSERT INTO role_permission
        (id, role_id, doctype, can_read, can_write, can_create, can_delete, can_submit, can_cancel, can_amend, can_print, can_export)
      VALUES (
        'rp_' || encode(gen_random_bytes(12), 'hex'),
        r.id, dt, true, true, true, true, true, true, true, true, true
      )
      ON CONFLICT (role_id, doctype) DO NOTHING;
    END LOOP;
  END LOOP;

  -- Non-system roles that already see `asset` get read-only on the new
  -- doctypes (except asset_settings, which is admin-only).
  FOR r IN
    SELECT DISTINCT rp.role_id AS id
    FROM role_permission rp
    JOIN role r2 ON r2.id = rp.role_id
    WHERE r2.is_system = false
      AND rp.doctype = 'asset'
      AND rp.can_read = true
  LOOP
    FOREACH dt IN ARRAY ARRAY['asset_category','asset_movement','asset_value_adjustment','finance_book'] LOOP
      INSERT INTO role_permission
        (id, role_id, doctype, can_read, can_write, can_create, can_delete, can_submit, can_cancel, can_amend, can_print, can_export)
      VALUES (
        'rp_' || encode(gen_random_bytes(12), 'hex'),
        r.id, dt, true, false, false, false, false, false, false, true, true
      )
      ON CONFLICT (role_id, doctype) DO NOTHING;
    END LOOP;
  END LOOP;
END$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permission WHERE doctype IN
  ('asset_category','asset_movement','asset_value_adjustment','finance_book','asset_settings');
-- +goose StatementEnd
