package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	testInvocationID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
	testSessionID    = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
	testToolCallID   = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
)

func TestRuntimeWorkflowsAndOutputModes(t *testing.T) {
	baseURL := os.Getenv("NVOKEN_CONFORMANCE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_CONFORMANCE_URL is not set")
	}
	t.Setenv("NVOKEN_API_KEY", "test-key")
	resetServer(t, baseURL)

	output, err := executeCLI(t, baseURL, true,
		"invoke",
		"hello",
		"--agent", "support",
		"--idempotency-key", "cli-lost-ack",
		"--instructions", "help",
		"--provider", "openai",
		"--model", "gpt-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	var handle struct {
		InvocationID string `json:"invocation_id"`
		SessionID    string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(output), &handle); err != nil {
		t.Fatal(err)
	}
	if handle.InvocationID != testInvocationID || handle.SessionID != testSessionID {
		t.Fatalf("unexpected JSON handle: %#v", handle)
	}

	output, err = executeCLI(t, baseURL, false,
		"invoke",
		"hello again",
		"--agent", "support",
		"--idempotency-key", "cli-answer",
		"--instructions", "help",
		"--provider", "openai",
		"--model", "gpt-test",
	)
	if err != nil || output != "The charge was duplicated.\n\nA refund is queued.\n" {
		t.Fatalf("text answer output=%q err=%v", output, err)
	}

	output, err = executeCLI(
		t,
		baseURL,
		false,
		"model",
		"list",
	)
	if err != nil ||
		!strings.Contains(output, "openai\tgpt-test\tpriced\tGPT Test (recommended)\n") ||
		!strings.Contains(output, "future_provider\tfuture-model\tpriced\tFuture Model (recommended)\n") {
		t.Fatalf("model list output=%q err=%v", output, err)
	}

	output, err = executeCLI(
		t,
		baseURL,
		false,
		"model",
		"get",
		"--provider",
		"openai",
		"--model",
		"gpt-test",
	)
	if err != nil || output != "openai\tgpt-test\tcataloged=true\tpriced\n" {
		t.Fatalf("model get output=%q err=%v", output, err)
	}

	output, err = executeCLI(
		t,
		baseURL,
		false,
		"model",
		"pricing",
		"--provider",
		"openai",
		"--model",
		"gpt-test",
	)
	if err != nil || output != "openai\tgpt-test\tpriced\tconformance-pricing-v1\n" {
		t.Fatalf("model pricing output=%q err=%v", output, err)
	}
	output, err = executeCLI(
		t,
		baseURL,
		true,
		"model",
		"pricing",
		"--provider",
		"openai",
		"--model",
		"gpt-test",
	)
	if err != nil ||
		!strings.Contains(output, `"provider":"openai"`) ||
		!strings.Contains(output, `"id":"gpt-test"`) ||
		!strings.Contains(output, `"pricing":{`) ||
		!strings.Contains(output, `"status":"priced"`) {
		t.Fatalf("model pricing JSON output=%q err=%v", output, err)
	}
	output, err = executeCLI(
		t,
		baseURL,
		false,
		"model",
		"check",
		"openai/gpt-test",
	)
	if err != nil ||
		!strings.Contains(output, "PASS\topenai/gpt-test\tcataloged=true\tpricing=priced") {
		t.Fatalf("model check output=%q err=%v", output, err)
	}

	output, err = executeCLI(
		t,
		baseURL,
		false,
		"mcp",
		"list-tools",
		"--name",
		"support",
		"--url",
		"https://mcp.example.test/rpc",
		"--allowed-tool",
		"lookup",
		"--header",
		"Authorization=Bearer conformance-mcp-secret",
	)
	if err != nil ||
		output != "tool\tsupport__lookup\tlookup\tLook up a support record.\n" ||
		strings.Contains(output, "conformance-mcp-secret") {
		t.Fatalf("MCP list-tools output=%q err=%v", output, err)
	}

	output, err = executeCLI(t, baseURL, false, "invocation", "get", testInvocationID)
	if err != nil || !strings.Contains(output, testInvocationID+"\tcompleted\t"+testSessionID) {
		t.Fatalf("text invocation output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, false, "invocation", "result", testInvocationID)
	if err != nil ||
		!strings.Contains(output, testInvocationID+"\tcompleted\t"+testSessionID) ||
		!strings.Contains(output, "\nThe charge was duplicated.\n\nA refund is queued.\n") {
		t.Fatalf("text invocation result output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "invocation", "result", testInvocationID)
	if err != nil ||
		!strings.Contains(output, `"output_text":"The charge was duplicated.\n\nA refund is queued."`) ||
		!strings.Contains(output, `"structured_output":{"answer":"world"}`) {
		t.Fatalf("JSON invocation result output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "invocation", "list")
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("JSON invocation list output=%q err=%v", output, err)
	}
	output, err = executeCLI(
		t,
		baseURL,
		false,
		"invocation",
		"wait",
		"invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328",
		"--until",
		"actionable",
	)
	if err != nil || !strings.Contains(output, "\twaiting\t") {
		t.Fatalf("actionable wait output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "list")
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("JSON Session list output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "messages", testSessionID)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("JSON messages output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, false, "session", "messages", testSessionID)
	if err != nil || output != "1\tuser\thello\nnext_cursor\tmessages-page-2\n" {
		t.Fatalf("text messages output=%q err=%v", output, err)
	}
	output, err = executeCLI(
		t,
		baseURL,
		false,
		"session",
		"resolve",
		"--session-key",
		"ticket-A-42",
		"--tenant",
		"acme",
	)
	if err != nil || !strings.HasPrefix(output, testSessionID+"\t") {
		t.Fatalf("Session resolve output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "transcript", testSessionID)
	if err != nil || !strings.Contains(output, `"resume_cursor":"cursor-2"`) {
		t.Fatalf("JSON transcript output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, false, "session", "transcript", testSessionID)
	if err != nil ||
		!strings.Contains(output, "1\tuser\thello\n") ||
		!strings.Contains(output, "2\tassistant\tworld\n") ||
		!strings.Contains(output, "resume_cursor\tcursor-2\n") {
		t.Fatalf("text transcript output=%q err=%v", output, err)
	}

	resetServer(t, baseURL)
	output, err = executeCLI(
		t,
		baseURL,
		true,
		"tool-result",
		"submit",
		testInvocationID,
		`{"ok":true}`,
		"--tool-call-id",
		testToolCallID,
	)
	if err != nil || !strings.Contains(output, `"deduplicated":true`) {
		t.Fatalf("tool-result output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, false, "invocation", "cancel", testInvocationID)
	if err != nil || !strings.Contains(output, "\tcancelled\t") {
		t.Fatalf("cancel output=%q err=%v", output, err)
	}

	resetServer(t, baseURL)
	output, err = executeCLI(t, baseURL, false, "session", "stream", testSessionID)
	if err != nil || !strings.Contains(output, "transcript.update\tcursor-2") || !strings.Contains(output, "stream.end\tcursor-2") {
		t.Fatalf("stream output=%q err=%v", output, err)
	}
}

func TestSpecFileAdmissionAndDeltaRendering(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	var admission map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/invocations":
			if err := json.NewDecoder(request.Body).Decode(&admission); err != nil {
				t.Errorf("decode admission: %v", err)
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(response, `{
				"agent_id":"agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320",
				"session_id":"sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321",
				"invocation_id":"invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
				"status":"queued",
				"deduplicated":false,
				"deadline_at":"2026-07-21T12:05:00Z"
			}`)
		case "/v1/invocations/" + testInvocationID + "/stream":
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "id: cursor-1\n")
			_, _ = io.WriteString(response, "event: output_text.delta\n")
			_, _ = io.WriteString(response, `data: {"text":"streamed answer"}`+"\n\n")
			_, _ = io.WriteString(response, "id: cursor-2\n")
			_, _ = io.WriteString(response, "event: invocation.result\n")
			_, _ = io.WriteString(response, "data: {}\n\n")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	specPath := filepath.Join(t.TempDir(), "spec.json")
	specJSON := `{
		"instructions":"Preserve this exact public shape.",
		"model":{"provider":"openai","id":"gpt-test"},
		"limits":{"total_timeout_seconds":42,"max_iterations":3},
		"output":{"schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}
	}`
	if err := os.WriteFile(specPath, []byte(specJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := executeCLI(
		t,
		server.URL,
		true,
		"invoke",
		"hello",
		"--agent",
		"support",
		"--idempotency-key",
		"spec-file-test",
		"--spec-file",
		specPath,
	)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("spec-file admission output=%q err=%v", output, err)
	}
	var expectedSpec any
	if err := json.Unmarshal([]byte(specJSON), &expectedSpec); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(admission["spec"], expectedSpec) {
		t.Fatalf("admitted spec=%#v want=%#v", admission["spec"], expectedSpec)
	}

	output, err = executeCLI(
		t,
		server.URL,
		false,
		"invocation",
		"stream",
		testInvocationID,
	)
	if err != nil || output != "streamed answer\n" {
		t.Fatalf("delta stream output=%q err=%v", output, err)
	}
}

func TestConfigurationPrecedenceAndMissingCredential(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"base_url":"http://config.example/"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NVOKEN_CONFIG", configPath)
	t.Setenv("NVOKEN_BASE_URL", "")
	resolved, err := resolveBaseURL("", "")
	if err != nil || resolved != "http://config.example" {
		t.Fatalf("config precedence: %q %v", resolved, err)
	}
	t.Setenv("NVOKEN_BASE_URL", "http://environment.example/")
	resolved, err = resolveBaseURL("", "")
	if err != nil || resolved != "http://environment.example" {
		t.Fatalf("environment precedence: %q %v", resolved, err)
	}
	resolved, err = resolveBaseURL("http://flag.example/", "")
	if err != nil || resolved != "http://flag.example" {
		t.Fatalf("flag precedence: %q %v", resolved, err)
	}
	t.Setenv("NVOKEN_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("NVOKEN_BASE_URL", "")
	resolved, err = resolveBaseURL("", "")
	if err != nil || resolved != localBaseURL {
		t.Fatalf("local default: %q %v", resolved, err)
	}

	t.Setenv("NVOKEN_API_KEY", "")
	app := newApp()
	err = app.ExecuteContext(context.Background(), []string{"invocation", "get", testInvocationID})
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("missing credential error: %v", err)
	}
}

func TestEveryOperationHasACommand(t *testing.T) {
	data, err := os.ReadFile("../../sdk/operations.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Operations []struct {
			OperationID string `json:"operation_id"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, operation := range manifest.Operations {
		if operationCommands[operation.OperationID] == "" {
			t.Errorf("operation %s has no CLI command", operation.OperationID)
		}
	}
	if len(operationCommands) != len(manifest.Operations) {
		t.Fatalf("command coverage has %d entries for %d operations", len(operationCommands), len(manifest.Operations))
	}
}

func TestMCPHeadersFromEnvironmentStaySecretSafe(t *testing.T) {
	t.Setenv("NVOKEN_TEST_MCP_HEADERS", `{"Authorization":"Bearer environment-secret"}`)
	headers, err := mcpHeaders(nil, "NVOKEN_TEST_MCP_HEADERS")
	if err != nil || headers["Authorization"] != "Bearer environment-secret" {
		t.Fatalf("MCP environment headers: %#v err=%v", headers, err)
	}
	_, err = mcpHeaders([]string{"Authorization=Bearer flag-secret"}, "NVOKEN_TEST_MCP_HEADERS")
	if err == nil || strings.Contains(err.Error(), "environment-secret") ||
		strings.Contains(err.Error(), "flag-secret") {
		t.Fatalf("duplicate MCP header error exposed a secret: %v", err)
	}
}

func executeCLI(t *testing.T, baseURL string, jsonOutput bool, arguments ...string) (string, error) {
	t.Helper()
	global := []string{"--base-url", baseURL}
	if jsonOutput {
		global = append(global, "--json")
	}
	arguments = append(global, arguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := newApp().SetStdout(&stdout).SetStderr(&stderr)
	err := app.ExecuteContext(context.Background(), arguments)
	if err != nil && stderr.Len() > 0 {
		t.Log(stderr.String())
	}
	return stdout.String(), err
}

func resetServer(t *testing.T, baseURL string) {
	t.Helper()
	response, err := http.Post(baseURL+"/__test/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}
