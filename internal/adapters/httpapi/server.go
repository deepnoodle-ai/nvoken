// Package httpapi serves the nvoken HTTP API.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

type Config struct {
	Addr string
}

type Server struct {
	http *http.Server
}

func NewServer(cfg Config) *Server {
	return &Server{
		http: &http.Server{
			Addr:              cfg.Addr,
			Handler:           newHandler(),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	slog.Info("nvokend listening", "addr", s.http.Addr)
	errc := make(chan error, 1)
	go func() { errc <- s.http.ListenAndServe() }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
