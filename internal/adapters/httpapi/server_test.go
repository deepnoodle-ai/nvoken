package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

const (
	testAccountID    = "acct_019b0a12-0000-7000-8000-000000000001"
	testAgentID      = "agnt_019b0a12-0000-7000-8000-000000000002"
	testSessionID    = "sesn_019b0a12-0000-7000-8000-000000000003"
	testInvocationID = "invk_019b0a12-0000-7000-8000-000000000004"
)

type fakeAuthenticator struct {
	auth domain.RuntimeAuthContext
	err  error
}

func (a fakeAuthenticator) Authenticate(context.Context, string) (domain.RuntimeAuthContext, error) {
	return a.auth, a.err
}

type fakeRuntime struct {
	admitInput       services.CreateInvocationInput
	toolResultsInput services.SubmitClientToolResultsInput
	admitCalls       int
	cancelCalls      int
	toolResultCalls  int
	ack              services.InvocationAcknowledgement
	toolResults      services.SubmitClientToolResultsResult
	invocation       services.InvocationRead
	session          services.SessionRead
	invocations      services.InvocationList
	sessions         services.SessionList
	messages         services.SessionMessageList
	transcript       services.TranscriptSnapshot
	err              error
}

type operationCheckingRuntime struct{ fakeRuntime }

func (r *operationCheckingRuntime) Admit(_ context.Context, auth domain.RuntimeAuthContext, _ services.CreateInvocationInput) (services.InvocationAcknowledgement, error) {
	if !auth.Allows(domain.OperationCreateInvocation) {
		return services.InvocationAcknowledgement{}, &services.PublicError{
			Code: services.CodeForbidden, Message: "The authenticated credential is not permitted to make this request.",
		}
	}
	return r.ack, nil
}

func (f *fakeRuntime) Admit(_ context.Context, _ domain.RuntimeAuthContext, input services.CreateInvocationInput) (services.InvocationAcknowledgement, error) {
	f.admitCalls++
	f.admitInput = input
	return f.ack, f.err
}

func (f *fakeRuntime) GetInvocation(context.Context, domain.RuntimeAuthContext, string) (services.InvocationRead, error) {
	return f.invocation, f.err
}

func (f *fakeRuntime) CancelInvocation(context.Context, domain.RuntimeAuthContext, string) (services.InvocationRead, error) {
	f.cancelCalls++
	return f.invocation, f.err
}

func (f *fakeRuntime) SubmitClientToolResults(
	_ context.Context,
	_ domain.RuntimeAuthContext,
	_ string,
	input services.SubmitClientToolResultsInput,
) (services.SubmitClientToolResultsResult, error) {
	f.toolResultCalls++
	f.toolResultsInput = input
	return f.toolResults, f.err
}

func (f *fakeRuntime) GetSession(context.Context, domain.RuntimeAuthContext, string) (services.SessionRead, error) {
	return f.session, f.err
}

func (f *fakeRuntime) ListInvocations(context.Context, domain.RuntimeAuthContext, services.InvocationListInput) (services.InvocationList, error) {
	return f.invocations, f.err
}

func (f *fakeRuntime) ListSessions(context.Context, domain.RuntimeAuthContext, services.SessionListInput) (services.SessionList, error) {
	return f.sessions, f.err
}

func (f *fakeRuntime) ListSessionMessages(context.Context, domain.RuntimeAuthContext, string, services.MessageListInput) (services.SessionMessageList, error) {
	return f.messages, f.err
}

func (f *fakeRuntime) GetSessionTranscript(context.Context, domain.RuntimeAuthContext, string, services.TranscriptInput) (services.TranscriptSnapshot, error) {
	return f.transcript, f.err
}

func (f *fakeRuntime) GetSessionTranscriptStreamState(context.Context, domain.RuntimeAuthContext, string) (services.TranscriptStreamState, error) {
	return services.TranscriptStreamState{Active: f.session.ActiveInvocationID != nil}, f.err
}

