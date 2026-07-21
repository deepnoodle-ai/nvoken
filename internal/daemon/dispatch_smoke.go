package daemon

import (
	"context"
	"fmt"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type DispatchSmokeConfig struct {
	DatabaseURL      string
	DatabaseMaxConns int32
	Queue            string
}

type DispatchSmokeResult struct {
	Work     domain.SyntheticDispatchWork
	Dispatch domain.ExecutionDispatch
}

func CreateDispatchSmoke(ctx context.Context, cfg DispatchSmokeConfig) (DispatchSmokeResult, error) {
	pool, err := postgres.OpenPoolWithConfig(ctx, cfg.DatabaseURL, postgres.PoolConfig{MaxConns: cfg.DatabaseMaxConns})
	if err != nil {
		return DispatchSmokeResult{}, fmt.Errorf("open runtime database: %w", err)
	}
	defer pool.Close()
	if err := postgres.CheckSchema(ctx, pool); err != nil {
		return DispatchSmokeResult{}, fmt.Errorf("check runtime database schema: %w", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	dispatchConfig := services.DefaultDispatchConfig()
	dispatchConfig.Queue = cfg.Queue
	service, err := services.NewDispatchService(
		postgres.NewStore(pool), postgres.NewTransactionManager(pool), clock, ids,
		dispatchConfig, nil,
	)
	if err != nil {
		return DispatchSmokeResult{}, err
	}
	work, dispatch, err := service.CreateSynthetic(ctx)
	if err != nil {
		return DispatchSmokeResult{}, err
	}
	return DispatchSmokeResult{Work: work, Dispatch: dispatch}, nil
}
