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
	toolResultsInput services.SubmitHostToolResultsInput
	admitCalls       int
	cancelCalls      int
	toolResultCalls  int
	ack              services.InvocationAcknowledgement
	toolResults      services.SubmitHostToolResultsResult
	invocation       services.InvocationRead
	invocationResult services.InvocationResultRead
	session          services.SessionRead
	invocations      services.InvocationList
	sessions         services.SessionList
	messages         services.SessionMessageList
	transcript       services.TranscriptSnapshot
	err              error
}

type fakeProviderCredentials struct {
	createInput services.CreateProviderCredentialInput
	created     services.ProviderCredentialRead
	createCalls int
}

type fakeModels struct {
	catalog           domain.ModelCatalog
	descriptor        domain.ModelDescriptor
	listProvider      domain.ModelProvider
	includeDeprecated bool
	resolvedProvider  domain.ModelProvider
	resolvedModel     string
}

func (f *fakeModels) ListModels(
	provider domain.ModelProvider,
	includeDeprecated bool,
) domain.ModelCatalog {
	f.listProvider = provider
	f.includeDeprecated = includeDeprecated
	return f.catalog
}

func (f *fakeModels) ResolveModel(
	provider domain.ModelProvider,
	model string,
) domain.ModelDescriptor {
	f.resolvedProvider = provider
	f.resolvedModel = model
	descriptor := f.descriptor
	descriptor.Provider = provider
	descriptor.ID = model
	descriptor.Pricing.Provider = provider
	descriptor.Pricing.Model = model
	return descriptor
}

func (f *fakeProviderCredentials) Create(
	_ context.Context,
	_ domain.RuntimeAuthContext,
	input services.CreateProviderCredentialInput,
) (services.ProviderCredentialRead, error) {
	f.createCalls++
	f.createInput = input
	return f.created, nil
}

func (f *fakeProviderCredentials) List(context.Context, domain.RuntimeAuthContext, services.ProviderCredentialListInput) (services.ProviderCredentialList, error) {
	return services.ProviderCredentialList{Items: []services.ProviderCredentialRead{}}, nil
}

func (f *fakeProviderCredentials) Get(context.Context, domain.RuntimeAuthContext, string) (services.ProviderCredentialRead, error) {
	return f.created, nil
}

func (f *fakeProviderCredentials) Rotate(context.Context, domain.RuntimeAuthContext, string, services.RotateProviderCredentialInput) (services.ProviderCredentialRead, error) {
	return f.created, nil
}

