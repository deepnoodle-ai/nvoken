package nvoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Enabling browser access and then anonymous access is the go-live path, and
// it is the one an integrator is most likely to reach for raw because a facade
// that drops the fields looks like a facade that does not support them.
func TestAppProvisioningReachesBrowserAndAnonymousAccess(t *testing.T) {
	var registered, updated map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		target := &registered
		status := http.StatusCreated
		if request.Method == http.MethodPatch {
			target = &updated
			status = http.StatusOK
		}
		if err := json.NewDecoder(request.Body).Decode(target); err != nil {
			t.Errorf("decode %s: %v", request.Method, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(`{"id":"app_test","name":"acme","status":"active"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	policy := CreditPolicyRequired
	if _, err := client.RegisterApp(context.Background(), "acme", RegisterAppOptions{
		DefaultRateLimits: &AppDefaultRateLimits{
			MaxAdmissionsPerMinute:   120,
			MaxConcurrentInvocations: 20,
		},
		MachineConcurrencyLimits: &MachineConcurrencyLimits{
			MaxConcurrentInvocationsPerTenant: 12,
			MaxConcurrentInvocationsPerUser:   3,
		},
		CreditPolicy: &policy,
		BrowserAccess: &BrowserAccess{
			AllowedOrigins: []string{"https://app.example.com"},
			InvocationWebhook: BrowserInvocationWebhook{
				URL:    "https://app.example.com/nvoken/webhook",
				Events: []WebhookEvent{WebhookEventWaiting, WebhookEventEnded},
			},
			Limits: BrowserRateLimits{
				MaxAdmissionsPerUserPerMinute:     6,
				MaxConcurrentInvocationsPerTenant: 8,
				MaxConcurrentInvocationsPerUser:   2,
			},
		},
	}); err != nil {
		t.Fatalf("RegisterApp: %v", err)
	}
	browser, ok := registered["browser_access"].(map[string]any)
	if !ok {
		t.Fatalf("registered = %#v", registered)
	}
	if origins, _ := browser["allowed_origins"].([]any); len(origins) != 1 ||
		origins[0] != "https://app.example.com" {
		t.Errorf("allowed origins = %#v", browser["allowed_origins"])
	}
	if registered["credit_policy"] != "required" {
		t.Errorf("credit policy = %#v", registered["credit_policy"])
	}
	limits, _ := registered["default_rate_limits"].(map[string]any)
	if limits["max_admissions_per_minute"] != float64(120) {
		t.Errorf("default rate limits = %#v", registered["default_rate_limits"])
	}
	machineLimits, _ := registered["machine_concurrency_limits"].(map[string]any)
	if machineLimits["max_concurrent_invocations_per_tenant"] != float64(12) ||
		machineLimits["max_concurrent_invocations_per_user"] != float64(3) {
		t.Errorf("machine concurrency limits = %#v", registered["machine_concurrency_limits"])
	}

	if _, err := client.UpdateApp(context.Background(), "app_test", UpdateAppOptions{
		AnonymousAccess: &AnonymousAccess{
			AgentID:          "agent_test",
			VisitorAllowance: Money{Amount: "1.000000", Currency: "USD"},
			Limits: AnonymousRateLimits{
				MaxAdmissionsPerMinute:     30,
				MaxTokenExchangesPerMinute: 60,
			},
			SessionRetention: SessionRetention{TTLSeconds: 604800},
			WebhookDelivery:  AnonymousWebhookDeliveryDisabled,
		},
		MachineConcurrencyLimits: &MachineConcurrencyLimits{
			MaxConcurrentInvocationsPerTenant: 20,
			MaxConcurrentInvocationsPerUser:   4,
		},
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	anonymous, ok := updated["anonymous_access"].(map[string]any)
	if !ok || anonymous["agent_id"] != "agent_test" {
		t.Fatalf("updated = %#v", updated)
	}
	anonymousLimits, _ := anonymous["limits"].(map[string]any)
	if anonymousLimits["max_admissions_per_minute"] != float64(30) ||
		anonymousLimits["max_token_exchanges_per_minute"] != float64(60) ||
		anonymous["webhook_delivery"] != "disabled" {
		t.Fatalf("anonymous configuration = %#v", anonymous)
	}
	machineLimits, _ = updated["machine_concurrency_limits"].(map[string]any)
	if machineLimits["max_concurrent_invocations_per_tenant"] != float64(20) ||
		machineLimits["max_concurrent_invocations_per_user"] != float64(4) {
		t.Errorf("updated machine concurrency limits = %#v", updated["machine_concurrency_limits"])
	}

	// Clearing has to travel as an explicit null: an omitted member preserves
	// what is stored, so a facade that could only omit could never turn
	// browser access off.
	if _, err := client.UpdateApp(context.Background(), "app_test", UpdateAppOptions{
		ClearBrowserAccess:            true,
		ClearMachineConcurrencyLimits: true,
	}); err != nil {
		t.Fatalf("UpdateApp clearing browser access: %v", err)
	}
	value, present := updated["browser_access"]
	if !present || value != nil {
		t.Errorf("cleared browser access = %#v (present=%v)", value, present)
	}
	value, present = updated["machine_concurrency_limits"]
	if !present || value != nil {
		t.Errorf("cleared machine concurrency limits = %#v (present=%v)", value, present)
	}
}

// Setting and clearing the same member is a contradiction the caller has to
// resolve, not one the SDK should pick a side in.
func TestAppUpdateRefusesContradictionsAndEmptyPatches(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "test-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.UpdateApp(context.Background(), "app_test", UpdateAppOptions{}); err == nil {
		t.Error("empty update: want error")
	}
	if _, err := client.UpdateApp(context.Background(), "app_test", UpdateAppOptions{
		DefaultRateLimits: &AppDefaultRateLimits{
			MaxAdmissionsPerMinute:   1,
			MaxConcurrentInvocations: 1,
		},
		ClearDefaultRateLimits: true,
	}); err == nil {
		t.Error("set and clear together: want error")
	}
	if _, err := client.RegisterApp(context.Background(), "acme", RegisterAppOptions{
		BrowserAccess: &BrowserAccess{
			InvocationWebhook: BrowserInvocationWebhook{URL: "https://example.test/hook"},
		},
	}); err == nil {
		t.Error("browser access with no origins: want error")
	}
}
