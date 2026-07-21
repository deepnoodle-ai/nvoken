package engine

import (
	"context"
	"testing"
	"time"
)

func TestStandaloneReaperRunsImmediatelyAndPeriodically(t *testing.T) {
	ownership := newFakeOwnership(0, time.Second)
	reaper, err := NewReaper(ownership, 20*time.Millisecond, 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reaper.Run(ctx) }()
	waitUntil(t, time.Second, func() bool { return ownership.reapCount() >= 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}
