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

	"github.com/deepnoodle-ai/nvoken/internal/adapters/callbackhttp"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/cloudtasks"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/divegen"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/executorhttp"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/funding"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpapi"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/httpguard"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/liveevents"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/worksignal"
	callbackruntime "github.com/deepnoodle-ai/nvoken/internal/callback"
	dispatchruntime "github.com/deepnoodle-ai/nvoken/internal/dispatch"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/observability"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type ProcessRole string

const (
	ProcessRoleCombined ProcessRole = "combined"
	ProcessRoleExecutor ProcessRole = "executor"
)

type Config struct {
	BuildVersion string
	// Port is the listen port for the HTTP API.
	Port                    string
	DatabaseURL             string
	DatabaseMaxConns        int32
	RuntimeAPIKey           string
	RuntimeTenantConstraint *string
	BootstrapOwnerSecret    string
	CredentialDeliveryKey   []byte
	PublicBaseURL           string
	TrustForwardedClientIP  bool
	AnthropicAPIKey         string
	OpenAIAPIKey            string
	CredentialPolicy        services.ProviderCredentialPolicy
	CredentialCipher        ports.CredentialCipher
	CredentialCleanupGrace  time.Duration
	PlatformAnthropicAPIKey string
	PlatformOpenAIAPIKey    string
	PlatformFundingEnabled  bool
	ShutdownTimeout         time.Duration
	DiagnosticTimeout       time.Duration
	ProcessRole             ProcessRole
	InvocationExecutionMode services.InvocationExecutionMode
	Engine                  engine.Config
	Budgets                 services.BudgetPolicy
	Dispatch                services.DispatchConfig
	DispatchController      dispatchruntime.ControllerConfig
	CloudTasks              cloudtasks.Config
	ExecutorAttemptTimeout  time.Duration
	RedisURL                string
	RedisPassword           string
	RedisCACertificate      string
	LiveEventBuffer         int
	Stream                  httpapi.StreamConfig
	CallbackSigningKey      string
	CallbackSigningKeyID    string
	CallbackSigningVersion  int64
	CallbackRequestTimeout  time.Duration
	CallbackDNSTimeout      time.Duration
	CallbackConnectTimeout  time.Duration
	CallbackTLSTimeout      time.Duration
	CallbackDelivery        services.CallbackDeliveryConfig
	CallbackController      callbackruntime.Config
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
	if cfg.CredentialPolicy.DeploymentMode == "" {
		cfg.CredentialPolicy = services.ProviderCredentialPolicy{
			DeploymentMode: services.CredentialDeploymentSelfHosted,
			DefaultSource:  domain.ProviderCredentialSourceInstallationBYOK,
		}
	}
	if cfg.CredentialCleanupGrace == 0 {
		cfg.CredentialCleanupGrace = 5 * time.Minute
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
		logProcessStartFailure("database_connectivity", observability.ErrorClass(err))
		return fmt.Errorf("open runtime database: %w", err)
	}
	closePool := true
	defer func() {
		if closePool {
			pool.Close()
		}
	}()
	schemaStatus, err := postgres.InspectSchema(ctx, pool)
	if err != nil {
		logProcessStartFailure("database_schema", observability.ErrorClass(err))
		return fmt.Errorf("inspect runtime database schema: %w", err)
	}
	if err := schemaStatus.CompatibilityError(); err != nil {
		logSchemaProcessStartFailure(schemaStatus)
		return fmt.Errorf("check runtime database schema: %w", err)
	}

	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	store := postgres.NewStore(pool)
	txm := postgres.NewTransactionManager(pool)
	toolCoordinator := services.NewToolCheckpointService(store, txm, clock, ids)
	credentialResolver := services.NewProviderCredentialResolver(
		store,
		cfg.CredentialCipher,
		clock,
		services.CredentialResolverConfig{
			DeploymentMode: cfg.CredentialPolicy.DeploymentMode,
			InstallationAPIKeys: map[string]string{
				string(domain.ModelProviderAnthropic): cfg.AnthropicAPIKey,
				string(domain.ModelProviderOpenAI):    cfg.OpenAIAPIKey,
			},
			PlatformAPIKeys: map[string]string{
				string(domain.ModelProviderAnthropic): cfg.PlatformAnthropicAPIKey,
				string(domain.ModelProviderOpenAI):    cfg.PlatformOpenAIAPIKey,
			},
		},
		funding.StaticGate{Allowed: cfg.PlatformFundingEnabled},
	)
	var liveBus interface {
		ports.LiveEventBus
		Close() error
	}
	if cfg.RedisURL != "" {
		liveBus, err = liveevents.NewRedisURL(
			cfg.RedisURL, cfg.RedisPassword, cfg.RedisCACertificate, cfg.LiveEventBuffer, slog.Default(),
		)
		if err != nil {
			logProcessStartFailure("live_event_fanout", "invalid_configuration")
			return fmt.Errorf("configure Redis live-event fan-out: %w", err)
		}
		defer func() {
			if err := liveBus.Close(); err != nil {
				slog.Warn("close live-event fan-out",
					"event", observability.EventProcessFailed,
					"component", "live_event_fanout",
					"error_class", observability.ErrorClass(err))
			}
		}()
	} else {
		inProcess := liveevents.NewInProcess(cfg.LiveEventBuffer)
		liveBus = &inProcessLiveBus{LiveEventBus: inProcess}
	}
	dispatchService, err := services.NewDispatchService(store, txm, clock, ids, cfg.Dispatch, slog.Default())
	if err != nil {
		return fmt.Errorf("configure execution dispatch service: %w", err)
	}
	if topology.privateExecutor {
		generator := divegen.New(divegen.Config{
			AnthropicAPIKey: cfg.AnthropicAPIKey,
			OpenAIAPIKey:    cfg.OpenAIAPIKey,
		},
			divegen.WithToolCoordinator(toolCoordinator),
			divegen.WithLogger(slog.Default()),
			divegen.WithCredentialResolver(credentialResolver),
		)
		invocationExecutor := services.NewGenerationExecutor(
			store,
			generator,
			slog.Default(),
			services.WithGenerationLiveEvents(liveBus),
			services.WithGenerationClock(clock),
		)
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
		logProcessStarted(cfg, schemaStatus)
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
	authenticator, err := services.NewIdentityService(store, store, txm, clock, ids, services.IdentityConfig{
		AccountID:             account.ID,
		VerificationBaseURL:   cfg.PublicBaseURL,
		DeliveryEncryptionKey: cfg.CredentialDeliveryKey,
		BootstrapOwnerSecret:  cfg.BootstrapOwnerSecret,
	})
	if err != nil {
		return fmt.Errorf("configure runtime authentication: %w", err)
	}
	if _, err := authenticator.BootstrapOwner(ctx); err != nil {
		return fmt.Errorf("bootstrap installation Owner: %w", err)
	}
	if _, err := authenticator.ImportStaticRuntimeCredential(ctx, cfg.RuntimeAPIKey, cfg.RuntimeTenantConstraint); err != nil {
		return fmt.Errorf("import configured Runtime credential: %w", err)
	}
	signaller := worksignal.NewInProcess()
	cancellations := worksignal.NewPostgresCancellation(pool)
	runtime := services.NewRuntimeService(store, txm, clock, ids,
		services.WithWorkSignaller(signaller), services.WithCancellationSignaller(cancellations),
		services.WithBudgetPolicy(cfg.Budgets), services.WithRuntimeLogger(slog.Default()),
		services.WithInvocationExecutionMode(cfg.InvocationExecutionMode, cfg.Dispatch.Queue),
		services.WithCallbackTools(cfg.CallbackSigningKey != ""),
		services.WithProviderCredentialPolicy(cfg.CredentialPolicy, cfg.CredentialCipher, cfg.CredentialCleanupGrace))
	providerCredentials := services.NewProviderCredentialService(store, txm, clock, ids, cfg.CredentialCipher)
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids,
		services.WithExecutionSegmentCeiling(cfg.Engine.ExecutionSegmentCeiling))
	srv := httpapi.NewServer(httpapi.Config{
		Addr:                   ":" + cfg.Port,
		Authenticator:          authenticator,
		Runtime:                runtime,
		Identity:               authenticator,
		ProviderCredentials:    providerCredentials,
		LiveEvents:             liveBus,
		Stream:                 cfg.Stream,
		TrustForwardedClientIP: cfg.TrustForwardedClientIP,
		// Leave the supervisor one second to observe component completion and
		// close the database pool inside the total process budget.
		ShutdownTimeout: cfg.ShutdownTimeout - time.Second,
	})
	components := []component{srv}
	if topology.embeddedRunner {
		generator := divegen.New(divegen.Config{
			AnthropicAPIKey: cfg.AnthropicAPIKey,
			OpenAIAPIKey:    cfg.OpenAIAPIKey,
		},
			divegen.WithToolCoordinator(toolCoordinator),
			divegen.WithLogger(slog.Default()),
			divegen.WithCredentialResolver(credentialResolver),
		)
		executor := services.NewGenerationExecutor(
			store,
			generator,
			slog.Default(),
			services.WithGenerationLiveEvents(liveBus),
			services.WithGenerationClock(clock),
		)
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
				slog.Warn("close Cloud Tasks client",
					"event", observability.EventProcessFailed,
					"component", "cloud_tasks_client",
					"error_class", observability.ErrorClass(err))
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
	if cfg.CallbackSigningKey != "" {
		callbackService, err := services.NewCallbackDeliveryService(
			store,
			txm,
			clock,
			ids,
			signaller,
			cfg.CallbackDelivery,
			slog.Default(),
		)
		if err != nil {
			return fmt.Errorf("configure callback delivery service: %w", err)
		}
		transport, err := callbackhttp.New(callbackhttp.Config{
			SigningKey:     []byte(cfg.CallbackSigningKey),
			SigningKeyID:   cfg.CallbackSigningKeyID,
			SigningVersion: cfg.CallbackSigningVersion,
			RequestTimeout: cfg.CallbackRequestTimeout,
			Client: httpguard.NewPublicClient(
				cfg.CallbackDNSTimeout,
				cfg.CallbackConnectTimeout,
				cfg.CallbackTLSTimeout,
			),
		})
		if err != nil {
			return fmt.Errorf("configure callback HTTP transport: %w", err)
		}
		callbackOwner, err := executionOwner()
		if err != nil {
			return fmt.Errorf("create callback delivery owner: %w", err)
		}
		controller, err := callbackruntime.NewController(
			callbackOwner,
			callbackService,
			transport,
			slog.Default(),
			cfg.CallbackController,
		)
		if err != nil {
			return fmt.Errorf("configure callback delivery controller: %w", err)
		}
		components = append(components, controller)
	}
	logProcessStarted(cfg, schemaStatus)
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

func logProcessStarted(cfg Config, schemaStatus postgres.SchemaStatus) {
	version := cfg.BuildVersion
	if version == "" {
		version = "devel"
	}
	liveEventMode := "in_process"
	if cfg.RedisURL != "" {
		liveEventMode = "redis"
	}
	reusableCredentials := cfg.CredentialCipher != nil
	slog.Info("nvokend process started",
		"event", observability.EventProcessStarted,
		"build_version", version,
		"schema_version", schemaStatus.Expected,
		"database_schema_version", schemaStatus.Current,
		"minimum_binary_schema_version", schemaStatus.MinimumBinarySchemaVersion,
		"schema_compatibility", schemaStatus.State,
		"process_role", cfg.ProcessRole,
		"execution_mode", cfg.InvocationExecutionMode,
		"anthropic_enabled", cfg.AnthropicAPIKey != "" || cfg.PlatformAnthropicAPIKey != "" || reusableCredentials,
		"openai_enabled", cfg.OpenAIAPIKey != "" || cfg.PlatformOpenAIAPIKey != "" || reusableCredentials,
		"callback_enabled", cfg.CallbackSigningKey != "",
		"cloud_tasks_enabled", cfg.CloudTasks.Queue != "",
		"live_event_mode", liveEventMode)
}

func logProcessStartFailure(check, errorClass string) {
	slog.Error("process startup check failed",
		"event", observability.EventProcessStartFailed,
		"check", check,
		"error_class", errorClass)
}

func logSchemaProcessStartFailure(status postgres.SchemaStatus) {
	slog.Error("process startup check failed",
		"event", observability.EventProcessStartFailed,
		"check", "database_schema",
		"error_class", status.State,
		"schema_version", status.Current,
		"expected_schema_version", status.Expected,
		"minimum_binary_schema_version", status.MinimumBinarySchemaVersion,
		"compatibility_schema_version", status.CompatibilitySchemaVersion,
		"dirty", status.Dirty)
}

type inProcessLiveBus struct {
	ports.LiveEventBus
}

func (*inProcessLiveBus) Close() error { return nil }

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
