-- +goose Up
-- +goose StatementBegin

-- doc_event + doc_view — partitioned logs that replace the single-table
-- document_audit. Range-partitioned on occurred_at so old partitions can be
-- detached/dropped wholesale (no VACUUM FULL ever needed) and so each
-- partition's indexes stay small even at billions of rows.
--
-- Retention defaults (enforced by the in-process partition manager):
--   doc_event: 36 months
--   doc_view:  180 days
--
-- Partition cadence: doc_event monthly, doc_view daily (views are 50-100x
-- more frequent than writes, so a finer partition gives faster pruning).

-- ============================================================================
-- doc_event — write log: create/update/submit/cancel/amend/delete events
-- ============================================================================
CREATE TABLE doc_event (
  id          text NOT NULL,
  doctype     text NOT NULL,
  document_id text NOT NULL,
  action      text NOT NULL CHECK (action IN ('create','update','submit','cancel','amend','delete')),
  changed_by  text NOT NULL REFERENCES users(id),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  diff        jsonb NOT NULL DEFAULT '{}'::jsonb,
  PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);

-- Initial monthly partitions: ~6 prior + current + next 2 (9 total) so the
-- in-process partition manager has runway on first deploy.
DO $$
DECLARE
  start_month date;
  i int;
  suffix text;
  lo timestamptz;
  hi timestamptz;
BEGIN
  start_month := date_trunc('month', now() - interval '6 months')::date;
  FOR i IN 0..8 LOOP
    suffix := to_char(start_month + (i * interval '1 month'), 'YYYY_MM');
    lo := start_month + (i * interval '1 month');
    hi := start_month + ((i + 1) * interval '1 month');
    EXECUTE format(
      'CREATE TABLE doc_event_%s PARTITION OF doc_event FOR VALUES FROM (%L) TO (%L)',
      suffix, lo, hi);
    EXECUTE format(
      'CREATE INDEX doc_event_%s_doc_idx ON doc_event_%s (doctype, document_id, occurred_at DESC)',
      suffix, suffix);
    EXECUTE format(
      'CREATE INDEX doc_event_%s_user_idx ON doc_event_%s (changed_by, occurred_at DESC)',
      suffix, suffix);
  END LOOP;
END$$;

-- Backfill from the old single-table audit. Atomic in this migration.
INSERT INTO doc_event (id, doctype, document_id, action, changed_by, occurred_at, diff)
SELECT id, doctype, document_id, action, changed_by, changed_at, diff FROM document_audit;

-- Append-only invariant — same guard as the legacy table.
CREATE OR REPLACE FUNCTION doc_event_block_update()
RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
  RAISE EXCEPTION 'doc_event/doc_view are append-only (% blocked)', TG_OP;
END$fn$;

CREATE TRIGGER doc_event_no_update BEFORE UPDATE OR DELETE ON doc_event
  FOR EACH ROW EXECUTE FUNCTION doc_event_block_update();

DROP TRIGGER IF EXISTS audit_no_update ON document_audit;
DROP TABLE document_audit;


-- ============================================================================
-- doc_view — read log: who viewed which document, deduped 24h server-side
-- ============================================================================
CREATE TABLE doc_view (
  id          text NOT NULL,
  doctype     text NOT NULL,
  document_id text NOT NULL,
  viewed_by   text NOT NULL REFERENCES users(id),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);

DO $$
DECLARE
  start_day date;
  i int;
  suffix text;
  lo timestamptz;
  hi timestamptz;
BEGIN
  start_day := (now() - interval '7 days')::date;
  FOR i IN 0..13 LOOP
    suffix := to_char(start_day + (i * interval '1 day'), 'YYYY_MM_DD');
    lo := start_day + (i * interval '1 day');
    hi := start_day + ((i + 1) * interval '1 day');
    EXECUTE format(
      'CREATE TABLE doc_view_%s PARTITION OF doc_view FOR VALUES FROM (%L) TO (%L)',
      suffix, lo, hi);
    -- Primary: timeline-for-this-doc lookup.
    EXECUTE format(
      'CREATE INDEX doc_view_%s_doc_idx ON doc_view_%s (doctype, document_id, occurred_at DESC)',
      suffix, suffix);
    -- Dedup: "did this user view this doc in the last 24h" lookup.
    EXECUTE format(
      'CREATE INDEX doc_view_%s_dedup_idx ON doc_view_%s (viewed_by, doctype, document_id, occurred_at DESC)',
      suffix, suffix);
  END LOOP;
END$$;

CREATE TRIGGER doc_view_no_update BEFORE UPDATE OR DELETE ON doc_view
  FOR EACH ROW EXECUTE FUNCTION doc_event_block_update();

-- +goose StatementEnd


-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS doc_view;
DROP TABLE IF EXISTS doc_event;
DROP FUNCTION IF EXISTS doc_event_block_update();

CREATE TABLE document_audit (
  id          text PRIMARY KEY,
  doctype     text NOT NULL,
  document_id text NOT NULL,
  action      text NOT NULL CHECK (action IN ('create','update','submit','cancel','amend','delete')),
  changed_by  text NOT NULL REFERENCES users(id),
  changed_at  timestamptz NOT NULL DEFAULT now(),
  diff        jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX document_audit_doc_idx  ON document_audit (doctype, document_id, changed_at DESC);
CREATE INDEX document_audit_user_idx ON document_audit (changed_by, changed_at DESC);
-- +goose StatementEnd
