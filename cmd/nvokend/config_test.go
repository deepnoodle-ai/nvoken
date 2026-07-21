package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDaemonConfigDefaults(t *testing.T) {
	setServeConfig(t)
	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("loadDaemonConfig: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "8080")
	}
	if cfg.DatabaseMaxConns != 10 {
		t.Errorf("DatabaseMaxConns: got %d, want 10", cfg.DatabaseMaxConns)
	}
	if cfg.ShutdownTimeout != 40*time.Second {
		t.Errorf("ShutdownTimeout: got %s, want 40s", cfg.ShutdownTimeout)
	}
	if cfg.Engine.Concurrency != 8 || cfg.Engine.PollInterval != time.Second ||
		cfg.Engine.LeaseDuration != 30*time.Second || cfg.Engine.HeartbeatInterval != 10*time.Second ||
		cfg.Engine.ReaperInterval != 10*time.Second || cfg.Engine.ReaperBatchLimit != 100 ||
		cfg.Engine.DrainGrace != 30*time.Second || cfg.Engine.ExecutionSegmentCeiling != 15*time.Minute ||
		cfg.Engine.SettlementReserve != 5*time.Second {
		t.Fatalf("Engine defaults: %#v", cfg.Engine)
	}
	if cfg.Budgets.DefaultWallClockTimeout != 30*time.Minute ||
		cfg.Budgets.DefaultActiveExecutionTimeout != 30*time.Minute || cfg.Budgets.DefaultMaxIterations != 1 {
		t.Fatalf("budget defaults: %#v", cfg.Budgets)
	}
	if cfg.ProcessRole != "combined" || cfg.InvocationExecutionMode != "embedded" || cfg.Dispatch.Queue != "execution" ||
		cfg.Dispatch.PublicationLease != 30*time.Second || cfg.Dispatch.StaleAfter != 5*time.Minute ||
		cfg.Dispatch.Retention != 7*24*time.Hour || cfg.Dispatch.BatchLimit != 100 ||
		cfg.DispatchController.RetentionInterval != time.Hour || cfg.DispatchController.BatchLimit != 100 ||
		cfg.ExecutorAttemptTimeout != 29*time.Minute+55*time.Second {
		t.Fatalf("dispatch defaults: %#v", cfg)
	}
	if cfg.LiveEventBuffer != 64 || cfg.Stream.PollInterval != time.Second ||
		cfg.Stream.KeepaliveInterval != 15*time.Second || cfg.Stream.MaxLifetime != 55*time.Minute ||
		cfg.Stream.WriteTimeout != 10*time.Second {
		t.Fatalf("stream defaults: %#v", cfg)
	}
	if cfg.CallbackSigningKey != "" || cfg.CallbackDelivery.LeaseDuration != 30*time.Second ||
		cfg.CallbackDelivery.MaxAttempts != 5 || cfg.CallbackController.Concurrency != 4 ||
		cfg.CallbackDelivery.Retention != 7*24*time.Hour || cfg.CallbackDelivery.BatchLimit != 100 ||
		cfg.CallbackController.RetentionInterval != time.Hour ||
		cfg.CallbackController.DrainGrace != 15*time.Second || cfg.CallbackRequestTimeout != 10*time.Second ||
		cfg.CallbackDNSTimeout != 5*time.Second {
		t.Fatalf("callback defaults: %#v", cfg)
	}
}

func TestLoadDaemonConfigCallbackSigningIsCombinedOnlyAndBounded(t *testing.T) {
	setServeConfig(t)
	t.Setenv("CALLBACK_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("CALLBACK_SIGNING_KEY_ID", "installation/callback")
	t.Setenv("CALLBACK_SIGNING_KEY_VERSION", "7")
	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("load callback config: %v", err)
	}
	if cfg.CallbackSigningKeyID != "installation/callback" || cfg.CallbackSigningVersion != 7 {
		t.Fatalf("callback identity = %#v", cfg)
	}

	t.Setenv("CALLBACK_DRAIN_GRACE", "40s")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "CALLBACK_DRAIN_GRACE") {
		t.Fatalf("callback drain error = %v", err)
	}
	t.Setenv("CALLBACK_DRAIN_GRACE", "15s")

	t.Setenv("CALLBACK_REQUEST_TIMEOUT", "30s")
	t.Setenv("CALLBACK_LEASE_DURATION", "30s")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "less than CALLBACK_LEASE_DURATION") {
		t.Fatalf("callback timeout error = %v", err)
	}

	t.Setenv("CALLBACK_REQUEST_TIMEOUT", "10s")
	t.Setenv("NVOKEN_PROCESS_ROLE", "executor")
	t.Setenv("DATABASE_MAX_CONNS", "1")
	t.Setenv("RUNTIME_API_KEY", "")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "available only to the combined role") {
		t.Fatalf("executor callback secret error = %v", err)
	}
}