func TestHealthIsPublic(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	testHandler(nil, nil, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok" {
		t.Fatalf("GET /health = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestCancelInvocationRequiresEmptyBodyAndReturnsAuthoritativeRow(t *testing.T) {
	runtime := &fakeRuntime{invocation: services.InvocationRead{
		ID: testInvocationID, AgentID: testAgentID, SessionID: testSessionID,
		Status: domain.InvocationCancelled, Budgets: services.InvocationBudgetRead{
			WallClockTimeoutSeconds: 1800, ActiveExecutionTimeoutSeconds: 1800, MaxIterations: 1,
		}, WallClockDeadlineAt: time.Date(2026, 7, 21, 12, 30, 0, 0, time.UTC),
	}}
	handler := testHandler(runtime, nil, io.Discard)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/v1/invocations/"+testInvocationID+"/cancel", nil))
	if recorder.Code != http.StatusOK || runtime.cancelCalls != 1 {
		t.Fatalf("cancel = %d %s, calls = %d", recorder.Code, recorder.Body.String(), runtime.cancelCalls)
	}
	var response invocationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Status != domain.InvocationCancelled {
		t.Fatalf("cancel response = %#v, error = %v", response, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/v1/invocations/"+testInvocationID+"/cancel", []byte(`{}`)))
	if recorder.Code != http.StatusBadRequest || runtime.cancelCalls != 1 {
		t.Fatalf("cancel with body = %d %s, calls = %d", recorder.Code, recorder.Body.String(), runtime.cancelCalls)
	}
}

func TestServerTimeoutsAreBounded(t *testing.T) {
	server := NewServer(Config{})
	if server.http.ReadHeaderTimeout <= 0 || server.http.ReadTimeout <= 0 || server.http.IdleTimeout <= 0 {
		t.Fatalf("server timeouts = header %s, read %s, write %s, idle %s",
			server.http.ReadHeaderTimeout, server.http.ReadTimeout,
			server.http.WriteTimeout, server.http.IdleTimeout)
	}
	if server.http.WriteTimeout != 0 {
		t.Fatalf("global write timeout = %s, want per-response deadlines", server.http.WriteTimeout)
	}
	if server.http.BaseContext != nil || server.cancelStreams == nil {
		t.Fatal("shutdown must cancel only stream handlers, not every request context")
	}
	if server.shutdownTimeout != defaultShutdownTimeout {
		t.Fatalf("shutdown timeout = %s, want %s", server.shutdownTimeout, defaultShutdownTimeout)
	}
	configured := NewServer(Config{ShutdownTimeout: 8 * time.Second})
	if configured.shutdownTimeout != 8*time.Second {
		t.Fatalf("configured shutdown timeout = %s", configured.shutdownTimeout)
	}
}

func TestRoutingErrorsUseContractEnvelope(t *testing.T) {
	tests := []struct {
		name, method, target, code, allow string
		status                            int
	}{
		{name: "wrong health method", method: http.MethodPost, target: "/health", code: "invalid_request", allow: http.MethodGet, status: http.StatusMethodNotAllowed},
		{name: "unknown route", method: http.MethodGet, target: "/v1/unknown", code: "not_found", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.target, nil)

			testHandler(nil, nil, io.Discard).ServeHTTP(recorder, request)

			if recorder.Code != test.status || recorder.Header().Get("Allow") != test.allow {
				t.Fatalf("status = %d, allow = %q, body = %s", recorder.Code, recorder.Header().Get("Allow"), recorder.Body.String())
			}
			assertErrorEnvelope(t, recorder.Body.Bytes(), test.code)
		})
	}
}

func TestCreateInvocationReturnsDurableAcknowledgement(t *testing.T) {
	runtime := &fakeRuntime{ack: services.InvocationAcknowledgement{
		AgentID: testAgentID, SessionID: testSessionID, InvocationID: testInvocationID,
		Status: domain.InvocationQueued,
	}}
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/invocations", validInvocationJSON())

	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response invocationAcknowledgementResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode acknowledgement: %v", err)
	}
	if response.InvocationID != testInvocationID || response.Status != domain.InvocationQueued || runtime.admitCalls != 1 {
		t.Fatalf("response = %#v, calls = %d", response, runtime.admitCalls)
	}
	if runtime.admitInput.AgentRef != "support" || runtime.admitInput.Spec.Model.Provider != "anthropic" {
		t.Fatalf("decoded input = %#v", runtime.admitInput)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID")
	}
}

func TestCreateInvocationDecodesStructuredOutputContract(t *testing.T) {
	runtime := &fakeRuntime{
		ack: services.InvocationAcknowledgement{
			AgentID:      testAgentID,
			SessionID:    testSessionID,
			InvocationID: testInvocationID,
			Status:       domain.InvocationQueued,
		},
	}
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/invocations", structuredInvocationJSON())

	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted || runtime.admitCalls != 1 {
		t.Fatalf("structured admission = %d, calls = %d, body = %s", recorder.Code, runtime.admitCalls, recorder.Body.String())
	}
	if runtime.admitInput.Spec.Output == nil ||
		string(runtime.admitInput.Spec.Output.Schema) != `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}` {
		t.Fatalf("decoded output = %#v", runtime.admitInput.Spec.Output)
	}
}

func TestCreateInvocationDecodesClientToolContract(t *testing.T) {
	runtime := &fakeRuntime{
		ack: services.InvocationAcknowledgement{
			AgentID:      testAgentID,
			SessionID:    testSessionID,
			InvocationID: testInvocationID,
			Status:       domain.InvocationQueued,
		},
	}
	body := []byte(`{
		"agent_ref":"support",
		"idempotency_key":"request-1",
		"input":{"content":[{"type":"text","text":"look it up"}]},
		"spec":{
			"instructions":"help",
			"model":{"provider":"anthropic","name":"test-model"},
			"tools":[{
				"name":"lookup_order",
				"description":"Look up an order",
				"mode":"client",
				"input_schema":{"type":"object","properties":{"order_id":{"type":"string"}},"additionalProperties":false}
			}]
		}
	}`)
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/invocations", body)

	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted || runtime.admitCalls != 1 {
		t.Fatalf("client tool admission = %d, calls = %d, body = %s", recorder.Code, runtime.admitCalls, recorder.Body.String())
	}
	if len(runtime.admitInput.Spec.Tools) != 1 || runtime.admitInput.Spec.Tools[0].Name != "lookup_order" {
		t.Fatalf("decoded client tools = %#v", runtime.admitInput.Spec.Tools)
	}
}

func TestSubmitClientToolResultsReturnsDurableAcknowledgement(t *testing.T) {
	deadline := time.Date(2026, 7, 21, 12, 30, 0, 0, time.UTC)
	toolCallID := "tcal_019f84a5-7838-7b57-a180-000000000001"
	runtime := &fakeRuntime{
		toolResults: services.SubmitClientToolResultsResult{
			InvocationID: testInvocationID,
			SessionID:    testSessionID,
			Status:       domain.InvocationWaiting,
			Results: []services.ClientToolResultAcceptance{
				{
					ToolCallID: toolCallID,
					Status:     domain.ToolCallCompleted,
				},
			},
			PendingToolCalls: []services.PendingClientToolCall{
				{
					ID:         "tcal_019f84a5-7838-7b57-a180-000000000002",
					Name:       "notify_user",
					Input:      json.RawMessage(`{"message":"hello"}`),
					DeadlineAt: deadline,
				},
			},
		},
	}
	body := []byte(`{"results":[{"tool_call_id":"` + toolCallID + `","content":{"order":"ready"}}]}`)
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/invocations/"+testInvocationID+"/tool-results",
		body,
	)

	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted || runtime.toolResultCalls != 1 {
		t.Fatalf("tool results = %d, calls = %d, body = %s", recorder.Code, runtime.toolResultCalls, recorder.Body.String())
	}
	if len(runtime.toolResultsInput.Results) != 1 ||
		string(runtime.toolResultsInput.Results[0].Content) != `{"order":"ready"}` {
		t.Fatalf("decoded tool results = %#v", runtime.toolResultsInput)
	}
	if !strings.Contains(recorder.Body.String(), `"status":"waiting"`) ||
		!strings.Contains(recorder.Body.String(), `"pending_tool_calls"`) {
		t.Fatalf("tool result response = %s", recorder.Body.String())
	}
}

