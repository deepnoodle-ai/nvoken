package divegen

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deepnoodle-ai/dive/llm"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

type sequenceLLM struct {
	mu        sync.Mutex
	responses []*llm.Response
	events    *[]string
	calls     int
}

type sequenceCredentialResolver struct {
	credentials []domain.ResolvedProviderCredential
	err         error
	calls       int
}

func (r *sequenceCredentialResolver) ResolveProviderCredential(
	context.Context,
	string,
	string,
) (domain.ResolvedProviderCredential, error) {
	if r.err != nil {
		return domain.ResolvedProviderCredential{}, r.err
	}
	credential := r.credentials[r.calls]
	r.calls++
	return credential, nil
}

func (*sequenceLLM) Name() string { return "sequence" }

func (m *sequenceLLM) Generate(_ context.Context, options ...llm.Option) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls >= len(m.responses) {
		return nil, errors.New("unexpected model call")
	}
	config := llm.Config{}
	config.Apply(options...)
	m.calls++
	if m.events != nil {
		*m.events = append(*m.events, "model")
	}
	return m.responses[m.calls-1], nil
}

func TestResolvingModelLoadsCredentialBeforeEveryProviderCall(t *testing.T) {
	resolver := &sequenceCredentialResolver{credentials: []domain.ResolvedProviderCredential{
		{
			Provider:             "openai",
			Source:               domain.ProviderCredentialSourceAccountBYOK,
			ProviderCredentialID: "pcrd-first",
			CredentialVersionID:  "pcvr-first",
			APIKey:               "first-secret",
		},
		{
			Provider:             "openai",
			Source:               domain.ProviderCredentialSourceAccountBYOK,
			ProviderCredentialID: "pcrd-second",
			CredentialVersionID:  "pcvr-second",
			APIKey:               "second-secret",
		},
	}}
	model := &sequenceLLM{responses: []*llm.Response{{}, {}}}
	var keys []string
	evidence := &modelEvidence{}
	resolving := &resolvingModel{
		resolver: resolver,
		factory: func(_, _, apiKey string) (llm.LLM, error) {
			keys = append(keys, apiKey)
			return model, nil
		},
		invocationID: "invocation",
		provider:     "openai",
		model:        "gpt-test",
		evidence:     evidence,
	}
	if _, err := resolving.Generate(context.Background()); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if _, err := resolving.Generate(context.Background()); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{"first-secret", "second-secret"}) || resolver.calls != 2 {
		t.Fatalf("resolved keys = %#v, calls = %d", keys, resolver.calls)
	}
	provenance := evidence.provenance("gpt-test", "gpt-served")
	if provenance.ProviderCredentialID != "pcrd-second" || provenance.CredentialVersionID != "pcvr-second" ||
		provenance.CredentialSource != "account_byok" {
		t.Fatalf("latest credential provenance = %#v", provenance)
	}
}

