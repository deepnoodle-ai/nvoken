package nvoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

const (
	conformanceAgentID      = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
	conformanceInvocationID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
	conformanceSessionID    = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
	conformanceToolCallID   = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
	conformanceWaitID       = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328"
	conformanceExactModelID = "experimental/model?variant=雪%#1"
)

func TestSharedFetchBuiltinFixture(t *testing.T) {
	var fixture struct {
		Declaration map[string]any `json:"declaration"`
	}
	decodeFile(t, "../conformance/fixtures/fetch-builtin-v1.json", &fixture)
	encoded, err := json.Marshal(FetchTool())
	if err != nil {
		t.Fatalf("encode fetch tool: %v", err)
	}
	var declaration map[string]any
	if err := json.Unmarshal(encoded, &declaration); err != nil {
		t.Fatalf("decode fetch tool: %v", err)
	}
	if !reflect.DeepEqual(declaration, fixture.Declaration) {
		t.Fatalf("fetch tool = %#v, want %#v", declaration, fixture.Declaration)
	}
}

// Why a turn stopped is a closed vocabulary the runtime settles against and
// four SDKs decode. A value added on one side and missed on the other reads as
// an unknown enum member to every host switching on it, so each SDK pins its
// generated enum to the same fixture the runtime's settlement test reads.
func TestSharedSettlementLegibilityFixture(t *testing.T) {
	var fixture struct {
		TerminalStatuses []string `json:"terminal_statuses"`
		StopReason       struct {
			Values                []string `json:"values"`
			PresentOnlyOnStatuses []string `json:"present_only_on_statuses"`
		} `json:"stop_reason"`
		MessagePhase struct {
			Values []string `json:"values"`
		} `json:"message_phase"`
	}
	decodeFile(t, "../conformance/fixtures/settlement-legibility-v1.json", &fixture)
	for _, value := range fixture.StopReason.Values {
		if !InvocationStopReason(value).Valid() {
			t.Fatalf("stop reason %q is not a known member", value)
		}
	}
	for _, value := range fixture.MessagePhase.Values {
		if !MessagePhase(value).Valid() {
			t.Fatalf("message phase %q is not a known member", value)
		}
	}
	// The wait helpers stop at exactly these statuses. A terminal the SDK does
	// not recognize is a wait that never returns, so the set is pinned rather
	// than the enum alone.
	for _, value := range fixture.TerminalStatuses {
		status := InvocationStatus(value)
		if !status.Valid() {
			t.Fatalf("terminal status %q is not a known member", value)
		}
		if !terminal(status) {
			t.Fatalf("terminal status %q is not treated as terminal", value)
		}
	}
	for _, status := range []InvocationStatus{InvocationQueued, InvocationRunning, InvocationWaiting, InvocationPaused} {
		if terminal(status) {
			t.Fatalf("%q must not be treated as terminal", status)
		}
	}
	for _, value := range fixture.StopReason.PresentOnlyOnStatuses {
		if status := InvocationStatus(value); status != InvocationCompleted &&
			status != InvocationIncomplete && status != InvocationPaused {
			t.Fatalf("stop reasons are pinned to status %q", value)
		}
	}
}

