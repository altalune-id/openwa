-- +goose NO TRANSACTION
-- +goose Up

ALTER TABLE {{.Schema}}.{{.TablePrefix}}messages
  VALIDATE CONSTRAINT {{.TablePrefix}}messages_match_outcome_check;

DROP INDEX CONCURRENTLY IF EXISTS {{.Schema}}.{{.TablePrefix}}messages_rule_hits_idx;

CREATE INDEX CONCURRENTLY {{.TablePrefix}}messages_rule_hits_idx
  ON {{.Schema}}.{{.TablePrefix}}messages (project_id, device_id, matched_rule_id, wa_timestamp DESC)
  WHERE matched_rule_id IS NOT NULL;

-- +goose Down

DROP INDEX CONCURRENTLY IF EXISTS {{.Schema}}.{{.TablePrefix}}messages_rule_hits_idx;
