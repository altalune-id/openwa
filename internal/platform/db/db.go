package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open opens the *sql.DB, pings it with bounded retry, and applies pool tuning.
func Open(ctx context.Context, cfg DBConfig, log *slog.Logger) (*sql.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	db, err := openPostgres(cfg)
	if err != nil {
		return nil, err
	}

	pingErr := retry(withRetryLogger(ctx, log), cfg.ConnectTimeout, cfg.ConnectBackoff,
		func(attemptCtx context.Context) error { return db.PingContext(attemptCtx) })
	if pingErr != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db: ping postgres: %w", pingErr)
	}

	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}

	if log != nil {
		log.Debug("db opened",
			slog.Int("max_open_conns", cfg.MaxOpenConns),
			slog.Int("max_idle_conns", cfg.MaxIdleConns),
			slog.Duration("conn_max_lifetime", cfg.ConnMaxLifetime),
			slog.Duration("conn_max_idle_time", cfg.ConnMaxIdleTime),
		)
	}

	return db, nil
}

func openPostgres(cfg DBConfig) (*sql.DB, error) {
	connCfg, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	if cfg.Schema != "" {
		connCfg.RuntimeParams["search_path"] = cfg.Schema
	}
	if cfg.Role == "" {
		return stdlib.OpenDB(*connCfg), nil
	}
	if err := validateRoleIdent(cfg.Role); err != nil {
		return nil, err
	}
	stmt := "SET ROLE " + quoteIdent(cfg.Role)
	afterConnect := func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, stmt)
		if execErr == nil {
			return nil
		}
		wrapped := fmt.Errorf("db: SET ROLE %q: %w", cfg.Role, execErr)
		if _, ok := errors.AsType[*pgconn.PgError](execErr); ok {
			return permanent(wrapped)
		}
		return wrapped
	}
	return stdlib.OpenDB(*connCfg, stdlib.OptionAfterConnect(afterConnect)), nil
}
