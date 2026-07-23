package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/liveevents"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type scriptedStreamRuntime struct {
	*fakeRuntime
	mu             sync.Mutex
	snapshots      []services.TranscriptSnapshot
	states         []bool
	transcriptCall int
	stateCall      int
	inputs         []services.TranscriptInput
	firstDrain     chan struct{}
	firstOnce      sync.Once
	transcriptErr  error
}

func (r *scriptedStreamRuntime) GetSessionTranscript(
	_ context.Context,
	_ domain.RuntimeAuthContext,
	_ string,
	input services.TranscriptInput,
) (services.TranscriptSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inputs = append(r.inputs, input)
	r.firstOnce.Do(func() {
		if r.firstDrain != nil {
			close(r.firstDrain)
		}
	})
	if r.transcriptErr != nil {
		return services.TranscriptSnapshot{}, r.transcriptErr
	}
	index := min(r.transcriptCall, len(r.snapshots)-1)
	r.transcriptCall++
	return r.snapshots[index], nil
}

func (r *scriptedStreamRuntime) GetSessionTranscriptStreamState(
	context.Context,
	domain.RuntimeAuthContext,
	string,
) (services.TranscriptStreamState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := min(r.stateCall, len(r.states)-1)
	r.stateCall++
	return services.TranscriptStreamState{Active: r.states[index]}, nil
}