func TestResolvingModelDoesNotCallProviderAfterCredentialFailure(t *testing.T) {
	resolver := &sequenceCredentialResolver{err: ports.ErrCredentialUnavailable}
	factoryCalls := 0
	resolving := &resolvingModel{
		resolver: resolver,
		factory: func(_, _, _ string) (llm.LLM, error) {
			factoryCalls++
			return &sequenceLLM{}, nil
		},
		invocationID: "invocation",
		provider:     "anthropic",
		model:        "claude-test",
		evidence:     &modelEvidence{},
	}
	if _, err := resolving.Generate(context.Background()); !errors.Is(err, ports.ErrCredentialUnavailable) {
		t.Fatalf("Generate error = %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("model factory calls = %d, want 0", factoryCalls)
	}
}

func TestGeneratorPreservesCredentialUnavailableForDurableSettlement(t *testing.T) {
	resolver := &sequenceCredentialResolver{err: ports.ErrCredentialUnavailable}
	coordinator := &recordingToolCoordinator{}
	generator := New(
		Config{},
		WithCredentialResolver(resolver),
		WithToolCoordinator(coordinator),
	)
	factoryCalls := 0
	generator.factory = func(_, _, _ string) (llm.LLM, error) {
		factoryCalls++
		return &sequenceLLM{}, nil
	}
	request := generationRequest("anthropic")
	request.Claim = generationClaim()
	_, err := generator.Generate(context.Background(), request)
	if !errors.Is(err, ports.ErrCredentialUnavailable) {
		t.Fatalf("Generate error = %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("model factory calls = %d, want 0", factoryCalls)
	}
}

func TestGeneratorReportsRegisteredUSDModelPricing(t *testing.T) {
	generator := New(Config{})
	dependencies := []*debug.Module{
		{
			Path:    "github.com/deepnoodle-ai/dive",
			Version: "v1.2.3",
		},
		{
			Path:    "github.com/deepnoodle-ai/dive/providers/openai",
			Version: "v4.5.6",
		},
	}
	generator.registryVersion = func(provider string) string {
		return diveRegistryVersionFromDependencies(provider, dependencies)
	}
	tests := []struct {
		name            string
		provider        string
		model           string
		status          domain.ModelPricingStatus
		registryVersion string
	}{
		{
			name:            "known OpenAI model",
			provider:        "openai",
			model:           "gpt-5.4-mini",
			status:          domain.ModelPricingPriced,
			registryVersion: "v4.5.6",
		},
		{
			name:            "known Anthropic model",
			provider:        "anthropic",
			model:           "claude-sonnet-4-6",
			status:          domain.ModelPricingPriced,
			registryVersion: "v1.2.3",
		},
		{
			name:            "Anthropic model under OpenAI",
			provider:        "openai",
			model:           "claude-sonnet-4-6",
			status:          domain.ModelPricingUnpriced,
			registryVersion: "v4.5.6",
		},
		{
			name:            "OpenAI model under Anthropic",
			provider:        "anthropic",
			model:           "gpt-5.4-mini",
			status:          domain.ModelPricingUnpriced,
			registryVersion: "v1.2.3",
		},
		{
			name:            "unregistered model",
			provider:        "openai",
			model:           "unregistered-model",
			status:          domain.ModelPricingUnpriced,
			registryVersion: "v4.5.6",
		},
		{
			name:            "unsupported provider",
			provider:        "unsupported",
			model:           "gpt-5.4-mini",
			status:          domain.ModelPricingUnknown,
			registryVersion: "unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capability := generator.ResolveModelPricing(test.provider, test.model)
			if capability.Status != test.status || capability.RegistryVersion != test.registryVersion {
				t.Fatalf(
					"pricing capability = %#v, want status %q and registry version %q",
					capability,
					test.status,
					test.registryVersion,
				)
			}
		})
	}
}

func TestDiveRegistryVersionUsesProviderDependency(t *testing.T) {
	dependencies := []*debug.Module{
		{
			Path:    "github.com/deepnoodle-ai/dive",
			Version: "v1.2.3",
		},
		{
			Path:    "github.com/deepnoodle-ai/dive/providers/openai",
			Version: "v4.5.6",
		},
	}
	tests := []struct {
		provider string
		want     string
	}{
		{
			provider: "anthropic",
			want:     "v1.2.3",
		},
		{
			provider: "openai",
			want:     "v4.5.6",
		},
		{
			provider: "unsupported",
			want:     "unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			if got := diveRegistryVersionFromDependencies(test.provider, dependencies); got != test.want {
				t.Fatalf("registry version = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGeneratorPreservesCredentialInfrastructureFailures(t *testing.T) {
	for name, resolverErr := range map[string]error{
		"retryable": ports.ErrRetryable,
		"deadline":  context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			resolver := &sequenceCredentialResolver{err: resolverErr}
			generator := New(
				Config{},
				WithCredentialResolver(resolver),
				WithToolCoordinator(&recordingToolCoordinator{}),
			)
			factoryCalls := 0
			generator.factory = func(_, _, _ string) (llm.LLM, error) {
				factoryCalls++
				return &sequenceLLM{}, nil
			}
			request := generationRequest("anthropic")
			request.Claim = generationClaim()
			_, err := generator.Generate(context.Background(), request)
			if !errors.Is(err, resolverErr) {
				t.Fatalf("Generate error = %v", err)
			}
			if factoryCalls != 0 {
				t.Fatalf("model factory calls = %d, want 0", factoryCalls)
			}
		})
	}
}

type recordingToolCoordinator struct {
	mu             sync.Mutex
	events         []string
	checkpoints    []domain.ModelCheckpointInput
	resultContents []json.RawMessage
	resultErrors   []bool
	starts         int
	existingCallID string
}

func (c *recordingToolCoordinator) RecordModelCheckpoint(_ context.Context, claim domain.InvocationClaim, input domain.ModelCheckpointInput) (domain.ModelCheckpointResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, "checkpoint")
	c.checkpoints = append(c.checkpoints, input)
	return domain.ModelCheckpointResult{}, nil
}

func (c *recordingToolCoordinator) StartBuiltinToolCall(_ context.Context, claim domain.InvocationClaim, iteration int, providerCallID string) (domain.ToolCallExecution, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.checkpoints) == 0 || c.checkpoints[len(c.checkpoints)-1].Iteration != iteration {
		return domain.ToolCallExecution{}, errors.New("builtin started before request checkpoint")
	}
	c.events = append(c.events, "start")
	c.starts++
	callID := c.existingCallID
	if callID == "" {
		callID = fmt.Sprintf("tcal_019f84a5-7838-7b57-a180-%012x", c.starts)
	}
	return domain.ToolCallExecution{
		Call: domain.ToolCall{
			ID:             callID,
			ProviderCallID: providerCallID,
			Iteration:      iteration,
		},
		Attempt: domain.ToolCallAttempt{
			ID:      "tcat_019f84a5-7838-7b57-a180-5f74a0b65be1",
			Attempt: 1,
		},
	}, nil
}