func (f *fakeProviderCredentials) Revoke(context.Context, domain.RuntimeAuthContext, string) (services.ProviderCredentialRead, error) {
	return f.created, nil
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

func (f *fakeRuntime) GetInvocationResult(context.Context, domain.RuntimeAuthContext, string) (services.InvocationResultRead, error) {
	return f.invocationResult, f.err
}

func (f *fakeRuntime) CancelInvocation(context.Context, domain.RuntimeAuthContext, string) (services.InvocationRead, error) {
	f.cancelCalls++
	return f.invocation, f.err
}

func (f *fakeRuntime) SubmitHostToolResults(
	_ context.Context,
	_ domain.RuntimeAuthContext,
	_ string,
	input services.SubmitHostToolResultsInput,
) (services.SubmitHostToolResultsResult, error) {
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
	var logs bytes.Buffer
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	testHandler(nil, nil, &logs).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != "ok" {
		t.Fatalf("GET /health = %d %q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(logs.String(), "event=http_request_completed") ||
		!strings.Contains(logs.String(), "outcome=success") ||
		!strings.Contains(logs.String(), "route=/health") {
		t.Fatalf("health request log = %s", logs.String())
	}
}

func TestListModelsFiltersAndSupportsConditionalReads(t *testing.T) {
	contextWindow := 1_000_000
	models := &fakeModels{catalog: domain.ModelCatalog{
		Items: []domain.ModelDescriptor{{
			Provider:            domain.ModelProviderOpenAI,
			ID:                  "gpt-test",
			Cataloged:           true,
			DisplayName:         "GPT Test",
			Description:         "A test model.",
			ContextWindowTokens: &contextWindow,
			InputModalities:     []string{"text"},
			Recommended:         true,
			Pricing: domain.ModelPricing{
				Status:         domain.ModelPricingPriced,
				Currency:       "USD",
				Unit:           "per_million_tokens",
				Input:          "1",
				Output:         "2",
				UpdatedAt:      "2026-07-23",
				PricingVersion: "pricing-1",
			},
		}},
		Version: "catalog-1",
	}}
	authenticator := &fakeAuthenticator{auth: domain.RuntimeAuthContext{AccountID: testAccountID}}
	handler := newHandler(handlerConfig{
		authenticator: authenticator,
		models:        models,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedRequest(
		http.MethodGet,
		"/v1/models?provider=openai&include_deprecated=true",
		nil,
	))
	if recorder.Code != http.StatusOK ||
		models.listProvider != domain.ModelProviderOpenAI ||
		!models.includeDeprecated {
		t.Fatalf(
			"model list = %d %s, provider = %q, include deprecated = %t",
			recorder.Code,
			recorder.Body.String(),
			models.listProvider,
			models.includeDeprecated,
		)
	}
	for _, expected := range []string{
		`"provider":"openai"`,
		`"id":"gpt-test"`,
		`"cataloged":true`,
		`"recommended":true`,
		`"deprecated":false`,
		`"pricing_version":"pricing-1"`,
		`"catalog_version":"catalog-1"`,
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("model list missing %s: %s", expected, recorder.Body.String())
		}
	}
	etag := recorder.Header().Get("ETag")
	if etag == "" {
		t.Fatal("model list has no ETag")
	}
	conditional := authenticatedRequest(http.MethodGet, "/v1/models?provider=openai&include_deprecated=true", nil)
	conditional.Header.Set("If-None-Match", "W/"+etag)
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional model list = %d %q", notModified.Code, notModified.Body.String())
	}
	models.catalog.Items[0].Description = "Changed metadata."
	changed := httptest.NewRecorder()
	handler.ServeHTTP(changed, authenticatedRequest(http.MethodGet, "/v1/models", nil))
	if changed.Header().Get("ETag") == etag {
		t.Fatal("model metadata change did not change the ETag")
	}
}

func TestGetModelRoundTripsEncodedModelIDAndOmitsUncatalogedMetadata(t *testing.T) {
	models := &fakeModels{descriptor: domain.ModelDescriptor{
		Cataloged: false,
		Pricing: domain.ModelPricing{
			Status:         domain.ModelPricingUnpriced,
			PricingVersion: "pricing-1",
		},
	}}
	handler := newHandler(handlerConfig{
		authenticator: &fakeAuthenticator{auth: domain.RuntimeAuthContext{AccountID: testAccountID}},
		models:        models,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedRequest(
		http.MethodGet,
		"/v1/models/openai/future%2Fmodel%3Fvariant%3D%E9%9B%AA%25",
		nil,
	))
	if recorder.Code != http.StatusOK ||
		models.resolvedProvider != domain.ModelProviderOpenAI ||
		models.resolvedModel != "future/model?variant=雪%" {
		t.Fatalf(
			"model descriptor = %d %s, provider = %q, model = %q",
			recorder.Code,
			recorder.Body.String(),
			models.resolvedProvider,
			models.resolvedModel,
		)
	}
	for _, expected := range []string{
		`"provider":"openai"`,
		`"id":"future/model?variant=雪%"`,
		`"cataloged":false`,
		`"status":"unpriced"`,
		`"pricing_version":"pricing-1"`,
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("model descriptor missing %s: %s", expected, recorder.Body.String())
		}
	}
	for _, omitted := range []string{"display_name", "recommended", "deprecated", "currency", "unit"} {
		if strings.Contains(recorder.Body.String(), omitted) {
			t.Fatalf("uncataloged model includes %q: %s", omitted, recorder.Body.String())
		}
	}
	etag := recorder.Header().Get("ETag")
	conditional := authenticatedRequest(
		http.MethodGet,
		"/v1/models/openai/future%2Fmodel%3Fvariant%3D%E9%9B%AA%25",
		nil,
	)
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional model descriptor = %d %q", notModified.Code, notModified.Body.String())
	}
}

func TestGetModelPreservesUnknownPricingShape(t *testing.T) {
	models := &fakeModels{descriptor: domain.ModelDescriptor{
		Pricing: domain.ModelPricing{
			Status:         domain.ModelPricingUnknown,
			PricingVersion: "pricing-unknown",
		},
	}}
	handler := newHandler(handlerConfig{
		authenticator: &fakeAuthenticator{auth: domain.RuntimeAuthContext{AccountID: testAccountID}},
		models:        models,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedRequest(
		http.MethodGet,
		"/v1/models/openai/adapter-undecidable",
		nil,
	))
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"status":"unknown"`) ||
		!strings.Contains(recorder.Body.String(), `"pricing_version":"pricing-unknown"`) {
		t.Fatalf("unknown model descriptor = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, omitted := range []string{"currency", "unit", "input", "output", "updated_at"} {
		if strings.Contains(recorder.Body.String(), omitted) {
			t.Fatalf("unknown pricing includes %q: %s", omitted, recorder.Body.String())
		}
	}
}

func TestModelsValidateAuthenticationAndInput(t *testing.T) {
	models := &fakeModels{}
	for _, test := range []struct {
		name   string
		target string
		auth   bool
		status int
	}{
		{name: "authentication", target: "/v1/models", status: http.StatusUnauthorized},
		{name: "list provider", target: "/v1/models?provider=other", auth: true, status: http.StatusBadRequest},
		{name: "list provider alias", target: "/v1/models?provider=open-ai", auth: true, status: http.StatusBadRequest},
		{name: "list blank provider", target: "/v1/models?provider=", auth: true, status: http.StatusBadRequest},
		{name: "list boolean", target: "/v1/models?include_deprecated=yes", auth: true, status: http.StatusBadRequest},
		{name: "list blank boolean", target: "/v1/models?include_deprecated=", auth: true, status: http.StatusBadRequest},
		{name: "list repeated query", target: "/v1/models?provider=openai&provider=openai", auth: true, status: http.StatusBadRequest},
		{name: "list unknown query", target: "/v1/models?extra=true", auth: true, status: http.StatusBadRequest},
		{name: "item provider", target: "/v1/models/other/gpt-test", auth: true, status: http.StatusBadRequest},
		{name: "item provider alias", target: "/v1/models/open-ai/gpt-test", auth: true, status: http.StatusBadRequest},
		{name: "item padded model", target: "/v1/models/openai/%20gpt-test", auth: true, status: http.StatusBadRequest},
		{name: "item unknown query", target: "/v1/models/openai/gpt-test?extra=true", auth: true, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			authenticator := &fakeAuthenticator{auth: domain.RuntimeAuthContext{AccountID: testAccountID}}
			handler := newHandler(handlerConfig{
				authenticator: authenticator,
				models:        models,
				logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			if test.auth {
				request.Header.Set("Authorization", "Bearer test-token")
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("GET %s = %d %s", test.target, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCancelInvocationRequiresEmptyBodyAndReturnsAuthoritativeRow(t *testing.T) {
	runtime := &fakeRuntime{invocation: services.InvocationRead{
		ID:        testInvocationID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		Status:    domain.InvocationCancelled,
		Limits: services.InvocationLimitRead{
			TotalTimeoutSeconds:  1800,
			ActiveTimeoutSeconds: 1800,
			MaxIterations:        1,
		},
		DeadlineAt: time.Date(2026, 7, 21, 12, 30, 0, 0, time.UTC),
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
	if runtime.admitInput.AgentKey != "support" || runtime.admitInput.Spec.Model.Provider != "anthropic" {
		t.Fatalf("decoded input = %#v", runtime.admitInput)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID")
	}
}

func TestCreateInvocationAcceptsStringInputAndOmittedInstructions(t *testing.T) {
	runtime := &fakeRuntime{ack: services.InvocationAcknowledgement{
		AgentID:      testAgentID,
		SessionID:    testSessionID,
		InvocationID: testInvocationID,
		Status:       domain.InvocationQueued,
	}}
	body := []byte(`{
		"agent_key":"support",
		"idempotency_key":"request-1",
		"input":"private caller text",
		"spec":{"model":{"provider":"anthropic","id":"test-model"}}
	}`)
	recorder := httptest.NewRecorder()

	testHandler(runtime, nil, io.Discard).ServeHTTP(
		recorder,
		authenticatedRequest(http.MethodPost, "/v1/invocations", body),
	)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(runtime.admitInput.Input.Content) != 1 ||
		runtime.admitInput.Input.Content[0].Text != "private caller text" ||
		runtime.admitInput.Spec.Instructions != "" {
		t.Fatalf("normalized input = %#v", runtime.admitInput)
	}
}

func TestCreateInvocationDecodesCredentialSelectionAndRejectsNull(t *testing.T) {
	runtime := &fakeRuntime{ack: services.InvocationAcknowledgement{
		AgentID:      testAgentID,
		SessionID:    testSessionID,
		InvocationID: testInvocationID,
		Status:       domain.InvocationQueued,
	}}
	body := []byte(`{"agent_key":"support","idempotency_key":"request-1","input":{"content":[{"type":"text","text":"hello"}]},"spec":{"instructions":"help","model":{"provider":"anthropic","id":"test-model"}},"provider_credentials":[{"provider":"anthropic","source":"caller_ephemeral","credential":{"api_key":"caller-secret"}}]}`)
	recorder := httptest.NewRecorder()
	testHandler(runtime, nil, io.Discard).ServeHTTP(
		recorder,
		authenticatedRequest(http.MethodPost, "/v1/invocations", body),
	)
	if recorder.Code != http.StatusAccepted || len(runtime.admitInput.ProviderCredentials) != 1 ||
		runtime.admitInput.ProviderCredentials[0].Credential == nil ||
		runtime.admitInput.ProviderCredentials[0].Credential.APIKey != "caller-secret" {
		t.Fatalf("credential admission = %d %s, input = %#v", recorder.Code, recorder.Body.String(), runtime.admitInput.ProviderCredentials)
	}

	nullBody := bytes.Replace(validInvocationJSON(), []byte(`"spec"`), []byte(`"provider_credentials":null,"spec"`), 1)
	recorder = httptest.NewRecorder()
	testHandler(runtime, nil, io.Discard).ServeHTTP(
		recorder,
		authenticatedRequest(http.MethodPost, "/v1/invocations", nullBody),
	)
	if recorder.Code != http.StatusBadRequest || runtime.admitCalls != 1 {
		t.Fatalf("null provider_credentials = %d %s, calls = %d", recorder.Code, recorder.Body.String(), runtime.admitCalls)
	}

	unusedNull := []byte(`{"agent_key":"support","idempotency_key":"request-2","input":{"content":[{"type":"text","text":"hello"}]},"spec":{"instructions":"help","model":{"provider":"anthropic","id":"test-model"}},"provider_credentials":[{"provider":"anthropic","source":"account_byok","credential":null}]}`)
	recorder = httptest.NewRecorder()
	testHandler(runtime, nil, io.Discard).ServeHTTP(
		recorder,
		authenticatedRequest(http.MethodPost, "/v1/invocations", unusedNull),
	)
	if recorder.Code != http.StatusBadRequest || runtime.admitCalls != 1 {
		t.Fatalf("unused null credential = %d %s, calls = %d", recorder.Code, recorder.Body.String(), runtime.admitCalls)
	}
}

func TestProviderCredentialCreateNeverReturnsOrLogsSecret(t *testing.T) {
	credentialID := "pcrd_019b0a12-0000-7000-8000-000000000005"
	versionID := "pcvr_019b0a12-0000-7000-8000-000000000006"
	now := time.Date(2026, time.July, 21, 18, 0, 0, 0, time.UTC)
	credentials := &fakeProviderCredentials{created: services.ProviderCredentialRead{
		ID:            credentialID,
		Provider:      "openai",
		Scope:         domain.ProviderCredentialScopeAccount,
		Status:        domain.ProviderCredentialActive,
		Version:       1,
		VersionID:     versionID,
		VersionStatus: domain.ProviderCredentialVersionActive,
		CreatedBy:     "operator:test",
		CreatedAt:     now,
		UpdatedAt:     now,
	}}
	var logs bytes.Buffer
	body := []byte(`{"provider":"openai","scope":"account","credential":{"api_key":"lifecycle-secret"},"idempotency_key":"credential-1"}`)
	recorder := httptest.NewRecorder()
	testHandlerWithCredentials(nil, credentials, nil, &logs).ServeHTTP(
		recorder,
		authenticatedRequest(http.MethodPost, "/v1/provider-credentials", body),
	)
	if recorder.Code != http.StatusCreated || credentials.createCalls != 1 || credentials.createInput.Credential.APIKey != "lifecycle-secret" {
		t.Fatalf("credential create = %d %s, calls = %d", recorder.Code, recorder.Body.String(), credentials.createCalls)
	}
	for _, output := range []string{recorder.Body.String(), logs.String()} {
		if strings.Contains(output, "lifecycle-secret") || strings.Contains(output, "api_key") {
			t.Fatalf("secret-bearing output = %s", output)
		}
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

func TestCreateInvocationDecodesHostToolContract(t *testing.T) {
	runtime := &fakeRuntime{
		ack: services.InvocationAcknowledgement{
			AgentID:      testAgentID,
			SessionID:    testSessionID,
			InvocationID: testInvocationID,
			Status:       domain.InvocationQueued,
		},
	}
	body := []byte(`{
		"agent_key":"support",
		"idempotency_key":"request-1",
		"input":{"content":[{"type":"text","text":"look it up"}]},
		"spec":{
			"instructions":"help",
			"model":{"provider":"anthropic","id":"test-model"},
			"tools":[{
				"name":"lookup_order",
				"description":"Look up an order",
				"mode":"host",
				"input_schema":{"type":"object","properties":{"order_id":{"type":"string"}},"additionalProperties":false}
			}]
		}
	}`)
	recorder := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/invocations", body)

	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted || runtime.admitCalls != 1 {
		t.Fatalf("host tool admission = %d, calls = %d, body = %s", recorder.Code, runtime.admitCalls, recorder.Body.String())
	}
	if len(runtime.admitInput.Spec.Tools) != 1 || runtime.admitInput.Spec.Tools[0].Name != "lookup_order" {
		t.Fatalf("decoded host tools = %#v", runtime.admitInput.Spec.Tools)
	}
}

func TestSubmitHostToolResultsReturnsDurableAcknowledgement(t *testing.T) {
	deadline := time.Date(2026, 7, 21, 12, 30, 0, 0, time.UTC)
	toolCallID := "tcal_019f84a5-7838-7b57-a180-000000000001"
	runtime := &fakeRuntime{
		toolResults: services.SubmitHostToolResultsResult{
			InvocationID: testInvocationID,
			SessionID:    testSessionID,
			Status:       domain.InvocationWaiting,
			Results: []services.HostToolResultAcceptance{
				{
					ToolCallID: toolCallID,
					Status:     domain.ToolCallCompleted,
				},
			},
			PendingToolCalls: []services.PendingHostToolCall{
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

func TestSubmitHostToolResultsRejectsMalformedJSONBeforeService(t *testing.T) {
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
			Limits: services.InvocationLimitRead{
				TotalTimeoutSeconds:  1800,
				ActiveTimeoutSeconds: 1800,
				MaxIterations:        3,
			},
			DeadlineAt: now.Add(time.Hour),
			CreatedAt:  now,
			UpdatedAt:  now,
			EndedAt:    &now,
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
		if !strings.Contains(recorder.Body.String(), `"structured_output":{"answer":"yes"}`) ||
			!strings.Contains(recorder.Body.String(), `"source":"tool_call"`) {
			t.Fatalf("GET %s omitted structured output: %s", target, recorder.Body.String())
		}
		for _, object := range renamedOutputObjects(t, target, recorder.Body.Bytes()) {
			for _, stale := range []string{"output", "output_provenance"} {
				if _, ok := object[stale]; ok {
					t.Fatalf("GET %s still serves %q: %s", target, stale, recorder.Body.String())
				}
			}
			for _, renamed := range []string{"structured_output", "structured_output_provenance"} {
				if _, ok := object[renamed]; !ok {
					t.Fatalf("GET %s omitted %q: %s", target, renamed, recorder.Body.String())
				}
			}
		}
	}
}

// renamedOutputObjects returns every JSON object in the response that
// carries the renamed structured-output fields, keyed for exact-name
// assertions that cannot collide with the legitimate usage "output" key.
func renamedOutputObjects(t *testing.T, target string, body []byte) []map[string]json.RawMessage {
	t.Helper()
	if strings.Contains(target, "/transcript") {
		var payload struct {
			InvocationChanges []map[string]json.RawMessage `json:"invocation_changes"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode transcript payload: %v", err)
		}
		return payload.InvocationChanges
	}
	var invocation map[string]json.RawMessage
	if err := json.Unmarshal(body, &invocation); err != nil {
		t.Fatalf("decode Invocation payload: %v", err)
	}
	return []map[string]json.RawMessage{invocation}
}

