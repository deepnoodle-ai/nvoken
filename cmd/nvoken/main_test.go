package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

	output, err = executeCLI(t, baseURL, false, "invocation", "get", testInvocationID)
	if err != nil || !strings.Contains(output, testInvocationID+"\tcompleted\t"+testSessionID) {
		t.Fatalf("text invocation output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, false, "invocation", "result", testInvocationID)
	if err != nil || !strings.Contains(output, testInvocationID+"\tcompleted\t"+testSessionID) || !strings.Contains(output, "\nworld\n") {
		t.Fatalf("text invocation result output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "invocation", "result", testInvocationID)
	if err != nil || !strings.Contains(output, `"output_text":"world"`) || !strings.Contains(output, `"structured_output":{"answer":"world"}`) {
		t.Fatalf("JSON invocation result output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "invocation", "list")
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("JSON invocation list output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "list")
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("JSON Session list output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "messages", testSessionID)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("JSON messages output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "transcript", testSessionID)
	if err != nil || !strings.Contains(output, `"resume_cursor":"cursor-2"`) {
		t.Fatalf("JSON transcript output=%q err=%v", output, err)
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
