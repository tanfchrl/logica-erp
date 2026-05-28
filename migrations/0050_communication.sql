-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- COMMUNICATION
-- ============================================================================
-- User-logged calls / meetings / emails / WA messages threaded against any
-- record via the (parent_doctype, parent_id) dynamic link.
--
-- v1: manual create only. SMTP outbox + WA Business API auto-population
-- land later; column shape already accommodates the integration.

CREATE TABLE communication (
  id              text PRIMARY KEY,
  company_id      text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  parent_doctype  text NOT NULL,
  parent_id       text NOT NULL,
  -- email | sms | phone | meeting | whatsapp — same set Twenty uses,
  -- plus whatsapp because Indonesian SMEs live on WA.
  kind            text NOT NULL CHECK (kind IN ('email','sms','phone','meeting','whatsapp')),
  -- in | out — who initiated. For meetings this is the perspective:
  -- "in" means the customer called us, "out" means we reached out.
  direction       text NOT NULL DEFAULT 'out' CHECK (direction IN ('in','out')),
  subject         text NOT NULL,
  body            text,
  -- Free-text contact identity — name, email, phone number, whatever
  -- the channel uses. Doesn't have to be an FK to contact (a call from
  -- an unknown number still needs to be logged).
  with_contact    text,
  -- sent_at is the actual touch time (vs created_at = "when we logged it").
  sent_at         timestamptz NOT NULL DEFAULT now(),
  -- Where did this row come from? "manual" = user typed; "smtp" =
  -- backend hooked the outbox; etc.
  source          text NOT NULL DEFAULT 'manual',
  is_deleted      boolean NOT NULL DEFAULT false,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text NOT NULL REFERENCES users(id),
  updated_by      text NOT NULL REFERENCES users(id)
);
CREATE INDEX comm_parent_idx ON communication (parent_doctype, parent_id, sent_at DESC)
  WHERE is_deleted = false;
CREATE INDEX comm_company_idx ON communication (company_id, sent_at DESC)
  WHERE is_deleted = false;
CREATE TRIGGER comm_touch BEFORE UPDATE ON communication
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS communication;
-- +goose StatementEnd
