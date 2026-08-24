package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/wonton/cli"

	"github.com/deepnoodle-ai/nvoken/internal/authstore"
)

//go:embed testdata/device_login.json
var deviceLoginContractJSON []byte

type deviceContractOutcome struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

type deviceLoginContract struct {
	Version int `json:"version"`
	Code    struct {
		Request map[string]string     `json:"request"`
		Success deviceContractOutcome `json:"success"`
	} `json:"code"`
	Token struct {
		Request              map[string]string     `json:"request"`
		AuthorizationPending deviceContractOutcome `json:"authorization_pending"`
		SlowDown             deviceContractOutcome `json:"slow_down"`
		ServerError          deviceContractOutcome `json:"server_error"`
		Success              deviceContractOutcome `json:"success"`
	} `json:"token"`
}

func loadDeviceLoginContract(t *testing.T) deviceLoginContract {
	t.Helper()
	var fixture deviceLoginContract
	if err := json.Unmarshal(deviceLoginContractJSON, &fixture); err != nil {
		t.Fatalf("decode device login contract: %v", err)
	}
	if fixture.Version != 2 {
		t.Fatalf("device login contract version = %d", fixture.Version)
	}
	return fixture
}

func writeDeviceOutcome(t *testing.T, response http.ResponseWriter, outcome deviceContractOutcome) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(outcome.Status)
	if _, err := response.Write(outcome.Body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func decodeStringMap(t *testing.T, request *http.Request) map[string]string {
	t.Helper()
	defer request.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return body
}

func TestDeviceLoginUsesPinnedContractAndSavesOrgProfile(t *testing.T) {
	fixture := loadDeviceLoginContract(t)
	var codeResponse deviceCodeResponse
	if err := json.Unmarshal(fixture.Code.Success.Body, &codeResponse); err != nil {
		t.Fatal(err)
	}
	if codeResponse.ExpiresIn != 600 || codeResponse.Interval != 5 {
		t.Fatalf("pinned constants = expires %d interval %d", codeResponse.ExpiresIn, codeResponse.Interval)
	}

	tokenCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if authorization := request.Header.Get("Authorization"); authorization != "" {
			t.Errorf("device endpoint Authorization = %q", authorization)
		}
		switch request.URL.Path {
		case "/api/cli/device/code":
			if body := decodeStringMap(t, request); !reflect.DeepEqual(body, fixture.Code.Request) {
				t.Errorf("code request = %#v", body)
			}
			writeDeviceOutcome(t, response, fixture.Code.Success)
		case "/api/cli/device/token":
			tokenCalls++
			if body := decodeStringMap(t, request); !reflect.DeepEqual(body, fixture.Token.Request) {
				t.Errorf("token request = %#v", body)
			}
			switch tokenCalls {
			case 1:
				writeDeviceOutcome(t, response, fixture.Token.AuthorizationPending)
			case 2:
				writeDeviceOutcome(t, response, fixture.Token.SlowDown)
			default:
				writeDeviceOutcome(t, response, fixture.Token.Success)
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials")
	authstore.SetPathOverride(path)
	if err := authstore.PutProfile("default", authstore.Profile{
		Endpoint:     "https://old.example",
		Token:        "saved-profile-token",
		CredentialID: "cred_old",
		CreatedAt:    "2026-08-01T00:00:00Z",
	}, true); err != nil {
		t.Fatal(err)
	}
	authstore.SetPathOverride("")

	var openedURL string
	var waits []time.Duration
	originalOpen := openDeviceLoginBrowser
	originalWait := waitForDeviceLoginPoll
	openDeviceLoginBrowser = func(target string) error {
		openedURL = target
		return nil
	}
	waitForDeviceLoginPoll = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		return nil
	}
	defer func() {
		openDeviceLoginBrowser = originalOpen
		waitForDeviceLoginPoll = originalWait
	}()

	t.Setenv("NVOKEN_API_KEY", "")
	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--credentials-file", path, "auth", "login", "--console-url", server.URL, "--label", "curtis-mbp"),
	)
	if !result.Success() {
		t.Fatalf("device login: %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	if tokenCalls != 3 {
		t.Fatalf("token calls = %d", tokenCalls)
	}
	if !reflect.DeepEqual(waits, []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}) {
		t.Fatalf("poll waits = %v", waits)
	}
	expectedApprovalURL := server.URL + "/app/cli/device?user_code=" + codeResponse.UserCode
	if openedURL != expectedApprovalURL || !strings.Contains(result.Stdout, expectedApprovalURL) || strings.Contains(result.Stdout, codeResponse.VerificationURIComplete) {
		t.Fatalf("approval URL = %q\nstdout: %s", openedURL, result.Stdout)
	}

	authstore.SetPathOverride(path)
	profile, err := authstore.ResolveProfile("default")
	authstore.SetPathOverride("")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Endpoint != "https://api.nvoken.com" || profile.Token != "nvk_example.secret" || profile.CredentialID == "" || profile.AppID == "" || profile.AppName != "Acme Support" || profile.CredentialType != "app" || profile.Label != "curtis-mbp" || !profile.Default {
		t.Fatalf("saved profile = %#v", profile)
	}
	if !strings.Contains(result.Stdout, "Authenticated to Acme Support with an app key") {
		t.Fatalf("success output = %s", result.Stdout)
	}
}

func TestDeviceLoginTreatsClaimServerFailureAsTerminal(t *testing.T) {
	fixture := loadDeviceLoginContract(t)
	tokenCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/cli/device/code":
			writeDeviceOutcome(t, response, fixture.Code.Success)
		case "/api/cli/device/token":
			tokenCalls++
			writeDeviceOutcome(t, response, fixture.Token.ServerError)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	originalWait := waitForDeviceLoginPoll
	waitForDeviceLoginPoll = func(context.Context, time.Duration) error { return nil }
	defer func() { waitForDeviceLoginPoll = originalWait }()

	path := filepath.Join(t.TempDir(), "credentials")
	t.Setenv("NVOKEN_API_KEY", "")
	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--credentials-file", path, "auth", "login", "--console-url", server.URL, "--label", "curtis-mbp", "--no-browser"),
	)
	if result.Success() || result.Err == nil || !strings.Contains(result.Err.Error(), "may have consumed the code") {
		t.Fatalf("device login failure = %v\nstdout: %s\nstderr: %s", result.Err, result.Stdout, result.Stderr)
	}
	if tokenCalls != 1 {
		t.Fatalf("token calls after terminal failure = %d", tokenCalls)
	}
	authstore.SetPathOverride(path)
	_, err := authstore.ResolveProfile("")
	authstore.SetPathOverride("")
	if err == nil {
		t.Fatalf("profile was saved after claim failure: %v", err)
	}
}
