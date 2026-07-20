// Package daemon wires adapters to services and runs the nvoken server.
package daemon

import (
	"context"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpapi"
)

type Config struct {
	// Port is the listen port for the HTTP API.
	Port string
}

// Run starts the server and blocks until ctx is cancelled or the server
// fails.
func Run(ctx context.Context, cfg Config) error {
	srv := httpapi.NewServer(httpapi.Config{Addr: ":" + cfg.Port})
	return srv.Run(ctx)
}
