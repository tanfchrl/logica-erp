-- +goose Up
-- ============================================================================
-- APPROVAL RULES — declarative triggers that demand approver action on a
-- document before it can be submitted. The engine that consults these (and
-- gates submit accordingly) is a follow-up; this migration only stores the
-- rules so the config UI is functional today.
--
-- A rule fires when its WHERE clause matches: doctype + optional company +
-- optional condition (e.g. grand_total > 50000000). On fire, the doc must
-- pass through the named role's approval before submit succeeds.
-- ============================================================================

CREATE TABLE approval_rule (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  doctype         text NOT NULL,
  company_id      text REFERENCES company(id) ON DELETE CASCADE,

  -- Optional condition. NULL condition_field = always fires for the doctype.
  condition_field text,                                    -- e.g. 'grand_total', 'amount'
  condition_op    text CHECK (condition_op IN ('=','<>','>','>=','<','<=') OR condition_op IS NULL),
  condition_value text,                                    -- compared as numeric for amount ops

  -- Required approver: a role. Anyone with that role can approve.
  required_role_id text NOT NULL REFERENCES role(id) ON DELETE RESTRICT,

  -- Sequence among multiple rules. Lower = earlier. Ties allowed.
  sequence        int  NOT NULL DEFAULT 100,
  is_active       boolean NOT NULL DEFAULT true,
  description     text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text REFERENCES users(id),
  updated_by      text REFERENCES users(id)
);
CREATE INDEX approval_rule_doctype_idx ON approval_rule (doctype, is_active);

-- ----------------------------------------------------------------------------
-- APPROVAL REQUESTS — the per-document pending state created when a rule
-- fires. Materialized so an "Approvals inbox" can show what awaits each
-- approver without re-scanning every document on every page load.
-- ----------------------------------------------------------------------------
CREATE TABLE approval_request (
  id              text PRIMARY KEY,
  rule_id         text NOT NULL REFERENCES approval_rule(id) ON DELETE CASCADE,
  doctype         text NOT NULL,
  document_id     text NOT NULL,
  document_name   text NOT NULL,                          -- human-readable e.g. 'PI-2026-00018'
  required_role_id text NOT NULL REFERENCES role(id),

  status          text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','approved','rejected','cancelled')),
  decided_by      text REFERENCES users(id),
  decided_at      timestamptz,
  decision_note   text NOT NULL DEFAULT '',

  requested_by    text NOT NULL REFERENCES users(id),
  requested_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (doctype, document_id, rule_id)
);
CREATE INDEX approval_request_status_idx  ON approval_request (status, requested_at DESC);
CREATE INDEX approval_request_role_idx    ON approval_request (required_role_id) WHERE status = 'pending';
CREATE INDEX approval_request_doc_idx     ON approval_request (doctype, document_id);

-- +goose Down
DROP TABLE IF EXISTS approval_request;
DROP TABLE IF EXISTS approval_rule;
