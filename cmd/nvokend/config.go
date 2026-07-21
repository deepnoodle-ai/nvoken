package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/wonton/env"

	"github.com/deepnoodle-ai/nvoken/internal/daemon"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
)

type config struct {
	Port             string        `env:"PORT" envDefault:"8080"`
	DatabaseURL      string        `env:"DATABASE_URL"`
	DatabaseMaxConns int32         `env:"DATABASE_MAX_CONNS" envDefault:"10"`
	RuntimeAPIKey    string        `env:"RUNTIME_API_KEY"`
	RuntimeTenantRef string        `env:"RUNTIME_TENANT_REF"`
	AnthropicAPIKey  string        `env:"ANTHROPIC_API_KEY"`
	OpenAIAPIKey     string        `env:"OPENAI_API_KEY"`
	ShutdownTimeout  time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"40s"`

	EngineConcurrency       int           `env:"ENGINE_CONCURRENCY" envDefault:"8"`
	EnginePollInterval      time.Duration `env:"ENGINE_POLL_INTERVAL" envDefault:"1s"`
	EngineLeaseDuration     time.Duration `env:"ENGINE_LEASE_DURATION" envDefault:"30s"`
	EngineHeartbeatInterval time.Duration `env:"ENGINE_HEARTBEAT_INTERVAL" envDefault:"10s"`
	EngineReaperInterval    time.Duration `env:"ENGINE_REAPER_INTERVAL" envDefault:"10s"`
	EngineReaperBatchLimit  int           `env:"ENGINE_REAPER_BATCH_LIMIT" envDefault:"100"`
	EngineDrainGrace        time.Duration `env:"ENGINE_DRAIN_GRACE" envDefault:"30s"`
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
	engineConfig := engine.Config{
		Concurrency: cfg.EngineConcurrency, PollInterval: cfg.EnginePollInterval,
		LeaseDuration: cfg.EngineLeaseDuration, HeartbeatInterval: cfg.EngineHeartbeatInterval,
		ReaperInterval: cfg.EngineReaperInterval, ReaperBatchLimit: cfg.EngineReaperBatchLimit,
		DrainGrace: cfg.EngineDrainGrace,
	}
	if err := engine.ValidateConfig(engineConfig); err != nil {
		return daemon.Config{}, fmt.Errorf("invalid engine configuration: %w", err)
	}
	if cfg.ShutdownTimeout <= 0 {
		return daemon.Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be positive")
	}
	if cfg.EngineDrainGrace > cfg.ShutdownTimeout-time.Second {
		return daemon.Config{}, fmt.Errorf("ENGINE_DRAIN_GRACE must leave at least 1s inside SHUTDOWN_TIMEOUT")
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
		AnthropicAPIKey: cfg.AnthropicAPIKey, OpenAIAPIKey: cfg.OpenAIAPIKey,
		ShutdownTimeout: cfg.ShutdownTimeout, Engine: engineConfig,
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