// The ask_user shape is published in four SDKs plus a fixture the runtime's own
// admission test reads. Five hand-written copies drift, and a host that copies
// the guide's schema into an agent nvoken then rejects gets the worst kind of
// bug report, so each copy is pinned to the fixture here and in the three other
// conformance suites.
func TestSharedAskUserToolFixture(t *testing.T) {
	var fixture struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"input_schema"`
	}
	decodeFile(t, "../conformance/fixtures/ask-user-tool-v1.json", &fixture)
	tool := AskUserTool("", nil)
	if tool.Name != fixture.Name || tool.Name != AskUserToolName {
		t.Fatalf("ask_user name = %q, want %q", tool.Name, fixture.Name)
	}
	if tool.Description != fixture.Description {
		t.Fatalf("ask_user description = %q, want %q", tool.Description, fixture.Description)
	}
	if !reflect.DeepEqual(tool.InputSchema, fixture.InputSchema) {
		t.Fatalf("ask_user schema = %#v, want %#v", tool.InputSchema, fixture.InputSchema)
	}
}

// Session options, host metadata, and provider tools are built by four
// independently written request builders. This pins each of them to the same
// fixture, so a field one binding spells differently fails here rather than
// being silently dropped on the way to the Runtime.
func TestSharedSessionLifecycleFixture(t *testing.T) {
	var fixture struct {
		SessionOptions struct {
			RetentionOnly json.RawMessage `json:"retention_only"`
			MetadataOnly  json.RawMessage `json:"metadata_only"`
			EveryMember   json.RawMessage `json:"every_member"`
		} `json:"session_options"`
		InvocationMetadata map[string]string `json:"invocation_metadata"`
		ProviderTools      struct {
			Defaults   json.RawMessage `json:"defaults"`
			Configured json.RawMessage `json:"configured"`
		} `json:"provider_tools"`
	}
	decodeFile(t, "../conformance/fixtures/session-lifecycle-v1.json", &fixture)

	for name, pair := range map[string]struct {
		Actual any
		Want   json.RawMessage
	}{
		"retention only": {
			Actual: SessionOptions{Retention: &SessionRetention{TTLSeconds: 86400}},
			Want:   fixture.SessionOptions.RetentionOnly,
		},
		"metadata only": {
			Actual: SessionOptions{Metadata: map[string]string{
				"board":    "brand-2026",
				"trace_id": "018f-4a",
			}},
			Want: fixture.SessionOptions.MetadataOnly,
		},
		"every member": {
			Actual: SessionOptions{
				Compaction: &ContextCompaction{TriggerTokens: ContextCompactionAt(32768)},
				Retention:  &SessionRetention{TTLSeconds: 3600},
				Metadata:   map[string]string{"surface": "web"},
			},
			Want: fixture.SessionOptions.EveryMember,
		},
		"provider tool defaults": {
			Actual: []ProviderTool{WebSearchProviderTool()},
			Want:   fixture.ProviderTools.Defaults,
		},
		"provider tool configured": {
			Actual: []ProviderTool{{
				Type: ProviderToolWebSearch,
				WebSearch: &WebSearchTool{
					MaxUses:        5,
					AllowedDomains: []string{"example.com", "docs.example.com"},
					UserLocation: &WebSearchLocation{
						City:     "Austin",
						Region:   "Texas",
						Country:  "US",
						Timezone: "America/Chicago",
					},
				},
			}},
			Want: fixture.ProviderTools.Configured,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assertEncodesTo(t, pair.Actual, pair.Want)
		})
	}

	// The wire builder carries both maps, so an option a caller sets cannot be
	// dropped between the typed request and the request body.
	sessionKey := "conformance"
	body, err := InvokeRequest{
		AgentKey:          "support",
		AgentDefinitionID: "def_conformance",
		IdempotencyKey:    "conformance",
		Input:             "hello",
		SessionKey:        &sessionKey,
		SessionOptions:    &SessionOptions{Retention: &SessionRetention{TTLSeconds: 86400}},
		Metadata:          fixture.InvocationMetadata,
	}.encoded()
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	assertEncodesTo(t, wire["metadata"], mustEncode(t, fixture.InvocationMetadata))
	assertEncodesTo(t, wire["session_options"], fixture.SessionOptions.RetentionOnly)
	if wire["agent_definition_id"] != "def_conformance" {
		t.Fatalf("agent_definition_id = %#v", wire["agent_definition_id"])
	}

	// Session options with no members would serialize to `{}`, which the
	// Runtime rejects for minProperties — catching it locally names the field.
	if _, err := (InvokeRequest{
		AgentKey:          "support",
		AgentDefinitionID: "def_conformance",
		IdempotencyKey:    "conformance",
		Input:             "hello",
		SessionKey:        &sessionKey,
		SessionOptions:    &SessionOptions{},
	}).encoded(); err == nil {
		t.Fatal("empty session options were admitted")
	}
}

// The Agent binding is where a host actually spends its time, and it is the
// layer that fell behind the contract in every SDK. Pinning the whole Agent-issued
// body means an option the binding cannot forward is a missing key here.
func TestSharedAgentRequestFixture(t *testing.T) {
	var fixture struct {
		AgentRequest struct {
			WebSearchMetadataUnbound json.RawMessage `json:"web_search_metadata_unbound"`
		} `json:"agent_request"`
	}
	decodeFile(t, "../conformance/fixtures/session-lifecycle-v1.json", &fixture)

	client, err := NewClient("https://runtime.example.test", "key")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	agent, err := client.Agent(AgentOptions{
		AgentKey:          "support",
		AgentDefinitionID: "def_conformance",
	})
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	// Durable options apply on a new anonymous Session too, which is where a
	// short retention window matters most.
	body, err := agent.request("hello", AgentInvocationOptions{
		IdempotencyKey:    "conformance",
		OnBudgetExhausted: BudgetExhaustionPause,
		Metadata:          map[string]string{"board": "brand-2026", "surface": "web"},
		SessionOptions: &SessionOptions{
			Retention: &SessionRetention{TTLSeconds: 86400},
		},
	}).encoded()
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	assertEncodesTo(t, json.RawMessage(body), fixture.AgentRequest.WebSearchMetadataUnbound)

	// A later admission may install compaction or compare existing options even
	// when the caller names the Session directly.
	sessionID := conformanceSessionID
	body, err = agent.request("hello", AgentInvocationOptions{
		IdempotencyKey: "conformance",
		SessionID:      &sessionID,
		SessionOptions: &SessionOptions{Retention: &SessionRetention{TTLSeconds: 86400}},
	}).encoded()
	if err != nil || !bytes.Contains(body, []byte(`"session_options":{"retention":{"ttl_seconds":86400}}`)) {
		t.Fatalf("session options with session id = %s, %v", body, err)
	}
}

func mustEncode(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return encoded
}

func assertEncodesTo(t *testing.T, actual any, want json.RawMessage) {
	t.Helper()
	var expected any
	if err := json.Unmarshal(want, &expected); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	var got any
	if err := json.Unmarshal(mustEncode(t, actual), &got); err != nil {
		t.Fatalf("decode value: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("encoded = %#v, want %#v", got, expected)
	}
}

func TestContextWindowFailureFixture(t *testing.T) {
	var fixture struct {
		Failure json.RawMessage `json:"context_window_failure"`
	}
	decodeFile(t, "../conformance/fixtures/invocation-result.json", &fixture)
	var failure generated.InvocationFailure
	if err := json.Unmarshal(fixture.Failure, &failure); err != nil {
		t.Fatalf("decode generated failure: %v", err)
	}
	if failure.Code != generated.InvocationFailureCodeContextWindowExceeded ||
		failure.Details == nil {
		t.Fatalf("generated failure = %#v", failure)
	}
	for name, want := range map[string]float64{
		"input_tokens":            205321,
		"context_window_tokens":   200000,
		"requested_output_tokens": 4096,
	} {
		if got := (*failure.Details)[name]; got != want {
			t.Fatalf("detail %s = %#v, want numeric %v", name, got, want)
		}
	}
	encoded, err := json.Marshal(failure)
	if err != nil {
		t.Fatalf("encode generated failure: %v", err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("decode round trip: %v", err)
	}
	details := roundTrip["details"].(map[string]any)
	if details["input_tokens"] != float64(205321) {
		t.Fatalf("round-trip details = %#v", details)
	}
}

func TestSharedReasoningControlFixture(t *testing.T) {
	var fixture struct {
		Efforts []ReasoningEffort `json:"efforts"`
		Budgets []int             `json:"budgets"`
		Error   struct {
			Category ErrorCategory  `json:"category"`
			Status   int            `json:"status"`
			Code     string         `json:"code"`
			Message  string         `json:"message"`
			Details  map[string]any `json:"details"`
		} `json:"combination_error"`
	}
	decodeFile(
		t,
		"../conformance/fixtures/reasoning-controls-v1.json",
		&fixture,
	)
	if len(fixture.Efforts) != 5 || len(fixture.Budgets) != 3 {
		t.Fatalf("reasoning fixture = %#v", fixture)
	}
	for _, effort := range fixture.Efforts {
		encoded, err := json.Marshal(Reasoning{Effort: &effort})
		if err != nil || !json.Valid(encoded) {
			t.Fatalf("encode reasoning effort %q: %s, %v", effort, encoded, err)
		}
	}
	for _, budget := range fixture.Budgets {
		encoded, err := json.Marshal(Reasoning{BudgetTokens: &budget})
		if err != nil || !json.Valid(encoded) {
			t.Fatalf("encode reasoning budget %d: %s, %v", budget, encoded, err)
		}
	}
	normalized := &Error{
		Category: fixture.Error.Category,
		Status:   fixture.Error.Status,
		Code:     fixture.Error.Code,
		Message:  fixture.Error.Message,
		Details:  fixture.Error.Details,
	}
	fields, ok := normalized.Details["fields"].([]any)
	if normalized.Category != ErrorValidation ||
		normalized.Status != 400 ||
		normalized.Code != "invalid_request" ||
		normalized.Details["kind"] != "model_control_combination_unsupported" ||
		!ok ||
		len(fields) != 2 ||
		fields[0] != "reasoning.budget_tokens" ||
		fields[1] != "sampling.temperature" {
		t.Fatalf("normalized reasoning error = %#v", normalized)
	}
}

func TestSharedToolChoiceFixture(t *testing.T) {
	var fixture struct {
		Choices []map[string]any `json:"choices"`
	}
	decodeFile(
		t,
		"../conformance/fixtures/tool-choice-v1.json",
		&fixture,
	)
	choices := []ToolChoice{
		{Mode: ToolChoiceAuto},
		{Mode: ToolChoiceNone},
		{Mode: ToolChoiceRequired},
		{Mode: ToolChoiceNamed, Name: "lookup"},
	}
	for index, choice := range choices {
		encoded, err := json.Marshal(choice)
		if err != nil {
			t.Fatalf("encode tool choice: %v", err)
		}
		var declaration map[string]any
		if err := json.Unmarshal(encoded, &declaration); err != nil {
			t.Fatalf("decode tool choice: %v", err)
		}
		if !reflect.DeepEqual(declaration, fixture.Choices[index]) {
			t.Fatalf(
				"tool choice = %#v, want %#v",
				declaration,
				fixture.Choices[index],
			)
		}
	}
}

func TestSharedMediaInputFixtureMatchesPreflight(t *testing.T) {
	var fixture struct {
		Limits struct {
			MediaBlocks     int `json:"media_blocks"`
			ImageBytes      int `json:"image_bytes"`
			DocumentBytes   int `json:"document_bytes"`
			MediaBytes      int `json:"media_bytes"`
			TitleCharacters int `json:"title_characters"`
		} `json:"limits"`
		MediaTypes struct {
			Image    []string `json:"image"`
			Document []string `json:"document"`
		} `json:"media_types"`
		Accepted []struct {
			ID      string         `json:"id"`
			Content []fixtureBlock `json:"content"`
		} `json:"accepted"`
		Rejected []struct {
			ID      string         `json:"id"`
			Content []fixtureBlock `json:"content"`
			Issue   struct {
				Code    string `json:"code"`
				Path    string `json:"path"`
				Message string `json:"message"`
			} `json:"issue"`
		} `json:"rejected"`
	}
	decodeFile(t, "../conformance/fixtures/media-input-v1.json", &fixture)
	if fixture.Limits.MediaBlocks != MaxMediaInputBlocks ||
		fixture.Limits.ImageBytes != MaxImageInputBytes ||
		fixture.Limits.DocumentBytes != MaxDocumentInputBytes ||
		fixture.Limits.MediaBytes != MaxMediaInputBytes ||
		fixture.Limits.TitleCharacters != MaxMediaTitleCharacters {
		t.Fatalf("media limits = %#v", fixture.Limits)
	}
	if !reflect.DeepEqual(fixture.MediaTypes.Image, ImageMediaTypes()) ||
		!reflect.DeepEqual(fixture.MediaTypes.Document, DocumentMediaTypes()) {
		t.Fatalf("media types = %#v", fixture.MediaTypes)
	}
	for _, accepted := range fixture.Accepted {
		if err := PreflightInputBlocks(fixtureBlocks(accepted.Content)); err != nil {
			t.Fatalf("%s: %v", accepted.ID, err)
		}
	}
	for _, rejected := range fixture.Rejected {
		err := PreflightInputBlocks(fixtureBlocks(rejected.Content))
		var typed *Error
		if !errors.As(err, &typed) ||
			typed.Code != MediaPreflightCode ||
			typed.Details["kind"] != "input_media" ||
			typed.Details["code"] != rejected.Issue.Code ||
			typed.Details["path"] != rejected.Issue.Path ||
			typed.Message != "input is invalid: "+rejected.Issue.Message {
			t.Fatalf("%s: error = %#v", rejected.ID, err)
		}
	}
}

func TestSharedAgentDefinitionReuseFixtureIsExpressible(t *testing.T) {
	var fixture struct {
		AgentDefinitionID string `json:"agent_definition_id"`
	}
	decodeFile(t, "../conformance/fixtures/agent-definition-reuse-v1.json", &fixture)
	encoded, err := (InvokeRequest{
		AgentKey:          "support",
		IdempotencyKey:    "agent-definition-reference",
		Input:             "hello",
		AgentDefinitionID: fixture.AgentDefinitionID,
	}).encoded()
	if err != nil {
		t.Fatalf("encode Agent Definition reference: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode Agent Definition reference: %v", err)
	}
	if wire["agent_definition_id"] != fixture.AgentDefinitionID ||
		wire["agent_definition"] != nil {
		t.Fatalf("Agent Definition reference wire = %#v", wire)
	}
}

type fixtureBlock struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Title  string `json:"title"`
	Source *struct {
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

func fixtureBlocks(blocks []fixtureBlock) []InputBlock {
	converted := make([]InputBlock, len(blocks))
	for index, block := range blocks {
		converted[index] = InputBlock{
			Type:  block.Type,
			Text:  block.Text,
			Title: block.Title,
		}
		if block.Source != nil {
			converted[index].Source = &InputBlockSource{
				MediaType: block.Source.MediaType,
				Data:      block.Source.Data,
				URL:       block.Source.URL,
			}
		}
	}
	return converted
}

func TestSharedContextCompactionFixtureIsExpressible(t *testing.T) {
	var fixture struct {
		Auto     json.RawMessage `json:"auto"`
		Explicit json.RawMessage `json:"explicit"`
		Errors   []struct {
			Kind   string   `json:"kind"`
			Field  string   `json:"field"`
			Fields []string `json:"fields"`
		} `json:"errors"`
	}
	decodeFile(
		t,
		"../conformance/fixtures/context-compaction-v1.json",
		&fixture,
	)
	auto := SessionOptions{
		Compaction: &ContextCompaction{
			TriggerTokens: AutoContextCompaction(),
		},
	}
	explicit := SessionOptions{
		Compaction: &ContextCompaction{
			TriggerTokens: ContextCompactionAt(32768),
			Model: &Model{
				Provider: "anthropic",
				ID:       "claude-sonnet-4-6",
			},
		},
	}
	for name, value := range map[string]struct {
		Actual any
		Want   json.RawMessage
	}{
		"auto":     {Actual: auto, Want: fixture.Auto},
		"explicit": {Actual: explicit, Want: fixture.Explicit},
	} {
		encoded, err := json.Marshal(value.Actual)
		if err != nil || !jsonEqual(encoded, value.Want) {
			t.Fatalf("%s context = %s, error = %v", name, encoded, err)
		}
	}
	if len(fixture.Errors) != 2 ||
		fixture.Errors[0].Field != "session_options.compaction.trigger_tokens" ||
		len(fixture.Errors[1].Fields) != 2 {
		t.Fatalf("context errors = %#v", fixture.Errors)
	}
}

func jsonEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	return json.Unmarshal(left, &leftValue) == nil &&
		json.Unmarshal(right, &rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}

func TestConformance(t *testing.T) {
	baseURL := os.Getenv("NVOKEN_CONFORMANCE_URL")
	if baseURL == "" {
		t.Skip("NVOKEN_CONFORMANCE_URL is not set")
	}
	resetConformance(t, baseURL)
	client, err := NewClient(baseURL, "test-key", WithRetryPolicy(RetryPolicy{
		MaxAttempts: 3,
		MinDelay:    time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	}))
	if err != nil {
		t.Fatal(err)
	}
	agentKey := "support"
	agents, err := client.ListAgents(context.Background(), ListAgentsOptions{AgentKey: &agentKey})
	if err != nil || len(agents.Items) != 1 || agents.Items[0].ID != conformanceAgentID {
		t.Fatalf("list Agent identities: %#v err=%v", agents, err)
	}
	agent, err := client.GetAgent(context.Background(), conformanceAgentID)
	if err != nil || agent.AgentKey != agentKey {
		t.Fatalf("get Agent identity: %#v err=%v", agent, err)
	}
	var resultFixture struct {
		MessageJoin struct {
			ExpectedOutputText string `json:"expected_output_text"`
		} `json:"message_join"`
	}
	decodeFile(t, "../conformance/fixtures/invocation-result.json", &resultFixture)
	var toolCallFixture struct {
		ToolCalls ToolCallList `json:"tool_calls"`
	}
	decodeFile(t, "../conformance/fixtures/tool-call-records-v1.json", &toolCallFixture)
	models, err := client.ListModels(context.Background(), ListModelsOptions{})
	if err != nil || models.CatalogVersion != "conformance-catalog-v1" {
		t.Fatalf("list models: %#v err=%v", models, err)
	}
	foundFutureProvider := false
	for _, model := range models.Items {
		if model.ID == "future-model" && model.Provider == "future_provider" {
			foundFutureProvider = true
		}
	}
	if !foundFutureProvider {
		t.Fatalf("future provider did not decode: %#v", models.Items)
	}
	if models.Items[0].Controls == nil ||
		!models.Items[0].Controls.Sampling.Temperature ||
		!models.Items[0].Controls.Reasoning.Effort.Supported ||
		len(models.Items[0].Controls.Reasoning.Effort.Values) != 5 {
		t.Fatalf("model controls did not decode: %#v", models.Items[0].Controls)
	}
	mcpTools, err := client.ListMCPTools(
		context.Background(),
		MCPServer{
			Name:         "support",
			URL:          "https://mcp.example.test/rpc",
			AllowedTools: []string{"lookup"},
		},
		map[string]string{"Authorization": "Bearer conformance-mcp-secret"},
	)
	if err != nil || len(mcpTools.Tools) != 1 ||
		mcpTools.Tools[0].ProjectedName != "support__lookup" {
		t.Fatalf("list MCP tools: %#v err=%v", mcpTools, err)
	}
	exactModel, err := client.GetModel(context.Background(), Model{
		Provider: "openai",
		ID:       conformanceExactModelID,
	})
	if err != nil || exactModel.ID != conformanceExactModelID || exactModel.Cataloged {
		t.Fatalf("exact model lookup: %#v err=%v", exactModel, err)
	}
	defaultTenant := true
	credits, err := client.AllocateCredits(context.Background(), AllocateCreditsInput{
		Amount:         Money{Amount: "25.000000", Currency: "USD"},
		DefaultTenant:  &defaultTenant,
		IdempotencyKey: "go-credit-conformance",
	})
	if err != nil || credits.Account.PausedInvocations != 2 || credits.Account.Available.Amount != "20.250000" {
		t.Fatalf("allocate Credits: %#v err=%v", credits, err)
	}
	accounts, err := client.ListCreditAccounts(context.Background(), &ListCreditAccountsParams{DefaultTenant: &defaultTenant})
	if err != nil || len(accounts.Items) != 1 || accounts.Items[0].Available.Amount != "20.250000" {
		t.Fatalf("list Credit accounts: %#v err=%v", accounts, err)
	}
	allocations, err := client.ListCreditAllocations(context.Background(), &ListCreditAllocationsParams{DefaultTenant: &defaultTenant})
	if err != nil || len(allocations.Items) != 1 || allocations.Items[0].Amount.Amount != "25.000000" {
		t.Fatalf("list Credit allocations: %#v err=%v", allocations, err)
	}
	request := InvokeRequest{
		AgentKey:          "support",
		AgentDefinitionID: "def_conformance",
		IdempotencyKey:    "conformance-lost-ack",
		IfActive:          IfActiveSupersede,
		Input:             "hello",
		MCPServerHeaders: []MCPServerHeaders{{
			Name:    "support",
			Headers: map[string]string{"Authorization": "Bearer conformance-mcp-secret"},
		}},
		ProviderKeys: []ProviderKeySelection{{
			Provider: "openai",
			Source:   ProviderKeyCallerEphemeral,
			APIKey:   "conformance-secret",
		}},
	}
	handle, err := client.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("lost-ack admission retry: %v", err)
	}
	if handle.InvocationID != conformanceInvocationID || handle.SessionID != conformanceSessionID {
		t.Fatalf("unexpected durable handle: %#v", handle)
	}
	toolCallLimit := 4
	toolCalls, err := handle.ListToolCalls(context.Background(), ListToolCallsOptions{Limit: &toolCallLimit})
	if err != nil || !reflect.DeepEqual(*toolCalls, toolCallFixture.ToolCalls) {
		t.Fatalf("ToolCall records = %#v, want %#v, err = %v", toolCalls, toolCallFixture.ToolCalls, err)
	}
	resumed := client.Invocation(conformanceInvocationID)
	_, err = resumed.Refresh(context.Background())
	if err != nil || resumed.Status != InvocationCompleted {
		t.Fatalf("resume by ID: status=%v err=%v", resumed.Status, err)
	}

	waitInvocationHandle := client.Invocation(conformanceWaitID)
	waitContext, cancelWait := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelWait()
	_, err = waitInvocationHandle.Wait(waitContext, WaitOptions{
		MinPollInterval: time.Millisecond,
		MaxPollInterval: 2 * time.Millisecond,
	})
	var waitError *Error
	if !errors.As(err, &waitError) || waitError.Category != ErrorTimeout {
		t.Fatalf("wait should end locally with timeout, got %v", err)
	}

	firstPage, err := client.ListInvocations(context.Background(), ListInvocationsOptions{
		AgentKey: &agentKey,
		Statuses: []InvocationStatus{
			InvocationQueued,
			InvocationRunning,
			InvocationQueued,
		},
	})
	if err != nil || !firstPage.HasMore || firstPage.NextCursor == nil {
		t.Fatalf("invocation cursor page: %#v err=%v", firstPage, err)
	}
	secondPage, err := client.ListInvocations(context.Background(), ListInvocationsOptions{
		AgentKey: &agentKey,
		Statuses: []InvocationStatus{
			InvocationWaiting,
			InvocationQueued,
			InvocationRunning,
		},
		Cursor: firstPage.NextCursor,
	})
	if err != nil || secondPage.HasMore {
		t.Fatalf("invocation cursor continuation: %#v err=%v", secondPage, err)
	}
	sessions, err := client.ListSessions(context.Background(), ListSessionsOptions{AgentKey: &agentKey})
	if err != nil || len(sessions.Items) != 1 || sessions.Items[0].AgentID == nil || string(*sessions.Items[0].AgentID) != conformanceAgentID ||
		sessions.Items[0].Usage == nil || sessions.Items[0].Usage.InputTokens != 9 ||
		sessions.Items[0].Context == nil || sessions.Items[0].Context.EstimatedTokens != 12 ||
		sessions.Items[0].Context.ContextWindowTokens == nil || *sessions.Items[0].Context.ContextWindowTokens != 128000 ||
		sessions.Items[0].Context.Model.Provider != "openai" || sessions.Items[0].Context.Model.ID != "gpt-test" {
		t.Fatalf("Session list by Agent key: %#v err=%v", sessions, err)
	}
	messagePage, err := client.ListSessionMessages(context.Background(), conformanceSessionID, MessageListOptions{})
	if err != nil || !messagePage.HasMore || messagePage.NextCursor == nil {
		t.Fatalf("message cursor page: %#v err=%v", messagePage, err)
	}
	compactions, err := client.ListSessionCompactions(
		context.Background(),
		conformanceSessionID,
		CompactionListOptions{},
	)
	if err != nil || len(compactions.Items) != 1 ||
		compactions.Items[0].Status != generated.Applied {
		t.Fatalf("Session compactions: %#v err=%v", compactions, err)
	}

	composed, err := handle.Result(context.Background())
	if err != nil || composed.Invocation.ID != conformanceInvocationID || composed.Invocation.Status != InvocationCompleted {
		t.Fatalf("composed result: %#v err=%v", composed, err)
	}
	if len(composed.Messages) != 3 || composed.OutputText == nil ||
		*composed.OutputText != resultFixture.MessageJoin.ExpectedOutputText {
		t.Fatalf("composed result payload: %#v", composed)
	}
	if composed.Messages[0].Role != "user" || composed.Messages[1].Role != "assistant" ||
		composed.Messages[2].Role != "assistant" {
		t.Fatalf("composed result roles: %#v", composed.Messages)
	}
	if composed.Invocation.StructuredOutput == nil || (*composed.Invocation.StructuredOutput)["answer"] != "world" {
		t.Fatalf("composed structured output: %#v", composed.Invocation.StructuredOutput)
	}
	if composed.Invocation.StructuredOutputProvenance == nil || composed.Invocation.StructuredOutputProvenance.Source != "tool_call" {
		t.Fatalf("composed structured output provenance: %#v", composed.Invocation.StructuredOutputProvenance)
	}
	text, err := handle.OutputText(context.Background())
	if err != nil || text != *composed.OutputText {
		t.Fatalf("handle text = %q, want the wire output_text; err=%v", text, err)
	}
	handleMessages, err := handle.ListMessages(context.Background())
	if err != nil || len(handleMessages) != 3 {
		t.Fatalf("handle messages: %#v err=%v", handleMessages, err)
	}

	result, err := handle.SubmitToolResults(context.Background(), []ToolResult{{
		ToolCallID: conformanceToolCallID,
		Content:    map[string]any{"ok": true},
	}})
	if err != nil || len(result.Results) != 1 || !result.Results[0].Deduplicated {
		t.Fatalf("tool result replay: %#v err=%v", result, err)
	}
	cancelled, err := handle.Cancel(context.Background())
	if err != nil || cancelled.Status != InvocationCancelled {
		t.Fatalf("explicit cancel: %#v err=%v", cancelled, err)
	}
	if cancelled.StopReason != nil {
		t.Fatalf("cancelled carried stop reason %q", *cancelled.StopReason)
	}
	interrupted, err := handle.Interrupt(context.Background())
	if err != nil || interrupted.Status != InvocationCompleted ||
		interrupted.StopReason == nil || *interrupted.StopReason != StopReasonInterrupted {
		t.Fatalf("graceful interrupt: %#v err=%v", interrupted, err)
	}
	if interrupted.Attempt != 1 {
		t.Fatalf("attempt = %d, want the wire value", interrupted.Attempt)
	}

	assertGoError(t, client, "conflict", ErrorConflict, http.StatusConflict)
	assertGoError(t, client, "unauthenticated", ErrorAuthentication, http.StatusUnauthorized)
	assertGoError(t, client, "forbidden", ErrorPermission, http.StatusForbidden)
	if _, err := client.GetInvocation(context.Background(), "rate-limit"); err != nil {
		t.Fatalf("429 should be retried: %v", err)
	}
	assertGoError(t, client, "rate-limit-always", ErrorRateLimit, http.StatusTooManyRequests)
	assertGoError(t, client, "server-error", ErrorServer, http.StatusServiceUnavailable)

	streamInvocationHandle := client.Invocation(conformanceInvocationID)
	var eventTypes []string
	deltas := false
	if err := streamInvocationHandle.StreamWithOptions(context.Background(), StreamOptions{Deltas: &deltas}, func(event StreamEvent) error {
		eventTypes = append(eventTypes, event.Type)
		return nil
	}); err != nil {
		t.Fatalf("resumable stream: %v", err)
	}
	if fmt.Sprint(eventTypes) != "[invocation.update stream.end invocation.update invocation.result]" {
		t.Fatalf("unexpected Invocation stream events: %#v", eventTypes)
	}
	var serverState struct {
		AdmissionAttempts    int      `json:"admission_attempts"`
		CredentialAdmissions int      `json:"credential_admissions"`
		ResultAttempts       int      `json:"result_attempts"`
		CancelAttempts       int      `json:"cancel_attempts"`
		InterruptAttempts    int      `json:"interrupt_attempts"`
		StreamAttempts       int      `json:"stream_attempts"`
		LastEventID          string   `json:"last_event_id"`
		LastStatuses         []string `json:"last_statuses"`
		LastDeltas           string   `json:"last_deltas"`
	}
	readJSON(t, baseURL+"/__test/state", &serverState)
	if serverState.AdmissionAttempts != 2 || serverState.CredentialAdmissions != 2 ||
		serverState.ResultAttempts != 2 || serverState.CancelAttempts != 1 ||
		serverState.InterruptAttempts != 1 ||
		serverState.StreamAttempts != 3 || serverState.LastEventID != "cursor-1" ||
		fmt.Sprint(serverState.LastStatuses) != "[waiting queued running]" ||
		serverState.LastDeltas != "false" {
		t.Fatalf("fault server did not observe replay semantics: %#v", serverState)
	}
}

func TestTransportErrorDistinguishesCancellationAndDeadline(t *testing.T) {
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	var cancelled *Error
	if err := transportError(cancelledContext.Err()); !errors.As(err, &cancelled) || cancelled.Category != ErrorCancelled {
		t.Fatalf("context cancellation category = %#v, want %q", err, ErrorCancelled)
	}

	deadlineContext, stop := context.WithTimeout(context.Background(), time.Nanosecond)
	defer stop()
	<-deadlineContext.Done()
	var timeout *Error
	if err := transportError(deadlineContext.Err()); !errors.As(err, &timeout) || timeout.Category != ErrorTimeout {
		t.Fatalf("context deadline category = %#v, want %q", err, ErrorTimeout)
	}
}

func TestSharedCallbackVector(t *testing.T) {
	var vector struct {
		Key     string            `json:"key"`
		Now     int64             `json:"now"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	decodeFile(t, "../../docs/design/callback-signing-v1.json", &vector)
	header := make(http.Header)
	for name, value := range vector.Headers {
		header.Set(name, value)
	}
	verified, err := VerifyCallback([]byte(vector.Key), header, []byte(vector.Body), time.Unix(vector.Now, 0))
	if err != nil || verified.ToolCallID != conformanceToolCallID {
		t.Fatalf("verify shared callback vector: %#v err=%v", verified, err)
	}
	for name, mutate := range map[string]func(http.Header, []byte) (http.Header, []byte){
		"body": func(headers http.Header, body []byte) (http.Header, []byte) {
			return headers, append(append([]byte(nil), body...), ' ')
		},
		"timestamp": func(headers http.Header, body []byte) (http.Header, []byte) {
			headers.Set("X-Nvoken-Timestamp", "1784635801")
			return headers, body
		},
		"delivery": func(headers http.Header, body []byte) (http.Header, []byte) {
			headers.Set("X-Nvoken-Delivery-ID", "different")
			return headers, body
		},
		"signature": func(headers http.Header, body []byte) (http.Header, []byte) {
			headers.Set("X-Nvoken-Signature", "sha256=00")
			return headers, body
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedHeader := header.Clone()
			changedHeader, changedBody := mutate(changedHeader, []byte(vector.Body))
			if _, err := VerifyCallback([]byte(vector.Key), changedHeader, changedBody, time.Unix(vector.Now, 0)); err == nil {
				t.Fatal("tampered callback was accepted")
			}
		})
	}
	store := &memoryResultStore{}
	first, duplicate, err := DeduplicateCallbackResult(context.Background(), store, conformanceToolCallID, json.RawMessage(`{"ok":true}`))
	if err != nil || duplicate {
		t.Fatalf("first result: %s duplicate=%v err=%v", first, duplicate, err)
	}
	stored, duplicate, err := DeduplicateCallbackResult(context.Background(), store, conformanceToolCallID, json.RawMessage(`{"ok":false}`))
	if err != nil || !duplicate || string(stored) != `{"ok":true}` {
		t.Fatalf("duplicate result: %s duplicate=%v err=%v", stored, duplicate, err)
	}
}

