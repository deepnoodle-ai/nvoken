// Package worksignal provides best-effort wake hints for durable Postgres
// queues. Correctness never depends on receiving a notification.
package worksignal

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type InProcess struct {
	mu   sync.Mutex
	subs map[*subscription]struct{}
}

func NewInProcess() *InProcess {
	return &InProcess{subs: make(map[*subscription]struct{})}
}

func (s *InProcess) Notify(_ context.Context, queue string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subs {
		if !sub.matches(queue) {
			continue
		}
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (s *InProcess) Subscribe(_ context.Context, queues []string) ports.WorkSubscription {
	sub := &subscription{parent: s, wake: make(chan struct{}, 1), queues: make(map[string]struct{}, len(queues))}
	for _, queue := range queues {
		sub.queues[queue] = struct{}{}
	}
	s.mu.Lock()
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	return sub
}

type subscription struct {
	parent *InProcess
	wake   chan struct{}
	queues map[string]struct{}
	closed atomic.Bool
}

func (s *subscription) matches(queue string) bool {
	if len(s.queues) == 0 {
		return true
	}
	_, ok := s.queues[queue]
	return ok
}

func (s *subscription) Wait(ctx context.Context, timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-s.wake:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.wake:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (s *subscription) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.parent.mu.Lock()
	delete(s.parent.subs, s)
	s.parent.mu.Unlock()
}

var _ ports.WorkSignaller = (*InProcess)(nil)
