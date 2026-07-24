package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lookupInput struct {
	ID string `json:"id"`
}

type lookupOutput struct {
	Value string `json:"value"`
}

func TestClientDiscoversAndCallsWithEphemeralHeaders(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "scripted",
		Version: "1.0.0",
	}, nil)
	notDestructive := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lookup",
		Description: "Look up one value",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &notDestructive,
		},
	}, func(
		_ context.Context,
		_ *mcp.CallToolRequest,
		input lookupInput,
	) (*mcp.CallToolResult, lookupOutput, error) {
		return nil, lookupOutput{Value: "value-" + input.ID}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	)
	authenticated := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer mcp-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(writer, request)
	})
	httpServer := httptest.NewTLSServer(authenticated)
	defer httpServer.Close()

	client := New(Config{HTTPClient: httpServer.Client()})
	connection := domain.MCPServerConnection{
		Name: "scripted",
		URL:  httpServer.URL,
		Headers: map[string]string{
			"Authorization": "Bearer mcp-secret",
		},
	}
	tools, err := client.Discover(context.Background(), connection)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "lookup" ||
		!tools[0].ReadOnly || !tools[0].Idempotent ||
		tools[0].Destructive == nil || *tools[0].Destructive {
		t.Fatalf("tools = %#v", tools)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("input schema = %s, error = %v", tools[0].InputSchema, err)
	}

	result, err := client.Call(
		context.Background(),
		connection,
		"lookup",
		json.RawMessage(`{"id":"42"}`),
	)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError || string(result.StructuredContent) != `{"value":"value-42"}` {
		t.Fatalf("call result = %#v", result)
	}
	var content []string
	if err := json.Unmarshal(result.Content, &content); err != nil || len(content) != 1 {
		t.Fatalf("content = %s, error = %v", result.Content, err)
	}
}
