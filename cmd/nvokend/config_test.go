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
	if cfg.Engine.Concurrency != 8 || cfg.Engine.PollInterval != time.Second ||
		cfg.Engine.LeaseDuration != 30*time.Second || cfg.Engine.HeartbeatInterval != 10*time.Second ||
		cfg.Engine.ReaperInterval != 10*time.Second || cfg.Engine.ReaperBatchLimit != 100 ||
		cfg.Engine.DrainGrace != 30*time.Second {
		t.Fatalf("Engine defaults: %#v", cfg.Engine)
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
		cfg.Engine.Concurrency != 3 || cfg.Engine.PollInterval != 250*time.Millisecond {
		t.Fatalf("generation config = %#v", cfg)
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

func setServeConfig(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://nvoken:secret@localhost/nvoken")
	t.Setenv("RUNTIME_API_KEY", "0123456789abcdef0123456789abcdef")
}
