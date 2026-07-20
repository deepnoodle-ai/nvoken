package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDaemonConfigDefaults(t *testing.T) {
	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("loadDaemonConfig: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "8080")
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
	t.Setenv("PORT", "9090")

	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("loadDaemonConfig: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "9090")
	}
}
