package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func TestAttemptServiceRoutesCancellationNotification(t *testing.T) {
	signaller := newAttemptCancellationSignaller()
	service := &AttemptService{
		cancellations: signaller,
		claimCancels:  make(map[string]context.CancelCauseFunc),
	}
	claimCtx, cancelClaim := context.WithCancelCause(context.Background())
	service.claimCancels["invk_test"] = cancelClaim
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runCtx) }()
	select {
	case <-signaller.ready:
	case <-time.After(time.Second):
		t.Fatal("cancellation listener did not start")
	}
	signaller.events <- "invk_test"
	select {
	case <-claimCtx.Done():
		if context.Cause(claimCtx) != errCancellationNotification {
			t.Fatalf("cancellation cause = %v", context.Cause(claimCtx))
		}
	case <-time.After(time.Second):
		t.Fatal("active attempt was not cancelled")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("listener: %v", err)
	}
}

type attemptCancellationSignaller struct {
	events chan string
	ready  chan struct{}
	once   sync.Once
}

func newAttemptCancellationSignaller() *attemptCancellationSignaller {
	return &attemptCancellationSignaller{events: make(chan string, 1), ready: make(chan struct{})}
}

func (s *attemptCancellationSignaller) NotifyCancellation(context.Context, string) {}

func (s *attemptCancellationSignaller) SubscribeCancellations(context.Context) ports.CancellationSubscription {
	s.once.Do(func() { close(s.ready) })
	return attemptCancellationSubscription{s.events}
}

type attemptCancellationSubscription struct{ events <-chan string }

func (s attemptCancellationSubscription) Wait(ctx context.Context, timeout time.Duration) (string, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case id := <-s.events:
		return id, true
	case <-ctx.Done():
		return "", false
	case <-timer.C:
		return "", false
	}
}

func (attemptCancellationSubscription) Close() {}
