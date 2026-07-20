package main

import "testing"

func TestLoadDaemonConfigDefaults(t *testing.T) {
	cfg, err := loadDaemonConfig()
	if err != nil {
		t.Fatalf("loadDaemonConfig: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "8080")
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
