-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- CRM admin-editable masters: Lead Source + Lost Reason
-- ============================================================================
-- v1 stored these as hardcoded enums (lead.source as text, opportunity.
-- lost_reason as text). Promote to per-company masters so admins can add
-- entries without redeploying.
--
-- We keep the lead.source / opportunity.lost_reason text columns for
-- backwards compatibility — the new tables provide a picklist; the text
-- value gets written from the picker's label. This lets old data render
-- unchanged.

CREATE TABLE lead_source (
  id          text PRIMARY KEY,
  company_id  text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  name        text NOT NULL,
  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (company_id, name)
);
CREATE INDEX lead_source_active_idx ON lead_source (company_id) WHERE is_active = true;

CREATE TABLE lost_reason (
  id          text PRIMARY KEY,
  company_id  text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  name        text NOT NULL,
  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (company_id, name)
);
CREATE INDEX lost_reason_active_idx ON lost_reason (company_id) WHERE is_active = true;

-- Seed the obvious defaults for every existing company so the first-run
-- UX isn't "create your first source/reason before you can use the form".
DO $$
DECLARE
  c record;
  src text;
  rsn text;
  sources text[]  := ARRAY['Website','Referral','Cold call','Event','Existing customer','WhatsApp','Other'];
  reasons text[]  := ARRAY['Price too high','No budget','Picked competitor','No decision','Out of scope','Timing','Other'];
BEGIN
  FOR c IN SELECT id FROM company LOOP
    FOREACH src IN ARRAY sources LOOP
      INSERT INTO lead_source (id, company_id, name)
      VALUES ('lsrc_' || encode(gen_random_bytes(10), 'hex'), c.id, src)
      ON CONFLICT (company_id, name) DO NOTHING;
    END LOOP;
    FOREACH rsn IN ARRAY reasons LOOP
      INSERT INTO lost_reason (id, company_id, name)
      VALUES ('lrsn_' || encode(gen_random_bytes(10), 'hex'), c.id, rsn)
      ON CONFLICT (company_id, name) DO NOTHING;
    END LOOP;
  END LOOP;
END$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS lost_reason;
DROP TABLE IF EXISTS lead_source;
-- +goose StatementEnd
