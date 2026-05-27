-- +goose Up
-- +goose StatementBegin

-- Agent service tables. The agent runs as a separate process (cmd/agent) but
-- shares the database — its tables live alongside the ERP core schema. The
-- agent never reads or writes ERP core tables directly; all of those go
-- through the public REST API as the acting user.

-- ---------------------------------------------------------------------------
-- agent_session: one row per Copilot conversation thread or Migration Wizard
-- run. Conversation messages live in agent_conversation linked by session_id.
-- ---------------------------------------------------------------------------
CREATE TABLE agent_session (
  id          text PRIMARY KEY,
  user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  company_id  text,
  kind        text NOT NULL CHECK (kind IN ('copilot','migration')),
  -- For migration sessions, the resumable workflow state (which step, the
  -- SetupProfile, COA proposal, staged data refs). JSON to stay flexible.
  state       jsonb NOT NULL DEFAULT '{}'::jsonb,
  title       text NOT NULL DEFAULT '',
  closed_at   timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_session_user_idx  ON agent_session (user_id, created_at DESC);
CREATE INDEX agent_session_open_idx  ON agent_session (user_id, kind) WHERE closed_at IS NULL;

-- ---------------------------------------------------------------------------
-- agent_conversation: chronological message log per session. Mirrors the
-- OpenAI chat-completions message shape (role + content + tool_calls / tool
-- responses) so the agent can replay it into any OpenAI-compatible model.
-- ---------------------------------------------------------------------------
CREATE TABLE agent_conversation (
  id          text PRIMARY KEY,
  session_id  text NOT NULL REFERENCES agent_session(id) ON DELETE CASCADE,
  turn        int NOT NULL,                           -- 1-based sequential ordering
  role        text NOT NULL CHECK (role IN ('user','assistant','tool','system')),
  content     text NOT NULL DEFAULT '',
  -- tool_calls + tool_call_id support the standard tool-use loop:
  --   assistant emits tool_calls (json array), tool role replies with the
  --   tool_call_id it answered.
  tool_calls    jsonb,
  tool_call_id  text,
  tool_name     text,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_conv_session_idx ON agent_conversation (session_id, turn);

-- ---------------------------------------------------------------------------
-- agent_audit_log: append-only event log. Trust foundation per spec §8.
-- Every prompt, tool call, tool result, proposal, approval, rejection, and
-- policy block lands here. Used to drive cost reporting and the future
-- "accuracy audit" that will justify enabling Tier 2.
--
-- Partitioned by month on created_at so retention/archival mirrors the audit
-- pattern from migration 0025. Partition manager wires up forward partitions
-- on a daily tick.
-- ---------------------------------------------------------------------------
CREATE TABLE agent_audit_log (
  id          text NOT NULL,
  session_id  text NOT NULL,
  user_id     text NOT NULL,
  company_id  text,
  turn        int NOT NULL DEFAULT 0,
  event_type  text NOT NULL CHECK (event_type IN (
    'prompt','tool_call','tool_result','proposal',
    'human_approved','human_rejected','policy_blocked',
    'error'
  )),
  payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
  model       text NOT NULL DEFAULT '',
  tokens_in   int NOT NULL DEFAULT 0,
  tokens_out  int NOT NULL DEFAULT 0,
  latency_ms  int NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (created_at, id)
) PARTITION BY RANGE (created_at);

-- Initial monthly partitions: 6 prior + current + next 2 — same runway as
-- doc_event from migration 0025.
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
      'CREATE TABLE agent_audit_log_%s PARTITION OF agent_audit_log FOR VALUES FROM (%L) TO (%L)',
      suffix, lo, hi);
    EXECUTE format(
      'CREATE INDEX agent_audit_log_%s_session_idx ON agent_audit_log_%s (session_id, turn, created_at)',
      suffix, suffix);
    EXECUTE format(
      'CREATE INDEX agent_audit_log_%s_user_idx ON agent_audit_log_%s (user_id, created_at DESC)',
      suffix, suffix);
  END LOOP;
END$$;

-- Append-only invariant — same guard pattern as doc_event.
CREATE OR REPLACE FUNCTION agent_audit_block_update()
RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
  RAISE EXCEPTION 'agent_audit_log is append-only (% blocked)', TG_OP;
END$fn$;

CREATE TRIGGER agent_audit_no_update BEFORE UPDATE OR DELETE ON agent_audit_log
  FOR EACH ROW EXECUTE FUNCTION agent_audit_block_update();

-- ---------------------------------------------------------------------------
-- agent_approval_queue: Tier-1 drafts the copilot has produced and is awaiting
-- human review/submit. Distinct from the workflow approval_request table —
-- that one gates submitted docs against business approval rules, this one is
-- the agent's own "drafts that need a human to open and Submit" queue.
-- ---------------------------------------------------------------------------
CREATE TABLE agent_approval_queue (
  id            text PRIMARY KEY,
  session_id    text NOT NULL REFERENCES agent_session(id) ON DELETE CASCADE,
  user_id       text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  company_id    text,
  -- The draft document the agent created:
  doctype       text NOT NULL,
  document_id   text NOT NULL,
  document_name text NOT NULL,
  -- The prompt that triggered it (for the future accuracy audit):
  prompt        text NOT NULL,
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','expired')),
  resolved_by   text,
  resolved_at   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_aprq_user_pending_idx ON agent_approval_queue (user_id, created_at DESC) WHERE status = 'pending';
CREATE INDEX agent_aprq_session_idx      ON agent_approval_queue (session_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- agent_nudge: ambient nudges generated by the background nudge job. Each
-- row is shown as a dismissible bar in the FE. Dismissals persist via
-- dismissed_at — undismissed nudges reappear until the underlying data
-- changes.
-- ---------------------------------------------------------------------------
CREATE TABLE agent_nudge (
  id            text PRIMARY KEY,
  rule_id       text NOT NULL,                     -- matches an AGENT_CONTRACT.md nudge_rules entry
  user_id       text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  company_id    text,
  priority      text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high','urgent')),
  message       text NOT NULL,
  cta_label     text NOT NULL DEFAULT '',
  cta_prompt    text NOT NULL DEFAULT '',
  dismissed_at  timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_nudge_active_idx ON agent_nudge (user_id, priority, created_at DESC) WHERE dismissed_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_nudge;
DROP TABLE IF EXISTS agent_approval_queue;
DROP TABLE IF EXISTS agent_audit_log;
DROP FUNCTION IF EXISTS agent_audit_block_update();
DROP TABLE IF EXISTS agent_conversation;
DROP TABLE IF EXISTS agent_session;
-- +goose StatementEnd
