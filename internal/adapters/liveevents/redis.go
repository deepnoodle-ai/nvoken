package liveevents

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const redisChannelPrefix = "nvoken:live:"

type redisEnvelope struct {
	Type      string          `json:"type"`
	AccountID string          `json:"account_id"`
	SessionID string          `json:"session_id"`
	Payload   json.RawMessage `json:"payload"`
}

type redisScope struct {
	accountID string
	pubsub    *redis.PubSub
	cancel    context.CancelFunc
	refs      int
	ready     chan struct{}
	readyErr  error
	mu        sync.Mutex
}

type redisSubscription struct {
	once  sync.Once
	bus   *Redis
	scope *redisScope
	local ports.LiveSubscription
}

func (s *redisSubscription) Events() <-chan ports.LiveEvent { return s.local.Events() }
func (s *redisSubscription) TakeGap() bool                  { return s.local.TakeGap() }
func (s *redisSubscription) Close() {
	s.once.Do(func() {
		s.local.Close()
		s.bus.releaseScope(s.scope)
	})
}

// Redis is a cross-process, lossy Pub/Sub broker. Publish is bounded and
// asynchronous so Redis latency can never stall model generation.
type Redis struct {
	client *redis.Client
	logger *slog.Logger
	local  *InProcess

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan ports.LiveEvent
	wg     sync.WaitGroup
	closed atomic.Bool

	scopesMu sync.Mutex
	scopes   map[string]*redisScope
	gapsMu   sync.Mutex
	gaps     map[string]ports.LiveEvent
}

func NewRedisURL(rawURL, password, caCertificate string, buffer int, logger *slog.Logger) (*Redis, error) {
	options, err := redisOptions(rawURL, password, caCertificate)
	if err != nil {
		return nil, err
	}
	return NewRedis(redis.NewClient(options), buffer, logger), nil
}

func redisOptions(rawURL, password, caCertificate string) (*redis.Options, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	if password != "" {
		options.Password = password
	}
	if caCertificate != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(caCertificate)) {
			return nil, errors.New("parse Redis CA certificate")
		}
		if options.TLSConfig == nil {
			options.TLSConfig = &tls.Config{}
		} else {
			// ParseURL carries the rediss host (including a Memorystore IP) as
			// ServerName. Preserve it while replacing the trust roots.
			options.TLSConfig = options.TLSConfig.Clone()
		}
		options.TLSConfig.MinVersion = tls.VersionTLS12
		options.TLSConfig.RootCAs = roots
	}
	return options, nil
}

func NewRedis(client *redis.Client, buffer int, logger *slog.Logger) *Redis {
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	broker := &Redis{
		client: client, logger: logger, local: NewInProcess(buffer),
		ctx: ctx, cancel: cancel, queue: make(chan ports.LiveEvent, buffer*4),
		scopes: make(map[string]*redisScope), gaps: make(map[string]ports.LiveEvent),
	}
	broker.wg.Add(1)
	go broker.publishLoop()
	return broker
}

func (r *Redis) Publish(_ context.Context, event ports.LiveEvent) {
	if r == nil || r.closed.Load() || event.Type == "" || event.AccountID == "" || event.SessionID == "" {
		return
	}
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	select {
	case r.queue <- event:
	default:
		r.recordPublishGap(event)
		r.logger.Warn("live event publish buffer overflow",
			"account_id", event.AccountID, "session_id", event.SessionID, "event_type", event.Type)
	}
}

func (r *Redis) Subscribe(ctx context.Context, accountID, sessionID string) ports.LiveSubscription {
	if r == nil || r.closed.Load() || accountID == "" || sessionID == "" {
		return closedSubscription()
	}
	local := r.local.Subscribe(ctx, accountID, sessionID)
	scope, err := r.retainScope(ctx, accountID)
	if err != nil {
		if current, ok := local.(*subscription); ok {
			current.gapped.Store(true)
		}
		r.logger.Warn("live event Redis subscribe acknowledgement failed", "account_id", accountID, "error", err)
	}
	return &redisSubscription{bus: r, scope: scope, local: local}
}

func (r *Redis) publishLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case event := <-r.queue:
			r.publishOne(event)
		case <-ticker.C:
			for _, event := range r.takePublishGaps() {
				r.publishOne(event)
			}
		}
	}
}

