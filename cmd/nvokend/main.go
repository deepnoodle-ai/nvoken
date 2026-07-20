package main

import (
	"context"
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

	if err := run(); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadDaemonConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return daemon.Run(ctx, cfg)
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
