package nvoken

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSharedConformanceServerUsesCurrentModelContract(t *testing.T) {
	baseURL := os.Getenv("NVOKEN_CONFORMANCE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_CONFORMANCE_URL is not set")
	}
	client, err := NewClient(baseURL, "conformance")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	provider := "future_provider"
	models, err := client.ListModels(t.Context(), ListModelsOptions{Provider: &provider})
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models.Items) != 1 || models.Items[0].Provider != provider || models.Items[0].ID != "future-model" {
		t.Fatalf("models = %#v", models.Items)
	}
}

func TestTurnAdmissionRetryReusesGeneratedIdempotencyKey(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/turns" {
			http.NotFound(writer, request)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode Turn request: %v", err)
		}
		mu.Lock()
		keys = append(keys, fmt.Sprint(body["idempotency_key"]))
		attempts++
		attempt := attempts
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"code":"unavailable","message":"retry"}`))
			return
		}
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"id":"2d668a41-2603-7d68-b6f9-6f22b4e53e14","status":"queued"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test", WithRetryPolicy(RetryPolicy{
		MaxAttempts: 2,
		MinDelay:    time.Nanosecond,
		MaxDelay:    time.Nanosecond,
	}))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	turn, err := client.Inline(Behavior{}).Start(context.Background(), "hello", TurnOptions{TenantKey: "acme"})
	if err != nil {
		t.Fatalf("start Turn: %v", err)
	}
	if turn.ID() != "2d668a41-2603-7d68-b6f9-6f22b4e53e14" || turn.IdempotencyKey() == "" {
		t.Fatalf("Turn handle = %#v", turn)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] || keys[0] != turn.IdempotencyKey() {
		t.Fatalf("retry idempotency keys = %#v, handle = %q", keys, turn.IdempotencyKey())
	}
}

func TestRecoveredTurnUsesExplicitAccessAndDrivesHostTool(t *testing.T) {
	settled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Nvoken-Tenant-Key") != "acme" || request.Header.Get("X-Nvoken-User-Key") != "alice" {
			t.Errorf("access headers = tenant %q user %q", request.Header.Get("X-Nvoken-Tenant-Key"), request.Header.Get("X-Nvoken-User-Key"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/turns/f106972a-8e62-7183-8a65-d0a97c934cf5/result" && !settled:
			_, _ = writer.Write([]byte(`{
				"turn":{"id":"f106972a-8e62-7183-8a65-d0a97c934cf5","status":"waiting","ended_at":null,
				"tool_calls":[{"id":"9f8fd6b3-9060-783d-b759-45c8ec70e8cb","name":"lookup_order","mode":"host","status":"pending","arguments":{"order_id":"42"},"updated_at":"2026-08-26T12:00:00Z"}]},
				"messages":[],"output_text":null
			}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/turns/f106972a-8e62-7183-8a65-d0a97c934cf5/tool-results":
			var body struct {
				Results []struct {
					ToolCallID string `json:"tool_call_id"`
					Content    any    `json:"content"`
				} `json:"results"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode host result: %v", err)
			}
			if len(body.Results) != 1 || body.Results[0].ToolCallID != "9f8fd6b3-9060-783d-b759-45c8ec70e8cb" {
				t.Fatalf("host results = %#v", body.Results)
			}
			settled = true
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"content_expires_at":null,"conversation_id":null,"results":[],"status":"queued","tool_calls":[],"turn_id":"f106972a-8e62-7183-8a65-d0a97c934cf5"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/turns/f106972a-8e62-7183-8a65-d0a97c934cf5/result":
			_, _ = writer.Write([]byte(`{"turn":{"id":"f106972a-8e62-7183-8a65-d0a97c934cf5","status":"completed","ended_at":"2026-08-26T12:00:01Z"},"messages":[],"output_text":"found"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	called := false
	turn := client.Turn("f106972a-8e62-7183-8a65-d0a97c934cf5", TurnAccess{TenantKey: "acme", UserKey: "alice"}).BindTools(Tool{
		Name: "lookup_order",
		Handler: func(_ context.Context, input any, toolContext TurnToolContext) (any, error) {
			called = true
			if toolContext.TurnID != "f106972a-8e62-7183-8a65-d0a97c934cf5" || toolContext.ToolCallID != "9f8fd6b3-9060-783d-b759-45c8ec70e8cb" {
				t.Fatalf("tool context = %#v", toolContext)
			}
			arguments, ok := input.(map[string]any)
			if !ok || arguments["order_id"] != "42" {
				t.Fatalf("tool input = %#v", input)
			}
			return map[string]any{"status": "shipped"}, nil
		},
	})
	result, err := turn.Result(context.Background())
	if err != nil {
		t.Fatalf("recover and drive Turn: %v", err)
	}
	if !called || result.OutputText == nil || *result.OutputText != "found" {
		t.Fatalf("result = %#v, handler called = %v", result, called)
	}
}

func TestTurnUpdatesStopsAtTerminalFrame(t *testing.T) {
	streamRequests := 0
	resultReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/v1/turns/turn_stream/result" {
			resultReads++
			status := "running"
			endedAt := "null"
			outputText := "null"
			if resultReads > 1 {
				status = "completed"
				endedAt = `"2026-08-26T12:00:00Z"`
				outputText = `"done"`
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"turn":{"id":"turn_stream","status":%q,"ended_at":%s,"tool_calls":[]},"messages":[],"output_text":%s}`, status, endedAt, outputText)
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/v1/turns/turn_stream/stream" {
			http.NotFound(writer, request)
			return
		}
		streamRequests++
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "id: cursor-1\nevent: transcript.update\ndata: "+strings.ReplaceAll(`{
			"type":"transcript.update","has_more":false,
			"messages":[],"turn_changes":[{
				"turn_id":"turn_stream","conversation_id":null,"content_expires_at":null,
				"revision":2,"status":"completed","terminal":true,"current":true,
				"through_message_sequence":null,"error":null,"structured_output":null,
				"occurred_at":"2026-08-26T12:00:00Z"
			}],"cursor":"cursor-1"
		}`, "\n", "")+"\n\n")
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	var updates []TurnUpdate
	err = client.Turn("turn_stream", TurnAccess{TenantKey: "acme"}).Updates(
		context.Background(),
		UpdatesOptions{},
		func(update TurnUpdate) error {
			updates = append(updates, update)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Turn updates: %v", err)
	}
	if streamRequests != 1 || resultReads != 2 || len(updates) != 3 {
		t.Fatalf("stream requests = %d result reads = %d updates = %#v", streamRequests, resultReads, updates)
	}
	last := updates[len(updates)-1].Snapshot
	if last.Resource.Status != TurnCompleted || last.OutputText == nil || *last.OutputText != "done" {
		t.Fatalf("final update = %#v", last)
	}
}