func TestLoadDaemonConfigRejectsInvalidRetentionSettings(t *testing.T) {
	t.Run("dispatch retention", func(t *testing.T) {
		setServeConfig(t)
		t.Setenv("DISPATCH_RETENTION", "5m")
		if _, err := loadDaemonConfig(); err == nil ||
			!strings.Contains(err.Error(), "dispatch retention must exceed stale age") {
			t.Fatalf("dispatch retention error = %v", err)
		}
	})

	t.Run("dispatch retention interval", func(t *testing.T) {
		setServeConfig(t)
		t.Setenv("DISPATCH_RETENTION_INTERVAL", "0s")
		if _, err := loadDaemonConfig(); err == nil ||
			!strings.Contains(err.Error(), "dispatch retention interval must be positive") {
			t.Fatalf("dispatch retention interval error = %v", err)
		}
	})

	t.Run("enabled callback retention", func(t *testing.T) {
		setServeConfig(t)
		t.Setenv("CALLBACK_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
		t.Setenv("CALLBACK_RETENTION", "0s")
		if _, err := loadDaemonConfig(); err == nil ||
			!strings.Contains(err.Error(), "callback retention must be positive") {
			t.Fatalf("callback retention error = %v", err)
		}
	})

	t.Run("enabled callback retention interval", func(t *testing.T) {
		setServeConfig(t)
		t.Setenv("CALLBACK_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
		t.Setenv("CALLBACK_RETENTION_INTERVAL", "0s")
		if _, err := loadDaemonConfig(); err == nil ||
			!strings.Contains(err.Error(), "callback retention interval must be positive") {
			t.Fatalf("callback retention interval error = %v", err)
		}
	})

	t.Run("disabled callback settings are inert", func(t *testing.T) {
		setServeConfig(t)
		t.Setenv("CALLBACK_RETENTION", "0s")
		t.Setenv("CALLBACK_RETENTION_INTERVAL", "0s")
		if _, err := loadDaemonConfig(); err != nil {
			t.Fatalf("disabled callback retention settings: %v", err)
		}
	})
}

func TestLoadDaemonConfigExecutorDoesNotRequirePublicRuntimeSecrets(t *testing.T) {
	t.Setenv("NVOKEN_PROCESS_ROLE", "executor")
	t.Setenv("DATABASE_URL", "postgres://nvoken:secret@localhost/nvoken")
	t.Setenv("DATABASE_MAX_CONNS", "1")
	t.Setenv("RUNTIME_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("load executor config: %v", err)
	}
	if cfg.ProcessRole != "executor" || cfg.DatabaseMaxConns != 1 || cfg.RuntimeAPIKey != "" {
		t.Fatalf("executor config = %#v", cfg)
	}
}

func TestLoadDaemonConfigEmbeddedCombinedIgnoresExecutorRequestTimeouts(t *testing.T) {
	setServeConfig(t)
	t.Setenv("EXECUTOR_ATTEMPT_TIMEOUT", "45m")
	t.Setenv("CLOUD_TASKS_DISPATCH_DEADLINE", "30m")
	if _, err := loadDaemonConfig(); err != nil {
		t.Fatalf("embedded combined config: %v", err)
	}
}

func TestLoadDaemonConfigRequiresCompleteCloudTasksIdentity(t *testing.T) {
	setServeConfig(t)
	t.Setenv("CLOUD_TASKS_QUEUE", "projects/test/locations/us-central1/queues/execution")
	_, err := loadDaemonConfig()
	if err == nil || !strings.Contains(err.Error(), "must be configured together") {
		t.Fatalf("partial Cloud Tasks error = %v", err)
	}
}

func TestLoadDaemonConfigCloudTasksModeRequiresIdentityAndEnablesRepair(t *testing.T) {
	setServeConfig(t)
	t.Setenv("INVOCATION_EXECUTION_MODE", "cloud_tasks")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "requires complete Cloud Tasks") {
		t.Fatalf("missing Cloud Tasks mode error = %v", err)
	}

	t.Setenv("CLOUD_TASKS_QUEUE", "projects/test/locations/us-central1/queues/execution")
	t.Setenv("CLOUD_TASKS_EXECUTOR_URL", "https://executor.example.run.app")
	t.Setenv("CLOUD_TASKS_OIDC_SERVICE_ACCOUNT", "task-caller@example-project.iam.gserviceaccount.com")
	t.Setenv("CLOUD_TASKS_OIDC_AUDIENCE", "https://executor.example.run.app")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "requires REDIS_URL") {
		t.Fatalf("missing Redis mode error = %v", err)
	}
	t.Setenv("REDIS_URL", "redis://10.0.0.2:6379")
	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("load Cloud Tasks mode: %v", err)
	}
	if cfg.InvocationExecutionMode != "cloud_tasks" || !cfg.DispatchController.RepairInvocations {
		t.Fatalf("Cloud Tasks config = %#v", cfg)
	}
}

