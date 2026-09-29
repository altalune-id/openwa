package onboard

import (
	"altalune.id/openwa/internal/platform/db"
)

// NewStore returns the Postgres Store implementation.
func NewStore(cfg db.DBConfig, pool db.Pool) Store {
	return newPostgresStore(pool, cfg.Schema, cfg.TablePrefix)
}
