-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

ALTER TABLE {{.Schema}}.{{.TablePrefix}}messages
  ADD COLUMN match_outcome TEXT NULL,
  ADD COLUMN matched_rule_id TEXT NULL;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}messages
  ADD CONSTRAINT {{.TablePrefix}}messages_match_outcome_check
  CHECK (match_outcome IN ('matched','ignored','none')) NOT VALID;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}messages
  DROP CONSTRAINT IF EXISTS {{.TablePrefix}}messages_match_outcome_check,
  DROP COLUMN IF EXISTS matched_rule_id,
  DROP COLUMN IF EXISTS match_outcome;

-- +goose StatementEnd
