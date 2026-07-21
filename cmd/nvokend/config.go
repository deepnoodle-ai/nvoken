package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/wonton/env"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/cloudtasks"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpapi"
	"github.com/deepnoodle-ai/nvoken/internal/daemon"
	dispatchruntime "github.com/deepnoodle-ai/nvoken/internal/dispatch"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type config struct {
	ProcessRole             string        `env:"NVOKEN_PROCESS_ROLE" envDefault:"combined"`
	InvocationExecutionMode string        `env:"INVOCATION_EXECUTION_MODE" envDefault:"embedded"`
	Port                    string        `env:"PORT" envDefault:"8080"`
	DatabaseURL             string        `env:"DATABASE_URL"`
	DatabaseMaxConns        int32         `env:"DATABASE_MAX_CONNS" envDefault:"10"`
	RuntimeAPIKey           string        `env:"RUNTIME_API_KEY"`
	RuntimeTenantRef        string        `env:"RUNTIME_TENANT_REF"`
	AnthropicAPIKey         string        `env:"ANTHROPIC_API_KEY"`
	OpenAIAPIKey            string        `env:"OPENAI_API_KEY"`
	ShutdownTimeout         time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"40s"`
	ExecutorAttemptTimeout  time.Duration `env:"EXECUTOR_ATTEMPT_TIMEOUT" envDefault:"29m55s"`
	RedisURL                string        `env:"REDIS_URL"`
	RedisPassword           string        `env:"REDIS_PASSWORD"`
	RedisCACertificate      string        `env:"REDIS_CA_CERT"`
	LiveEventBuffer         int           `env:"LIVE_EVENT_BUFFER" envDefault:"64"`
	StreamPollInterval      time.Duration `env:"STREAM_POLL_INTERVAL" envDefault:"1s"`
	StreamKeepaliveInterval time.Duration `env:"STREAM_KEEPALIVE_INTERVAL" envDefault:"15s"`
	StreamMaxLifetime       time.Duration `env:"STREAM_MAX_LIFETIME" envDefault:"55m"`
	StreamWriteTimeout      time.Duration `env:"STREAM_WRITE_TIMEOUT" envDefault:"10s"`

	DispatchQueue                 string        `env:"DISPATCH_QUEUE" envDefault:"execution"`
	DispatchPublicationLease      time.Duration `env:"DISPATCH_PUBLICATION_LEASE" envDefault:"30s"`
	DispatchPublishRetryBase      time.Duration `env:"DISPATCH_PUBLISH_RETRY_BASE" envDefault:"1s"`
	DispatchPublishRetryMax       time.Duration `env:"DISPATCH_PUBLISH_RETRY_MAX" envDefault:"1m"`
	DispatchStaleAfter            time.Duration `env:"DISPATCH_STALE_AFTER" envDefault:"5m"`
	DispatchRetention             time.Duration `env:"DISPATCH_RETENTION" envDefault:"168h"`
	DispatchBatchLimit            int           `env:"DISPATCH_BATCH_LIMIT" envDefault:"100"`
	DispatchPublishInterval       time.Duration `env:"DISPATCH_PUBLISH_INTERVAL" envDefault:"1s"`
	DispatchReconcileInterval     time.Duration `env:"DISPATCH_RECONCILE_INTERVAL" envDefault:"1m"`
	DispatchRetentionInterval     time.Duration `env:"DISPATCH_RETENTION_INTERVAL" envDefault:"1h"`
	DispatchSyntheticAttemptDelay time.Duration `env:"DISPATCH_SYNTHETIC_ATTEMPT_DELAY" envDefault:"0s"`

	CloudTasksQueue              string        `env:"CLOUD_TASKS_QUEUE"`
	CloudTasksExecutorURL        string        `env:"CLOUD_TASKS_EXECUTOR_URL"`
	CloudTasksOIDCServiceAccount string        `env:"CLOUD_TASKS_OIDC_SERVICE_ACCOUNT"`
	CloudTasksOIDCAudience       string        `env:"CLOUD_TASKS_OIDC_AUDIENCE"`
	CloudTasksDispatchDeadline   time.Duration `env:"CLOUD_TASKS_DISPATCH_DEADLINE" envDefault:"30m"`

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

type dispatchSmokeConfig struct {
	DatabaseURL      string `env:"DATABASE_URL"`
	DatabaseMaxConns int32  `env:"DATABASE_MAX_CONNS" envDefault:"2"`
	Queue            string `env:"DISPATCH_QUEUE" envDefault:"execution"`
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
	role := daemon.ProcessRole(cfg.ProcessRole)
	if role != daemon.ProcessRoleCombined && role != daemon.ProcessRoleExecutor {
		return daemon.Config{}, fmt.Errorf("serve: NVOKEN_PROCESS_ROLE must be combined or executor")
	}
	executionMode := services.InvocationExecutionMode(cfg.InvocationExecutionMode)
	if executionMode != services.InvocationExecutionEmbedded && executionMode != services.InvocationExecutionCloudTasks {
		return daemon.Config{}, fmt.Errorf("serve: INVOCATION_EXECUTION_MODE must be embedded or cloud_tasks")
	}
	minimumConns := int32(1)
	if role == daemon.ProcessRoleCombined || (role == daemon.ProcessRoleExecutor && executionMode == services.InvocationExecutionCloudTasks) {
		minimumConns = 2
	}
	if cfg.DatabaseMaxConns < minimumConns {
		return daemon.Config{}, fmt.Errorf("serve: DATABASE_MAX_CONNS must be at least %d for the %s role", minimumConns, role)
	}
	if role == daemon.ProcessRoleCombined && cfg.RuntimeAPIKey == "" {
		return daemon.Config{}, fmt.Errorf("serve: RUNTIME_API_KEY is required for the combined role")
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
	if cfg.ShutdownTimeout <= time.Second {
		return daemon.Config{}, fmt.Errorf("serve: SHUTDOWN_TIMEOUT must exceed 1s")
	}
	if role == daemon.ProcessRoleCombined && cfg.EngineDrainGrace > cfg.ShutdownTimeout-time.Second {
		return daemon.Config{}, fmt.Errorf("serve: ENGINE_DRAIN_GRACE must leave at least 1s inside SHUTDOWN_TIMEOUT")
	}
	if role == daemon.ProcessRoleCombined && len(cfg.RuntimeAPIKey) < 32 {
		return daemon.Config{}, fmt.Errorf("serve: RUNTIME_API_KEY must be at least 32 bytes")
	}
	dispatchConfig := services.DispatchConfig{
		Queue: cfg.DispatchQueue, PublicationLease: cfg.DispatchPublicationLease,
		PublishRetryBase: cfg.DispatchPublishRetryBase, PublishRetryMax: cfg.DispatchPublishRetryMax,
		StaleAfter: cfg.DispatchStaleAfter, Retention: cfg.DispatchRetention, BatchLimit: cfg.DispatchBatchLimit,
		SyntheticAttemptDelay: cfg.DispatchSyntheticAttemptDelay,
	}
	if err := services.ValidateDispatchConfig(dispatchConfig); err != nil {
		return daemon.Config{}, fmt.Errorf("invalid execution dispatch configuration: %w", err)
	}
	controllerConfig := dispatchruntime.ControllerConfig{
		PublishInterval: cfg.DispatchPublishInterval, ReconcileInterval: cfg.DispatchReconcileInterval,
		RetentionInterval: cfg.DispatchRetentionInterval, BatchLimit: cfg.DispatchBatchLimit,
		RepairInvocations: executionMode == services.InvocationExecutionCloudTasks,
	}
	if err := dispatchruntime.ValidateControllerConfig(controllerConfig); err != nil {
		return daemon.Config{}, fmt.Errorf("invalid execution dispatch controller configuration: %w", err)
	}
	cloudTasksConfig := cloudtasks.Config{
		Queue: cfg.CloudTasksQueue, ExecutorURL: cfg.CloudTasksExecutorURL,
		OIDCServiceAccount: cfg.CloudTasksOIDCServiceAccount, OIDCAudience: cfg.CloudTasksOIDCAudience,
		DispatchDeadline: cfg.CloudTasksDispatchDeadline,
	}
	cloudTasksFields := []string{cfg.CloudTasksQueue, cfg.CloudTasksExecutorURL, cfg.CloudTasksOIDCServiceAccount, cfg.CloudTasksOIDCAudience}
	configuredCloudTasksFields := 0
	for _, value := range cloudTasksFields {
		if value != "" {
			configuredCloudTasksFields++
		}
	}
	if configuredCloudTasksFields != 0 && configuredCloudTasksFields != len(cloudTasksFields) {
		return daemon.Config{}, fmt.Errorf("serve: Cloud Tasks queue, executor URL, OIDC service account, and audience must be configured together")
	}
	if configuredCloudTasksFields > 0 {
		if role != daemon.ProcessRoleCombined {
			return daemon.Config{}, fmt.Errorf("serve: Cloud Tasks publication is available only in the combined role")
		}
		if err := cloudtasks.ValidateConfig(cloudTasksConfig); err != nil {
			return daemon.Config{}, fmt.Errorf("invalid Cloud Tasks configuration: %w", err)
		}
	}
	if role == daemon.ProcessRoleCombined && executionMode == services.InvocationExecutionCloudTasks && configuredCloudTasksFields != len(cloudTasksFields) {
		return daemon.Config{}, fmt.Errorf("serve: cloud_tasks Invocation execution requires complete Cloud Tasks queue and OIDC configuration")
	}
	if role == daemon.ProcessRoleExecutor && (cfg.ExecutorAttemptTimeout <= 0 || cfg.ExecutorAttemptTimeout >= cfg.CloudTasksDispatchDeadline) {
		return daemon.Config{}, fmt.Errorf("serve: EXECUTOR_ATTEMPT_TIMEOUT must be positive and less than CLOUD_TASKS_DISPATCH_DEADLINE")
	}
	if role == daemon.ProcessRoleExecutor && executionMode == services.InvocationExecutionCloudTasks && cfg.EngineExecutionSegmentCeiling > cfg.ExecutorAttemptTimeout {
		return daemon.Config{}, fmt.Errorf("serve: ENGINE_EXECUTION_SEGMENT_CEILING must not exceed EXECUTOR_ATTEMPT_TIMEOUT")
	}
	if executionMode == services.InvocationExecutionCloudTasks && cfg.RedisURL == "" {
		return daemon.Config{}, fmt.Errorf("serve: cloud_tasks Invocation execution requires REDIS_URL for cross-process live output")
	}
	if cfg.LiveEventBuffer <= 0 {
		return daemon.Config{}, fmt.Errorf("serve: LIVE_EVENT_BUFFER must be positive")
	}
	if cfg.StreamPollInterval <= 0 || cfg.StreamKeepaliveInterval <= 0 || cfg.StreamMaxLifetime <= 0 || cfg.StreamWriteTimeout <= 0 {
		return daemon.Config{}, fmt.Errorf("serve: stream intervals, lifetime, and write timeout must be positive")
	}
	if cfg.StreamWriteTimeout >= cfg.StreamMaxLifetime {
		return daemon.Config{}, fmt.Errorf("serve: STREAM_WRITE_TIMEOUT must be less than STREAM_MAX_LIFETIME")
	}
	generatesInvocations := (role == daemon.ProcessRoleCombined && executionMode == services.InvocationExecutionEmbedded) ||
		(role == daemon.ProcessRoleExecutor && executionMode == services.InvocationExecutionCloudTasks)
	if generatesInvocations && cfg.AnthropicAPIKey == "" && cfg.OpenAIAPIKey == "" {
		return daemon.Config{}, fmt.Errorf("serve: the Invocation-generating role requires ANTHROPIC_API_KEY, OPENAI_API_KEY, or both")
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
		ProcessRole:             role,
		InvocationExecutionMode: executionMode,
		RuntimeAPIKey:           cfg.RuntimeAPIKey, RuntimeTenantConstraint: tenantConstraint,
		AnthropicAPIKey: cfg.AnthropicAPIKey, OpenAIAPIKey: cfg.OpenAIAPIKey,
		ShutdownTimeout: cfg.ShutdownTimeout, Engine: engineConfig, Budgets: budgetPolicy,
		Dispatch: dispatchConfig, DispatchController: controllerConfig, CloudTasks: cloudTasksConfig,
		ExecutorAttemptTimeout: cfg.ExecutorAttemptTimeout,
		RedisURL:               cfg.RedisURL, RedisPassword: cfg.RedisPassword,
		RedisCACertificate: cfg.RedisCACertificate, LiveEventBuffer: cfg.LiveEventBuffer,
		Stream: httpapi.StreamConfig{
			PollInterval: cfg.StreamPollInterval, KeepaliveInterval: cfg.StreamKeepaliveInterval,
			MaxLifetime: cfg.StreamMaxLifetime, WriteTimeout: cfg.StreamWriteTimeout,
		},
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

func loadDispatchSmokeConfig() (daemon.DispatchSmokeConfig, error) {
	_ = env.LoadEnvFile(".env")
	cfg, err := env.Parse[dispatchSmokeConfig]()
	if err != nil {
		return daemon.DispatchSmokeConfig{}, fmt.Errorf("failed to load configuration: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return daemon.DispatchSmokeConfig{}, fmt.Errorf("dispatch-smoke: DATABASE_URL is required")
	}
	if cfg.DatabaseMaxConns < 1 {
		return daemon.DispatchSmokeConfig{}, fmt.Errorf("dispatch-smoke: DATABASE_MAX_CONNS must be positive")
	}
	dispatchCfg := services.DefaultDispatchConfig()
	dispatchCfg.Queue = cfg.Queue
	if err := services.ValidateDispatchConfig(dispatchCfg); err != nil {
		return daemon.DispatchSmokeConfig{}, fmt.Errorf("dispatch-smoke: %w", err)
	}
	return daemon.DispatchSmokeConfig{
		DatabaseURL: cfg.DatabaseURL, DatabaseMaxConns: cfg.DatabaseMaxConns, Queue: cfg.Queue,
	}, nil
}