func TestDurableToolFreeGenerationCheckpointsFinalResponse(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	model := &fakeLLM{
		result: successfulDiveResponse(),
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithToolCoordinator(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}
	request := generationRequest("anthropic")
	request.Claim = generationClaim()

	response, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	events, checkpoints, starts := coordinator.snapshot()
	if !reflect.DeepEqual(events, []string{"checkpoint"}) || len(checkpoints) != 1 || starts != 0 {
		t.Fatalf("durable final events = %#v, checkpoints = %d, starts = %d", events, len(checkpoints), starts)
	}
	if len(checkpoints[0].ToolCalls) != 0 || !response.MessagesCheckpointed || len(response.Messages) != 0 {
		t.Fatalf("durable final response = %#v, checkpoint = %#v", response, checkpoints[0])
	}
}

func TestDurableGenerationReplaysOpenBuiltinWithStableCallID(t *testing.T) {
	const stableCallID = "tcal_019f84a5-7838-7b57-a180-111111111111"
	coordinator := &recordingToolCoordinator{
		checkpoints: []domain.ModelCheckpointInput{
			{
				Iteration: 1,
			},
		},
		existingCallID: stableCallID,
	}
	model := &fakeLLM{
		result: textResponse("continued"),
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithDeterministicTestBuiltin(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}
	request := generationRequest("anthropic")
	request.Messages = []domain.GenerationMessage{
		{
			Role:    domain.MessageRoleUser,
			Content: json.RawMessage(`[{"type":"text","text":"question"}]`),
		},
		{
			Role: domain.MessageRoleAssistant,
			Content: json.RawMessage(`[{"type":"tool_use","id":"` + stableCallID +
				`","name":"nvoken_test_echo","input":{"value":"hello"}}]`),
		},
	}
	request.Claim = generationClaim()
	request.Claim.Attempt = 2
	request.Claim.Invocation.LeaseAttempt = 2
	request.MaxIterations = 3
	request.Resume = &domain.GenerationResume{
		Iteration: 1,
		Usage: domain.ModelUsage{
			InputTokens:  3,
			OutputTokens: 1,
			Iterations:   1,
		},
		OpenToolCalls: []domain.ResumableToolCall{
			{
				Call: domain.ToolCall{
					ID:             stableCallID,
					ProviderCallID: "provider-call-1",
					Name:           deterministicEchoToolName,
					Mode:           domain.ToolCallModeBuiltin,
				},
				Input: json.RawMessage(`{"value":"hello"}`),
			},
		},
	}

	response, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	events, checkpoints, starts := coordinator.snapshot()
	if !reflect.DeepEqual(events, []string{"start", "result", "checkpoint"}) || starts != 1 || len(checkpoints) != 2 {
		t.Fatalf("resume events = %#v, starts = %d, checkpoints = %#v", events, starts, checkpoints)
	}
	if len(model.config.Messages) != 3 {
		t.Fatalf("resume messages = %#v", model.config.Messages)
	}
	result, ok := model.config.Messages[2].Content[0].(*llm.ToolResultContent)
	if !ok || result.ToolUseID != stableCallID || result.Content != `{"value":"hello"}` {
		t.Fatalf("replayed result = %#v", model.config.Messages[2].Content)
	}
	if response.Usage.Iterations != 2 || response.Usage.InputTokens != 6 || response.Usage.OutputTokens != 2 {
		t.Fatalf("cumulative response = %#v", response)
	}
}

func (c *recordingToolCoordinator) AcceptBuiltinToolResult(_ context.Context, _ domain.InvocationClaim, execution domain.ToolCallExecution, content json.RawMessage, isError bool) (domain.ToolCall, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if execution.Call.ID == "" || len(content) == 0 {
		return domain.ToolCall{}, errors.New("invalid deterministic result")
	}
	c.events = append(c.events, "result")
	c.resultContents = append(c.resultContents, append(json.RawMessage(nil), content...))
	c.resultErrors = append(c.resultErrors, isError)
	return execution.Call, nil
}

func (c *recordingToolCoordinator) snapshot() ([]string, []domain.ModelCheckpointInput, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...), append([]domain.ModelCheckpointInput(nil), c.checkpoints...), c.starts
}

func (c *recordingToolCoordinator) errorSnapshot() []bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]bool(nil), c.resultErrors...)
}

func (c *recordingToolCoordinator) resultSnapshot() []json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]json.RawMessage, len(c.resultContents))
	for index, content := range c.resultContents {
		result[index] = append(json.RawMessage(nil), content...)
	}
	return result
}

type fakeLLM struct {
	config llm.Config
	result *llm.Response
	err    error
}

type fakeStreamingLLM struct {
	fakeLLM
	events    []*llm.Event
	streamErr error
}

func (f *fakeStreamingLLM) Stream(_ context.Context, options ...llm.Option) (llm.StreamIterator, error) {
	f.config.Apply(options...)
	return &fakeStreamIterator{events: f.events, err: f.streamErr, index: -1}, nil
}

type fakeStreamIterator struct {
	events []*llm.Event
	err    error
	index  int
}

