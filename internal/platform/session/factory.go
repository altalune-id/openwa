package session

import (
	"context"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/sealer"
)

// NewStore returns the Postgres session store.
func NewStore(
	cfg db.DBConfig,
	pool db.Pool,
	sl sealer.Sealer,
	unexpected apperror.UnexpectedFunc,
) Store {
	if unexpected == nil {
		unexpected = discardUnexpected
	}
	// NOTE: pool.W for reads too — pool.R may lag, and the redirect after a login would miss the row.
	return &pgStore{
		db:         pool.W,
		table:      pgent.NewSessions(cfg.Schema, cfg.TablePrefix),
		sealer:     sl,
		unexpected: unexpected,
	}
}

func discardUnexpected(context.Context, string, error, ...any) *apperror.AppError { return nil }
