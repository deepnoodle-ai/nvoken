// Package liveevents provides lossy fan-out adapters for live-only output.
// Postgres remains authoritative; these adapters exist only to reduce latency.
package liveevents

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const defaultSubscriberBuffer = 64

type subscription struct {
	bus       *InProcess
	id        uint64
	accountID string
	sessionID string
	channel   chan ports.LiveEvent
	mu        sync.Mutex
	closed    atomic.Bool
	gapped    atomic.Bool
}

func (s *subscription) Events() <-chan ports.LiveEvent { return s.channel }
func (s *subscription) TakeGap() bool                  { return s.gapped.Swap(false) }

func (s *subscription) Close() {
	if s == nil || s.closed.Swap(true) {
		return
	}
	if s.bus != nil {
		s.bus.remove(s)
		return
	}
	s.closeChannel()
}

func (s *subscription) deliver(event ports.LiveEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return
	}
	select {
	case s.channel <- event:
	default:
		s.gapped.Store(true)
	}
}

func (s *subscription) closeChannel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.channel)
}

// InProcess is a bounded, nonblocking broker for one-process installations.
type InProcess struct {
	mu      sync.RWMutex
	next    uint64
	buffer  int
	byScope map[string]map[uint64]*subscription
}

func NewInProcess(buffer int) *InProcess {
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}
	return &InProcess{buffer: buffer, byScope: make(map[string]map[uint64]*subscription)}
}

func (b *InProcess) Publish(_ context.Context, event ports.LiveEvent) {
	if b == nil || event.AccountID == "" || event.SessionID == "" || event.Type == "" {
		return
	}
	b.mu.RLock()
	current := b.byScope[liveScope(event.AccountID, event.SessionID)]
	targets := make([]*subscription, 0, len(current))
	for _, target := range current {
		targets = append(targets, target)
	}
	b.mu.RUnlock()
	for _, target := range targets {
		target.deliver(event)
	}
}

func (b *InProcess) Subscribe(_ context.Context, accountID, sessionID string) ports.LiveSubscription {
	if b == nil || accountID == "" || sessionID == "" {
		return closedSubscription()
	}
	b.mu.Lock()
	b.next++
	sub := &subscription{
		bus: b, id: b.next, accountID: accountID, sessionID: sessionID,
		channel: make(chan ports.LiveEvent, b.buffer),
	}
	key := liveScope(accountID, sessionID)
	if b.byScope[key] == nil {
		b.byScope[key] = make(map[uint64]*subscription)
	}
	b.byScope[key][sub.id] = sub
	b.mu.Unlock()
	return sub
}

func (b *InProcess) remove(sub *subscription) {
	b.mu.Lock()
	key := liveScope(sub.accountID, sub.sessionID)
	delete(b.byScope[key], sub.id)
	if len(b.byScope[key]) == 0 {
		delete(b.byScope, key)
	}
	b.mu.Unlock()
	sub.closeChannel()
}

func liveScope(accountID, sessionID string) string { return accountID + "\x00" + sessionID }

func closedSubscription() ports.LiveSubscription {
	sub := &subscription{channel: make(chan ports.LiveEvent)}
	sub.closed.Store(true)
	close(sub.channel)
	return sub
}

var _ ports.LiveEventBus = (*InProcess)(nil)
