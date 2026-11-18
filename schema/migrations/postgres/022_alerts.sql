-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

ALTER TABLE {{.Schema}}.{{.TablePrefix}}whatsapp_sessions
  ADD COLUMN disconnected_at        TIMESTAMPTZ,
  ADD COLUMN disconnect_notified_at TIMESTAMPTZ;

CREATE TABLE {{.Schema}}.{{.TablePrefix}}alert_settings (
  project_id         UUID PRIMARY KEY REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  org_id             UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  disconnect_enabled BOOLEAN NOT NULL DEFAULT true,
  recipients         TEXT NOT NULL DEFAULT '[]',
  updated_at         TIMESTAMPTZ NOT NULL
);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}alert_optouts (
  project_id UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  org_id     UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  email      TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (project_id, email)
);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}alert_send_counts (
  project_id   UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  org_id       UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL CHECK (kind IN ('test','email')),
  window_start TIMESTAMPTZ NOT NULL,
  sent         INTEGER NOT NULL CHECK (sent >= 0),
  PRIMARY KEY (project_id, kind, window_start)
);

CREATE INDEX {{.TablePrefix}}alert_send_counts_org_window_idx
  ON {{.Schema}}.{{.TablePrefix}}alert_send_counts (org_id, window_start);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}alert_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}alert_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}alert_settings_tenant
  ON {{.Schema}}.{{.TablePrefix}}alert_settings
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}alert_optouts ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}alert_optouts FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}alert_optouts_tenant
  ON {{.Schema}}.{{.TablePrefix}}alert_optouts
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}alert_send_counts ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}alert_send_counts FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}alert_send_counts_tenant
  ON {{.Schema}}.{{.TablePrefix}}alert_send_counts
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}alert_send_counts;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}alert_optouts;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}alert_settings;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}whatsapp_sessions
  DROP COLUMN IF EXISTS disconnect_notified_at,
  DROP COLUMN IF EXISTS disconnected_at;

-- +goose StatementEnd