func TestTranscriptStreamProjectsDurableCursorAndQueryPrecedesHeader(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	runtime := &scriptedStreamRuntime{
		fakeRuntime: &fakeRuntime{},
		snapshots: []services.TranscriptSnapshot{
			{
				Messages: []domain.SessionMessage{{
					ID:           "smsg_019b0a12-0000-7000-8000-000000000005",
					SessionID:    testSessionID,
					AgentID:      testAgentID,
					InvocationID: testInvocationID,
					Sequence:     1,
					Role:         domain.MessageRoleAssistant,
					Content:      json.RawMessage(`[{"type":"text","text":"done"}]`),
					CreatedAt:    now,
				}},
				HasMore:       true,
				ResumeCursor:  "cursor-message",
				NextPageToken: streamStringPointer("page-lifecycle"),
			},
			{
				InvocationChanges: []domain.InvocationLifecycleChange{{InvocationState: domain.InvocationState{
					InvocationID:           testInvocationID,
					Revision:               2,
					Status:                 domain.InvocationCompleted,
					ThroughMessageSequence: int64Pointer(1),
					CreatedAt:              now,
				}}},
				ResumeCursor: "cursor-final",
			},
			{Messages: []domain.SessionMessage{}, InvocationChanges: []domain.InvocationLifecycleChange{}, ResumeCursor: "cursor-final"},
		},
		states: []bool{false, false, false},
	}
	server := newStreamTestServer(t, runtime, nil, StreamConfig{})
	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/sessions/"+testSessionID+"/transcript/stream?cursor=query-cursor", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Last-Event-ID", "header-cursor")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream response = %d %q: %s", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	text := string(body)
	for _, fragment := range []string{
		"id: cursor-message", "id: cursor-final", "event: transcript.update", `"text":"done"`, `"status":"completed"`,
		"event: stream.end", `"reason":"terminal"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Errorf("stream lacks %q: %s", fragment, text)
		}
	}
	runtime.mu.Lock()
	firstInput := runtime.inputs[0]
	secondInput := runtime.inputs[1]
	runtime.mu.Unlock()
	if firstInput.Cursor != "query-cursor" {
		t.Fatalf("first cursor = %q, want query cursor", firstInput.Cursor)
	}
	if secondInput.Cursor != "" || secondInput.PageToken != "page-lifecycle" {
		t.Fatalf("second input = %+v, want page-token continuation without cursor", secondInput)
	}
	if strings.Index(text, "id: cursor-message") > strings.Index(text, "id: cursor-final") {
		t.Fatalf("lifecycle snapshot preceded message snapshot: %s", text)
	}
	assertNoIDOnEvent(t, text, domain.LiveEventStreamEnd)

	reconnect, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/sessions/"+testSessionID+"/transcript/stream", nil)
	reconnect.Header.Set("Authorization", "Bearer test-token")
	reconnect.Header.Set("Last-Event-ID", "cursor-final")
	reconnected, err := http.DefaultClient.Do(reconnect)
	if err != nil {
		t.Fatalf("reconnect stream: %v", err)
	}
	reconnectedBody, _ := io.ReadAll(reconnected.Body)
	_ = reconnected.Body.Close()
	if strings.Contains(string(reconnectedBody), "event: transcript.update") {
		t.Fatalf("reconnect duplicated durable snapshot: %s", reconnectedBody)
	}
	runtime.mu.Lock()
	reconnectInput := runtime.inputs[len(runtime.inputs)-1]
	runtime.mu.Unlock()
	if reconnectInput.Cursor != "cursor-final" {
		t.Fatalf("reconnect cursor = %q, want final delivered ID", reconnectInput.Cursor)
	}
}

func TestCompletedInvocationReplayStreamsAcceptedResultAndEnd(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	terminal := services.InvocationRead{
		ID:         testInvocationID,
		AgentID:    testAgentID,
		SessionID:  testSessionID,
		Status:     domain.InvocationCompleted,
		DeadlineAt: now.Add(5 * time.Minute),
		CreatedAt:  now,
		UpdatedAt:  now.Add(time.Second),
		EndedAt:    streamTimePointer(now.Add(time.Second)),
	}
	runtime := &scriptedStreamRuntime{
		fakeRuntime: &fakeRuntime{
			ack: services.InvocationAcknowledgement{
				AgentID:      testAgentID,
				SessionID:    testSessionID,
				InvocationID: testInvocationID,
				Status:       domain.InvocationCompleted,
				Deduplicated: true,
				DeadlineAt:   now.Add(5 * time.Minute),
			},
			invocationResult: services.InvocationResultRead{
				Invocation: terminal,
				Messages:   []domain.SessionMessage{},
			},
		},
		snapshots: []services.TranscriptSnapshot{{ResumeCursor: "cursor-final"}},
		states:    []bool{false},
	}
	server := newStreamTestServer(t, runtime, nil, StreamConfig{})
	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/v1/invocations",
		strings.NewReader(`{
			"agent_key":"support",
			"idempotency_key":"replay-1",
			"input":"hello",
			"spec":{"model":{"provider":"anthropic","id":"test-model"}}
		}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("stream status = %d: %s", response.StatusCode, body)
	}
	var eventTypes []string
	for _, frame := range strings.Split(string(body), "\n\n") {
		for _, line := range strings.Split(frame, "\n") {
			if value, found := strings.CutPrefix(line, "event: "); found {
				eventTypes = append(eventTypes, value)
			}
		}
	}
	if strings.Join(eventTypes, ",") != "invocation.accepted,invocation.result,stream.end" {
		t.Fatalf("event types = %#v, stream = %s", eventTypes, body)
	}
	if !strings.Contains(string(body), `"deduplicated":true`) ||
		!strings.Contains(string(body), `"type":"invocation.result"`) {
		t.Fatalf("completed replay payload = %s", body)
	}
}

