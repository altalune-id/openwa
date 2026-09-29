package tenant

import (
	"context"
)

// UnitOfWork runs fn in one transaction; every store called with fn's ctx joins it.
type UnitOfWork func(ctx context.Context, fn func(ctx context.Context) error) error

// NewUnitOfWork returns the UnitOfWork that runs fn in a tenant-scoped Postgres transaction.
func NewUnitOfWork(pc *PgConn) UnitOfWork {
	return func(ctx context.Context, fn func(ctx context.Context) error) error {
		tc, err := From(ctx)
		if err != nil {
			return err
		}
		return RunInTx(ctx, pc, tc, fn)
	}
}
