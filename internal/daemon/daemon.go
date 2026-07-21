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
	"github.com/deepnoodle-ai/nvoken/internal/adapters/cloudtasks"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/divegen"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/executorhttp"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpapi"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/worksignal"
	dispatchruntime "github.com/deepnoodle-ai/nvoken/internal/dispatch"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type ProcessRole string

const (
	ProcessRoleCombined ProcessRole = "combined"
	ProcessRoleExecutor ProcessRole = "executor"
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
	ProcessRole             ProcessRole
	InvocationExecutionMode services.InvocationExecutionMode
	Engine                  engine.Config
	Budgets                 services.BudgetPolicy
	Dispatch                services.DispatchConfig
	DispatchController      dispatchruntime.ControllerConfig
	CloudTasks              cloudtasks.Config
	ExecutorAttemptTimeout  time.Duration
}

type component interface {
	Run(context.Context) error
}

type runtimeTopology struct {
	privateExecutor bool
	publicAPI       bool
	embeddedRunner  bool
	reaper          bool
	dispatchControl bool
}

func resolveRuntimeTopology(role ProcessRole, mode services.InvocationExecutionMode, cloudTasksConfigured bool) (runtimeTopology, error) {
	switch role {
	case ProcessRoleExecutor:
		return runtimeTopology{privateExecutor: true}, nil
	case ProcessRoleCombined:
		topology := runtimeTopology{publicAPI: true, dispatchControl: cloudTasksConfigured}
		switch mode {
		case services.InvocationExecutionEmbedded:
			topology.embeddedRunner = true
		case services.InvocationExecutionCloudTasks:
			if !cloudTasksConfigured {
				return runtimeTopology{}, fmt.Errorf("cloud_tasks Invocation execution requires Cloud Tasks configuration")
			}
			topology.reaper = true
		default:
			return runtimeTopology{}, fmt.Errorf("unsupported Invocation execution mode %q", mode)
		}
		return topology, nil
	default:
		return runtimeTopology{}, fmt.Errorf("unsupported process role %q", role)
	}
}

