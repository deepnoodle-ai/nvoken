package nvoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSharedOutputSchemaPreflightFixtures(t *testing.T) {
	fixture := loadOutputSchemaFixture(t)
	for _, test := range fixture.Accepted {
		t.Run("accept/"+test.ID, func(t *testing.T) {
			schema := expandOutputSchemaFixture(t, test)
			if err := PreflightOutputSchema(schema); err != nil {
				t.Fatalf("preflight schema: %v", err)
			}
		})
	}
	for _, test := range fixture.Rejected {
		t.Run("reject/"+test.ID, func(t *testing.T) {
			schema := expandOutputSchemaFixture(t, test)
			err := PreflightOutputSchema(schema)
			var sdkError *Error
			if !errors.As(err, &sdkError) {
				t.Fatalf("preflight error = %v, want *Error", err)
			}
			if sdkError.Category != ErrorValidation ||
				sdkError.Code != SchemaPreflightCode ||
				sdkError.Details["kind"] != "output_schema" ||
				sdkError.Details["code"] != test.Issue.Code ||
				sdkError.Details["path"] != test.Issue.Path {
				t.Fatalf("preflight error = %#v, want %#v", sdkError, test.Issue)
			}
			if keyword, _ := sdkError.Details["keyword"].(string); keyword != test.Issue.Keyword {
				t.Fatalf("keyword = %q, want %q", keyword, test.Issue.Keyword)
			}
		})
	}
}

func TestCreateAgentDefinitionPreflightsOutputSchemaBeforeTransport(t *testing.T) {
	var attempts atomic.Int64
	client, err := NewClient(
		"https://runtime.example.test",
		"test-key",
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts.Add(1)
			return nil, errors.New("transport must not be called")
		})}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	for _, test := range loadOutputSchemaFixture(t).Rejected {
		_, err = client.CreateAgentDefinition(context.Background(), AgentDefinition{
			DefinitionKey: "schema-preflight",
			Name:          "Schema preflight",
			Model: Model{
				Provider: "anthropic",
				ID:       "test-model",
			},
			OutputSchema: expandOutputSchemaFixture(t, test),
		}, CreateAgentDefinitionOptions{})
		var sdkError *Error
		if !errors.As(err, &sdkError) ||
			sdkError.Code != SchemaPreflightCode {
			t.Fatalf("%s: invoke error = %#v", test.ID, sdkError)
		}
	}
	if attempts.Load() != 0 {
		t.Fatalf("transport attempts = %d", attempts.Load())
	}
}

func TestAgentDefinitionOutputSchemaEncodesDirectly(t *testing.T) {
	scope := MemoryConfigScopeUser
	mode := MemoryContextModeFull
	body, err := json.Marshal(AgentDefinition{
		Model:        Model{Provider: "anthropic", ID: "test-model"},
		Memory:       &MemoryConfig{Scope: &scope, Context: &MemoryContextConfig{Mode: &mode}},
		OutputSchema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var wire struct {
		OutputSchema     map[string]any  `json:"output_schema"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Memory           *MemoryConfig   `json:"memory"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode wire body: %v", err)
	}
	if wire.StructuredOutput != nil {
		t.Fatalf("structured_output = %s, want absent", wire.StructuredOutput)
	}
	if wire.OutputSchema["type"] != "object" {
		t.Fatalf(
			"output_schema = %#v, want the supplied OutputSchema",
			wire.OutputSchema,
		)
	}
	if wire.Memory == nil || wire.Memory.Scope == nil || *wire.Memory.Scope != MemoryConfigScopeUser ||
		wire.Memory.Context == nil || wire.Memory.Context.Mode == nil || *wire.Memory.Context.Mode != MemoryContextModeFull {
		t.Fatalf("memory = %#v, want user-scoped full context", wire.Memory)
	}
}

type outputSchemaFixture struct {
	Accepted []outputSchemaFixtureCase `json:"accepted"`
	Rejected []outputSchemaFixtureCase `json:"rejected"`
}

type outputSchemaFixtureCase struct {
	ID       string                       `json:"id"`
	Schema   map[string]any               `json:"schema"`
	Repeat   *outputSchemaFixtureRepeat   `json:"repeat"`
	Generate *outputSchemaFixtureGenerate `json:"generate"`
	Issue    outputSchemaFixtureIssue     `json:"issue"`
}

type outputSchemaFixtureRepeat struct {
	Path      string `json:"path"`
	Character string `json:"character"`
	Count     int    `json:"count"`
}

type outputSchemaFixtureGenerate struct {
	Kind  string `json:"kind"`
	Depth int    `json:"depth"`
}

type outputSchemaFixtureIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Keyword string `json:"keyword"`
}

func loadOutputSchemaFixture(t *testing.T) outputSchemaFixture {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(
		"..",
		"conformance",
		"fixtures",
		"structured-output-schema-v1.json",
	))
	if err != nil {
		t.Fatalf("read schema fixture: %v", err)
	}
	var fixture outputSchemaFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatalf("decode schema fixture: %v", err)
	}
	return fixture
}

