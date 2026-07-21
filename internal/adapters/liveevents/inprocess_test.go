package liveevents

import (
	"context"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func TestInProcessScopesAndMarksOverflow(t *testing.T) {
	bus := NewInProcess(1)
	sub := bus.Subscribe(context.Background(), "account-a", "session-a")
	other := bus.Subscribe(context.Background(), "account-b", "session-a")
	t.Cleanup(sub.Close)
	t.Cleanup(other.Close)

	bus.Publish(context.Background(), ports.LiveEvent{Type: "generation.delta", AccountID: "account-a", SessionID: "session-a"})
	bus.Publish(context.Background(), ports.LiveEvent{Type: "generation.delta", AccountID: "account-a", SessionID: "session-a"})

	if !sub.TakeGap() || sub.TakeGap() {
		t.Fatal("overflow gap was not reported exactly once")
	}
	select {
	case <-sub.Events():
	default:
		t.Fatal("matching event was not delivered")
	}
	select {
	case event := <-other.Events():
		t.Fatalf("cross-account event delivered: %#v", event)
	default:
	}
}

func TestInProcessCloseIsIdempotent(t *testing.T) {
	bus := NewInProcess(1)
	sub := bus.Subscribe(context.Background(), "account", "session")
	sub.Close()
	sub.Close()
	if _, ok := <-sub.Events(); ok {
		t.Fatal("closed subscription channel remains open")
	}
}
