package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
)

type MigrationConfig struct {
	DatabaseURL string
	Timeout     time.Duration
}

// Migrate applies all known database migrations as one bounded, serialized
// operation. Server startup deliberately does not call this function.
func Migrate(ctx context.Context, cfg MigrationConfig) error {
	if cfg.Timeout <= 0 {
		return fmt.Errorf("migration timeout must be positive")
	}
	migrationCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	if err := postgres.NewMigrator(cfg.DatabaseURL, cfg.Timeout, slog.Default()).Apply(migrationCtx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}