func (r *Redis) publishOne(event ports.LiveEvent) {
	envelope, err := json.Marshal(redisEnvelope{
		Type: event.Type, AccountID: event.AccountID, SessionID: event.SessionID, Payload: event.Payload,
	})
	if err != nil {
		r.logger.Warn("live event envelope encode failed", "event_type", event.Type, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 500*time.Millisecond)
	defer cancel()
	if err := r.client.Publish(ctx, redisChannel(event.AccountID), envelope).Err(); err != nil && r.ctx.Err() == nil {
		r.recordPublishGap(event)
		r.logger.Warn("live event Redis publish failed",
			"account_id", event.AccountID, "session_id", event.SessionID, "event_type", event.Type, "error", err)
	}
}

func (r *Redis) recordPublishGap(event ports.LiveEvent) {
	r.gapsMu.Lock()
	key := liveScope(event.AccountID, event.SessionID)
	_, alreadyPending := r.gaps[key]
	payload, _ := json.Marshal(domain.StreamResyncEvent{
		EventType: domain.LiveEventStreamResync, SessionID: event.SessionID, Reason: "live_delivery_gap",
	})
	r.gaps[key] = ports.LiveEvent{
		Type: domain.LiveEventStreamResync, AccountID: event.AccountID, SessionID: event.SessionID, Payload: payload,
	}
	r.gapsMu.Unlock()
	if !alreadyPending {
		r.markScopeGapped(event.AccountID, event.SessionID)
	}
}

func (r *Redis) takePublishGaps() []ports.LiveEvent {
	r.gapsMu.Lock()
	defer r.gapsMu.Unlock()
	events := make([]ports.LiveEvent, 0, len(r.gaps))
	for _, event := range r.gaps {
		events = append(events, event)
	}
	clear(r.gaps)
	return events
}

func (r *Redis) retainScope(ctx context.Context, accountID string) (*redisScope, error) {
	r.scopesMu.Lock()
	if r.closed.Load() {
		r.scopesMu.Unlock()
		return nil, context.Canceled
	}
	if scope := r.scopes[accountID]; scope != nil {
		scope.refs++
		r.scopesMu.Unlock()
		select {
		case <-ctx.Done():
			return scope, ctx.Err()
		case <-scope.ready:
			scope.mu.Lock()
			err := scope.readyErr
			scope.mu.Unlock()
			return scope, err
		}
	}
	scopeCtx, cancel := context.WithCancel(r.ctx)
	pubsub := r.client.Subscribe(scopeCtx, redisChannel(accountID))
	scope := &redisScope{accountID: accountID, pubsub: pubsub, cancel: cancel, refs: 1, ready: make(chan struct{})}
	r.scopes[accountID] = scope
	r.wg.Add(1)
	r.scopesMu.Unlock()

	ackCtx, ackCancel := context.WithTimeout(ctx, 2*time.Second)
	_, err := pubsub.Receive(ackCtx)
	ackCancel()
	scope.mu.Lock()
	scope.readyErr = err
	scope.mu.Unlock()
	close(scope.ready)
	go r.readScope(scopeCtx, scope)
	return scope, err
}

func (r *Redis) releaseScope(scope *redisScope) {
	if scope == nil {
		return
	}
	r.scopesMu.Lock()
	scope.refs--
	if scope.refs > 0 || r.scopes[scope.accountID] != scope {
		r.scopesMu.Unlock()
		return
	}
	delete(r.scopes, scope.accountID)
	r.scopesMu.Unlock()
	scope.cancel()
	scope.mu.Lock()
	pubsub := scope.pubsub
	scope.mu.Unlock()
	if pubsub != nil {
		_ = pubsub.Close()
	}
}

func (r *Redis) readScope(ctx context.Context, scope *redisScope) {
	defer r.wg.Done()
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		scope.mu.Lock()
		pubsub := scope.pubsub
		scope.mu.Unlock()
		message, err := pubsub.ReceiveMessage(ctx)
		if err == nil {
			backoff = 100 * time.Millisecond
			r.dispatch(message.Payload)
			continue
		}
		if ctx.Err() != nil {
			return
		}
		r.markAccountGapped(scope.accountID)
		r.logger.Warn("live event Redis subscription interrupted", "account_id", scope.accountID, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
		next := r.client.Subscribe(ctx, redisChannel(scope.accountID))
		ackCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, ackErr := next.Receive(ackCtx)
		cancel()
		if ackErr != nil {
			_ = next.Close()
			continue
		}
		scope.mu.Lock()
		old := scope.pubsub
		scope.pubsub = next
		scope.readyErr = nil
		scope.mu.Unlock()
		_ = old.Close()
	}
}

func (r *Redis) dispatch(raw string) {
	var envelope redisEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		r.logger.Warn("live event Redis envelope decode failed", "error", err)
		return
	}
	r.local.Publish(r.ctx, ports.LiveEvent{
		Type: envelope.Type, AccountID: envelope.AccountID,
		SessionID: envelope.SessionID, Payload: envelope.Payload,
	})
}

func (r *Redis) markAccountGapped(accountID string) {
	r.local.mu.RLock()
	targets := make([]*subscription, 0)
	for _, subscribers := range r.local.byScope {
		for _, sub := range subscribers {
			if sub.accountID == accountID {
				targets = append(targets, sub)
			}
		}
	}
	r.local.mu.RUnlock()
	for _, target := range targets {
		target.gapped.Store(true)
	}
}

func (r *Redis) markScopeGapped(accountID, sessionID string) {
	r.local.mu.RLock()
	current := r.local.byScope[liveScope(accountID, sessionID)]
	targets := make([]*subscription, 0, len(current))
	for _, sub := range current {
		targets = append(targets, sub)
	}
	r.local.mu.RUnlock()
	for _, target := range targets {
		target.gapped.Store(true)
	}
}

func (r *Redis) Close() error {
	if r == nil || r.closed.Swap(true) {
		return nil
	}
	r.cancel()
	r.scopesMu.Lock()
	scopes := make([]*redisScope, 0, len(r.scopes))
	for _, scope := range r.scopes {
		scopes = append(scopes, scope)
	}
	clear(r.scopes)
	r.scopesMu.Unlock()
	for _, scope := range scopes {
		scope.cancel()
		scope.mu.Lock()
		pubsub := scope.pubsub
		scope.mu.Unlock()
		_ = pubsub.Close()
	}
	r.wg.Wait()
	return r.client.Close()
}

func redisChannel(accountID string) string { return redisChannelPrefix + accountID }

var _ ports.LiveEventBus = (*Redis)(nil)
