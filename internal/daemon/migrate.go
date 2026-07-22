package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/observability"
)

type MigrationConfig struct {
	DatabaseURL                string
	Timeout                    time.Duration
	CurrentBuildVersion        string
	CurrentBinarySchemaVersion uint
	TargetBuildVersion         string
	Mode                       postgres.UpgradeMode
}

// PreflightMigration validates a release pair without changing the database.
func PreflightMigration(ctx context.Context, cfg MigrationConfig) error {
	if cfg.Timeout <= 0 {
		return fmt.Errorf("migration timeout must be positive")
	}
	preflightCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	_, err := migrationPreflight(preflightCtx, cfg, slog.Default())
	return err
}

// Migrate applies all known database migrations as one bounded, serialized
// operation. Server startup deliberately does not call this function.
func Migrate(ctx context.Context, cfg MigrationConfig) error {
	if cfg.Timeout <= 0 {
		return fmt.Errorf("migration timeout must be positive")
	}
	migrationCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	result, err := migrationPreflight(migrationCtx, cfg, slog.Default())
	if err != nil {
		return err
	}
	if err := postgres.NewMigrator(cfg.DatabaseURL, cfg.Timeout, slog.Default()).Apply(migrationCtx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	pool, err := postgres.OpenPoolWithConfig(migrationCtx, cfg.DatabaseURL, postgres.PoolConfig{MaxConns: 1})
	if err != nil {
		return fmt.Errorf("open migrated database: %w", err)
	}
	defer pool.Close()
	status, err := postgres.InspectSchema(migrationCtx, pool)
	if err != nil {
		return fmt.Errorf("inspect migrated database: %w", err)
	}
	if err := status.CompatibilityError(); err != nil {
		return fmt.Errorf("verify migrated database: %w", err)
	}
	if status.Current != result.TargetSchemaVersion ||
		status.MinimumBinarySchemaVersion != result.TargetMinimumBinarySchemaVersion {
		return fmt.Errorf("verify migrated compatibility record: schema %06d minimum %06d, want schema %06d minimum %06d",
			status.Current,
			status.MinimumBinarySchemaVersion,
			result.TargetSchemaVersion,
			result.TargetMinimumBinarySchemaVersion,
		)
	}
	return nil
}

func migrationPreflight(
	ctx context.Context,
	cfg MigrationConfig,
	logger *slog.Logger,
) (postgres.UpgradePreflightResult, error) {
	pool, err := postgres.OpenPoolWithConfig(ctx, cfg.DatabaseURL, postgres.PoolConfig{MaxConns: 1})
	if err != nil {
		logUpgradePreflight(logger, postgres.UpgradePreflightResult{
			CurrentBuildVersion: cfg.CurrentBuildVersion,
			TargetBuildVersion:  cfg.TargetBuildVersion,
			Mode:                cfg.Mode,
		}, observability.OutcomeFailed, observability.ErrorClass(err))
		return postgres.UpgradePreflightResult{}, fmt.Errorf("open database for migration preflight: %w", err)
	}
	defer pool.Close()
	result, err := postgres.PreflightUpgrade(ctx, pool, postgres.UpgradePreflightRequest{
		CurrentBuildVersion:        cfg.CurrentBuildVersion,
		CurrentBinarySchemaVersion: cfg.CurrentBinarySchemaVersion,
		TargetBuildVersion:         cfg.TargetBuildVersion,
		Mode:                       cfg.Mode,
	})
	if err != nil {
		errorClass := observability.ErrorClass(err)
		if errorClass == "internal" {
			errorClass = "incompatible"
		}
		logUpgradePreflight(logger, result, observability.OutcomeFailed, errorClass)
		return postgres.UpgradePreflightResult{}, fmt.Errorf("migration preflight: %w", err)
	}
	logUpgradePreflight(logger, result, observability.OutcomeSuccess, "none")
	return result, nil
}

func logUpgradePreflight(
	logger *slog.Logger,
	result postgres.UpgradePreflightResult,
	outcome string,
	errorClass string,
) {
	logger.Info("database upgrade preflight",
		"event", observability.EventUpgradePreflight,
		"outcome", outcome,
		"error_class", errorClass,
		"migration_mode", result.Mode,
		"current_build_version", result.CurrentBuildVersion,
		"target_build_version", result.TargetBuildVersion,
		"current_binary_schema_version", result.CurrentBinarySchemaVersion,
		"current_database_schema_version", result.CurrentDatabaseSchemaVersion,
		"target_schema_version", result.TargetSchemaVersion,
		"target_minimum_binary_schema_version", result.TargetMinimumBinarySchemaVersion,
		"target_classification", result.TargetClassification,
		"ordinary_compatibility_window", result.OrdinaryCompatibilityWindow)
}
