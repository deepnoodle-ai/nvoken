// Package daemon wires adapters to services and runs the nvoken server.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/auth"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/divegen"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpapi"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/worksignal"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type Config struct {
	// Port is the listen port for the HTTP API.
	Port                    string
	DatabaseURL             string
	DatabaseMaxConns        int32
	RuntimeAPIKey           string
	RuntimeTenantConstraint *string
	AnthropicAPIKey         string
	OpenAIAPIKey            string
	Engine                  engine.Config
}

type component interface {
	Run(context.Context) error
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
	generator := divegen.New(divegen.Config{
		AnthropicAPIKey: cfg.AnthropicAPIKey, OpenAIAPIKey: cfg.OpenAIAPIKey,
	})
	executor := services.NewGenerationExecutor(store, generator, slog.Default())
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	owner, err := executionOwner()
	if err != nil {
		return fmt.Errorf("create execution owner: %w", err)
	}
	runner, err := engine.NewRunner(owner, ownership, executor, signaller, slog.Default(), cfg.Engine)
	if err != nil {
		return fmt.Errorf("configure Invocation engine: %w", err)
	}
	srv := httpapi.NewServer(httpapi.Config{
		Addr: ":" + cfg.Port, Authenticator: authenticator, Runtime: runtime,
	})
	return runComponents(ctx, srv, runner)
}

func executionOwner() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	tail := fmt.Sprintf(":%d:%s", os.Getpid(), hex.EncodeToString(suffix[:]))
	if maximum := services.MaxExecutionOwnerCharacters - len(tail); len(hostname) > maximum {
		hostname = hostname[:maximum]
	}
	return hostname + tail, nil
}

func runComponents(parent context.Context, components ...component) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	type outcome struct{ err error }
	results := make(chan outcome, len(components))
	for _, current := range components {
		go func() { results <- outcome{err: current.Run(ctx)} }()
	}
	first := <-results
	cancel()
	allErrors := []error{first.err}
	for range len(components) - 1 {
		allErrors = append(allErrors, (<-results).err)
	}
	if parent.Err() != nil {
		return nil
	}
	return errors.Join(allErrors...)
}
