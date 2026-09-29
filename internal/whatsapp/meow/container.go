// Package meow is the whatsmeow engine adapter; it is the only package that imports go.mau.fi.
package meow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"

	"altalune.id/openwa/version"
)

const dialect = "pgx"

// Container wraps the whatsmeow store container so callers outside this package hold no go.mau.fi type.
type Container struct {
	inner *sqlstore.Container
}

// NotUpgradedError reports that whatsmeow's schema is absent from the runtime database.
type NotUpgradedError struct{}

func (*NotUpgradedError) Error() string {
	return "whatsmeow: schema not present — run `openwa migrate` (or enable db.autoMigrate) before serving"
}

// IsNotUpgradedError reports whether err is a NotUpgradedError.
func IsNotUpgradedError(err error) bool {
	_, ok := errors.AsType[*NotUpgradedError](err)
	return ok
}

// Upgrade creates or upgrades whatsmeow's tables on the migrator connection; it is the only DDL path.
func Upgrade(ctx context.Context, migDB *sql.DB, log *slog.Logger) error {
	c := sqlstore.NewWithDB(migDB, dialect, newLogger(log, "wa-migrate"))
	if err := c.Upgrade(ctx); err != nil {
		return fmt.Errorf("whatsmeow upgrade: %w", err)
	}
	return nil
}

// AssertUpgraded checks that whatsmeow's version table exists on the current search_path.
func AssertUpgraded(ctx context.Context, db *sql.DB) error {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT to_regclass('whatsmeow_version') IS NOT NULL`).Scan(&ok)
	if err != nil {
		return fmt.Errorf("whatsmeow: assert upgraded: %w", err)
	}
	if !ok {
		return &NotUpgradedError{}
	}
	return nil
}

// NewContainer opens the runtime container on the application pool and brands the linked-device name; clientName is validated by config (`validate:"required"`).
func NewContainer(db *sql.DB, log *slog.Logger, clientName string) (*Container, error) {
	store.SetOSInfo(clientName, version.Triple())
	return &Container{inner: sqlstore.NewWithDB(db, dialect, newLogger(log, "wa-store"))}, nil
}

// Close releases the container; the *sql.DB is owned by the caller and stays open.
func (c *Container) Close() error { return nil }
