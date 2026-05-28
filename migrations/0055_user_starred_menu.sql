-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- USER_STARRED_MENU (per-user sidebar favourites)
-- ============================================================================
-- Backs the "Starred" section in the sidebar. Users star/unstar list pages
-- from the toolbar on each doctype list; this table is the source of truth.
--
-- One row per (user, path). label is captured at star-time so the sidebar
-- can render it without round-tripping through the doctype registry.
-- position lets the user reorder the section later (UI not yet wired —
-- defaults to starred_at).

CREATE TABLE user_starred_menu (
  user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  path        text NOT NULL,
  label       text NOT NULL,
  position    int  NOT NULL DEFAULT 0,
  starred_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, path)
);
CREATE INDEX user_starred_menu_user_idx ON user_starred_menu (user_id, position, starred_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_starred_menu;
-- +goose StatementEnd
