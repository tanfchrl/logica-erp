-- +goose Up
-- +goose StatementBegin

-- 0049: widen task so it can hang off any record (not just project)
--
-- Adds parent_doctype + parent_id alongside the existing project_id.
-- The new fields are nullable; when either is set, the task displays
-- on that record's detail page. Existing project-linked tasks
-- keep working (project_id stays).
--
-- Adds:
--   - assigned_to (user FK) — who owns the task
--   - due_date  — the date columns we already have are exp_*/act_*,
--                 which is more elaborate than CRM tasks need. due_date
--                 is the single "do it by" column the Tasks panel uses.

ALTER TABLE task
  ADD COLUMN parent_doctype text,
  ADD COLUMN parent_id      text,
  ADD COLUMN assigned_to    text REFERENCES users(id),
  ADD COLUMN due_date       date;

-- task_parent_idx is already taken in 0010 (for parent_task_id, the
-- recursive subtask link). Use a distinct name.
CREATE INDEX task_dynamic_parent_idx ON task (parent_doctype, parent_id)
  WHERE is_deleted = false AND parent_doctype IS NOT NULL;
CREATE INDEX task_assigned_idx ON task (assigned_to, status)
  WHERE is_deleted = false AND status IN ('Open','Working');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS task_assigned_idx;
DROP INDEX IF EXISTS task_dynamic_parent_idx;
ALTER TABLE task
  DROP COLUMN IF EXISTS parent_doctype,
  DROP COLUMN IF EXISTS parent_id,
  DROP COLUMN IF EXISTS assigned_to,
  DROP COLUMN IF EXISTS due_date;
-- +goose StatementEnd