func TestSubmitClientToolResultsRejectsMalformedJSONBeforeService(t *testing.T) {
	toolCallID := "tcal_019f84a5-7838-7b57-a180-000000000001"
	tests := map[string][]byte{
		"unknown field":    []byte(`{"results":[{"tool_call_id":"` + toolCallID + `","content":{},"unknown":true}]}`),
		"duplicate member": []byte(`{"results":[{"tool_call_id":"` + toolCallID + `","content":{},"content":true}]}`),
		"trailing value":   []byte(`{"results":[{"tool_call_id":"` + toolCallID + `","content":{}}]} {}`),
		"too deep": []byte(
			`{"results":[{"tool_call_id":"` + toolCallID + `","content":` +
				strings.Repeat("[", maxJSONNestingDepth+1) +
				"0" +
				strings.Repeat("]", maxJSONNestingDepth+1) +
				`}]}`,
		),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			runtime := &fakeRuntime{}
			recorder := httptest.NewRecorder()
			request := authenticatedRequest(
				http.MethodPost,
				"/v1/invocations/"+testInvocationID+"/tool-results",
				body,
			)
			testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || runtime.toolResultCalls != 0 {
				t.Fatalf("malformed result = %d, calls = %d, body = %s", recorder.Code, runtime.toolResultCalls, recorder.Body.String())
			}
		})
	}
}

