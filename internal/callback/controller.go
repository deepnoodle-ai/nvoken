package callback

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/observability"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type Config struct {
	Concurrency       int
	PollInterval      time.Duration
	RecoveryInterval  time.Duration
	RetentionInterval time.Duration
	DrainGrace        time.Duration
}

func DefaultConfig() Config {
	return Config{
		Concurrency:       4,
		PollInterval:      time.Second,
		RecoveryInterval:  10 * time.Second,
		RetentionInterval: time.Hour,
		DrainGrace:        15 * time.Second,
	}
}

func ValidateConfig(config Config) error {
	if config.Concurrency < 1 || config.Concurrency > 100 {
		return fmt.Errorf("callback concurrency must be between 1 and 100")
	}
	if config.PollInterval <= 0 {
		return fmt.Errorf("callback poll interval must be positive")
	}
	if config.RecoveryInterval <= 0 {
		return fmt.Errorf("callback recovery interval must be positive")
	}
	if config.RetentionInterval <= 0 {
		return fmt.Errorf("callback retention interval must be positive")
	}
	if config.DrainGrace <= 0 {
		return fmt.Errorf("callback drain grace must be positive")
	}
	return nil
}

type Controller struct {
	owner     string
	service   deliveryService
	transport ports.CallbackTransport
	logger    *slog.Logger
	config    Config
}

type deliveryService interface {
	ClaimNext(context.Context, string) (domain.CallbackDeliveryClaim, bool, error)
	ProcessClaim(context.Context, ports.CallbackTransport, domain.CallbackDeliveryClaim) error
	RecoverExpired(context.Context) (int64, error)
	Prune(context.Context) (int64, error)
}

func NewController(
	owner string,
	service deliveryService,
	transport ports.CallbackTransport,
	logger *slog.Logger,
	config Config,
) (*Controller, error) {
	if owner == "" || service == nil || transport == nil {
		return nil, fmt.Errorf("callback controller dependencies are required")
	}
	if err := ValidateConfig(config); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{
		owner:     owner,
		service:   service,
		transport: transport,
		logger:    logger,
		config:    config,
	}, nil
}

func (c *Controller) Run(ctx context.Context) error {
	executionCtx, cancelExecutions := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelExecutions()

	var workers sync.WaitGroup
	for index := 0; index < c.config.Concurrency; index++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			c.runWorker(ctx, executionCtx, workerOwner(c.owner, worker))
		}(index)
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		c.runMaintenance(ctx)
	}()
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}
	timer := time.NewTimer(c.config.DrainGrace)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		cancelExecutions()
		<-done
		return nil
	}
}

func workerOwner(base string, worker int) string {
	suffix := fmt.Sprintf(":callback:%d", worker)
	if len(base)+len(suffix) > services.MaxExecutionOwnerCharacters {
		base = base[:services.MaxExecutionOwnerCharacters-len(suffix)]
	}
	return base + suffix
}

func (c *Controller) runWorker(
	claimCtx context.Context,
	executionCtx context.Context,
	owner string,
) {
	ticker := time.NewTicker(c.config.PollInterval)
	defer ticker.Stop()
	for {
		for claimCtx.Err() == nil {
			claim, found, err := c.service.ClaimNext(claimCtx, owner)
			if err != nil {
				c.logger.Warn("Callback delivery claim failed",
					"event", observability.EventCallbackClaimFailed,
					"error_class", observability.ErrorClass(err))
				break
			}
			if !found {
				break
			}
			if err := c.service.ProcessClaim(executionCtx, c.transport, claim); err != nil && claimCtx.Err() == nil {
				c.logger.Warn(
					"Callback delivery processing failed",
					"event",
					observability.EventCallbackProcessFailed,
					"error_class",
					observability.ErrorClass(err),
					"delivery_id",
					claim.Delivery.ID,
					"tool_call_id",
					claim.Delivery.ToolCallID,
					"attempt",
					claim.Attempt,
				)
			}
		}
		select {
		case <-claimCtx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Controller) runMaintenance(ctx context.Context) {
	recovery := time.NewTicker(c.config.RecoveryInterval)
	retention := time.NewTicker(c.config.RetentionInterval)
	defer recovery.Stop()
	defer retention.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-recovery.C:
			count, err := c.service.RecoverExpired(ctx)
			if err != nil {
				c.logger.Warn("Callback delivery recovery failed",
					"event", observability.EventCallbackRecoveryFailed,
					"error_class", observability.ErrorClass(err))
			} else if count != 0 {
				c.logger.Warn(
					"Expired callback delivery leases recovered",
					"event",
					observability.EventCallbackLeaseRecovered,
					"count",
					count,
				)
			}
		case <-retention.C:
			count, err := c.service.Prune(ctx)
			if err != nil {
				c.logger.Warn("Callback delivery prune failed",
					"event", observability.EventCallbackPruneFailed,
					"error_class", observability.ErrorClass(err))
			} else if count != 0 {
				c.logger.Info(
					"Terminal callback deliveries pruned",
					"event",
					observability.EventCallbackPruned,
					"count",
					count,
				)
			}
		}
	}
}
