package boot

import (
	"context"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"time"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/whatsapp/meow"
	"altalune.id/openwa/schema"
)

func openDBAndMigrate(ctx context.Context, cfg *config.Config, log *slog.Logger) (db.Pool, *tenant.PgConn, error) {
	if cfg.DB.AutoMigrate {
		if err := RunMigrations(ctx, cfg, log); err != nil {
			return db.Pool{}, nil, err
		}
	}
	pool, err := db.OpenPool(ctx, cfg.DB, log)
	if err != nil {
		return db.Pool{}, nil, err
	}
	if err := schema.AssertRequiredTables(ctx, pool.W, &cfg.DB); err != nil {
		_ = pool.Close()
		return db.Pool{}, nil, fmt.Errorf("boot: %w", err)
	}
	if !cfg.DB.AutoMigrate {
		if err := meow.AssertUpgraded(ctx, pool.W); err != nil {
			_ = pool.Close()
			return db.Pool{}, nil, fmt.Errorf("boot: %w", err)
		}
	}
	if err := schema.RLSGuard(ctx, pool.W, cfg); err != nil {
		_ = pool.Close()
		return db.Pool{}, nil, fmt.Errorf("boot: rls guard: %w", err)
	}
	return pool, tenant.NewPgConn(pool.W), nil
}

func buildSealer(cfg *config.Config) (sealer.Sealer, error) {
	key, err := sealerKey(cfg)
	if err != nil {
		return nil, err
	}
	sl, err := sealer.New(key)
	if err != nil {
		return nil, fmt.Errorf("boot: sealer: %w", err)
	}
	return sl, nil
}

func sealerKey(cfg *config.Config) ([]byte, error) {
	key, err := sealer.ParseKey(cfg.Security.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("boot: security.encryptionKey: %w", err)
	}
	return key, nil
}

// MigrationLockName is the advisory-lock name that serialises migrate runs sharing one table prefix.
func MigrationLockName(prefix string) string { return "migrate:" + prefix }

// MigratorDBConfig shapes the connection migrations run on: the migrator DSN when set, the migrator role always, on two sessions.
func MigratorDBConfig(cfg *config.Config) db.DBConfig {
	migCfg := cfg.DB
	if cfg.DB.Migrator.DSN != "" {
		migCfg.DSN = cfg.DB.Migrator.DSN
	}
	migCfg.Role = cfg.DB.Migrator.Role
	// NOTE: one pinned session holds the advisory lock; the second runs goose and the whatsmeow upgrade.
	migCfg.MaxOpenConns = 2
	migCfg.MaxIdleConns = 2
	return migCfg
}

const migrationUnlockBudget = 5 * time.Second

// RunMigrations applies goose migrations under one advisory lock; it is the only migrate path.
func RunMigrations(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	migCfg := MigratorDBConfig(cfg)
	migDB, err := db.Open(ctx, migCfg, log)
	if err != nil {
		return fmt.Errorf("boot: open migrator: %w", err)
	}
	defer func() { _ = migDB.Close() }()

	// NOTE: two connections suffice — goose pins one conn per run and whatsmeow's upgrade takes one conn per transaction; the other conn holds the lock.
	lockConn, err := migDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("boot: migrator lock conn: %w", err)
	}
	key := db.LockKey(MigrationLockName(cfg.DB.TablePrefix))
	if _, err := lockConn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		_ = lockConn.Close()
		return fmt.Errorf("boot: migrator lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), migrationUnlockBudget)
		defer cancel()
		if _, uerr := lockConn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key); uerr != nil {
			_ = lockConn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = lockConn.Close()
	}()

	migAwareCfg := *cfg
	migAwareCfg.DB = migCfg
	if err := schema.MigrateUp(ctx, migDB, &migAwareCfg); err != nil {
		return fmt.Errorf("boot: migrate: %w", err)
	}
	if err := meow.Upgrade(ctx, migDB, log); err != nil {
		return fmt.Errorf("boot: whatsmeow upgrade: %w", err)
	}
	if log != nil && cfg.DB.Migrator.DSN != "" {
		log.Info("boot: migrations applied via dedicated migrator connection")
	}
	return nil
}
