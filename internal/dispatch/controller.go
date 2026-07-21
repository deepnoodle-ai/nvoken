// Package dispatch runs the transport-facing execution dispatch background
// loops. Services and Postgres remain authoritative.
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type ControllerConfig struct {
	PublishInterval   time.Duration
	ReconcileInterval time.Duration
	RetentionInterval time.Duration
	BatchLimit        int
}

func DefaultControllerConfig() ControllerConfig {
	return ControllerConfig{
		PublishInterval: time.Second, ReconcileInterval: time.Minute,
		RetentionInterval: time.Hour, BatchLimit: 100,
	}
}

func ValidateControllerConfig(cfg ControllerConfig) error {
	if cfg.PublishInterval <= 0 || cfg.ReconcileInterval <= 0 || cfg.RetentionInterval <= 0 {
		return fmt.Errorf("dispatch controller intervals must be positive")
	}
	if cfg.BatchLimit <= 0 || cfg.BatchLimit > 1000 {
		return fmt.Errorf("dispatch controller batch limit must be from 1 through 1000")
	}
	return nil
}

type Controller struct {
	owner   string
	service *services.DispatchService
	tasks   ports.ExecutionTaskQueue
	logger  *slog.Logger
	config  ControllerConfig
}

func NewController(owner string, service *services.DispatchService, tasks ports.ExecutionTaskQueue, logger *slog.Logger, cfg ControllerConfig) (*Controller, error) {
	if owner == "" || service == nil || tasks == nil {
		return nil, fmt.Errorf("dispatch controller owner, service, and task queue are required")
	}
	if err := ValidateControllerConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{owner: owner, service: service, tasks: tasks, logger: logger, config: cfg}, nil
}

func (c *Controller) Run(ctx context.Context) error {
	publish := time.NewTicker(c.config.PublishInterval)
	reconcile := time.NewTicker(c.config.ReconcileInterval)
	retention := time.NewTicker(c.config.RetentionInterval)
	defer publish.Stop()
	defer reconcile.Stop()
	defer retention.Stop()

	c.publish(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-publish.C:
			c.publish(ctx)
		case <-reconcile.C:
			c.reconcile(ctx)
		case <-retention.C:
			c.prune(ctx)
		}
	}
}

func (c *Controller) publish(ctx context.Context) {
	for range c.config.BatchLimit {
		claim, ok, err := c.service.ClaimNext(ctx, c.owner)
		if err != nil {
			c.logger.Error("claim execution dispatch", "error", err)
			return
		}
		if !ok {
			return
		}
		if err := c.service.PublishClaim(ctx, c.tasks, claim); err != nil {
			// The service has already made a fenced durable retry decision when
			// possible. Keep the component alive for unrelated dispatches.
			c.logger.Warn("publish execution dispatch",
				"dispatch_id", claim.Dispatch.ID, "dispatch_kind", claim.Dispatch.Kind,
				"error", err)
		}
	}
}

func (c *Controller) reconcile(ctx context.Context) {
	if err := c.service.LogAged(ctx); err != nil {
		c.logger.Error("inspect aged execution dispatches", "error", err)
	}
	result, err := c.service.Reconcile(ctx, c.tasks)
	if err != nil {
		c.logger.Error("reconcile execution dispatches", "error", err)
		return
	}
	if result.Settled+result.Succeeded > 0 {
		c.logger.Info("reconciled execution dispatches",
			"existing_tasks", result.Existing, "settled_dispatches", result.Settled,
			"successor_dispatches", result.Succeeded)
	}
}

func (c *Controller) prune(ctx context.Context) {
	count, err := c.service.Prune(ctx)
	if err != nil {
		c.logger.Error("prune execution dispatches", "error", err)
		return
	}
	if count > 0 {
		c.logger.Info("pruned execution dispatches", "count", count)
	}
}
