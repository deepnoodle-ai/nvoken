package nvoken

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestAgentLookupAndStartKeepOwnerActorAndContinuitySeparate(t *testing.T) {
	var admitted map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/agents":
			if request.URL.Query().Get("owner_kind") != "app" || request.URL.Query().Get("agent_key") != "analyst" {
				t.Fatalf("lookup query = %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[{"id":"47fc63e5-ae78-727c-ab52-a2872fe8728f","agent_key":"analyst","name":"Analyst","owner":{"kind":"app"},"current_revision":3,"archived_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"has_more":false,"next_cursor":null}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/turns":
			if err := json.NewDecoder(request.Body).Decode(&admitted); err != nil {
				t.Fatal(err)
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"476dd7be-97a1-78f3-8096-d7032468a80a","status":"queued","tenant_key":"acme","user_key":"alice","attempt":0,"active_execution_ms":0,"conversation_id":null,"memory_space_id":null,"content_expires_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","deadline_at":null,"ended_at":null,"error":null,"stop_reason":null,"structured_output":null,"tool_calls":[]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := client.Agent(context.Background(), "analyst")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := agent.Start(context.Background(), "Compare these listings", TurnOptions{
		TenantKey:    "acme",
		UserKey:      "alice",
		Memory:       TenantMemory("shared"),
		Conversation: ContinueConversation("18325d9f-b9bc-797d-9259-96ece372defd"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.ID() != "476dd7be-97a1-78f3-8096-d7032468a80a" || turn.IdempotencyKey() == "" {
		t.Fatalf("Turn = %#v", turn)
	}
	if admitted["tenant_key"] != "acme" || admitted["user_key"] != "alice" {
		t.Fatalf("actor = %#v", admitted)
	}
	behavior := admitted["behavior"].(map[string]any)
	if behavior["kind"] != "agent" {
		t.Fatalf("behavior = %#v", behavior)
	}
	conversation := admitted["conversation"].(map[string]any)
	if conversation["conversation_id"] != "18325d9f-b9bc-797d-9259-96ece372defd" {
		t.Fatalf("conversation = %#v", conversation)
	}
	memory := admitted["memory"].(map[string]any)
	if memory["scope"] != "tenant" || memory["namespace"] != "shared" {
		t.Fatalf("memory = %#v", memory)
	}
}

func TestAgentListReturnsRunnableHandles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/agents" {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("include_archived") != "true" {
			t.Fatalf("list query = %s", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"items":[{"id":"47fc63e5-ae78-727c-ab52-a2872fe8728f","agent_key":"analyst","name":"Analyst","owner":{"kind":"app"},"current_revision":3,"archived_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"has_more":true,"next_cursor":"next"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Agents().List(context.Background(), ListAgentsOptions{Archived: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Resource().ID != "47fc63e5-ae78-727c-ab52-a2872fe8728f" || !page.HasMore || page.NextCursor == nil || *page.NextCursor != "next" {
		t.Fatalf("page = %#v", page)
	}
}

func TestBehaviorExposesOnlyPortableFields(t *testing.T) {
	typeOfBehavior := reflect.TypeOf(Behavior{})
	want := []string{"Instructions", "Model", "Tools", "Limits", "OutputSchema", "Memory"}
	if typeOfBehavior.NumField() != len(want) {
		t.Fatalf("Behavior fields = %d, want %d", typeOfBehavior.NumField(), len(want))
	}
	for index, name := range want {
		if field := typeOfBehavior.Field(index); field.Name != name {
			t.Fatalf("Behavior field %d = %s, want %s", index, field.Name, name)
		}
	}
}

func TestReducerSettlesOnTargetTurnChange(t *testing.T) {
	reducer := NewReducer()
	event := StreamEvent{ID: "cursor-1", Type: "transcript.update", Data: json.RawMessage(`{
		"type":"transcript.update","cursor":"cursor-1","has_more":false,
		"messages":[],"turn_changes":[{"turn_id":"476dd7be-97a1-78f3-8096-d7032468a80a","revision":2,"status":"completed","terminal":true,"current":true,"conversation_id":null,"content_expires_at":null,"through_message_sequence":null,"occurred_at":"2026-01-01T00:00:00Z","error":null,"structured_output":null}]
	}`)}
	if err := reducer.Apply(event); err != nil {
		t.Fatal(err)
	}
	if !reducer.Settled("476dd7be-97a1-78f3-8096-d7032468a80a") {
		t.Fatal("terminal Turn was not settled")
	}
	snapshot := reducer.Snapshot()
	if snapshot.Cursor != "cursor-1" || len(snapshot.TurnChanges) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestConversationBindsExecutionContextAndSharesClientLock(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{client: client, value: AgentResource{ID: "47fc63e5-ae78-727c-ab52-a2872fe8728f"}}
	maxIterations := 8
	selection := *ContinueOrCreateConversation("case-42", UserConversation("alice"))
	options := ConversationOptions{
		TenantKey: "acme",
		UserKey:   "alice",
		Selection: selection,
		Memory:    UserMemory("analyst"),
		Limits:    &Limits{MaxIterations: &maxIterations},
	}
	first := agent.Conversation(options)
	second := agent.Conversation(options)
	if first.mu != second.mu {
		t.Fatal("Conversation wrappers for the same effective identity do not share a Client lock")
	}

	narrower := 4
	turnOptions, err := first.turnOptions([]ConversationTurnOptions{
		{IdempotencyKey: "retry-1", Limits: &Limits{MaxIterations: &narrower}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if turnOptions.TenantKey != "acme" || turnOptions.UserKey != "alice" || turnOptions.Memory.Scope != "user" {
		t.Fatalf("bound context = %#v", turnOptions)
	}
	if turnOptions.Limits == nil || turnOptions.Limits.MaxIterations == nil || *turnOptions.Limits.MaxIterations != 4 {
		t.Fatalf("narrowed limits = %#v", turnOptions.Limits)
	}

	wider := 9
	_, err = first.turnOptions([]ConversationTurnOptions{{Limits: &Limits{MaxIterations: &wider}}})
	var sdkError *Error
	if !errors.As(err, &sdkError) || sdkError.Category != ErrorValidation {
		t.Fatalf("widening error = %v", err)
	}
}

func TestConversationDeepCopiesBoundContext(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{client: client, value: AgentResource{ID: "47fc63e5-ae78-727c-ab52-a2872fe8728f"}}
	maxIterations := 8
	metadata := map[string]any{"nested": map[string]any{"value": "original"}}
	selection := ContinueOrCreateConversation("case-42", TenantConversation())
	selection.Metadata = metadata
	memory := TenantMemory("shared")
	conversation := agent.Conversation(ConversationOptions{
		TenantKey: "acme",
		Selection: *selection,
		Memory:    memory,
		Limits:    &Limits{MaxIterations: &maxIterations},
	})

	selection.Key = "mutated"
	metadata["nested"].(map[string]any)["value"] = "mutated"
	memory.Namespace = "mutated"
	maxIterations = 99

	first, err := conversation.turnOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Conversation.Key != "case-42" || first.Memory.Namespace != "shared" || *first.Limits.MaxIterations != 8 {
		t.Fatalf("bound context changed through caller values: %#v", first)
	}
	nested := first.Conversation.Metadata["nested"].(map[string]any)
	if nested["value"] != "original" {
		t.Fatalf("bound metadata = %#v", first.Conversation.Metadata)
	}

	first.Memory.Namespace = "changed-after-read"
	*first.Limits.MaxIterations = 1
	first.Conversation.Metadata["nested"].(map[string]any)["value"] = "changed-after-read"
	second, err := conversation.turnOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Memory.Namespace != "shared" || *second.Limits.MaxIterations != 8 || second.Conversation.Metadata["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("returned context aliased binding: %#v", second)
	}
}

func TestAgentOwnerConstructorsDoNotAliasEmptyCoordinates(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []AgentOwner{TenantOwned(""), UserOwned("acme", ""), UserOwned("", "alice")} {
		_, err := client.Agent(context.Background(), "analyst", AgentLookupOptions{OwnedBy: owner})
		var sdkError *Error
		if !errors.As(err, &sdkError) || sdkError.Category != ErrorValidation {
			t.Fatalf("owner %#v error = %v", owner, err)
		}
	}
	_, err = encodeConversationSelection(ContinueOrCreateConversation("case-42", UserConversation("")))
	var sdkError *Error
	if !errors.As(err, &sdkError) || sdkError.Category != ErrorValidation {
		t.Fatalf("empty user Conversation owner error = %v", err)
	}
}

func TestInlineMemoryRequiresNamespaceAndUserMemoryRequiresActor(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	inline := client.Inline(Behavior{})
	_, err = inline.Start(context.Background(), "hello", TurnOptions{
		TenantKey: "acme",
		Memory:    TenantMemory(""),
	})
	var sdkError *Error
	if !errors.As(err, &sdkError) || sdkError.Category != ErrorValidation {
		t.Fatalf("inline namespace error = %v", err)
	}

	agent := &Agent{client: client, value: AgentResource{ID: "47fc63e5-ae78-727c-ab52-a2872fe8728f"}}
	_, err = agent.Start(context.Background(), "hello", TurnOptions{
		TenantKey: "acme",
		Memory:    UserMemory("personal"),
	})
	if !errors.As(err, &sdkError) || sdkError.Category != ErrorValidation {
		t.Fatalf("user memory actor error = %v", err)
	}
}

func TestAgentLifecycleReturnsAndUpdatesResource(t *testing.T) {
	archivedAt := `"2026-08-26T12:00:00Z"`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodDelete && request.URL.Path == "/v1/agents/47fc63e5-ae78-727c-ab52-a2872fe8728f":
			_, _ = writer.Write([]byte(`{"id":"47fc63e5-ae78-727c-ab52-a2872fe8728f","agent_key":"analyst","name":"Analyst","owner":{"kind":"app"},"current_revision":1,"archived_at":` + archivedAt + `,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-08-26T12:00:00Z"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/agents/47fc63e5-ae78-727c-ab52-a2872fe8728f/restore":
			_, _ = writer.Write([]byte(`{"id":"47fc63e5-ae78-727c-ab52-a2872fe8728f","agent_key":"analyst","name":"Analyst","owner":{"kind":"app"},"current_revision":1,"archived_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-08-26T12:00:01Z"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{client: client, value: AgentResource{ID: "47fc63e5-ae78-727c-ab52-a2872fe8728f"}}
	returned, err := agent.Archive(context.Background())
	if err != nil || returned != agent || agent.Resource().ArchivedAt == nil {
		t.Fatalf("archive returned = %#v error = %v resource = %#v", returned, err, agent.Resource())
	}
	returned, err = agent.Restore(context.Background())
	if err != nil || returned != agent || agent.Resource().ArchivedAt != nil {
		t.Fatalf("restore returned = %#v error = %v resource = %#v", returned, err, agent.Resource())
	}
}

func TestTurnExecutionAndTimeoutErrorsRetainRecoveryContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/turns/ca2779a8-1755-7ea1-aed1-9e84834989cd/result" {
			_, _ = writer.Write([]byte(`{"turn":{"id":"ca2779a8-1755-7ea1-aed1-9e84834989cd","status":"failed","ended_at":"2026-08-26T12:00:00Z","error":{"code":"provider_error","message":"model failed"},"tool_calls":[]},"messages":[],"output_text":null}`))
			return
		}
		if request.URL.Path == "/v1/turns/f106972a-8e62-7183-8a65-d0a97c934cf5/result" {
			_, _ = writer.Write([]byte(`{"turn":{"id":"f106972a-8e62-7183-8a65-d0a97c934cf5","status":"running","ended_at":null,"tool_calls":[]},"messages":[],"output_text":null}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Turn("ca2779a8-1755-7ea1-aed1-9e84834989cd", TurnAccess{TenantKey: "acme"}).Result(context.Background())
	var executionError *TurnExecutionError
	if !errors.As(err, &executionError) || executionError.Result == nil || executionError.Result.Resource.ID != "ca2779a8-1755-7ea1-aed1-9e84834989cd" {
		t.Fatalf("execution error = %#v (%v)", executionError, err)
	}

	turn := client.Turn("f106972a-8e62-7183-8a65-d0a97c934cf5", TurnAccess{TenantKey: "acme"})
	turn.idempotencyKey = "idem-1"
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = turn.Result(ctx)
	var timeoutError *TurnTimeoutError
	if !errors.As(err, &timeoutError) || timeoutError.Turn != turn || timeoutError.IdempotencyKey != "idem-1" {
		t.Fatalf("timeout error = %#v (%v)", timeoutError, err)
	}
}

func TestToolHandlerRunsOncePerTurnCallID(t *testing.T) {
	submissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		submissions++
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"content_expires_at":null,"conversation_id":null,"results":[],"status":"queued","tool_calls":[],"turn_id":"476dd7be-97a1-78f3-8096-d7032468a80a"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	handled := 0
	turn := client.Turn("476dd7be-97a1-78f3-8096-d7032468a80a", TurnAccess{TenantKey: "acme"}).BindTools(Tool{
		Name: "lookup",
		Handler: func(_ context.Context, _ any, toolContext TurnToolContext) (any, error) {
			handled++
			if toolContext.TurnID != "476dd7be-97a1-78f3-8096-d7032468a80a" || toolContext.ToolCallID != "9f8fd6b3-9060-783d-b759-45c8ec70e8cb" {
				t.Fatalf("tool context = %#v", toolContext)
			}
			return map[string]any{"ok": true}, nil
		},
	})
	arguments := map[string]any{"id": "42"}
	calls := []ToolCallSummary{{ID: "9f8fd6b3-9060-783d-b759-45c8ec70e8cb", Name: "lookup", Mode: ToolCallModeHost, Arguments: &arguments}}
	if _, err := turn.settleHostTools(context.Background(), calls); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.settleHostTools(context.Background(), calls); err != nil {
		t.Fatal(err)
	}
	if handled != 1 || submissions != 2 {
		t.Fatalf("handled = %d submissions = %d", handled, submissions)
	}
}

func TestUncertainAdmissionRetainsGeneratedIdempotencyKey(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	client, err := NewClient(
		"https://runtime.example.test",
		"secret",
		WithHTTPClient(httpClient),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Inline(Behavior{}).Start(context.Background(), "hello", TurnOptions{
		TenantKey: "acme",
		Memory:    NoneMemory(),
	})
	var timeoutError *TurnTimeoutError
	if !errors.As(err, &timeoutError) || timeoutError.Turn != nil || timeoutError.IdempotencyKey == "" {
		t.Fatalf("admission timeout = %#v (%v)", timeoutError, err)
	}
}

func TestTurnStatusIsPassiveResultSnapshot(t *testing.T) {
	toolSubmissions := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/turns/683649a1-6faa-7fa3-b774-89ecf248cae7/result":
			_, _ = writer.Write([]byte(`{
				"turn":{"id":"683649a1-6faa-7fa3-b774-89ecf248cae7","status":"completed","ended_at":"2026-08-26T12:00:01Z","tool_calls":[]},
				"messages":[],"output_text":"done"
			}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/turns/f106972a-8e62-7183-8a65-d0a97c934cf5/result":
			_, _ = writer.Write([]byte(`{
				"turn":{"id":"f106972a-8e62-7183-8a65-d0a97c934cf5","status":"waiting","ended_at":null,"tool_calls":[{"id":"9f8fd6b3-9060-783d-b759-45c8ec70e8cb","name":"missing","mode":"host","status":"pending","arguments":{},"updated_at":"2026-08-26T12:00:00Z"}]},
				"messages":[],"output_text":null
			}`))
		case request.Method == http.MethodPost:
			toolSubmissions++
			http.Error(writer, "unexpected", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	turn := client.Turn("683649a1-6faa-7fa3-b774-89ecf248cae7", TurnAccess{TenantKey: "acme"})
	snapshot, err := turn.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Resource.Status != TurnCompleted || snapshot.OutputText == nil || *snapshot.OutputText != "done" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if toolSubmissions != 0 {
		t.Fatalf("Status submitted %d tool result requests", toolSubmissions)
	}

	turn = client.Turn("f106972a-8e62-7183-8a65-d0a97c934cf5", TurnAccess{TenantKey: "acme"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err = turn.Result(ctx)
	var sdkError *Error
	if !errors.As(err, &sdkError) || sdkError.Category != ErrorTimeout {
		t.Fatalf("missing handler result error = %v", err)
	}
	if toolSubmissions != 0 {
		t.Fatalf("missing handler submitted %d tool result requests", toolSubmissions)
	}
}

func TestRecoveredTurnRequiresTenantOnFirstOperation(t *testing.T) {
	client, err := NewClient("https://runtime.example.test", "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Turn("476dd7be-97a1-78f3-8096-d7032468a80a", TurnAccess{}).Status(context.Background())
	var sdkError *Error
	if !errors.As(err, &sdkError) || sdkError.Category != ErrorValidation {
		t.Fatalf("missing tenant error = %v", err)
	}
}
