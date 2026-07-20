package main

import (
	"fmt"

	"github.com/deepnoodle-ai/wonton/env"

	"github.com/deepnoodle-ai/nvoken/internal/daemon"
)

type config struct {
	Port string `env:"PORT" envDefault:"8080"`
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
