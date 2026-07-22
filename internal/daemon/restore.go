package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/observability"
)

type RestoreVerificationConfig struct {
	DatabaseURL      string
	DatabaseMaxConns int32
	Timeout          time.Duration
}

// VerifyRestore runs the bounded database verifier without starting the
// Runtime API, engines, dispatch publication, live events, or callbacks.
func VerifyRestore(parent context.Context, cfg RestoreVerificationConfig) error {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	pool, err := postgres.OpenPoolWithConfig(ctx, cfg.DatabaseURL, postgres.PoolConfig{
		MaxConns:                 cfg.DatabaseMaxConns,
		StatementTimeout:         timeout,
		IdleInTransactionTimeout: timeout,
	})
	if err != nil {
		logRestoreVerification(ctx, postgres.RestoreCheck{
			Component:  "database_connectivity",
			ErrorClass: observability.ErrorClass(err),
		})
		return fmt.Errorf("open restored database")
	}
	defer pool.Close()
	logRestoreVerification(ctx, postgres.RestoreCheck{
		Component:  "database_connectivity",
		Passed:     true,
		ErrorClass: "none",
	})

	verification, verifyErr := postgres.VerifyRestore(ctx, pool)
	for _, check := range verification.Checks {
		logRestoreVerification(ctx, check)
	}
	if verifyErr != nil {
		return fmt.Errorf("restore verification failed")
	}
	return nil
}

func logRestoreVerification(ctx context.Context, check postgres.RestoreCheck) {
	outcome := observability.OutcomeFailed
	level := slog.LevelWarn
	if check.Passed {
		outcome = observability.OutcomeSuccess
		level = slog.LevelInfo
	}
	attrs := []slog.Attr{
		slog.String("event", observability.EventRestoreVerification),
		slog.String("component", check.Component),
		slog.String("outcome", outcome),
		slog.String("error_class", check.ErrorClass),
	}
	if check.RecordsExamined > 0 {
		attrs = append(attrs, slog.Int64("records_examined", check.RecordsExamined))
	}
	slog.LogAttrs(ctx, level, "restore verification check", attrs...)
}
