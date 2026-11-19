package alert

import (
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
)

// NewStore returns the Postgres Store implementation.
func NewStore(cfg db.DBConfig, pc *tenant.PgConn) Store {
	return newPostgresStore(pc, cfg.Schema, cfg.TablePrefix)
}
