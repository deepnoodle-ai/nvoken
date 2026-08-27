package nvoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentityLifecycleMethods(t *testing.T) {
	const credentialID = "2a69daf1-e8ad-7b66-9b31-d77bb1aef9c0"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer operator-key" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/identity":
			_, _ = writer.Write([]byte(`{"authentication":{"credential_id":"` + credentialID + `","app_id":"f10c774d-8f44-752b-ae47-ab3ec9a7776d","type":"app","tenant_key":null,"method":"api_key"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/identity/credentials":
			if request.URL.Query().Get("status") != "active" || request.URL.Query().Get("cursor") != "page-2" || request.URL.Query().Get("limit") != "10" {
				t.Errorf("list query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[` + credentialFixture(credentialID, "active") + `],"has_more":true,"next_cursor":"page-3"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/identity/credentials":
			assertIdentityIdempotencyKey(t, request, "create-once")
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body["name"] != "worker" || body["type"] != "app" {
				t.Errorf("create body = %#v", body)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(issuanceFixture(credentialID, "nvk_one-time", false)))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/identity/credentials/"+credentialID:
			_, _ = writer.Write([]byte(credentialFixture(credentialID, "active")))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/identity/credentials/"+credentialID+"/rotate":
			assertIdentityIdempotencyKey(t, request, "rotate-once")
			var body struct {
				OverlapSeconds int `json:"overlap_seconds"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.OverlapSeconds != 300 {
				t.Errorf("rotate body = %#v, %v", body, err)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(issuanceFixture(credentialID, "nvk_rotated", true)))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/identity/credentials/"+credentialID+"/revoke":
			_, _ = writer.Write([]byte(credentialFixture(credentialID, "revoked")))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "operator-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	identity, err := client.GetCurrentIdentity(context.Background())
	// One identity schema serves every caller kind, so the machine-only fields
	// are optional. A machine credential must still receive this one.
	if err != nil || identity.Authentication.CredentialID == nil ||
		string(*identity.Authentication.CredentialID) != credentialID {
		t.Fatalf("GetCurrentIdentity = %#v, %v", identity, err)
	}
	status := CredentialStatusActive
	cursor := "page-2"
	limit := 10
	listed, err := client.ListCredentials(context.Background(), ListCredentialsOptions{
		Status: &status,
		Cursor: &cursor,
		Limit:  &limit,
	})
	if err != nil || len(listed.Items) != 1 || !listed.HasMore || listed.NextCursor == nil || *listed.NextCursor != "page-3" {
		t.Fatalf("ListCredentials = %#v, %v", listed, err)
	}
	created, err := client.CreateCredential(context.Background(), CreateCredentialInput{
		Name:           "worker",
		Type:           CredentialTypeApp,
		IdempotencyKey: "create-once",
	})
	if err != nil || created.Secret != "nvk_one-time" || created.Replayed {
		t.Fatalf("CreateCredential = %#v, %v", created, err)
	}
	read, err := client.GetCredential(context.Background(), credentialID)
	if err != nil || read.ID != credentialID {
		t.Fatalf("GetCredential = %#v, %v", read, err)
	}
	rotated, err := client.RotateCredential(context.Background(), credentialID, RotateCredentialInput{
		OverlapSeconds: 300,
		IdempotencyKey: "rotate-once",
	})
	if err != nil || rotated.Secret != "nvk_rotated" || !rotated.Replayed {
		t.Fatalf("RotateCredential = %#v, %v", rotated, err)
	}
	revoked, err := client.RevokeCredential(context.Background(), credentialID)
	if err != nil || revoked.Status != CredentialStatusRevoked {
		t.Fatalf("RevokeCredential = %#v, %v", revoked, err)
	}
	if client.Raw() == nil || requests != 6 {
		t.Fatalf("raw client = %#v, requests = %d", client.Raw(), requests)
	}
}

func assertIdentityIdempotencyKey(t *testing.T, request *http.Request, expected string) {
	t.Helper()
	if actual := request.Header.Get("Idempotency-Key"); actual != expected {
		t.Errorf("Idempotency-Key = %q, want %q", actual, expected)
	}
}

func credentialFixture(id, status string) string {
	encoded, _ := json.Marshal(map[string]any{
		"id":         id,
		"name":       "worker",
		"prefix":     "nvk_public",
		"status":     status,
		"type":       "app",
		"created_at": "2026-08-08T12:00:00Z",
		"updated_at": "2026-08-08T12:00:00Z",
	})
	return string(encoded)
}

func issuanceFixture(id, secret string, replayed bool) string {
	encoded, _ := json.Marshal(map[string]any{
		"credential":          json.RawMessage(credentialFixture(id, "active")),
		"secret":              secret,
		"delivery_expires_at": "2026-08-08T12:05:00Z",
		"replayed":            replayed,
	})
	return string(encoded)
}
