package nvoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppProvisioningUsesTargetTurnLimitsAndWebhook(t *testing.T) {
	var registered map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&registered); err != nil {
			t.Fatalf("decode App registration: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"app":{"id":"app_test","name":"acme"},"signing_keys":[]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	policy := CreditPolicyRequired
	_, err = client.RegisterApp(context.Background(), "acme", RegisterAppOptions{
		DefaultRateLimits: &AppDefaultRateLimits{
			MaxAdmissionsPerMinute: 120,
			MaxConcurrentTurns:     20,
		},
		MachineConcurrencyLimits: &MachineConcurrencyLimits{
			MaxConcurrentTurnsPerTenant: 12,
			MaxConcurrentTurnsPerUser:   3,
		},
		CreditPolicy: &policy,
		BrowserAccess: &BrowserAccess{
			AllowedOrigins: []string{"https://app.example.com"},
			TurnWebhook: BrowserTurnWebhook{
				URL:    "https://app.example.com/nvoken/webhook",
				Events: []WebhookEvent{WebhookEventWaiting, WebhookEventEnded},
			},
			Limits: BrowserRateLimits{
				MaxAdmissionsPerUserPerMinute: 6,
				MaxConcurrentTurnsPerTenant:   8,
				MaxConcurrentTurnsPerUser:     2,
			},
		},
	})
	if err != nil {
		t.Fatalf("register App: %v", err)
	}
	browser := registered["browser_access"].(map[string]any)
	if browser["turn_webhook"].(map[string]any)["url"] != "https://app.example.com/nvoken/webhook" {
		t.Fatalf("browser access = %#v", browser)
	}
	limits := registered["default_rate_limits"].(map[string]any)
	if limits["max_concurrent_turns"] != float64(20) {
		t.Fatalf("default rate limits = %#v", limits)
	}
}

func TestAppUpdateRefusesContradictionsAndEmptyPatches(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "test-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.UpdateApp(context.Background(), "app_test", UpdateAppOptions{}); err == nil {
		t.Fatal("empty update was accepted")
	}
	if _, err := client.UpdateApp(context.Background(), "app_test", UpdateAppOptions{
		DefaultRateLimits:      &AppDefaultRateLimits{MaxAdmissionsPerMinute: 1, MaxConcurrentTurns: 1},
		ClearDefaultRateLimits: true,
	}); err == nil {
		t.Fatal("set and clear together was accepted")
	}
}
