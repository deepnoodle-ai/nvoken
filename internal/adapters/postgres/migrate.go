package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const migrationTable = "nvoken_schema_migrations"

//go:embed migrations/*.up.sql
var migrationFiles embed.FS

type Migrator struct {
	databaseURL string
	timeout     time.Duration
	logger      *slog.Logger
}

func NewMigrator(databaseURL string, timeout time.Duration, logger *slog.Logger) *Migrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Migrator{databaseURL: databaseURL, timeout: timeout, logger: logger}
}

func (m *Migrator) Apply(ctx context.Context) error {
	if m.databaseURL == "" {
		return fmt.Errorf("database URL is required")
	}
	effectiveTimeout, err := migrationTimeout(ctx, m.timeout)
	if err != nil {
		return err
	}

	sourceDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	versions, err := migrationVersions(sourceDriver)
	if err != nil {
		_ = sourceDriver.Close()
		return err
	}

	config, err := pgx.ParseConfig(m.databaseURL)
	if err != nil {
		_ = sourceDriver.Close()
		return fmt.Errorf("parse migration database URL: %w", err)
	}
	if config.ConnectTimeout <= 0 || config.ConnectTimeout > effectiveTimeout {
		config.ConnectTimeout = effectiveTimeout
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	// golang-migrate's pgx driver uses context.Background for its pinned
	// connection. A server-side timeout therefore bounds both advisory-lock
	// acquisition and statements even if a caller cannot cancel that context.
	config.RuntimeParams["statement_timeout"] = strconv.FormatInt(max(1, effectiveTimeout.Milliseconds()), 10)

	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	databaseDriver, err := migratepgx.WithInstance(db, &migratepgx.Config{
		MigrationsTable:  migrationTable,
		StatementTimeout: effectiveTimeout,
	})
	if err != nil {
		_ = db.Close()
		_ = sourceDriver.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("open migration database: %w", err)
	}

	runner, err := migrate.NewWithInstance("iofs", sourceDriver, "pgx5", databaseDriver)
	if err != nil {
		_ = databaseDriver.Close()
		_ = sourceDriver.Close()
		return fmt.Errorf("create migration runner: %w", err)
	}
	runner.LockTimeout = effectiveTimeout
	runner.Log = migrateSlog{logger: m.logger}
	defer func() {
		sourceErr, databaseErr := runner.Close()
		if sourceErr != nil {
			m.logger.Warn("close migration source", "error", sourceErr)
		}
		if databaseErr != nil {
			m.logger.Warn("close migration database", "error", databaseErr)
		}
	}()

	stopWatching := make(chan struct{})
	defer close(stopWatching)
	go func() {
		select {
		case <-ctx.Done():
			select {
			case runner.GracefulStop <- true:
			default:
			}
		case <-stopWatching:
		}
	}()

	current, err := validateMigrationState(runner, versions)
	if err != nil {
		return err
	}
	m.logger.Info("database migration started", "current_version", current, "target_version", versions[len(versions)-1])

	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("apply database migrations: %w", err)
	}
	current, err = validateMigrationState(runner, versions)
	if err != nil {
		return err
	}
	m.logger.Info("database migration completed", "version", current)
	return nil
}

func migrationTimeout(ctx context.Context, configured time.Duration) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if configured <= 0 {
		return 0, fmt.Errorf("migration timeout must be positive")
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		if remaining < configured {
			return remaining, nil
		}
	}
	return configured, nil
}

func migrationVersions(driver source.Driver) ([]uint, error) {
	version, err := driver.First()
	if err != nil {
		return nil, fmt.Errorf("read first embedded migration: %w", err)
	}
	versions := []uint{version}
	for {
		next, err := driver.Next(version)
		if errors.Is(err, fs.ErrNotExist) {
			return versions, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read embedded migration after %06d: %w", version, err)
		}
		versions = append(versions, next)
		version = next
	}
}

func validateMigrationState(runner *migrate.Migrate, versions []uint) (uint, error) {
	version, dirty, err := runner.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read database migration version: %w", err)
	}
	if dirty {
		return 0, fmt.Errorf("database migration version %06d is dirty", version)
	}
	known := false
	for _, candidate := range versions {
		if candidate == version {
			known = true
			break
		}
	}
	if !known {
		return 0, fmt.Errorf("database migration version %06d is unknown to this binary", version)
	}
	return version, nil
}

type migrateSlog struct {
	logger *slog.Logger
}

func (l migrateSlog) Printf(format string, args ...any) {
	detail := strings.TrimSpace(fmt.Sprintf(format, args...))
	if detail != "" {
		l.logger.Info("database migration progress", "detail", detail)
	}
}

func (migrateSlog) Verbose() bool { return true }
