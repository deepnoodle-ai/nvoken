package nvoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderKeyLifecycleMethods(t *testing.T) {
	const credentialID = "pkey_019b0a12-8d51-7f34-aed2-0e07c1bdb330"
	secretRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		status := "active"
		version := 1
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/provider-keys":
			if request.URL.Query().Get("provider") != "openai" || request.URL.Query().Get("scope") != "app" ||
				request.URL.Query().Get("status") != "active" || request.URL.Query().Get("cursor") != "page-2" ||
				request.URL.Query().Get("limit") != "10" {
				t.Errorf("list query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[` + providerKeyFixture(credentialID, status, version) + `],"has_more":false,"next_cursor":null}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/provider-keys":
			assertProviderKeySecretRequest(t, request, "create-secret", "create-once")
			secretRequests++
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(providerKeyFixture(credentialID, status, version)))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/provider-keys/"+credentialID:
			_, _ = writer.Write([]byte(providerKeyFixture(credentialID, status, version)))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/provider-keys/"+credentialID+"/rotate":
			assertProviderKeySecretRequest(t, request, "rotate-secret", "rotate-once")
			secretRequests++
			version = 2
			_, _ = writer.Write([]byte(providerKeyFixture(credentialID, status, version)))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/provider-keys/"+credentialID:
			status = "revoked"
			_, _ = writer.Write([]byte(providerKeyFixture(credentialID, status, version)))
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
	scope := ProviderKeyScopeApp
	status := ProviderKeyStatusActive
	cursor := "page-2"
	limit := 10
	listed, err := client.ListProviderKeys(context.Background(), ListProviderKeysOptions{
		Provider: &provider,
		Scope:    &scope,
		Status:   &status,
		Cursor:   &cursor,
		Limit:    &limit,
	})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != credentialID {
		t.Fatalf("ListProviderKeys = %#v, %v", listed, err)
	}
	created, err := client.CreateProviderKey(context.Background(), CreateProviderKeyInput{
		Provider:       provider,
		Scope:          scope,
		APIKey:         "create-secret",
		IdempotencyKey: "create-once",
	})
	if err != nil || created.ID != credentialID {
		t.Fatalf("CreateProviderKey = %#v, %v", created, err)
	}
	read, err := client.GetProviderKey(context.Background(), credentialID)
	if err != nil || read.ID != credentialID {
		t.Fatalf("GetProviderKey = %#v, %v", read, err)
	}
	rotated, err := client.RotateProviderKey(context.Background(), credentialID, RotateProviderKeyInput{
		APIKey:         "rotate-secret",
		IdempotencyKey: "rotate-once",
	})
	if err != nil || rotated.Version != 2 {
		t.Fatalf("RotateProviderKey = %#v, %v", rotated, err)
	}
	revoked, err := client.RevokeProviderKey(context.Background(), credentialID)
	if err != nil || revoked.Status != ProviderKeyStatusRevoked {
		t.Fatalf("RevokeProviderKey = %#v, %v", revoked, err)
	}
	if secretRequests != 2 {
		t.Fatalf("secret requests = %d, want 2", secretRequests)
	}
}

func TestInvokeProviderKeySelections(t *testing.T) {
	base := InvokeRequest{
		AgentKey:       "support",
		IdempotencyKey: "credential-selection",
		Input:          "hello",
		AgentDefinition: &AgentDefinition{
			Model: Model{Provider: "openai", ID: "gpt-test"},
		},
	}

	for _, test := range []struct {
		name      string
		selection ProviderKeySelection
		want      string
	}{
		{
			name: "caller ephemeral",
			selection: ProviderKeySelection{
				Provider: "openai",
				Source:   ProviderKeyCallerEphemeral,
				APIKey:   "secret",
			},
			want: `"provider_keys":[{"key":{"api_key":"secret"},"provider":"openai","source":"caller_ephemeral"}]`,
		},
		{
			name: "stored app BYOK",
			selection: ProviderKeySelection{
				Provider: "openai",
				Source:   ProviderKeyAppBYOK,
			},
			want: `"provider_keys":[{"provider":"openai","source":"app_byok"}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.ProviderKeys = []ProviderKeySelection{test.selection}
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

func assertProviderKeySecretRequest(t *testing.T, request *http.Request, apiKey, idempotencyKey string) {
	t.Helper()
	var body struct {
		Key struct {
			APIKey string `json:"api_key"`
		} `json:"key"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Errorf("decode credential request: %v", err)
		return
	}
	if body.Key.APIKey != apiKey || body.IdempotencyKey != idempotencyKey {
		t.Errorf("credential request = %#v", body)
	}
}

func providerKeyFixture(id, status string, version int) string {
	encoded, _ := json.Marshal(map[string]any{
		"id":                  id,
		"provider":            "openai",
		"scope":               "app",
		"tenant_key":          nil,
		"status":              status,
		"version":             version,
		"version_id":          "pkeyv_019b0a12-8d51-7f34-aed2-0e07c1bdb331",
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
