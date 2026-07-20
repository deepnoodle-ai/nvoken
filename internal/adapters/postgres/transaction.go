package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type transactionContextKey struct{}

type TransactionManager struct {
	pool *pgxpool.Pool
}

func NewTransactionManager(pool *pgxpool.Pool) *TransactionManager {
	return &TransactionManager{pool: pool}
}

func (m *TransactionManager) WithTransaction(ctx context.Context, fn func(context.Context) error) (err error) {
	if _, ok := ctx.Value(transactionContextKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	txCtx := context.WithValue(ctx, transactionContextKey{}, tx)
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(context.Background())
			panic(recovered)
		}
	}()

	if err := fn(txCtx); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return fmt.Errorf("rollback transaction after %w: %v", err, rollbackErr)
		}
		return normalizeTransactionError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", normalizeTransactionError(err))
	}
	return nil
}

func normalizeTransactionError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.Code {
	case "40001", "40P01", "55P03", "57014":
		return fmt.Errorf("%w: %w", ports.ErrRetryable, err)
	case "23505":
		if postgresError.ConstraintName == "invocations_one_nonterminal_per_session" ||
			postgresError.ConstraintName == "invocations_idempotency_scope" {
			return fmt.Errorf("%w: %w", ports.ErrRetryable, err)
		}
	}
	return err
}

func transactionFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(transactionContextKey{}).(pgx.Tx)
	return tx, ok
}
