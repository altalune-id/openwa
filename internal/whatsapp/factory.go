package whatsapp

import (
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
)

// NewStore builds the Postgres session Store.
func NewStore(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) Store {
	return newPostgresStore(pool, pc, cfg.Schema, cfg.TablePrefix)
}

// NewLeaseStore builds the Postgres LeaseStore; it runs on pool.W without tenant scope and never joins a tenant transaction.
func NewLeaseStore(cfg db.DBConfig, pool db.Pool) LeaseStore {
	return newPgLeaseStore(pool, cfg.Schema, cfg.TablePrefix)
}
