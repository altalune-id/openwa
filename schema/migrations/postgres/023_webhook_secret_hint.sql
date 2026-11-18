-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints
  ADD COLUMN secret_hint TEXT NOT NULL DEFAULT '',
  ADD COLUMN secondary_secret_hint TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints
  DROP COLUMN IF EXISTS secondary_secret_hint,
  DROP COLUMN IF EXISTS secret_hint;

-- +goose StatementEnd
