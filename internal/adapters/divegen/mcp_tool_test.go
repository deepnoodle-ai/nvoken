package divegen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/dive"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

type recordingMCPToolCoordinator struct {
	events          *[]string
	start           domain.MCPToolCallStart
	acceptedContent json.RawMessage
	acceptedError   bool
}

func (c *recordingMCPToolCoordinator) StartMCPToolCall(
	context.Context,
	domain.InvocationClaim,
	int,
	string,
	bool,
) (domain.MCPToolCallStart, error) {
	*c.events = append(*c.events, "start")
	return c.start, nil
}

func (c *recordingMCPToolCoordinator) AcceptMCPToolResult(
	_ context.Context,
	_ domain.InvocationClaim,
	_ domain.ToolCallExecution,
	content json.RawMessage,
	isError bool,
) (domain.ToolCall, error) {
	*c.events = append(*c.events, "accept")
	c.acceptedContent = append(json.RawMessage(nil), content...)
	c.acceptedError = isError
	return domain.ToolCall{ID: "tool-call"}, nil
}

type recordingMCPToolCredentials struct {
	events *[]string
}

func (c recordingMCPToolCredentials) ResolveMCPServerHeaders(
	context.Context,
	string,
	string,
) (map[string]string, error) {
	*c.events = append(*c.events, "resolve")
	return map[string]string{"Authorization": "Bearer secret"}, nil
}

type recordingMCPToolClient struct {
	events     *[]string
	connection domain.MCPServerConnection
	remoteName string
	input      json.RawMessage
	result     domain.MCPCallResult
	err        error
}

func (*recordingMCPToolClient) Discover(
	context.Context,
	domain.MCPServerConnection,
) ([]domain.MCPRemoteTool, error) {
	return nil, errors.New("not used")
}

func (c *recordingMCPToolClient) Call(
	_ context.Context,
	connection domain.MCPServerConnection,
	remoteName string,
	input json.RawMessage,
) (domain.MCPCallResult, error) {
	*c.events = append(*c.events, "call")
	c.connection = connection
	c.remoteName = remoteName
	c.input = append(json.RawMessage(nil), input...)
	return c.result, c.err
}

func TestMCPToolFencesBeforeEgressAndAcceptsBoundedResult(t *testing.T) {
	events := []string{}
	execution := domain.ToolCallExecution{
		Call: domain.ToolCall{ID: "tool-call"},
		Attempt: domain.ToolCallAttempt{
			Attempt: 1,
		},
	}
	coordinator := &recordingMCPToolCoordinator{
		events: &events,
		start:  domain.MCPToolCallStart{Execution: &execution},
	}
	client := &recordingMCPToolClient{
		events: &events,
		result: domain.MCPCallResult{
			Content:           json.RawMessage(`["found"]`),
			StructuredContent: json.RawMessage(`{"count":1}`),
		},
	}
	state := &generationCheckpointState{}
	state.iteration.Store(1)
	tool := newMCPTool(
		client,
		recordingMCPToolCredentials{events: &events},
		coordinator,
		domain.InvocationClaim{Invocation: domain.Invocation{ID: "invocation"}, Attempt: 2},
		state,
		domain.MCPToolDefinition{
			ServerName:  "calendar",
			URL:         "https://calendar.example.test/mcp",
			RemoteName:  "lookup",
			Name:        "calendar__lookup",
			Description: "Lookup",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			CallTimeout: time.Second,
		},
		&dive.Schema{Type: dive.Object},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	result, err := tool.Call(
		dive.WithToolCallID(context.Background(), "provider-call"),
		json.RawMessage(`{"id":"one"}`),
	)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("Call = %#v, %v", result, err)
	}
	if strings.Join(events, ",") != "start,resolve,call,accept" {
		t.Fatalf("event order = %v", events)
	}
	if client.connection.Headers["Authorization"] != "Bearer secret" ||
		client.remoteName != "lookup" ||
		string(client.input) != `{"id":"one"}` {
		t.Fatalf("MCP call = %#v, %q, %s", client.connection, client.remoteName, client.input)
	}
	if coordinator.acceptedError ||
		string(coordinator.acceptedContent) != `{"content":["found"],"structured_content":{"count":1}}` {
		t.Fatalf("accepted = %s, error=%v", coordinator.acceptedContent, coordinator.acceptedError)
	}
}

func TestMCPToolReturnsRecoveredUnknownOutcomeWithoutEgress(t *testing.T) {
	events := []string{}
	content := json.RawMessage(`"The outcome is unknown."`)
	coordinator := &recordingMCPToolCoordinator{
		events: &events,
		start: domain.MCPToolCallStart{
			RecoveredContent: content,
			RecoveredIsError: true,
		},
	}
	client := &recordingMCPToolClient{events: &events}
	state := &generationCheckpointState{}
	state.iteration.Store(1)
	tool := newMCPTool(
		client,
		recordingMCPToolCredentials{events: &events},
		coordinator,
		domain.InvocationClaim{Invocation: domain.Invocation{ID: "invocation"}, Attempt: 2},
		state,
		domain.MCPToolDefinition{
			ServerName:  "calendar",
			RemoteName:  "write",
			Name:        "calendar__write",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			CallTimeout: time.Second,
		},
		&dive.Schema{Type: dive.Object},
		nil,
	)
	result, err := tool.Call(
		dive.WithToolCallID(context.Background(), "provider-call"),
		json.RawMessage(`{}`),
	)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("Call = %#v, %v", result, err)
	}
	if strings.Join(events, ",") != "start" || result.Content[0].Text != "The outcome is unknown." {
		t.Fatalf("events = %v, result = %#v", events, result)
	}
}

func TestNormalizeMCPToolResultRejectsOversizeAndDepth(t *testing.T) {
	if _, err := normalizeMCPToolResult(domain.MCPCallResult{
		Content: json.RawMessage(`["` + strings.Repeat("x", maxMCPToolResultBytes) + `"]`),
	}); err == nil {
		t.Fatal("oversized result was accepted")
	}
	deep := strings.Repeat(`{"x":`, maxMCPToolResultDepth+1) + `"value"` +
		strings.Repeat(`}`, maxMCPToolResultDepth+1)
	if _, err := normalizeMCPToolResult(domain.MCPCallResult{
		Content: json.RawMessage(deep),
	}); err == nil {
		t.Fatal("deep result was accepted")
	}
}
