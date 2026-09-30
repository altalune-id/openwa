-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}devices (
  id                  UUID PRIMARY KEY,
  public_id           TEXT NOT NULL,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  rules               JSONB NOT NULL DEFAULT '{}'::jsonb,
  version             INTEGER NOT NULL DEFAULT 1,
  created_at          TIMESTAMPTZ NOT NULL,
  updated_at          TIMESTAMPTZ NOT NULL,
  CONSTRAINT {{.TablePrefix}}devices_public_id_key UNIQUE (public_id)
);

CREATE UNIQUE INDEX {{.TablePrefix}}devices_project_lower_name_idx
  ON {{.Schema}}.{{.TablePrefix}}devices (project_id, lower(name));

CREATE INDEX {{.TablePrefix}}devices_org_project_created_idx
  ON {{.Schema}}.{{.TablePrefix}}devices (org_id, project_id, created_at DESC);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}whatsapp_sessions (
  device_id           UUID PRIMARY KEY REFERENCES {{.Schema}}.{{.TablePrefix}}devices(id) ON DELETE CASCADE,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  engine              TEXT NOT NULL,
  jid                 TEXT NOT NULL DEFAULT '',
  lid                 TEXT NOT NULL DEFAULT '',
  phone               TEXT NOT NULL DEFAULT '',
  push_name           TEXT NOT NULL DEFAULT '',
  platform            TEXT NOT NULL DEFAULT '',
  state               TEXT NOT NULL CHECK (state IN ('unlinked','linking','connected','disconnected','logged_out')),
  reason              TEXT NOT NULL DEFAULT '',
  last_connected_at   TIMESTAMPTZ,
  last_seen_at        TIMESTAMPTZ,
  last_error          TEXT NOT NULL DEFAULT '',
  version             INTEGER NOT NULL DEFAULT 1,
  updated_at          TIMESTAMPTZ NOT NULL
);

CREATE INDEX {{.TablePrefix}}whatsapp_sessions_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}whatsapp_sessions (org_id, project_id);

-- NOTE: no RLS on purpose — the runtime claims leases across every tenant before any tenant scope exists.
CREATE TABLE {{.Schema}}.{{.TablePrefix}}whatsapp_leases (
  device_id           UUID PRIMARY KEY REFERENCES {{.Schema}}.{{.TablePrefix}}devices(id) ON DELETE CASCADE,
  org_id              UUID NOT NULL,
  project_id          UUID NOT NULL,
  jid                 TEXT NOT NULL,
  owner               TEXT,
  expires_at          TIMESTAMPTZ,
  updated_at          TIMESTAMPTZ NOT NULL
);

CREATE INDEX {{.TablePrefix}}whatsapp_leases_owner_idx
  ON {{.Schema}}.{{.TablePrefix}}whatsapp_leases (owner);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}devices FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}devices_tenant
  ON {{.Schema}}.{{.TablePrefix}}devices
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}whatsapp_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}whatsapp_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}whatsapp_sessions_tenant
  ON {{.Schema}}.{{.TablePrefix}}whatsapp_sessions
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}whatsapp_leases;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}whatsapp_sessions;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}devices;

-- +goose StatementEnd
