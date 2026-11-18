-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

ALTER TABLE {{.Schema}}.{{.TablePrefix}}devices
  ADD CONSTRAINT {{.TablePrefix}}devices_project_id_key UNIQUE (project_id, id);

ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints
  ADD COLUMN device_id UUID NULL;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints
  ADD CONSTRAINT {{.TablePrefix}}webhook_endpoints_device_fk
  FOREIGN KEY (project_id, device_id)
  REFERENCES {{.Schema}}.{{.TablePrefix}}devices (project_id, id) ON DELETE CASCADE;

CREATE INDEX {{.TablePrefix}}webhook_endpoints_device_idx
  ON {{.Schema}}.{{.TablePrefix}}webhook_endpoints (project_id, device_id)
  WHERE device_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}webhook_endpoints_device_idx;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints
  DROP CONSTRAINT IF EXISTS {{.TablePrefix}}webhook_endpoints_device_fk,
  DROP COLUMN IF EXISTS device_id;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}devices
  DROP CONSTRAINT IF EXISTS {{.TablePrefix}}devices_project_id_key;

-- +goose StatementEnd
