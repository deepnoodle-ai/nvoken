package nvoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderCredentialLifecycleMethods(t *testing.T) {
	const credentialID = "pcrd_019b0a12-8d51-7f34-aed2-0e07c1bdb330"
	secretRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		status := "active"
		version := 1
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/provider-credentials":
			if request.URL.Query().Get("provider") != "openai" || request.URL.Query().Get("scope") != "account" ||
				request.URL.Query().Get("status") != "active" || request.URL.Query().Get("limit") != "10" {
				t.Errorf("list query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[` + providerCredentialFixture(credentialID, status, version) + `]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/provider-credentials":
			assertProviderCredentialSecretRequest(t, request, "create-secret", "create-once")
			secretRequests++
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(providerCredentialFixture(credentialID, status, version)))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/provider-credentials/"+credentialID:
			_, _ = writer.Write([]byte(providerCredentialFixture(credentialID, status, version)))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/provider-credentials/"+credentialID+"/rotate":
			assertProviderCredentialSecretRequest(t, request, "rotate-secret", "rotate-once")
			secretRequests++
			version = 2
			_, _ = writer.Write([]byte(providerCredentialFixture(credentialID, status, version)))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/provider-credentials/"+credentialID:
			status = "revoked"
			_, _ = writer.Write([]byte(providerCredentialFixture(credentialID, status, version)))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	provider := ModelProviderOpenAI
	scope := ProviderCredentialScopeAccount
	status := ProviderCredentialStatusActive
	limit := 10
	listed, err := client.ListProviderCredentials(context.Background(), ListProviderCredentialsOptions{
		Provider: &provider,
		Scope:    &scope,
		Status:   &status,
		Limit:    &limit,
	})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != credentialID {
		t.Fatalf("ListProviderCredentials = %#v, %v", listed, err)
	}
	created, err := client.CreateProviderCredential(context.Background(), CreateProviderCredentialInput{
		Provider:       provider,
		Scope:          scope,
		APIKey:         "create-secret",
		IdempotencyKey: "create-once",
	})
	if err != nil || created.ID != credentialID {
		t.Fatalf("CreateProviderCredential = %#v, %v", created, err)
	}
	read, err := client.GetProviderCredential(context.Background(), credentialID)
	if err != nil || read.ID != credentialID {
		t.Fatalf("GetProviderCredential = %#v, %v", read, err)
	}
	rotated, err := client.RotateProviderCredential(context.Background(), credentialID, RotateProviderCredentialInput{
		APIKey:         "rotate-secret",
		IdempotencyKey: "rotate-once",
	})
	if err != nil || rotated.Version != 2 {
		t.Fatalf("RotateProviderCredential = %#v, %v", rotated, err)
	}
	revoked, err := client.RevokeProviderCredential(context.Background(), credentialID)
	if err != nil || revoked.Status != ProviderCredentialStatusRevoked {
		t.Fatalf("RevokeProviderCredential = %#v, %v", revoked, err)
	}
	if secretRequests != 2 {
		t.Fatalf("secret requests = %d, want 2", secretRequests)
	}
}

func TestInvokeProviderCredentialSelections(t *testing.T) {
	base := InvokeRequest{
		AgentKey:       "support",
		IdempotencyKey: "credential-selection",
		Input:          "hello",
		Spec: ExecutionSpec{
			Model: Model{Provider: "openai", ID: "gpt-test"},
		},
	}

	for _, test := range []struct {
		name      string
		selection ProviderCredentialSelection
		want      string
	}{
		{
			name: "caller ephemeral",
			selection: ProviderCredentialSelection{
				Provider: "openai",
				Source:   ProviderCredentialCallerEphemeral,
				APIKey:   "secret",
			},
			want: `"provider_credentials":[{"credential":{"api_key":"secret"},"provider":"openai","source":"caller_ephemeral"}]`,
		},
		{
			name: "stored account BYOK",
			selection: ProviderCredentialSelection{
				Provider: "openai",
				Source:   ProviderCredentialAccountBYOK,
			},
			want: `"provider_credentials":[{"provider":"openai","source":"account_byok"}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.ProviderCredentials = []ProviderCredentialSelection{test.selection}
			generatedRequest, err := request.generated()
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(generatedRequest)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), test.want) {
				t.Fatalf("generated request = %s, want fragment %s", encoded, test.want)
			}
		})
	}
}

func assertProviderCredentialSecretRequest(t *testing.T, request *http.Request, apiKey, idempotencyKey string) {
	t.Helper()
	var body struct {
		Credential struct {
			APIKey string `json:"api_key"`
		} `json:"credential"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Errorf("decode credential request: %v", err)
		return
	}
	if body.Credential.APIKey != apiKey || body.IdempotencyKey != idempotencyKey {
		t.Errorf("credential request = %#v", body)
	}
}

func providerCredentialFixture(id, status string, version int) string {
	encoded, _ := json.Marshal(map[string]any{
		"id":                  id,
		"provider":            "openai",
		"scope":               "account",
		"tenant_key":          nil,
		"status":              status,
		"version":             version,
		"version_id":          "pcvr_019b0a12-8d51-7f34-aed2-0e07c1bdb331",
		"previous_version_id": nil,
		"version_status":      status,
		"expires_at":          nil,
		"overlap_expires_at":  nil,
		"created_by":          "operator:test",
		"created_at":          "2026-07-21T18:00:00Z",
		"updated_at":          "2026-07-21T18:00:00Z",
		"revoked_at":          nil,
	})
	return string(encoded)
}
