package org

import (
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
)

// NewStore selects the Store implementation for the configured driver.
func NewStore(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) Store {
	return newPostgresStore(pool, pc, cfg.Schema, cfg.TablePrefix)
}
