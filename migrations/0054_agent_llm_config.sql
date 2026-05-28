-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- AGENT_LLM_CONFIG (per-company BYOM)
-- ============================================================================
-- Lets admins switch the agent's LLM provider / endpoint / model without a
-- redeploy. One row per company; service guarantees that via
-- UNIQUE(company_id) + upsert semantics.
--
-- API key is AES-256-GCM ciphertext using AGENT_CONFIG_ENCRYPTION_KEY from
-- env. Nonce stored alongside; last4 stored separately for the UI's
-- "•••• 1a2b" display so we don't have to round-trip the secret.
--
-- When a company has no row, the agent falls back to the AGENT_LLM_*
-- env vars on the agent container (existing behaviour).

CREATE TABLE agent_llm_config (
  id                    text PRIMARY KEY,
  company_id            text NOT NULL UNIQUE REFERENCES company(id) ON DELETE CASCADE,
  -- Provider hint helps the UI pick presets (e.g. show "us-east" region for
  -- Azure). Free text; doesn't constrain anything server-side.
  provider              text NOT NULL DEFAULT 'openai',
  base_url              text NOT NULL,
  model                 text NOT NULL,
  -- AES-GCM blob + nonce; nullable so a company can clear the key without
  -- removing the whole config row. Display hint = last 4 chars of plaintext.
  api_key_ciphertext    bytea,
  api_key_nonce         bytea,
  api_key_last4         text,
  is_active             boolean NOT NULL DEFAULT true,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  updated_by            text REFERENCES users(id)
);
CREATE TRIGGER agent_llm_config_touch BEFORE UPDATE ON agent_llm_config
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_llm_config;
-- +goose StatementEnd
