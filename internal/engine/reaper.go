package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

type ReaperOwnership interface {
	ReapExpired(context.Context, int) ([]domain.Invocation, error)
}

type Reaper struct {
	ownership ReaperOwnership
	interval  time.Duration
	limit     int
	logger    *slog.Logger
}

func NewReaper(ownership ReaperOwnership, interval time.Duration, limit int, logger *slog.Logger) (*Reaper, error) {
	if ownership == nil || interval <= 0 || limit <= 0 {
		return nil, fmt.Errorf("reaper ownership, interval, and batch limit are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Reaper{ownership: ownership, interval: interval, limit: limit, logger: logger}, nil
}

func (r *Reaper) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.reap(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.reap(ctx)
		}
	}
}

func (r *Reaper) reap(ctx context.Context) {
	items, err := r.ownership.ReapExpired(ctx, r.limit)
	for _, invocation := range items {
		logReapedInvocation(r.logger, invocation)
	}
	if err != nil && ctx.Err() == nil {
		r.logger.Warn("Invocation lease scan failed; retrying", "error", err.Error())
	}
}
