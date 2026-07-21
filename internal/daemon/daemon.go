// Package daemon wires adapters to services and runs the nvoken server.
package daemon

import (
	"context"
	"fmt"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/auth"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpapi"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/worksignal"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type Config struct {
	// Port is the listen port for the HTTP API.
	Port                    string
	DatabaseURL             string
	DatabaseMaxConns        int32
	RuntimeAPIKey           string
	RuntimeTenantConstraint *string
}

// Run starts the server and blocks until ctx is cancelled or the server
// fails.
func Run(ctx context.Context, cfg Config) error {
	pool, err := postgres.OpenPoolWithConfig(ctx, cfg.DatabaseURL, postgres.PoolConfig{MaxConns: cfg.DatabaseMaxConns})
	if err != nil {
		return fmt.Errorf("open runtime database: %w", err)
	}
	defer pool.Close()
	if err := postgres.CheckSchema(ctx, pool); err != nil {
		return fmt.Errorf("check runtime database schema: %w", err)
	}

	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	store := postgres.NewStore(pool)
	txm := postgres.NewTransactionManager(pool)
	account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
	if err != nil {
		return fmt.Errorf("bootstrap installation: %w", err)
	}
	authenticator, err := auth.NewStaticAuthenticator(auth.StaticConfig{
		Token: cfg.RuntimeAPIKey, AccountID: account.ID, TenantConstraint: cfg.RuntimeTenantConstraint,
	})
	if err != nil {
		return fmt.Errorf("configure runtime authentication: %w", err)
	}
	signaller := worksignal.NewInProcess()
	runtime := services.NewRuntimeService(store, txm, clock, ids, services.WithWorkSignaller(signaller))
	srv := httpapi.NewServer(httpapi.Config{
		Addr: ":" + cfg.Port, Authenticator: authenticator, Runtime: runtime,
	})
	return srv.Run(ctx)
}