func TestInvocationReadAndTranscriptProjectStructuredOutput(t *testing.T) {
	output := json.RawMessage(`{"answer":"yes"}`)
	provenance := json.RawMessage(`{"source":"tool_call","tool_call_id":"tcal_019f84a5-7838-7b57-a180-000000000001","schema_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	runtime := &fakeRuntime{
		invocation: services.InvocationRead{
			ID:               testInvocationID,
			AgentID:          testAgentID,
			SessionID:        testSessionID,
			Status:           domain.InvocationCompleted,
			Output:           output,
			OutputProvenance: provenance,
			Budgets: services.InvocationBudgetRead{
				WallClockTimeoutSeconds:       1800,
				ActiveExecutionTimeoutSeconds: 1800,
				MaxIterations:                 3,
			},
			WallClockDeadlineAt: now.Add(time.Hour),
			CreatedAt:           now,
			UpdatedAt:           now,
			CompletedAt:         &now,
		},
		transcript: services.TranscriptSnapshot{
			InvocationChanges: []domain.InvocationLifecycleChange{
				{
					InvocationState: domain.InvocationState{
						InvocationID: testInvocationID,
						Revision:     2,
						Status:       domain.InvocationCompleted,
						CreatedAt:    now,
					},
					Output:           output,
					OutputProvenance: provenance,
				},
			},
			ResumeCursor: "cursor",
		},
	}
	handler := testHandler(runtime, nil, io.Discard)

	for _, target := range []string{
		"/v1/invocations/" + testInvocationID,
		"/v1/sessions/" + testSessionID + "/transcript",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", target, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"output":{"answer":"yes"}`) ||
			!strings.Contains(recorder.Body.String(), `"source":"tool_call"`) {
			t.Fatalf("GET %s omitted structured output: %s", target, recorder.Body.String())
		}
	}
}

func TestInvocationRequestRejectsInvalidJSONBeforeService(t *testing.T) {
	valid := validInvocationJSON()
	oversized := `{"agent_ref":"` + strings.Repeat("a", services.MaxInvocationBodyBytes) + `"}`
	cases := map[string][]byte{
		"unknown field": bytes.Replace(valid, []byte(`"spec":{`), []byte(`"unknown":true,"spec":{`), 1),
		"callback missing target": bytes.Replace(
			valid,
			[]byte(`"instructions":"help",`),
			[]byte(`"instructions":"help","tools":[{"name":"lookup","description":"Look up","mode":"callback","input_schema":{"type":"object"}}],`),
			1,
		),
		"unknown callback field": bytes.Replace(
			valid,
			[]byte(`"instructions":"help",`),
			[]byte(`"instructions":"help","tools":[{"name":"lookup","description":"Look up","mode":"callback","input_schema":{"type":"object"},"callback":{"url":"https://callbacks.example.test/tool","unknown":true}}],`),
			1,
		),
		"client callback null": bytes.Replace(
			valid,
			[]byte(`"instructions":"help",`),
			[]byte(`"instructions":"help","tools":[{"name":"lookup","description":"Look up","mode":"client","input_schema":{"type":"object"},"callback":null}],`),
			1,
		),
		"duplicate key": bytes.Replace(valid, []byte(`"provider":"anthropic"`), []byte(`"provider":"anthropic","provider":"openai"`), 1),
		"duplicate schema key": bytes.Replace(
			structuredInvocationJSON(),
			[]byte(`"type":"object","properties"`),
			[]byte(`"type":"object","type":"array","properties"`),
			1,
		),
		"trailing value": append(append([]byte{}, valid...), []byte(` {}`)...),
		"null optional":  bytes.Replace(valid, []byte(`"agent_ref":"support",`), []byte(`"agent_ref":"support","tenant_ref":null,`), 1),
		"two selectors":  bytes.Replace(valid, []byte(`"idempotency_key":`), []byte(`"session_id":"`+testSessionID+`","session_key":"ticket","idempotency_key":`), 1),
		"lone surrogate": bytes.Replace(valid, []byte("private caller text"), append([]byte("private "), []byte{92, 'u', 'd', '8', '0', '0'}...), 1),
		"invalid utf8":   append(append([]byte{}, valid[:len(valid)-1]...), 0xff, '}'),
		"deep nesting":   []byte(`{"x":` + strings.Repeat("[", maxJSONNestingDepth+1) + `0` + strings.Repeat("]", maxJSONNestingDepth+1) + `}`),
		"oversized":      []byte(oversized),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			runtime := &fakeRuntime{}
			recorder := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, "/v1/invocations", body)
			testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			if runtime.admitCalls != 0 {
				t.Fatalf("admission called %d times", runtime.admitCalls)
			}
			assertErrorEnvelope(t, recorder.Body.Bytes(), "invalid_request")
		})
	}
}