func (i *fakeStreamIterator) Next() bool {
	i.index++
	return i.index < len(i.events)
}
func (i *fakeStreamIterator) Event() *llm.Event { return i.events[i.index] }
func (i *fakeStreamIterator) Err() error        { return i.err }
func (i *fakeStreamIterator) Close() error      { return nil }

func (*fakeLLM) Name() string { return "fake" }

func (f *fakeLLM) Generate(_ context.Context, options ...llm.Option) (*llm.Response, error) {
	f.config.Apply(options...)
	return f.result, f.err
}

func TestGeneratorUsesExplicitProviderKeyAndToolFreeDiveCall(t *testing.T) {
	for _, test := range []struct {
		provider string
		wantKey  string
	}{
		{provider: "anthropic", wantKey: "anthropic-secret"},
		{provider: "openai", wantKey: "openai-secret"},
	} {
		t.Run(test.provider, func(t *testing.T) {
			model := &fakeLLM{result: successfulDiveResponse()}
			var gotProvider, gotModel, gotKey string
			generator := New(Config{AnthropicAPIKey: "anthropic-secret", OpenAIAPIKey: "openai-secret"})
			generator.factory = func(provider, requestedModel, apiKey string) (llm.LLM, error) {
				gotProvider, gotModel, gotKey = provider, requestedModel, apiKey
				return model, nil
			}
			request := generationRequest(test.provider)

			response, err := generator.Generate(context.Background(), request)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if gotProvider != test.provider || gotModel != "requested-model" || gotKey != test.wantKey {
				t.Fatalf("factory selection = %q, %q, %q", gotProvider, gotModel, gotKey)
			}
			if !strings.HasPrefix(model.config.SystemPrompt, "durable instructions\n\nRuntime context may appear") || len(model.config.Tools) != 0 {
				t.Fatalf("Dive config = %#v", model.config)
			}
			if len(model.config.Messages) != 2 || model.config.Messages[0].Role != llm.User ||
				model.config.Messages[0].LastText() != "question" || model.config.Messages[1].Role != llm.Assistant ||
				model.config.Messages[1].LastText() != "prior answer" {
				t.Fatalf("Dive messages = %#v", model.config.Messages)
			}
			if model.config.MaxTokens == nil || *model.config.MaxTokens != 321 {
				t.Fatalf("Dive max tokens = %v, want 321", model.config.MaxTokens)
			}
			wantUsage := domain.ModelUsage{
				InputTokens: 11, OutputTokens: 7, CacheCreationInputTokens: 3,
				CacheReadInputTokens: 2, ReasoningTokens: 1, Iterations: 1,
				EstimatedCost: &domain.ModelCost{
					Input: .1, Output: .2, CacheRead: .01, CacheWrite: .02,
					Total: .33, Currency: "USD",
				},
			}
			if response.ServedModel != "served-model" || !reflect.DeepEqual(response.Usage, wantUsage) {
				t.Fatalf("response evidence = %#v", response)
			}
			if len(response.Messages) != 1 || response.Messages[0].Role != domain.MessageRoleAssistant ||
				string(response.Messages[0].Content) != `[{"type":"text","text":"new answer"},{"type":"text","text":"second block"}]` {
				t.Fatalf("normalized messages = %#v", response.Messages)
			}
		})
	}
}

