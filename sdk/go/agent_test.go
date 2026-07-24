package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	agentTestAgentID   = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
	agentTestSessionID = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
	agentTestToolID    = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
)

func TestAgentFiveVerbsDispatchAndStructuredOutput(t *testing.T) {
	runtime := newAgentTestRuntime()
	server := httptest.NewServer(runtime)
	defer server.Close()
	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	var handlerCalls atomic.Int64
	agent, err := client.Agent(AgentOptions{
		AgentKey: "support",
		Spec: ExecutionSpec{
			Model: Model{
				Provider: "openai",
				ID:       "gpt-test",
			},
			Tools: []Tool{{
				Mode:        ToolModeHost,
				Name:        "weather",
				Description: "Weather lookup",
				InputSchema: map[string]any{"type": "object"},
				Handler: func(_ context.Context, input any) (any, error) {
					handlerCalls.Add(1)
					value, ok := input.(map[string]any)
					if !ok || value["city"] != "Paris" {
						t.Fatalf("unexpected tool input: %#v", input)
					}
					return map[string]any{"temperature": 21}, nil
				},
			}},
			OutputSchema: map[string]any{"type": "object"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handle, err := agent.Invoke(
		context.Background(),
		"invoke",
		AgentInvocationOptions{},
	)
	if err != nil || handle.InvocationID == "" {
		t.Fatalf("invoke: handle=%#v err=%v", handle, err)
	}

	var streamed []string
	_, err = agent.Stream(
		context.Background(),
		"stream",
		AgentInvocationOptions{},
		func(event AgentStreamEvent) error {
			streamed = append(streamed, event.Event.Type)
			return nil
		},
	)
	if err != nil || fmt.Sprint(streamed) != "[invocation.result]" {
		t.Fatalf("stream: events=%v err=%v", streamed, err)
	}

	result, err := agent.Run(
		context.Background(),
		"tool structured",
		AgentInvocationOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputText == nil || *result.OutputText != "hello" {
		t.Fatalf("run output: %#v", result)
	}
	var decoded struct {
		Answer string `json:"answer"`
	}
	decoded, err = DecodeStructuredOutput[struct {
		Answer string `json:"answer"`
	}](result)
	if err != nil || decoded.Answer != "world" {
		t.Fatalf("structured output: %#v err=%v", decoded, err)
	}
	if handlerCalls.Load() != 1 || runtime.toolSubmissions() != 1 {
		t.Fatalf(
			"tool dispatch calls=%d submissions=%d",
			handlerCalls.Load(),
			runtime.toolSubmissions(),
		)
	}

	text, err := agent.Text(
		context.Background(),
		"text",
		AgentInvocationOptions{},
	)
	if err != nil || text != "hello" {
		t.Fatalf("text=%q err=%v", text, err)
	}

	bound, err := agent.Session(SessionBinding{SessionKey: "customer-123"})
	if err != nil {
		t.Fatal(err)
	}
	text, err = bound.Text(
		context.Background(),
		"bound",
		AgentInvocationOptions{},
	)
	if err != nil || text != "hello" {
		t.Fatalf("bound text=%q err=%v", text, err)
	}
	if runtime.lastSessionKey() != "customer-123" {
		t.Fatalf("bound Session key = %q", runtime.lastSessionKey())
	}
}

func TestAgentMissingHandlerPolicyAndNoOutputKinds(t *testing.T) {
	runtime := newAgentTestRuntime()
	server := httptest.NewServer(runtime)
	defer server.Close()
	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	missingTool := Tool{
		Mode:        ToolModeHost,
		Name:        "weather",
		Description: "Weather lookup",
		InputSchema: map[string]any{"type": "object"},
	}
	agent, err := client.Agent(AgentOptions{
		AgentKey: "support",
		Spec: ExecutionSpec{
			Model: Model{
				Provider: "openai",
				ID:       "gpt-test",
			},
			Tools: []Tool{missingTool},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = agent.Run(
		context.Background(),
		"missing",
		AgentInvocationOptions{},
	)
	var cancelled *MissingToolHandlerError
	if !errors.As(err, &cancelled) || !cancelled.InvocationCancelled {
		t.Fatalf("default missing handler error: %#v", err)
	}
	if runtime.cancellations() != 1 {
		t.Fatalf("cancel count = %d, want 1", runtime.cancellations())
	}

	_, err = agent.Run(
		context.Background(),
		"missing opt-out",
		AgentInvocationOptions{LeaveWaitingOnMissingHandler: true},
	)
	var preserved *MissingToolHandlerError
	if !errors.As(err, &preserved) || preserved.InvocationCancelled {
		t.Fatalf("opt-out missing handler error: %#v", err)
	}
	if runtime.cancellations() != 1 {
		t.Fatalf("opt-out unexpectedly cancelled: %d", runtime.cancellations())
	}

	_, err = agent.Text(
		context.Background(),
		"structured-only",
		AgentInvocationOptions{},
	)
	var noText *NoOutputTextError
	if !errors.As(err, &noText) || noText.ResultKind != "structured output" {
		t.Fatalf("structured-only text error: %#v", err)
	}

	_, err = agent.Text(
		context.Background(),
		"tool-only",
		AgentInvocationOptions{},
	)
	if !errors.As(err, &noText) || noText.ResultKind != "tool-only output" {
		t.Fatalf("tool-only text error: %#v", err)
	}
}

func TestBoundSessionSerializesAdmission(t *testing.T) {
	runtime := newAgentTestRuntime()
	server := httptest.NewServer(runtime)
	defer server.Close()
	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := client.Agent(AgentOptions{
		AgentKey: "support",
		Spec: ExecutionSpec{Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := agent.Session(SessionBinding{SessionID: agentTestSessionID})
	if err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		result *AgentResult
		err    error
	}
	first := make(chan outcome, 1)
	second := make(chan outcome, 1)
	go func() {
		result, runErr := bound.Run(
			context.Background(),
			"slow first",
			AgentInvocationOptions{},
		)
		first <- outcome{result: result, err: runErr}
	}()
	runtime.waitForAdmissions(t, 1)
	go func() {
		result, runErr := bound.Run(
			context.Background(),
			"slow second",
			AgentInvocationOptions{},
		)
		second <- outcome{result: result, err: runErr}
	}()
	time.Sleep(20 * time.Millisecond)
	if runtime.admissions() != 1 {
		t.Fatalf(
			"second bound admission ran concurrently; admissions=%d",
			runtime.admissions(),
		)
	}
	runtime.releaseSlow()
	for index, channel := range []chan outcome{first, second} {
		select {
		case value := <-channel:
			if value.err != nil || value.result == nil {
				t.Fatalf("bound run %d: result=%#v err=%v", index, value.result, value.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("bound run %d did not finish", index)
		}
	}
	if runtime.admissions() != 2 {
		t.Fatalf("admissions=%d, want 2", runtime.admissions())
	}
}

func TestWaitOptionsOverallTimeoutAndCondition(t *testing.T) {
	runtime := newAgentTestRuntime()
	server := httptest.NewServer(runtime)
	defer server.Close()
	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := client.Agent(AgentOptions{
		AgentKey: "support",
		Spec: ExecutionSpec{Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := handle.Invoke(
		context.Background(),
		"missing",
		AgentInvocationOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := admitted.Wait(context.Background(), WaitOptions{
		Until:           WaitUntilActionable,
		MinPollInterval: time.Millisecond,
		MaxPollInterval: time.Millisecond,
	})
	if err != nil || invocation.Status != InvocationWaiting {
		t.Fatalf("actionable wait: invocation=%#v err=%v", invocation, err)
	}
	_, err = admitted.Wait(context.Background(), WaitOptions{
		Timeout:         time.Millisecond,
		MinPollInterval: time.Millisecond,
		MaxPollInterval: time.Millisecond,
	})
	var timeout *Error
	if !errors.As(err, &timeout) || timeout.Category != ErrorTimeout {
		t.Fatalf("overall wait timeout: %#v", err)
	}
}

type agentTestInvocation struct {
	input      string
	submitted  bool
	cancelled  bool
	sessionKey string
}

type agentTestRuntime struct {
	mu          sync.Mutex
	nextID      int
	invocations map[string]*agentTestInvocation
	submissions int
	cancelCount int
	slow        chan struct{}
	slowOnce    sync.Once
}

func newAgentTestRuntime() *agentTestRuntime {
	return &agentTestRuntime{
		invocations: make(map[string]*agentTestInvocation),
		slow:        make(chan struct{}),
	}
}

func (r *agentTestRuntime) ServeHTTP(
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.Path == "/v1/invocations" && request.Method == http.MethodPost {
		r.create(response, request)
		return
	}
	const prefix = "/v1/invocations/"
	if !strings.HasPrefix(request.URL.Path, prefix) {
		http.NotFound(response, request)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, prefix)
	switch {
	case strings.HasSuffix(path, "/stream") && request.Method == http.MethodGet:
		r.stream(
			response,
			request.Context(),
			strings.TrimSuffix(path, "/stream"),
		)
	case strings.HasSuffix(path, "/tool-results") &&
		request.Method == http.MethodPost:
		r.submit(response, strings.TrimSuffix(path, "/tool-results"))
	case strings.HasSuffix(path, "/cancel") && request.Method == http.MethodPost:
		r.cancel(response, strings.TrimSuffix(path, "/cancel"))
	case strings.HasSuffix(path, "/result") && request.Method == http.MethodGet:
		r.result(response, strings.TrimSuffix(path, "/result"))
	case request.Method == http.MethodGet:
		r.get(response, path)
	default:
		http.NotFound(response, request)
	}
}

func (r *agentTestRuntime) create(
	response http.ResponseWriter,
	request *http.Request,
) {
	var body struct {
		Input      string `json:"input"`
		SessionKey string `json:"session_key"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.nextID++
	id := fmt.Sprintf(
		"invk_019b0a12-8d51-7f34-aed2-%012x",
		r.nextID,
	)
	r.invocations[id] = &agentTestInvocation{
		input:      body.Input,
		sessionKey: body.SessionKey,
	}
	r.mu.Unlock()
	writeAgentTestJSON(response, http.StatusAccepted, map[string]any{
		"agent_id":      agentTestAgentID,
		"session_id":    agentTestSessionID,
		"invocation_id": id,
		"status":        "queued",
		"deduplicated":  false,
		"deadline_at":   "2026-07-21T12:05:00Z",
	})
}

func (r *agentTestRuntime) get(response http.ResponseWriter, id string) {
	state := r.state(id)
	status := "completed"
	if needsTool(state.input) && !state.submitted {
		status = "waiting"
	}
	if state.cancelled {
		status = "cancelled"
	}
	writeAgentTestJSON(
		response,
		http.StatusOK,
		agentTestInvocationPayload(id, status),
	)
}

func (r *agentTestRuntime) stream(
	response http.ResponseWriter,
	ctx context.Context,
	id string,
) {
	state := r.state(id)
	response.Header().Set("Content-Type", "text/event-stream")
	response.WriteHeader(http.StatusOK)
	flusher := response.(http.Flusher)
	if needsTool(state.input) {
		fmt.Fprint(response, "event: invocation.update\ndata: {}\n\n")
		flusher.Flush()
		deadline := time.Now().Add(time.Second)
		for {
			state = r.state(id)
			if state.submitted || state.cancelled || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
		if !state.submitted {
			return
		}
	}
	if strings.Contains(state.input, "slow") {
		<-r.slow
	}
	fmt.Fprint(response, "event: invocation.result\ndata: {}\n\n")
	flusher.Flush()
}

func (r *agentTestRuntime) submit(response http.ResponseWriter, id string) {
	r.mu.Lock()
	state := r.invocations[id]
	state.submitted = true
	r.submissions++
	r.mu.Unlock()
	writeAgentTestJSON(response, http.StatusAccepted, map[string]any{
		"invocation_id": id,
		"session_id":    agentTestSessionID,
		"status":        "queued",
		"results": []any{map[string]any{
			"tool_call_id": agentTestToolID,
			"status":       "completed",
			"deduplicated": false,
		}},
		"pending_tool_calls": []any{},
	})
}

func (r *agentTestRuntime) cancel(response http.ResponseWriter, id string) {
	r.mu.Lock()
	state := r.invocations[id]
	state.cancelled = true
	r.cancelCount++
	r.mu.Unlock()
	writeAgentTestJSON(
		response,
		http.StatusOK,
		agentTestInvocationPayload(id, "cancelled"),
	)
}

func (r *agentTestRuntime) result(response http.ResponseWriter, id string) {
	state := r.state(id)
	invocation := agentTestInvocationPayload(id, "completed")
	if strings.Contains(state.input, "structured") {
		invocation["structured_output"] = map[string]any{"answer": "world"}
	}
	var output any = "hello"
	if strings.Contains(state.input, "structured-only") ||
		strings.Contains(state.input, "tool-only") {
		output = nil
	}
	writeAgentTestJSON(response, http.StatusOK, map[string]any{
		"invocation":  invocation,
		"messages":    []any{},
		"output_text": output,
	})
}

func (r *agentTestRuntime) state(id string) agentTestInvocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return *r.invocations[id]
}

func (r *agentTestRuntime) admissions() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nextID
}

func (r *agentTestRuntime) toolSubmissions() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.submissions
}

func (r *agentTestRuntime) cancellations() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancelCount
}

func (r *agentTestRuntime) lastSessionKey() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var key string
	for _, state := range r.invocations {
		if state.sessionKey != "" {
			key = state.sessionKey
		}
	}
	return key
}

func (r *agentTestRuntime) waitForAdmissions(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for r.admissions() < count {
		if time.Now().After(deadline) {
			t.Fatalf("admissions=%d, want at least %d", r.admissions(), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *agentTestRuntime) releaseSlow() {
	r.slowOnce.Do(func() {
		close(r.slow)
	})
}

func needsTool(input string) bool {
	return input == "tool structured" || strings.Contains(input, "missing")
}

func agentTestInvocationPayload(id, status string) map[string]any {
	var endedAt any
	if status == "completed" || status == "cancelled" {
		endedAt = "2026-07-21T12:00:03Z"
	}
	value := map[string]any{
		"id":                           id,
		"agent_id":                     agentTestAgentID,
		"session_id":                   agentTestSessionID,
		"status":                       status,
		"error":                        nil,
		"usage":                        nil,
		"provenance":                   nil,
		"structured_output":            nil,
		"structured_output_provenance": nil,
		"limits": map[string]any{
			"total_timeout_seconds":   300,
			"active_timeout_seconds":  120,
			"waiting_timeout_seconds": 180,
			"max_iterations":          16,
		},
		"active_execution_ms": 250,
		"deadline_at":         "2026-07-21T12:05:00Z",
		"created_at":          "2026-07-21T12:00:00Z",
		"updated_at":          "2026-07-21T12:00:03Z",
		"ended_at":            endedAt,
	}
	if status == "waiting" {
		value["pending_tool_calls"] = []any{map[string]any{
			"id":          agentTestToolID,
			"name":        "weather",
			"input":       map[string]any{"city": "Paris"},
			"deadline_at": "2026-07-21T12:05:00Z",
		}}
	}
	return value
}

func writeAgentTestJSON(
	response http.ResponseWriter,
	status int,
	value any,
) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
