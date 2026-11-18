-- +goose NO TRANSACTION
-- +goose Up

DROP INDEX CONCURRENTLY IF EXISTS {{.Schema}}.{{.TablePrefix}}webhook_deliveries_delivered_idx;

CREATE INDEX CONCURRENTLY {{.TablePrefix}}webhook_deliveries_delivered_idx
  ON {{.Schema}}.{{.TablePrefix}}webhook_deliveries (org_id, project_id, event_type)
  WHERE status_code BETWEEN 200 AND 299 AND error = '';

-- +goose Down

DROP INDEX CONCURRENTLY IF EXISTS {{.Schema}}.{{.TablePrefix}}webhook_deliveries_delivered_idx;
