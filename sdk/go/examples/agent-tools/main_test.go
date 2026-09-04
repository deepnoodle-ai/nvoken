// ABOUTME: Proves the agent-tools example drives a host tool locally and carries its result forward.
// ABOUTME: Runs the example against an in-process fake service so the wire contract is exact and fast.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	nvoken "github.com/deepnoodle-ai/nvoken/sdk/go"
)

const (
	fakeAgentID       = "0198f1c2-0000-7000-8000-000000000a01"
	fakeFirstTurnID   = "0198f1c2-0000-7000-8000-000000000b01"
	fakeSecondTurnID  = "0198f1c2-0000-7000-8000-000000000b02"
	fakeFirstCallID   = "0198f1c2-0000-7000-8000-000000000c01"
	fakeSecondCallID  = "0198f1c2-0000-7000-8000-000000000c02"
	fakeFirstAnswer   = "Order order-42 is shipped and should arrive tomorrow."
	fakeCarriedAnswer = "You told me earlier that it arrives tomorrow."
)

// fakeService is the smallest nvoken that can admit two Turns into one
// Conversation, hand the first Turn's host tool call to the SDK, and answer
// the second Turn from the committed transcript.
type fakeService struct {
	t *testing.T

	mu             sync.Mutex
	agentCreate    map[string]any
	turnAdmissions []map[string]any
	toolResults    []map[string]any
	firstSettled   bool
	secondSettled  bool

	offerToolOnSecondTurn bool
	secondAnswer          string
}

func newFakeService(t *testing.T) *fakeService {
	return &fakeService{t: t, secondAnswer: fakeCarriedAnswer}
}

func (s *fakeService) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/v1/agents":
		s.agentCreate = decodeBody(s.t, request)
		writer.WriteHeader(http.StatusCreated)
		fmt.Fprintf(writer, `{"id":%q,"agent_key":%q,"name":"Order support","owner":{"kind":"tenant","tenant_key":%q},
			"current_revision":1,"created_at":"2026-09-04T12:00:00Z","updated_at":"2026-09-04T12:00:00Z","archived_at":null}`,
			fakeAgentID, s.agentCreate["agent_key"], field(s.t, s.agentCreate, "owner", "tenant_key"))
	case request.Method == http.MethodPost && request.URL.Path == "/v1/turns":
		s.turnAdmissions = append(s.turnAdmissions, decodeBody(s.t, request))
		turnID := fakeFirstTurnID
		if len(s.turnAdmissions) > 1 {
			turnID = fakeSecondTurnID
		}
		writer.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(writer, `{"id":%q,"status":"queued"}`, turnID)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/turns/"+fakeFirstTurnID+"/result":
		if !s.firstSettled {
			writeWaitingForHostTool(writer, fakeFirstTurnID, fakeFirstCallID)
			return
		}
		writeCompleted(writer, fakeFirstTurnID, fakeFirstAnswer)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/turns/"+fakeFirstTurnID+"/tool-results":
		s.toolResults = append(s.toolResults, decodeBody(s.t, request))
		s.firstSettled = true
		writeResultsAccepted(writer, fakeFirstTurnID)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/turns/"+fakeSecondTurnID+"/result":
		if s.offerToolOnSecondTurn && !s.secondSettled {
			writeWaitingForHostTool(writer, fakeSecondTurnID, fakeSecondCallID)
			return
		}
		writeCompleted(writer, fakeSecondTurnID, s.secondAnswer)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/turns/"+fakeSecondTurnID+"/tool-results":
		s.toolResults = append(s.toolResults, decodeBody(s.t, request))
		s.secondSettled = true
		writeResultsAccepted(writer, fakeSecondTurnID)
	default:
		s.t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		http.NotFound(writer, request)
	}
}

func decodeBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s %s: %v", request.Method, request.URL.Path, err)
	}
	return body
}

func writeWaitingForHostTool(writer http.ResponseWriter, turnID, callID string) {
	fmt.Fprintf(writer, `{"turn":{"id":%q,"status":"waiting","ended_at":null,
		"tool_calls":[{"id":%q,"name":"lookup_order","mode":"host","status":"pending",
		"arguments":{"orderId":"order-42"},"updated_at":"2026-09-04T12:00:00Z"}]},
		"messages":[],"output_text":null}`, turnID, callID)
}

func writeCompleted(writer http.ResponseWriter, turnID, text string) {
	fmt.Fprintf(writer, `{"turn":{"id":%q,"status":"completed","ended_at":"2026-09-04T12:00:01Z"},
		"messages":[],"output_text":%q}`, turnID, text)
}

func writeResultsAccepted(writer http.ResponseWriter, turnID string) {
	writer.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(writer, `{"content_expires_at":null,"conversation_id":null,"results":[],"status":"queued","tool_calls":[],"turn_id":%q}`, turnID)
}

// field walks nested JSON objects and fails the test when a step is missing.
func field(t *testing.T, value any, path ...string) any {
	t.Helper()
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("expected object at %q, got %#v", key, value)
		}
		value, ok = object[key]
		if !ok {
			t.Fatalf("missing field %q in %#v", key, object)
		}
	}
	return value
}

