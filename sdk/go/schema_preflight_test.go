package nvoken

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestTargetBehaviorPreflightsOutputSchemaBeforeTransport(t *testing.T) {
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
	invalid := OutputSchema{"type": "object", "$ref": "remote.json"}
	behavior := Behavior{OutputSchema: &invalid}

	if _, err := client.Agents().Create(context.Background(), CreateAgentOptions{
		Key:      "schema-preflight",
		Behavior: behavior,
	}); !isSchemaPreflightError(err) {
		t.Fatalf("Agent create error = %v", err)
	}
	agent := &Agent{client: client, value: AgentResource{ID: "agent_1"}}
	if _, err := agent.Publish(context.Background(), behavior, PublishOptions{}); !isSchemaPreflightError(err) {
		t.Fatalf("Agent publish error = %v", err)
	}
	if _, err := client.Inline(behavior).Start(context.Background(), "hello", TurnOptions{TenantKey: "acme"}); !isSchemaPreflightError(err) {
		t.Fatalf("inline start error = %v", err)
	}
	if attempts.Load() != 0 {
		t.Fatalf("transport attempts = %d", attempts.Load())
	}

	valid := map[string]any{"type": "object"}
	if err := PreflightOutputSchema(valid); err != nil {
		t.Fatalf("valid target schema: %v", err)
	}
}

func isSchemaPreflightError(err error) bool {
	var sdkError *Error
	return errors.As(err, &sdkError) && sdkError.Code == SchemaPreflightCode
}
