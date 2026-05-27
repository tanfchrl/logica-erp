-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- OPPORTUNITY (CRM deal)
-- ============================================================================
-- The pipeline doctype. Each opportunity tracks one deal from prospecting
-- through Won / Lost. opportunity_from is a dynamic link to either a Lead
-- (early stage) or a Customer (existing client).
--
-- Stage is a string enum with seven values mirroring the Twenty default
-- pipeline. Probability defaults match the typical funnel:
--   prospecting   10%
--   qualification 25%
--   proposal      50%
--   negotiation   75%
--   closed_won   100%
--   closed_lost    0%
-- The service stamps the default on insert; users can override per row.

CREATE TABLE opportunity (
  id                     text PRIMARY KEY,
  name                   text NOT NULL,
  company_id             text NOT NULL REFERENCES company(id) ON DELETE RESTRICT,
  -- Title / subject the user uses to recognise the deal.
  subject                text NOT NULL,
  -- Dynamic-link parent: lead or customer.
  opportunity_from       text NOT NULL CHECK (opportunity_from IN ('lead','customer')),
  party_id               text NOT NULL,
  -- Free-text fallback for when a Lead/Customer record doesn't exist yet
  -- (shouldn't happen in normal flow but lets us load legacy data).
  party_name             text,
  -- Pipeline state.
  stage                  text NOT NULL DEFAULT 'prospecting'
                          CHECK (stage IN ('prospecting','qualification','proposal','negotiation','closed_won','closed_lost')),
  probability_pct        numeric(5,2) NOT NULL DEFAULT 10
                          CHECK (probability_pct >= 0 AND probability_pct <= 100),
  amount                 numeric(18,4) NOT NULL DEFAULT 0 CHECK (amount >= 0),
  currency               text NOT NULL REFERENCES currency(code) DEFAULT 'IDR',
  expected_close_date    date,
  -- Owner = the salesperson responsible. Defaults to the user who created.
  owner_user_id          text REFERENCES users(id),
  source                 text,
  -- Lost-only fields. Populated by Service.MarkLost.
  lost_reason            text,
  closed_at              timestamptz,
  -- Free-text remarks; richer threading lives on Note + Communication.
  remarks                text,
  custom_fields          jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  created_by             text NOT NULL REFERENCES users(id),
  updated_by             text NOT NULL REFERENCES users(id),
  UNIQUE (company_id, name)
);
-- Indexes drive the two hot reads: kanban (by stage per company) and
-- "my opportunities" (by owner).
CREATE INDEX opp_stage_idx ON opportunity (company_id, stage);
CREATE INDEX opp_owner_idx ON opportunity (company_id, owner_user_id);
CREATE INDEX opp_party_idx ON opportunity (opportunity_from, party_id);
CREATE INDEX opp_close_idx ON opportunity (expected_close_date)
  WHERE stage NOT IN ('closed_won','closed_lost');
CREATE TRIGGER opp_touch BEFORE UPDATE ON opportunity
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

INSERT INTO naming_series (id, doctype, company_id, pattern, is_default)
VALUES ('nms_default_opp', 'opportunity', NULL, 'OPP-.YYYY.-.####', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM naming_series WHERE id = 'nms_default_opp';
DROP TABLE IF EXISTS opportunity;
-- +goose StatementEnd