func TestTranscriptStreamForwardsLiveDeltaThenReconcilesTerminalState(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	firstDrain := make(chan struct{})
	runtime := &scriptedStreamRuntime{
		fakeRuntime: &fakeRuntime{},
		firstDrain:  firstDrain,
		snapshots: []services.TranscriptSnapshot{
			{Messages: []domain.SessionMessage{}, InvocationChanges: []domain.InvocationLifecycleChange{}, ResumeCursor: "cursor-0"},
			{
				Messages: []domain.SessionMessage{{
					ID:           "smsg_019b0a12-0000-7000-8000-000000000006",
					SessionID:    testSessionID,
					AgentID:      testAgentID,
					InvocationID: testInvocationID,
					Sequence:     2,
					Role:         domain.MessageRoleAssistant,
					Content:      json.RawMessage(`[{"type":"text","text":"hello"}]`),
					CreatedAt:    now,
				}},
				InvocationChanges: []domain.InvocationLifecycleChange{{InvocationState: domain.InvocationState{
					InvocationID:           testInvocationID,
					Revision:               3,
					Status:                 domain.InvocationCompleted,
					ThroughMessageSequence: int64Pointer(2),
					CreatedAt:              now,
				}}},
				ResumeCursor: "cursor-1",
			},
			{Messages: []domain.SessionMessage{}, InvocationChanges: []domain.InvocationLifecycleChange{}, ResumeCursor: "cursor-1"},
		},
		states: []bool{true, true, false, false},
	}
	bus := liveevents.NewInProcess(8)
	server := newStreamTestServer(t, runtime, bus, StreamConfig{
		PollInterval:      25 * time.Millisecond,
		KeepaliveInterval: time.Second,
		MaxLifetime:       time.Second,
		WriteTimeout:      100 * time.Millisecond,
	})
	result := make(chan string, 1)
	go func() { result <- readAuthenticatedStream(t, server.URL) }()
	<-firstDrain
	payload, _ := json.Marshal(domain.GenerationDeltaEvent{
		Type:         domain.LiveEventOutputTextDelta,
		SessionID:    testSessionID,
		InvocationID: testInvocationID,
		Attempt:      1,
		Iteration:    1,
		ContentIndex: 0,
		Text:         "hel",
		EmittedAt:    now,
	})
	bus.Publish(context.Background(), ports.LiveEvent{
		Type:      domain.LiveEventOutputTextDelta,
		AccountID: testAccountID,
		SessionID: testSessionID,
		Payload:   payload,
	})
	text := <-result
	for _, fragment := range []string{
		"event: output_text.delta", `"text":"hel"`, "id: cursor-1", `"text":"hello"`, `"reason":"terminal"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Errorf("stream lacks %q: %s", fragment, text)
		}
	}
	assertNoIDOnEvent(t, text, domain.LiveEventOutputTextDelta)
}

func TestTranscriptStreamSignalsOverflowAndRotates(t *testing.T) {
	firstDrain := make(chan struct{})
	runtime := &scriptedStreamRuntime{
		fakeRuntime: &fakeRuntime{},
		firstDrain:  firstDrain,
		snapshots: []services.TranscriptSnapshot{{
			Messages: []domain.SessionMessage{}, InvocationChanges: []domain.InvocationLifecycleChange{}, ResumeCursor: "cursor-0",
		}},
		states: []bool{true},
	}
	bus := liveevents.NewInProcess(1)
	server := newStreamTestServer(t, runtime, bus, StreamConfig{
		PollInterval:      20 * time.Millisecond,
		KeepaliveInterval: 10 * time.Millisecond,
		MaxLifetime:       80 * time.Millisecond,
		WriteTimeout:      50 * time.Millisecond,
	})
	result := make(chan string, 1)
	go func() { result <- readAuthenticatedStream(t, server.URL) }()
	<-firstDrain
	for index := range 3 {
		payload, _ := json.Marshal(domain.GenerationDeltaEvent{
			Type:         domain.LiveEventOutputTextDelta,
			SessionID:    testSessionID,
			InvocationID: testInvocationID,
			Attempt:      1,
			Iteration:    1,
			ContentIndex: index,
			Text:         "x",
			EmittedAt:    time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
		})
		bus.Publish(context.Background(), ports.LiveEvent{
			Type:      domain.LiveEventOutputTextDelta,
			AccountID: testAccountID,
			SessionID: testSessionID,
			Payload:   payload,
		})
	}
	text := <-result
	for _, fragment := range []string{"event: stream.resync", `"reason":"live_delivery_gap"`, ": keepalive", `"reason":"rotate"`} {
		if !strings.Contains(text, fragment) {
			t.Errorf("stream lacks %q: %s", fragment, text)
		}
	}
	assertNoIDOnEvent(t, text, domain.LiveEventStreamResync)
	assertNoIDOnEvent(t, text, domain.LiveEventStreamEnd)
}

func TestTranscriptStreamShutdownRotatesWithoutCancellingRequest(t *testing.T) {
	firstDrain := make(chan struct{})
	runtime := &scriptedStreamRuntime{
		fakeRuntime: &fakeRuntime{},
		firstDrain:  firstDrain,
		snapshots:   []services.TranscriptSnapshot{{ResumeCursor: "cursor-0"}},
		states:      []bool{true},
	}
	shutdown, cancelShutdown := context.WithCancel(context.Background())
	server := newStreamTestServerWithShutdown(t, runtime, liveevents.NewInProcess(1), StreamConfig{
		PollInterval:      time.Second,
		KeepaliveInterval: time.Second,
		MaxLifetime:       time.Minute,
		WriteTimeout:      100 * time.Millisecond,
	}, shutdown)
	result := make(chan string, 1)
	go func() { result <- readAuthenticatedStream(t, server.URL) }()
	<-firstDrain
	cancelShutdown()
	text := <-result
	if !strings.Contains(text, "event: stream.end") || !strings.Contains(text, `"reason":"rotate"`) {
		t.Fatalf("shutdown stream = %s", text)
	}
}

func TestTranscriptStreamPollsTerminalSettlementDuringRedisOutage(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	firstDrain := make(chan struct{})
	runtime := &scriptedStreamRuntime{
		fakeRuntime: &fakeRuntime{},
		firstDrain:  firstDrain,
		snapshots: []services.TranscriptSnapshot{
			{ResumeCursor: "cursor-0"},
			{
				Messages: []domain.SessionMessage{{
					ID:           "smsg_019b0a12-0000-7000-8000-000000000007",
					SessionID:    testSessionID,
					AgentID:      testAgentID,
					InvocationID: testInvocationID,
					Sequence:     1,
					Role:         domain.MessageRoleAssistant,
					Content:      json.RawMessage(`[{"type":"text","text":"from-postgres"}]`),
					CreatedAt:    now,
				}},
				HasMore:       true,
				ResumeCursor:  "cursor-message",
				NextPageToken: streamStringPointer("lifecycle"),
			},
			{
				InvocationChanges: []domain.InvocationLifecycleChange{{InvocationState: domain.InvocationState{
					InvocationID:           testInvocationID,
					Revision:               2,
					Status:                 domain.InvocationCompleted,
					ThroughMessageSequence: int64Pointer(1),
					CreatedAt:              now,
				}}},
				ResumeCursor: "cursor-final",
			},
			{ResumeCursor: "cursor-final"},
		},
		states: []bool{true, true, false, false},
	}
	redisServer := miniredis.RunT(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := liveevents.NewRedis(redis.NewClient(&redis.Options{Addr: redisServer.Addr()}), 2, logger)
	t.Cleanup(func() { _ = bus.Close() })
	server := newStreamTestServer(t, runtime, bus, StreamConfig{
		PollInterval:      25 * time.Millisecond,
		KeepaliveInterval: time.Second,
		MaxLifetime:       time.Second,
		WriteTimeout:      100 * time.Millisecond,
	})
	result := make(chan string, 1)
	go func() { result <- readAuthenticatedStream(t, server.URL) }()
	<-firstDrain
	redisServer.Close()
	text := <-result
	for _, fragment := range []string{`"text":"from-postgres"`, `"status":"completed"`, `"reason":"terminal"`} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("Redis-outage stream lacks %q: %s", fragment, text)
		}
	}
}

func TestTranscriptStreamValidatesBeforeCommittingSSEHeaders(t *testing.T) {
	runtime := &scriptedStreamRuntime{
		fakeRuntime: &fakeRuntime{}, snapshots: []services.TranscriptSnapshot{{}}, states: []bool{true},
		transcriptErr: &services.PublicError{Code: services.CodeInvalidRequest, Message: "cursor is invalid."},
	}
	server := newStreamTestServer(t, runtime, liveevents.NewInProcess(1), StreamConfig{})
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/sessions/"+testSessionID+"/transcript/stream?cursor=invalid", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("invalid stream = %d %q: %s", response.StatusCode, response.Header.Get("Content-Type"), body)
	}

	blankHeader, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/sessions/"+testSessionID+"/transcript/stream", nil)
	blankHeader.Header.Set("Authorization", "Bearer test-token")
	blankHeader.Header["Last-Event-ID"] = []string{" "}
	blankResponse, err := http.DefaultClient.Do(blankHeader)
	if err != nil {
		t.Fatal(err)
	}
	_ = blankResponse.Body.Close()
	if blankResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("blank Last-Event-ID status = %d", blankResponse.StatusCode)
	}
}

func TestStreamWriteStopsAtConfiguredDeadline(t *testing.T) {
	writer := &deadlineResponseWriter{header: make(http.Header)}
	started := time.Now()
	err := writeSSEControl(writer, 20*time.Millisecond, "event: test\ndata: {}\n\n")
	if err == nil {
		t.Fatal("writeSSEControl error = nil, want blocked write deadline")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("blocked write took %s, want bounded close", elapsed)
	}
}

func TestGenerationDeltaValidationBindsPayloadToStream(t *testing.T) {
	valid := domain.GenerationDeltaEvent{
		Type:         domain.LiveEventOutputTextDelta,
		SessionID:    testSessionID,
		InvocationID: testInvocationID,
		Attempt:      2,
		Iteration:    3,
		ContentIndex: 0,
		Text:         "ok",
		EmittedAt:    time.Now().UTC(),
	}
	if !validGenerationDeltaEvent(valid, testSessionID, testInvocationID) {
		t.Fatal("valid generation delta was rejected")
	}
	valid.SessionID = "sesn_019b0a12-0000-7000-8000-000000000099"
	if validGenerationDeltaEvent(valid, testSessionID, testInvocationID) {
		t.Fatal("cross-Session generation delta was accepted")
	}
}

type deadlineResponseWriter struct {
	header   http.Header
	deadline time.Time
}

func (w *deadlineResponseWriter) Header() http.Header { return w.header }
func (w *deadlineResponseWriter) WriteHeader(int)     {}
func (w *deadlineResponseWriter) SetWriteDeadline(value time.Time) error {
	w.deadline = value
	return nil
}
func (w *deadlineResponseWriter) Write([]byte) (int, error) {
	if delay := time.Until(w.deadline); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
	}
	return 0, context.DeadlineExceeded
}

func newStreamTestServer(t *testing.T, runtime RuntimeService, bus ports.LiveEventBus, config StreamConfig) *httptest.Server {
	return newStreamTestServerWithShutdown(t, runtime, bus, config, context.Background())
}

func newStreamTestServerWithShutdown(
	t *testing.T,
	runtime RuntimeService,
	bus ports.LiveEventBus,
	config StreamConfig,
	shutdown context.Context,
) *httptest.Server {
	t.Helper()
	authenticator := &fakeAuthenticator{auth: domain.RuntimeAuthContext{
		AccountID: testAccountID,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationCreateInvocation: {},
			domain.OperationGetInvocation:    {},
			domain.OperationGetTranscript:    {},
		},
	}}
	handler := newHandler(handlerConfig{
		authenticator:  authenticator,
		runtime:        runtime,
		liveEvents:     bus,
		stream:         config,
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		streamShutdown: shutdown,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func readAuthenticatedStream(t *testing.T, baseURL string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+"/v1/sessions/"+testSessionID+"/transcript/stream", nil)
	if err != nil {
		t.Errorf("build stream request: %v", err)
		return ""
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("open stream: %v", err)
		return ""
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Errorf("read stream: %v", err)
	}
	return string(body)
}

func assertNoIDOnEvent(t *testing.T, stream, event string) {
	t.Helper()
	for _, frame := range strings.Split(stream, "\n\n") {
		if strings.Contains(frame, "event: "+event) && strings.Contains(frame, "id:") {
			t.Fatalf("event %s unexpectedly has an id: %s", event, frame)
		}
	}
}

func streamStringPointer(value string) *string     { return &value }
func streamTimePointer(value time.Time) *time.Time { return &value }