func runExample(t *testing.T, service *fakeService) (string, error) {
	t.Helper()
	server := httptest.NewServer(service)
	t.Cleanup(server.Close)
	client, err := nvoken.NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	var out bytes.Buffer
	err = run(context.Background(), runOptions{
		client: client,
		model:  "anthropic/claude-sonnet-5",
		runID:  "run-1",
		out:    &out,
	})
	return out.String(), err
}

func TestRunPublishesTheHostToolContractOnTheAgent(t *testing.T) {
	service := newFakeService(t)
	if _, err := runExample(t, service); err != nil {
		t.Fatalf("run: %v", err)
	}
	create := service.agentCreate
	if got := field(t, create, "owner", "kind"); got != "tenant" {
		t.Fatalf("owner kind = %#v", got)
	}
	if got := field(t, create, "owner", "tenant_key"); got != "agent-tools-run-1" {
		t.Fatalf("owner tenant = %#v", got)
	}
	if got := field(t, create, "model"); got != "anthropic/claude-sonnet-5" {
		t.Fatalf("model = %#v", got)
	}
	tools, ok := create["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", create["tools"])
	}
	if got := field(t, tools[0], "mode"); got != "host" {
		t.Fatalf("tool mode = %#v", got)
	}
	if got := field(t, tools[0], "name"); got != "lookup_order" {
		t.Fatalf("tool name = %#v", got)
	}
	if got := field(t, tools[0], "input_schema", "required"); fmt.Sprint(got) != "[orderId]" {
		t.Fatalf("tool required = %#v", got)
	}
}

func TestRunAnswersTheHostToolCallFromThisProcess(t *testing.T) {
	service := newFakeService(t)
	out, err := runExample(t, service)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.toolResults) != 1 {
		t.Fatalf("tool result submissions = %d, want 1", len(service.toolResults))
	}
	results, ok := service.toolResults[0]["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %#v", service.toolResults[0]["results"])
	}
	if got := field(t, results[0], "tool_call_id"); got != fakeFirstCallID {
		t.Fatalf("tool_call_id = %#v", got)
	}
	if got := field(t, results[0], "content", "orderId"); got != "order-42" {
		t.Fatalf("content.orderId = %#v", got)
	}
	if got := field(t, results[0], "content", "state"); got != "shipped" {
		t.Fatalf("content.state = %#v", got)
	}
	if got := field(t, results[0], "content", "idempotencyKey"); got != fakeFirstCallID {
		t.Fatalf("content.idempotencyKey = %#v, want the ToolCall ID", got)
	}
	if !strings.Contains(out, fakeFirstCallID) || !strings.Contains(out, fakeFirstTurnID) {
		t.Fatalf("output should name the ToolCall and Turn the handler served, got:\n%s", out)
	}
}

func TestRunSendsBothTurnsThroughOneConversation(t *testing.T) {
	service := newFakeService(t)
	out, err := runExample(t, service)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.turnAdmissions) != 2 {
		t.Fatalf("turn admissions = %d, want 2", len(service.turnAdmissions))
	}
	for index, admission := range service.turnAdmissions {
		if got := field(t, admission, "tenant_key"); got != "agent-tools-run-1" {
			t.Fatalf("turn %d tenant_key = %#v", index+1, got)
		}
		if got := field(t, admission, "conversation", "mode"); got != "continue_or_create" {
			t.Fatalf("turn %d conversation mode = %#v", index+1, got)
		}
		if got := field(t, admission, "conversation", "conversation_key"); got != "order-chat-run-1" {
			t.Fatalf("turn %d conversation_key = %#v", index+1, got)
		}
		if got := field(t, admission, "conversation", "owner", "kind"); got != "tenant" {
			t.Fatalf("turn %d conversation owner = %#v", index+1, got)
		}
		if got := field(t, admission, "behavior", "agent", "agent_id"); got != fakeAgentID {
			t.Fatalf("turn %d agent_id = %#v", index+1, got)
		}
	}
	if !strings.Contains(out, fakeFirstAnswer) || !strings.Contains(out, fakeCarriedAnswer) {
		t.Fatalf("output should print both answers, got:\n%s", out)
	}
}

func TestRunFailsWhenTheSecondTurnRunsTheHandlerAgain(t *testing.T) {
	service := newFakeService(t)
	service.offerToolOnSecondTurn = true
	_, err := runExample(t, service)
	if err == nil || !strings.Contains(err.Error(), "handler ran 2 times") {
		t.Fatalf("expected a handler count failure, got %v", err)
	}
}

func TestRunFailsWhenTheSecondAnswerDoesNotCarryTheResultForward(t *testing.T) {
	service := newFakeService(t)
	service.secondAnswer = "I am not sure when it arrives."
	_, err := runExample(t, service)
	if err == nil || !strings.Contains(err.Error(), "tomorrow") {
		t.Fatalf("expected a carry-forward failure, got %v", err)
	}
}