func TestLoadDaemonConfigCloudExecutorRequiresCapacityTimeoutAndProvider(t *testing.T) {
	t.Setenv("NVOKEN_PROCESS_ROLE", "executor")
	t.Setenv("INVOCATION_EXECUTION_MODE", "cloud_tasks")
	t.Setenv("DATABASE_URL", "postgres://nvoken:secret@localhost/nvoken")
	t.Setenv("DATABASE_MAX_CONNS", "1")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-secret")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("executor connection reserve error = %v", err)
	}

	t.Setenv("DATABASE_MAX_CONNS", "2")
	t.Setenv("REDIS_URL", "redis://10.0.0.2:6379")
	t.Setenv("ENGINE_EXECUTION_SEGMENT_CEILING", "10m")
	t.Setenv("EXECUTOR_ATTEMPT_TIMEOUT", "9m")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("executor segment nesting error = %v", err)
	}

	t.Setenv("EXECUTOR_ATTEMPT_TIMEOUT", "11m")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "Invocation-generating role") {
		t.Fatalf("executor provider error = %v", err)
	}
}

func TestLoadDaemonConfigRejectsInvalidInvocationExecutionMode(t *testing.T) {
	setServeConfig(t)
	t.Setenv("INVOCATION_EXECUTION_MODE", "automatic")
	_, err := loadDaemonConfig()
	if err == nil || !strings.Contains(err.Error(), "embedded or cloud_tasks") {
		t.Fatalf("execution mode error = %v", err)
	}
}

func TestLoadMigrationConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://nvoken:secret@localhost/nvoken")
	t.Setenv("MIGRATION_TIMEOUT", "45s")

	cfg, err := loadMigrationConfig()
	if err != nil {
		t.Fatalf("loadMigrationConfig: %v", err)
	}
	if cfg.DatabaseURL != "postgres://nvoken:secret@localhost/nvoken" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.Timeout != 45*time.Second {
		t.Errorf("Timeout = %s, want 45s", cfg.Timeout)
	}
}

func TestLoadMigrationConfigRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := loadMigrationConfig()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("loadMigrationConfig error = %v", err)
	}
}

