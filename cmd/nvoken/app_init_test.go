package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/deepnoodle-ai/wonton/cli"
)

func TestAppInitProvisionsBrowserAppAndPrintsEnvironment(t *testing.T) {
	var sequence []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		sequence = append(sequence, request.Method+" "+request.URL.Path)
		response.Header().Set("Content-Type", "application/json")
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer operator-key" {
			t.Errorf("Authorization = %q", authorization)
		}
		switch request.Method + " " + request.URL.Path {
		case "POST /v1/apps":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode App registration: %v", err)
			} else if body["name"] != "support" || body["display_name"] != "Support" || body["org_id"] != "org_test" {
				t.Errorf("App registration = %#v", body)
			} else if _, exists := body["browser_access"]; exists {
				t.Errorf("App registration enabled browser access before credential issuance: %#v", body)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(appInitRegistrationFixture))
		case "POST /v1/identity/credentials":
			if request.Header.Get("Idempotency-Key") == "" {
				t.Error("credential issuance omitted Idempotency-Key")
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode credential issuance: %v", err)
			} else if body["app_id"] != "app_test" || body["profile"] != "runtime" || body["name"] != "support deploy" {
				t.Errorf("credential issuance = %#v", body)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(appInitCredentialFixture))
		case "PATCH /v1/apps/app_test":
			var body struct {
				BrowserAccess struct {
					AllowedOrigins    []string `json:"allowed_origins"`
					InvocationWebhook struct {
						URL string `json:"url"`
					} `json:"invocation_webhook"`
					Limits struct {
						PerTenant int64 `json:"max_concurrent_invocations_per_tenant"`
						PerUser   int64 `json:"max_concurrent_invocations_per_user"`
						PerMinute int64 `json:"max_admissions_per_user_per_minute"`
					} `json:"limits"`
				} `json:"browser_access"`
				DefaultRateLimits struct {
					Concurrent int64 `json:"max_concurrent_invocations"`
					PerMinute  int64 `json:"max_admissions_per_minute"`
				} `json:"default_rate_limits"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode browser access: %v", err)
			} else if !reflect.DeepEqual(body.BrowserAccess.AllowedOrigins, []string{"https://app.example.test", "http://localhost:5173"}) ||
				body.BrowserAccess.InvocationWebhook.URL != "https://api.example.test/nvoken/events" ||
				body.BrowserAccess.Limits.PerTenant != 12 ||
				body.BrowserAccess.Limits.PerUser != 2 ||
				body.BrowserAccess.Limits.PerMinute != 10 ||
				body.DefaultRateLimits.Concurrent != 30 ||
				body.DefaultRateLimits.PerMinute != 200 {
				t.Errorf("browser configuration = %#v", body)
			}
			_, _ = response.Write([]byte(`{"id":"app_test","name":"support","status":"active"}`))
		case "POST /v1/apps/app_test/client-keys":
			var body struct {
				Name      string `json:"name"`
				PublicKey []byte `json:"public_key"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode client key: %v", err)
			} else if body.Name != "web production" || len(body.PublicKey) != 32 {
				t.Errorf("client key = name %q, public key length %d", body.Name, len(body.PublicKey))
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"id":"ckey_test","name":"web production","fingerprint":"fingerprint","created_at":"2026-08-17T12:00:02Z"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs(
			"--base-url", server.URL,
			"--api-key", "operator-key",
			"app", "init", "support",
			"--display-name", "Support",
			"--org-id", "org_test",
			"--credential-name", "support deploy",
			"--browser",
			"--origin", "https://app.example.test",
			"--origin", "http://localhost:5173",
			"--webhook-url", "https://api.example.test/nvoken/events",
			"--client-key-name", "web production",
			"--max-concurrent-invocations", "30",
			"--max-admissions-per-minute", "200",
			"--max-concurrent-invocations-per-tenant", "12",
			"--max-concurrent-invocations-per-user", "2",
			"--max-admissions-per-user-per-minute", "10",
		),
	)
	if !result.Success() {
		t.Fatalf("app init: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	wantSequence := []string{
		"POST /v1/apps",
		"POST /v1/identity/credentials",
		"PATCH /v1/apps/app_test",
		"POST /v1/apps/app_test/client-keys",
	}
	if !reflect.DeepEqual(sequence, wantSequence) {
		t.Fatalf("request sequence = %#v, want %#v", sequence, wantSequence)
	}
	for _, assignment := range []string{
		"NVOKEN_BASE_URL='" + server.URL + "'",
		"NVOKEN_APP_ID='app_test'",
		"NVOKEN_API_KEY='nvk_runtime.secret'",
		"NVOKEN_CALLBACK_KEY_ID='sign_callback'",
		"NVOKEN_CALLBACK_KEY_VERSION='1'",
		"NVOKEN_CALLBACK_SECRET='callback-secret'",
		"NVOKEN_WEBHOOK_KEY_ID='sign_webhook'",
		"NVOKEN_WEBHOOK_KEY_VERSION='1'",
		"NVOKEN_WEBHOOK_SECRET='webhook-secret'",
		"NVOKEN_CLIENT_KEY_ID='ckey_test'",
	} {
		if !strings.Contains(result.Stdout, assignment+"\n") {
			t.Errorf("environment is missing %q:\n%s", assignment, result.Stdout)
		}
	}
	privateSeed := appInitEnvironmentValue(t, result.Stdout, "NVOKEN_CLIENT_PRIVATE_KEY")
	decodedSeed, err := base64.StdEncoding.DecodeString(privateSeed)
	if err != nil || len(decodedSeed) != 32 {
		t.Fatalf("client private seed = %q, %v", privateSeed, err)
	}
	if strings.Contains(result.Stdout, "NVOKEN_CLIENT_PUBLIC_KEY") {
		t.Fatalf("successful environment retained recovery-only public key:\n%s", result.Stdout)
	}
}

