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
	testAgentID      = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
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
		"--if-active", "supersede",
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
		"--if-active", "supersede",
		"--instructions", "help",
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
	if err != nil || output != testAgentID+"\tsupport\n" {
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
		"invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328",
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
	if err != nil || !strings.Contains(output, "transcript.update\tcursor-2") || !strings.Contains(output, "stream.end\tcursor-2") {
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
	if err != nil || !strings.Contains(output, "stream.end\tcursor-2") {
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

// The CLI's invoke flags are flat, but the admitted body nests every execution
// field under agent_definition. Pinning both halves keeps a flag from silently
// landing at the top level, where the Runtime would reject it.
func TestNestedAgentDefinitionAdmissionAndDeltaRendering(t *testing.T) {
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
				"agent_definition_id":"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
				"agent_definition_revision":1,
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
				"id":"invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
				"agent_id":"agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320",
				"session_id":"sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321",
				"agent_definition_id":"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
				"agent_definition":null,
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
		"--agent",
		"support",
		"--idempotency-key",
		"flat-admission-test",
		"--agent-definition-id",
		"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
	)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("flat admission output=%q err=%v", output, err)
	}
	for _, leaked := range []string{"instructions", "model", "limits", "tools"} {
		if admission[leaked] != nil {
			t.Fatalf("execution field %q leaked to the top level: %#v", leaked, admission)
		}
	}
	if admission["agent_definition_id"] != "def_019b0a12-8d51-7f34-aed2-0e07c1bdb330" || admission["agent_definition"] != nil {
		t.Fatalf("admission did not carry only the Agent Definition ID: %#v", admission)
	}

	output, err = executeCLI(
		t,
		server.URL,
		true,
		"invoke",
		"inspect these",
		"--agent",
		"support",
		"--idempotency-key",
		"definition-url-test",
		"--agent-definition-id",
		"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
		"--image-url",
		"https://media.example.test/chart.png",
		"--document-url",
		"https://media.example.test/report.pdf",
	)
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("definition URL admission output=%q err=%v", output, err)
	}
	if admission["agent_definition_id"] != "def_019b0a12-8d51-7f34-aed2-0e07c1bdb330" ||
		admission["agent_definition"] != nil {
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
				"agent_id":"agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320",
				"session_id":"sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321",
				"id":"`+testInvocationID+`",
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

	definition, ok := admission["agent_definition"].(map[string]any)
	if !ok {
		t.Fatalf("probe request carried no inline Agent Definition: %#v", admission)
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
		"--agent",
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
		"--agent",
		"support",
		"--agent-definition-id",
		"def_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
		"--provider",
		"openai",
		"--model",
		"gpt-test",
	)
	if err == nil || called {
		t.Fatalf("mixed definition error = %v, network called = %t", err, called)
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
			var body struct {
				VisitorToken *string `json:"visitor_token"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode anonymous-token request: %v", err)
			} else if body.VisitorToken == nil || *body.VisitorToken != "visitor-old" {
				t.Errorf("visitor_token = %#v", body.VisitorToken)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"access_token":"access-new","access_token_expires_at":"2026-08-12T12:15:00Z","visitor_token":"visitor-new","visitor_token_expires_at":"2027-08-12T12:00:00Z","session_id":null}`))
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
	if _, err = executeCLI(t, server.URL, false, "memory", "delete", testMemoryID); err != nil {
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
		if _, err := executeCLI(t, server.URL, false, arguments...); err != nil {
			t.Fatalf("%v: %v", arguments, err)
		}
	}
	if requests != len(commands) {
		t.Fatalf("requests = %d, want %d", requests, len(commands))
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
