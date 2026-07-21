package liveevents

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func TestRedisFansEventsAcrossProcessesAndMarksDisconnectGap(t *testing.T) {
	server := miniredis.RunT(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	publisher := NewRedis(redis.NewClient(&redis.Options{Addr: server.Addr()}), 4, logger)
	subscriber := NewRedis(redis.NewClient(&redis.Options{Addr: server.Addr()}), 4, logger)
	t.Cleanup(func() { _ = publisher.Close() })
	t.Cleanup(func() { _ = subscriber.Close() })

	subscription := subscriber.Subscribe(context.Background(), "account-a", "session-a")
	t.Cleanup(subscription.Close)
	publisher.Publish(context.Background(), ports.LiveEvent{
		Type: "generation.delta", AccountID: "account-a", SessionID: "session-a",
		Payload: json.RawMessage(`{"text":"hello"}`),
	})
	select {
	case event := <-subscription.Events():
		if event.Type != "generation.delta" || event.AccountID != "account-a" ||
			event.SessionID != "session-a" || string(event.Payload) != `{"text":"hello"}` {
			t.Fatalf("received event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cross-process Redis event was not delivered")
	}

	server.Close()
	deadline := time.Now().Add(2 * time.Second)
	gapped := subscription.TakeGap()
	for !gapped && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		gapped = subscription.TakeGap()
	}
	if !gapped {
		t.Fatal("Redis disconnect did not mark subscriber gapped")
	}
}

func TestRedisOptionsApplySecretAndTLSRoots(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	testCA := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: tlsServer.TLS.Certificates[0].Certificate[0],
	})
	tlsServer.Close()
	options, err := redisOptions("rediss://10.42.0.4:6378/0", "redis-secret", string(testCA))
	if err != nil {
		t.Fatalf("redis options: %v", err)
	}
	if options.Password != "redis-secret" || options.TLSConfig == nil || options.TLSConfig.RootCAs == nil ||
		options.TLSConfig.MinVersion == 0 {
		t.Fatalf("secure Redis options = %#v", options)
	}
	if _, err := redisOptions("rediss://10.42.0.4:6378/0", "", "not a certificate"); err == nil {
		t.Fatal("invalid Redis CA error = nil")
	}
}
