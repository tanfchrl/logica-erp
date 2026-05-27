-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- WORKFLOW ENGINE
-- ============================================================================
CREATE TABLE workflow (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  doctype     text NOT NULL UNIQUE,                 -- one active workflow per doctype
  state_field text NOT NULL DEFAULT 'status',
  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workflow_state (
  id          text PRIMARY KEY,
  workflow_id text NOT NULL REFERENCES workflow(id) ON DELETE CASCADE,
  name        text NOT NULL,
  doc_status  smallint NOT NULL DEFAULT 0 CHECK (doc_status IN (0,1,2)),
  is_initial  boolean NOT NULL DEFAULT false,
  is_terminal boolean NOT NULL DEFAULT false,
  UNIQUE (workflow_id, name)
);

CREATE TABLE workflow_transition (
  id              text PRIMARY KEY,
  workflow_id     text NOT NULL REFERENCES workflow(id) ON DELETE CASCADE,
  from_state      text NOT NULL,
  to_state        text NOT NULL,
  action          text NOT NULL,             -- e.g. 'submit_for_approval', 'approve', 'reject'
  allowed_role_id text REFERENCES role(id),  -- NULL = anyone with write
  UNIQUE (workflow_id, from_state, action)
);

-- ============================================================================
-- DOCUMENT COMMENTS + ATTACHMENTS
-- ============================================================================
CREATE TABLE document_comment (
  id          text PRIMARY KEY,
  doctype     text NOT NULL,
  document_id text NOT NULL,
  parent_comment_id text REFERENCES document_comment(id),
  body        text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  text NOT NULL REFERENCES users(id)
);
CREATE INDEX doc_comment_idx ON document_comment (doctype, document_id, created_at DESC);

CREATE TABLE document_attachment (
  id          text PRIMARY KEY,
  doctype     text NOT NULL,
  document_id text NOT NULL,
  file_name   text NOT NULL,
  file_size   bigint NOT NULL,
  content_type text NOT NULL,
  storage_key text NOT NULL,            -- local path or S3 key
  storage_driver text NOT NULL DEFAULT 'local',
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  text NOT NULL REFERENCES users(id)
);
CREATE INDEX doc_attach_idx ON document_attachment (doctype, document_id);

-- ============================================================================
-- NOTIFICATIONS (in-app)
-- ============================================================================
CREATE TABLE notification (
  id          text PRIMARY KEY,
  user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  subject     text NOT NULL,
  body        text,
  link_doctype text,
  link_document_id text,
  is_read     boolean NOT NULL DEFAULT false,
  read_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notif_user_idx ON notification (user_id, is_read, created_at DESC);

-- ============================================================================
-- GLOBAL SEARCH INDEX (lightweight materialised projection)
-- Populated by application code on doc submit/update; queried via /search.
-- ============================================================================
CREATE TABLE search_index (
  doctype     text NOT NULL,
  document_id text NOT NULL,
  name        text NOT NULL,
  title       text NOT NULL,
  body        text,
  company_id  text,
  ts          tsvector,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (doctype, document_id)
);
CREATE INDEX search_ts_idx ON search_index USING GIN (ts);
CREATE INDEX search_co_idx ON search_index (company_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS search_index;
DROP TABLE IF EXISTS notification;
DROP TABLE IF EXISTS document_attachment;
DROP TABLE IF EXISTS document_comment;
DROP TABLE IF EXISTS workflow_transition;
DROP TABLE IF EXISTS workflow_state;
DROP TABLE IF EXISTS workflow;
-- +goose StatementEnd
