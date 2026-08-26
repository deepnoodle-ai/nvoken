package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deepnoodle-ai/wonton/cli"
)

func TestTurnStartUsesTargetCommandAndEndpoint(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/agents/agent_1":
			_, _ = writer.Write([]byte(`{"id":"agent_1","agent_key":"analyst","name":"Analyst","owner":{"kind":"app"},"current_revision":1,"archived_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
		case "/v1/turns":
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"turn_1","status":"queued","tenant_key":"acme","attempt":0,"active_execution_ms":0,"conversation_id":null,"memory_space_id":null,"content_expires_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","deadline_at":null,"ended_at":null,"error":null,"stop_reason":null,"structured_output":null,"tool_calls":[]}`))
		case "/v1/turns/turn_1/result":
			_, _ = writer.Write([]byte(`{"turn":{"id":"turn_1","status":"queued","tenant_key":"acme","attempt":0,"active_execution_ms":0,"conversation_id":null,"memory_space_id":null,"content_expires_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","deadline_at":null,"ended_at":null,"error":null,"stop_reason":null,"structured_output":null,"tool_calls":[]},"messages":[],"output_text":null}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	resetActiveAuth()
	result := newApp().Test(t,
		cli.TestArgs("--json", "turn", "start", "--agent-id", "agent_1", "--tenant", "acme", "hello"),
		cli.TestEnv("NVOKEN_BASE_URL", server.URL),
		cli.TestEnv("NVOKEN_API_KEY", "secret"),
	)
	if !result.Success() {
		t.Fatalf("turn start: %v\n%s", result.Err, result.Stderr)
	}
	if !strings.Contains(result.Stdout, `"id": "turn_1"`) {
		t.Fatalf("stdout = %s", result.Stdout)
	}
	want := []string{"GET /v1/agents/agent_1", "POST /v1/turns", "GET /v1/turns/turn_1/result"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestLegacyRuntimeCommandsAreGone(t *testing.T) {
	for _, name := range []string{"invoke", "invocation", "session", "agent-definition", "memory"} {
		result := newApp().Test(t, cli.TestArgs(name, "--help"))
		if result.Success() {
			t.Fatalf("legacy command %q is still registered", name)
		}
	}
}
