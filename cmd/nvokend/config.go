package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/wonton/env"

	"github.com/deepnoodle-ai/nvoken/internal/daemon"
)

type config struct {
	Port             string `env:"PORT" envDefault:"8080"`
	DatabaseURL      string `env:"DATABASE_URL"`
	DatabaseMaxConns int32  `env:"DATABASE_MAX_CONNS" envDefault:"10"`
	RuntimeAPIKey    string `env:"RUNTIME_API_KEY"`
	RuntimeTenantRef string `env:"RUNTIME_TENANT_REF"`
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
	if cfg.DatabaseURL == "" {
		return daemon.Config{}, fmt.Errorf("DATABASE_URL is required for serve")
	}
	if cfg.DatabaseMaxConns <= 0 {
		return daemon.Config{}, fmt.Errorf("DATABASE_MAX_CONNS must be positive")
	}
	if cfg.RuntimeAPIKey == "" {
		return daemon.Config{}, fmt.Errorf("RUNTIME_API_KEY is required for serve")
	}
	if len(cfg.RuntimeAPIKey) < 32 {
		return daemon.Config{}, fmt.Errorf("RUNTIME_API_KEY must be at least 32 bytes")
	}
	var tenantConstraint *string
	if cfg.RuntimeTenantRef != "" {
		if !utf8.ValidString(cfg.RuntimeTenantRef) || strings.TrimSpace(cfg.RuntimeTenantRef) == "" {
			return daemon.Config{}, fmt.Errorf("RUNTIME_TENANT_REF must be valid UTF-8 and not blank")
		}
		if utf8.RuneCountInString(cfg.RuntimeTenantRef) > 255 {
			return daemon.Config{}, fmt.Errorf("RUNTIME_TENANT_REF must be at most 255 Unicode characters")
		}
		tenantConstraint = &cfg.RuntimeTenantRef
	}
	return daemon.Config{
		Port: cfg.Port, DatabaseURL: cfg.DatabaseURL, DatabaseMaxConns: cfg.DatabaseMaxConns,
		RuntimeAPIKey: cfg.RuntimeAPIKey, RuntimeTenantConstraint: tenantConstraint,
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