func TestInvocationBodySizeBoundary(t *testing.T) {
	base := validInvocationJSON()
	marker := []byte("private caller text")
	fillerLength := services.MaxInvocationBodyBytes - (len(base) - len(marker))
	if fillerLength <= 0 {
		t.Fatal("valid fixture is unexpectedly larger than the request limit")
	}
	exact := bytes.Replace(base, marker, bytes.Repeat([]byte("x"), fillerLength), 1)
	if len(exact) != services.MaxInvocationBodyBytes {
		t.Fatalf("boundary fixture is %d bytes", len(exact))
	}

	runtime := &fakeRuntime{ack: services.InvocationAcknowledgement{
		AgentID: testAgentID, SessionID: testSessionID, InvocationID: testInvocationID, Status: domain.InvocationQueued,
	}}
	recorder := httptest.NewRecorder()
	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/v1/invocations", exact))
	if recorder.Code != http.StatusAccepted || runtime.admitCalls != 1 {
		t.Fatalf("exact-limit request = %d, calls = %d, body = %s", recorder.Code, runtime.admitCalls, recorder.Body.String())
	}

	runtime = &fakeRuntime{}
	recorder = httptest.NewRecorder()
	over := append(append([]byte{}, exact...), ' ')
	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/v1/invocations", over))
	if recorder.Code != http.StatusBadRequest || runtime.admitCalls != 0 {
		t.Fatalf("over-limit request = %d, calls = %d, body = %s", recorder.Code, runtime.admitCalls, recorder.Body.String())
	}
}

func TestRuntimeRoutesRequireOneBearerCredential(t *testing.T) {
	for _, header := range []string{"", "Basic abc", "Bearer", "Bearer wrong token"} {
		t.Run(header, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/v1/invocations/"+testInvocationID, nil)
			if header != "" {
				request.Header.Set("Authorization", header)
			}
			testHandler(&fakeRuntime{}, &fakeAuthenticator{err: context.Canceled}, io.Discard).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			assertErrorEnvelope(t, recorder.Body.Bytes(), "unauthenticated")
		})
	}
}

func TestCreateInvocationRejectsCredentialWithoutOperation(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/invocations", validInvocationJSON())
	authenticator := &fakeAuthenticator{auth: domain.RuntimeAuthContext{AccountID: testAccountID}}

	testHandler(&operationCheckingRuntime{}, authenticator, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	assertErrorEnvelope(t, recorder.Body.Bytes(), string(services.CodeForbidden))
}

func TestRuntimePublicErrorsHaveStableHTTPMappings(t *testing.T) {
	tests := []struct {
		name   string
		code   services.ErrorCode
		status int
	}{
		{name: "invalid request", code: services.CodeInvalidRequest, status: http.StatusBadRequest},
		{name: "forbidden", code: services.CodeForbidden, status: http.StatusForbidden},
		{name: "not found", code: services.CodeNotFound, status: http.StatusNotFound},
		{name: "idempotency conflict", code: services.CodeIdempotencyConflict, status: http.StatusConflict},
		{name: "active invocation", code: services.CodeSessionInvocationActive, status: http.StatusConflict},
		{
			name:   "invocation not waiting",
			code:   services.CodeInvocationNotWaiting,
			status: http.StatusConflict,
		},
		{
			name:   "tool result conflict",
			code:   services.CodeToolResultConflict,
			status: http.StatusConflict,
		},
		{
			name:   "tool result expired",
			code:   services.CodeToolResultExpired,
			status: http.StatusConflict,
		},
		{name: "unavailable", code: services.CodeUnavailable, status: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &fakeRuntime{err: &services.PublicError{Code: test.code, Message: "stable message"}}
			recorder := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, "/v1/invocations", validInvocationJSON())

			testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.status, recorder.Body.String())
			}
			assertErrorEnvelope(t, recorder.Body.Bytes(), string(test.code))
		})
	}
}

