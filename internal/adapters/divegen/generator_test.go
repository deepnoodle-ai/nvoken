package divegen

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/deepnoodle-ai/dive/llm"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type fakeLLM struct {
	config llm.Config
	result *llm.Response
	err    error
}

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
