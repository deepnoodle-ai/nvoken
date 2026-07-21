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
	"time"

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
	ShutdownTimeout         time.Duration
	Engine                  engine.Config
	Budgets                 services.BudgetPolicy
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
	closePool := true
	defer func() {
		if closePool {
			pool.Close()
		}
	}()
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
	cancellations := worksignal.NewPostgresCancellation(pool)
	runtime := services.NewRuntimeService(store, txm, clock, ids,
		services.WithWorkSignaller(signaller), services.WithCancellationSignaller(cancellations),
		services.WithBudgetPolicy(cfg.Budgets), services.WithRuntimeLogger(slog.Default()))
	generator := divegen.New(divegen.Config{
		AnthropicAPIKey: cfg.AnthropicAPIKey, OpenAIAPIKey: cfg.OpenAIAPIKey,
	})
	executor := services.NewGenerationExecutor(store, generator, slog.Default())
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids,
		services.WithExecutionSegmentCeiling(cfg.Engine.ExecutionSegmentCeiling))
	owner, err := executionOwner()
	if err != nil {
		return fmt.Errorf("create execution owner: %w", err)
	}
	runner, err := engine.NewRunner(owner, ownership, executor, signaller, slog.Default(), cfg.Engine,
		engine.WithCancellationSignaller(cancellations))
	if err != nil {
		return fmt.Errorf("configure Invocation engine: %w", err)
	}
	srv := httpapi.NewServer(httpapi.Config{
		Addr: ":" + cfg.Port, Authenticator: authenticator, Runtime: runtime,
		// Leave the supervisor one second to observe component completion and
		// close the database pool inside the total process budget.
		ShutdownTimeout: cfg.ShutdownTimeout - time.Second,
	})
	joined, err := runComponents(ctx, cfg.ShutdownTimeout, srv, runner)
	if !joined {
		// An uncooperative component can still hold a pool connection. Do not
		// block past the process shutdown budget waiting for pgxpool.Close;
		// Cloud Run will reclaim process resources at termination.
		closePool = false
		slog.Warn("process shutdown budget expired before components joined",
			"shutdown_timeout_ms", cfg.ShutdownTimeout.Milliseconds())
	}
	return err
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

func runComponents(parent context.Context, shutdownTimeout time.Duration, components ...component) (bool, error) {
	if shutdownTimeout <= 0 {
		return true, fmt.Errorf("shutdown timeout must be positive")
	}
	if len(components) == 0 {
		return true, nil
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	type outcome struct{ err error }
	results := make(chan outcome, len(components))
	for _, current := range components {
		go func() { results <- outcome{err: current.Run(ctx)} }()
	}
	remaining := len(components)
	allErrors := make([]error, 0, remaining)
	parentDone := parent.Done()
	var shutdownTimer *time.Timer
	var shutdownDeadline <-chan time.Time
	beginShutdown := func() {
		if shutdownTimer != nil {
			return
		}
		cancel()
		shutdownTimer = time.NewTimer(shutdownTimeout)
		shutdownDeadline = shutdownTimer.C
	}
	defer func() {
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
	}()

	for remaining > 0 {
		select {
		case result := <-results:
			remaining--
			allErrors = append(allErrors, result.err)
			beginShutdown()
		case <-parentDone:
			parentDone = nil
			beginShutdown()
		case <-shutdownDeadline:
			if parent.Err() != nil {
				return false, nil
			}
			return false, fmt.Errorf("component shutdown exceeded %s", shutdownTimeout)
		}
	}
	if parent.Err() != nil {
		return true, nil
	}
	return true, errors.Join(allErrors...)
}
