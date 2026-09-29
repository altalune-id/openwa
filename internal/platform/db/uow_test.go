package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/db"
)

func openMockPool(t *testing.T) (db.Pool, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db.Pool{W: sqlDB, R: sqlDB}, mock
}

func TestRunInTx_CommitOnSuccess(t *testing.T) {
	t.Parallel()
	pool, mock := openMockPool(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO things").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := db.RunInTx(t.Context(), pool, func(ctx context.Context) error {
		tx, ok := db.CurrentTx(ctx)
		if !ok {
			return errors.New("expected CurrentTx to return the enrolled tx")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO things (name) VALUES ('committed')")
		return err
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunInTx_RollbackOnError(t *testing.T) {
	t.Parallel()
	pool, mock := openMockPool(t)
	mock.ExpectBegin()
	mock.ExpectRollback()

	want := errors.New("boom")
	err := db.RunInTx(t.Context(), pool, func(context.Context) error { return want })
	require.ErrorIs(t, err, want)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunInTx_RollbackOnPanic(t *testing.T) {
	t.Parallel()
	pool, mock := openMockPool(t)
	mock.ExpectBegin()
	mock.ExpectRollback()

	require.PanicsWithValue(t, "kaboom", func() {
		_ = db.RunInTx(t.Context(), pool, func(context.Context) error { panic("kaboom") })
	})
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunInTx_RejectsNestedUnitOfWork(t *testing.T) {
	t.Parallel()
	pool, mock := openMockPool(t)
	mock.ExpectBegin()
	mock.ExpectRollback()

	outerErr := db.RunInTx(t.Context(), pool, func(outer context.Context) error {
		return db.RunInTx(outer, pool, func(context.Context) error {
			t.Fatalf("nested fn should not run")
			return nil
		})
	})
	require.ErrorIs(t, outerErr, db.ErrNestedUnitOfWork)
	require.NoError(t, mock.ExpectationsWereMet())
}