func TestInvocationResultUsesContractShape(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	text := "Hello!"
	runtime := &fakeRuntime{
		invocationResult: services.InvocationResultRead{
			Invocation: services.InvocationRead{
				ID:        testInvocationID,
				AgentID:   testAgentID,
				SessionID: testSessionID,
				Status:    domain.InvocationCompleted,
				CreatedAt: now,
				UpdatedAt: now,
				EndedAt:   &now,
			},
			Messages: []domain.SessionMessage{
				{
					ID:           "smsg_019b0a12-0000-7000-8000-000000000005",
					SessionID:    testSessionID,
					AgentID:      testAgentID,
					InvocationID: testInvocationID,
					Sequence:     1,
					Role:         domain.MessageRoleUser,
					Content:      json.RawMessage(`[{"type":"text","text":"Say hello."}]`),
					CreatedAt:    now,
				},
				{
					ID:           "smsg_019b0a12-0000-7000-8000-000000000006",
					SessionID:    testSessionID,
					AgentID:      testAgentID,
					InvocationID: testInvocationID,
					Sequence:     2,
					Role:         domain.MessageRoleAssistant,
					Content:      json.RawMessage(`[{"type":"text","text":"Hello!"}]`),
					CreatedAt:    now,
				},
			},
			OutputText: &text,
		},
	}
	recorder := httptest.NewRecorder()
	target := "/v1/invocations/" + testInvocationID + "/result"
	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", target, recorder.Code, recorder.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode result payload: %v", err)
	}
	if len(payload) != 3 {
		t.Fatalf("result payload has %d top-level fields, want exactly invocation, messages, output_text: %s", len(payload), recorder.Body.String())
	}
	for _, field := range []string{"invocation", "messages", "output_text"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("result payload lacks %q: %s", field, recorder.Body.String())
		}
	}
	if !strings.Contains(recorder.Body.String(), `"structured_output":null`) ||
		!strings.Contains(recorder.Body.String(), `"output_text":"Hello!"`) ||
		!strings.Contains(recorder.Body.String(), `"sequence":1`) ||
		!strings.Contains(recorder.Body.String(), `"sequence":2`) {
		t.Fatalf("result payload is missing contract fragments: %s", recorder.Body.String())
	}
}

