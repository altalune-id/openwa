-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}chats (
  id                    UUID PRIMARY KEY,
  public_id             TEXT NOT NULL,
  org_id                UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  device_id             UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}devices(id) ON DELETE CASCADE,
  jid                   TEXT NOT NULL,
  lid                   TEXT NOT NULL DEFAULT '',
  kind                  TEXT NOT NULL CHECK (kind IN ('dm','group')),
  name                  TEXT NOT NULL DEFAULT '',
  last_message_at       TIMESTAMPTZ,
  last_message_preview  TEXT NOT NULL DEFAULT '',
  unread_count          INTEGER NOT NULL DEFAULT 0 CHECK (unread_count >= 0),
  archived              BOOLEAN NOT NULL DEFAULT false,
  activity_at           TIMESTAMPTZ GENERATED ALWAYS AS (COALESCE(last_message_at, created_at)) STORED,
  version               INTEGER NOT NULL DEFAULT 1,
  created_at            TIMESTAMPTZ NOT NULL,
  updated_at            TIMESTAMPTZ NOT NULL,
  CONSTRAINT {{.TablePrefix}}chats_device_jid_key UNIQUE (device_id, jid),
  CONSTRAINT {{.TablePrefix}}chats_public_id_key UNIQUE (public_id)
);

CREATE UNIQUE INDEX {{.TablePrefix}}chats_device_lid_key
  ON {{.Schema}}.{{.TablePrefix}}chats (device_id, lid) WHERE lid <> '';

CREATE INDEX {{.TablePrefix}}chats_project_device_activity_idx
  ON {{.Schema}}.{{.TablePrefix}}chats (project_id, device_id, activity_at DESC, id DESC);

-- NOTE: text_pattern_ops, so the name-prefix LIKE uses the index under a non-C collation.
CREATE INDEX {{.TablePrefix}}chats_project_name_idx
  ON {{.Schema}}.{{.TablePrefix}}chats (project_id, lower(name) text_pattern_ops);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}contacts (
  id                    UUID PRIMARY KEY,
  org_id                UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  device_id             UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}devices(id) ON DELETE CASCADE,
  jid                   TEXT NOT NULL,
  lid                   TEXT NOT NULL DEFAULT '',
  phone                 TEXT NOT NULL DEFAULT '',
  name                  TEXT NOT NULL DEFAULT '',
  push_name             TEXT NOT NULL DEFAULT '',
  business_name         TEXT NOT NULL DEFAULT '',
  updated_at            TIMESTAMPTZ NOT NULL,
  CONSTRAINT {{.TablePrefix}}contacts_device_jid_key UNIQUE (device_id, jid)
);

CREATE INDEX {{.TablePrefix}}contacts_project_device_updated_idx
  ON {{.Schema}}.{{.TablePrefix}}contacts (project_id, device_id, updated_at DESC, id DESC);

CREATE INDEX {{.TablePrefix}}contacts_project_device_name_idx
  ON {{.Schema}}.{{.TablePrefix}}contacts (project_id, device_id, lower(name) text_pattern_ops);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}messages (
  id                    UUID PRIMARY KEY,
  public_id             TEXT NOT NULL,
  org_id                UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  device_id             UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}devices(id) ON DELETE CASCADE,
  chat_id               UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}chats(id) ON DELETE CASCADE,
  direction             TEXT NOT NULL CHECK (direction IN ('in','out')),
  wa_message_id         TEXT NOT NULL DEFAULT '',
  sender_jid            TEXT NOT NULL DEFAULT '',
  sender_lid            TEXT NOT NULL DEFAULT '',
  sender_phone          TEXT NOT NULL DEFAULT '',
  sender_name           TEXT NOT NULL DEFAULT '',
  from_me               BOOLEAN NOT NULL DEFAULT false,
  type                  TEXT NOT NULL,
  body                  TEXT NOT NULL DEFAULT '',
  media                 JSONB,
  location              JSONB,
  quoted_wa_message_id  TEXT NOT NULL DEFAULT '',
  mentions              JSONB NOT NULL DEFAULT '[]'::jsonb,
  target_wa_message_id  TEXT NOT NULL DEFAULT '',
  status                TEXT NOT NULL CHECK (status IN ('queued','sending','sent','delivered','read','played','failed','received')),
  error                 TEXT NOT NULL DEFAULT '',
  attempts              INTEGER NOT NULL DEFAULT 0,
  wa_timestamp          TIMESTAMPTZ NOT NULL,
  sent_at               TIMESTAMPTZ,
  delivered_at          TIMESTAMPTZ,
  read_at               TIMESTAMPTZ,
  raw                   BYTEA,
  version               INTEGER NOT NULL DEFAULT 1,
  created_at            TIMESTAMPTZ NOT NULL,
  updated_at            TIMESTAMPTZ NOT NULL,
  CONSTRAINT {{.TablePrefix}}messages_public_id_key UNIQUE (public_id)
);

-- NOTE: device-wide rather than per chat and sender, because receipts arrive keyed by message id alone.
CREATE UNIQUE INDEX {{.TablePrefix}}messages_device_wa_id_key
  ON {{.Schema}}.{{.TablePrefix}}messages (device_id, wa_message_id) WHERE wa_message_id <> '';

CREATE INDEX {{.TablePrefix}}messages_chat_ts_idx
  ON {{.Schema}}.{{.TablePrefix}}messages (chat_id, wa_timestamp DESC, id DESC);

CREATE INDEX {{.TablePrefix}}messages_chat_id_idx
  ON {{.Schema}}.{{.TablePrefix}}messages (chat_id, id);

CREATE INDEX {{.TablePrefix}}messages_device_ts_idx
  ON {{.Schema}}.{{.TablePrefix}}messages (device_id, wa_timestamp DESC, id DESC);

CREATE INDEX {{.TablePrefix}}messages_outbound_queue_idx
  ON {{.Schema}}.{{.TablePrefix}}messages (device_id, created_at, id) WHERE direction = 'out' AND status IN ('queued','sending');

CREATE INDEX {{.TablePrefix}}messages_project_ts_idx
  ON {{.Schema}}.{{.TablePrefix}}messages (project_id, wa_timestamp, id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}message_retention (
  project_id            UUID PRIMARY KEY REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  org_id                UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  days                  INTEGER NOT NULL CHECK (days BETWEEN 1 AND 365),
  updated_at            TIMESTAMPTZ NOT NULL
);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}chats ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}chats FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}chats_tenant
  ON {{.Schema}}.{{.TablePrefix}}chats
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}contacts FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}contacts_tenant
  ON {{.Schema}}.{{.TablePrefix}}contacts
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}messages FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}messages_tenant
  ON {{.Schema}}.{{.TablePrefix}}messages
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}message_retention ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}message_retention FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}message_retention_tenant
  ON {{.Schema}}.{{.TablePrefix}}message_retention
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}message_retention;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}messages;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}contacts;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}chats;

-- +goose StatementEnd
