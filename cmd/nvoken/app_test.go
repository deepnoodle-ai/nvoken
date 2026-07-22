package main

import (
	"context"
	"encoding/json"
	"errors"
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
		writeAccountFixture(w)
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
		writeAccountFixture(w)
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
		if r.URL.Path != "/v1/sessions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
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
		cli.TestArgs("--credentials-file", path, "--output", "json", "session", "list"),
	)
	if !result.Success() {
		t.Fatalf("session list: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	if authorization != "Bearer profile-token" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if !strings.Contains(result.Stdout, `"items":[]`) {
		t.Fatalf("JSON output = %s", result.Stdout)
	}
}

func TestDeviceLoginSavesNamedProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/device/code":
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "device-code", "user_code": "ABCDE-12345", "verification_uri": "https://example.test/auth/device", "verification_uri_complete": "https://example.test/auth/device?user_code=ABCDE-12345", "expires_in": 5, "interval": 1})
		case "/v1/auth/device/token":
			_ = r.ParseForm()
			if r.Form.Get("device_code") != "device-code" {
				t.Errorf("device_code = %q", r.Form.Get("device_code"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "nvk_user.secret", "token_type": "Bearer", "credential_id": "cred_user", "account_id": "acct_test"})
		case "/v1/account":
			if r.Header.Get("Authorization") != "Bearer nvk_user.secret" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "acct_test", "created_at": "2026-07-21T12:00:00Z",
				"authentication": map[string]any{"credential_id": "cred_user", "credential_kind": "user", "effective_profile": "Operator", "method": "device_authorization", "assurance": "bearer", "operations": []string{}},
				"subject":        map[string]any{"id": "osub_test", "issuer": "nvoken:installation", "subject": "bootstrap-owner"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--credentials-file", path, "--base-url", server.URL, "--profile", "work", "auth", "login", "--no-browser"),
	)
	if !result.Success() {
		t.Fatalf("auth login: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	authstore.SetPathOverride(path)
	profile, err := authstore.ResolveProfile("work")
	authstore.SetPathOverride("")
	if err != nil || profile.Token != "nvk_user.secret" || profile.Endpoint != server.URL || !profile.Default || profile.SubjectID != "osub_test" || profile.Subject != "bootstrap-owner" {
		t.Fatalf("saved profile = %#v, %v", profile, err)
	}
}

func TestDeviceLoginHonorsCancellation(t *testing.T) {
	challengeSent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/device/code" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "device-code",
			"user_code":                 "ABCDE-12345",
			"verification_uri":          "https://example.test/auth/device",
			"verification_uri_complete": "https://example.test/auth/device?user_code=ABCDE-12345",
			"expires_in":                30,
			"interval":                  1,
		})
		close(challengeSent)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-challengeSent
		cancel()
	}()
	resetActiveAuth()
	err := newApp().ExecuteContext(ctx, []string{"--base-url", server.URL, "auth", "login", "--no-browser"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled login error = %v", err)
	}
}

func TestLogoutIsLocalAndRevokeCleansUpAfterRemoteSuccess(t *testing.T) {
	remoteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls++
		if r.URL.Path != "/v1/account/credentials/cred_saved/revoke" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer saved-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "cred_saved",
			"kind":       "user",
			"name":       "saved",
			"prefix":     "nvk_saved",
			"status":     "revoked",
			"operations": []string{},
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
		AccountID:    "acct_test",
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

func writeAccountFixture(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "acct_test", "created_at": "2026-07-21T12:00:00Z",
		"authentication": map[string]any{"credential_id": "cred_test", "credential_kind": "machine", "effective_profile": "Runtime", "method": "api_key", "assurance": "bearer", "operations": []string{}},
	})
}