func TestAppInitWithoutBrowserReturnsStructuredEnvironment(t *testing.T) {
	var sequence []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		sequence = append(sequence, request.Method+" "+request.URL.Path)
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /v1/apps":
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(appInitRegistrationFixture))
		case "POST /v1/identity/credentials":
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(appInitCredentialFixture))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--json", "--base-url", server.URL, "--api-key", "operator-key", "app", "init", "support"),
	)
	if !result.Success() {
		t.Fatalf("app init: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	if !reflect.DeepEqual(sequence, []string{"POST /v1/apps", "POST /v1/identity/credentials"}) {
		t.Fatalf("request sequence = %#v", sequence)
	}
	var output struct {
		Complete         bool   `json:"complete"`
		CredentialSecret string `json:"credential_secret"`
		ClientKey        any    `json:"client_key"`
		Environment      string `json:"environment"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		t.Fatalf("decode output: %v\n%s", err, result.Stdout)
	}
	if !output.Complete || output.CredentialSecret != "nvk_runtime.secret" || output.ClientKey != nil {
		t.Fatalf("structured result = %#v", output)
	}
	if strings.Contains(output.Environment, "NVOKEN_CLIENT_") {
		t.Fatalf("non-browser environment = %q", output.Environment)
	}
}

func TestAppInitValidatesBrowserOptionsBeforeRegistering(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	for name, arguments := range map[string][]string{
		"browser needs an origin": {
			"--browser", "--webhook-url", "https://api.example.test/nvoken/events",
		},
		"browser needs a webhook": {
			"--browser", "--origin", "https://app.example.test",
		},
		"browser limits are positive": {
			"--browser", "--origin", "https://app.example.test", "--webhook-url", "https://api.example.test/nvoken/events",
			"--max-concurrent-invocations-per-user", "0",
		},
		"browser flags need browser mode": {
			"--origin", "https://app.example.test",
		},
	} {
		t.Run(name, func(t *testing.T) {
			resetActiveAuth()
			args := []string{"--base-url", server.URL, "--api-key", "operator-key", "app", "init", "support"}
			result := newApp().Test(t, cli.TestArgs(append(args, arguments...)...))
			if result.Success() {
				t.Fatalf("app init accepted invalid browser options: %s", result.Stdout)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("invalid browser options made %d requests", requests)
	}
}

func TestAppInitPrintsOneTimeRecoveryValuesAfterPartialFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /v1/apps":
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(appInitRegistrationFixture))
		case "POST /v1/identity/credentials":
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(appInitCredentialFixture))
		case "PATCH /v1/apps/app_test":
			_, _ = response.Write([]byte(`{"id":"app_test","name":"support","status":"active"}`))
		case "POST /v1/apps/app_test/client-keys":
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte(`{"code":"internal","message":"try again"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	resetActiveAuth()
	result := newApp().Test(t, cli.TestArgs(
		"--base-url", server.URL,
		"--api-key", "operator-key",
		"app", "init", "support",
		"--browser",
		"--origin", "https://app.example.test",
		"--webhook-url", "https://api.example.test/nvoken/events",
	))
	if result.Success() || !strings.Contains(fmt.Sprint(result.Err), "browser client-key registration") {
		t.Fatalf("app init failure = %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	for _, name := range []string{
		"NVOKEN_API_KEY",
		"NVOKEN_CALLBACK_SECRET",
		"NVOKEN_WEBHOOK_SECRET",
		"NVOKEN_CLIENT_PUBLIC_KEY",
		"NVOKEN_CLIENT_PRIVATE_KEY",
	} {
		if appInitEnvironmentValue(t, result.Stdout, name) == "" {
			t.Errorf("recovery output omitted %s:\n%s", name, result.Stdout)
		}
	}
	if !strings.Contains(result.Stdout, "# nvoken app init stopped during browser client-key registration.") {
		t.Fatalf("recovery output did not explain partial state:\n%s", result.Stdout)
	}
}

func appInitEnvironmentValue(t *testing.T, output, name string) string {
	t.Helper()
	prefix := name + "='"
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "'") {
			return strings.TrimSuffix(strings.TrimPrefix(line, prefix), "'")
		}
	}
	t.Errorf("output has no %s assignment:\n%s", name, output)
	return ""
}

const appInitRegistrationFixture = `{
  "app": {"id":"app_test","name":"support","status":"active"},
  "signing_keys": [
    {"purpose":"callback","key_id":"sign_callback","version":1,"active":true,"secret":"callback-secret"},
    {"purpose":"webhook","key_id":"sign_webhook","version":1,"active":true,"secret":"webhook-secret"}
  ]
}`

const appInitCredentialFixture = `{
  "credential": {
    "id":"cred_test",
    "app_id":"app_test",
    "name":"support runtime",
    "prefix":"nvk_runtime",
    "profile":"runtime",
    "status":"active",
    "operations":[],
    "created_at":"2026-08-17T12:00:00Z",
    "updated_at":"2026-08-17T12:00:00Z"
  },
  "secret":"nvk_runtime.secret",
  "delivery_expires_at":"2026-08-17T12:10:00Z",
  "replayed":false
}`