func TestAuthoritativeReadsUseContractShapes(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	tenant, key, active := "tenant-a", "ticket-1", testInvocationID
	runtime := &fakeRuntime{
		invocation: services.InvocationRead{
			ID: testInvocationID, AgentID: testAgentID, SessionID: testSessionID,
			Status:     domain.InvocationCompleted,
			Usage:      json.RawMessage(`{"input_tokens":2,"output_tokens":1}`),
			Provenance: json.RawMessage(`{"provider":"anthropic","requested_model":"requested","served_model":"served","credential_source":"installation_byok"}`),
			CreatedAt:  now, UpdatedAt: now, CompletedAt: &now,
		},
		session: services.SessionRead{
			ID: testSessionID, AgentID: testAgentID, TenantRef: &tenant, SessionKey: &key,
			ActiveInvocationID: &active, ActiveInvocationStatus: statusPointer(domain.InvocationRunning),
			CreatedAt: now, UpdatedAt: now,
		},
	}
	for path, required := range map[string][]string{
		"/v1/invocations/" + testInvocationID: {`"id":"` + testInvocationID + `"`, `"input_tokens":2`, `"provider":"anthropic"`},
		"/v1/sessions/" + testSessionID:       {`"tenant_ref":"tenant-a"`, `"active_invocation_id":"` + testInvocationID + `"`, `"active_invocation_status":"running"`},
	} {
		recorder := httptest.NewRecorder()
		testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, recorder.Code, recorder.Body.String())
		}
		for _, fragment := range required {
			if !strings.Contains(recorder.Body.String(), fragment) {
				t.Errorf("GET %s body %s lacks %s", path, recorder.Body.String(), fragment)
			}
		}
	}
}

func TestRecoveryCollectionAndTranscriptShapes(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	tenant, key, next, active := "tenant-a", "ticket-1", "opaque-next", testInvocationID
	runtime := &fakeRuntime{
		invocations: services.InvocationList{
			Items: []services.InvocationRead{{
				ID: testInvocationID, AgentID: testAgentID, SessionID: testSessionID,
				Status: domain.InvocationQueued, CreatedAt: now, UpdatedAt: now,
			}}, HasMore: true, NextCursor: &next,
		},
		sessions: services.SessionList{Items: []services.SessionRead{{
			ID: testSessionID, AgentID: testAgentID, TenantRef: &tenant, SessionKey: &key,
			ActiveInvocationID: &active, ActiveInvocationStatus: statusPointer(domain.InvocationQueued),
			CreatedAt: now, UpdatedAt: now,
		}}},
		messages: services.SessionMessageList{Items: []domain.SessionMessage{{
			ID: "smsg_019b0a12-0000-7000-8000-000000000005", SessionID: testSessionID,
			AgentID: testAgentID, InvocationID: testInvocationID, Sequence: 1,
			Role: domain.MessageRoleUser, Content: json.RawMessage(`[{"type":"text","text":"hello"}]`), CreatedAt: now,
		}}},
		transcript: services.TranscriptSnapshot{
			Messages: []domain.SessionMessage{},
			InvocationChanges: []domain.InvocationLifecycleChange{{
				InvocationState: domain.InvocationState{
					InvocationID: testInvocationID, Revision: 2, Status: domain.InvocationCompleted,
					ThroughMessageSequence: int64Pointer(1), CreatedAt: now,
				},
				Usage: json.RawMessage(`{"input_tokens":2,"output_tokens":1}`),
			}},
			ResumeCursor: "opaque-resume",
		},
	}

	tests := map[string][]string{
		"/v1/invocations?limit=1&tenant_ref=tenant-a":   {`"items":[{"id":"` + testInvocationID + `"`, `"has_more":true`, `"next_cursor":"opaque-next"`},
		"/v1/sessions?default_tenant=false":             {`"active_invocation_status":"queued"`, `"has_more":false`},
		"/v1/sessions/" + testSessionID + "/messages":   {`"sequence":1`, `"text":"hello"`, `"next_cursor":null`},
		"/v1/sessions/" + testSessionID + "/transcript": {`"messages":[]`, `"revision":2`, `"input_tokens":2`, `"resume_cursor":"opaque-resume"`},
	}
	for path, fragments := range tests {
		recorder := httptest.NewRecorder()
		testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, recorder.Code, recorder.Body.String())
		}
		for _, fragment := range fragments {
			if !strings.Contains(recorder.Body.String(), fragment) {
				t.Errorf("GET %s body %s lacks %s", path, recorder.Body.String(), fragment)
			}
		}
	}
}

