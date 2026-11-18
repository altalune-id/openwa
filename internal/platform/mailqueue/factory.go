package mailqueue

import (
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/sealer"
)

// NewStore returns the Postgres Store, sealing every payload with sl.
func NewStore(cfg db.DBConfig, pool db.Pool, sl sealer.Sealer) Store {
	return newPostgresStore(pool, sl, cfg.Schema, cfg.TablePrefix)
}
