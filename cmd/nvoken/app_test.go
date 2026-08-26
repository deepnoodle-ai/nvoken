package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
)

func TestEnvironmentAuthenticationCreatesNoCredentialsFile(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		writeIdentityFixture(w)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--output", "json", "auth", "status"),
		cli.TestEnv("NVOKEN_API_KEY", "environment-token"),
		cli.TestEnv("NVOKEN_BASE_URL", server.URL),
		cli.TestEnv("NVOKEN_CREDENTIALS_FILE", path),
	)
	if !result.Success() {
		t.Fatalf("auth status: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	if authorization != "Bearer environment-token" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("environment-backed command created credentials file: %v", err)
	}
	if !strings.Contains(result.Stdout, `"credential_id": "cred_test"`) {
		t.Fatalf("JSON output = %s", result.Stdout)
	}
}

func TestVersionUsesReleaseValue(t *testing.T) {
	previous := version
	version = "0.1.1-test"
	t.Cleanup(func() { version = previous })

	result := newApp().Test(t, cli.TestArgs("--version"))
	if !result.Success() {
		t.Fatalf("version: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "0.1.1-test") {
		t.Fatalf("version output = %q", result.Stdout)
	}
}

func TestEndpointAndCredentialPrecedenceAreIndependent(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		writeIdentityFixture(w)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	authstore.SetPathOverride(path)
	if err := authstore.PutProfile("saved", authstore.Profile{Endpoint: "https://wrong.example", Token: "profile-token", CredentialID: "cred_profile"}, true); err != nil {
		t.Fatal(err)
	}
	authstore.SetPathOverride("")

	resetActiveAuth()
	first := newApp().Test(t,
		cli.TestArgs("--credentials-file", path, "--base-url", server.URL, "auth", "status"),
	)
	if !first.Success() {
		t.Fatalf("endpoint override status: %v", first.Err)
	}

	resetActiveAuth()
	second := newApp().Test(t,
		cli.TestArgs("--credentials-file", path, "--base-url", server.URL, "--api-key", "override-token", "auth", "status"),
	)
	if !second.Success() {
		t.Fatalf("credential override status: %v", second.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(authorizations) != 2 || authorizations[0] != "Bearer profile-token" || authorizations[1] != "Bearer override-token" {
		t.Fatalf("Authorization sequence = %#v", authorizations)
	}
}

func TestRuntimeCommandUsesSavedProfile(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/conversations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"has_more":false,"next_cursor":null}`))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	authstore.SetPathOverride(path)
	if err := authstore.PutProfile("runtime", authstore.Profile{
		Endpoint: server.URL,
		Token:    "profile-token",
	}, true); err != nil {
		t.Fatal(err)
	}
	authstore.SetPathOverride("")

	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--credentials-file", path, "--output", "json", "conversation", "list"),
	)
	if !result.Success() {
		t.Fatalf("conversation list: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	if authorization != "Bearer profile-token" {
		t.Fatalf("Authorization = %q", authorization)
	}
	var output struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil || len(output.Items) != 0 {
		t.Fatalf("JSON output = %s, err = %v", result.Stdout, err)
	}
}

func TestAuthLoginVerifiesAPIKeyAndSavesNamedProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/identity" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer nvk_machine.secret" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authentication": map[string]any{
				"credential_id": "cred_machine",
				"app_id":        "app_test",
				"type":          "app",
				"method":        "api_key",
			},
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--json", "--credentials-file", path, "--base-url", server.URL, "--api-key", "nvk_machine.secret", "--profile", "work", "auth", "login", "--default"),
	)
	if !result.Success() {
		t.Fatalf("auth login: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	authstore.SetPathOverride(path)
	profile, err := authstore.ResolveProfile("work")
	authstore.SetPathOverride("")
	if err != nil || profile.Token != "nvk_machine.secret" || profile.Endpoint != server.URL || !profile.Default || profile.CredentialID != "cred_machine" {
		t.Fatalf("saved profile = %#v, %v", profile, err)
	}
	if !json.Valid([]byte(result.Stdout)) || !strings.Contains(result.Stdout, `"profile": "work"`) || strings.Contains(result.Stdout, "nvk_machine.secret") {
		t.Fatalf("JSON login receipt = %s", result.Stdout)
	}
}

func TestAuthLoginRejectsAnUnusableAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "unauthenticated", "message": "no", "request_id": "req_test",
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--credentials-file", path, "--base-url", server.URL, "--api-key", "nvk_bad.secret", "--profile", "work", "auth", "login"),
	)
	if result.Success() {
		t.Fatalf("auth login accepted an unusable API key: %s", result.Stdout)
	}
	authstore.SetPathOverride(path)
	_, err := authstore.ResolveProfile("work")
	authstore.SetPathOverride("")
	if err == nil {
		t.Fatal("auth login saved a profile for an unusable API key")
	}
}

func TestLogoutIsLocalAndRevokeCleansUpAfterRemoteSuccess(t *testing.T) {
	remoteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls++
		if r.URL.Path != "/v1/identity/credentials/cred_saved/revoke" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer saved-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "cred_saved",
			"name":       "saved",
			"prefix":     "nvk_saved",
			"status":     "revoked",
			"type":       "installation_admin",
			"created_at": "2026-07-21T12:00:00Z",
			"updated_at": "2026-07-21T12:00:01Z",
		})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	authstore.SetPathOverride(path)
	profile := authstore.Profile{
		Endpoint:     server.URL,
		Token:        "saved-token",
		CredentialID: "cred_saved",
		CreatedAt:    "2026-07-21T12:00:00Z",
	}
	if err := authstore.PutProfile("saved", profile, true); err != nil {
		t.Fatal(err)
	}
	authstore.SetPathOverride("")

	resetActiveAuth()
	logout := newApp().Test(t, cli.TestArgs("--credentials-file", path, "auth", "logout"))
	if !logout.Success() || remoteCalls != 0 {
		t.Fatalf("logout = %v, remote calls = %d, stderr = %s", logout.Err, remoteCalls, logout.Stderr)
	}
	authstore.SetPathOverride(path)
	if _, err := authstore.ResolveProfile("saved"); err == nil {
		t.Fatal("logout retained local profile")
	}
	if err := authstore.PutProfile("saved", profile, true); err != nil {
		t.Fatal(err)
	}
	authstore.SetPathOverride("")

	resetActiveAuth()
	revoke := newApp().Test(t, cli.TestArgs("--credentials-file", path, "auth", "revoke"))
	if !revoke.Success() || remoteCalls != 1 {
		t.Fatalf("revoke = %v, remote calls = %d, stderr = %s", revoke.Err, remoteCalls, revoke.Stderr)
	}
	authstore.SetPathOverride(path)
	defer authstore.SetPathOverride("")
	if _, err := authstore.ResolveProfile("saved"); err == nil {
		t.Fatal("revoke retained local profile")
	}
}

func writeIdentityFixture(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "acct_test", "created_at": "2026-07-21T12:00:00Z",
		"authentication": map[string]any{"credential_id": "cred_test", "app_id": "app_test", "type": "app", "method": "api_key"},
	})
}
