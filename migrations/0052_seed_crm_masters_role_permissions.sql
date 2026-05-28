-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
  r record;
  new_doctypes text[] := ARRAY['lead_source','lost_reason'];
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
  -- Read access for any non-system role that already sees lead, so the
  -- picker on the opportunity form just works for sales users.
  FOR r IN
    SELECT DISTINCT rp.role_id AS id FROM role_permission rp
    JOIN role r2 ON r2.id = rp.role_id
    WHERE r2.is_system = false AND rp.doctype = 'lead' AND rp.can_read = true
  LOOP
    FOREACH dt IN ARRAY new_doctypes LOOP
      INSERT INTO role_permission
        (id, role_id, doctype, can_read, can_write, can_create, can_delete, can_submit, can_cancel, can_amend, can_print, can_export)
      VALUES (
        'rp_' || encode(gen_random_bytes(12), 'hex'),
        r.id, dt, true, false, false, false, false, false, false, false, false
      )
      ON CONFLICT (role_id, doctype) DO NOTHING;
    END LOOP;
  END LOOP;
END$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM role_permission WHERE doctype IN ('lead_source','lost_reason');
-- +goose StatementEnd
