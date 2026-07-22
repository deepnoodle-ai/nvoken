// Package postgres implements nvoken's durable runtime ports with pgx.
package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	idleInTransactionTimeout = "30000"
	statementTimeout         = "120000"
)

type PoolConfig struct {
	MaxConns                 int32
	MinConns                 int32
	StatementTimeout         time.Duration
	IdleInTransactionTimeout time.Duration
}

func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return OpenPoolWithConfig(ctx, databaseURL, PoolConfig{})
}

func OpenPoolWithConfig(ctx context.Context, databaseURL string, cfg PoolConfig) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL")
	}
	if cfg.MaxConns > 0 {
		config.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		config.MinConns = cfg.MinConns
	}
	if config.MinConns > config.MaxConns {
		return nil, fmt.Errorf("database minimum connections cannot exceed maximum connections")
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	configurePoolTimeout(
		config.ConnConfig.RuntimeParams,
		"idle_in_transaction_session_timeout",
		idleInTransactionTimeout,
		cfg.IdleInTransactionTimeout,
	)
	configurePoolTimeout(
		config.ConnConfig.RuntimeParams,
		"statement_timeout",
		statementTimeout,
		cfg.StatementTimeout,
	)
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func configurePoolTimeout(
	runtimeParams map[string]string,
	name string,
	defaultValue string,
	override time.Duration,
) {
	if override > 0 {
		runtimeParams[name] = strconv.FormatInt(max(1, override.Milliseconds()), 10)
		return
	}
	if _, ok := runtimeParams[name]; !ok {
		runtimeParams[name] = defaultValue
	}
}
