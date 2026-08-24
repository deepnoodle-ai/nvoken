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

	"github.com/deepnoodle-ai/wonton/cli"
)

const (
	testAgentID      = "agent_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
	testInvocationID = "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
	testSessionID    = "sess_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
	testToolCallID   = "call_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
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
		"--agent-key", "support",
		"--idempotency-key", "cli-lost-ack",
		"--if-active", "supersede",
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
		"--agent-key", "support",
		"--idempotency-key", "cli-answer",
		"--if-active", "supersede",
		"--provider", "openai",
		"--model", "gpt-test",
	)
	// Text mode prints the prose as it streams and closes the line when the
	// turn settles. The composed answer is what it falls back to when nothing
	// streamed, which `invocation result` covers below.
	if err != nil || output != "streamed answer\n" {
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

	output, err = executeCLI(t, baseURL, false, "agent", "list", "--agent-key", "support")
	if err != nil || output != testAgentID+"\tsupport\tSupport\n" {
		t.Fatalf("Agent list output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "agent", "get", testAgentID)
	if err != nil || !strings.Contains(output, `"agent_key":"support"`) {
		t.Fatalf("Agent get output=%q err=%v", output, err)
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
	output, err = executeCLI(
		t,
		baseURL,
		true,
		"invocation",
		"list",
		"--agent-key",
		"support",
		"--status",
		"waiting",
		"--status",
		"queued",
		"--status",
		"running",
	)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("JSON invocation list output=%q err=%v", output, err)
	}
	queryState := readServerState(t, baseURL)
	if !reflect.DeepEqual(queryState.LastStatuses, []string{"waiting", "queued", "running"}) {
		t.Fatalf("repeatable status query = %#v", queryState.LastStatuses)
	}
	output, err = executeCLI(
		t,
		baseURL,
		false,
		"invocation",
		"wait",
		"inv_019b0a12-8d51-7f34-aed2-0e07c1bdb328",
		"--until",
		"actionable",
	)
	if err != nil || !strings.Contains(output, "\twaiting\t") {
		t.Fatalf("actionable wait output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "list", "--agent-key", "support")
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
	output, err = executeCLI(t, baseURL, true, "session", "compactions", testSessionID)
	if err != nil || !strings.Contains(output, `"status":"applied"`) {
		t.Fatalf("JSON compactions output=%q err=%v", output, err)
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
		"--agent-key",
		"support",
	)
	if err != nil || !strings.HasPrefix(output, testSessionID+"\t") {
		t.Fatalf("Session resolve output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, true, "session", "transcript", testSessionID)
	if err != nil || !strings.Contains(output, `"cursor":"cursor-2"`) {
		t.Fatalf("JSON transcript output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, baseURL, false, "session", "transcript", testSessionID)
	if err != nil ||
		!strings.Contains(output, "1\tuser\thello\n") ||
		!strings.Contains(output, "2\tassistant\tworld\n") ||
		!strings.Contains(output, "cursor\tcursor-2\n") {
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
	output, err = executeCLI(t, baseURL, false, "invocation", "interrupt", testInvocationID)
	if err != nil || !strings.Contains(output, "\tcompleted\t") ||
		!strings.HasSuffix(output, "\tinterrupted\n") {
		t.Fatalf("interrupt output=%q err=%v", output, err)
	}

	resetServer(t, baseURL)
	output, err = executeCLI(t, baseURL, false, "session", "stream", testSessionID)
	if err != nil || !strings.Contains(output, "transcript.update\tcursor-2") || !strings.Contains(output, "connection.closing\tcursor-2") {
		t.Fatalf("stream output=%q err=%v", output, err)
	}

	resetServer(t, baseURL)
	output, err = executeCLI(
		t,
		baseURL,
		false,
		"invocation",
		"stream",
		testInvocationID,
		"--deltas=false",
	)
	if err != nil ||
		!strings.Contains(output, "transcript.update\tcursor-2") ||
		strings.Contains(output, "streamed answer") {
		t.Fatalf("durable-only Invocation stream output=%q err=%v", output, err)
	}
	queryState = readServerState(t, baseURL)
	if queryState.LastDeltas != "false" {
		t.Fatalf("Invocation stream deltas query = %q", queryState.LastDeltas)
	}

	resetServer(t, baseURL)
	output, err = executeCLI(
		t,
		baseURL,
		false,
		"session",
		"stream",
		testSessionID,
		"--deltas=false",
	)
	if err != nil || !strings.Contains(output, "connection.closing\tcursor-2") {
		t.Fatalf("durable-only Session stream output=%q err=%v", output, err)
	}
	queryState = readServerState(t, baseURL)
	if queryState.LastInvocationFilter != "" {
		t.Fatalf("Session stream sent an Invocation filter = %q", queryState.LastInvocationFilter)
	}
	queryState = readServerState(t, baseURL)
	if queryState.LastDeltas != "false" {
		t.Fatalf("Session stream deltas query = %q", queryState.LastDeltas)
	}
}

func TestObservationCommandsSurfaceCorrelationAndNextCursor(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	const traceID = "11111111111111111111111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/invocations/" + testInvocationID + "/traces":
			_, _ = io.WriteString(response, `{
				"status":"available",
				"items":[{
					"trace_id":"`+traceID+`",
					"root_span_id":"1111111111111111",
					"name":"invoke_agent support",
					"status":"unset",
					"started_at":"2026-08-11T12:00:00Z",
					"span_count":1,
					"error_count":0,
					"is_partial":true,
					"attempt":2
				}],
				"next_cursor":"trace-page-2"
			}`)
		case "/v1/invocations/" + testInvocationID + "/logs":
			if got := request.URL.Query().Get("trace_id"); got != traceID {
				t.Errorf("trace_id = %q", got)
			}
			_, _ = io.WriteString(response, `{
				"status":"available",
				"items":[{
					"id":"log-1",
					"timestamp":"2026-08-11T12:00:01Z",
					"severity":"INFO",
					"severity_number":9,
					"message":"Invocation claimed",
					"attempt":2,
					"iteration":1,
					"lease_attempt":2
				}],
				"next_cursor":"logs-page-2"
			}`)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	output, err := executeCLI(t, server.URL, false, "invocation", "traces", testInvocationID)
	if err != nil || output != "2026-08-11T12:00:00Z\t"+traceID+"\tunset\tpartial\tattempt=2\t1\t0ms\nnext_cursor\ttrace-page-2\n" {
		t.Fatalf("trace output=%q err=%v", output, err)
	}
	output, err = executeCLI(
		t,
		server.URL,
		false,
		"invocation",
		"logs",
		testInvocationID,
		"--trace-id",
		traceID,
	)
	if err != nil || output != "2026-08-11T12:00:01Z\tINFO\tattempt=2\titeration=1\tlease_attempt=2\tInvocation claimed\nnext_cursor\tlogs-page-2\n" {
		t.Fatalf("log output=%q err=%v", output, err)
	}
}

// Invocation admission selects one tenant-scoped Agent and never carries its
// reusable Definition inline.
func TestAgentAdmissionAndDeltaRendering(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	var admission map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		// The stream is Session-scoped, so a bare Invocation ID resolves the
		// Session it belongs to before the stream opens.
		case "/v1/invocations/" + testInvocationID:
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{
				"id":"`+testInvocationID+`",
				"session_id":"`+testSessionID+`",
				"definition_id":"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
				"definition_revision":1,
				"status":"completed",
				"attempt":1,
				"active_execution_ms":0,
				"created_at":"2026-07-21T12:00:00Z",
				"updated_at":"2026-07-21T12:00:03Z"
			}`)
		case "/v1/invocations":
			admission = nil
			if err := json.NewDecoder(request.Body).Decode(&admission); err != nil {
				t.Errorf("decode admission: %v", err)
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(response, `{
				"id":"inv_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
				"agent_id":"agent_019b0a12-8d51-7f34-aed2-0e07c1bdb320",
				"agent_key":"support",
				"session_id":"sess_019b0a12-8d51-7f34-aed2-0e07c1bdb321",
				"definition_id":"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
				"definition_revision":1,
				"definition":null,
				"status":"queued",
				"stop_reason":null,
				"attempt":0,
				"error":null,
				"usage":null,
				"provenance":null,
				"structured_output":null,
				"structured_output_provenance":null,
				"metadata":null,
				"limits":{"total_timeout_seconds":300,"active_timeout_seconds":120,"waiting_timeout_seconds":180,"max_iterations":3},
				"active_execution_ms":0,
				"deduplicated":false,
				"deadline_at":"2026-07-21T12:05:00Z",
				"created_at":"2026-07-21T12:00:00Z",
				"updated_at":"2026-07-21T12:00:00Z",
				"ended_at":null
			}`)
		case "/v1/sessions/" + testSessionID + "/stream":
			response.Header().Set("Content-Type", "text/event-stream")
			// Complete frames, as the service sends them. The reader refuses a
			// payload missing a member the contract requires, so a fixture that
			// abbreviates one is describing a response that cannot arrive.
			_, _ = io.WriteString(response, "event: message.delta\n")
			_, _ = io.WriteString(response, `data: {"type":"message.delta","session_id":"`+testSessionID+`",`+
				`"invocation_id":"`+testInvocationID+`","attempt":1,"message_id":"pmsg_1",`+
				`"content_index":0,"kind":"text","delta":"streamed answer",`+
				`"emitted_at":"2026-07-21T12:00:02Z"}`+"\n\n")
			_, _ = io.WriteString(response, "id: cursor-2\n")
			_, _ = io.WriteString(response, "event: transcript.update\n")
			_, _ = io.WriteString(response, `data: {"type":"transcript.update","session_id":"`+testSessionID+`",`+
				`"messages":[],"invocation_changes":[`+
				`{"invocation_id":"`+testInvocationID+`","revision":1,"status":"completed","terminal":true,`+
				`"through_message_sequence":null,"error":null,"structured_output":null,`+
				`"occurred_at":"2026-07-21T12:00:03Z"}],"cursor":"cursor-2"}`+"\n\n")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	output, err := executeCLI(
		t,
		server.URL,
		true,
		"invoke",
		"hello",
		"--agent-key",
		"support",
		"--idempotency-key",
		"flat-admission-test",
		"--parent-invocation-id",
		testInvocationID,
		"--tool-call-id",
		testToolCallID,
	)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("flat admission output=%q err=%v", output, err)
	}
	for _, leaked := range []string{"instructions", "model", "limits", "tools", "definition", "definition_id"} {
		if admission[leaked] != nil {
			t.Fatalf("execution field %q leaked to the top level: %#v", leaked, admission)
		}
	}
	if admission["agent_key"] != "support" || admission["agent_id"] != nil {
		t.Fatalf("admission did not carry only the Agent key: %#v", admission)
	}
	if trigger, ok := admission["triggered_by"].(map[string]any); !ok ||
		trigger["type"] != "tool_call" ||
		trigger["parent_invocation_id"] != testInvocationID ||
		trigger["tool_call_id"] != testToolCallID {
		t.Fatalf("admission trigger = %#v", admission["triggered_by"])
	}

	output, err = executeCLI(
		t,
		server.URL,
		true,
		"invoke",
		"inspect these",
		"--agent-key",
		"support",
		"--idempotency-key",
		"definition-url-test",
		"--image-url",
		"https://media.example.test/chart.png",
		"--document-url",
		"https://media.example.test/report.pdf",
	)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("definition URL admission output=%q err=%v", output, err)
	}
	if admission["agent_key"] != "support" || admission["agent_id"] != nil {
		t.Fatalf("definition URL admission=%#v", admission)
	}
	content, ok := admission["input"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("definition URL input=%#v", admission["input"])
	}

	output, err = executeCLI(
		t,
		server.URL,
		false,
		"invocation",
		"stream",
		testInvocationID,
	)
	if err != nil || output != "streamed answer\ntranscript.update\tcursor-2\n" {
		t.Fatalf("delta stream output=%q err=%v", output, err)
	}
}

// TestModelCheckProbeCarriesAUsableOutputBudget pins the budget the credential
// probe admits with. It was 8 tokens, which providers reject outright on a
// reasoning model because the budget covers reasoning tokens too — so `model
// check` reported FAIL for providers that were configured and healthy. A
// credential check that cannot tell "your key is wrong" from "your key is fine"
// is worse than none, so the floor is asserted rather than left to a literal.
func TestModelCheckProbeCarriesAUsableOutputBudget(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	var admission map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/v1/models/openai/gpt-test":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{
				"provider":"openai",
				"id":"gpt-test",
				"cataloged":true,
				"pricing":{"status":"priced"}
			}`)
		case "/v1/invocations":
			if err := json.NewDecoder(request.Body).Decode(&admission); err != nil {
				t.Errorf("decode admission: %v", err)
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(response, `{
				"agent_id":"agent_019b0a12-8d51-7f34-aed2-0e07c1bdb320",
				"agent_key":"support",
				"session_id":"sess_019b0a12-8d51-7f34-aed2-0e07c1bdb321",
				"id":"`+testInvocationID+`",
				"definition_id":"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
				"definition_revision":1,
				"status":"queued",
				"deduplicated":false,
				"deadline_at":"2026-07-21T12:05:00Z"
			}`)
		case "/v1/invocations/" + testInvocationID:
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{
				"id":"`+testInvocationID+`",
				"status":"completed"
			}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	if _, err := executeCLI(t, server.URL, false, "model", "check", "openai/gpt-test"); err != nil {
		t.Fatalf("model check: %v", err)
	}

	definition, ok := admission["overrides"].(map[string]any)
	if !ok {
		t.Fatalf("probe request carried no safe overrides: %#v", admission)
	}
	limits, ok := definition["limits"].(map[string]any)
	if !ok {
		t.Fatalf("probe request carried no limits: %#v", admission)
	}
	budget, ok := limits["max_output_tokens"].(float64)
	if !ok {
		t.Fatalf("probe definition carried no max_output_tokens: %#v", limits)
	}
	if budget < 1024 {
		t.Fatalf("probe output budget = %v, too low for a reasoning model to answer", budget)
	}

	// The flag exists so an operator can probe a model with a tighter ceiling,
	// but it must not be able to reintroduce the failure by accident.
	if _, err := executeCLI(
		t,
		server.URL,
		false,
		"model",
		"check",
		"openai/gpt-test",
		"--max-output-tokens",
		"0",
	); err == nil {
		t.Fatal("model check accepted a zero output budget")
	}
}

func TestInvokeRejectsInvalidIfActiveBeforeNetwork(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		called = true
		http.Error(response, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := executeCLI(
		t,
		server.URL,
		false,
		"invoke",
		"hello",
		"--agent-key",
		"support",
		"--provider",
		"openai",
		"--model",
		"gpt-test",
		"--if-active",
		"queue",
	)
	if err == nil || called {
		t.Fatalf("invalid if-active error = %v, network called = %t", err, called)
	}

	_, err = executeCLI(
		t,
		server.URL,
		false,
		"invoke",
		"hello",
		"--agent-key",
		"support",
		"--agent-id",
		testAgentID,
		"--provider",
		"openai",
		"--model",
		"gpt-test",
	)
	if err == nil || called {
		t.Fatalf("mixed Agent identity error = %v, network called = %t", err, called)
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
		path := operationCommands[operation.OperationID]
		if path == "" {
			t.Errorf("operation %s has no CLI command", operation.OperationID)
			continue
		}
		assertDocumentedCommandHelp(t, path)
	}
	if len(operationCommands) != len(manifest.Operations) {
		t.Fatalf("command coverage has %d entries for %d operations", len(operationCommands), len(manifest.Operations))
	}
}

func TestEveryCompositeAndLocalCommandHasDocumentedHelp(t *testing.T) {
	for _, path := range []string{
		"completion",
		"invocation wait",
		"invocation stream",
		"model pricing",
		"model check",
		"session resolve",
		"auth login",
		"auth list",
		"auth use",
		"auth logout",
		"auth revoke",
	} {
		assertDocumentedCommandHelp(t, path)
	}
}

func TestCompleteRequestFilesAndBatchToolResults(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	captured := make(map[string]map[string]any)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		key := request.Method + " " + request.URL.Path
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", key, err)
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		captured[key] = body
		response.Header().Set("Content-Type", "application/json")
		switch key {
		case "POST /v1/invocations":
			response.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(response, `{
				"id":"inv_test","agent_id":"agent_test","agent_key":"support",
				"session_id":"sess_test","definition_id":"def_test",
				"definition_revision":1,"status":"queued","active_execution_ms":0,
				"attempt":0,"created_at":"2026-08-16T12:00:00Z","updated_at":"2026-08-16T12:00:00Z"
			}`)
		case "POST /v1/apps":
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"app":{"id":"app_test","name":"raw-app"},"signing_keys":[]}`)
		case "PATCH /v1/apps/app_test":
			_, _ = io.WriteString(response, `{"id":"app_test","name":"raw-app"}`)
		case "POST /v1/sessions":
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"id":"sess_test"}`)
		case "POST /v1/sessions/sess_source/fork":
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{"id":"sess_fork"}`)
		case "POST /v1/invocations/inv_test/tool-results":
			response.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(response, `{"invocation_id":"inv_test","session_id":"sess_test","status":"queued","results":[]}`)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	writeRequest := func(name, value string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "invocation",
			args: []string{"invoke", "--request-file", writeRequest("invocation.json", `{
				"agent_key":"support","input":"hello","idempotency_key":"raw-invoke",
				"session_options":{"authorization_context":{"case":"42"},"retention":{"ttl_seconds":3600}},
				"on_budget_exhausted":"hold"
			}`)},
		},
		{
			name: "app register",
			args: []string{"app", "register", "--request-file", writeRequest("app.json", `{
				"name":"raw-app","anonymous_access":{"enabled":true},
				"default_rate_limits":{"invocations_per_minute":12}
			}`)},
		},
		{
			name: "app update",
			args: []string{"app", "update", "app_test", "--request-file", writeRequest("app-update.json", `{
				"display_name":null,"callback_timeout_seconds":20
			}`)},
		},
		{
			name: "session create",
			args: []string{"session", "create", "--request-file", writeRequest("session.json", `{
				"agent_id":"agent_test","session_options":{"authorization_context":{"branch":"root"},"retention":{"ttl_seconds":3600}}
			}`)},
		},
		{
			name: "session fork",
			args: []string{"session", "fork", "sess_source", "--request-file", writeRequest("fork.json", `{
				"from_message":7,"session_options":{"authorization_context":{"branch":"alternate"}}
			}`)},
		},
		{
			name: "tool result batch",
			args: []string{"tool-result", "submit", "inv_test", "--file", writeRequest("results.json", `[
				{"tool_call_id":"call_one","content":{"answer":1}},
				{"tool_call_id":"call_two","content":"failed","is_error":true}
			]`)},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			output, err := executeCLI(t, server.URL, true, test.args...)
			if err != nil || !json.Valid([]byte(output)) {
				t.Fatalf("output=%q err=%v", output, err)
			}
		})
	}

	invocation := captured["POST /v1/invocations"]
	options, ok := invocation["session_options"].(map[string]any)
	if !ok || options["authorization_context"].(map[string]any)["case"] != "42" ||
		invocation["on_budget_exhausted"] != "hold" {
		t.Fatalf("complete Invocation request was not preserved: %#v", invocation)
	}
	registered := captured["POST /v1/apps"]
	if registered["anonymous_access"].(map[string]any)["enabled"] != true || registered["default_rate_limits"] == nil {
		t.Fatalf("complete App request was not preserved: %#v", registered)
	}
	updated := captured["PATCH /v1/apps/app_test"]
	if value, present := updated["display_name"]; !present || value != nil {
		t.Fatalf("explicit App null was not preserved: %#v", updated)
	}
	createdSession := captured["POST /v1/sessions"]
	if createdSession["agent_id"] != "agent_test" || createdSession["session_options"] == nil {
		t.Fatalf("complete Session request was not preserved: %#v", createdSession)
	}
	forkedSession := captured["POST /v1/sessions/sess_source/fork"]
	if forkedSession["from_message"] != float64(7) || forkedSession["session_options"] == nil {
		t.Fatalf("complete fork request was not preserved: %#v", forkedSession)
	}
	results, ok := captured["POST /v1/invocations/inv_test/tool-results"]["results"].([]any)
	if !ok || len(results) != 2 || results[1].(map[string]any)["is_error"] != true {
		t.Fatalf("batch tool results were not preserved: %#v", captured["POST /v1/invocations/inv_test/tool-results"])
	}
}

func TestCLIMapsAllAddedFiltersAndExtensibleValues(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	queries := make(map[string]map[string][]string)
	var credentialBody map[string]any
	var credentialIdempotency string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		key := request.Method + " " + request.URL.Path
		queries[key] = request.URL.Query()
		response.Header().Set("Content-Type", "application/json")
		switch key {
		case "GET /v1/invocations":
			_, _ = io.WriteString(response, `{"items":[],"has_more":false,"next_cursor":null}`)
		case "GET /v1/sessions/sess_test/messages":
			_, _ = io.WriteString(response, `{"items":[],"has_more":false,"next_cursor":null}`)
		case "GET /v1/usage/timeseries":
			_, _ = io.WriteString(response, `{
				"buckets":[],"start_at":"2026-08-01T00:00:00Z","end_at":"2026-08-02T00:00:00Z",
				"interval":"day","timezone":"UTC","totals":{
					"activity":{"invocations":0},"model":{"model_calls":0,"input_tokens":0,"output_tokens":0},
					"tools":{"tool_calls":0},"cost":{"model_cost":{"amount":"0","currency":"USD"}}
				}
			}`)
		case "GET /v1/usage/records":
			response.Header().Set("Content-Type", "text/csv")
			response.Header().Set("X-Nvoken-Next-Cursor", "usage-page-2")
			_, _ = io.WriteString(response, "id,status\ncall_1,succeeded\n")
		case "GET /v1/models/future_provider/future-model":
			_, _ = io.WriteString(response, `{"provider":"future_provider","id":"future-model","cataloged":true,"pricing":{"status":"unpriced"}}`)
		case "POST /v1/identity/credentials":
			credentialIdempotency = request.Header.Get("Idempotency-Key")
			if err := json.NewDecoder(request.Body).Decode(&credentialBody); err != nil {
				t.Errorf("decode credential: %v", err)
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(response, `{
				"credential":{"id":"cred_test","name":"reporter","prefix":"nvk_test","status":"active",
				"type":"app","app_id":"app_test",
				"created_at":"2026-08-16T12:00:00Z","updated_at":"2026-08-16T12:00:00Z"},
				"secret":"nvk_test.secret","delivery_expires_at":"2026-08-16T12:05:00Z","replayed":false
			}`)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	commands := [][]string{
		{"invocation", "list", "--tenant", "tenant-a", "--default-tenant", "--parent-invocation-id", "null"},
		{"session", "messages", "sess_test", "--order", "desc"},
		{
			"usage", "timeseries",
			"--start-at", "2026-08-01T00:00:00Z", "--end-at", "2026-08-02T00:00:00Z", "--interval", "day",
			"--provider-key-source", "tenant_byok", "--provider-key-id", "pkey_test",
			"--credential-family-id", "cfam_test", "--authentication-method", "api_key",
			"--call-kind", "generation", "--tool-name", "lookup", "--tool-mode", "host",
			"--group-by", "authentication_method",
		},
		{"model", "get", "--provider", "future_provider", "--model", "future-model"},
		{
			"credentials", "create", "--name", "reporter", "--type", "app",
			"--app-id", "app_test", "--idempotency-key", "credential-retry-key",
		},
	}
	for _, arguments := range commands {
		output, err := executeCLI(t, server.URL, true, arguments...)
		if err != nil || !json.Valid([]byte(output)) {
			t.Fatalf("%v output=%q err=%v", arguments, output, err)
		}
	}
	csv, err := executeCLI(
		t,
		server.URL,
		false,
		"usage", "records",
		"--start-at", "2026-08-01T00:00:00Z",
		"--end-at", "2026-08-02T00:00:00Z",
		"--format", "csv",
	)
	if err != nil || csv != "id,status\ncall_1,succeeded\n" {
		t.Fatalf("CSV output=%q err=%v", csv, err)
	}

	invocationQuery := queries["GET /v1/invocations"]
	if invocationQuery["tenant_key"][0] != "tenant-a" ||
		invocationQuery["default_tenant"][0] != "true" ||
		invocationQuery["parent_invocation_id"][0] != "null" {
		t.Fatalf("Invocation query = %#v", invocationQuery)
	}
	messageQuery := queries["GET /v1/sessions/sess_test/messages"]
	if messageQuery["order"][0] != "desc" {
		t.Fatalf("Session message query = %#v", messageQuery)
	}
	usageQuery := queries["GET /v1/usage/timeseries"]
	for key, expected := range map[string]string{
		"provider_key_source":   "tenant_byok",
		"provider_key_id":       "pkey_test",
		"credential_family_id":  "cfam_test",
		"authentication_method": "api_key",
		"call_kind":             "generation",
		"tool_name":             "lookup",
		"tool_mode":             "host",
		"group_by":              "authentication_method",
	} {
		if len(usageQuery[key]) != 1 || usageQuery[key][0] != expected {
			t.Errorf("usage query %s = %#v, want %q", key, usageQuery[key], expected)
		}
	}
	if credentialIdempotency != "credential-retry-key" || credentialBody["type"] != "app" || credentialBody["app_id"] != "app_test" {
		t.Fatalf("credential request key=%q body=%#v", credentialIdempotency, credentialBody)
	}
	if recordsQuery := queries["GET /v1/usage/records"]; recordsQuery["format"][0] != "csv" {
		t.Fatalf("usage records query = %#v", recordsQuery)
	}
}

func assertDocumentedCommandHelp(t *testing.T, path string) {
	t.Helper()
	t.Run(strings.ReplaceAll(path, " ", "/"), func(t *testing.T) {
		arguments := append(strings.Fields(path), "--help")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		app := newApp().SetStdout(&stdout).SetStderr(&stderr)
		if err := app.ExecuteContext(context.Background(), arguments); err != nil && !cli.IsHelpRequested(err) {
			t.Fatalf("render help: %v\nstderr: %s", err, stderr.String())
		}
		help := stdout.String()
		firstLine, _, _ := strings.Cut(help, "\n")
		_, description, found := strings.Cut(firstLine, " - ")
		if !found || strings.TrimSpace(description) == "" {
			t.Fatalf("missing command description:\n%s", help)
		}
		assertHelpRowsDocumented(t, help, "Arguments:")
		assertHelpRowsDocumented(t, help, "Flags:")
	})
}

func assertHelpRowsDocumented(t *testing.T, help, heading string) {
	t.Helper()
	section := false
	for _, line := range strings.Split(help, "\n") {
		if line == heading {
			section = true
			continue
		}
		if !section {
			continue
		}
		if strings.TrimSpace(line) == "" {
			return
		}
		fields := strings.Fields(line)
		minimum := 2
		if strings.Contains(line, ", --") {
			minimum = 3
		}
		if len(fields) < minimum {
			t.Errorf("undocumented %s row %q\n%s", strings.TrimSuffix(heading, ":"), line, help)
		}
	}
}

func TestAnonymousTokenAndMemoryCommands(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "operator-key")
	const (
		testAppID    = "app_test"
		testMemoryID = "mem_test"
	)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/apps/"+testAppID+"/anonymous-tokens":
			if authorization := request.Header.Get("Authorization"); authorization != "" {
				t.Errorf("anonymous-token Authorization = %q", authorization)
			}
			if origin := request.Header.Get("Origin"); origin != "https://chat.example.test" {
				t.Errorf("anonymous-token Origin = %q", origin)
			}
			if idempotencyKey := request.Header.Get("Idempotency-Key"); idempotencyKey != "anonymous-exchange-1" {
				t.Errorf("anonymous-token Idempotency-Key = %q", idempotencyKey)
			}
			var body struct {
				VisitorToken *string `json:"visitor_token"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode anonymous-token request: %v", err)
			} else if body.VisitorToken == nil || *body.VisitorToken != "visitor-old" {
				t.Errorf("visitor_token = %#v", body.VisitorToken)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"access_token":"access-new","access_token_expires_in_seconds":900,"visitor_token":"visitor-new","visitor_token_expires_at":"2027-08-12T12:00:00Z","session_id":null}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/memories":
			if authorization := request.Header.Get("Authorization"); authorization != "Bearer operator-key" {
				t.Errorf("memory list Authorization = %q", authorization)
			}
			query := request.URL.Query()
			if query.Get("agent_id") != testAgentID ||
				query.Get("tenant_key") != "acme" ||
				query.Get("query") != "theme" ||
				query.Get("search_mode") != "hybrid" ||
				query.Get("kind") != "preference" ||
				query.Get("limit") != "10" {
				t.Errorf("memory list query = %q", request.URL.RawQuery)
			}
			_, _ = response.Write([]byte(`{"items":[{"memory":{"id":"mem_test","agent_id":"` + testAgentID + `","tenant_key":"acme","key":"preference.theme","kind":"preference","content":"Use dark mode.","importance":80,"pinned":true,"version":1,"last_accessed_at":"2026-08-12T12:00:00Z","created_at":"2026-08-12T12:00:00Z","updated_at":"2026-08-12T12:00:00Z"},"score":0.875}],"search_coverage":{"indexed_entries":1,"total_entries":1,"complete":true,"semantic_available":true},"has_more":false,"next_cursor":null}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/memories/"+testMemoryID:
			_, _ = response.Write([]byte(`{"id":"mem_test","agent_id":"` + testAgentID + `","tenant_key":"acme","key":"preference.theme","kind":"preference","content":"Use dark mode.","importance":80,"pinned":true,"version":1,"last_accessed_at":"2026-08-12T12:00:00Z","created_at":"2026-08-12T12:00:00Z","updated_at":"2026-08-12T12:00:00Z"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/memories/"+testMemoryID:
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	output, err := executeCLI(
		t,
		server.URL,
		true,
		"app", "anonymous-token", testAppID,
		"--origin", "https://chat.example.test",
		"--idempotency-key", "anonymous-exchange-1",
		"--visitor-token", "visitor-old",
	)
	if err != nil || !strings.Contains(output, `"access_token":"access-new"`) ||
		!strings.Contains(output, `"visitor_token":"visitor-new"`) {
		t.Fatalf("anonymous-token output=%q err=%v", output, err)
	}

	output, err = executeCLI(
		t,
		server.URL,
		false,
		"memory", "list",
		"--agent-id", testAgentID,
		"--tenant-key", "acme",
		"--query", "theme",
		"--search-mode", "hybrid",
		"--kind", "preference",
		"--limit", "10",
	)
	if err != nil ||
		!strings.Contains(output, "mem_test\tpreference\tpreference.theme\tscore=0.875000") ||
		!strings.Contains(output, "search_coverage\tindexed=1\ttotal=1\tcomplete=true\tsemantic_available=true") {
		t.Fatalf("memory list output=%q err=%v", output, err)
	}

	output, err = executeCLI(t, server.URL, false, "memory", "get", testMemoryID)
	if err != nil || !strings.Contains(output, "mem_test\tpreference\tpreference.theme") ||
		!strings.Contains(output, "Use dark mode.") {
		t.Fatalf("memory get output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, server.URL, false, "memory", "delete", testMemoryID)
	if err != nil || output != "deleted\t"+testMemoryID+"\n" {
		t.Fatalf("memory delete: %v", err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4", requests)
	}
}

func TestArchiveLifecycleCommands(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "operator-key")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/agent-definitions":
			if request.URL.Query().Get("include_archived") != "true" || request.URL.Query().Get("limit") != "10" {
				t.Errorf("Agent Definition query = %q", request.URL.RawQuery)
			}
			_, _ = response.Write([]byte(`{"items":[{"id":"def_test","revision":2,"model":{"provider":"openai","id":"gpt-test"}}],"has_more":false,"next_cursor":null}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/agent-definitions/def_test":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/v1/agent-definitions/def_test/restore":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/apps":
			if request.URL.Query().Get("status") != "archived" {
				t.Errorf("App query = %q", request.URL.RawQuery)
			}
			_, _ = response.Write([]byte(`{"items":[{"id":"app_test","name":"support"}]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/apps/app_test":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/v1/apps/app_test/restore":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/orgs":
			if request.URL.Query().Get("status") != "all" {
				t.Errorf("Org query = %q", request.URL.RawQuery)
			}
			_, _ = response.Write([]byte(`{"items":[{"id":"org_test","display_name":"Example"}]}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/orgs/org_test":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && request.URL.Path == "/v1/orgs/org_test/restore":
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	commands := [][]string{
		{"agent-definition", "list", "--include-archived", "--limit", "10"},
		{"agent-definition", "archive", "def_test"},
		{"agent-definition", "restore", "def_test"},
		{"app", "list", "--status", "archived"},
		{"app", "archive", "app_test"},
		{"app", "restore", "app_test"},
		{"org", "list", "--status", "all"},
		{"org", "archive", "org_test"},
		{"org", "restore", "org_test"},
	}
	for _, arguments := range commands {
		output, err := executeCLI(t, server.URL, false, arguments...)
		if err != nil {
			t.Fatalf("%v: %v", arguments, err)
		}
		if (arguments[1] == "archive" || arguments[1] == "restore") && strings.TrimSpace(output) == "" {
			t.Fatalf("%v returned no mutation receipt", arguments)
		}
	}
	if requests != len(commands) {
		t.Fatalf("requests = %d, want %d", requests, len(commands))
	}
}

// The rotation is a sequence, and the CLI is where an operator walks it. Drive
// the whole thing — mint, activate, retire — so the commands are pinned to the
// routes and to the order the runbook prescribes.
func TestSigningKeyRotationCommands(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "operator-key")
	const appID = "app_test"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/apps/"+appID+"/signing-keys":
			_, _ = response.Write([]byte(`{"items":[{"purpose":"callback","key_id":"key_cb","version":1,"active":true,"created_at":"2026-08-12T12:00:00Z"}]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/apps/"+appID+"/signing-keys":
			var body struct {
				Purpose  string `json:"purpose"`
				Activate *bool  `json:"activate"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode mint request: %v", err)
			} else if body.Purpose != "callback" || body.Activate != nil {
				// An ordinary mint must not activate. Sending `activate` at all
				// here would be the difference between a rotation nobody
				// notices and one that fails every delivery until the receiver
				// catches up.
				t.Errorf("mint body purpose=%q activate=%#v", body.Purpose, body.Activate)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"purpose":"callback","key_id":"key_cb","version":2,"active":false,"secret":"minted-secret","created_at":"2026-08-12T12:05:00Z"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/apps/"+appID+"/signing-keys/callback/2/activate":
			_, _ = response.Write([]byte(`{"purpose":"callback","key_id":"key_cb","version":2,"active":true,"created_at":"2026-08-12T12:05:00Z"}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/apps/"+appID+"/signing-keys/callback/1":
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	output, err := executeCLI(t, server.URL, false, "signing-key", "list", appID)
	if err != nil || !strings.Contains(output, "callback\t1\tactive=true\tkey_cb") {
		t.Fatalf("signing-key list output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, server.URL, false, "signing-key", "mint", appID, "--purpose", "callback")
	if err != nil || !strings.Contains(output, "callback\t2\tactive=false\tkey_cb\tminted-secret") {
		t.Fatalf("signing-key mint output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, server.URL, false, "signing-key", "activate", appID, "callback", "2")
	if err != nil || !strings.Contains(output, "callback\t2\tactive=true\tkey_cb") {
		t.Fatalf("signing-key activate output=%q err=%v", output, err)
	}
	output, err = executeCLI(t, server.URL, false, "signing-key", "retire", appID, "callback", "1")
	if err != nil || !strings.Contains(output, "retired\tcallback\t1") {
		t.Fatalf("signing-key retire output=%q err=%v", output, err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4", requests)
	}

	// A bad purpose or version is refused before any request goes out, so a
	// typo cannot reach a route that would answer 404 and read as "no such
	// key" instead of "you typed it wrong".
	if _, err := executeCLI(t, server.URL, false, "signing-key", "activate", appID, "anonymous_token", "2"); err == nil {
		t.Fatal("activate accepted a purpose outside the receiver-facing pair")
	}
	if _, err := executeCLI(t, server.URL, false, "signing-key", "retire", appID, "callback", "0"); err == nil {
		t.Fatal("retire accepted version 0")
	}
	if requests != 4 {
		t.Fatalf("rejected arguments still reached the service: requests = %d", requests)
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

type conformanceQueryState struct {
	LastStatuses         []string `json:"last_statuses"`
	LastDeltas           string   `json:"last_deltas"`
	LastInvocationFilter string   `json:"last_invocation_filter"`
}

func readServerState(t *testing.T, baseURL string) conformanceQueryState {
	t.Helper()
	response, err := http.Get(baseURL + "/__test/state")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state conformanceQueryState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// The scope flags exist so an operator addressing an id from a stale link
// cannot act outside the tenant or end user they meant. That is only true if
// the assertion actually leaves the process, so this asserts the headers.
func TestScopeFlagsNarrowEveryRequest(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "test-key")
	var observed http.Header
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		observed = request.Header.Clone()
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"items":[],"has_more":false,"next_cursor":null}`)
	}))
	t.Cleanup(server.Close)

	if _, err := executeCLI(t, server.URL, true, "session", "list"); err != nil {
		t.Fatalf("unscoped session list: %v", err)
	}
	if got := observed.Get("X-Nvoken-Tenant-Key"); got != "" {
		t.Errorf("unscoped tenant header = %q, want none", got)
	}

	if _, err := executeCLI(
		t, server.URL, true,
		"--scope-tenant-key", "acme",
		"--scope-user-key", "user-7c1f",
		"session", "list",
	); err != nil {
		t.Fatalf("scoped session list: %v", err)
	}
	if got := observed.Get("X-Nvoken-Tenant-Key"); got != "acme" {
		t.Errorf("tenant header = %q", got)
	}
	if got := observed.Get("X-Nvoken-User-Key"); got != "user-7c1f" {
		t.Errorf("user header = %q", got)
	}
}

func TestProbesNeedNoCredentialAndFailLoudlyWhenRefused(t *testing.T) {
	t.Setenv("NVOKEN_API_KEY", "")
	var reached []string
	var sawAuthorization bool
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		reached = append(reached, request.URL.Path)
		if request.Header.Get("Authorization") != "" {
			sawAuthorization = true
		}
		response.Header().Set("Content-Type", "text/plain")
		if request.URL.Path == "/ready" {
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(response, "database unavailable")
			return
		}
		_, _ = io.WriteString(response, "ok")
	}))
	t.Cleanup(server.Close)

	output, err := executeCLI(t, server.URL, true, "health")
	if err != nil {
		t.Fatalf("health without a credential: %v", err)
	}
	if !strings.Contains(output, `"ready":true`) || !strings.Contains(output, `"status":200`) {
		t.Errorf("health output = %s", output)
	}

	output, err = executeCLI(t, server.URL, true, "ready")
	if err == nil {
		t.Fatal("a refused readiness probe must exit non-zero")
	}
	if code := cli.GetExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(output, `"ready":false`) || !strings.Contains(output, "database unavailable") {
		t.Errorf("readiness output = %s", output)
	}

	if sawAuthorization {
		t.Error("a probe must not send a credential")
	}
	if len(reached) != 2 || reached[0] != "/health" || reached[1] != "/ready" {
		t.Errorf("paths reached = %v", reached)
	}
}
