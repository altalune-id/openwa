-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}users ADD COLUMN theme TEXT NOT NULL DEFAULT '';
ALTER TABLE {{.Schema}}.{{.TablePrefix}}users ADD COLUMN color_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE {{.Schema}}.{{.TablePrefix}}users ADD COLUMN tips_seen TEXT NOT NULL DEFAULT '[]';
ALTER TABLE {{.Schema}}.{{.TablePrefix}}users ADD COLUMN last_project_id UUID NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE SET NULL;
CREATE INDEX {{.TablePrefix}}users_last_project_idx
  ON {{.Schema}}.{{.TablePrefix}}users (last_project_id) WHERE last_project_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}users_last_project_idx;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}users DROP COLUMN IF EXISTS last_project_id;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}users DROP COLUMN IF EXISTS tips_seen;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}users DROP COLUMN IF EXISTS color_mode;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}users DROP COLUMN IF EXISTS theme;

-- +goose StatementEnd
