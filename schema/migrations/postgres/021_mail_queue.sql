-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}mail_queue (
  id              UUID PRIMARY KEY,
  payload         BYTEA,
  status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed')),
  attempt         INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  next_attempt_at TIMESTAMPTZ NOT NULL,
  last_error      TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL,
  settled_at      TIMESTAMPTZ,
  -- SECURITY: a settled row must not keep the message, which holds addresses and live links.
  CONSTRAINT {{.TablePrefix}}mail_queue_settled_erased CHECK (status = 'pending' OR payload IS NULL),
  CONSTRAINT {{.TablePrefix}}mail_queue_pending_payload CHECK (status <> 'pending' OR payload IS NOT NULL)
);

CREATE INDEX {{.TablePrefix}}mail_queue_due_idx
  ON {{.Schema}}.{{.TablePrefix}}mail_queue (next_attempt_at)
  WHERE status = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}mail_queue;

-- +goose StatementEnd
