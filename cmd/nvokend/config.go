package main

import (
	"fmt"
	"time"

	"github.com/deepnoodle-ai/wonton/env"

	"github.com/deepnoodle-ai/nvoken/internal/daemon"
)

type config struct {
	Port string `env:"PORT" envDefault:"8080"`
}

type migrationConfig struct {
	DatabaseURL string        `env:"DATABASE_URL"`
	Timeout     time.Duration `env:"MIGRATION_TIMEOUT" envDefault:"5m"`
}

func loadDaemonConfig() (daemon.Config, error) {
	// Publish .env into os.Environ for local dev. No-overwrite, so shell
	// exports still win. Missing .env is fine.
	_ = env.LoadEnvFile(".env")

	cfg, err := env.Parse[config]()
	if err != nil {
		return daemon.Config{}, fmt.Errorf("failed to load configuration: %w", err)
	}
	return daemon.Config{
		Port: cfg.Port,
	}, nil
}

func loadMigrationConfig() (daemon.MigrationConfig, error) {
	_ = env.LoadEnvFile(".env")

	cfg, err := env.Parse[migrationConfig]()
	if err != nil {
		return daemon.MigrationConfig{}, fmt.Errorf("failed to load migration configuration: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return daemon.MigrationConfig{}, fmt.Errorf("DATABASE_URL is required for migrate")
	}
	return daemon.MigrationConfig{
		DatabaseURL: cfg.DatabaseURL,
		Timeout:     cfg.Timeout,
	}, nil
}
