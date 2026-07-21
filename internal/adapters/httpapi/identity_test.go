package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type identityHTTPFake struct {
	IdentityService
	account     services.AccountIdentity
	credentials []domain.Credential
	pollError   error
	startCalls  *int
}

func (f identityHTTPFake) CurrentAccount(context.Context, domain.RuntimeAuthContext) (services.AccountIdentity, error) {
	return f.account, nil
}

func (f identityHTTPFake) ListCredentials(context.Context, domain.RuntimeAuthContext) ([]domain.Credential, error) {
	return f.credentials, nil
}

func (f identityHTTPFake) PollDeviceAuthorization(context.Context, string) (services.CredentialIssuanceResult, error) {
	return services.CredentialIssuanceResult{}, f.pollError
}

func (f identityHTTPFake) StartDeviceAuthorization(context.Context, services.DeviceCodeInput) (services.DeviceCodeResult, error) {
	if f.startCalls != nil {
		(*f.startCalls)++
	}
	return services.DeviceCodeResult{
		DeviceCode:              "device-code",
		UserCode:                "ABCDE-FGHIJ",
		VerificationURI:         "https://nvoken.example/auth/device",
		VerificationURIComplete: "https://nvoken.example/auth/device?user_code=ABCDE-FGHIJ",
		ExpiresIn:               900,
		Interval:                5,
	}, nil
}

func (f identityHTTPFake) CreateBootstrapBrowserSession(context.Context, string) (services.BootstrapBrowserSession, error) {
	return services.BootstrapBrowserSession{Token: "browser-secret", CSRFToken: "csrf-secret", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func TestIdentityResponsesExplainAuthAndRedactVerifier(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	profile := domain.CredentialProfileOperator
	credential := domain.Credential{ID: "cred_test", AccountID: testAccountID, Kind: domain.CredentialKindMachine, Name: "operator", Prefix: "nvk_public", Verifier: []byte("raw-secret-must-not-appear"), Status: domain.CredentialStatusActive, Profile: &profile, CreatedAt: now, UpdatedAt: now}
	fake := identityHTTPFake{
		account:     services.AccountIdentity{Account: domain.Account{ID: testAccountID, CreatedAt: now}, Credential: credential, EffectiveRole: profile, Method: "api_key", Assurance: "bearer", Operations: []domain.RuntimeOperation{domain.OperationGetSession}},
		credentials: []domain.Credential{credential},
	}
	handler := newHandler(handlerConfig{authenticator: &fakeAuthenticator{auth: domain.RuntimeAuthContext{AccountID: testAccountID}}, identity: fake})

	for _, path := range []string{"/v1/account", "/v1/account/credentials"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer presented-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "raw-secret") || strings.Contains(response.Body.String(), "presented-secret") {
			t.Fatalf("GET %s exposed secret: %s", path, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	request.Header.Set("Authorization", "Bearer presented-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	for _, expected := range []string{`"effective_profile":"Operator"`, `"operations":["get_session"]`, `"method":"api_key"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("account response missing %s: %s", expected, response.Body.String())
		}
	}
}

func TestDevicePollingUsesRFCErrorEnvelope(t *testing.T) {
	handler := newHandler(handlerConfig{identity: identityHTTPFake{pollError: &services.DeviceFlowError{Code: services.DeviceSlowDown}}})
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/device/token", strings.NewReader("grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code&device_code=test"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" || strings.TrimSpace(response.Body.String()) != `{"error":"slow_down"}` {
		t.Fatalf("device response = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
}

func TestDeviceCodeStartRateLimit(t *testing.T) {
	startCalls := 0
	handler := newHandler(handlerConfig{identity: identityHTTPFake{startCalls: &startCalls}})
	for attempt := 1; attempt <= 11; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/auth/device/code", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "192.0.2.5:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if attempt <= 10 && response.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, body = %s", attempt, response.Code, response.Body.String())
		}
		if attempt == 11 && (response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "") {
			t.Fatalf("rate limit response = %d %#v", response.Code, response.Header())
		}
	}
	if startCalls != 10 {
		t.Fatalf("StartDeviceAuthorization calls = %d, want 10", startCalls)
	}
}

func TestBootstrapSessionCookiePolicyAndRateLimit(t *testing.T) {
	handler := newHandler(handlerConfig{identity: identityHTTPFake{}})
	for attempt := 1; attempt <= 6; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "https://nvoken.example/v1/auth/bootstrap/session", strings.NewReader(`{"bootstrap_secret":"secret"}`))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "192.0.2.10:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if attempt <= 5 {
			if response.Code != http.StatusCreated {
				body, _ := io.ReadAll(response.Result().Body)
				t.Fatalf("attempt %d status = %d, body = %s", attempt, response.Code, body)
			}
			cookies := response.Result().Cookies()
			if len(cookies) != 2 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || !cookies[1].HttpOnly || !cookies[1].Secure {
				t.Fatalf("cookies = %#v", cookies)
			}
		} else if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
			t.Fatalf("rate limit response = %d %#v", response.Code, response.Header())
		}
	}
}

func TestDeviceConfirmationRequiresCSRFToken(t *testing.T) {
	handler := newHandler(handlerConfig{identity: identityHTTPFake{}})
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/device/confirm", strings.NewReader("user_code=ABCDE-FGHIJ&decision=approve"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: bootstrapSessionCookie, Value: "browser-secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing CSRF status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestDeviceConfirmationRateLimit(t *testing.T) {
	handler := newHandler(handlerConfig{identity: identityHTTPFake{}})
	for attempt := 1; attempt <= 21; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/auth/device/confirm", strings.NewReader("user_code=ABCDE-FGHIJ&decision=approve"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.RemoteAddr = "192.0.2.20:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if attempt <= 20 && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, body = %s", attempt, response.Code, response.Body.String())
		}
		if attempt == 21 && (response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "") {
			t.Fatalf("rate limit response = %d %#v", response.Code, response.Header())
		}
	}
}

func TestClientAddressTrustsForwardedOnlyWhenConfigured(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "35.191.0.1:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 35.191.0.1")

	if got := clientAddress(request, false); got != "35.191.0.1" {
		t.Fatalf("untrusted client address = %q", got)
	}
	if got := clientAddress(request, true); got != "203.0.113.9" {
		t.Fatalf("trusted client address = %q", got)
	}
}

func TestAttemptLimiterPrunesAndBoundsWindows(t *testing.T) {
	limiter := newAttemptLimiter(1, time.Minute)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	for index := 0; index < maximumAttemptLimiterWindows+100; index++ {
		limiter.Allow(fmt.Sprintf("192.0.2.%d", index), now)
	}
	if got := len(limiter.windows); got > maximumAttemptLimiterWindows {
		t.Fatalf("limiter window count = %d", got)
	}

	if !limiter.Allow("198.51.100.10", now.Add(2*time.Minute)) {
		t.Fatal("fresh client should be allowed after window expiry")
	}
	if got := len(limiter.windows); got != 1 {
		t.Fatalf("pruned limiter window count = %d, want 1", got)
	}
}
