// Package divegen adapts nvoken's durable, provider-neutral generation request
// to one explicit Dive provider call.
package divegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/deepnoodle-ai/dive"
	"github.com/deepnoodle-ai/dive/llm"
	"github.com/deepnoodle-ai/dive/providers/anthropic"
	"github.com/deepnoodle-ai/dive/providers/openai"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type Config struct {
	AnthropicAPIKey string
	OpenAIAPIKey    string
}

type modelFactory func(provider, model, apiKey string) (llm.LLM, error)

type Generator struct {
	config  Config
	factory modelFactory
}

func New(config Config) *Generator {
	return &Generator{config: config, factory: newModel}
}

func newModel(provider, model, apiKey string) (llm.LLM, error) {
	switch provider {
	case "anthropic":
		return anthropic.New(anthropic.WithModel(model), anthropic.WithAPIKey(apiKey)), nil
	case "openai":
		return openai.New(openai.WithModel(model), openai.WithAPIKey(apiKey)), nil
	default:
		return nil, ports.ErrProviderUnsupported
	}
}

func (g *Generator) Generate(ctx context.Context, request domain.GenerationRequest) (domain.GenerationResponse, error) {
	if g == nil || g.factory == nil {
		return domain.GenerationResponse{}, fmt.Errorf("dive generator is not configured")
	}
	provider := strings.ToLower(strings.TrimSpace(request.Provider))
	var apiKey string
	switch provider {
	case "anthropic":
		apiKey = g.config.AnthropicAPIKey
	case "openai":
		apiKey = g.config.OpenAIAPIKey
	default:
		return domain.GenerationResponse{}, ports.ErrProviderUnsupported
	}
	if apiKey == "" {
		return domain.GenerationResponse{}, ports.ErrProviderKeyMissing
	}

	model, err := g.factory(provider, request.Model, apiKey)
	if err != nil {
		return domain.GenerationResponse{}, err
	}
	messages := make([]*llm.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		converted, err := toDiveMessage(message)
		if err != nil {
			return domain.GenerationResponse{}, fmt.Errorf("%w: convert durable message", ports.ErrGenerationInputInvalid)
		}
		messages = append(messages, converted)
	}
	evidence := &evidenceModel{LLM: model}
	agent, err := dive.NewAgent(dive.AgentOptions{
		SystemPrompt:       request.Instructions,
		Model:              evidence,
		Tools:              nil,
		ModelSettings:      &dive.ModelSettings{MaxTokens: request.MaxOutputTokens},
		ToolIterationLimit: request.MaxIterations,
	})
	if err != nil {
		return domain.GenerationResponse{}, fmt.Errorf("configure Dive agent: %w", err)
	}
	response, err := agent.CreateResponse(ctx, dive.WithMessages(messages...))
	if err != nil {
		if ctx.Err() != nil {
			return domain.GenerationResponse{}, ctx.Err()
		}
		return domain.GenerationResponse{}, fmt.Errorf("%w: Dive provider call", ports.ErrGenerationFailed)
	}
	if response == nil || response.Status == dive.ResponseStatusSuspended || response.Suspension != nil {
		return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
	}
	if len(response.OutputMessages) == 0 || response.Usage == nil {
		return domain.GenerationResponse{}, ports.ErrModelResponseInvalid
	}
	output := make([]domain.GenerationMessage, 0, len(response.OutputMessages))
	for _, message := range response.OutputMessages {
		converted, err := fromDiveMessage(message)
		if err != nil {
			return domain.GenerationResponse{}, fmt.Errorf("%w: normalize output message", ports.ErrModelResponseInvalid)
		}
		output = append(output, converted)
	}
	servedModel := evidence.servedModel()
	if servedModel == "" {
		servedModel = request.Model
	}
	usage := normalizeUsage(response.Usage)
	usage.Iterations = evidence.iterations()
	return domain.GenerationResponse{Messages: output, Usage: usage, ServedModel: servedModel}, nil
}

// evidenceModel captures the provider's response model without exposing the
// raw response outside this adapter. It intentionally implements only LLM so
// this slice remains one non-streaming generation call.
type evidenceModel struct {
	llm.LLM
	mu     sync.Mutex
	served string
	calls  int
}

func (m *evidenceModel) Generate(ctx context.Context, options ...llm.Option) (*llm.Response, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	response, err := m.LLM.Generate(ctx, options...)
	if response != nil && strings.TrimSpace(response.Model) != "" {
		m.mu.Lock()
		m.served = response.Model
		m.mu.Unlock()
	}
	return response, err
}

func (m *evidenceModel) iterations() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *evidenceModel) servedModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.served
}

func toDiveMessage(message domain.GenerationMessage) (*llm.Message, error) {
	role, err := toDiveRole(message.Role)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		Role    llm.Role        `json:"role"`
		Content json.RawMessage `json:"content"`
	}{Role: role, Content: message.Content})
	if err != nil {
		return nil, err
	}
	var converted llm.Message
	if err := json.Unmarshal(payload, &converted); err != nil {
		return nil, err
	}
	return &converted, nil
}

func fromDiveMessage(message *llm.Message) (domain.GenerationMessage, error) {
	if message == nil || message.Role != llm.Assistant || len(message.Content) == 0 {
		return domain.GenerationMessage{}, errors.New("dive output is not a nonempty assistant message")
	}
	content, err := json.Marshal(message.Content)
	if err != nil {
		return domain.GenerationMessage{}, err
	}
	return domain.GenerationMessage{Role: domain.MessageRoleAssistant, Content: content}, nil
}

func toDiveRole(role domain.MessageRole) (llm.Role, error) {
	switch role {
	case domain.MessageRoleUser:
		return llm.User, nil
	case domain.MessageRoleAssistant:
		return llm.Assistant, nil
	default:
		return "", fmt.Errorf("unsupported message role %q", role)
	}
}

func normalizeUsage(usage *llm.Usage) domain.ModelUsage {
	normalized := domain.ModelUsage{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		ReasoningTokens:          usage.ReasoningTokens,
	}
	if usage.Cost != nil {
		normalized.EstimatedCost = &domain.ModelCost{
			Input: usage.Cost.Input, Output: usage.Cost.Output,
			CacheRead: usage.Cost.CacheRead, CacheWrite: usage.Cost.CacheWrite,
			Total: usage.Cost.Total, Currency: usage.Cost.Currency, Model: usage.Cost.Model,
		}
	}
	return normalized
}
