package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/wonton/env"

	"github.com/deepnoodle-ai/nvoken/internal/daemon"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/services"
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

	EngineConcurrency             int           `env:"ENGINE_CONCURRENCY" envDefault:"8"`
	EnginePollInterval            time.Duration `env:"ENGINE_POLL_INTERVAL" envDefault:"1s"`
	EngineLeaseDuration           time.Duration `env:"ENGINE_LEASE_DURATION" envDefault:"30s"`
	EngineHeartbeatInterval       time.Duration `env:"ENGINE_HEARTBEAT_INTERVAL" envDefault:"10s"`
	EngineReaperInterval          time.Duration `env:"ENGINE_REAPER_INTERVAL" envDefault:"10s"`
	EngineReaperBatchLimit        int           `env:"ENGINE_REAPER_BATCH_LIMIT" envDefault:"100"`
	EngineDrainGrace              time.Duration `env:"ENGINE_DRAIN_GRACE" envDefault:"30s"`
	EngineExecutionSegmentCeiling time.Duration `env:"ENGINE_EXECUTION_SEGMENT_CEILING" envDefault:"15m"`
	EngineSettlementReserve       time.Duration `env:"ENGINE_SETTLEMENT_RESERVE" envDefault:"5s"`

	InvocationDefaultWallClockTimeout time.Duration `env:"INVOCATION_DEFAULT_WALL_CLOCK_TIMEOUT" envDefault:"30m"`
	InvocationDefaultActiveTimeout    time.Duration `env:"INVOCATION_DEFAULT_ACTIVE_EXECUTION_TIMEOUT" envDefault:"30m"`
	InvocationDefaultMaxIterations    int           `env:"INVOCATION_DEFAULT_MAX_ITERATIONS" envDefault:"1"`
	InvocationMaxWallClockTimeout     time.Duration `env:"INVOCATION_MAX_WALL_CLOCK_TIMEOUT" envDefault:"24h"`
	InvocationMaxActiveTimeout        time.Duration `env:"INVOCATION_MAX_ACTIVE_EXECUTION_TIMEOUT" envDefault:"24h"`
	InvocationMaxOutputTokens         int           `env:"INVOCATION_MAX_OUTPUT_TOKENS" envDefault:"1000000"`
	InvocationMaxEstimatedCostMicros  int64         `env:"INVOCATION_MAX_ESTIMATED_COST_MICROUSD" envDefault:"1000000000"`
	InvocationMaxIterations           int           `env:"INVOCATION_MAX_ITERATIONS" envDefault:"100"`
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
		return daemon.Config{}, fmt.Errorf("serve: DATABASE_URL is required")
	}
	if cfg.DatabaseMaxConns < 2 {
		return daemon.Config{}, fmt.Errorf("serve: DATABASE_MAX_CONNS must be at least 2 (one connection is reserved for cancellation notifications)")
	}
	if cfg.RuntimeAPIKey == "" {
		return daemon.Config{}, fmt.Errorf("serve: RUNTIME_API_KEY is required")
	}
	engineConfig := engine.Config{
		Concurrency: cfg.EngineConcurrency, PollInterval: cfg.EnginePollInterval,
		LeaseDuration: cfg.EngineLeaseDuration, HeartbeatInterval: cfg.EngineHeartbeatInterval,
		ReaperInterval: cfg.EngineReaperInterval, ReaperBatchLimit: cfg.EngineReaperBatchLimit,
		DrainGrace:              cfg.EngineDrainGrace,
		ExecutionSegmentCeiling: cfg.EngineExecutionSegmentCeiling,
		SettlementReserve:       cfg.EngineSettlementReserve,
	}
	budgetPolicy := services.BudgetPolicy{
		DefaultWallClockTimeout:       cfg.InvocationDefaultWallClockTimeout,
		DefaultActiveExecutionTimeout: cfg.InvocationDefaultActiveTimeout,
		DefaultMaxIterations:          cfg.InvocationDefaultMaxIterations,
		MaxWallClockTimeout:           cfg.InvocationMaxWallClockTimeout,
		MaxActiveExecutionTimeout:     cfg.InvocationMaxActiveTimeout,
		MaxOutputTokens:               cfg.InvocationMaxOutputTokens,
		MaxEstimatedCostMicros:        cfg.InvocationMaxEstimatedCostMicros,
		MaxIterations:                 cfg.InvocationMaxIterations,
	}
	if _, err := budgetPolicy.Resolve(nil); err != nil {
		return daemon.Config{}, fmt.Errorf("invalid Invocation budget configuration: %w", err)
	}
	if err := engine.ValidateConfig(engineConfig); err != nil {
		return daemon.Config{}, fmt.Errorf("invalid engine configuration: %w", err)
	}
	if cfg.ShutdownTimeout <= 0 {
		return daemon.Config{}, fmt.Errorf("serve: SHUTDOWN_TIMEOUT must be positive")
	}
	if cfg.EngineDrainGrace > cfg.ShutdownTimeout-time.Second {
		return daemon.Config{}, fmt.Errorf("serve: ENGINE_DRAIN_GRACE must leave at least 1s inside SHUTDOWN_TIMEOUT")
	}
	if len(cfg.RuntimeAPIKey) < 32 {
		return daemon.Config{}, fmt.Errorf("serve: RUNTIME_API_KEY must be at least 32 bytes")
	}
	var tenantConstraint *string
	if cfg.RuntimeTenantRef != "" {
		if !utf8.ValidString(cfg.RuntimeTenantRef) || strings.TrimSpace(cfg.RuntimeTenantRef) == "" {
			return daemon.Config{}, fmt.Errorf("serve: RUNTIME_TENANT_REF must be valid UTF-8 and not blank")
		}
		if utf8.RuneCountInString(cfg.RuntimeTenantRef) > 255 {
			return daemon.Config{}, fmt.Errorf("serve: RUNTIME_TENANT_REF must be at most 255 Unicode characters")
		}
		tenantConstraint = &cfg.RuntimeTenantRef
	}
	return daemon.Config{
		Port: cfg.Port, DatabaseURL: cfg.DatabaseURL, DatabaseMaxConns: cfg.DatabaseMaxConns,
		RuntimeAPIKey: cfg.RuntimeAPIKey, RuntimeTenantConstraint: tenantConstraint,
		AnthropicAPIKey: cfg.AnthropicAPIKey, OpenAIAPIKey: cfg.OpenAIAPIKey,
		ShutdownTimeout: cfg.ShutdownTimeout, Engine: engineConfig, Budgets: budgetPolicy,
	}, nil
}

func loadMigrationConfig() (daemon.MigrationConfig, error) {
	_ = env.LoadEnvFile(".env")

	cfg, err := env.Parse[migrationConfig]()
	if err != nil {
		return daemon.MigrationConfig{}, fmt.Errorf("failed to load migration configuration: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return daemon.MigrationConfig{}, fmt.Errorf("migrate: DATABASE_URL is required")
	}
	return daemon.MigrationConfig{
		DatabaseURL: cfg.DatabaseURL,
		Timeout:     cfg.Timeout,
	}, nil
}
