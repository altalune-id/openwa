package blog

import (
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
)

// NewStore returns the Postgres Store implementation.
func NewStore(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) Store {
	return newPostgresStore(pool, pc, cfg.Schema, cfg.TablePrefix)
}