func TestLoadDaemonConfigFromEnv(t *testing.T) {
	setServeConfig(t)
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_MAX_CONNS", "17")
	t.Setenv("RUNTIME_TENANT_REF", "tenant-acme")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-secret")
	t.Setenv("OPENAI_API_KEY", "openai-secret")
	t.Setenv("ENGINE_CONCURRENCY", "3")
	t.Setenv("ENGINE_POLL_INTERVAL", "250ms")
	t.Setenv("SHUTDOWN_TIMEOUT", "8s")
	t.Setenv("ENGINE_DRAIN_GRACE", "7s")
	t.Setenv("CALLBACK_DRAIN_GRACE", "7s")
	t.Setenv("REDIS_URL", "rediss://10.0.0.2:6378/0")
	t.Setenv("REDIS_PASSWORD", "redis-secret")
	t.Setenv("REDIS_CA_CERT", "test-ca")
	t.Setenv("LIVE_EVENT_BUFFER", "12")
	t.Setenv("STREAM_POLL_INTERVAL", "200ms")
	t.Setenv("STREAM_KEEPALIVE_INTERVAL", "3s")
	t.Setenv("STREAM_MAX_LIFETIME", "4m")
	t.Setenv("STREAM_WRITE_TIMEOUT", "2s")

	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("loadDaemonConfig: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "9090")
	}
	if cfg.DatabaseMaxConns != 17 || cfg.RuntimeTenantConstraint == nil || *cfg.RuntimeTenantConstraint != "tenant-acme" {
		t.Fatalf("daemon config = %#v", cfg)
	}
	if cfg.AnthropicAPIKey != "anthropic-secret" || cfg.OpenAIAPIKey != "openai-secret" ||
		cfg.Engine.Concurrency != 3 || cfg.Engine.PollInterval != 250*time.Millisecond ||
		cfg.ShutdownTimeout != 8*time.Second || cfg.Engine.DrainGrace != 7*time.Second ||
		cfg.CallbackController.DrainGrace != 7*time.Second ||
		cfg.RedisURL != "rediss://10.0.0.2:6378/0" || cfg.RedisPassword != "redis-secret" ||
		cfg.RedisCACertificate != "test-ca" ||
		cfg.LiveEventBuffer != 12 || cfg.Stream.PollInterval != 200*time.Millisecond ||
		cfg.Stream.KeepaliveInterval != 3*time.Second || cfg.Stream.MaxLifetime != 4*time.Minute ||
		cfg.Stream.WriteTimeout != 2*time.Second {
		t.Fatalf("generation config = %#v", cfg)
	}
}

func TestLoadDaemonConfigRejectsInvalidStreamBounds(t *testing.T) {
	setServeConfig(t)
	t.Setenv("LIVE_EVENT_BUFFER", "0")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "LIVE_EVENT_BUFFER") {
		t.Fatalf("live-event buffer error = %v", err)
	}
	t.Setenv("LIVE_EVENT_BUFFER", "1")
	t.Setenv("STREAM_MAX_LIFETIME", "5s")
	t.Setenv("STREAM_WRITE_TIMEOUT", "5s")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "less than") {
		t.Fatalf("stream lifetime error = %v", err)
	}
}

func TestLoadDaemonConfigRejectsDrainOutsideShutdownBudget(t *testing.T) {
	setServeConfig(t)
	t.Setenv("SHUTDOWN_TIMEOUT", "8s")
	t.Setenv("ENGINE_DRAIN_GRACE", "8s")

	_, err := loadDaemonConfig()
	if err == nil || !strings.Contains(err.Error(), "leave at least 1s") {
		t.Fatalf("shutdown budget error = %v", err)
	}
}

func TestLoadDaemonConfigRejectsInvalidEngineConfiguration(t *testing.T) {
	setServeConfig(t)
	t.Setenv("ENGINE_LEASE_DURATION", "10s")
	t.Setenv("ENGINE_HEARTBEAT_INTERVAL", "5s")

	_, err := loadDaemonConfig()
	if err == nil || !strings.Contains(err.Error(), "heartbeat interval") {
		t.Fatalf("invalid engine error = %v", err)
	}
}

func TestLoadDaemonConfigRequiresRuntimeDependencies(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("RUNTIME_API_KEY", "")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("missing database error = %v", err)
	}

	t.Setenv("DATABASE_URL", "postgres://localhost/nvoken")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "RUNTIME_API_KEY") {
		t.Fatalf("missing runtime key error = %v", err)
	}

	t.Setenv("RUNTIME_API_KEY", "short")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("short runtime key error = %v", err)
	}

	t.Setenv("RUNTIME_API_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("RUNTIME_TENANT_REF", strings.Repeat("界", 256))
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "255 Unicode characters") {
		t.Fatalf("long tenant constraint error = %v", err)
	}
}

func TestLoadDaemonConfigReservesDatabaseConnectionForCancellation(t *testing.T) {
	setServeConfig(t)
	t.Setenv("DATABASE_MAX_CONNS", "1")
	if _, err := loadDaemonConfig(); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("database connection error = %v", err)
	}
}

func setServeConfig(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://nvoken:secret@localhost/nvoken")
	t.Setenv("RUNTIME_API_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-secret")
}
