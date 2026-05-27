-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- FINANCE BOOK + per-asset book depreciation
-- ============================================================================
-- Indonesian tax law (PMK-96/2018) requires depreciation rates that differ
-- from accounting (PSAK 16) rates for most asset classes. Finance Book lets
-- one asset carry both schedules at once.
--
-- Model:
--   - finance_book master per company. Exactly one row should be is_primary.
--     The primary book is the one PostDepreciation actually posts to GL.
--   - asset_finance_book is the per-(asset, book) config. The PRIMARY entry
--     is created implicitly from the asset's own columns; non-primary books
--     add entries here when the asset is submitted.
--   - asset_finance_book_schedule mirrors depreciation_schedule but per book.
--     v1 generates these read-only — tax-book entries don't post to GL.

CREATE TABLE finance_book (
  id          text PRIMARY KEY,
  company_id  text NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  name        text NOT NULL,
  is_primary  boolean NOT NULL DEFAULT false,
  is_deleted  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (company_id, name)
);
CREATE INDEX finance_book_primary_idx
  ON finance_book (company_id) WHERE is_primary = true AND is_deleted = false;

CREATE TABLE asset_finance_book (
  id                       text PRIMARY KEY,
  asset_id                 text NOT NULL REFERENCES asset(id) ON DELETE CASCADE,
  finance_book_id          text NOT NULL REFERENCES finance_book(id) ON DELETE CASCADE,
  depreciation_method      text NOT NULL DEFAULT 'straight_line',
  depreciation_rate_pct    numeric(10,4),
  useful_life_months       integer NOT NULL CHECK (useful_life_months > 0),
  pro_rata_basis           boolean NOT NULL DEFAULT true,
  expected_value_after_useful_life numeric(18,4) NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (asset_id, finance_book_id)
);

CREATE TABLE asset_finance_book_schedule (
  id                       text PRIMARY KEY,
  asset_finance_book_id    text NOT NULL REFERENCES asset_finance_book(id) ON DELETE CASCADE,
  row_index                integer NOT NULL,
  schedule_date            date    NOT NULL,
  depreciation_amount      numeric(18,4) NOT NULL,
  accumulated_after        numeric(18,4) NOT NULL,
  UNIQUE (asset_finance_book_id, row_index)
);
CREATE INDEX afbs_book_idx ON asset_finance_book_schedule (asset_finance_book_id, schedule_date);

-- Seed a single "Accounting Book" per existing company that's flagged
-- primary. After this runs, every company has at least one book, and the
-- primary book ID matches the implicit "what we always did" semantics.
INSERT INTO finance_book (id, company_id, name, is_primary)
SELECT 'fb_'||id, id, 'Accounting Book', true FROM company
ON CONFLICT (company_id, name) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS asset_finance_book_schedule;
DROP TABLE IF EXISTS asset_finance_book;
DROP TABLE IF EXISTS finance_book;
-- +goose StatementEnd
