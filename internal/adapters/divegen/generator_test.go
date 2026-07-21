package divegen

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deepnoodle-ai/dive/llm"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type sequenceLLM struct {
	mu        sync.Mutex
	responses []*llm.Response
	events    *[]string
	calls     int
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

type recordingToolCoordinator struct {
	mu          sync.Mutex
	events      []string
	checkpoints []domain.ModelCheckpointInput
	starts      int
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
	return domain.ToolCallExecution{
		Call: domain.ToolCall{
			ID:             "tcal_019f84a5-7838-7b57-a180-5f74a0b65be0",
			ProviderCallID: providerCallID,
			Iteration:      iteration,
		},
		Attempt: domain.ToolCallAttempt{
			ID:      "tcat_019f84a5-7838-7b57-a180-5f74a0b65be1",
			Attempt: 1,
		},
	}, nil
}

func (c *recordingToolCoordinator) AcceptBuiltinToolResult(_ context.Context, _ domain.InvocationClaim, execution domain.ToolCallExecution, content json.RawMessage, isError bool) (domain.ToolCall, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if execution.Call.ID == "" || len(content) == 0 || isError {
		return domain.ToolCall{}, errors.New("invalid deterministic result")
	}
	c.events = append(c.events, "result")
	return execution.Call, nil
}

func (c *recordingToolCoordinator) snapshot() ([]string, []domain.ModelCheckpointInput, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...), append([]domain.ModelCheckpointInput(nil), c.checkpoints...), c.starts
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
