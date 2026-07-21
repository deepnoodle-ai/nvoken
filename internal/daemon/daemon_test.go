package daemon

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

type componentFunc func(context.Context) error

func (f componentFunc) Run(ctx context.Context) error { return f(ctx) }

func TestRunComponentsCancelsAndJoinsSibling(t *testing.T) {
	wantErr := errors.New("server failed")
	joined := make(chan struct{})
	var cancelled atomic.Bool
	err := runComponents(context.Background(),
		componentFunc(func(context.Context) error { return wantErr }),
		componentFunc(func(ctx context.Context) error {
			<-ctx.Done()
			cancelled.Store(true)
			close(joined)
			return nil
		}),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runComponents error = %v", err)
	}
	if !cancelled.Load() {
		t.Fatal("sibling was not cancelled and joined")
	}
	select {
	case <-joined:
	default:
		t.Fatal("sibling did not finish before return")
	}
}

func TestRunComponentsTreatsParentCancellationAsCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	component := componentFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err := runComponents(ctx, component, component); err != nil {
		t.Fatalf("runComponents cancellation error = %v", err)
	}
}

func TestExecutionOwnerIsUniqueAndBounded(t *testing.T) {
	first, err := executionOwner()
	if err != nil {
		t.Fatal(err)
	}
	second, err := executionOwner()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || strings.TrimSpace(first) == "" {
		t.Fatalf("owners are not unique: %q and %q", first, second)
	}
	if len(first) > 255 {
		t.Fatalf("owner is %d bytes, want at most 255", len(first))
	}
}
