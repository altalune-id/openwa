-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

CREATE UNIQUE INDEX {{.TablePrefix}}webhook_endpoints_device_url_key
  ON {{.Schema}}.{{.TablePrefix}}webhook_endpoints (project_id, device_id, url)
  WHERE device_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}webhook_endpoints_device_url_key;

-- +goose StatementEnd
