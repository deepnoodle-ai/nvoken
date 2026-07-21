package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/auth"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/liveevents"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/postgres"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/engine"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
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

func TestTranscriptStreamEndToEndSurvivesHandlerRestart(t *testing.T) {
	schemaURL, cleanup := httpTestDatabase(t)
	defer cleanup()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := postgres.NewMigrator(schemaURL, 5*time.Second, logger).Apply(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := postgres.OpenPool(context.Background(), schemaURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	store := postgres.NewStore(pool)
	txm := postgres.NewTransactionManager(pool)
	account, err := services.BootstrapInstallation(context.Background(), store, txm, clock, ids)
	if err != nil {
		pool.Close()
		t.Fatalf("bootstrap: %v", err)
	}
	authenticator := runtimeTestAuthenticator(t, account.ID)
	runtime := services.NewRuntimeService(store, txm, clock, ids)
	bus := liveevents.NewInProcess(8)
	server := httptest.NewServer(newHandler(handlerConfig{
		authenticator: authenticator, runtime: runtime, liveEvents: bus, logger: logger,
		stream: StreamConfig{PollInterval: 10 * time.Millisecond, KeepaliveInterval: time.Second,
			MaxLifetime: time.Second, WriteTimeout: 100 * time.Millisecond},
	}))

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/invocations", bytes.NewReader(validInvocationJSON()))
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		server.Close()
		pool.Close()
		t.Fatalf("admit: %v", err)
	}
	var acknowledgement invocationAcknowledgementResponse
	if err := json.NewDecoder(response.Body).Decode(&acknowledgement); err != nil {
		t.Fatalf("decode acknowledgement: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("admit = %d", response.StatusCode)
	}

	streamRequest, _ := http.NewRequest(http.MethodGet,
		server.URL+"/v1/sessions/"+acknowledgement.SessionID+"/transcript/stream", nil)
	streamRequest.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	streamResponse, err := http.DefaultClient.Do(streamRequest)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	executor := services.NewGenerationExecutor(store, endToEndStreamingGenerator{}, logger,
		services.WithGenerationLiveEvents(bus))
	runnerConfig := engine.DefaultConfig()
	runnerConfig.Concurrency = 1
	runnerConfig.PollInterval = 5 * time.Millisecond
	runnerConfig.LeaseDuration = time.Second
	runnerConfig.HeartbeatInterval = 100 * time.Millisecond
	runnerConfig.ReaperInterval = 100 * time.Millisecond
	runnerConfig.DrainGrace = time.Second
	runner, err := engine.NewRunner("stream-e2e", ownership, executor, nil, logger, runnerConfig)
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	runnerContext, cancelRunner := context.WithCancel(context.Background())
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Run(runnerContext) }()
	streamBody, err := io.ReadAll(streamResponse.Body)
	_ = streamResponse.Body.Close()
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	cancelRunner()
	if err := <-runnerDone; err != nil {
		t.Fatalf("stop runner: %v", err)
	}
	body := string(streamBody)
	for _, fragment := range []string{
		"event: generation.delta", `"text":"hel"`, `"text":"hello"`, `"status":"completed"`,
		"event: stream.end", `"reason":"terminal"`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("end-to-end stream lacks %q: %s", fragment, body)
		}
	}
	resumeCursor := lastSSEID(body)
	if resumeCursor == "" {
		t.Fatalf("end-to-end stream lacks durable ID: %s", body)
	}
	server.Close()
	pool.Close()

	restartedPool, err := postgres.OpenPool(context.Background(), schemaURL)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer restartedPool.Close()
	restartedStore := postgres.NewStore(restartedPool)
	restartedRuntime := services.NewRuntimeService(
		restartedStore, postgres.NewTransactionManager(restartedPool), clock, identity.NewUUIDv7Generator(clock),
	)
	restarted := httptest.NewServer(newHandler(handlerConfig{
		authenticator: runtimeTestAuthenticator(t, account.ID), runtime: restartedRuntime, logger: logger,
		stream: StreamConfig{PollInterval: 10 * time.Millisecond, KeepaliveInterval: time.Second,
			MaxLifetime: time.Second, WriteTimeout: 100 * time.Millisecond},
	}))
	defer restarted.Close()
	reconnect, _ := http.NewRequest(http.MethodGet,
		restarted.URL+"/v1/sessions/"+acknowledgement.SessionID+"/transcript/stream", nil)
	reconnect.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	reconnect.Header.Set("Last-Event-ID", resumeCursor)
	reconnected, err := http.DefaultClient.Do(reconnect)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	reconnectedBody, _ := io.ReadAll(reconnected.Body)
	_ = reconnected.Body.Close()
	if strings.Contains(string(reconnectedBody), "event: transcript.snapshot") ||
		!strings.Contains(string(reconnectedBody), `"reason":"terminal"`) {
		t.Fatalf("restart replay = %s", reconnectedBody)
	}
}

type endToEndStreamingGenerator struct{}

func (endToEndStreamingGenerator) Generate(
	context.Context,
	domain.GenerationRequest,
) (domain.GenerationResponse, error) {
	return endToEndGenerationResponse(), nil
}

func (endToEndStreamingGenerator) GenerateStream(
	_ context.Context,
	_ domain.GenerationRequest,
	emit ports.GenerationDeltaEmitter,
) (domain.GenerationResponse, error) {
	emit(domain.GenerationDelta{ContentIndex: 0, Type: "text", Text: "hel"})
	emit(domain.GenerationDelta{ContentIndex: 0, Type: "text", Text: "lo"})
	return endToEndGenerationResponse(), nil
}

func endToEndGenerationResponse() domain.GenerationResponse {
	return domain.GenerationResponse{
		Messages: []domain.GenerationMessage{{
			Role: domain.MessageRoleAssistant, Content: json.RawMessage(`[{"type":"text","text":"hello"}]`),
		}},
		Usage:       domain.ModelUsage{InputTokens: 3, OutputTokens: 2, Iterations: 1},
		ServedModel: "test-model",
	}
}

func runtimeTestAuthenticator(t *testing.T, accountID string) ports.RuntimeAuthenticator {
	t.Helper()
	authenticator, err := auth.NewStaticAuthenticator(auth.StaticConfig{
		Token: "0123456789abcdef0123456789abcdef", AccountID: accountID,
	})
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return authenticator
}

func lastSSEID(stream string) string {
	last := ""
	for _, line := range strings.Split(stream, "\n") {
		if strings.HasPrefix(line, "id: ") {
			last = strings.TrimPrefix(line, "id: ")
		}
	}
	return last
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