// Run starts the server and blocks until ctx is cancelled or the server
// fails.
func Run(ctx context.Context, cfg Config) error {
	if cfg.ProcessRole == "" {
		cfg.ProcessRole = ProcessRoleCombined
	}
	if cfg.Dispatch.Queue == "" {
		cfg.Dispatch = services.DefaultDispatchConfig()
	}
	if cfg.DispatchController.PublishInterval == 0 {
		repairInvocations := cfg.DispatchController.RepairInvocations
		cfg.DispatchController = dispatchruntime.DefaultControllerConfig()
		cfg.DispatchController.RepairInvocations = repairInvocations
	}
	if cfg.InvocationExecutionMode == "" {
		cfg.InvocationExecutionMode = services.InvocationExecutionEmbedded
	}
	if cfg.InvocationExecutionMode != services.InvocationExecutionEmbedded && cfg.InvocationExecutionMode != services.InvocationExecutionCloudTasks {
		return fmt.Errorf("unsupported Invocation execution mode %q", cfg.InvocationExecutionMode)
	}
	topology, err := resolveRuntimeTopology(cfg.ProcessRole, cfg.InvocationExecutionMode, cfg.CloudTasks.Queue != "")
	if err != nil {
		return err
	}
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
	dispatchService, err := services.NewDispatchService(store, txm, clock, ids, cfg.Dispatch, slog.Default())
	if err != nil {
		return fmt.Errorf("configure execution dispatch service: %w", err)
	}
	if topology.privateExecutor {
		generator := divegen.New(divegen.Config{
			AnthropicAPIKey: cfg.AnthropicAPIKey, OpenAIAPIKey: cfg.OpenAIAPIKey,
		})
		invocationExecutor := services.NewGenerationExecutor(store, generator, slog.Default())
		ownership := services.NewInvocationExecutionService(store, txm, clock, ids,
			services.WithExecutionSegmentCeiling(cfg.Engine.ExecutionSegmentCeiling))
		owner, err := executionOwner()
		if err != nil {
			return fmt.Errorf("create executor owner: %w", err)
		}
		cancellations := worksignal.NewPostgresCancellation(pool)
		attempts, err := dispatchruntime.NewAttemptService(
			dispatchService, ownership, invocationExecutor, store, txm, clock,
			owner, cfg.Engine, cancellations, slog.Default(),
		)
		if err != nil {
			return fmt.Errorf("configure request-bound Invocation attempts: %w", err)
		}
		srv, err := executorhttp.NewServer(executorhttp.Config{
			Addr: ":" + cfg.Port, Attempts: attempts, Logger: slog.Default(),
			AttemptTimeout:  cfg.ExecutorAttemptTimeout,
			ShutdownTimeout: cfg.ShutdownTimeout - time.Second,
		})
		if err != nil {
			return fmt.Errorf("configure private executor: %w", err)
		}
		joined, err := runComponents(ctx, cfg.ShutdownTimeout, srv, attempts)
		if !joined {
			closePool = false
			slog.Warn("executor shutdown budget expired before components joined",
				"shutdown_timeout_ms", cfg.ShutdownTimeout.Milliseconds())
		}
		return err
	}
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
		services.WithBudgetPolicy(cfg.Budgets), services.WithRuntimeLogger(slog.Default()),
		services.WithInvocationExecutionMode(cfg.InvocationExecutionMode, cfg.Dispatch.Queue))
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids,
		services.WithExecutionSegmentCeiling(cfg.Engine.ExecutionSegmentCeiling))
	srv := httpapi.NewServer(httpapi.Config{
		Addr: ":" + cfg.Port, Authenticator: authenticator, Runtime: runtime,
		// Leave the supervisor one second to observe component completion and
		// close the database pool inside the total process budget.
		ShutdownTimeout: cfg.ShutdownTimeout - time.Second,
	})
	components := []component{srv}
	if topology.embeddedRunner {
		generator := divegen.New(divegen.Config{
			AnthropicAPIKey: cfg.AnthropicAPIKey, OpenAIAPIKey: cfg.OpenAIAPIKey,
		})
		executor := services.NewGenerationExecutor(store, generator, slog.Default())
		owner, err := executionOwner()
		if err != nil {
			return fmt.Errorf("create execution owner: %w", err)
		}
		runner, err := engine.NewRunner(owner, ownership, executor, signaller, slog.Default(), cfg.Engine,
			engine.WithCancellationSignaller(cancellations))
		if err != nil {
			return fmt.Errorf("configure Invocation engine: %w", err)
		}
		components = append(components, runner)
	} else if topology.reaper {
		reaper, err := engine.NewReaper(ownership, cfg.Engine.ReaperInterval, cfg.Engine.ReaperBatchLimit, slog.Default())
		if err != nil {
			return fmt.Errorf("configure Invocation reaper: %w", err)
		}
		components = append(components, reaper)
	}
	if topology.dispatchControl {
		tasks, err := cloudtasks.New(ctx, cfg.CloudTasks)
		if err != nil {
			return fmt.Errorf("configure Cloud Tasks: %w", err)
		}
		defer func() {
			if err := tasks.Close(); err != nil {
				slog.Warn("close Cloud Tasks client", "error", err)
			}
		}()
		publisherOwner, err := executionOwner()
		if err != nil {
			return fmt.Errorf("create dispatch publisher owner: %w", err)
		}
		controller, err := dispatchruntime.NewController(publisherOwner, dispatchService, tasks, slog.Default(), cfg.DispatchController)
		if err != nil {
			return fmt.Errorf("configure execution dispatch controller: %w", err)
		}
		components = append(components, controller)
	}
	joined, err := runComponents(ctx, cfg.ShutdownTimeout, components...)
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