func TestSharedReducerVector(t *testing.T) {
	var fixture struct {
		Events []struct {
			ID    string          `json:"id"`
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		} `json:"events"`
		PreviewCases []struct {
			Name   string `json:"name"`
			Events []struct {
				ID    string          `json:"id"`
				Event string          `json:"event"`
				Data  json.RawMessage `json:"data"`
			} `json:"events"`
			ExpectedPreviews []StreamPreview `json:"expected_previews"`
		} `json:"preview_cases"`
		Expected struct {
			MessageSequences    []int64         `json:"message_sequences"`
			InvocationRevisions []int64         `json:"invocation_revisions"`
			ResumeCursor        string          `json:"resume_cursor"`
			Previews            []StreamPreview `json:"previews"`
		} `json:"expected"`
	}
	decodeFile(t, "../conformance/fixtures/reducer.json", &fixture)
	reducer := NewReducer()
	for _, event := range fixture.Events {
		if err := reducer.Apply(StreamEvent{
			ID:   event.ID,
			Type: event.Event,
			Data: event.Data,
		}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := reducer.Snapshot()
	if len(snapshot.Messages) != len(fixture.Expected.MessageSequences) || len(snapshot.InvocationChanges) != len(fixture.Expected.InvocationRevisions) {
		t.Fatalf("reducer counts differ: %#v", snapshot)
	}
	for index, sequence := range fixture.Expected.MessageSequences {
		if snapshot.Messages[index].Sequence != sequence {
			t.Fatalf("message sequence %d = %d, want %d", index, snapshot.Messages[index].Sequence, sequence)
		}
	}
	for index, revision := range fixture.Expected.InvocationRevisions {
		if snapshot.InvocationChanges[index].Revision != revision {
			t.Fatalf("Invocation revision %d = %d, want %d", index, snapshot.InvocationChanges[index].Revision, revision)
		}
	}
	if snapshot.ResumeCursor != fixture.Expected.ResumeCursor {
		t.Fatalf("resume cursor = %q, want %q", snapshot.ResumeCursor, fixture.Expected.ResumeCursor)
	}
	if len(snapshot.Previews) != len(fixture.Expected.Previews) {
		t.Fatalf("previews = %#v, want %#v", snapshot.Previews, fixture.Expected.Previews)
	}
	for _, previewCase := range fixture.PreviewCases {
		t.Run(previewCase.Name, func(t *testing.T) {
			previewReducer := NewReducer()
			for _, event := range previewCase.Events {
				if err := previewReducer.Apply(StreamEvent{
					ID:   event.ID,
					Type: event.Event,
					Data: event.Data,
				}); err != nil {
					t.Fatal(err)
				}
			}
			actual := previewReducer.Snapshot().Previews
			if len(actual) != len(previewCase.ExpectedPreviews) {
				t.Fatalf("previews = %#v, want %#v", actual, previewCase.ExpectedPreviews)
			}
			for index := range actual {
				if actual[index] != previewCase.ExpectedPreviews[index] {
					t.Fatalf("preview %d = %#v, want %#v", index, actual[index], previewCase.ExpectedPreviews[index])
				}
			}
		})
	}
}

type memoryResultStore struct {
	value json.RawMessage
}

func (s *memoryResultStore) PutIfAbsent(_ context.Context, _ string, result json.RawMessage) (json.RawMessage, bool, error) {
	if s.value != nil {
		return s.value, false, nil
	}
	s.value = append(json.RawMessage(nil), result...)
	return s.value, true, nil
}

func assertGoError(t *testing.T, client *Client, invocationID string, category ErrorCategory, status int) {
	t.Helper()
	_, err := client.GetInvocation(context.Background(), invocationID)
	var typed *Error
	if !errors.As(err, &typed) || typed.Category != category || typed.Status != status || typed.RequestID == "" {
		t.Fatalf("typed error %s: %#v", invocationID, err)
	}
	if category == ErrorRateLimit && typed.RetryAfter != time.Second {
		t.Fatalf("typed rate-limit error did not preserve Retry-After: %#v", typed)
	}
}

func resetConformance(t *testing.T, baseURL string) {
	t.Helper()
	response, err := http.Post(baseURL+"/__test/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func readJSON(t *testing.T, url string, target any) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func decodeFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

// TestSharedModelProviderFixtureStaysExpressibleAndUnnormalized proves the SDK
// transmits provider identity verbatim. Provider is an open string in the wire
// contract, so an SDK build predating a provider must still be able to select it,
// and no SDK may normalize a value the Runtime is responsible for judging.
func TestSharedInvocationWebhookFixture(t *testing.T) {
	var fixture struct {
		Events        []string `json:"events"`
		DefaultEvents []string `json:"default_events_when_omitted"`
		Rejected      []string `json:"rejected_events"`
		RejectedURLs  []string `json:"rejected_urls"`
		PayloadFields struct {
			Nvoken     []string `json:"nvoken"`
			Invocation []string `json:"invocation"`
		} `json:"payload_fields"`
		PayloadAbsent  []string `json:"payload_absent_fields"`
		ExampleRequest struct {
			Webhook struct {
				URL    string   `json:"url"`
				Events []string `json:"events"`
			} `json:"webhook"`
		} `json:"example_request"`
		EndedPayload   map[string]map[string]any `json:"example_ended_payload"`
		WaitingPayload map[string]map[string]any `json:"example_waiting_payload"`
		PausedPayload  map[string]map[string]any `json:"example_paused_payload"`
	}
	decodeFile(t, "../conformance/fixtures/invocation-webhooks-v1.json", &fixture)

	events := make([]WebhookEvent, 0, len(fixture.ExampleRequest.Webhook.Events))
	for _, event := range fixture.ExampleRequest.Webhook.Events {
		events = append(events, WebhookEvent(event))
	}
	request := InvokeRequest{
		AgentKey:          "support-bot",
		AgentDefinitionID: "def_conformance",
		IdempotencyKey:    "req-1",
		Input:             "hello",
		Webhook: &WebhookTarget{
			URL:    fixture.ExampleRequest.Webhook.URL,
			Events: events,
		},
	}
	encoded, err := request.encoded()
	if err != nil {
		t.Fatalf("encode webhook request: %v", err)
	}
	var wire struct {
		Webhook struct {
			URL    string   `json:"url"`
			Events []string `json:"events"`
		} `json:"webhook"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode webhook request: %v", err)
	}
	if wire.Webhook.URL != fixture.ExampleRequest.Webhook.URL {
		t.Fatalf("webhook url = %q", wire.Webhook.URL)
	}
	if !slices.Equal(wire.Webhook.Events, fixture.ExampleRequest.Webhook.Events) {
		t.Fatalf("webhook events = %v", wire.Webhook.Events)
	}

	// Omitting events must stay omitted on the wire. The Runtime applies the
	// complete-set default, and an empty array is a rejected request, so an SDK
	// that materialized the default here would change what a replay fingerprints
	// against.
	request.Webhook = &WebhookTarget{URL: fixture.ExampleRequest.Webhook.URL}
	encoded, err = request.encoded()
	if err != nil {
		t.Fatalf("encode webhook request without events: %v", err)
	}
	var withoutEvents map[string]any
	if err := json.Unmarshal(encoded, &withoutEvents); err != nil {
		t.Fatalf("decode webhook request without events: %v", err)
	}
	target, ok := withoutEvents["webhook"].(map[string]any)
	if !ok {
		t.Fatalf("webhook absent from encoded request")
	}
	if _, present := target["events"]; present {
		t.Fatalf("omitted events materialized as %#v", target["events"])
	}
	if !slices.Equal(fixture.DefaultEvents, fixture.Events) {
		t.Fatalf("fixture default events = %v, want the complete set", fixture.DefaultEvents)
	}

	// An event outside the closed set is refused before a request is built.
	for _, rejected := range fixture.Rejected {
		request.Webhook = &WebhookTarget{
			URL:    fixture.ExampleRequest.Webhook.URL,
			Events: []WebhookEvent{WebhookEvent(rejected)},
		}
		if _, err := request.encoded(); err == nil {
			t.Fatalf("event %q was accepted", rejected)
		}
	}
	request.Webhook = &WebhookTarget{}
	if _, err := request.encoded(); err == nil {
		t.Fatal("empty webhook url was accepted")
	}

	// The payload stays a pointer: nothing the fixture lists as absent may appear
	// in either documented example.
	for name, payload := range map[string]map[string]map[string]any{
		"ended":   fixture.EndedPayload,
		"waiting": fixture.WaitingPayload,
		"paused":  fixture.PausedPayload,
	} {
		for key := range payload["nvoken"] {
			if !slices.Contains(fixture.PayloadFields.Nvoken, key) {
				t.Fatalf("%s payload has unexpected nvoken field %q", name, key)
			}
		}
		for key := range payload["invocation"] {
			if !slices.Contains(fixture.PayloadFields.Invocation, key) {
				t.Fatalf("%s payload has unexpected invocation field %q", name, key)
			}
		}
		serialized, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s payload: %v", name, err)
		}
		for _, absent := range fixture.PayloadAbsent {
			if bytes.Contains(serialized, []byte(absent)) {
				t.Fatalf("%s payload leaked %q", name, absent)
			}
		}
	}
	if len(fixture.RejectedURLs) == 0 {
		t.Fatal("fixture must name rejected endpoint forms")
	}
}

func TestSharedModelProviderFixtureStaysExpressibleAndUnnormalized(t *testing.T) {
	var fixture struct {
		Canonical    []string          `json:"canonical"`
		Aliases      map[string]string `json:"aliases_normalized_by_the_runtime_only"`
		Rejected     []string          `json:"rejected_by_the_runtime"`
		Forward      string            `json:"forward_compatible"`
		ExampleModel Model             `json:"example_model"`
	}
	decodeFile(t, "../conformance/fixtures/model-provider-v1.json", &fixture)
	transmitted := append([]string(nil), fixture.Canonical...)
	for alias := range fixture.Aliases {
		transmitted = append(transmitted, alias)
	}
	transmitted = append(transmitted, fixture.Rejected...)
	transmitted = append(transmitted, fixture.Forward)
	for _, provider := range transmitted {
		encoded, err := json.Marshal(Model{
			Provider: provider,
			ID:       "model-id",
		})
		if err != nil {
			t.Fatalf("marshal provider %q: %v", provider, err)
		}
		var decoded Model
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal provider %q: %v", provider, err)
		}
		if decoded.Provider != provider {
			t.Fatalf("provider %q round-tripped as %q", provider, decoded.Provider)
		}
	}
	if fixture.ExampleModel.Provider != "xai" || fixture.ExampleModel.ID == "" {
		t.Fatalf("example model = %#v", fixture.ExampleModel)
	}
}

// TestModelProviderConstantsCoverCanonicalFixture keeps the convenience
// constants from drifting behind the canonical provider set. Provider identity
// stays an open string on the wire, so these constants are a spelling aid
// rather than validation — but a partial list is worse than none, because a
// user reaching for a missing one silently falls back to a string literal.
func TestModelProviderConstantsCoverCanonicalFixture(t *testing.T) {
	declared := map[ModelProvider]bool{
		ModelProviderAnthropic: true,
		ModelProviderOpenAI:    true,
		ModelProviderXAI:       true,
		ModelProviderGoogle:    true,
	}
	var fixture struct {
		Canonical []string `json:"canonical"`
	}
	decodeFile(t, "../conformance/fixtures/model-provider-v1.json", &fixture)
	if len(fixture.Canonical) == 0 {
		t.Fatal("fixture must name canonical providers")
	}
	for _, provider := range fixture.Canonical {
		if !declared[ModelProvider(provider)] {
			t.Errorf("canonical provider %q has no SDK constant", provider)
		}
		delete(declared, ModelProvider(provider))
	}
	for provider := range declared {
		t.Errorf("SDK constant %q is not a canonical provider", provider)
	}
}

// Mid-turn steering is one contract across four SDKs and the runtime: the
// status vocabulary a host switches on, the request body it sends, and the
// acknowledgement fields it reads to know where to watch the transcript. A
// value added on one side and missed on another reads as an unknown member to
// every host, so each SDK pins its copy to this fixture.
func TestSharedInvocationNudgeFixture(t *testing.T) {
	var fixture struct {
		Request struct {
			ContentOnly        json.RawMessage `json:"content_only"`
			WithIdempotencyKey json.RawMessage `json:"with_idempotency_key"`
		} `json:"request"`
		Acknowledgement struct {
			Status int      `json:"status"`
			Fields []string `json:"fields"`
		} `json:"acknowledgement"`
		NudgeStatus struct {
			Values         []string `json:"values"`
			ConsumedState  string   `json:"consumed_state"`
			DrainedCarries string   `json:"drained_carries"`
		} `json:"nudge_status"`
	}
	decodeFile(t, "../conformance/fixtures/invocation-nudge-v1.json", &fixture)

	known := map[string]NudgeStatus{
		"pending":   NudgePending,
		"drained":   NudgeDrained,
		"expired":   NudgeExpired,
		"cancelled": NudgeCancelled,
	}
	for _, value := range fixture.NudgeStatus.Values {
		if known[value] != NudgeStatus(value) {
			t.Fatalf("nudge status %q is not a known member", value)
		}
	}
	if NudgeStatus(fixture.NudgeStatus.ConsumedState) != NudgePending {
		t.Fatalf("consumed state = %q", fixture.NudgeStatus.ConsumedState)
	}

	// The request builder is the one place a field name can drift from the
	// contract, so it is encoded here rather than described.
	for name, pair := range map[string]struct {
		Request NudgeRequest
		Want    json.RawMessage
	}{
		"content only":         {Request: NudgeRequest{Content: "focus on the marine segment"}, Want: fixture.Request.ContentOnly},
		"with idempotency key": {Request: NudgeRequest{Content: "focus on the marine segment", IdempotencyKey: "nudge-1"}, Want: fixture.Request.WithIdempotencyKey},
	} {
		encoded, err := json.Marshal(struct {
			Content        string `json:"content"`
			IdempotencyKey string `json:"idempotency_key,omitempty"`
		}{Content: pair.Request.Content, IdempotencyKey: pair.Request.IdempotencyKey})
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		if !equalJSON(t, encoded, pair.Want) {
			t.Fatalf("%s request = %s, want %s", name, encoded, pair.Want)
		}
	}

	acknowledgement, err := json.Marshal(NudgeAcknowledgement{})
	if err != nil {
		t.Fatalf("encode acknowledgement: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(acknowledgement, &fields); err != nil {
		t.Fatalf("decode acknowledgement: %v", err)
	}
	for _, field := range fixture.Acknowledgement.Fields {
		if _, ok := fields[field]; !ok {
			t.Fatalf("acknowledgement is missing %q; got %v", field, fields)
		}
	}
	if len(fields) != len(fixture.Acknowledgement.Fields) {
		t.Fatalf("acknowledgement fields = %v, want %v", fields, fixture.Acknowledgement.Fields)
	}

	// The drained receipt is what tells a host the model actually saw the
	// input, so its spelling is pinned too.
	drained, err := json.Marshal(Nudge{Status: NudgeDrained})
	if err != nil {
		t.Fatalf("encode nudge: %v", err)
	}
	var pending map[string]any
	if err := json.Unmarshal(drained, &pending); err != nil {
		t.Fatalf("decode nudge: %v", err)
	}
	if _, ok := pending[fixture.NudgeStatus.DrainedCarries]; !ok {
		var typed Nudge
		if err := json.Unmarshal([]byte(`{"drained_message_sequence":7}`), &typed); err != nil ||
			typed.DrainedMessageSequence == nil || *typed.DrainedMessageSequence != 7 {
			t.Fatalf("nudge does not carry %q", fixture.NudgeStatus.DrainedCarries)
		}
	}
}

func equalJSON(t *testing.T, left, right json.RawMessage) bool {
	t.Helper()
	var decodedLeft, decodedRight any
	if err := json.Unmarshal(left, &decodedLeft); err != nil {
		t.Fatalf("decode left: %v", err)
	}
	if err := json.Unmarshal(right, &decodedRight); err != nil {
		t.Fatalf("decode right: %v", err)
	}
	return reflect.DeepEqual(decodedLeft, decodedRight)
}

type recordedContextFixture struct {
	Limits struct {
		Items             int `json:"items"`
		NameCharacters    int `json:"name_characters"`
		ContentBytes      int `json:"content_bytes"`
		TotalContentBytes int `json:"total_content_bytes"`
	} `json:"limits"`
	Tiers    []string `json:"tiers"`
	Accepted struct {
		ID      string `json:"id"`
		Request struct {
			AgentKey          string        `json:"agent_key"`
			SessionKey        string        `json:"session_key"`
			IdempotencyKey    string        `json:"idempotency_key"`
			Input             string        `json:"input"`
			AgentDefinitionID string        `json:"agent_definition_id"`
			Context           []ContextItem `json:"context"`
		} `json:"request"`
		Messages []json.RawMessage `json:"messages"`
	} `json:"accepted"`
	Rejected []struct {
		ID      string        `json:"id"`
		Context []ContextItem `json:"context"`
	} `json:"rejected"`
}

// TestSharedRecordedContextFixtureIsExpressible proves recorded context reaches
// the wire at the top level rather than inside the Agent Definition, and that
// every locally checkable bound is refused before a request is spent.
func TestSharedRecordedContextFixtureIsExpressible(t *testing.T) {
	var fixture recordedContextFixture
	decodeFile(t, "../conformance/fixtures/recorded-context-v1.json", &fixture)
	if fixture.Limits.Items != maxContextItems ||
		fixture.Limits.NameCharacters != maxContextNameRunes ||
		fixture.Limits.ContentBytes != maxContextContentBytes ||
		fixture.Limits.TotalContentBytes != maxContextTotalBytes {
		t.Fatalf("context limits = %#v", fixture.Limits)
	}
	if !slices.Equal(fixture.Tiers, []string{
		string(ContextTierContextual),
		string(ContextTierOperator),
	}) {
		t.Fatalf("context tiers = %#v", fixture.Tiers)
	}

	accepted := fixture.Accepted.Request
	encoded, err := (InvokeRequest{
		AgentKey:          accepted.AgentKey,
		SessionKey:        &accepted.SessionKey,
		IdempotencyKey:    accepted.IdempotencyKey,
		Input:             accepted.Input,
		AgentDefinitionID: accepted.AgentDefinitionID,
		Context:           accepted.Context,
	}).encoded()
	if err != nil {
		t.Fatalf("encode recorded context: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode recorded context: %v", err)
	}
	expected, err := json.Marshal(accepted.Context)
	if err != nil {
		t.Fatalf("encode fixture context: %v", err)
	}
	if !equalJSON(t, wire["context"], expected) {
		t.Fatalf("context wire = %s", wire["context"])
	}
	if _, nested := wire["agent_definition"]; nested {
		t.Fatal("recorded context must not carry an Agent Definition")
	}

	// The transcript stores each snapshot as a typed reminder block whose name
	// carries the reserved prefix the request omits.
	for _, encodedMessage := range fixture.Accepted.Messages {
		var message SessionMessage
		if err := json.Unmarshal(encodedMessage, &message); err != nil {
			t.Fatalf("decode reminder message: %v", err)
		}
		if len(message.Content) != 1 {
			t.Fatalf("reminder message content = %#v", message.Content)
		}
		reminder, err := message.Content[0].AsReminderBlock()
		if err != nil {
			t.Fatalf("decode reminder block: %v", err)
		}
		if reminder.Type != generated.TypeReminder ||
			!strings.HasPrefix(reminder.Name, "app-") ||
			reminder.Content == "" {
			t.Fatalf("reminder block = %#v", reminder)
		}
	}

	for _, rejected := range fixture.Rejected {
		if _, err := (InvokeRequest{Context: rejected.Context}).encodedContext(); err == nil {
			t.Fatalf("%s: recorded context was accepted", rejected.ID)
		}
	}
	for _, generatedCase := range []struct {
		id      string
		context []ContextItem
	}{
		{"too-many-items", generatedContextItems(fixture.Limits.Items+1, 1)},
		{"oversize-name", []ContextItem{{
			Name:    strings.Repeat("a", fixture.Limits.NameCharacters+1),
			Tier:    ContextTierContextual,
			Content: "x",
		}}},
		{"oversize-content", []ContextItem{{
			Name:    "customer",
			Tier:    ContextTierContextual,
			Content: strings.Repeat("a", fixture.Limits.ContentBytes+1),
		}}},
		{"oversize-total", generatedContextItems(3, fixture.Limits.ContentBytes)},
	} {
		if _, err := (InvokeRequest{Context: generatedCase.context}).encodedContext(); err == nil {
			t.Fatalf("%s: recorded context was accepted", generatedCase.id)
		}
	}
}

func generatedContextItems(count, contentBytes int) []ContextItem {
	items := make([]ContextItem, count)
	for index := range items {
		items[index] = ContextItem{
			Name:    fmt.Sprintf("c%d", index),
			Tier:    ContextTierContextual,
			Content: strings.Repeat("a", contentBytes),
		}
	}
	return items
}