func TestInvocationResultSerializesEmptyMessagesAndNullText(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	runtime := &fakeRuntime{
		invocationResult: services.InvocationResultRead{
			Invocation: services.InvocationRead{
				ID:        testInvocationID,
				AgentID:   testAgentID,
				SessionID: testSessionID,
				Status:    domain.InvocationQueued,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
	recorder := httptest.NewRecorder()
	target := "/v1/invocations/" + testInvocationID + "/result"
	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", target, recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"messages":[]`) ||
		!strings.Contains(recorder.Body.String(), `"output_text":null`) {
		t.Fatalf("queued result must serialize empty messages and null text: %s", recorder.Body.String())
	}
}

func TestInvocationResultMapsServiceErrors(t *testing.T) {
	runtime := &fakeRuntime{err: &services.PublicError{Code: services.CodeNotFound, Message: "The requested resource was not found."}}
	recorder := httptest.NewRecorder()
	target := "/v1/invocations/" + testInvocationID + "/result"
	testHandler(runtime, nil, io.Discard).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("GET %s = %d, want 404: %s", target, recorder.Code, recorder.Body.String())
	}
	assertErrorEnvelope(t, recorder.Body.Bytes(), "not_found")
}

func TestInvocationRequestRejectsInvalidJSONBeforeService(t *testing.T) {
	valid := validInvocationJSON()
	oversized := `{"agent_key":"` + strings.Repeat("a", services.MaxInvocationBodyBytes) + `"}`
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
			[]byte(`"instructions":"help","tools":[{"name":"lookup","description":"Look up","mode":"host","input_schema":{"type":"object"},"callback":null}],`),
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
		"null optional":  bytes.Replace(valid, []byte(`"agent_key":"support",`), []byte(`"agent_key":"support","tenant_key":null,`), 1),
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
			ID:         testInvocationID,
			AgentID:    testAgentID,
			SessionID:  testSessionID,
			Status:     domain.InvocationCompleted,
			Usage:      json.RawMessage(`{"input_tokens":2,"output_tokens":1}`),
			Provenance: json.RawMessage(`{"provider":"anthropic","requested_model":"requested","served_model":"served","credential_source":"installation_byok"}`),
			CreatedAt:  now,
			UpdatedAt:  now,
			EndedAt:    &now,
		},
		session: services.SessionRead{
			ID:                     testSessionID,
			AgentID:                testAgentID,
			TenantKey:              &tenant,
			SessionKey:             &key,
			ActiveInvocationID:     &active,
			ActiveInvocationStatus: statusPointer(domain.InvocationRunning),
			CreatedAt:              now,
			UpdatedAt:              now,
		},
	}
	for path, required := range map[string][]string{
		"/v1/invocations/" + testInvocationID: {`"id":"` + testInvocationID + `"`, `"input_tokens":2`, `"provider":"anthropic"`},
		"/v1/sessions/" + testSessionID:       {`"tenant_key":"tenant-a"`, `"active_invocation_id":"` + testInvocationID + `"`, `"active_invocation_status":"running"`},
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
				ID:        testInvocationID,
				AgentID:   testAgentID,
				SessionID: testSessionID,
				Status:    domain.InvocationQueued,
				CreatedAt: now,
				UpdatedAt: now,
			}},
			HasMore:    true,
			NextCursor: &next,
		},
		sessions: services.SessionList{Items: []services.SessionRead{{
			ID:                     testSessionID,
			AgentID:                testAgentID,
			TenantKey:              &tenant,
			SessionKey:             &key,
			ActiveInvocationID:     &active,
			ActiveInvocationStatus: statusPointer(domain.InvocationQueued),
			CreatedAt:              now,
			UpdatedAt:              now,
		}}},
		messages: services.SessionMessageList{Items: []domain.SessionMessage{{
			ID:           "smsg_019b0a12-0000-7000-8000-000000000005",
			SessionID:    testSessionID,
			AgentID:      testAgentID,
			InvocationID: testInvocationID,
			Sequence:     1,
			Role:         domain.MessageRoleUser,
			Content:      json.RawMessage(`[{"type":"text","text":"hello"}]`),
			CreatedAt:    now,
		}}},
		transcript: services.TranscriptSnapshot{
			Messages: []domain.SessionMessage{},
			InvocationChanges: []domain.InvocationLifecycleChange{{
				InvocationState: domain.InvocationState{
					InvocationID:           testInvocationID,
					Revision:               2,
					Status:                 domain.InvocationCompleted,
					ThroughMessageSequence: int64Pointer(1),
					CreatedAt:              now,
				},
				Usage: json.RawMessage(`{"input_tokens":2,"output_tokens":1}`),
			}},
			ResumeCursor: "opaque-resume",
		},
	}

	tests := map[string][]string{
		"/v1/invocations?limit=1&tenant_key=tenant-a":   {`"items":[{"id":"` + testInvocationID + `"`, `"has_more":true`, `"next_cursor":"opaque-next"`},
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
		AgentID:      testAgentID,
		SessionID:    testSessionID,
		InvocationID: testInvocationID,
		Status:       domain.InvocationQueued,
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
	return testHandlerWithCredentials(runtime, nil, authenticator, logWriter)
}

