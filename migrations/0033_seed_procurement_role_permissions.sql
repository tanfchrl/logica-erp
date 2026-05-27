-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- Seed role_permission rows for the new procurement doctypes
-- ============================================================================
--
-- The system_administrator role bypasses the per-doctype check via the
-- `users.is_system` shortcut, so the practical effect is small for admins.
-- This backfill matters when:
--
--   (a) an operator created a "Manager" / "Accountant" role and clicked
--       Read/Write on most accounting doctypes — they shouldn't suddenly
--       lose visibility because new doctypes were added.
--   (b) any other role flagged `is_system = true` exists in this database.
--
-- Strategy: for every role currently flagged is_system=true, grant every
-- action on the new doctypes (matches cmd/logica seed.ensureFullPermissions).
-- Non-system roles get a read-only grant on the read-heavy doctypes only,
-- preserving any explicit permission boundaries the admin set up.
--
-- Idempotent — uses ON CONFLICT DO NOTHING against the
-- (role_id, doctype) unique key.

DO $$
DECLARE
  r record;
  new_doctypes text[] := ARRAY['material_request','purchase_receipt','buying_settings'];
  dt text;
BEGIN
  -- System roles: grant everything on the new doctypes.
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

  -- Non-system roles that already had read access to purchase_invoice get
  -- read access to MR + GRN too. Mirrors how a typical "Accountant" role
  -- would already be touching purchase docs. buying_settings is admin-only
  -- so we deliberately don't seed it for non-system roles.
  FOR r IN
    SELECT DISTINCT rp.role_id AS id
    FROM role_permission rp
    JOIN role r2 ON r2.id = rp.role_id
    WHERE r2.is_system = false
      AND rp.doctype = 'purchase_invoice'
      AND rp.can_read = true
  LOOP
    FOREACH dt IN ARRAY ARRAY['material_request','purchase_receipt'] LOOP
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
DELETE FROM role_permission
  WHERE doctype IN ('material_request','purchase_receipt','buying_settings');
-- +goose StatementEnd
