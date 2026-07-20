// Package postgres implements nvoken's durable runtime ports with pgx.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	idleInTransactionTimeout = "30000"
	statementTimeout         = "120000"
)

type PoolConfig struct {
	MaxConns int32
	MinConns int32
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
	if _, ok := config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"]; !ok {
		config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = idleInTransactionTimeout
	}
	if _, ok := config.ConnConfig.RuntimeParams["statement_timeout"]; !ok {
		config.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeout
	}
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