func TestDeterministicBuiltinCheckpointsBeforeExecutionAndNextModelCall(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	model := &sequenceLLM{
		responses: []*llm.Response{
			{
				Model: "served-model",
				Role:  llm.Assistant,
				Content: []llm.Content{
					&llm.ToolUseContent{
						ID:    "provider-call-1",
						Name:  deterministicEchoToolName,
						Input: json.RawMessage(`{"value":"hello"}`),
					},
				},
				Usage: llm.Usage{
					InputTokens:  3,
					OutputTokens: 1,
				},
			},
			{
				Model: "served-model",
				Role:  llm.Assistant,
				Content: []llm.Content{
					&llm.TextContent{Text: "done"},
				},
				Usage: llm.Usage{
					InputTokens:  5,
					OutputTokens: 2,
				},
			},
		},
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithDeterministicTestBuiltin(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}
	request := generationRequest("anthropic")
	request.Messages = request.Messages[:1]
	request.Claim = generationClaim()
	request.MaxIterations = 2
	response, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	events, checkpoints, starts := coordinator.snapshot()
	if !reflect.DeepEqual(events, []string{"checkpoint", "start", "result", "checkpoint"}) || starts != 1 {
		t.Fatalf("durable builtin events = %#v, starts = %d", events, starts)
	}
	if len(checkpoints) != 2 || len(checkpoints[0].ToolCalls) != 1 || len(checkpoints[1].ToolCalls) != 0 {
		t.Fatalf("model checkpoints = %#v", checkpoints)
	}
	if !response.MessagesCheckpointed || len(response.Messages) != 0 || response.BudgetExceeded != "" ||
		response.Usage.InputTokens != 8 || response.Usage.OutputTokens != 3 || response.Usage.Iterations != 2 {
		t.Fatalf("checkpointed response = %#v", response)
	}
}

func TestClientToolSuspendsAfterDurableModelCheckpoint(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	model := &sequenceLLM{
		responses: []*llm.Response{
			{
				Model: "served-model",
				Role:  llm.Assistant,
				Content: []llm.Content{
					&llm.ToolUseContent{
						ID:    "provider-client-call",
						Name:  "lookup_order",
						Input: json.RawMessage(`{"order_id":"order-1"}`),
					},
				},
				Usage: llm.Usage{
					InputTokens:  3,
					OutputTokens: 1,
				},
			},
		},
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithToolCoordinator(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}
	request := generationRequest("anthropic")
	request.Messages = request.Messages[:1]
	request.Claim = generationClaim()
	request.MaxIterations = 2
	request.ClientTools = []domain.ClientToolDefinition{
		{
			Name:        "lookup_order",
			Description: "Look up an order",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"],"additionalProperties":false}`),
		},
	}

	response, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	events, checkpoints, starts := coordinator.snapshot()
	if !reflect.DeepEqual(events, []string{"checkpoint"}) || starts != 0 || len(checkpoints) != 1 {
		t.Fatalf("client tool events = %#v, starts = %d, checkpoints = %#v", events, starts, checkpoints)
	}
	if len(checkpoints[0].ToolCalls) != 1 ||
		checkpoints[0].ToolCalls[0].Name != "lookup_order" ||
		checkpoints[0].ToolCalls[0].Mode != domain.ToolCallModeClient {
		t.Fatalf("client tool checkpoint = %#v", checkpoints[0])
	}
	if !response.ExternalToolsPending || !response.MessagesCheckpointed || response.Usage.Iterations != 1 {
		t.Fatalf("client tool response = %#v", response)
	}
}

func TestDeterministicBuiltinStopsAtCheckpointBudgetBeforeToolRuns(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	model := &sequenceLLM{
		responses: []*llm.Response{
			{
				Model: "served-model",
				Role:  llm.Assistant,
				Content: []llm.Content{
					&llm.ToolUseContent{
						ID:    "provider-call-1",
						Name:  deterministicEchoToolName,
						Input: json.RawMessage(`{"value":"hello"}`),
					},
				},
				Usage: llm.Usage{
					InputTokens:  3,
					OutputTokens: 1,
				},
			},
		},
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithDeterministicTestBuiltin(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}
	request := generationRequest("anthropic")
	request.Messages = request.Messages[:1]
	request.Claim = generationClaim()
	request.MaxIterations = 1
	response, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	events, checkpoints, starts := coordinator.snapshot()
	if !reflect.DeepEqual(events, []string{"checkpoint"}) || len(checkpoints) != 1 || starts != 0 {
		t.Fatalf("budget events = %#v, checkpoints = %d, starts = %d", events, len(checkpoints), starts)
	}
	if response.BudgetExceeded != "iterations" || !response.MessagesCheckpointed {
		t.Fatalf("budget response = %#v", response)
	}
}

func TestStructuredOutputCheckpointsAcceptedValueBeforeFinalResponse(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	model := &sequenceLLM{
		responses: []*llm.Response{
			toolUseResponse("provider-output-1", `{"answer":"yes"}`),
			textResponse("done"),
		},
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithToolCoordinator(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}
	request := structuredGenerationRequest("anthropic")

	response, err := generator.Generate(context.Background(), request)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	events, checkpoints, starts := coordinator.snapshot()
	if !reflect.DeepEqual(events, []string{"checkpoint", "start", "result", "checkpoint"}) || starts != 1 {
		t.Fatalf("structured output events = %#v, starts = %d", events, starts)
	}
	if len(checkpoints) != 2 || len(checkpoints[0].ToolCalls) != 1 || len(checkpoints[1].ToolCalls) != 0 {
		t.Fatalf("model checkpoints = %#v", checkpoints)
	}
	if response.StructuredOutput == nil || string(response.StructuredOutput.Value) != `{"answer":"yes"}` {
		t.Fatalf("structured output = %#v", response.StructuredOutput)
	}
	if response.StructuredOutput.Provenance.Source != "tool_call" ||
		response.StructuredOutput.Provenance.ToolCallID != "tcal_019f84a5-7838-7b57-a180-000000000001" ||
		response.StructuredOutput.Provenance.SchemaSHA256 != fmt.Sprintf("%x", request.StructuredOutput.SchemaDigest) {
		t.Fatalf("structured output provenance = %#v", response.StructuredOutput.Provenance)
	}
	if !response.MessagesCheckpointed || response.StructuredOutputFailure != "" || response.Usage.Iterations != 2 {
		t.Fatalf("structured generation response = %#v", response)
	}
}

func TestStructuredOutputRejectsThenCorrectsDurably(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	model := &sequenceLLM{
		responses: []*llm.Response{
			toolUseResponse("provider-output-1", `{}`),
			toolUseResponse("provider-output-2", `{"answer":"corrected"}`),
			textResponse("done"),
		},
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithToolCoordinator(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}

	response, err := generator.Generate(context.Background(), structuredGenerationRequest("anthropic"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	wantEvents := []string{
		"checkpoint",
		"start",
		"result",
		"checkpoint",
		"start",
		"result",
		"checkpoint",
	}
	events, checkpoints, starts := coordinator.snapshot()
	if !reflect.DeepEqual(events, wantEvents) || starts != 2 || len(checkpoints) != 3 {
		t.Fatalf("correction events = %#v, starts = %d, checkpoints = %d", events, starts, len(checkpoints))
	}
	if !reflect.DeepEqual(coordinator.errorSnapshot(), []bool{true, false}) {
		t.Fatalf("result errors = %#v", coordinator.errorSnapshot())
	}
	results := coordinator.resultSnapshot()
	if len(results) != 2 || !strings.Contains(string(results[0]), "answer") || len(results[0]) > 700 {
		t.Fatalf("bounded correction result = %s", results[0])
	}
	if response.StructuredOutput == nil || string(response.StructuredOutput.Value) != `{"answer":"corrected"}` ||
		response.StructuredOutput.Provenance.ToolCallID != "tcal_019f84a5-7838-7b57-a180-000000000002" {
		t.Fatalf("corrected output = %#v", response.StructuredOutput)
	}
}

func TestStructuredOutputDoesNotAcceptFinalText(t *testing.T) {
	for _, text := range []string{
		`{"answer":"text only"}`,
		"```json\n{\"answer\":\"fenced\"}\n```",
	} {
		t.Run(text, func(t *testing.T) {
			coordinator := &recordingToolCoordinator{}
			model := &sequenceLLM{
				responses: []*llm.Response{
					textResponse(text),
				},
			}
			generator := New(
				Config{
					AnthropicAPIKey: "secret",
				},
				WithToolCoordinator(coordinator),
			)
			generator.factory = func(string, string, string) (llm.LLM, error) {
				return model, nil
			}

			response, err := generator.Generate(context.Background(), structuredGenerationRequest("anthropic"))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if response.StructuredOutput != nil || response.StructuredOutputFailure != "missing" || !response.MessagesCheckpointed {
				t.Fatalf("final-text response = %#v", response)
			}
		})
	}
}

func TestStructuredOutputClassifiesOversizedSubmission(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	oversized := `{"answer":"` + strings.Repeat("x", structuredoutput.MaxValueBytes) + `"}`
	model := &sequenceLLM{
		responses: []*llm.Response{
			toolUseResponse("provider-output-1", oversized),
			textResponse("unable to correct"),
		},
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithToolCoordinator(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}

	response, err := generator.Generate(context.Background(), structuredGenerationRequest("anthropic"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if response.StructuredOutput != nil || response.StructuredOutputFailure != "oversized" {
		t.Fatalf("oversized response = %#v", response)
	}
	if !reflect.DeepEqual(coordinator.errorSnapshot(), []bool{true}) {
		t.Fatalf("result errors = %#v", coordinator.errorSnapshot())
	}
}

func TestStructuredOutputFirstAcceptedValueWins(t *testing.T) {
	coordinator := &recordingToolCoordinator{}
	model := &sequenceLLM{
		responses: []*llm.Response{
			toolUseResponse("provider-output-1", `{"answer":"first"}`),
			toolUseResponse("provider-output-2", `{"answer":"second"}`),
			textResponse("done"),
		},
	}
	generator := New(
		Config{
			AnthropicAPIKey: "secret",
		},
		WithToolCoordinator(coordinator),
	)
	generator.factory = func(string, string, string) (llm.LLM, error) {
		return model, nil
	}

	response, err := generator.Generate(context.Background(), structuredGenerationRequest("anthropic"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if response.StructuredOutput == nil || string(response.StructuredOutput.Value) != `{"answer":"first"}` ||
		response.StructuredOutput.Provenance.ToolCallID != "tcal_019f84a5-7838-7b57-a180-000000000001" {
		t.Fatalf("first accepted output = %#v", response.StructuredOutput)
	}
	if !reflect.DeepEqual(coordinator.errorSnapshot(), []bool{false, false}) {
		t.Fatalf("result errors = %#v", coordinator.errorSnapshot())
	}
}

func TestStructuredOutputSchemaProjectsForSupportedProviders(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai"} {
		t.Run(provider, func(t *testing.T) {
			model := &fakeLLM{
				result: textResponse("done"),
			}
			generator := New(
				Config{
					AnthropicAPIKey: "anthropic-secret",
					OpenAIAPIKey:    "openai-secret",
				},
				WithToolCoordinator(&recordingToolCoordinator{}),
			)
			generator.factory = func(string, string, string) (llm.LLM, error) {
				return model, nil
			}

			response, err := generator.Generate(context.Background(), structuredGenerationRequest(provider))
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if response.StructuredOutputFailure != "missing" {
				t.Fatalf("response = %#v", response)
			}
			if len(model.config.Tools) != 1 || model.config.Tools[0].Name() != "nvoken_submit_output" {
				t.Fatalf("tools = %#v", model.config.Tools)
			}
			schemaPayload, err := json.Marshal(model.config.Tools[0].Schema())
			if err != nil {
				t.Fatalf("marshal projected schema: %v", err)
			}
			if !jsonEqualForTest(schemaPayload, structuredOutputSchema()) {
				t.Fatalf("projected schema = %s", schemaPayload)
			}
		})
	}
}

func TestCheckpointBudgetUsesInjectedObservationTime(t *testing.T) {
	deadline := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	request := generationRequest("anthropic")
	request.Claim = &domain.InvocationClaim{
		Invocation: domain.Invocation{
			WallClockDeadlineAt: deadline,
		},
	}
	if got := checkpointBudgetExceeded(request, domain.ModelUsage{}, false, deadline.Add(-time.Nanosecond)); got != "" {
		t.Fatalf("budget before deadline = %q", got)
	}
	if got := checkpointBudgetExceeded(request, domain.ModelUsage{}, false, deadline); got != "wall_clock" {
		t.Fatalf("budget at deadline = %q, want wall_clock", got)
	}
}

func generationClaim() *domain.InvocationClaim {
	now := time.Now().UTC()
	owner := "test-owner"
	leaseExpiresAt := now.Add(time.Minute)
	executionDeadlineAt := now.Add(time.Minute)
	return &domain.InvocationClaim{
		Invocation: domain.Invocation{
			ID:                  "invk_019f84a5-7838-7b57-a180-5f74a0b65be2",
			Status:              domain.InvocationRunning,
			LeaseOwner:          &owner,
			LeaseExpiresAt:      &leaseExpiresAt,
			LeaseAttempt:        1,
			WallClockDeadlineAt: now.Add(time.Hour),
			ExecutionDeadlineAt: &executionDeadlineAt,
		},
		Owner:          owner,
		Attempt:        1,
		LeaseExpiresAt: leaseExpiresAt,
	}
}

func TestGeneratorStreamsNormalizedDeltasAndReturnsCompleteEvidence(t *testing.T) {
	index := 0
	model := &fakeStreamingLLM{events: []*llm.Event{
		{Type: llm.EventTypeMessageStart, Message: &llm.Response{
			ID: "message-1", Model: "served-stream-model", Role: llm.Assistant, Type: "message",
			Usage: llm.Usage{InputTokens: 4},
		}},
		{Type: llm.EventTypeContentBlockStart, Index: &index, ContentBlock: &llm.EventContentBlock{Type: llm.ContentTypeText}},
		{Type: llm.EventTypeContentBlockDelta, Index: &index, Delta: &llm.EventDelta{Type: llm.EventDeltaTypeText, Text: "hello"}},
		{Type: llm.EventTypeMessageDelta, Delta: &llm.EventDelta{StopReason: "end_turn"}, Usage: &llm.Usage{OutputTokens: 2}},
		{Type: llm.EventTypeMessageStop},
	}}
	generator := New(Config{AnthropicAPIKey: "secret"})
	generator.factory = func(string, string, string) (llm.LLM, error) { return model, nil }
	var deltas []domain.GenerationDelta
	response, err := generator.GenerateStream(context.Background(), generationRequest("anthropic"), func(delta domain.GenerationDelta) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if len(deltas) != 1 || deltas[0].Type != "text" || deltas[0].Text != "hello" || deltas[0].ContentIndex != 0 {
		t.Fatalf("deltas = %#v", deltas)
	}
	if response.ServedModel != "served-stream-model" || response.Usage.InputTokens != 4 ||
		response.Usage.OutputTokens != 2 || response.Usage.Iterations != 1 {
		t.Fatalf("stream evidence = %#v", response)
	}
	if len(response.Messages) != 1 || string(response.Messages[0].Content) != `[{"type":"text","text":"hello"}]` {
		t.Fatalf("stream messages = %#v", response.Messages)
	}
}

func TestGeneratorRejectsUnsupportedOrUnconfiguredProviderBeforeFactory(t *testing.T) {
	for _, test := range []struct {
		name    string
		request domain.GenerationRequest
		want    error
	}{
		{"unsupported", generationRequest("other"), ports.ErrProviderUnsupported},
		{"missing Anthropic key", generationRequest("anthropic"), ports.ErrProviderKeyMissing},
		{"missing OpenAI key", generationRequest("openai"), ports.ErrProviderKeyMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			generator := New(Config{})
			called := false
			generator.factory = func(string, string, string) (llm.LLM, error) {
				called = true
				return nil, nil
			}
			_, err := generator.Generate(context.Background(), test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Generate error = %v, want %v", err, test.want)
			}
			if called {
				t.Fatal("model factory was called")
			}
		})
	}
}

func TestGeneratorRejectsInvalidOutputAndProviderErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		result *llm.Response
		err    error
		want   error
	}{
		{"empty output", &llm.Response{Role: llm.Assistant}, nil, ports.ErrModelResponseInvalid},
		{"provider error", nil, errors.New("secret provider body"), ports.ErrGenerationFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &fakeLLM{result: test.result, err: test.err}
			generator := New(Config{AnthropicAPIKey: "secret"})
			generator.factory = func(string, string, string) (llm.LLM, error) { return model, nil }
			_, err := generator.Generate(context.Background(), generationRequest("anthropic"))
			if !errors.Is(err, test.want) {
				t.Fatalf("Generate error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClassifiedProviderCallErrorPreservesOnlyBoundedClass(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want ports.ProviderFailureClass
	}{
		{name: "unsupported", err: ports.ErrProviderUnsupported, want: ports.ProviderFailureConfiguration},
		{name: "throttled", err: providerStatusError{status: 429}, want: ports.ProviderFailureThrottled},
		{name: "rejected", err: providerStatusError{status: 400}, want: ports.ProviderFailureUpstreamRejected},
		{name: "outage", err: providerStatusError{status: 503}, want: ports.ProviderFailureUpstreamUnavailable},
		{name: "canceled", err: context.Canceled, want: ports.ProviderFailureCanceled},
		{name: "timeout", err: context.DeadlineExceeded, want: ports.ProviderFailureTimeoutOrTransport},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := classifiedProviderCallError(test.err)
			var classified *ports.ProviderCallError
			if !errors.As(err, &classified) || classified.Class != test.want || !errors.Is(err, ports.ErrGenerationFailed) {
				t.Fatalf("classified error = %#v, %v", classified, err)
			}
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "503") {
				t.Fatalf("provider detail leaked through bounded error: %v", err)
			}
		})
	}
}

type providerStatusError struct{ status int }

func (e providerStatusError) Error() string   { return "sensitive provider body" }
func (e providerStatusError) StatusCode() int { return e.status }

func TestGeneratorClassifiesDurableInputConversionSeparately(t *testing.T) {
	model := &fakeLLM{result: successfulDiveResponse()}
	generator := New(Config{AnthropicAPIKey: "secret"})
	generator.factory = func(string, string, string) (llm.LLM, error) { return model, nil }
	request := generationRequest("anthropic")
	request.Messages[0].Content = []byte(`[{"type":"dynamic","payload":"not-supported"}]`)

	_, err := generator.Generate(context.Background(), request)
	if !errors.Is(err, ports.ErrGenerationInputInvalid) || errors.Is(err, ports.ErrModelResponseInvalid) {
		t.Fatalf("Generate error = %v, want durable input classification", err)
	}
}

func generationRequest(provider string) domain.GenerationRequest {
	maxTokens := 321
	return domain.GenerationRequest{
		Instructions: "durable instructions", Provider: provider, Model: "requested-model",
		MaxOutputTokens: &maxTokens, MaxIterations: 2,
		Messages: []domain.GenerationMessage{
			{Role: domain.MessageRoleUser, Content: []byte(`[{"type":"text","text":"question"}]`)},
			{Role: domain.MessageRoleAssistant, Content: []byte(`[{"type":"text","text":"prior answer"}]`)},
		},
	}
}

func structuredGenerationRequest(provider string) domain.GenerationRequest {
	request := generationRequest(provider)
	request.Messages = request.Messages[:1]
	request.Claim = generationClaim()
	request.MaxIterations = 3
	schema := structuredOutputSchema()
	digest := sha256.Sum256(schema)
	request.StructuredOutput = &domain.StructuredOutputRequest{
		Schema:       schema,
		SchemaDigest: digest[:],
	}
	return request
}

func structuredOutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
}

func toolUseResponse(providerCallID, input string) *llm.Response {
	return &llm.Response{
		Model: "served-model",
		Role:  llm.Assistant,
		Content: []llm.Content{
			&llm.ToolUseContent{
				ID:    providerCallID,
				Name:  "nvoken_submit_output",
				Input: json.RawMessage(input),
			},
		},
		Usage: llm.Usage{
			InputTokens:  3,
			OutputTokens: 1,
		},
	}
}

func textResponse(text string) *llm.Response {
	return &llm.Response{
		Model: "served-model",
		Role:  llm.Assistant,
		Content: []llm.Content{
			&llm.TextContent{
				Text: text,
			},
		},
		Usage: llm.Usage{
			InputTokens:  3,
			OutputTokens: 1,
		},
	}
}

func jsonEqualForTest(left, right []byte) bool {
	var leftValue any
	var rightValue any
	return json.Unmarshal(left, &leftValue) == nil &&
		json.Unmarshal(right, &rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}

func successfulDiveResponse() *llm.Response {
	return &llm.Response{
		Model: "served-model", Role: llm.Assistant,
		Content: []llm.Content{
			&llm.TextContent{Text: "new answer"},
			&llm.TextContent{Text: "second block"},
		},
		Usage: llm.Usage{
			InputTokens: 11, OutputTokens: 7, CacheCreationInputTokens: 3,
			CacheReadInputTokens: 2, ReasoningTokens: 1,
			Cost: &llm.Cost{Input: .1, Output: .2, CacheRead: .01, CacheWrite: .02, Total: .33, Currency: "USD", Model: "served-model"},
		},
	}
}
