-- +goose Up
-- +goose StatementBegin

-- agent_policy_value_limit caps Tier-1 drafts by amount per doctype.
-- Spec §2: "Tier 1 allowed for POs under Rp 50 juta" — this is the
-- backing store for those rules.
--
-- (company_id, doctype, field) is unique. NULL company_id is the
-- "global default" row; a per-company override row beats it.
--
-- Limits are checked at Tier-1 dispatch time in cmd/agent — see
-- internal/agent/policy/gate.go::CheckPayload.

CREATE TABLE agent_policy_value_limit (
  id         text PRIMARY KEY,
  company_id text REFERENCES company(id) ON DELETE CASCADE,  -- NULL = global default
  doctype    text NOT NULL,
  field      text NOT NULL,        -- e.g. 'grand_total', 'paid_amount'
  max_idr    numeric(20,2) NOT NULL CHECK (max_idr >= 0),
  -- Optional human-readable label shown in the policy_blocked audit row
  -- (e.g. "above the Rp 50 juta agent limit").
  label      text NOT NULL DEFAULT '',
  is_active  boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX agent_policy_value_limit_key
  ON agent_policy_value_limit (coalesce(company_id, ''), doctype, field);
CREATE INDEX agent_policy_value_limit_active_idx
  ON agent_policy_value_limit (doctype, is_active) WHERE is_active;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_policy_value_limit;
-- +goose StatementEnd
