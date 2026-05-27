-- +goose Up
-- ============================================================================
-- EMAIL — SMTP configuration + per-event message templates.
-- A single workspace-wide SMTP profile keeps the surface small; per-company
-- override can be added later by promoting smtp_config to a (company_id, ...)
-- composite key.
-- ============================================================================

CREATE TABLE smtp_config (
  id              text PRIMARY KEY DEFAULT 'smtp_singleton' CHECK (id = 'smtp_singleton'),
  host            text NOT NULL,
  port            int  NOT NULL DEFAULT 587,
  username        text NOT NULL DEFAULT '',
  -- Stored as opaque ciphertext when crypto.PasswordEncrypt is enabled; plain
  -- text otherwise (dev convenience). Never returned via the API.
  password        text NOT NULL DEFAULT '',
  use_tls         boolean NOT NULL DEFAULT true,
  from_email      text NOT NULL,
  from_name       text NOT NULL DEFAULT '',
  reply_to_email  text NOT NULL DEFAULT '',
  is_enabled      boolean NOT NULL DEFAULT false,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  updated_by      text REFERENCES users(id)
);

-- Per-event subject/body templates. event_key identifies the trigger; body is
-- a Go text/template string with placeholders that the dispatcher resolves.
CREATE TABLE email_template (
  id          text PRIMARY KEY,
  event_key   text NOT NULL,
  company_id  text REFERENCES company(id) ON DELETE CASCADE,
  subject     text NOT NULL,
  body_html   text NOT NULL,
  is_enabled  boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  created_by  text REFERENCES users(id),
  updated_by  text REFERENCES users(id),
  UNIQUE (event_key, company_id)
);
CREATE INDEX email_template_event_idx ON email_template (event_key);

-- Append-only sent-message log so the UI can show delivery history without
-- coupling to whatever transport actually delivered the message.
CREATE TABLE email_log (
  id            text PRIMARY KEY,
  to_addr       text NOT NULL,
  subject       text NOT NULL,
  event_key     text,
  status        text NOT NULL,                 -- 'sent' | 'failed'
  error_message text NOT NULL DEFAULT '',
  sent_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_log_sent_at_idx ON email_log (sent_at DESC);

-- +goose Down
DROP TABLE IF EXISTS email_log;
DROP TABLE IF EXISTS email_template;
DROP TABLE IF EXISTS smtp_config;
