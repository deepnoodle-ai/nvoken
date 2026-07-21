package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/auth"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

var httpTestSchemaCounter atomic.Uint64

func TestRuntimeHTTPStateSurvivesAPIRestart(t *testing.T) {
	schemaURL, cleanup := httpTestDatabase(t)
	defer cleanup()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := postgres.NewMigrator(schemaURL, 5*time.Second, logger).Apply(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, firstHandler := openRuntimeHTTP(t, schemaURL)
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/invocations", validInvocationJSON())
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	firstHandler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("POST = %d %s", recorder.Code, recorder.Body.String())
	}
	var acknowledgement invocationAcknowledgementResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &acknowledgement); err != nil {
		t.Fatalf("decode acknowledgement: %v", err)
	}
	replayRecorder := httptest.NewRecorder()
	replayRequest := authenticatedRequest(http.MethodPost, "/v1/invocations", reorderedInvocationJSON())
	replayRequest.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	firstHandler.ServeHTTP(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusAccepted {
		t.Fatalf("reordered replay = %d %s", replayRecorder.Code, replayRecorder.Body.String())
	}
	var replay invocationAcknowledgementResponse
	if err := json.Unmarshal(replayRecorder.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode replay acknowledgement: %v", err)
	}
	if replay.InvocationID != acknowledgement.InvocationID || replay.SessionID != acknowledgement.SessionID || !replay.Deduplicated {
		t.Fatalf("reordered replay = %#v, first = %#v", replay, acknowledgement)
	}
	pool.Close()

	restartedPool, restartedHandler := openRuntimeHTTP(t, schemaURL)
	defer restartedPool.Close()
	for _, path := range []string{
		"/v1/invocations/" + acknowledgement.InvocationID,
		"/v1/invocations?session_id=" + acknowledgement.SessionID,
		"/v1/sessions?session_key=ticket-1",
		"/v1/sessions/" + acknowledgement.SessionID,
		"/v1/sessions/" + acknowledgement.SessionID + "/messages",
		"/v1/sessions/" + acknowledgement.SessionID + "/transcript",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
		restartedHandler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s after restart = %d %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func reorderedInvocationJSON() []byte {
	return []byte(`{"spec":{"model":{"name":"test-model","provider":"anthropic"},"instructions":"help"},"input":{"content":[{"text":"private caller text","type":"text"}]},"idempotency_key":"request-1","agent_ref":"support"}`)
}

func openRuntimeHTTP(t *testing.T, databaseURL string) (*pgxpool.Pool, http.Handler) {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.OpenPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if err := postgres.CheckSchema(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("check schema: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	store := postgres.NewStore(pool)
	txm := postgres.NewTransactionManager(pool)
	account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
	if err != nil {
		pool.Close()
		t.Fatalf("bootstrap: %v", err)
	}
	authenticator, err := auth.NewStaticAuthenticator(auth.StaticConfig{
		Token: "0123456789abcdef0123456789abcdef", AccountID: account.ID,
	})
	if err != nil {
		pool.Close()
		t.Fatalf("authenticator: %v", err)
	}
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	handler := newHandler(handlerConfig{
		authenticator: authenticator, runtime: runtime,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return pool, handler
}

func httpTestDatabase(t *testing.T) (string, func()) {
	t.Helper()
	baseURL := os.Getenv("NVOKEN_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := postgres.OpenPool(ctx, baseURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	schema := fmt.Sprintf("nvoken_http_test_%d_%d", time.Now().UnixNano(), httpTestSchemaCounter.Add(1))
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		admin.Close()
		t.Fatalf("parse database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	}
	return parsed.String(), cleanup
}
