package tenant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pdb "altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/pgtest"
)

func TestTenantRunInTx_AppliesSetConfig(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	pc := tenant.NewPgConn(sqlDB)
	orgID := uuid.New()
	tc := tenant.Context{OrgID: orgID, UserID: uuid.New()}

	var got string
	err := tenant.RunInTx(t.Context(), pc, tc, func(ctx context.Context) error {
		tx, ok := pdb.CurrentTx(ctx)
		require.True(t, ok, "expected tx to be enrolled in ctx")
		return tx.QueryRowContext(ctx, "SELECT current_setting('app.current_org_id', true)").Scan(&got)
	})
	require.NoError(t, err)
	require.Equal(t, orgID.String(), got)
}

func TestNewUnitOfWork_Postgres_AppliesSetConfig(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	pc := tenant.NewPgConn(sqlDB)
	uow := tenant.NewUnitOfWork(pc)

	orgID := uuid.New()
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: uuid.New()})

	var got string
	err := uow(ctx, func(ctx context.Context) error {
		tx, ok := pdb.CurrentTx(ctx)
		require.True(t, ok, "expected tx to be enrolled in ctx")
		return tx.QueryRowContext(ctx, "SELECT current_setting('app.current_org_id', true)").Scan(&got)
	})
	require.NoError(t, err)
	require.Equal(t, orgID.String(), got)
}

func TestNewUnitOfWork_Postgres_NoTenantReturnsTenantFromError(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	pc := tenant.NewPgConn(sqlDB)
	uow := tenant.NewUnitOfWork(pc)

	err := uow(t.Context(), func(context.Context) error {
		t.Fatal("fn must not run without tenant scope")
		return nil
	})
	require.True(t, tenant.IsMissingError(err))
}

func TestNewUnitOfWork_Postgres_RejectsNestedUnitOfWork(t *testing.T) {
	h := pgtest.New(t)
	uow := tenant.NewUnitOfWork(tenant.NewPgConn(h.OpenDB(t)))
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), UserID: uuid.New()})

	err := uow(ctx, func(ctx context.Context) error {
		return uow(ctx, func(context.Context) error {
			t.Fatal("nested fn should not run")
			return nil
		})
	})
	require.ErrorIs(t, err, pdb.ErrNestedUnitOfWork)
}