func expandOutputSchemaFixture(
	t *testing.T,
	test outputSchemaFixtureCase,
) map[string]any {
	t.Helper()
	if test.Generate != nil {
		if test.Generate.Kind != "nested-object" {
			t.Fatalf("unknown schema generator %q", test.Generate.Kind)
		}
		var node any = map[string]any{
			"type": "string",
		}
		for depth := 1; depth < test.Generate.Depth; depth++ {
			node = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"child": node,
				},
				"required": []any{"child"},
			}
		}
		return node.(map[string]any)
	}
	schema := test.Schema
	if test.Repeat != nil {
		setOutputSchemaFixturePointer(
			t,
			schema,
			test.Repeat.Path,
			strings.Repeat(test.Repeat.Character, test.Repeat.Count),
		)
	}
	return schema
}

func setOutputSchemaFixturePointer(
	t *testing.T,
	schema map[string]any,
	path string,
	value any,
) {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	current := schema
	for _, encoded := range parts[:len(parts)-1] {
		member := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		child, ok := current[member].(map[string]any)
		if !ok {
			t.Fatalf("fixture pointer %q does not resolve", path)
		}
		current = child
	}
	member := parts[len(parts)-1]
	member = strings.ReplaceAll(strings.ReplaceAll(member, "~1", "/"), "~0", "~")
	current[member] = value
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// A replacement replaces the whole resource, so a writable field this SDK
// forgets to carry from a read into the next write is data loss. The fixture
// populates every one of them, and the assertion is that the body sent back is
// the resource it was read from, minus the read-only fields and the immutable
// key, with exactly one field changed.
func TestAgentDefinitionRoundTripKeepsEveryWritableField(t *testing.T) {
	resource := completeAgentDefinitionResource()
	var written map[string]any
	var ifMatch string
	client, err := NewClient(
		"https://runtime.example.test",
		"test-key",
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodPut {
				ifMatch = request.Header.Get("If-Match")
				body, readErr := io.ReadAll(request.Body)
				if readErr != nil {
					return nil, readErr
				}
				if unmarshalErr := json.Unmarshal(body, &written); unmarshalErr != nil {
					return nil, unmarshalErr
				}
			}
			payload, marshalErr := json.Marshal(resource)
			if marshalErr != nil {
				return nil, marshalErr
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(payload)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		})}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	current, err := client.GetAgentDefinition(context.Background(), "def_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	definition, err := AgentDefinitionFromResource(current)
	if err != nil {
		t.Fatalf("from resource: %v", err)
	}
	definition.Instructions = "Be concise and warm."
	if _, err := client.UpdateAgentDefinition(
		context.Background(),
		current.ID,
		definition,
		UpdateAgentDefinitionOptions{ExpectedRevision: current.Revision},
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	if ifMatch != `"4"` {
		t.Fatalf("If-Match = %q", ifMatch)
	}
	expected := map[string]any{}
	encoded, err := json.Marshal(resource)
	if err != nil {
		t.Fatalf("encode resource: %v", err)
	}
	if err := json.Unmarshal(encoded, &expected); err != nil {
		t.Fatalf("decode resource: %v", err)
	}
	for _, readOnly := range []string{
		"id", "revision", "definition_key", "created_at", "updated_at", "archived_at",
	} {
		delete(expected, readOnly)
	}
	expected["instructions"] = "Be concise and warm."
	if !reflect.DeepEqual(written, expected) {
		t.Fatalf("replacement body\n got %#v\nwant %#v", written, expected)
	}
}

func TestCreateAgentDefinitionSendsTheFlatDefinition(t *testing.T) {
	var written map[string]any
	var idempotencyKey string
	client, err := NewClient(
		"https://runtime.example.test",
		"test-key",
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			idempotencyKey = request.Header.Get("Idempotency-Key")
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				return nil, readErr
			}
			if unmarshalErr := json.Unmarshal(body, &written); unmarshalErr != nil {
				return nil, unmarshalErr
			}
			payload, marshalErr := json.Marshal(completeAgentDefinitionResource())
			if marshalErr != nil {
				return nil, marshalErr
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader(payload)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		})}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.CreateAgentDefinition(context.Background(), AgentDefinition{
		DefinitionKey: "support",
		Name:          "Billing support",
		Instructions:  "Be brief.",
		Model:         Model{Provider: "anthropic", ID: "claude-sonnet-5"},
		ClientInterface: &ClientInterface{
			ContextNames: []string{"cart"},
			ToolNames:    []string{},
		},
	}, CreateAgentDefinitionOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Nothing is invented: a key the SDK made up would be new on every attempt.
	if idempotencyKey != "" {
		t.Fatalf("Idempotency-Key = %q", idempotencyKey)
	}
	expected := map[string]any{
		"definition_key":   "support",
		"name":             "Billing support",
		"instructions":     "Be brief.",
		"model":            map[string]any{"provider": "anthropic", "id": "claude-sonnet-5"},
		"client_interface": map[string]any{"context_names": []any{"cart"}},
	}
	if !reflect.DeepEqual(written, expected) {
		t.Fatalf("create body\n got %#v\nwant %#v", written, expected)
	}
}

func TestAgentDefinitionToolChoiceNamesAToolOnlyInNamedMode(t *testing.T) {
	base := AgentDefinition{
		DefinitionKey: "support",
		Model:         Model{Provider: "anthropic", ID: "claude-sonnet-5"},
	}
	for _, test := range []struct {
		name   string
		choice ToolChoice
	}{
		{"named without a name", ToolChoice{Mode: ToolChoiceNamed}},
		{"auto with a name", ToolChoice{Mode: ToolChoiceAuto, Name: "x"}},
	} {
		definition := base
		definition.ToolChoice = &test.choice
		if _, err := definition.encoded(true); err == nil {
			t.Fatalf("%s: expected a validation error", test.name)
		}
	}
	definition := base
	definition.ToolChoice = &ToolChoice{Mode: ToolChoiceNamed, Name: "x"}
	if _, err := definition.encoded(true); err != nil {
		t.Fatalf("named with a name: %v", err)
	}
}

func completeAgentDefinitionResource() *AgentDefinitionResource {
	var resource AgentDefinitionResource
	if err := json.Unmarshal([]byte(`{
		"id": "def_1",
		"definition_key": "support",
		"name": "Billing support",
		"revision": 4,
		"instructions": "Be brief.",
		"model": {"provider": "anthropic", "id": "claude-sonnet-5"},
		"sampling": {"temperature": 0.4},
		"reasoning": {"effort": "high", "budget_tokens": 2048},
		"tool_choice": {"mode": "named", "name": "lookup_invoice"},
		"limits": {"max_iterations": 6, "max_output_tokens": 1024},
		"output_schema": {"type": "object", "properties": {"answer": {"type": "string"}}},
		"tools": [
			{"mode": "builtin", "name": "nvoken_fetch"},
			{
				"mode": "host",
				"name": "lookup_invoice",
				"description": "Look up an invoice.",
				"input_schema": {"type": "object", "properties": {"id": {"type": "string"}}}
			},
			{
				"mode": "callback",
				"name": "refund",
				"description": "Issue a refund.",
				"input_schema": {"type": "object", "properties": {"id": {"type": "string"}}},
				"callback": {"url": "https://tools.example.test/refund"}
			}
		],
		"mcp_servers": [{
			"name": "billing",
			"url": "https://mcp.example.test/billing",
			"transport": "streamable_http",
			"allowed_tools": ["search"],
			"timeouts": {"discovery_seconds": 5, "call_seconds": 30}
		}],
		"provider_tools": [{
			"type": "web_search",
			"web_search": {"max_uses": 3, "allowed_domains": ["example.test"]}
		}],
		"memory": {"scope": "user", "context": {"mode": "index", "max_bytes": 1536}},
		"client_interface": {"context_names": ["cart"], "tool_names": ["lookup_invoice"]},
		"created_at": "2026-07-21T12:00:00Z",
		"updated_at": "2026-07-21T12:00:00Z",
		"archived_at": null
	}`), &resource); err != nil {
		panic(err)
	}
	return &resource
}
