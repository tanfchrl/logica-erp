-- +goose Up
-- +goose StatementBegin

-- Seed role_permission for the new CRM doctypes: contact, opportunity,
-- note. Same pattern as 0033 / 0043 — full grants for system roles,
-- read-only for non-system roles that already see `lead`.

DO $$
DECLARE
  r record;
  new_doctypes text[] := ARRAY['contact','opportunity','note'];
  dt text;
BEGIN
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

  FOR r IN
    SELECT DISTINCT rp.role_id AS id
    FROM role_permission rp
    JOIN role r2 ON r2.id = rp.role_id
    WHERE r2.is_system = false
      AND rp.doctype = 'lead'
      AND rp.can_read = true
  LOOP
    FOREACH dt IN ARRAY new_doctypes LOOP
      INSERT INTO role_permission
        (id, role_id, doctype, can_read, can_write, can_create, can_delete, can_submit, can_cancel, can_amend, can_print, can_export)
      VALUES (
        'rp_' || encode(gen_random_bytes(12), 'hex'),
        r.id, dt, true, true, true, false, false, false, false, true, true
      )
      ON CONFLICT (role_id, doctype) DO NOTHING;
    END LOOP;
  END LOOP;
END$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permission WHERE doctype IN ('contact','opportunity','note');
-- +goose StatementEnd
