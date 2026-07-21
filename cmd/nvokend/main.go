package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/deepnoodle-ai/nvoken/internal/daemon"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: cloudLoggingReplaceAttr,
	}))
	slog.SetDefault(logger)

	if err := run(os.Args[1:]); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) == 0 || (len(args) == 1 && args[0] == "serve") {
		cfg, err := loadDaemonConfig()
		if err != nil {
			return err
		}
		return daemon.Run(ctx, cfg)
	}
	if len(args) == 1 && args[0] == "migrate" {
		cfg, err := loadMigrationConfig()
		if err != nil {
			return err
		}
		return daemon.Migrate(ctx, cfg)
	}
	if len(args) == 1 && args[0] == "dispatch-smoke" {
		cfg, err := loadDispatchSmokeConfig()
		if err != nil {
			return err
		}
		result, err := daemon.CreateDispatchSmoke(ctx, cfg)
		if err != nil {
			return err
		}
		slog.Info("created synthetic execution dispatch",
			"work_id", result.Work.ID, "dispatch_id", result.Dispatch.ID,
			"dispatch_kind", result.Dispatch.Kind, "dispatch_status", result.Dispatch.Status)
		return nil
	}
	return fmt.Errorf("usage: nvokend [serve|migrate|dispatch-smoke]")
}

func cloudLoggingReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		if level, ok := a.Value.Any().(slog.Level); ok {
			a.Value = slog.StringValue(cloudLoggingSeverity(level))
		}
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

func cloudLoggingSeverity(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARNING"
	case level <= slog.LevelDebug:
		return "DEBUG"
	default:
		return "INFO"
	}
}
