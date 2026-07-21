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
	admitInput services.CreateInvocationInput
	admitCalls int
	ack        services.InvocationAcknowledgement
	invocation services.InvocationRead
	session    services.SessionRead
	err        error
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

func (f *fakeRuntime) GetSession(context.Context, domain.RuntimeAuthContext, string) (services.SessionRead, error) {
	return f.session, f.err
}

func TestHealthzIsPublic(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	testHandler(nil, nil, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok" {
		t.Fatalf("GET /healthz = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestServerTimeoutsAreBounded(t *testing.T) {
	server := NewServer(Config{})
	if server.http.ReadHeaderTimeout <= 0 || server.http.ReadTimeout <= 0 ||
		server.http.WriteTimeout <= 0 || server.http.IdleTimeout <= 0 {
		t.Fatalf("server timeouts = header %s, read %s, write %s, idle %s",
			server.http.ReadHeaderTimeout, server.http.ReadTimeout,
			server.http.WriteTimeout, server.http.IdleTimeout)
	}
	if server.http.WriteTimeout <= server.http.ReadTimeout {
		t.Fatalf("write timeout %s must leave time after read timeout %s", server.http.WriteTimeout, server.http.ReadTimeout)
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
		{name: "wrong method", method: http.MethodPost, target: "/healthz", code: "invalid_request", allow: http.MethodGet, status: http.StatusMethodNotAllowed},
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

func TestInvocationRequestRejectsInvalidJSONBeforeService(t *testing.T) {
	valid := validInvocationJSON()
	oversized := `{"agent_ref":"` + strings.Repeat("a", services.MaxInvocationBodyBytes) + `"}`
	cases := map[string][]byte{
		"unknown field":  bytes.Replace(valid, []byte(`"spec":{`), []byte(`"unknown":true,"spec":{`), 1),
		"deferred tools": bytes.Replace(valid, []byte(`"instructions":"help",`), []byte(`"instructions":"help","tools":[],`), 1),
		"duplicate key":  bytes.Replace(valid, []byte(`"provider":"anthropic"`), []byte(`"provider":"anthropic","provider":"openai"`), 1),
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
			Status: domain.InvocationQueued, CreatedAt: now, UpdatedAt: now,
		},
		session: services.SessionRead{
			ID: testSessionID, AgentID: testAgentID, TenantRef: &tenant, SessionKey: &key,
			ActiveInvocationID: &active, CreatedAt: now, UpdatedAt: now,
		},
	}
	for path, required := range map[string][]string{
		"/v1/invocations/" + testInvocationID: {`"id":"` + testInvocationID + `"`, `"error":null`, `"completed_at":null`},
		"/v1/sessions/" + testSessionID:       {`"tenant_ref":"tenant-a"`, `"active_invocation_id":"` + testInvocationID + `"`},
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
				domain.OperationCreateInvocation: {}, domain.OperationGetInvocation: {}, domain.OperationGetSession: {},
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
