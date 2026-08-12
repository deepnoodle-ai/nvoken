package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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
		_, err = client.CreateAgentDefinition(context.Background(), CreateAgentDefinitionInput{
			IdempotencyKey: "schema-preflight",
			Definition: AgentDefinition{
				Model: Model{
					Provider: "anthropic",
					ID:       "test-model",
				},
				OutputSchema: expandOutputSchemaFixture(t, test),
			},
		})
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
