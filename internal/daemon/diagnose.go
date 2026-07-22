package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/cloudtasks"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/liveevents"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/observability"
)

// Diagnose runs the bounded, read-only dependency checks used by operators.
// It does not migrate the database, create tasks, publish live events, call a
// model provider, or send callbacks.
func Diagnose(ctx context.Context, cfg Config) error {
	return diagnose(ctx, cfg, slog.Default())
}

func diagnose(parent context.Context, cfg Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	timeout := cfg.DiagnosticTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	failed := false
	logDiagnosticResult(ctx, logger, "configuration", observability.OutcomeSuccess, "none")

	pool, err := postgres.OpenPoolWithConfig(ctx, cfg.DatabaseURL, postgres.PoolConfig{MaxConns: cfg.DatabaseMaxConns})
	if err != nil {
		failed = true
		logDiagnosticResult(ctx, logger, "database_connectivity", observability.OutcomeFailed, observability.ErrorClass(err))
		logDiagnosticResult(ctx, logger, "database_schema", observability.OutcomeSkipped, "dependency_failed")
	} else {
		logDiagnosticResult(ctx, logger, "database_connectivity", observability.OutcomeSuccess, "none")
		status, schemaErr := postgres.InspectSchema(ctx, pool)
		if schemaErr != nil {
			failed = true
			logDiagnosticResult(ctx, logger, "database_schema", observability.OutcomeFailed, observability.ErrorClass(schemaErr))
		} else {
			outcome := observability.OutcomeSuccess
			if !status.Compatible() {
				failed = true
				outcome = observability.OutcomeFailed
			}
			logger.LogAttrs(ctx, diagnosticLevel(outcome), "diagnostic schema check",
				slog.String("event", observability.EventDiagnosticCheck),
				slog.String("component", "database_schema"),
				slog.String("outcome", outcome),
				slog.String("error_class", string(status.State)),
				slog.Int64("schema_version", int64(status.Current)),
				slog.Int64("expected_schema_version", int64(status.Expected)),
				slog.Bool("dirty", status.Dirty))
		}
		pool.Close()
	}

	if cfg.RedisURL != "" {
		if err := liveevents.CheckRedis(ctx, cfg.RedisURL, cfg.RedisPassword, cfg.RedisCACertificate); err != nil {
			failed = true
			logDiagnosticResult(ctx, logger, "live_event_redis", observability.OutcomeFailed, observability.ErrorClass(err))
		} else {
			logDiagnosticResult(ctx, logger, "live_event_redis", observability.OutcomeSuccess, "none")
		}
	} else {
		logDiagnosticResult(ctx, logger, "live_event_redis", observability.OutcomeSkipped, "not_configured")
	}

	if cfg.CloudTasks.Queue != "" {
		queue, err := cloudtasks.New(ctx, cfg.CloudTasks)
		if err == nil {
			err = queue.Check(ctx)
			_ = queue.Close()
		}
		if err != nil {
			failed = true
			logDiagnosticResult(ctx, logger, "cloud_tasks_queue", observability.OutcomeFailed, observability.ErrorClass(err))
		} else {
			logDiagnosticResult(ctx, logger, "cloud_tasks_queue", observability.OutcomeSuccess, "none")
		}
	} else {
		logDiagnosticResult(ctx, logger, "cloud_tasks_queue", observability.OutcomeSkipped, "not_configured")
	}

	if failed {
		return fmt.Errorf("one or more diagnostic checks failed")
	}
	return nil
}

func logDiagnosticResult(ctx context.Context, logger *slog.Logger, component, outcome, errorClass string) {
	logger.LogAttrs(ctx, diagnosticLevel(outcome), "diagnostic check",
		slog.String("event", observability.EventDiagnosticCheck),
		slog.String("component", component),
		slog.String("outcome", outcome),
		slog.String("error_class", errorClass))
}

func diagnosticLevel(outcome string) slog.Level {
	if outcome == observability.OutcomeSuccess || outcome == observability.OutcomeSkipped {
		return slog.LevelInfo
	}
	return slog.LevelWarn
}
