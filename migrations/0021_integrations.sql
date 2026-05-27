-- +goose Up
-- ============================================================================
-- INTEGRATIONS + ADMIN — webhooks, API tokens, generic connector configs,
-- notification rules, payroll setting. Consolidated to one migration to keep
-- the schema graph compact.
-- ============================================================================

-- ---- Webhooks ----------------------------------------------------------------
CREATE TABLE webhook_subscription (
  id            text PRIMARY KEY,
  name          text NOT NULL,
  url           text NOT NULL,
  secret        text NOT NULL,                                  -- HMAC-SHA256 signing key
  events        text[] NOT NULL DEFAULT '{}',                   -- e.g. {invoice.submitted, payment.received}
  is_enabled    boolean NOT NULL DEFAULT true,
  retry_max     int NOT NULL DEFAULT 5,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  created_by    text REFERENCES users(id),
  updated_by    text REFERENCES users(id)
);
CREATE INDEX webhook_subscription_event_idx ON webhook_subscription USING GIN (events);

CREATE TABLE webhook_delivery (
  id              text PRIMARY KEY,
  subscription_id text NOT NULL REFERENCES webhook_subscription(id) ON DELETE CASCADE,
  event           text NOT NULL,
  payload         jsonb NOT NULL,
  attempt         int NOT NULL DEFAULT 1,
  status          text NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued','succeeded','failed')),
  response_code   int,
  response_body   text,
  error_message   text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  delivered_at    timestamptz
);
CREATE INDEX webhook_delivery_status_idx ON webhook_delivery (status, created_at DESC);
CREATE INDEX webhook_delivery_sub_idx    ON webhook_delivery (subscription_id, created_at DESC);

-- ---- API tokens --------------------------------------------------------------
-- Token plaintext is shown ONCE on create; only the hash is stored.
CREATE TABLE api_token (
  id            text PRIMARY KEY,
  name          text NOT NULL,
  token_hash    text NOT NULL UNIQUE,                           -- sha-256 of "lt_<random>"
  prefix        text NOT NULL,                                  -- first 8 chars of the plaintext, for the UI
  user_id       text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scopes        text[] NOT NULL DEFAULT '{}',                   -- "*" = full account access
  expires_at    timestamptz,
  last_used_at  timestamptz,
  revoked_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    text NOT NULL REFERENCES users(id)
);
CREATE INDEX api_token_user_idx ON api_token (user_id, revoked_at);

-- ---- Connector configs (payment gateways / bank feeds / marketplaces) -------
CREATE TABLE connector_config (
  id            text PRIMARY KEY,
  kind          text NOT NULL CHECK (kind IN ('payment_gateway','bank_feed','marketplace','shipping')),
  provider      text NOT NULL,                                  -- midtrans, xendit, bca, tokopedia, ...
  name          text NOT NULL,                                  -- display label
  company_id    text REFERENCES company(id) ON DELETE CASCADE,
  is_enabled    boolean NOT NULL DEFAULT false,
  test_mode     boolean NOT NULL DEFAULT true,
  -- Credentials stored opaque; surfaced only as has_credentials=true in API responses.
  credentials   jsonb NOT NULL DEFAULT '{}'::jsonb,
  -- Provider-specific non-secret config (account number, sender id, etc.)
  config        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  created_by    text REFERENCES users(id),
  updated_by    text REFERENCES users(id),
  UNIQUE (kind, provider, company_id, name)
);
CREATE INDEX connector_config_kind_idx ON connector_config (kind, is_enabled);

-- ---- Notification rules ------------------------------------------------------
CREATE TABLE notification_rule (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  event_key       text NOT NULL,                                -- e.g. 'invoice.overdue'
  company_id      text REFERENCES company(id) ON DELETE CASCADE,
  is_active       boolean NOT NULL DEFAULT true,
  -- Recipients: array of "user:<id>" or "role:<id>"
  recipients      text[] NOT NULL DEFAULT '{}',
  -- Channels: subset of {in_app, email, whatsapp}
  channels        text[] NOT NULL DEFAULT '{in_app}',
  -- Optional condition shape mirrors approval_rule.
  condition_field text,
  condition_op    text CHECK (condition_op IN ('=','<>','>','>=','<','<=') OR condition_op IS NULL),
  condition_value text,
  description     text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text REFERENCES users(id),
  updated_by      text REFERENCES users(id)
);
CREATE INDEX notification_rule_event_idx ON notification_rule (event_key, is_active);

-- ---- Payroll setting ---------------------------------------------------------
-- Singleton-per-company row holding PPh21 TER + BPJS rates for the current FY.
-- Indonesian DJP updates these annually; admins edit in the UI when rules change.
CREATE TABLE payroll_setting (
  id                       text PRIMARY KEY,
  company_id               text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  effective_from           date NOT NULL,
  -- BPJS rates (employer + employee %)
  bpjs_kesehatan_employer  numeric(5,4) NOT NULL DEFAULT 0.0400,
  bpjs_kesehatan_employee  numeric(5,4) NOT NULL DEFAULT 0.0100,
  bpjs_kesehatan_cap       numeric(18,4) NOT NULL DEFAULT 12000000,
  bpjs_jht_employer        numeric(5,4) NOT NULL DEFAULT 0.0370,
  bpjs_jht_employee        numeric(5,4) NOT NULL DEFAULT 0.0200,
  bpjs_jp_employer         numeric(5,4) NOT NULL DEFAULT 0.0200,
  bpjs_jp_employee         numeric(5,4) NOT NULL DEFAULT 0.0100,
  bpjs_jp_cap              numeric(18,4) NOT NULL DEFAULT 10042300,
  bpjs_jkk_employer        numeric(5,4) NOT NULL DEFAULT 0.0024,
  bpjs_jkm_employer        numeric(5,4) NOT NULL DEFAULT 0.0030,
  -- PPh21 TER brackets are stored as JSONB so DJP table updates don't need a migration.
  -- Shape: [{"category":"A","brackets":[{"max":5400000,"rate":0.0},...]}]
  pph21_ter                jsonb NOT NULL DEFAULT '[]'::jsonb,
  updated_at               timestamptz NOT NULL DEFAULT now(),
  updated_by               text REFERENCES users(id),
  UNIQUE (company_id, effective_from)
);

-- +goose Down
DROP TABLE IF EXISTS payroll_setting;
DROP TABLE IF EXISTS notification_rule;
DROP TABLE IF EXISTS connector_config;
DROP TABLE IF EXISTS api_token;
DROP TABLE IF EXISTS webhook_delivery;
DROP TABLE IF EXISTS webhook_subscription;