func testHandlerWithCredentials(
	runtime RuntimeService,
	credentials ProviderCredentialService,
	authenticator *fakeAuthenticator,
	logWriter io.Writer,
) http.Handler {
	if authenticator == nil {
		authenticator = &fakeAuthenticator{auth: domain.RuntimeAuthContext{
			AccountID: testAccountID,
			Operations: map[domain.RuntimeOperation]struct{}{
				domain.OperationCreateInvocation: {}, domain.OperationGetInvocation: {}, domain.OperationCancelInvocation: {}, domain.OperationSubmitToolResults: {}, domain.OperationGetSession: {},
				domain.OperationListInvocations: {}, domain.OperationListSessions: {},
				domain.OperationListMessages: {}, domain.OperationGetTranscript: {},
				domain.OperationListProviderCredentials: {}, domain.OperationCreateProviderCredential: {},
				domain.OperationGetProviderCredential: {}, domain.OperationRotateProviderCredential: {},
				domain.OperationRevokeProviderCredential: {},
			},
		}}
	}
	logger := slog.New(slog.NewTextHandler(logWriter, nil))
	return newHandler(handlerConfig{
		authenticator:       authenticator,
		runtime:             runtime,
		providerCredentials: credentials,
		logger:              logger,
	})
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
	return []byte(`{"agent_key":"support","idempotency_key":"request-1","input":{"content":[{"type":"text","text":"private caller text"}]},"spec":{"instructions":"help","model":{"provider":"anthropic","id":"test-model"}}}`)
}

func structuredInvocationJSON() []byte {
	return []byte(`{"agent_key":"support","idempotency_key":"request-1","input":{"content":[{"type":"text","text":"private caller text"}]},"spec":{"instructions":"help","model":{"provider":"anthropic","id":"test-model"},"output":{"schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}}}`)
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
