package nvoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

// syncRequest is what one leg of a sync sent, which is the whole point of the
// loop: it writes and never reads.
type syncRequest struct {
	Method  string
	Path    string
	IfMatch string
	Body    map[string]any
}

// recordingSyncClient answers each write from respond and records what it was
// asked, so a test can assert both the outcomes and that nothing was read.
func recordingSyncClient(
	t *testing.T,
	requests *[]syncRequest,
	respond func(request syncRequest) (int, any),
) *Client {
	t.Helper()
	client, err := NewClient(
		"https://runtime.example.test",
		"test-key",
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			recorded := syncRequest{
				Method:  request.Method,
				Path:    request.URL.Path,
				IfMatch: request.Header.Get("If-Match"),
			}
			if err := json.Unmarshal(raw, &recorded.Body); err != nil {
				return nil, err
			}
			*requests = append(*requests, recorded)
			status, body := respond(recorded)
			payload, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(bytes.NewReader(payload)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		})}),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func definitionKeyConflict(definitionID string) map[string]any {
	return map[string]any{
		"code":    "agent_definition_key_conflict",
		"message": "definition_key is held by a different definition",
		"details": map[string]any{"definition_id": definitionID, "definition_key": "changed"},
	}
}

func syncedDefinition(definitionKey string, revision int) map[string]any {
	return map[string]any{
		"id":             "def_" + definitionKey,
		"definition_key": definitionKey,
		"name":           definitionKey,
		"revision":       revision,
		"model":          map[string]any{"provider": "anthropic", "id": "claude-sonnet-5"},
		"created_at":     "2026-08-17T12:00:00Z",
		"updated_at":     "2026-08-17T12:00:00Z",
	}
}

func TestSyncDefinitionsWritesWithoutReadingAndReportsWhatEachWriteDid(t *testing.T) {
	var requests []syncRequest
	client := recordingSyncClient(t, &requests, func(request syncRequest) (int, any) {
		if request.Method == http.MethodPut {
			return http.StatusCreated, syncedDefinition("changed", 7)
		}
		// A key the App has never used, then a restatement of one it holds,
		// then one whose contents differ.
		switch request.Body["definition_key"] {
		case "new":
			return http.StatusCreated, syncedDefinition("new", 1)
		case "same":
			return http.StatusOK, syncedDefinition("same", 3)
		default:
			return http.StatusConflict, definitionKeyConflict("def_changed")
		}
	})

	model := Model{Provider: "anthropic", ID: "claude-sonnet-5"}
	synced, err := client.SyncDefinitions(context.Background(), []AgentDefinition{
		{DefinitionKey: "new", Model: model},
		{DefinitionKey: "same", Model: model},
		{DefinitionKey: "changed", Model: model, Instructions: "Be warm."},
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	want := []DefinitionSync{
		{DefinitionKey: "new", Outcome: DefinitionCreated},
		{DefinitionKey: "same", Outcome: DefinitionUnchanged},
		{DefinitionKey: "changed", Outcome: DefinitionUpdated},
	}
	if len(synced) != len(want) {
		t.Fatalf("synced %d definitions, want %d", len(synced), len(want))
	}
	for i, expected := range want {
		if synced[i].DefinitionKey != expected.DefinitionKey || synced[i].Outcome != expected.Outcome {
			t.Fatalf("synced[%d] = %s/%s, want %s/%s",
				i, synced[i].DefinitionKey, synced[i].Outcome,
				expected.DefinitionKey, expected.Outcome)
		}
		if synced[i].Definition == nil {
			t.Fatalf("synced[%d] carries no definition", i)
		}
	}
	if synced[2].Definition.Revision != 7 {
		t.Fatalf("replaced revision = %d, want 7", synced[2].Definition.Revision)
	}

	// Nothing was read: three creates, and one replacement the conflict
	// addressed.
	wantCalls := []string{
		"POST /v1/agent-definitions",
		"POST /v1/agent-definitions",
		"POST /v1/agent-definitions",
		"PUT /v1/agent-definitions/def_changed",
	}
	if len(requests) != len(wantCalls) {
		t.Fatalf("sent %d requests, want %d: %+v", len(requests), len(wantCalls), requests)
	}
	for i, call := range wantCalls {
		if got := requests[i].Method + " " + requests[i].Path; got != call {
			t.Fatalf("request %d = %q, want %q", i, got, call)
		}
	}
	// `*`, because the conflict proves the resource exists and differs, not
	// which revision it is at. The replacement drops the immutable key.
	if requests[3].IfMatch != "*" {
		t.Fatalf("If-Match = %q, want %q", requests[3].IfMatch, "*")
	}
	if _, present := requests[3].Body["definition_key"]; present {
		t.Fatalf("replacement body carries definition_key: %+v", requests[3].Body)
	}
	if requests[3].Body["instructions"] != "Be warm." {
		t.Fatalf("replacement instructions = %v", requests[3].Body["instructions"])
	}
}

func TestSyncDefinitionsReportsARacedReplacementAsUnchanged(t *testing.T) {
	var requests []syncRequest
	client := recordingSyncClient(t, &requests, func(request syncRequest) (int, any) {
		if request.Method == http.MethodPost {
			return http.StatusConflict, definitionKeyConflict("def_raced")
		}
		// Someone else published these exact contents between the two calls, so
		// the replacement had nothing left to publish.
		return http.StatusOK, syncedDefinition("support", 2)
	})

	synced, err := client.SyncDefinitions(context.Background(), []AgentDefinition{
		{DefinitionKey: "support", Model: Model{Provider: "anthropic", ID: "claude-sonnet-5"}},
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(synced) != 1 || synced[0].Outcome != DefinitionUnchanged {
		t.Fatalf("synced = %+v, want one unchanged", synced)
	}
}

func TestSyncDefinitionsStopsAtTheFirstError(t *testing.T) {
	var requests []syncRequest
	client := recordingSyncClient(t, &requests, func(syncRequest) (int, any) {
		// Restoring an archived key is a decision, not a sync step.
		return http.StatusConflict, map[string]any{
			"code":    "agent_definition_archived",
			"message": "definition_key is held by an archived definition",
			"details": map[string]any{"definition_id": "def_archived"},
		}
	})

	model := Model{Provider: "anthropic", ID: "claude-sonnet-5"}
	_, err := client.SyncDefinitions(context.Background(), []AgentDefinition{
		{DefinitionKey: "gone", Model: model},
		{DefinitionKey: "next", Model: model},
	})
	var sdkError *Error
	if !errors.As(err, &sdkError) || sdkError.Code != "agent_definition_archived" {
		t.Fatalf("sync error = %v, want agent_definition_archived", err)
	}
	if len(requests) != 1 {
		t.Fatalf("sent %d requests after the first error, want 1", len(requests))
	}
}
