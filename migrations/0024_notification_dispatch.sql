-- +goose Up
-- notification_dispatch — the log + retry queue for the notification dispatcher.
-- Mirrors webhook_delivery's shape so the dispatcher's reliability story
-- matches what users already see in the webhook surface.

CREATE TABLE notification_dispatch (
  id              text PRIMARY KEY,
  rule_id         text REFERENCES notification_rule(id) ON DELETE SET NULL,
  event_key       text NOT NULL,
  channel         text NOT NULL CHECK (channel IN ('in_app','email','whatsapp')),
  recipient_user  text REFERENCES users(id) ON DELETE SET NULL,
  recipient_addr  text NOT NULL DEFAULT '',
  payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
  attempt         int NOT NULL DEFAULT 0,
  max_attempts    int NOT NULL DEFAULT 5,
  status          text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','sent','failed','permanently_failed')),
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error      text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  delivered_at    timestamptz
);
CREATE INDEX notification_dispatch_due_idx
  ON notification_dispatch (next_attempt_at)
  WHERE status = 'pending';
CREATE INDEX notification_dispatch_recipient_idx
  ON notification_dispatch (recipient_user, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS notification_dispatch;