func TestRecoveryRoutesRejectAmbiguousQueriesBeforeService(t *testing.T) {
	for _, path := range []string{
		"/v1/invocations?limit=1&limit=2",
		"/v1/invocations?unknown=true",
		"/v1/invocations?default_tenant=1",
		"/v1/invocations?limit=0",
		"/v1/invocations?limit=201",
		"/v1/sessions?limit=not-a-number",
		"/v1/sessions/" + testSessionID + "/messages?cursor=",
		"/v1/sessions/" + testSessionID + "/transcript?page_token=a&page_token=b",
	} {
		recorder := httptest.NewRecorder()
		testHandler(&fakeRuntime{}, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d %s", path, recorder.Code, recorder.Body.String())
		}
		assertErrorEnvelope(t, recorder.Body.Bytes(), string(services.CodeInvalidRequest))
	}
}

func TestLogsDoNotContainCredentialsOrRequestContent(t *testing.T) {
	var logs bytes.Buffer
	runtime := &fakeRuntime{ack: services.InvocationAcknowledgement{
		AgentID: testAgentID, SessionID: testSessionID, InvocationID: testInvocationID, Status: domain.InvocationQueued,
	}}
	request := authenticatedRequest(http.MethodPost, "/v1/invocations", validInvocationJSON())
	testHandler(runtime, nil, &logs).ServeHTTP(httptest.NewRecorder(), request)
	for _, secret := range []string{"test-token", "private caller text", "help"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs contain %q: %s", secret, logs.String())
		}
	}
}

func testHandler(runtime RuntimeService, authenticator *fakeAuthenticator, logWriter io.Writer) http.Handler {
	if authenticator == nil {
		authenticator = &fakeAuthenticator{auth: domain.RuntimeAuthContext{
			AccountID: testAccountID,
			Operations: map[domain.RuntimeOperation]struct{}{
				domain.OperationCreateInvocation: {}, domain.OperationGetInvocation: {}, domain.OperationCancelInvocation: {}, domain.OperationSubmitToolResults: {}, domain.OperationGetSession: {},
				domain.OperationListInvocations: {}, domain.OperationListSessions: {},
				domain.OperationListMessages: {}, domain.OperationGetTranscript: {},
			},
		}}
	}
	logger := slog.New(slog.NewTextHandler(logWriter, nil))
	return newHandler(handlerConfig{authenticator: authenticator, runtime: runtime, logger: logger})
}

func authenticatedRequest(method, target string, body []byte) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-token")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func validInvocationJSON() []byte {
	return []byte(`{"agent_ref":"support","idempotency_key":"request-1","input":{"content":[{"type":"text","text":"private caller text"}]},"spec":{"instructions":"help","model":{"provider":"anthropic","name":"test-model"}}}`)
}

func structuredInvocationJSON() []byte {
	return []byte(`{"agent_ref":"support","idempotency_key":"request-1","input":{"content":[{"type":"text","text":"private caller text"}]},"spec":{"instructions":"help","model":{"provider":"anthropic","name":"test-model"},"output":{"schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}}}`)
}

func assertErrorEnvelope(t *testing.T, body []byte, code string) {
	t.Helper()
	var response errorResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode error response: %v (%s)", err, body)
	}
	if response.Code != code || response.RequestID == "" || response.Message == "" {
		t.Fatalf("error response = %#v", response)
	}
}

func statusPointer(value domain.InvocationStatus) *domain.InvocationStatus { return &value }

func int64Pointer(value int64) *int64 { return &value }
