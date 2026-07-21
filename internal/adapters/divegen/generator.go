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
	return g.generate(ctx, request, nil)
}

func (g *Generator) GenerateStream(
	ctx context.Context,
	request domain.GenerationRequest,
	emit ports.GenerationDeltaEmitter,
) (domain.GenerationResponse, error) {
	return g.generate(ctx, request, emit)
}

func (g *Generator) generate(
	ctx context.Context,
	request domain.GenerationRequest,
	emit ports.GenerationDeltaEmitter,
) (domain.GenerationResponse, error) {
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
	evidence := &modelEvidence{}
	var agentModel llm.LLM = &evidenceModel{LLM: model, evidence: evidence}
	if streaming, ok := model.(llm.StreamingLLM); ok && emit != nil {
		agentModel = &streamingEvidenceModel{StreamingLLM: streaming, evidence: evidence}
	}
	agent, err := dive.NewAgent(dive.AgentOptions{
		SystemPrompt:       request.Instructions,
		Model:              agentModel,
		Tools:              nil,
		ModelSettings:      &dive.ModelSettings{MaxTokens: request.MaxOutputTokens},
		ToolIterationLimit: request.MaxIterations,
	})
	if err != nil {
		return domain.GenerationResponse{}, fmt.Errorf("configure Dive agent: %w", err)
	}
	options := []dive.CreateResponseOption{dive.WithMessages(messages...)}
	if emit != nil {
		options = append(options, dive.WithEventCallback(func(_ context.Context, item *dive.ResponseItem) error {
			if delta, ok := normalizedDelta(item); ok {
				emit(delta)
			}
			return nil
		}))
	}
	response, err := agent.CreateResponse(ctx, options...)
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

// evidenceModel captures blocking-provider evidence without exposing the raw
// response outside this adapter.
type evidenceModel struct {
	llm.LLM
	evidence *modelEvidence
}

type modelEvidence struct {
	mu     sync.Mutex
	served string
	calls  int
}

func (m *evidenceModel) Generate(ctx context.Context, options ...llm.Option) (*llm.Response, error) {
	m.evidence.recordCall()
	response, err := m.LLM.Generate(ctx, options...)
	if response != nil && strings.TrimSpace(response.Model) != "" {
		m.evidence.recordModel(response.Model)
	}
	return response, err
}

type streamingEvidenceModel struct {
	llm.StreamingLLM
	evidence *modelEvidence
}

func (m *streamingEvidenceModel) Stream(ctx context.Context, options ...llm.Option) (llm.StreamIterator, error) {
	m.evidence.recordCall()
	iterator, err := m.StreamingLLM.Stream(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &evidenceIterator{StreamIterator: iterator, evidence: m.evidence}, nil
}

type evidenceIterator struct {
	llm.StreamIterator
	evidence *modelEvidence
}

func (i *evidenceIterator) Next() bool {
	if !i.StreamIterator.Next() {
		return false
	}
	event := i.StreamIterator.Event()
	if event != nil && event.Message != nil {
		i.evidence.recordModel(event.Message.Model)
	}
	return true
}

func (m *modelEvidence) recordCall() {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
}

func (m *modelEvidence) recordModel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	m.mu.Lock()
	m.served = model
	m.mu.Unlock()
}

func (m *modelEvidence) iterations() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *modelEvidence) servedModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.served
}

func normalizedDelta(item *dive.ResponseItem) (domain.GenerationDelta, bool) {
	if item == nil || item.Type != dive.ResponseItemTypeModelEvent || item.Event == nil {
		return domain.GenerationDelta{}, false
	}
	event := item.Event
	index := 0
	if event.Index != nil {
		index = *event.Index
	}
	switch event.Type {
	case llm.EventTypeContentBlockStart:
		if event.ContentBlock == nil {
			return domain.GenerationDelta{}, false
		}
		switch event.ContentBlock.Type {
		case llm.ContentTypeText:
			if event.ContentBlock.Text != "" {
				return domain.GenerationDelta{ContentIndex: index, Type: "text", Text: event.ContentBlock.Text}, true
			}
		case llm.ContentTypeThinking:
			if event.ContentBlock.Thinking != "" {
				return domain.GenerationDelta{ContentIndex: index, Type: "thinking", Thinking: event.ContentBlock.Thinking}, true
			}
		}
	case llm.EventTypeContentBlockDelta:
		if event.Delta == nil {
			return domain.GenerationDelta{}, false
		}
		switch event.Delta.Type {
		case llm.EventDeltaTypeText:
			if event.Delta.Text != "" {
				return domain.GenerationDelta{ContentIndex: index, Type: "text", Text: event.Delta.Text}, true
			}
		case llm.EventDeltaTypeThinking:
			if event.Delta.Thinking != "" {
				return domain.GenerationDelta{ContentIndex: index, Type: "thinking", Thinking: event.Delta.Thinking}, true
			}
		}
	}
	return domain.GenerationDelta{}, false
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
