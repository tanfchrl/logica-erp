-- +goose Up
-- ============================================================================
-- PRINT — letterheads + per-doctype print templates.
--
-- Both tables are workspace-wide with optional per-company override. The
-- service layer picks the most-specific match: company-scoped > all-companies,
-- with `is_default` as the tiebreaker.
-- ============================================================================

CREATE TABLE letterhead (
  id              text PRIMARY KEY,
  name            text NOT NULL,
  company_id      text REFERENCES company(id) ON DELETE CASCADE,
  is_default      boolean NOT NULL DEFAULT false,
  -- Data-URL or absolute URL. Render-time, the image tag is dropped into header_html.
  logo_url        text NOT NULL DEFAULT '',
  header_html     text NOT NULL DEFAULT '',
  footer_html     text NOT NULL DEFAULT '',
  paper_size      text NOT NULL DEFAULT 'A4',                            -- A4 | Letter | Legal
  margin_top      numeric(4,2) NOT NULL DEFAULT 0.50,                    -- inches
  margin_bottom   numeric(4,2) NOT NULL DEFAULT 0.50,
  margin_left     numeric(4,2) NOT NULL DEFAULT 0.50,
  margin_right    numeric(4,2) NOT NULL DEFAULT 0.50,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text REFERENCES users(id),
  updated_by      text REFERENCES users(id),
  UNIQUE (company_id, name)
);
CREATE INDEX letterhead_default_idx ON letterhead (company_id) WHERE is_default;

CREATE TABLE print_template (
  id              text PRIMARY KEY,
  doctype         text NOT NULL,
  name            text NOT NULL,
  company_id      text REFERENCES company(id) ON DELETE CASCADE,
  is_default      boolean NOT NULL DEFAULT false,
  letterhead_id   text REFERENCES letterhead(id) ON DELETE SET NULL,
  body_html       text NOT NULL DEFAULT '',
  is_enabled      boolean NOT NULL DEFAULT true,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_by      text REFERENCES users(id),
  updated_by      text REFERENCES users(id),
  UNIQUE (doctype, company_id, name)
);
CREATE INDEX print_template_lookup_idx ON print_template (doctype, company_id, is_default);

-- +goose Down
DROP TABLE IF EXISTS print_template;
DROP TABLE IF EXISTS letterhead;
