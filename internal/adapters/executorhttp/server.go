// Package executorhttp serves the private request-bound executor surface.
package executorhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type AttemptService interface {
	Attempt(context.Context, string) (services.DispatchAttemptOutcome, error)
}

type Config struct {
	Addr            string
	Attempts        AttemptService
	Logger          *slog.Logger
	AttemptTimeout  time.Duration
	ShutdownTimeout time.Duration
}

type Server struct {
	http            *http.Server
	shutdownTimeout time.Duration
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.Attempts == nil {
		return nil, fmt.Errorf("executor attempt service is required")
	}
	if cfg.AttemptTimeout <= 0 {
		return nil, fmt.Errorf("executor attempt timeout must be positive")
	}
	if cfg.ShutdownTimeout <= 0 {
		return nil, fmt.Errorf("executor shutdown timeout must be positive")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	handler := newHandler(cfg.Attempts, logger, cfg.AttemptTimeout)
	return &Server{
		http: &http.Server{
			Addr: cfg.Addr, Handler: handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			// A future real execution segment may hold the response for nearly
			// the Cloud Tasks deadline. Its application context is the bound.
			WriteTimeout:   0,
			IdleTimeout:    60 * time.Second,
			MaxHeaderBytes: 1 << 20,
		},
		shutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}

func newHandler(attempts AttemptService, logger *slog.Logger, attemptTimeout time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /internal/execution-dispatches/{dispatch_id}/attempts", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 0)
		if _, err := io.ReadAll(r.Body); err != nil {
			logger.Warn("ignored malformed execution dispatch delivery",
				"dispatch_id", r.PathValue("dispatch_id"), "handler_outcome", "poison_body")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), attemptTimeout)
		defer cancel()
		outcome, err := attempts.Attempt(ctx, r.PathValue("dispatch_id"))
		if err != nil {
			retryReason := "infrastructure"
			if errors.Is(err, ports.ErrDispatchAttemptActive) {
				retryReason = "active_owner"
			} else if errors.Is(err, ports.ErrDispatchAttemptPending) {
				retryReason = "durable_decision_pending"
			}
			logger.Error("execution dispatch attempt undecided",
				"event", "dispatch_attempt_retry", "dispatch_id", r.PathValue("dispatch_id"),
				"handler_outcome", "retry", "retry_reason", retryReason, "error", err)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		logger.Info("execution dispatch attempt decided",
			"dispatch_id", r.PathValue("dispatch_id"), "handler_outcome", outcome)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (s *Server) Run(ctx context.Context) error {
	slog.Info("nvokend executor listening", "addr", s.http.Addr)
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- s.http.ListenAndServe() }()
	select {
	case err := <-errorsCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	shutdownErr := s.http.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		closeErr := s.http.Close()
		listenErr := <-errorsCh
		if errors.Is(listenErr, http.ErrServerClosed) {
			listenErr = nil
		}
		return errors.Join(shutdownErr, closeErr, listenErr)
	}
	if err := <-errorsCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
